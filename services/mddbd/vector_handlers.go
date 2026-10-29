package main

import (
	"context"
	"errors"
	"log/slog"
	"mddb/internal/embedding"
	"mddb/internal/envconf"
	json "mddb/internal/jsonx"
	"mddb/internal/storage"
	vec "mddb/internal/vector"
	"net/http"
	"strings"
	"time"

	bolt "go.etcd.io/bbolt"
)

// VectorSearchRequest represents an HTTP vector search request.
type VectorSearchRequest struct {
	Collection     string              `json:"collection"`
	Query          string              `json:"query"`
	QueryVector    []float32           `json:"queryVector"`
	TopK           int                 `json:"topK"`
	Threshold      float64             `json:"threshold"`
	FilterMeta     map[string][]string `json:"filterMeta"`
	Filter         MetaFilter          `json:"filter,omitempty"` // operator filter (SRCH-017): prefix, contains, gt/gte/lt/lte, not, exists; ANDed with filterMeta
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
	Oversample float64  `json:"oversample,omitempty"`
	GroupBy    *GroupBy `json:"groupBy,omitempty"` // keep perGroup results per metadata value, document or collection (SRCH-016)

	IndexWait      // readLevel, waitForIndex, waitForKeys (GO-042)
	PassageOptions // format, passageLines (AGT-002)
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
	// Group and GroupRank are set when the request had groupBy (SRCH-016):
	// the group this result counted against and its 1-based place in it.
	Group     string `json:"group,omitempty"`
	GroupRank int    `json:"groupRank,omitempty"`
	// PendingEmbedding is true for a document whose embedding is still queued:
	// found by its text, not yet by its meaning (GO-042).
	PendingEmbedding bool `json:"pendingEmbedding,omitempty"`
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
	// Indexing reports the wait when the request set waitForIndex (GO-042).
	Indexing *IndexWaitResult `json:"indexing,omitempty"`
}

