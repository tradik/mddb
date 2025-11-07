package main

import (
	"context"
	"fmt"
	"sync"

	proto "mddb/proto"

	bolt "go.etcd.io/bbolt"
)

// BatchDeleter handles batch delete operations
type BatchDeleter struct {
	server     *Server
	maxWorkers int
}

// NewBatchDeleter creates a new batch deleter
func NewBatchDeleter(server *Server, maxWorkers int) *BatchDeleter {
	if maxWorkers <= 0 {
		maxWorkers = 8
	}
	return &BatchDeleter{
		server:     server,
		maxWorkers: maxWorkers,
	}
}

// DeletedDoc represents a document to delete
type DeletedDoc struct {
	Key      string
	Lang     string
	DocID    string
	Found    bool
	OldMeta  map[string][]string
	Error    error
}

// ProcessBatchDelete processes multiple document deletions in parallel
func (bd *BatchDeleter) ProcessBatchDelete(ctx context.Context, collection string, deleteDocs []*proto.DeleteDocument) (*proto.DeleteBatchResponse, error) {
	if len(deleteDocs) == 0 {
		return &proto.DeleteBatchResponse{}, nil
	}

	// Phase 1: Parallel lookup
	deleted := bd.parallelLookup(ctx, collection, deleteDocs)
	
	// Phase 2: Single transaction delete
	resp := bd.commitDelete(collection, deleted)
	
	return resp, nil
}

// parallelLookup looks up documents in parallel
func (bd *BatchDeleter) parallelLookup(ctx context.Context, collection string, deleteDocs []*proto.DeleteDocument) []*DeletedDoc {
	deleted := make([]*DeletedDoc, len(deleteDocs))
	
	numWorkers := bd.maxWorkers
	if len(deleteDocs) < numWorkers {
		numWorkers = len(deleteDocs)
	}
	
	jobs := make(chan int, len(deleteDocs))
	var wg sync.WaitGroup
	
	// Start workers
	for w := 0; w < numWorkers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for idx := range jobs {
				deleted[idx] = bd.lookupDocument(collection, deleteDocs[idx])
			}
		}()
	}
	
	// Send jobs
	for i := range deleteDocs {
		jobs <- i
	}
	close(jobs)
	
	wg.Wait()
	return deleted
}

// lookupDocument looks up a document for deletion
func (bd *BatchDeleter) lookupDocument(collection string, deleteDoc *proto.DeleteDocument) *DeletedDoc {
	result := &DeletedDoc{
		Key:  deleteDoc.Key,
		Lang: deleteDoc.Lang,
	}
	
	// Validate
	if deleteDoc.Key == "" || deleteDoc.Lang == "" {
		result.Error = fmt.Errorf("missing key or lang")
		return result
	}
	
	// Generate ID
	docID := genID(collection, deleteDoc.Key, deleteDoc.Lang)
	result.DocID = docID
	
	// Load existing document (to get metadata for cleanup)
	err := bd.server.DB.View(func(tx *bolt.Tx) error {
		bDocs := tx.Bucket(bd.server.BucketNames.Docs)
		if v := bDocs.Get(kDoc(collection, docID)); v != nil {
			existingDoc, err := unmarshalDoc(v)
			if err != nil {
				return err
			}
			result.Found = true
			result.OldMeta = existingDoc.Meta
		}
		return nil
	})
	
	if err != nil {
		result.Error = err
		return result
	}
	
	return result
}

// commitDelete commits all deletions in a single transaction
func (bd *BatchDeleter) commitDelete(collection string, deleted []*DeletedDoc) *proto.DeleteBatchResponse {
	resp := &proto.DeleteBatchResponse{}
	
	// Single transaction for all deletions
	err := bd.server.DB.Update(func(tx *bolt.Tx) error {
		bDocs := tx.Bucket(bd.server.BucketNames.Docs)
		bIdx := tx.Bucket(bd.server.BucketNames.IdxMeta)
		bRev := tx.Bucket(bd.server.BucketNames.Rev)
		bByK := tx.Bucket(bd.server.BucketNames.ByKey)
		
		for _, d := range deleted {
			if d.Error != nil {
				resp.Failed++
				resp.Errors = append(resp.Errors, fmt.Sprintf("%s/%s: %v", d.Key, d.Lang, d.Error))
				continue
			}
			
			if !d.Found {
				resp.NotFound++
				continue
			}
			
			// Delete document
			docKey := kDoc(collection, d.DocID)
			if err := bDocs.Delete(docKey); err != nil {
				resp.Failed++
				resp.Errors = append(resp.Errors, fmt.Sprintf("%s/%s: delete error: %v", d.Key, d.Lang, err))
				continue
			}
			
			// Delete bykey index
			byKeyKey := kByKey(collection, d.Key, d.Lang)
			_ = bByK.Delete(byKeyKey)
			
			// Delete metadata indices
			if d.OldMeta != nil {
				for mk, vals := range d.OldMeta {
					for _, mv := range vals {
						metaKey := append(kMetaKeyPrefix(collection, mk, mv), []byte(d.DocID)...)
						_ = bIdx.Delete(metaKey)
					}
				}
			}
			
			// Delete revisions
			revPrefix := kRevPrefix(collection, d.DocID)
			c := bRev.Cursor()
			for k, _ := c.Seek(revPrefix); k != nil && len(k) >= len(revPrefix); k, _ = c.Next() {
				if string(k[:len(revPrefix)]) != string(revPrefix) {
					break
				}
				_ = bRev.Delete(k)
			}
			
			// Invalidate cache
			cacheKey := BuildCacheKey(collection, d.Key, d.Lang)
			bd.server.Cache.Delete(cacheKey)
			
			resp.Deleted++
		}
		
		return nil
	})
	
	if err != nil {
		resp.Failed++
		resp.Errors = append(resp.Errors, fmt.Sprintf("transaction error: %v", err))
	}
	
	return resp
}
