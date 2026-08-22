package main

import (
	"context"
	"fmt"
	"sync"
	"time"

	"mddb/internal/binlog"
	"mddb/internal/cache"
	"mddb/internal/indexqueue"
	"mddb/internal/storage"
	proto "mddb/proto"

	bolt "go.etcd.io/bbolt"
)

// BatchUpdater handles batch update operations
type BatchUpdater struct {
	server     *Server
	maxWorkers int
}

// NewBatchUpdater creates a new batch updater
func NewBatchUpdater(server *Server, maxWorkers int) *BatchUpdater {
	if maxWorkers <= 0 {
		maxWorkers = 8
	}
	return &BatchUpdater{
		server:     server,
		maxWorkers: maxWorkers,
	}
}

// UpdatedDoc represents a processed update
type UpdatedDoc struct {
	Key          string
	Lang         string
	DocID        string
	Doc          storage.Doc
	Buf          []byte
	Meta         map[string][]string
	Existing     storage.Doc
	Found        bool
	SaveRevision bool
	Error        error
}

// ProcessBatchUpdate processes multiple document updates in parallel
func (bu *BatchUpdater) ProcessBatchUpdate(ctx context.Context, collection string, updateDocs []*proto.UpdateDocument) (*proto.UpdateBatchResponse, error) {
	if len(updateDocs) == 0 {
		return &proto.UpdateBatchResponse{}, nil
	}

	now := time.Now().Unix()

	// Phase 1: Parallel processing
	updated := bu.parallelProcess(ctx, collection, updateDocs, now)

	// Phase 2: Single transaction commit
	resp := bu.commitUpdate(collection, updated, now)

	return resp, nil
}

// parallelProcess processes updates in parallel
func (bu *BatchUpdater) parallelProcess(ctx context.Context, collection string, updateDocs []*proto.UpdateDocument, now int64) []*UpdatedDoc {
	updated := make([]*UpdatedDoc, len(updateDocs))

	numWorkers := bu.maxWorkers
	if len(updateDocs) < numWorkers {
		numWorkers = len(updateDocs)
	}

	jobs := make(chan int, len(updateDocs))
	var wg sync.WaitGroup

	// Start workers
	for w := 0; w < numWorkers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for idx := range jobs {
				updated[idx] = bu.processDocument(collection, updateDocs[idx], now)
			}
		}()
	}

	// Send jobs
	for i := range updateDocs {
		jobs <- i
	}
	close(jobs)

	wg.Wait()
	return updated
}

// processDocument processes a single update
func (bu *BatchUpdater) processDocument(collection string, updateDoc *proto.UpdateDocument, now int64) *UpdatedDoc {
	result := &UpdatedDoc{
		Key:          updateDoc.Key,
		Lang:         updateDoc.Lang,
		SaveRevision: updateDoc.SaveRevision,
	}

	// Validate
	if updateDoc.Key == "" || updateDoc.Lang == "" {
		result.Error = fmt.Errorf("missing key or lang")
		return result
	}

	// Convert meta
	meta := make(map[string][]string)
	for k, v := range updateDoc.Meta {
		meta[k] = v.Values
	}
	result.Meta = meta

	// Generate ID
	docID := genID(collection, updateDoc.Key, updateDoc.Lang)
	result.DocID = docID

	// Load existing
	existing := storage.Doc{}
	err := bu.server.DBView(func(tx *bolt.Tx) error {
		bDocs := tx.Bucket(bu.server.BucketNames.Docs)
		if v := bDocs.Get(storage.DocKey(collection, docID)); v != nil {
			existingDoc, err := unmarshalDoc(v)
			if err != nil {
				return err
			}
			existing = *existingDoc
			result.Found = true
		}
		return nil
	})

	if err != nil {
		result.Error = err
		return result
	}

	if !result.Found {
		result.Error = fmt.Errorf("document not found")
		return result
	}

	result.Existing = existing

	// Prepare updated document.
	//
	// An omitted field means "leave it alone", not "clear it" (TEST-002).
	// This used to assign both unconditionally, so an agent updating tags on
	// a hundred documents erased the content of all of them, and one updating
	// content erased their metadata. The single-document path already
	// distinguishes the two by taking pointers; the batch types cannot, so
	// empty is read as absent.
	//
	// The cost is that a batch cannot deliberately blank a field. Clearing is
	// rare, deliberate and available on the single-document endpoint —
	// silently destroying content that nobody asked to change is not a
	// trade worth keeping.
	content := existing.ContentMD
	if updateDoc.ContentMd != "" {
		content = updateDoc.ContentMd
	}
	effectiveMeta := existing.Meta
	if len(meta) > 0 {
		effectiveMeta = meta
	}

	doc := storage.Doc{
		ID:        docID,
		Key:       updateDoc.Key,
		Lang:      updateDoc.Lang,
		Meta:      effectiveMeta,
		ContentMD: content,
		AddedAt:   existing.AddedAt,
		UpdatedAt: now,
	}

	// Marshal (+ optional at-rest encryption based on CollectionConfig.Encrypted)
	buf, err := marshalAndEncrypt(&doc, collection)
	if err != nil {
		result.Error = err
		return result
	}

	result.Doc = doc
	result.Buf = buf

	return result
}

