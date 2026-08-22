package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"mddb/internal/envconf"
	"mddb/internal/fts"
	"mddb/internal/storage"
	vec "mddb/internal/vector"
	"strings"
	"time"

	bolt "go.etcd.io/bbolt"
)

// VectorSearch performs a vector similarity search via the direct client.
func (c *DirectClient) VectorSearch(ctx context.Context, req *MCPVectorSearchRequest) (*MCPVectorSearchResponse, error) {
	s := c.server

	if req.Collection == "" {
		return nil, errors.New("missing collection")
	}
	if req.Query == "" && len(req.QueryVector) == 0 {
		return nil, errors.New("either query or queryVector is required")
	}

	algo := req.Algorithm
	if algo == "" {
		algo = "flat"
	}
	// Auto-select quantized searcher if collection has quantization configured
	// (disk-only collections exist ONLY in the quantized index).
	if algo == "flat" && s.QuantizedVecIndex != nil && s.QuantizedVecIndex.HasCollection(req.Collection) {
		algo = "quantized"
	}
	searcher, ok := s.VectorSearchers[algo]
	if !ok {
		return nil, errors.New("unknown algorithm: " + algo)
	}
	if !searcher.IsReady() {
		searcher = s.VectorSearchers["flat"]
		algo = "flat"
	}
	if !searcher.IsReady() {
		return nil, errors.New("vector index is loading, please retry")
	}

	var queryVector []float32
	if len(req.QueryVector) > 0 {
		queryVector = req.QueryVector
	} else if s.Embedding != nil {
		embedCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		var err error
		queryVector, err = s.Embedding.Embed(embedCtx, req.Query)
		if err != nil {
			return nil, fmt.Errorf("failed to embed query: %w", err)
		}
	} else {
		return nil, errors.New("no embedding provider configured and no queryVector provided")
	}

	// RAG-001: request > collection profile > historical default.
	topK := c.server.ResolveTopK(req.Collection, req.TopK, 5)

	// Oversample for chunk deduplication (SRCH-005).
	searchTopK := OversampledTopK(topK, s.ResolveOversample(req.Collection, req.Oversample), 20)

	metric := vec.ResolveSimilarity(req.DistanceMetric)
	metricName := req.DistanceMetric
	if metricName == "" {
		metricName = "cosine"
	}

	var results []vec.VectorResult
	if len(req.FilterMeta) > 0 {
		allowedIDs := s.getDocIDsByMeta(req.Collection, req.FilterMeta)
		if len(allowedIDs) == 0 {
			resp := &MCPVectorSearchResponse{
				Results:        []MCPVectorSearchResult{},
				Algorithm:      algo,
				DistanceMetric: metricName,
				// Included even with no results: a field that appears only
				// sometimes is a field agents handle inconsistently.
				ResponsePrompt: s.ResponsePrompt(req.Collection, req.Query),
			}
			if s.Embedding != nil {
				resp.Model = s.Embedding.Model()
				resp.Dimensions = s.Embedding.Dimensions()
			}
			return resp, nil
		}
		results = searcher.SearchWithFilter(req.Collection, queryVector, searchTopK, req.Threshold, allowedIDs, metric)
	} else {
		results = searcher.Search(req.Collection, queryVector, searchTopK, req.Threshold, metric)
	}

	// Parent mode (default): dedupe chunks to their parent document.
	// Chunk/window modes keep chunk hits and return the matching passage.
	if !validRetrievalMode(req.RetrievalMode) {
		return nil, errors.New("unknown retrievalMode: " + req.RetrievalMode + ", available: parent, chunk, window")
	}
	// Disk-only collections: rescore quantized candidates from disk first.
	var diskVecs map[string][]float32
	if s.collectionDiskOnly(req.Collection) {
		results, diskVecs = s.rescoreFromDisk(req.Collection, queryVector, results, metric)
	}

	chunkMode := req.RetrievalMode == RetrievalModeChunk || req.RetrievalMode == RetrievalModeWindow
	if !chunkMode {
		results = vec.DeduplicateChunkResults(results)
	}
	if req.MMR {
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

	items := make([]MCPVectorSearchResult, 0, len(results))
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
			item := MCPVectorSearchResult{
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
			item.Document = docToMCPDocument(doc)
			items = append(items, item)
		}
		return nil
	})

	resp := &MCPVectorSearchResponse{
		Results:   items,
		Total:     len(items),
		Algorithm: algo,
		// RAG-002: the collection's formatting instruction travels with the
		// results, so an agent does not need a second call to learn how this
		// collection wants its answers shaped.
		ResponsePrompt: s.ResponsePrompt(req.Collection, req.Query),
	}
	if s.Embedding != nil {
		resp.Model = s.Embedding.Model()
		resp.Dimensions = s.Embedding.Dimensions()
	}

	return resp, nil
}

