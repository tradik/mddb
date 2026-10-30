package main

import (
	"context"
	"fmt"
	"sync"
	"time"

	"mddb/internal/binlog"
	"mddb/internal/storage"
	proto "mddb/proto"

	bolt "go.etcd.io/bbolt"
)

// BatchProcessor handles batch document processing
type BatchProcessor struct {
	server     *Server
	maxWorkers int
}

// NewBatchProcessor creates a new batch processor
func NewBatchProcessor(server *Server, maxWorkers int) *BatchProcessor {
	if maxWorkers <= 0 {
		maxWorkers = 4 // Default to 4 workers
	}
	return &BatchProcessor{
		server:     server,
		maxWorkers: maxWorkers,
	}
}

// ProcessedDoc represents a processed document ready for storage
type ProcessedDoc struct {
	DocID        string
	Doc          storage.Doc
	Buf          []byte
	Meta         map[string][]string
	Existing     storage.Doc
	IsUpdate     bool
	SaveRevision bool
	Error        error
}

// ProcessBatch processes multiple documents in parallel, then commits in single transaction
func (bp *BatchProcessor) ProcessBatch(ctx context.Context, collection string, batchDocs []*proto.BatchDocument) (*proto.AddBatchResponse, error) {
	if len(batchDocs) == 0 {
		return &proto.AddBatchResponse{}, nil
	}

	now := time.Now().Unix()

	// Phase 1: Parallel processing (prepare documents)
	processed := bp.parallelProcess(ctx, collection, batchDocs, now)

	// Phase 2: Single transaction commit
	resp, committed, err := bp.commitBatch(collection, processed, now)
	if err != nil {
		return nil, err
	}

	// Phase 3: post-commit side-effect pipeline (GO-001) — FTS, geo, webhooks,
	// SSE, temporal, embedding. Previously batch gRPC writes skipped all of
	// these, so docs were invisible to search and fired no events. Reuses the
	// same firePostBatchHooks the HTTP batch path uses (DRY).
	bp.server.firePostBatchHooks(collection, committed, postBatchOptions{})

	return resp, nil
}

// parallelProcess processes documents in parallel
func (bp *BatchProcessor) parallelProcess(ctx context.Context, collection string, batchDocs []*proto.BatchDocument, now int64) []*ProcessedDoc {
	processed := make([]*ProcessedDoc, len(batchDocs))

	// Create worker pool
	numWorkers := bp.maxWorkers
	if len(batchDocs) < numWorkers {
		numWorkers = len(batchDocs)
	}

	jobs := make(chan int, len(batchDocs))
	var wg sync.WaitGroup

	// Start workers
	for w := 0; w < numWorkers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()

			for idx := range jobs {
				// A cancelled request still gets an answer for every
				// document. Returning here used to leave nil slots that
				// commitBatch dereferenced.
				if err := ctx.Err(); err != nil {
					processed[idx] = &ProcessedDoc{Doc: storage.Doc{Key: batchDocs[idx].Key}, Error: err}
					continue
				}
				processed[idx] = bp.processDocument(collection, batchDocs[idx], now)
			}
		}()
	}

	// Send jobs
	for i := range batchDocs {
		jobs <- i
	}
	close(jobs)

	// Wait for completion
	wg.Wait()

	return processed
}