// commitUpdate commits all updates in a single transaction
func (bu *BatchUpdater) commitUpdate(collection string, updated []*UpdatedDoc, now int64) *proto.UpdateBatchResponse {
	resp := &proto.UpdateBatchResponse{}

	var bo binlog.BinlogOps
	// Metadata reindex jobs collected during the tx and enqueued AFTER commit:
	// Enqueue's full-queue fallback opens its own write transaction, so it must
	// never run inside this one (GO-010 — would deadlock BoltDB's single writer).
	var indexJobs []*indexqueue.IndexJob
	// Single transaction for all updates
	err := bu.server.DBUpdate(func(tx *bolt.Tx) error {
		bDocs := tx.Bucket(bu.server.BucketNames.Docs)
		bRev := tx.Bucket(bu.server.BucketNames.Rev)

		for _, u := range updated {
			if u.Error != nil {
				if u.Error.Error() == "document not found" {
					resp.NotFound++
				} else {
					resp.Failed++
					resp.Errors = append(resp.Errors, fmt.Sprintf("%s/%s: %v", u.Key, u.Lang, u.Error))
				}
				continue
			}

			// Update document
			docKey := storage.DocKey(collection, u.DocID)
			if err := bDocs.Put(docKey, u.Buf); err != nil {
				resp.Failed++
				resp.Errors = append(resp.Errors, fmt.Sprintf("%s/%s: update error: %v", u.Key, u.Lang, err))
				continue
			}
			bo.Put("docs", docKey, u.Buf)

			// Collect metadata reindex job; enqueued after the tx commits.
			if metadataChanged(u.Existing.Meta, u.Doc.Meta) {
				indexJobs = append(indexJobs, &indexqueue.IndexJob{
					Collection: collection,
					DocID:      u.DocID,
					OldMeta:    u.Existing.Meta,
					NewMeta:    u.Doc.Meta,
				})
			}

			// Revision (optional)
			if u.SaveRevision {
				rkey := append(storage.RevPrefix(collection, u.Doc.ID), []byte(fmt.Sprintf("%020d", now))...)
				if err := bRev.Put(rkey, u.Buf); err != nil {
					resp.Failed++
					resp.Errors = append(resp.Errors, fmt.Sprintf("%s/%s: revision error: %v", u.Key, u.Lang, err))
					continue
				}
				bo.Put("rev", rkey, u.Buf)
			}

			// Update cache
			cacheKey := cache.BuildCacheKey(collection, u.Key, u.Lang)
			bu.server.Cache.Set(cacheKey, u.Buf)

			resp.Updated++
		}

		return nil
	})

	if err != nil {
		resp.Failed++
		resp.Errors = append(resp.Errors, fmt.Sprintf("transaction error: %v", err))
		return resp
	}
	bo.FlushTo(bu.server.Binlog)

	// Enqueue metadata reindex jobs now that the write tx has committed.
	// Enqueue never drops (synchronous fallback on a full queue); a returned
	// error means the index entry could not be written — surface it rather
	// than letting the doc silently fall out of meta queries (GO-010).
	if bu.server.IndexQueue != nil {
		for _, job := range indexJobs {
			if jerr := bu.server.IndexQueue.Enqueue(job); jerr != nil {
				resp.Errors = append(resp.Errors, fmt.Sprintf("%s: meta index error: %v", job.DocID, jerr))
			}
		}
	}

	return resp
}
