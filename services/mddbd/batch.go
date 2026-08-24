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
	resp, committed := bp.commitBatch(collection, processed, now)

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
				select {
				case <-ctx.Done():
					return
				default:
					processed[idx] = bp.processDocument(collection, batchDocs[idx], now)
				}
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
	result := &ProcessedDoc{}

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
	err := bp.server.DBView(func(tx *bolt.Tx) error {
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
	EnrichCodeSymbols(&doc)

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
// caller can run the shared post-write pipeline on exactly those).
func (bp *BatchProcessor) commitBatch(collection string, processed []*ProcessedDoc, now int64) (*proto.AddBatchResponse, []*ProcessedDoc) {
	resp := &proto.AddBatchResponse{}
	committed := make([]*ProcessedDoc, 0, len(processed))

	// GO-021: payloads reach an external backend before the transaction that
	// indexes them opens.
	pushBatchPayloads(bp.server, collection, processed)

	var bo binlog.BinlogOps
	// Single transaction for all documents
	err := bp.server.DBUpdate(func(tx *bolt.Tx) error {
		bDocs := tx.Bucket(bp.server.BucketNames.Docs)
		bIdx := tx.Bucket(bp.server.BucketNames.IdxMeta)
		bRev := tx.Bucket(bp.server.BucketNames.Rev)
		bByK := tx.Bucket(bp.server.BucketNames.ByKey)

		for _, p := range processed {
			// Skip failed documents
			if p.Error != nil {
				resp.Failed++
				resp.Errors = append(resp.Errors, fmt.Sprintf("%s: %v", p.Doc.Key, p.Error))
				continue
			}

			// Store document
			docKey := storage.DocKey(collection, p.DocID)
			if err := bDocs.Put(docKey, p.Buf); err != nil {
				resp.Failed++
				resp.Errors = append(resp.Errors, fmt.Sprintf("%s: put error: %v", p.Doc.Key, err))
				continue
			}
			bo.Put("docs", docKey, p.Buf)

			// Store key index
			byKeyK := storage.ByKeyKey(collection, p.Doc.Key, p.Doc.Lang)
			if err := bByK.Put(byKeyK, []byte(p.DocID)); err != nil {
				resp.Failed++
				resp.Errors = append(resp.Errors, fmt.Sprintf("%s: bykey error: %v", p.Doc.Key, err))
				continue
			}
			bo.Put("bykey", byKeyK, []byte(p.DocID))

			// Only reindex metadata if changed
			if metadataChanged(p.Existing.Meta, p.Doc.Meta) {
				// Delete old indices
				if p.Existing.ID != "" && p.Existing.Meta != nil {
					for mk, vals := range p.Existing.Meta {
						for _, mv := range vals {
							prefix := append(storage.MetaKeyPrefix(collection, mk, mv), []byte(p.Existing.ID)...)
							_ = bIdx.Delete(prefix)
							bo.Delete("idxmeta", prefix)
						}
					}
				}

				// Add new indices
				for mk, vals := range p.Doc.Meta {
					for _, mv := range vals {
						key := append(storage.MetaKeyPrefix(collection, mk, mv), []byte(p.Doc.ID)...)
						if err := bIdx.Put(key, []byte("1")); err != nil {
							resp.Failed++
							resp.Errors = append(resp.Errors, fmt.Sprintf("%s: index error: %v", p.Doc.Key, err))
							continue
						}
						bo.Put("idxmeta", key, []byte("1"))
					}
				}
			}

			// Revision (optional - only if requested)
			if p.SaveRevision {
				rkey := append(storage.RevPrefix(collection, p.Doc.ID), []byte(fmt.Sprintf("%020d", now))...)
				if err := bRev.Put(rkey, p.Buf); err != nil {
					resp.Failed++
					resp.Errors = append(resp.Errors, fmt.Sprintf("%s: revision error: %v", p.Doc.Key, err))
					continue
				}
				bo.Put("rev", rkey, p.Buf)

				// Enforce per-collection MaxRevisions so batch writes trim
				// history just like the single-doc path (GO-001).
				if bp.server.CollectionManager != nil {
					if cfg, found := bp.server.CollectionManager.Get(collection); found && cfg.MaxRevisions > 0 {
						if err := trimRevisions(tx, &bo, collection, p.Doc.ID, cfg.MaxRevisions); err != nil {
							resp.Failed++
							resp.Errors = append(resp.Errors, fmt.Sprintf("%s: revision trim error: %v", p.Doc.Key, err))
							continue
						}
					}
				}
			}

			// Count success
			if p.IsUpdate {
				resp.Updated++
			} else {
				resp.Added++
			}
			committed = append(committed, p)
		}

		return nil
	})

	if err != nil {
		resp.Failed = safeInt32(len(processed))
		resp.Errors = append(resp.Errors, fmt.Sprintf("transaction error: %v", err))
		// The transaction rolled back — nothing was actually committed.
		return resp, nil
	}
	bo.FlushTo(bp.server.Binlog)

	return resp, committed
}
