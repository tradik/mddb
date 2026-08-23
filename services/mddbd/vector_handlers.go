package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	json "mddb/internal/jsonx"
	bolt "go.etcd.io/bbolt"
	"log/slog"
	"mddb/internal/envconf"
	"mddb/internal/sliceutil"
	"mddb/internal/storage"
	vec "mddb/internal/vector"
	"net/http"
	"strings"
	"time"
)

// VectorSearchRequest represents an HTTP vector search request.
type VectorSearchRequest struct {
	Collection     string              `json:"collection"`
	Query          string              `json:"query"`
	QueryVector    []float32           `json:"queryVector"`
	TopK           int                 `json:"topK"`
	Threshold      float64             `json:"threshold"`
	FilterMeta     map[string][]string `json:"filterMeta"`
	IncludeContent bool                `json:"includeContent"`
	Algorithm      string              `json:"algorithm"`      // "flat" (default), "hnsw", "ivf", "pq", "opq", "sq", "sq4", "bq"
	DistanceMetric string              `json:"distanceMetric"` // "cosine" (default), "dot_product", "euclidean"
	RetrievalMode  string              `json:"retrievalMode"`  // "parent" (default), "chunk", "window", "graph"
	// GraphExpand tunes retrievalMode "graph" (SRCH-006). Ignored in the other
	// modes, so a request carrying both is not ambiguous.
	GraphExpand GraphExpandOptions `json:"graphExpand,omitempty"`
	WindowSize  int                `json:"windowSize"` // neighbor chunks per side in "window" mode (default 1)
	MMR         bool               `json:"mmr"`        // diversify results via Maximal Marginal Relevance
	MMRLambda   float64            `json:"mmrLambda"`  // relevance/diversity balance, 0..1 (default 0.5)
	// Oversample is the recall/latency knob (SRCH-005): candidates asked of
	// the index per requested result, before deduplication trims them.
	// 1.0-10.0; 0 = use the collection profile, then the default.
	Oversample float64 `json:"oversample,omitempty"`
}

// VectorSearchResultItem represents a single search result.
type VectorSearchResultItem struct {
	Document   storage.Doc `json:"document"`
	Score      float32     `json:"score"`
	Rank       int         `json:"rank"`
	ChunkIndex *int        `json:"chunkIndex,omitempty"` // set in chunk/window retrieval modes
	ChunkText  string      `json:"chunkText,omitempty"`  // matching passage (chunk/window modes)
	// StartLine and EndLine are 1-based and inclusive, locating the passage in
	// the parent document (CODE-002). Without them a caller knows which
	// document matched and has to read it to find where.
	StartLine int `json:"startLine,omitempty"`
	EndLine   int `json:"endLine,omitempty"`
}

// VectorSearchResponseHTTP represents the response from vector search.
type VectorSearchResponseHTTP struct {
	Results        []VectorSearchResultItem `json:"results"`
	Total          int                      `json:"total"`
	Model          string                   `json:"model"`
	Dimensions     int                      `json:"dimensions"`
	Algorithm      string                   `json:"algorithm"`
	DistanceMetric string                   `json:"distanceMetric"`
	Stats          *SearchStats             `json:"searchStats,omitempty"`
	// ContextTruncated reports that the collection's contextTokenBudget
	// dropped results from the tail (RAG-001).
	ContextTruncated bool `json:"contextTruncated,omitempty"`
	// GraphExpansions lists documents added by retrievalMode "graph" and the
	// edge that justifies each (SRCH-006). Present only in that mode: a
	// document that matched nothing needs to say why it is here.
	GraphExpansions []GraphExpansion `json:"graphExpansions,omitempty"`
}

// VectorReindexRequestHTTP represents a reindex request.
type VectorReindexRequestHTTP struct {
	Collection string `json:"collection"`
	Force      bool   `json:"force"`
}