// VectorReindexRequestHTTP represents a reindex request.
type VectorReindexRequestHTTP struct {
	Collection string `json:"collection"`
	Force      bool   `json:"force"`
	Async      bool   `json:"async"` // #347: return a job at once
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

	// #346: progress, smallest collections first, each searchable when done.
	load := newVectorLoadState(counts)
	s.vectorLoad.Store(load)
	defer load.finish()

	totalLoaded := 0
	for _, collection := range load.order() {
		count := counts[collection]
		load.begin(collection)
		records, err := s.VectorStore.LoadCollection(collection)
		if err != nil {
			slog.Error("failed to load vectors for collection", "collection", collection, "err", err)
			load.finishCollection(collection) // searchable, with what it has: nothing
			continue
		}

		// Collect vectors for trainable indexes
		collVecs := make(map[string][]float32, len(records))

		// Disk-only collections keep RAM quantized-only: full-precision
		// vectors are never loaded into the float32 searchers.
		diskOnly := s.collectionDiskOnly(collection)

		refused, sinceReport := 0, 0
		for docID, rec := range records {
			if sinceReport++; sinceReport == 1000 {
				load.add(sinceReport)
				sinceReport = 0
			}
			// docID may be "id" or "id#0", "id#1", etc.
			if err := s.indexChunk(collection, docID, rec.Vector, diskOnly); err != nil {
				// A store written by 2.15.0 or earlier can hold vectors a
				// provider returned empty (#252). Loading one used to poison
				// the whole graph — and map order is random, so whether it did
				// changed from one restart to the next. Refused now, and
				// counted so the operator is told to reindex.
				refused++
				continue
			}
			collVecs[docID] = rec.Vector
		}
		if refused > 0 {
			slog.Warn("stored vectors the index cannot hold were skipped; reindex the collection to replace them",
				"collection", collection, "skipped", refused, "loaded", len(records)-refused,
				"remedy", "POST /v1/vector-reindex")
		}
		if diskOnly {
			collVecs = nil // nothing to train float32 indexes with
		}

		// Trigger training for trainable indexes (IVF, PQ)
		for _, searcher := range s.VectorSearchers {
			if trainer, ok := searcher.(vec.Trainable); ok {
				vec.TrainSafely(trainer, collection, collVecs)
			}
		}

		load.add(sinceReport)
		load.finishCollection(collection)
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
	req.GroupBy = s.ResolveGroupBy(req.Collection, req.GroupBy)
	if err := req.GroupBy.Validate(); err != nil {
		unprocessable(w, err)
		return
	}
	if err := req.ValidateIndexWait(); err != nil {
		unprocessable(w, err)
		return
	}
	if err := req.GraphExpand.ValidateKinds(); err != nil {
		unprocessable(w, err)
		return
	}
	req.PassageOptions = s.ResolveFormat(req.Collection, req.PassageOptions)
	if err := req.ValidatePassageOptions(); err != nil {
		unprocessable(w, err)
		return
	}
	// Passages are cut from the document, so it is loaded whole and the body
	// dropped afterwards, when the passages are built (AGT-002).
	includeContent := req.IncludeContent || req.wantsPassages()
	// GO-042: wait for this collection's queued embeddings before searching.
	waited := s.waitForIndex(r.Context(), req.Collection, req.IndexWait)
	// RAG-008: request, then collection profile, then default — the same
	// precedence ResolveTopK applies. The resolved value is what gets
	// validated: an invalid explicit mode passes through Resolve unchanged and
	// is still caught, and so is a profile written by a version that knew a
	// mode this one does not.
	mode := s.ResolveRetrievalMode(req.Collection, req.RetrievalMode, RetrievalModeParent)
	if !validRetrievalMode(mode) {
		bad(w, errors.New("unknown retrievalMode: "+mode+", available: parent, chunk, window"))
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

	searcher, algo, err := s.readySearcher(req.Collection, algo)
	if err != nil {
		s.writeVectorUnavailable(w, err, "flat, hnsw, ivf, pq, quantized")
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
		queryVector, err = s.Embedding.Embed(ctx, req.Query, embedding.RoleQuery)
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
	if allowedIDs, active := s.allowedDocIDs(req.Collection, req.FilterMeta, req.Filter); active {
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

	chunkMode := mode == RetrievalModeChunk || mode == RetrievalModeWindow
	var bestChunk map[string]int
	if req.wantsPassages() {
		bestChunk = map[string]int{}
	}
	// Dedupe (parent mode), groupBy, MMR over the oversampled set, then topK.
	results, groupTags := s.shapeVectorResults(vectorShaping{
		Collection: req.Collection, ChunkMode: chunkMode, GroupBy: req.GroupBy,
		MMR: req.MMR, MMRLambda: req.MMRLambda, TopK: topK, DiskVecs: diskVecs, BestChunk: bestChunk,
	}, results)

	windowSize := 0
	if mode == RetrievalModeWindow {
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
	_ = s.DBViewC(req.Collection, func(tx *bolt.Tx) error {
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
			if t, ok := groupTags[vr.DocID]; ok {
				item.Group, item.GroupRank = t.Group, t.Rank
			}
			if chunkMode {
				idx := chunkIndex
				item.ChunkIndex = &idx
				item.ChunkText, item.StartLine, item.EndLine =
					chunkPassageWithLinesSize(doc.ContentMD, chunkIndex, windowSize,
						s.chunkModeFor(req.Collection, &doc), s.chunkSizeFor(req.Collection))
			}
			if !includeContent {
				doc.ContentMD = ""
			}
			item.Document = doc
			items = append(items, item)
		}
		return nil
	})

	// GO-042: documents still waiting for their embedding, found by text.
	if req.ReadLevel == ReadLevelIndexedPlusPending {
		for _, doc := range pendingDocsFor(s, req.Collection, req.Query, req.FilterMeta, req.Filter, topK, items, func(it VectorSearchResultItem) string { return it.Document.ID }) {
			if !includeContent {
				doc.ContentMD = ""
			}
			items = append(items, VectorSearchResultItem{Document: doc, Rank: len(items) + 1, PendingEmbedding: true})
		}
	}

	// RAG-001: cap the total context this collection hands back. In chunk and
	// window modes the passage is what the caller receives, so that is what
	// counts against the budget — charging the whole parent document would
	// make the cap meaningless for exactly the modes RAG uses.
	// With passages the budget counts the passages, below.
	var contextTruncated bool
	if !req.wantsPassages() {
		items, contextTruncated = applyContextBudget(s, req.Collection, items,
			func(it VectorSearchResultItem) int {
				if it.ChunkText != "" {
					return approxTokens(it.ChunkText)
				}
				return approxTokens(it.Document.ContentMD)
			})
	}

	// SRCH-006: reach documents the query matches on no term, by following the
	// edges the matching documents already have. Applied after the budget so
	// expansion cannot be trimmed away by a cap it was not counted against,
	// and before the response so its documents arrive with the rest.
	var expansions []GraphExpansion
	if mode == RetrievalModeGraph {
		expansions, items = s.appendGraphNeighbours(req.Collection, items, req.GraphExpand, includeContent)
	}

	if req.wantsPassages() {
		hits := make([]passageSource, 0, len(items))
		terms := queryTerms(req.Query)
		for _, it := range items {
			best, ok := bestChunk[it.Document.ID]
			if !ok || it.PendingEmbedding {
				best = -1
			}
			hits = append(hits, passageSource{doc: it.Document, score: float64(it.Score), chunkText: it.ChunkText,
				startLine: it.StartLine, endLine: it.EndLine, bestChunk: best, terms: terms})
		}
		ok(w, s.finishPassages(req.Collection, s.buildPassages(req.Collection, hits, req.PassageOptions, "vector"), waited))
		return
	}

	resp := VectorSearchResponseHTTP{
		Results:          items,
		Total:            len(items),
		Algorithm:        algo,
		DistanceMetric:   metricName,
		ContextTruncated: contextTruncated,
		GraphExpansions:  expansions,
		Indexing:         waited,
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
		bad(w, errNoEmbeddingProvider)
		return
	}
	// #347: a large reindex outlasts any sensible request; run it as a job.
	if req.Async {
		job, err := s.startReindexJob(req.Collection, req.Force)
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Location", "/v1/vector-reindex/jobs/"+job.ID)
		if errors.Is(err, errReindexRunning) {
			w.WriteHeader(http.StatusConflict)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": err.Error(), "job": job})
			return
		}
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(job)
		return
	}
	// Synchronous, as before: the write deadline is lifted, as for add-batch
	// and backup, so a long reindex still delivers its result.
	liftWriteDeadline(w, r)
	res, err := s.reindexVectors(r.Context(), req.Collection, req.Force, nil)
	if err != nil {
		bad(w, err)
		return
	}
	ok(w, map[string]interface{}{
		"embedded":    res.Embedded,
		"skipped":     res.Skipped,
		"failed":      res.Failed,
		"totalChunks": res.TotalChunks,
		"errors":      res.Errors,
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

	// #349: the counts come from the shared snapshot; this walked the
	// documents and every vector on each call, and never returned on a
	// large database.
	docCounts, vectorCounts, chunkCounts := map[string]int{}, map[string]int{}, map[string]int{}
	if snap := s.dbStats(statsWait()); snap == nil {
		resp["statsPending"] = true
	} else {
		resp["statsComputedAt"] = snap.ComputedAt.UTC().Format(time.RFC3339)
		resp["statsAgeSeconds"] = int(time.Since(snap.ComputedAt).Seconds())
		for name, cc := range snap.Collections {
			docCounts[name], vectorCounts[name], chunkCounts[name] = cc.Documents, cc.Embeddings, cc.Chunks
		}
	}

	// A tenant user sees only the collections of its namespace, as in
	// /v1/stats (MULTI_TENANCY.md, "Scoped listings").
	tenant := TenantFromContext(r.Context())
	collections := make(map[string]interface{})
	allColls := make(map[string]bool)
	for c, n := range docCounts {
		if (n > 0 || vectorCounts[c] > 0) && CollectionInTenant(tenant, c) {
			allColls[c] = true
		}
	}

	for coll := range allColls {
		collInfo := map[string]interface{}{
			"total_documents":    docCounts[coll],
			"embedded_documents": vectorCounts[coll],
			"total_chunks":       chunkCounts[coll],
			"chunkSize":          s.chunkSizeFor(coll), // effective, after the collection's override (#325)
		}
		if c := s.collectionChunking(coll); c != nil && c.ChunkMode != "" {
			collInfo["chunkMode"] = c.ChunkMode
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
	// #346: while loading, how far it is and which collections already answer.
	if l := s.vectorLoad.Load(); l != nil {
		if p := l.progress(); p != nil {
			resp["indexProgress"] = p
		}
	}

	// Chunk configuration
	resp["chunking"] = map[string]interface{}{
		"enabled":   envconf.String("MDDB_EMBEDDING_CHUNK_ENABLED", "true") == "true",
		"chunkSize": envconf.Int("MDDB_EMBEDDING_CHUNK_SIZE", 1500),
	}

	// Queue state (#232). `dropped` is the number that turns "embedded is
	// lower than total" from a question into an answer: either the queue is
	// still draining (depth > 0) or it gave up on that many documents, and
	// before this the two looked identical from outside.
	if s.EmbeddingWorker != nil {
		size, depth, dropped, wait := s.EmbeddingWorker.QueueStats()
		resp["queue"] = map[string]interface{}{
			"size":    size,
			"depth":   depth,
			"dropped": dropped,
			"wait":    wait.String(),
		}
	}

	ok(w, resp)
}

// getDocIDsByMeta returns the IDs of documents matching an equality filter.
// It is the filterMeta-only form of allowedDocIDs, kept for the surfaces that
// take nothing else (geo, gRPC).
func (s *Server) getDocIDsByMeta(collection string, filterMeta map[string][]string) map[string]bool {
	ids, active := s.allowedDocIDs(collection, filterMeta, nil)
	if !active {
		return map[string]bool{}
	}
	return ids
}
