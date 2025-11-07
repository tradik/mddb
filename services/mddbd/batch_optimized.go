package main

import (
	"context"
	"fmt"
	"sync"
	"time"

	bolt "go.etcd.io/bbolt"
	proto "mddb/proto"
)

// OptimizedBatchProcessor handles batch document processing with extreme optimizations
type OptimizedBatchProcessor struct {
	server       *Server
	maxWorkers   int
	keyBuilders  sync.Pool // Pool of KeyBuilders
	metaMaps     sync.Pool // Pool of metadata maps
	docBuffers   sync.Pool // Pool of document buffers
}

// NewOptimizedBatchProcessor creates an optimized batch processor
func NewOptimizedBatchProcessor(server *Server, maxWorkers int) *OptimizedBatchProcessor {
	if maxWorkers <= 0 {
		maxWorkers = 8
	}
	
	return &OptimizedBatchProcessor{
		server:     server,
		maxWorkers: maxWorkers,
		keyBuilders: sync.Pool{
			New: func() interface{} {
				return &KeyBuilder{}
			},
		},
		metaMaps: sync.Pool{
			New: func() interface{} {
				return make(map[string][]string, 10)
			},
		},
		docBuffers: sync.Pool{
			New: func() interface{} {
				return make([]byte, 0, 4096)
			},
		},
	}
}

// ProcessBatch processes multiple documents with single-read optimization
func (obp *OptimizedBatchProcessor) ProcessBatch(ctx context.Context, collection string, batchDocs []*proto.BatchDocument) (*proto.AddBatchResponse, error) {
	if len(batchDocs) == 0 {
		return &proto.AddBatchResponse{}, nil
	}

	now := time.Now().Unix()
	
	// Phase 1: Batch read all existing documents in SINGLE transaction
	existingDocs := obp.batchReadExisting(collection, batchDocs)
	
	// Phase 2: Parallel processing (prepare documents)
	processed := obp.parallelProcess(ctx, collection, batchDocs, existingDocs, now)
	
	// Phase 3: Single transaction commit
	resp := obp.commitBatch(collection, processed, now)
	
	return resp, nil
}

// batchReadExisting reads all existing documents in a SINGLE transaction
func (obp *OptimizedBatchProcessor) batchReadExisting(collection string, batchDocs []*proto.BatchDocument) map[string]*Doc {
	existingDocs := make(map[string]*Doc, len(batchDocs))
	
	// Single read transaction for ALL documents
	_ = obp.server.DB.View(func(tx *bolt.Tx) error {
		bDocs := tx.Bucket(obp.server.BucketNames.Docs)
		
		// Get KeyBuilder from pool
		kb := obp.keyBuilders.Get().(*KeyBuilder)
		defer obp.keyBuilders.Put(kb)
		
		for _, batchDoc := range batchDocs {
			if batchDoc.Key == "" || batchDoc.Lang == "" {
				continue
			}
			
			// Use optimized ID generation
			docID := fastGenID(collection, batchDoc.Key, batchDoc.Lang)
			
			// Build key using pooled KeyBuilder
			docKey := kb.BuildDocKey(collection, docID)
			
			if v := bDocs.Get(docKey); v != nil {
				if existingDoc, err := unmarshalDoc(v); err == nil {
					existingDocs[docID] = existingDoc
				}
			}
		}
		
		return nil
	})
	
	return existingDocs
}

// parallelProcess processes documents in parallel with pooled resources
func (obp *OptimizedBatchProcessor) parallelProcess(ctx context.Context, collection string, batchDocs []*proto.BatchDocument, existingDocs map[string]*Doc, now int64) []*ProcessedDoc {
	processed := make([]*ProcessedDoc, len(batchDocs))
	
	numWorkers := obp.maxWorkers
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
			
			// Get pooled resources for this worker
			kb := obp.keyBuilders.Get().(*KeyBuilder)
			defer obp.keyBuilders.Put(kb)
			
			for idx := range jobs {
				select {
				case <-ctx.Done():
					return
				default:
					processed[idx] = obp.processDocumentOptimized(collection, batchDocs[idx], existingDocs, now, kb)
				}
			}
		}()
	}
	
	// Send jobs
	for i := range batchDocs {
		jobs <- i
	}
	close(jobs)
	
	wg.Wait()
	
	return processed
}