// processDocument processes a single document (validation, conversion, marshaling)
func (bp *BatchProcessor) processDocument(collection string, batchDoc *proto.BatchDocument, now int64) *ProcessedDoc {
	// The key is set first so an error reported for this document names it.
	result := &ProcessedDoc{Doc: storage.Doc{Key: batchDoc.Key}}

	// Validate
	if batchDoc.Key == "" || batchDoc.Lang == "" {
		result.Error = fmt.Errorf("missing key or lang")
		return result
	}

	// Convert meta
	meta := make(map[string][]string)
	for k, v := range batchDoc.Meta {
		meta[k] = v.Values
	}
	result.Meta = meta

	// GO-003: schema validation (opt-in) — the batch path previously skipped it.
	if bp.server.SchemaManager != nil {
		if err := bp.server.SchemaManager.Validate(collection, meta); err != nil {
			result.Error = err
			return result
		}
	}

	// Generate ID
	docID := genID(collection, batchDoc.Key, batchDoc.Lang)
	result.DocID = docID

	// Load existing (in read transaction)
	existing := storage.Doc{}
	err := bp.server.DBViewC(collection, func(tx *bolt.Tx) error {
		bDocs := tx.Bucket(bp.server.BucketNames.Docs)
		if v := bDocs.Get(storage.DocKey(collection, docID)); v != nil {
			existingDoc, err := unmarshalDoc(v)
			if err != nil {
				return err
			}
			existing = *existingDoc
			result.IsUpdate = true
		}
		return nil
	})

	if err != nil {
		result.Error = err
		return result
	}

	result.Existing = existing

	// Prepare document
	added := existing.AddedAt
	if added == 0 {
		added = now
	}

	doc := storage.Doc{
		ID: docID, Key: batchDoc.Key, Lang: batchDoc.Lang, Meta: meta,
		ContentMD: batchDoc.ContentMd, AddedAt: added, UpdatedAt: now,
	}
	// CODE-004: same enrichment as the single-document path — one behaviour
	// across every write transport (the GO-001 lesson).
	EnrichCodeSymbolsWith(&doc, bp.server.codeIncludePaths(collection))

	// Marshal (+ optional at-rest encryption based on CollectionConfig.Encrypted)
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

// commitBatch commits all processed documents in a single transaction and
// returns the response plus the docs that were durably committed (so the
// caller can run the shared post-write pipeline on exactly those). The error
// is a quota refusal (GO-043), which refuses the whole batch; any other
// failure is reported per document in the response.
func (bp *BatchProcessor) commitBatch(collection string, processed []*ProcessedDoc, now int64) (*proto.AddBatchResponse, []*ProcessedDoc, error) {
	resp := &proto.AddBatchResponse{}
	committed := make([]*ProcessedDoc, 0, len(processed))

	// GO-021: payloads reach an external backend before the transaction that
	// indexes them opens.
	pushBatchPayloads(bp.server, collection, processed)

	var bo binlog.BinlogOps
	// Single transaction for all documents
	err := bp.server.DBUpdateC(collection, func(tx *bolt.Tx) error {
		if err := bp.server.checkWriteQuota(tx, collection, docQuotaWrites(collection, processed)); err != nil {
			return err
		}
		for _, p := range processed {
			if p.Error == nil {
				p.Error = bp.server.writeBatchDoc(tx, &bo, collection, p, now)
			}
			if p.Error != nil {
				resp.Failed++
				resp.Errors = append(resp.Errors, fmt.Sprintf("%s: %v", p.Doc.Key, p.Error))
				continue
			}
			if p.IsUpdate {
				resp.Updated++
			} else {
				resp.Added++
			}
			committed = append(committed, p)
		}
		return nil
	})

	if qe := wholeBatchRefusal(err); qe != nil { // GO-043, AGT-005
		return nil, nil, qe
	}
	if err != nil {
		resp.Failed = safeInt32(len(processed))
		resp.Errors = append(resp.Errors, fmt.Sprintf("transaction error: %v", err))
		// The transaction rolled back — nothing was actually committed.
		return resp, nil, nil
	}
	bo.FlushTo(bp.server.Binlog)

	return resp, committed, nil
}

// writeBatchDoc stores one prepared document inside the batch transaction: the
// document, its key index, its metadata index and — when asked — a revision
// trimmed to the collection's MaxRevisions. The steps are the single-document
// path's (addDocument), so a batch indexes and keeps history the same way.
//
// A failure is reported for this document only and the batch goes on, so
// writes made before the failing step stay in the transaction: a document
// whose revision could not be written is stored but counted as failed.
func (s *Server) writeBatchDoc(tx *bolt.Tx, bo *binlog.BinlogOps, collection string, p *ProcessedDoc, now int64) error {
	if err := putLogged(bo,
		boltWrite{tx.Bucket(s.BucketNames.Docs), "docs", storage.DocKey(collection, p.DocID), p.Buf},
		boltWrite{tx.Bucket(s.BucketNames.ByKey), "bykey", storage.ByKeyKey(collection, p.Doc.Key, p.Doc.Lang), []byte(p.DocID)}); err != nil {
		return fmt.Errorf("put error: %w", err)
	}
	if metadataChanged(p.Existing.Meta, p.Doc.Meta) {
		if err := reindexMeta(tx.Bucket(s.BucketNames.IdxMeta), bo, collection, p.Doc.ID, p.Existing.Meta, p.Doc.Meta); err != nil {
			return fmt.Errorf("index error: %w", err)
		}
	}
	if p.SaveRevision {
		if err := s.writeRevision(tx, bo, collection, p.Doc.ID, now, p.Buf); err != nil {
			return fmt.Errorf("revision error: %w", err)
		}
	}
	return nil
}