// loadVectorIndex loads all vectors from BoltDB into all in-memory indexes.
func (s *Server) loadVectorIndex() {
	start := time.Now()

	// Get all collections with vectors
	counts, err := s.VectorStore.CountByCollection()
	if err != nil {
		slog.Error("failed to count vector collections", "err", err)
		s.VectorIndex.SetReady()
		for _, searcher := range s.VectorSearchers {
			searcher.SetReady()
		}
		return
	}

	totalLoaded := 0
	for collection, count := range counts {
		records, err := s.VectorStore.LoadCollection(collection)
		if err != nil {
			slog.Error("failed to load vectors for collection", "collection", collection, "err", err)
			continue
		}

		// Collect vectors for trainable indexes
		collVecs := make(map[string][]float32, len(records))

		// Disk-only collections keep RAM quantized-only: full-precision
		// vectors are never loaded into the float32 searchers.
		diskOnly := s.collectionDiskOnly(collection)

		for docID, rec := range records {
			// Add to all searchers (docID may be "id" or "id#0", "id#1", etc.)
			if !diskOnly {
				for name, searcher := range s.VectorSearchers {
					if name == "quantized" {
						continue // quantized index is populated separately below
					}
					searcher.Add(collection, docID, rec.Vector)
				}
			}
			// Also add to quantized index (it will self-check if collection has quantization)
			if s.QuantizedVecIndex != nil {
				s.QuantizedVecIndex.Add(collection, docID, rec.Vector)
			}
			collVecs[docID] = rec.Vector
		}
		if diskOnly {
			collVecs = nil // nothing to train float32 indexes with
		}

		// Trigger training for trainable indexes (IVF, PQ)
		for _, searcher := range s.VectorSearchers {
			if trainer, ok := searcher.(vec.Trainable); ok {
				trainer.Train(collection, collVecs)
			}
		}

		totalLoaded += count
	}

	// Mark all searchers as ready
	for _, searcher := range s.VectorSearchers {
		searcher.SetReady()
	}
	slog.Info("vector index loaded",
		"documents", totalLoaded, "collections", len(counts), "elapsed", time.Since(start),
		"algorithms", "flat, hnsw, ivf, pq")
}

