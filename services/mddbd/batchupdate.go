package main

import (
	"context"
	"fmt"
	"sync"
	"time"

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
	Doc          Doc
	Buf          []byte
	Meta         map[string][]string
	Existing     Doc
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
	existing := Doc{}
	err := bu.server.DB.View(func(tx *bolt.Tx) error {
		bDocs := tx.Bucket(bu.server.BucketNames.Docs)
		if v := bDocs.Get(kDoc(collection, docID)); v != nil {
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
	
	// Prepare updated document
	doc := Doc{
		ID:        docID,
		Key:       updateDoc.Key,
		Lang:      updateDoc.Lang,
		Meta:      meta,
		ContentMD: updateDoc.ContentMd,
		AddedAt:   existing.AddedAt,
		UpdatedAt: now,
	}
	
	// Marshal
	buf, err := marshalDoc(&doc)
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
	
	// Single transaction for all updates
	err := bu.server.DB.Update(func(tx *bolt.Tx) error {
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
			docKey := kDoc(collection, u.DocID)
			if err := bDocs.Put(docKey, u.Buf); err != nil {
				resp.Failed++
				resp.Errors = append(resp.Errors, fmt.Sprintf("%s/%s: update error: %v", u.Key, u.Lang, err))
				continue
			}
			
			// Queue metadata reindexing (lazy)
			if metadataChanged(u.Existing.Meta, u.Doc.Meta) {
				bu.server.IndexQueue.Enqueue(&IndexJob{
					Collection: collection,
					DocID:      u.DocID,
					OldMeta:    u.Existing.Meta,
					NewMeta:    u.Doc.Meta,
				})
			}
			
			// Revision (optional)
			if u.SaveRevision {
				rkey := append(kRevPrefix(collection, u.Doc.ID), []byte(fmt.Sprintf("%020d", now))...)
				if err := bRev.Put(rkey, u.Buf); err != nil {
					resp.Failed++
					resp.Errors = append(resp.Errors, fmt.Sprintf("%s/%s: revision error: %v", u.Key, u.Lang, err))
					continue
				}
			}
			
			// Update cache
			cacheKey := BuildCacheKey(collection, u.Key, u.Lang)
			bu.server.Cache.Set(cacheKey, u.Buf)
			
			resp.Updated++
		}
		
		return nil
	})
	
	if err != nil {
		resp.Failed++
		resp.Errors = append(resp.Errors, fmt.Sprintf("transaction error: %v", err))
	}
	
	return resp
}
