package main

import (
	"context"
	"fmt"
	"sync"
	"time"

	bolt "go.etcd.io/bbolt"
	proto "mddb/proto"
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
	resp := fbp.commitBatch(collection, processed, now)
	
	return resp, nil
}

// batchReadAll reads ALL documents in SINGLE transaction
func (fbp *FinalBatchProcessor) batchReadAll(collection string, batchDocs []*proto.BatchDocument) map[string][]byte {
	existingMap := make(map[string][]byte, len(batchDocs))
	
	// SINGLE READ TRANSACTION for ALL documents
	_ = fbp.server.DB.View(func(tx *bolt.Tx) error {
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
				select {
				case <-ctx.Done():
					return
				default:
					processed[idx] = fbp.processDocumentFast(collection, batchDocs[idx], existingMap, now)
				}
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
	
	doc := Doc{
		ID: docID, Key: batchDoc.Key, Lang: batchDoc.Lang, Meta: meta,
		ContentMD: batchDoc.ContentMd, AddedAt: added, UpdatedAt: now,
	}
	
	buf, err := marshalDoc(&doc)
	if err != nil {
		result.Error = err
		return result
	}
	
	result.Doc = doc
	result.Buf = buf
	result.SaveRevision = batchDoc.SaveRevision
	
	return result
}

// commitBatch commits with optimized key building
func (fbp *FinalBatchProcessor) commitBatch(collection string, processed []*ProcessedDoc, now int64) *proto.AddBatchResponse {
	resp := &proto.AddBatchResponse{}
	
	err := fbp.server.DB.Update(func(tx *bolt.Tx) error {
		bDocs := tx.Bucket(fbp.server.BucketNames.Docs)
		bIdx := tx.Bucket(fbp.server.BucketNames.IdxMeta)
		bRev := tx.Bucket(fbp.server.BucketNames.Rev)
		bByK := tx.Bucket(fbp.server.BucketNames.ByKey)
		
		// Pre-allocate reusable buffers
		docKeyBuf := make([]byte, 0, 256)
		byKeyBuf := make([]byte, 0, 256)
		metaKeyBuf := make([]byte, 0, 256)
		revKeyBuf := make([]byte, 0, 256)
		
		for _, p := range processed {
			if p.Error != nil {
				resp.Failed++
				resp.Errors = append(resp.Errors, fmt.Sprintf("%s: %v", p.Doc.Key, p.Error))
				continue
			}
			
			// Build doc key
			docKeyBuf = docKeyBuf[:0]
			docKeyBuf = append(docKeyBuf, "doc|"...)
			docKeyBuf = append(docKeyBuf, collection...)
			docKeyBuf = append(docKeyBuf, '|')
			docKeyBuf = append(docKeyBuf, p.DocID...)
			
			if err := bDocs.Put(docKeyBuf, p.Buf); err != nil {
				resp.Failed++
				continue
			}
			
			// Build bykey index
			byKeyBuf = byKeyBuf[:0]
			byKeyBuf = append(byKeyBuf, "bykey|"...)
			byKeyBuf = append(byKeyBuf, collection...)
			byKeyBuf = append(byKeyBuf, '|')
			byKeyBuf = append(byKeyBuf, p.Doc.Key...)
			byKeyBuf = append(byKeyBuf, '|')
			byKeyBuf = append(byKeyBuf, p.Doc.Lang...)
			
			if err := bByK.Put(byKeyBuf, []byte(p.DocID)); err != nil {
				resp.Failed++
				continue
			}
			
			// Metadata indexing
			if metadataChanged(p.Existing.Meta, p.Doc.Meta) {
				// Delete old indices
				if p.Existing.ID != "" && p.Existing.Meta != nil {
					for mk, vals := range p.Existing.Meta {
						for _, mv := range vals {
							metaKeyBuf = metaKeyBuf[:0]
							metaKeyBuf = append(metaKeyBuf, "meta|"...)
							metaKeyBuf = append(metaKeyBuf, collection...)
							metaKeyBuf = append(metaKeyBuf, '|')
							metaKeyBuf = append(metaKeyBuf, mk...)
							metaKeyBuf = append(metaKeyBuf, '|')
							metaKeyBuf = append(metaKeyBuf, mv...)
							metaKeyBuf = append(metaKeyBuf, '|')
							metaKeyBuf = append(metaKeyBuf, p.Existing.ID...)
							_ = bIdx.Delete(metaKeyBuf)
						}
					}
				}
				
				// Add new indices
				for mk, vals := range p.Doc.Meta {
					for _, mv := range vals {
						metaKeyBuf = metaKeyBuf[:0]
						metaKeyBuf = append(metaKeyBuf, "meta|"...)
						metaKeyBuf = append(metaKeyBuf, collection...)
						metaKeyBuf = append(metaKeyBuf, '|')
						metaKeyBuf = append(metaKeyBuf, mk...)
						metaKeyBuf = append(metaKeyBuf, '|')
						metaKeyBuf = append(metaKeyBuf, mv...)
						metaKeyBuf = append(metaKeyBuf, '|')
						metaKeyBuf = append(metaKeyBuf, p.Doc.ID...)
						_ = bIdx.Put(metaKeyBuf, []byte("1"))
					}
				}
			}
			
			// Revision
			if p.SaveRevision {
				revKeyBuf = revKeyBuf[:0]
				revKeyBuf = append(revKeyBuf, "rev|"...)
				revKeyBuf = append(revKeyBuf, collection...)
				revKeyBuf = append(revKeyBuf, '|')
				revKeyBuf = append(revKeyBuf, p.Doc.ID...)
				revKeyBuf = append(revKeyBuf, '|')
				
				// Format timestamp efficiently
				ts := FormatTimestamp(now, make([]byte, 20))
				revKeyBuf = append(revKeyBuf, ts...)
				
				_ = bRev.Put(revKeyBuf, p.Buf)
			}
			
			// Update cache
			cacheKey := BuildCacheKey(collection, p.Doc.Key, p.Doc.Lang)
			if fbp.server.UseExtreme && fbp.server.LockFreeCache != nil {
				fbp.server.LockFreeCache.Set(cacheKey, p.Buf)
			} else {
				fbp.server.Cache.Set(cacheKey, p.Buf)
			}
			
			if p.IsUpdate {
				resp.Updated++
			} else {
				resp.Added++
			}
		}
		
		return nil
	})
	
	if err != nil {
		resp.Failed = int32(len(processed))
		resp.Errors = append(resp.Errors, fmt.Sprintf("transaction error: %v", err))
	}
	
	return resp
}