// processDocumentOptimized processes a single document with pooled resources
func (obp *OptimizedBatchProcessor) processDocumentOptimized(collection string, batchDoc *proto.BatchDocument, existingDocs map[string]*Doc, now int64, kb *KeyBuilder) *ProcessedDoc {
	result := &ProcessedDoc{}
	
	// Validate
	if batchDoc.Key == "" || batchDoc.Lang == "" {
		result.Error = fmt.Errorf("missing key or lang")
		return result
	}
	
	// Get metadata map from pool
	meta := obp.metaMaps.Get().(map[string][]string)
	defer func() {
		// Clear and return to pool
		for k := range meta {
			delete(meta, k)
		}
		obp.metaMaps.Put(meta)
	}()
	
	// Convert meta (reuse map)
	for k, v := range batchDoc.Meta {
		meta[k] = v.Values
	}
	
	// Copy meta for result (can't reuse pooled map)
	resultMeta := make(map[string][]string, len(meta))
	for k, v := range meta {
		resultMeta[k] = v
	}
	result.Meta = resultMeta
	
	// Use optimized ID generation
	docID := fastGenID(collection, batchDoc.Key, batchDoc.Lang)
	result.DocID = docID
	
	// Get existing from pre-loaded map (no transaction!)
	var existing Doc
	if existingDoc, ok := existingDocs[docID]; ok {
		existing = *existingDoc
		result.IsUpdate = true
	}
	result.Existing = existing
	
	// Prepare document
	added := existing.AddedAt
	if added == 0 {
		added = now
	}
	
	doc := Doc{
		ID: docID, Key: batchDoc.Key, Lang: batchDoc.Lang, Meta: resultMeta,
		ContentMD: batchDoc.ContentMd, AddedAt: added, UpdatedAt: now,
	}
	
	// Marshal
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

// commitBatch commits all processed documents in a single transaction
func (obp *OptimizedBatchProcessor) commitBatch(collection string, processed []*ProcessedDoc, now int64) *proto.AddBatchResponse {
	resp := &proto.AddBatchResponse{}
	
	// Get KeyBuilder from pool for commit phase
	kb := obp.keyBuilders.Get().(*KeyBuilder)
	defer obp.keyBuilders.Put(kb)
	
	// Single transaction for all documents
	err := obp.server.DB.Update(func(tx *bolt.Tx) error {
		bDocs := tx.Bucket(obp.server.BucketNames.Docs)
		bIdx := tx.Bucket(obp.server.BucketNames.IdxMeta)
		bRev := tx.Bucket(obp.server.BucketNames.Rev)
		bByK := tx.Bucket(obp.server.BucketNames.ByKey)
		
		for _, p := range processed {
			if p.Error != nil {
				resp.Failed++
				resp.Errors = append(resp.Errors, fmt.Sprintf("%s: %v", p.Doc.Key, p.Error))
				continue
			}
			
			// Use KeyBuilder for all keys
			docKey := kb.BuildDocKey(collection, p.DocID)
			if err := bDocs.Put(docKey, p.Buf); err != nil {
				resp.Failed++
				resp.Errors = append(resp.Errors, fmt.Sprintf("%s: put error: %v", p.Doc.Key, err))
				continue
			}
			
			byKeyKey := kb.BuildByKey(collection, p.Doc.Key, p.Doc.Lang)
			if err := bByK.Put(byKeyKey, []byte(p.DocID)); err != nil {
				resp.Failed++
				resp.Errors = append(resp.Errors, fmt.Sprintf("%s: bykey error: %v", p.Doc.Key, err))
				continue
			}
			
			// Only reindex metadata if changed
			if metadataChanged(p.Existing.Meta, p.Doc.Meta) {
				// Delete old indices
				if p.Existing.ID != "" && p.Existing.Meta != nil {
					for mk, vals := range p.Existing.Meta {
						for _, mv := range vals {
							metaKey := kb.BuildMetaKey(collection, mk, mv, p.Existing.ID)
							_ = bIdx.Delete(metaKey)
						}
					}
				}
				
				// Add new indices
				for mk, vals := range p.Doc.Meta {
					for _, mv := range vals {
						metaKey := kb.BuildMetaKey(collection, mk, mv, p.Doc.ID)
						if err := bIdx.Put(metaKey, []byte("1")); err != nil {
							resp.Failed++
							resp.Errors = append(resp.Errors, fmt.Sprintf("%s: index error: %v", p.Doc.Key, err))
							continue
						}
					}
				}
			}
			
			// Revision (optional)
			if p.SaveRevision {
				revKey := kb.BuildRevKey(collection, p.Doc.ID, now)
				if err := bRev.Put(revKey, p.Buf); err != nil {
					resp.Failed++
					resp.Errors = append(resp.Errors, fmt.Sprintf("%s: revision error: %v", p.Doc.Key, err))
					continue
				}
			}
			
			// Update cache (use lock-free if available)
			cacheKey := BuildCacheKey(collection, p.Doc.Key, p.Doc.Lang)
			if obp.server.UseExtreme && obp.server.LockFreeCache != nil {
				obp.server.LockFreeCache.Set(cacheKey, p.Buf)
			} else {
				obp.server.Cache.Set(cacheKey, p.Buf)
			}
			
			// Count success
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

// fastGenID generates ID without string allocations
func fastGenID(parts ...string) string {
	// Pre-calculate total length
	totalLen := 0
	for i, part := range parts {
		totalLen += len(part)
		if i < len(parts)-1 {
			totalLen++ // for '|'
		}
	}
	
	// Single allocation
	buf := make([]byte, 0, totalLen)
	
	for i, part := range parts {
		// Convert to lowercase inline
		for j := 0; j < len(part); j++ {
			c := part[j]
			if c >= 'A' && c <= 'Z' {
				c += 'a' - 'A'
			}
			buf = append(buf, c)
		}
		
		if i < len(parts)-1 {
			buf = append(buf, '|')
		}
	}
	
	return string(buf)
}