// VectorReindex rebuilds the vector index via the direct client.
func (c *DirectClient) VectorReindex(ctx context.Context, req *MCPVectorReindexRequest) (*MCPVectorReindexResponse, error) {
	s := c.server

	if req.Collection == "" {
		return nil, errors.New("missing collection")
	}
	if s.Embedding == nil {
		return nil, errors.New("no embedding provider configured")
	}

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
		cur := bDocs.Cursor()
		prefix := []byte("doc|" + req.Collection + "|")
		for k, v := cur.Seek(prefix); k != nil && bytes.HasPrefix(k, prefix); k, v = cur.Next() {
			d, err := loadDoc(v)
			if err != nil {
				continue
			}
			docs = append(docs, docEntry{ID: d.ID, ContentMD: d.ContentMD, Mode: ChunkModeFor(d)})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	embedded, skipped, failed := 0, 0, 0
	var errs []string

	for _, d := range docs {
		if d.ContentMD == "" {
			skipped++
			continue
		}

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
		chunkSize := envconf.Int("MDDB_EMBEDDING_CHUNK_SIZE", 1500)
		chunkEnabled := envconf.String("MDDB_EMBEDDING_CHUNK_ENABLED", "true") == "true"
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

		// Embed each chunk
		var chunkEmbeddings []vec.ChunkEmbedding
		chunkFailed := false
		for i, chunk := range chunks {
			embedCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
			vector, err := s.Embedding.Embed(embedCtx, chunk)
			cancel()
			if err != nil {
				failed++
				errs = append(errs, fmt.Sprintf("%s chunk %d: %s", d.ID, i, err.Error()))
				chunkFailed = true
				break
			}
			chunkEmbeddings = append(chunkEmbeddings, vec.ChunkEmbedding{ChunkIndex: i, Vector: vector})
		}
		if chunkFailed {
			continue
		}

		contentHash := vec.ContentHash(d.ContentMD)
		if err := s.VectorStore.PutChunks(req.Collection, d.ID, chunkEmbeddings, s.Embedding.Model(), contentHash); err != nil {
			failed++
			errs = append(errs, d.ID+": store: "+err.Error())
			continue
		}
		for _, ce := range chunkEmbeddings {
			chunkKey := fmt.Sprintf("%s#%d", d.ID, ce.ChunkIndex)
			for _, searcher := range s.VectorSearchers {
				searcher.Add(req.Collection, chunkKey, ce.Vector)
			}
		}
		s.VectorStore.CleanStaleChunks(req.Collection, d.ID, len(chunkEmbeddings), s.VectorIndex)
		embedded++
	}

	if embedded > 0 {
		if records, loadErr := s.VectorStore.LoadCollection(req.Collection); loadErr == nil {
			collVecs := make(map[string][]float32, len(records))
			for docID, rec := range records {
				collVecs[docID] = rec.Vector
			}
			for _, searcher := range s.VectorSearchers {
				if trainer, ok := searcher.(vec.Trainable); ok {
					go trainer.Train(req.Collection, collVecs)
				}
			}
		}
	}

	return &MCPVectorReindexResponse{
		Embedded: embedded,
		Skipped:  skipped,
		Failed:   failed,
		Errors:   errs,
	}, nil
}

// VectorStats returns vector index statistics via the direct client.
func (c *DirectClient) VectorStats(ctx context.Context) (*MCPVectorStatsResponse, error) {
	s := c.server
	resp := &MCPVectorStatsResponse{
		Enabled:     s.Embedding != nil,
		Collections: make(map[string]MCPVectorCollectionStats),
	}

	if s.Embedding != nil {
		resp.Provider = s.Embedding.Model()
		resp.Model = s.Embedding.Model()
		resp.Dimensions = s.Embedding.Dimensions()
	}

	vectorCounts, _ := s.VectorStore.CountByCollection()

	docCounts := make(map[string]int)
	_ = s.DBView(func(tx *bolt.Tx) error {
		bDocs := tx.Bucket([]byte("docs"))
		if bDocs == nil {
			return nil
		}
		cur := bDocs.Cursor()
		for k, _ := cur.First(); k != nil; k, _ = cur.Next() {
			parts := vec.SplitKey(k)
			if len(parts) >= 2 {
				docCounts[parts[1]]++
			}
		}
		return nil
	})

	allColls := make(map[string]bool)
	for col := range docCounts {
		allColls[col] = true
	}
	for col := range vectorCounts {
		allColls[col] = true
	}

	for coll := range allColls {
		resp.Collections[coll] = MCPVectorCollectionStats{
			TotalDocuments:    docCounts[coll],
			EmbeddedDocuments: vectorCounts[coll],
		}
	}

	return resp, nil
}

// ImportURL imports a document from a URL via the direct client.
func (c *DirectClient) ImportURL(ctx context.Context, req *MCPImportURLRequest) (*MCPDocument, error) {
	if req.Collection == "" || req.URL == "" || req.Lang == "" {
		return nil, errors.New("missing required fields: collection, url, lang")
	}

	key := req.Key
	if key == "" {
		key = deriveKeyFromURL(req.URL)
		if key == "" {
			return nil, errors.New("cannot derive key from URL; provide key explicitly")
		}
	}

	content, err := fetchURL(ctx, req.URL)
	if err != nil {
		return nil, err
	}

	fmMeta, body := parseFrontmatter(content)
	mergedMeta := fmMeta
	if mergedMeta == nil {
		mergedMeta = make(map[string][]string)
	}
	for k, v := range req.Meta {
		mergedMeta[k] = v
	}

	saved, _, err := c.server.addDocument(req.Collection, key, req.Lang, mergedMeta, body, req.TTL, true)
	if err != nil {
		return nil, err
	}

	doc := docToMCPDocument(saved)
	return &doc, nil
}

// SetTTL sets a time-to-live on a document via the direct client.
func (c *DirectClient) SetTTL(ctx context.Context, req *MCPSetTTLRequest) (*MCPDocument, error) {
	if req.Collection == "" || req.Key == "" || req.Lang == "" {
		return nil, errors.New("missing required fields")
	}

	docID := genID(req.Collection, req.Key, req.Lang)

	now := time.Now().Unix()
	var expiresAt int64
	if req.TTL > 0 {
		expiresAt = now + req.TTL
	}

	var updated storage.Doc
	err := c.server.DBUpdate(func(tx *bolt.Tx) error {
		bDocs := tx.Bucket([]byte("docs"))
		dk := storage.DocKey(req.Collection, docID)
		v := bDocs.Get(dk)
		if v == nil {
			return errors.New("document not found")
		}
		docPtr, err := loadDoc(v)
		if err != nil {
			return err
		}
		updated = *docPtr
		updated.ExpiresAt = expiresAt
		buf, err := marshalDoc(&updated)
		if err != nil {
			return err
		}
		return bDocs.Put(dk, buf)
	})
	if err != nil {
		return nil, err
	}

	if c.server.TTLManager != nil {
		if expiresAt > 0 {
			_ = c.server.TTLManager.Set(req.Collection, docID, expiresAt)
		} else {
			_ = c.server.TTLManager.Remove(req.Collection, docID)
		}
	}

	doc := docToMCPDocument(updated)
	return &doc, nil
}

// FTSSearch performs a full-text search via the direct client.
func (c *DirectClient) FTSSearch(ctx context.Context, req *MCPFTSSearchRequest) (*MCPFTSSearchResponse, error) {
	if req.Collection == "" || req.Query == "" {
		return nil, errors.New("missing required fields: collection, query")
	}
	if c.server.FTSIndex == nil {
		return nil, errors.New("full-text search not initialized")
	}

	// RAG-001: request > collection profile > historical default.
	limit := c.server.ResolveTopK(req.Collection, req.Limit, 50)
	algo := req.Algorithm
	if algo == "" {
		algo = "tfidf"
	}
	fuzzy := req.Fuzzy
	if fuzzy < 0 {
		fuzzy = 0
	}
	if fuzzy > 2 {
		fuzzy = 2
	}

	var results []fts.FTSResult
	var err error
	switch algo {
	case "bm25":
		if fuzzy > 0 {
			results, err = c.server.FTSIndex.SearchBM25Fuzzy(req.Collection, req.Query, limit, fuzzy)
		} else {
			results, err = c.server.FTSIndex.SearchBM25(req.Collection, req.Query, limit)
		}
	case "tfidf":
		if fuzzy > 0 {
			results, err = c.server.FTSIndex.SearchFuzzy(req.Collection, req.Query, limit, fuzzy)
		} else {
			results, err = c.server.FTSIndex.Search(req.Collection, req.Query, limit)
		}
	case "pmisparse":
		if fuzzy > 0 {
			results, err = c.server.FTSIndex.SearchPMISparseFuzzy(req.Collection, req.Query, limit, fuzzy)
		} else {
			results, err = c.server.FTSIndex.SearchPMISparse(req.Collection, req.Query, limit)
		}
	default:
		return nil, fmt.Errorf("unknown algorithm: %s, available: tfidf, bm25, pmisparse", algo)
	}
	if err != nil {
		return nil, err
	}

	// Apply per-query boosts/demotions (metadata-based score multipliers).
	results = c.server.applyBoostFTS(req.Collection, results, req.Boost)

	resp := &MCPFTSSearchResponse{
		Algorithm:      algo,
		Fuzzy:          fuzzy,
		Lang:           req.Lang,
		Results:        make([]MCPFTSResult, 0, len(results)),
		ResponsePrompt: c.server.ResponsePrompt(req.Collection, req.Query),
	}

	_ = c.server.DBView(func(tx *bolt.Tx) error {
		bDocs := tx.Bucket([]byte("docs"))
		for _, res := range results {
			v := bDocs.Get(storage.DocKey(req.Collection, res.DocID))
			if v == nil {
				continue
			}
			docPtr, err := loadDoc(v)
			if err != nil {
				continue
			}
			if docPtr.ExpiresAt > 0 && docPtr.ExpiresAt < time.Now().Unix() {
				continue
			}
			// CODE-002: fragments and their line ranges are extracted before
			// the body is dropped — the whole point is to answer without
			// carrying the document, so the body must go and the lines stay.
			var highlights []fts.Highlight
			if req.Highlight && docPtr.ContentMD != "" {
				highlights = fts.ExtractHighlights(docPtr.ContentMD, res.MatchedTerms, fts.HighlightOptions{
					OpenTag:      req.HighlightTag,
					MaxFragments: req.MaxHighlights,
					FragmentSize: req.FragmentSize,
				})
			}
			if !req.IncludeContent {
				docPtr.ContentMD = "" // GO-022: don't carry a body the caller discards
			}
			resp.Results = append(resp.Results, MCPFTSResult{
				Document:     docToMCPDocument(*docPtr),
				Score:        res.Score,
				MatchedTerms: res.MatchedTerms,
				Highlights:   highlights,
			})
		}
		return nil
	})
	resp.Total = len(resp.Results)

	return resp, nil
}

// FTSReindex re-indexes all documents in a collection using their lang field.
func (c *DirectClient) FTSReindex(ctx context.Context, req *MCPFTSReindexRequest) (*MCPFTSReindexResponse, error) {
	if req.Collection == "" {
		return nil, errors.New("missing required field: collection")
	}
	if c.server.FTSIndex == nil {
		return nil, errors.New("full-text search not initialized")
	}

	// Collect docs first (read tx), then index outside to avoid deadlock
	type reindexDoc struct {
		ID, ContentMD, Lang string
		Meta                map[string][]string
	}
	var docs []reindexDoc
	var skipped int
	_ = c.server.DBView(func(tx *bolt.Tx) error {
		bDocs := tx.Bucket([]byte("docs"))
		if bDocs == nil {
			return nil
		}
		prefix := []byte("doc|" + req.Collection + "|")
		cur := bDocs.Cursor()
		for k, v := cur.Seek(prefix); k != nil && bytes.HasPrefix(k, prefix); k, v = cur.Next() {
			docPtr, err := loadDoc(v)
			if err != nil || docPtr.ContentMD == "" {
				skipped++
				continue
			}
			if docPtr.ExpiresAt > 0 && docPtr.ExpiresAt < time.Now().Unix() {
				skipped++
				continue
			}
			docs = append(docs, reindexDoc{docPtr.ID, docPtr.ContentMD, docPtr.Lang, docPtr.Meta})
		}
		return nil
	})

	reindexed := 0
	for _, d := range docs {
		_ = c.server.FTSIndex.IndexWithLang(req.Collection, d.ID, d.ContentMD, d.Lang)
		_ = c.server.FTSIndex.IndexPositionsWithLang(req.Collection, d.ID, d.ContentMD, d.Lang)
		fields := map[string]string{"content": d.ContentMD}
		for mk, vals := range d.Meta {
			if len(vals) > 0 {
				fields["meta."+mk] = strings.Join(vals, " ")
			}
		}
		_ = c.server.FTSIndex.IndexFieldsWithLang(req.Collection, d.ID, fields, d.Lang)
		reindexed++
	}

	return &MCPFTSReindexResponse{
		Status:    "ok",
		Reindexed: reindexed,
		Skipped:   skipped,
	}, nil
}

// FTSLanguages returns the list of supported FTS languages.
func (c *DirectClient) FTSLanguages(ctx context.Context) (*MCPFTSLanguagesResponse, error) {
	if c.server.FTSIndex == nil || c.server.FTSIndex.LangRegistry() == nil {
		return &MCPFTSLanguagesResponse{Languages: []MCPFTSLanguageInfo{}}, nil
	}

	var langs []MCPFTSLanguageInfo
	for _, code := range c.server.FTSIndex.LangRegistry().Languages() {
		cfg := c.server.FTSIndex.LangRegistry().Resolve(code)
		name := code
		if cfg != nil {
			name = cfg.Name
		}
		langs = append(langs, MCPFTSLanguageInfo{Code: code, Name: name})
	}

	return &MCPFTSLanguagesResponse{
		Languages:   langs,
		DefaultLang: c.server.FTSIndex.LangRegistry().DefaultLang(),
	}, nil
}

// HybridSearch performs a combined full-text and vector search via the direct client.
func (c *DirectClient) HybridSearch(ctx context.Context, req *MCPHybridSearchRequest) (*MCPHybridSearchResponse, error) {
	if req.Collection == "" || req.Query == "" {
		return nil, errors.New("missing required fields: collection, query")
	}

	httpReq := HybridSearchRequest{
		Collection:      req.Collection,
		Query:           req.Query,
		TopK:            req.TopK,
		Algorithm:       req.Algorithm,
		VectorAlgorithm: req.VectorAlgorithm,
		Alpha:           req.Alpha,
		Strategy:        req.Strategy,
		RRFK:            req.RRFK,
		Fuzzy:           req.Fuzzy,
		Threshold:       req.Threshold,
		DistanceMetric:  req.DistanceMetric,
		FilterMeta:      req.FilterMeta,
		Boost:           req.Boost,
		Sort:            req.Sort,
		Oversample:      req.Oversample,
		IncludeContent:  true,
	}

	// Run FTS
	ftsResults, err := c.server.runFTSSearch(httpReq)
	if err != nil {
		return nil, err
	}

	// Run vector
	vectorResults, err := c.server.runVectorSearch(ctx, httpReq)
	if err != nil {
		return nil, err
	}

	// Defaults. RAG-001: request > collection profile > historical default.
	httpReq.TopK = c.server.ResolveTopK(httpReq.Collection, httpReq.TopK, 10)
	if httpReq.Strategy == "" {
		httpReq.Strategy = "alpha"
	}
	if httpReq.RRFK <= 0 {
		httpReq.RRFK = 60
	}
	if httpReq.Strategy == "alpha" && httpReq.Alpha == 0 {
		httpReq.Alpha = 0.5
	}

	var merged []HybridSearchResultItem
	switch httpReq.Strategy {
	case "rrf":
		merged = mergeRRF(ftsResults, vectorResults, httpReq.RRFK, httpReq.TopK)
	default:
		merged = mergeAlpha(ftsResults, vectorResults, httpReq.Alpha, httpReq.TopK)
	}

	// Apply per-query boosts/demotions (metadata-based score multipliers).
	merged = c.server.applyBoostHybrid(req.Collection, merged, req.Boost)
	if len(merged) > httpReq.TopK {
		merged = merged[:httpReq.TopK]
	}

	items := c.server.loadHybridDocs(req.Collection, merged, true)

	distMetric := httpReq.DistanceMetric
	if distMetric == "" {
		distMetric = "cosine"
	}
	resp := &MCPHybridSearchResponse{
		Strategy:        httpReq.Strategy,
		FTSAlgorithm:    httpReq.Algorithm,
		VectorAlgorithm: httpReq.VectorAlgorithm,
		DistanceMetric:  distMetric,
		Results:         make([]MCPHybridSearchResult, 0, len(items)),
		ResponsePrompt:  c.server.ResponsePrompt(httpReq.Collection, httpReq.Query),
	}
	for _, item := range items {
		resp.Results = append(resp.Results, MCPHybridSearchResult{
			Document:      docToMCPDocument(item.Document),
			CombinedScore: item.CombinedScore,
			FTSScore:      item.FTSScore,
			VectorScore:   item.VectorScore,
			MatchedTerms:  item.MatchedTerms,
			Rank:          item.Rank,
		})
	}
	resp.Total = len(resp.Results)

	return resp, nil
}
