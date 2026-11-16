package main

import (
	"context"
	"fmt"
	"sync"
	"time"

	"mddb/internal/cache"
	"mddb/internal/storage"
	proto "mddb/proto"

	bolt "go.etcd.io/bbolt"
)

// FinalBatchProcessor - FINAL optimized batch processor
// Key optimization: SINGLE READ transaction for ALL documents
type FinalBatchProcessor struct {
	server     *Server
	maxWorkers int
}

// NewFinalBatchProcessor creates final optimized batch processor
func NewFinalBatchProcessor(server *Server, maxWorkers int) *FinalBatchProcessor {
	if maxWorkers <= 0 {
		maxWorkers = 8
	}

	return &FinalBatchProcessor{
		server:     server,
		maxWorkers: maxWorkers,
	}
}

// ProcessBatch processes batch with SINGLE READ transaction
func (fbp *FinalBatchProcessor) ProcessBatch(ctx context.Context, collection string, batchDocs []*proto.BatchDocument) (*proto.AddBatchResponse, error) {
	if len(batchDocs) == 0 {
		return &proto.AddBatchResponse{}, nil
	}

	now := time.Now().Unix()

	// CRITICAL: Read ALL existing docs in SINGLE transaction
	existingMap := fbp.batchReadAll(collection, batchDocs)

	// Phase 2: Parallel marshal (no DB access)
	processed := fbp.parallelMarshal(ctx, collection, batchDocs, existingMap, now)

	// Phase 3: Single write transaction
	resp, committed, err := fbp.commitBatch(collection, processed, now)
	if err != nil {
		return nil, err
	}

	// Phase 4: post-commit side-effect pipeline (GO-001) — FTS, geo, webhooks,
	// SSE, temporal, embedding. The extreme path skipped these entirely, so
	// batch-ingested docs were invisible to search. Reuses firePostBatchHooks
	// (DRY with the HTTP and standard-batch paths).
	fbp.server.firePostBatchHooks(collection, committed, postBatchOptions{})

	return resp, nil
}

// batchReadAll reads ALL documents in SINGLE transaction
func (fbp *FinalBatchProcessor) batchReadAll(collection string, batchDocs []*proto.BatchDocument) map[string][]byte {
	existingMap := make(map[string][]byte, len(batchDocs))

	// SINGLE READ TRANSACTION for ALL documents
	_ = fbp.server.DBViewC(collection, func(tx *bolt.Tx) error {
		bDocs := tx.Bucket(fbp.server.BucketNames.Docs)

		// Pre-allocate buffer for key building
		keyBuf := make([]byte, 0, 256)

		for _, batchDoc := range batchDocs {
			if batchDoc.Key == "" || batchDoc.Lang == "" {
				continue
			}

			docID := genID(collection, batchDoc.Key, batchDoc.Lang)

			// Build key efficiently
			keyBuf = keyBuf[:0]
			keyBuf = append(keyBuf, "doc|"...)
			keyBuf = append(keyBuf, collection...)
			keyBuf = append(keyBuf, '|')
			keyBuf = append(keyBuf, docID...)

			if v := bDocs.Get(keyBuf); v != nil {
				// Store raw bytes - avoid unmarshal until needed
				existingMap[docID] = v
			}
		}

		return nil
	})

	return existingMap
}

// parallelMarshal marshals documents in parallel (no DB access)
func (fbp *FinalBatchProcessor) parallelMarshal(ctx context.Context, collection string, batchDocs []*proto.BatchDocument, existingMap map[string][]byte, now int64) []*ProcessedDoc {
	processed := make([]*ProcessedDoc, len(batchDocs))

	numWorkers := fbp.maxWorkers
	if len(batchDocs) < numWorkers {
		numWorkers = len(batchDocs)
	}

	jobs := make(chan int, len(batchDocs))
	var wg sync.WaitGroup

	for w := 0; w < numWorkers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()

			for idx := range jobs {
				// A cancelled request still gets an answer for every
				// document, as in BatchProcessor.parallelProcess: returning
				// here left nil slots that commitBatch dereferenced.
				if err := ctx.Err(); err != nil {
					processed[idx] = &ProcessedDoc{Doc: storage.Doc{Key: batchDocs[idx].Key}, Error: err}
					continue
				}
				processed[idx] = fbp.processDocumentFast(collection, batchDocs[idx], existingMap, now)
			}
		}()
	}

	for i := range batchDocs {
		jobs <- i
	}
	close(jobs)

	wg.Wait()

	return processed
}

// processDocumentFast processes document without DB access
func (fbp *FinalBatchProcessor) processDocumentFast(collection string, batchDoc *proto.BatchDocument, existingMap map[string][]byte, now int64) *ProcessedDoc {
	result := &ProcessedDoc{}

	if batchDoc.Key == "" || batchDoc.Lang == "" {
		result.Error = fmt.Errorf("missing key or lang")
		return result
	}

	// Convert meta directly
	meta := make(map[string][]string, len(batchDoc.Meta))
	for k, v := range batchDoc.Meta {
		meta[k] = v.Values
	}
	result.Meta = meta

	// GO-003: schema validation (opt-in) — the batch path previously skipped it.
	if fbp.server.SchemaManager != nil {
		if err := fbp.server.SchemaManager.Validate(collection, meta); err != nil {
			result.Error = err
			return result
		}
	}

	docID := genID(collection, batchDoc.Key, batchDoc.Lang)
	result.DocID = docID

	// Check existing from pre-loaded map
	var added int64
	if existingBytes, ok := existingMap[docID]; ok {
		result.IsUpdate = true
		// Only unmarshal if we need AddedAt
		if existingDoc, err := unmarshalDoc(existingBytes); err == nil {
			added = existingDoc.AddedAt
			result.Existing = *existingDoc
		}
	}

	if added == 0 {
		added = now
	}

	doc := storage.Doc{
		ID: docID, Key: batchDoc.Key, Lang: batchDoc.Lang, Meta: meta,
		ContentMD: batchDoc.ContentMd, AddedAt: added, UpdatedAt: now,
	}

	buf, err := marshalAndEncrypt(&doc, collection)
	if err != nil {
		result.Error = err
		return result
	}

	result.Doc = doc
	result.Buf = buf
	result.SaveRevision = batchDoc.SaveRevision

	return result
}

// commitBatch commits the batch the way the standard path does — the same
// document, key-index, metadata-index and revision writes, recorded in the
// binlog inside the transaction (GO-051) — and then warms the cache with
// what committed. It used to write the same keys itself without any log
// entries: a batch on the extreme path never reached a follower, and on a
// node that did not lead it committed with nothing logged. Its error is a
// quota refusal of the whole batch (GO-043).
func (fbp *FinalBatchProcessor) commitBatch(collection string, processed []*ProcessedDoc, now int64) (*proto.AddBatchResponse, []*ProcessedDoc, error) {
	bp := &BatchProcessor{server: fbp.server, maxWorkers: fbp.maxWorkers}
	resp, committed, err := bp.commitBatch(collection, processed, now)
	for _, p := range committed {
		cacheKey := cache.BuildCacheKey(collection, p.Doc.Key, p.Doc.Lang)
		if fbp.server.UseExtreme && fbp.server.LockFreeCache != nil {
			fbp.server.LockFreeCache.Set(cacheKey, p.Buf)
		} else {
			fbp.server.Cache.Set(cacheKey, p.Buf)
		}
	}
	return resp, committed, err
}