// handleVectorSearch handles POST /v1/vector-search
func (s *Server) handleVectorSearch(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	var req VectorSearchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		bad(w, err)
		return
	}
	if req.Collection == "" {
		bad(w, errors.New("missing collection"))
		return
	}
	if req.Query == "" && len(req.QueryVector) == 0 {
		bad(w, errors.New("either query or queryVector is required"))
		return
	}
	// SRCH-005: out of range is 422, not 400 — the body parsed fine, the
	// number is the problem.
	if err := ValidateOversample(req.Oversample); err != nil {
		unprocessable(w, err)
		return
	}
	if !validRetrievalMode(req.RetrievalMode) {
		bad(w, errors.New("unknown retrievalMode: "+req.RetrievalMode+", available: parent, chunk, window"))
		return
	}

	// Select algorithm
	algo := req.Algorithm
	if algo == "" {
		algo = "flat"
	}

	// Auto-select quantized searcher if collection has quantization configured
	if algo == "flat" && s.QuantizedVecIndex != nil && s.QuantizedVecIndex.HasCollection(req.Collection) {
		algo = "quantized"
	}

	searcher, ok2 := s.VectorSearchers[algo]
	if !ok2 {
		bad(w, errors.New("unknown algorithm: "+algo+", available: flat, hnsw, ivf, pq, quantized"))
		return
	}
	if !searcher.IsReady() {
		// Fallback to flat if the requested algorithm isn't ready
		searcher = s.VectorSearchers["flat"]
		algo = "flat"
	}
	if !searcher.IsReady() {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"error":"vector index is loading, please retry"}`))
		return
	}

	// Get query vector
	var queryVector []float32
	if len(req.QueryVector) > 0 {
		queryVector = req.QueryVector
	} else if s.Embedding != nil {
		ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
		defer cancel()
		var err error
		queryVector, err = s.Embedding.Embed(ctx, req.Query)
		if err != nil {
			bad(w, errors.New("failed to embed query: "+err.Error()))
			return
		}
	} else {
		bad(w, errors.New("no embedding provider configured and no queryVector provided"))
		return
	}

	// RAG-001: request > collection profile > this path's historical default.
	topK := s.ResolveTopK(req.Collection, req.TopK, 5)

	// Resolve distance metric
	metric := vec.ResolveSimilarity(req.DistanceMetric)
	metricName := req.DistanceMetric
	if metricName == "" {
		metricName = "cosine"
	}

	// Oversample for chunk deduplication: search for more results, then
	// deduplicate. SRCH-005 makes the multiplier a request parameter — it was
	// the fixed `topK * 3` here and in four other places.
	searchTopK := OversampledTopK(topK, s.ResolveOversample(req.Collection, req.Oversample), 20)

	// Search: with or without metadata filter
	var results []vec.VectorResult
	if len(req.FilterMeta) > 0 {
		allowedIDs := s.getDocIDsByMeta(req.Collection, req.FilterMeta)
		if len(allowedIDs) == 0 {
			ok(w, VectorSearchResponseHTTP{
				Results:        []VectorSearchResultItem{},
				Total:          0,
				Algorithm:      algo,
				DistanceMetric: metricName,
			})
			return
		}
		results = searcher.SearchWithFilter(req.Collection, queryVector, searchTopK, req.Threshold, allowedIDs, metric)
	} else {
		results = searcher.Search(req.Collection, queryVector, searchTopK, req.Threshold, metric)
	}

	// Parent mode (default): one result per document, best chunk wins.
	// Chunk/window modes keep individual chunk hits so the exact matching
	// passage can be returned alongside the parent document.
	// Disk-only collections: rescore the quantized candidates against the
	// full-precision vectors on disk before any downstream ranking.
	var diskVecs map[string][]float32
	if s.collectionDiskOnly(req.Collection) {
		results, diskVecs = s.rescoreFromDisk(req.Collection, queryVector, results, metric)
	}

	chunkMode := req.RetrievalMode == RetrievalModeChunk || req.RetrievalMode == RetrievalModeWindow
	if !chunkMode {
		results = vec.DeduplicateChunkResults(results)
	}
	if req.MMR {
		// Diversify over the oversampled candidate set, then keep topK.
		results = vec.MMRRerank(results, mmrLambdaOrDefault(req.MMRLambda), topK, func(id string) []float32 {
			if v, ok := diskVecs[id]; ok {
				return v
			}
			return s.VectorIndex.GetVector(req.Collection, id)
		})
	}
	if len(results) > topK {
		results = results[:topK]
	}

	windowSize := 0
	if req.RetrievalMode == RetrievalModeWindow {
		windowSize = req.WindowSize
		if windowSize <= 0 {
			windowSize = 1
		}
	}

	// Track vector search operation
	if s.Metrics != nil {
		s.Metrics.IncOp("vector_search", algo)
	}

	// Load full documents for results
	items := make([]VectorSearchResultItem, 0, len(results))
	_ = s.DBView(func(tx *bolt.Tx) error {
		bDocs := tx.Bucket([]byte("docs"))
		if bDocs == nil {
			return nil
		}
		for rank, vr := range results {
			docID, chunkIndex := splitChunkKey(vr.DocID)
			v := bDocs.Get(storage.DocKey(req.Collection, docID))
			if v == nil {
				continue
			}
			docPtr, err := loadDoc(v)
			if err != nil {
				continue
			}
			doc := *docPtr
			item := VectorSearchResultItem{
				Score: vr.Score,
				Rank:  rank + 1,
			}
			if chunkMode {
				idx := chunkIndex
				item.ChunkIndex = &idx
				item.ChunkText, item.StartLine, item.EndLine =
					chunkPassageWithLines(doc.ContentMD, chunkIndex, windowSize, ChunkModeFor(&doc))
			}
			if !req.IncludeContent {
				doc.ContentMD = ""
			}
			item.Document = doc
			items = append(items, item)
		}
		return nil
	})

	// RAG-001: cap the total context this collection hands back. In chunk and
	// window modes the passage is what the caller receives, so that is what
	// counts against the budget — charging the whole parent document would
	// make the cap meaningless for exactly the modes RAG uses.
	items, contextTruncated := applyContextBudget(s, req.Collection, items,
		func(it VectorSearchResultItem) int {
			if it.ChunkText != "" {
				return approxTokens(it.ChunkText)
			}
			return approxTokens(it.Document.ContentMD)
		})

	// SRCH-006: reach documents the query matches on no term, by following the
	// edges the matching documents already have. Applied after the budget so
	// expansion cannot be trimmed away by a cap it was not counted against,
	// and before the response so its documents arrive with the rest.
	var expansions []GraphExpansion
	if req.RetrievalMode == RetrievalModeGraph {
		expansions, items = s.appendGraphNeighbours(req.Collection, items, req.GraphExpand, req.IncludeContent)
	}

	resp := VectorSearchResponseHTTP{
		Results:          items,
		Total:            len(items),
		Algorithm:        algo,
		DistanceMetric:   metricName,
		ContextTruncated: contextTruncated,
		GraphExpansions:  expansions,
	}
	if s.Embedding != nil {
		resp.Model = s.Embedding.Model()
		resp.Dimensions = s.Embedding.Dimensions()
	}

	if searchStatsEnabled() {
		indexSize := 0
		if searcher != nil {
			indexSize = searcher.CollectionSize(req.Collection)
		}
		queryTerms := strings.Fields(req.Query)
		resp.Stats = &SearchStats{
			DurationMs:  float64(time.Since(start).Microseconds()) / 1000.0,
			QueryTerms:  queryTerms,
			IndexSize:   indexSize,
			TotalTokens: len(queryTerms),
		}
	}

	ok(w, resp)
}

// handleVectorReindex handles POST /v1/vector-reindex
func (s *Server) handleVectorReindex(w http.ResponseWriter, r *http.Request) {
	var req VectorReindexRequestHTTP
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		bad(w, err)
		return
	}
	if req.Collection == "" {
		bad(w, errors.New("missing collection"))
		return
	}
	if s.Embedding == nil {
		bad(w, errors.New("no embedding provider configured"))
		return
	}

	chunkSize := envconf.Int("MDDB_EMBEDDING_CHUNK_SIZE", 1500)
	chunkEnabled := envconf.String("MDDB_EMBEDDING_CHUNK_ENABLED", "true") == "true"

	// Resolve quantization for this collection. Disk-only collections store
	// FULL-precision vectors on disk (the quantized form lives only in RAM),
	// so storage quantization is disabled for them.
	var qt vec.QuantizationType
	if s.CollectionManager != nil {
		if cfg, ok := s.CollectionManager.Get(req.Collection); ok && cfg.Quantization != "" {
			qt = vec.ParseQuantization(cfg.Quantization)
		}
	}
	diskOnly := s.collectionDiskOnly(req.Collection)
	if diskOnly {
		qt = vec.QuantNone
	}

	// Load all documents in collection
	type docEntry struct {
		ID        string
		ContentMD string
		// Mode has to travel with the document: reindexing must segment it
		// exactly as the embedding worker did, or the stored chunk indices
		// stop pointing at the passages they named (CODE-003).
		Mode ChunkMode
	}
	var docs []docEntry

	err := s.DBView(func(tx *bolt.Tx) error {
		bDocs := tx.Bucket([]byte("docs"))
		if bDocs == nil {
			return nil
		}
		c := bDocs.Cursor()
		prefix := []byte("doc|" + req.Collection + "|")
		for k, v := c.Seek(prefix); k != nil && bytes.HasPrefix(k, prefix); k, v = c.Next() {
			d, err := loadDoc(v)
			if err != nil {
				continue
			}
			docs = append(docs, docEntry{ID: d.ID, ContentMD: d.ContentMD, Mode: ChunkModeFor(d)})
		}
		return nil
	})
	if err != nil {
		bad(w, err)
		return
	}

	embedded, skipped, failed, totalChunks := 0, 0, 0, 0
	var errs []string

	for _, d := range docs {
		if d.ContentMD == "" {
			skipped++
			continue
		}

		// Check if already embedded with same content hash
		if !req.Force {
			existing, err := s.VectorStore.Get(req.Collection, d.ID)
			if err == nil && existing != nil {
				currentHash := vec.ContentHash(d.ContentMD)
				if existing.ContentHash == currentHash {
					skipped++
					continue
				}
			}
		}

		// Split into chunks
		var chunks []string
		if chunkEnabled {
			chunks = chunkTextsMode(d.ContentMD, chunkSize, d.Mode)
		} else {
			chunks = []string{d.ContentMD}
		}

		if len(chunks) == 0 {
			skipped++
			continue
		}

		// Generate embedding for each chunk
		var chunkEmbeddings []vec.ChunkEmbedding
		chunkFailed := false
		for i, chunk := range chunks {
			ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
			vector, err := s.Embedding.Embed(ctx, chunk)
			cancel()
			if err != nil {
				failed++
				errs = append(errs, fmt.Sprintf("%s chunk %d: %s", d.ID, i, err.Error()))
				chunkFailed = true
				break
			}
			chunkEmbeddings = append(chunkEmbeddings, vec.ChunkEmbedding{
				ChunkIndex: i,
				Vector:     vector,
			})
		}
		if chunkFailed {
			continue
		}

		// Store all chunks (with quantization if configured)
		contentHash := vec.ContentHash(d.ContentMD)
		if err := s.VectorStore.PutChunksQuantized(req.Collection, d.ID, chunkEmbeddings, s.Embedding.Model(), contentHash, qt); err != nil {
			failed++
			errs = append(errs, d.ID+": store: "+err.Error())
			continue
		}

		// Update in-memory indexes (disk-only: quantized index exclusively)
		for _, ce := range chunkEmbeddings {
			chunkKey := fmt.Sprintf("%s#%d", d.ID, ce.ChunkIndex)
			if !diskOnly {
				for name, searcher := range s.VectorSearchers {
					if name == "quantized" {
						continue
					}
					searcher.Add(req.Collection, chunkKey, ce.Vector)
				}
			}
			if s.QuantizedVecIndex != nil {
				s.QuantizedVecIndex.Add(req.Collection, chunkKey, ce.Vector)
			}
		}

		// Clean stale chunks
		s.VectorStore.CleanStaleChunks(req.Collection, d.ID, len(chunkEmbeddings), s.VectorIndex)

		embedded++
		totalChunks += len(chunkEmbeddings)
	}

	// Trigger training for trainable indexes after reindex
	// (disk-only collections have no float32 indexes to train)
	if embedded > 0 && !diskOnly {
		if records, loadErr := s.VectorStore.LoadCollection(req.Collection); loadErr == nil {
			collVecs := make(map[string][]float32, len(records))
			for docID, rec := range records {
				collVecs[docID] = rec.Vector
			}
			for _, searcher := range s.VectorSearchers {
				if trainer, isTrainable := searcher.(vec.Trainable); isTrainable {
					go trainer.Train(req.Collection, collVecs)
				}
			}
		}
	}

	ok(w, map[string]interface{}{
		"embedded":    embedded,
		"skipped":     skipped,
		"failed":      failed,
		"totalChunks": totalChunks,
		"errors":      errs,
	})
}

// handleVectorStats handles GET /v1/vector-stats
func (s *Server) handleVectorStats(w http.ResponseWriter, r *http.Request) {
	resp := map[string]interface{}{
		"enabled": s.Embedding != nil,
	}

	if s.Embedding != nil {
		resp["provider"] = s.Embedding.Model()
		resp["model"] = s.Embedding.Model()
		resp["dimensions"] = s.Embedding.Dimensions()
	}

	// Count embeddings per collection (unique documents)
	vectorCounts, _ := s.VectorStore.CountByCollection()

	// Count total chunks per collection
	chunkCounts, _ := s.VectorStore.CountChunksByCollection()

	// Count total docs per collection
	docCounts := make(map[string]int)
	_ = s.DBView(func(tx *bolt.Tx) error {
		bDocs := tx.Bucket([]byte("docs"))
		if bDocs == nil {
			return nil
		}
		c := bDocs.Cursor()
		for k, _ := c.First(); k != nil; k, _ = c.Next() {
			parts := vec.SplitKey(k)
			if len(parts) >= 2 {
				docCounts[parts[1]]++
			}
		}
		return nil
	})

	collections := make(map[string]interface{})
	allColls := make(map[string]bool)
	for c := range docCounts {
		allColls[c] = true
	}
	for c := range vectorCounts {
		allColls[c] = true
	}

	for coll := range allColls {
		collInfo := map[string]interface{}{
			"total_documents":    docCounts[coll],
			"embedded_documents": vectorCounts[coll],
			"total_chunks":       chunkCounts[coll],
		}
		// Add quantization info if configured
		if s.CollectionManager != nil {
			if cfg, cfgOK := s.CollectionManager.Get(coll); cfgOK && cfg.Quantization != "" {
				collInfo["quantization"] = cfg.Quantization
				collInfo["diskOnlyVectors"] = cfg.DiskOnlyVectors
			} else {
				collInfo["quantization"] = "float32"
			}
		}
		collections[coll] = collInfo
	}

	resp["collections"] = collections
	resp["index_ready"] = s.VectorIndex.IsReady()

	// Chunk configuration
	resp["chunking"] = map[string]interface{}{
		"enabled":   envconf.String("MDDB_EMBEDDING_CHUNK_ENABLED", "true") == "true",
		"chunkSize": envconf.Int("MDDB_EMBEDDING_CHUNK_SIZE", 1500),
	}

	ok(w, resp)
}

// getDocIDsByMeta returns a set of doc IDs matching metadata filters.
func (s *Server) getDocIDsByMeta(collection string, filterMeta map[string][]string) map[string]bool {
	result := make(map[string]bool)

	_ = s.DBView(func(tx *bolt.Tx) error {
		bIdx := tx.Bucket([]byte("idxmeta"))
		if bIdx == nil {
			return nil
		}

		var sets [][]string
		for mk, mvals := range filterMeta {
			var ids []string
			for _, mv := range mvals {
				prefix := storage.MetaKeyPrefix(collection, mk, mv)
				c := bIdx.Cursor()
				for k, _ := c.Seek(prefix); k != nil && bytes.HasPrefix(k, prefix); k, _ = c.Next() {
					id := string(k[len(prefix):])
					ids = append(ids, id)
				}
			}
			ids = sliceutil.Unique(ids)
			sets = append(sets, ids)
		}

		for _, id := range intersect(sets...) {
			result[id] = true
		}
		return nil
	})

	return result
}
