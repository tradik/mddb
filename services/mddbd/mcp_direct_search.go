package main

import (
	"context"
	"errors"
	"fmt"
	"mddb/internal/embedding"
	"mddb/internal/storage"
	vec "mddb/internal/vector"
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
	searcher, algo, err := s.readySearcher(req.Collection, algo)
	if err != nil {
		return nil, err
	}

	var queryVector []float32
	if len(req.QueryVector) > 0 {
		queryVector = req.QueryVector
	} else if s.Embedding != nil {
		embedCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		var err error
		queryVector, err = s.Embedding.Embed(embedCtx, req.Query, embedding.RoleQuery)
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
	// RAG-008: the documented precedence is "request, then collection profile,
	// then default" — the same rule ResolveTopK applies above. Reading
	// req.RetrievalMode directly skipped the middle term, so a collection that
	// asked for chunk retrieval silently got whole documents.
	//
	// The resolved value is what gets validated, not the requested one: an
	// invalid explicit mode still passes through Resolve unchanged and is
	// caught here, and a profile written by a version that knew a mode this
	// one does not is caught too.
	mode := s.ResolveRetrievalMode(req.Collection, req.RetrievalMode, RetrievalModeParent)
	if !validRetrievalMode(mode) {
		return nil, errors.New("unknown retrievalMode: " + mode + ", available: parent, chunk, window")
	}
	// Disk-only collections: rescore quantized candidates from disk first.
	var diskVecs map[string][]float32
	if s.collectionDiskOnly(req.Collection) {
		results, diskVecs = s.rescoreFromDisk(req.Collection, queryVector, results, metric)
	}

	chunkMode := mode == RetrievalModeChunk || mode == RetrievalModeWindow
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
	if mode == RetrievalModeWindow {
		windowSize = req.WindowSize
		if windowSize <= 0 {
			windowSize = 1
		}
	}

	items := make([]MCPVectorSearchResult, 0, len(results))
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
			item := MCPVectorSearchResult{
				Score: vr.Score,
				Rank:  rank + 1,
			}
			if chunkMode {
				idx := chunkIndex
				item.ChunkIndex = &idx
				item.ChunkText, item.StartLine, item.EndLine =
					chunkPassageWithLinesSize(doc.ContentMD, chunkIndex, windowSize,
						c.server.chunkModeFor(req.Collection, &doc), c.server.chunkSizeFor(req.Collection))
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

	// RAG-001: cap what this collection hands back. The MCP paths resolved
	// topK from the profile and returned its responsePrompt, and then skipped
	// the budget — so the caller the cap exists for, an agent assembling a
	// prompt, was the one path that never had it applied.
	resp.Results, resp.ContextTruncated = applyContextBudget(s, req.Collection, resp.Results,
		func(r MCPVectorSearchResult) int { return approxTokens(r.Document.ContentMD) })
	resp.Total = len(resp.Results)

	return resp, nil
}

// VectorReindex rebuilds the vector index via the direct client.
func (c *DirectClient) VectorReindex(ctx context.Context, req *MCPVectorReindexRequest) (*MCPVectorReindexResponse, error) {
	// #347: the shared implementation; this copy ignored quantization.
	res, err := c.server.reindexVectors(ctx, req.Collection, req.Force, nil)
	if err != nil {
		return nil, err
	}
	return &MCPVectorReindexResponse{
		Embedded: res.Embedded,
		Skipped:  res.Skipped,
		Failed:   res.Failed,
		Errors:   res.Errors,
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
	_ = s.DBViewAll(func(tx *bolt.Tx) error {
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
	err := c.server.DBUpdateC(req.Collection, func(tx *bolt.Tx) error {
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
	// One implementation with POST /v1/fts (#328): filters, modes, BM25F,
	// stemming and synonym control, curation and highlights come with it.
	full, err := c.server.searchFTS(FTSSearchRequest{
		Collection:        req.Collection,
		Query:             req.Query,
		Limit:             req.Limit,
		Algorithm:         req.Algorithm,
		Fuzzy:             req.Fuzzy,
		Lang:              req.Lang,
		Boost:             req.Boost,
		Highlight:         req.Highlight,
		HighlightTag:      req.HighlightTag,
		MaxHighlights:     req.MaxHighlights,
		FragmentSize:      req.FragmentSize,
		FilterMeta:        req.FilterMeta,
		DisableStem:       req.DisableStem,
		DisableSynonyms:   req.DisableSynonyms,
		Mode:              req.Mode,
		Distance:          req.Distance,
		FacetBy:           req.FacetBy,
		FacetMaxValues:    req.FacetMaxValues,
		skipContextBudget: true,
	}, time.Now())
	if err != nil {
		return nil, err
	}

	resp := &MCPFTSSearchResponse{
		Algorithm:      full.Algorithm,
		Fuzzy:          full.Fuzzy,
		Lang:           req.Lang,
		Results:        make([]MCPFTSResult, 0, len(full.Results)),
		Facets:         full.Facets,
		ResponsePrompt: c.server.ResponsePrompt(req.Collection, req.Query),
	}
	for _, r := range full.Results {
		doc := r.Document
		if !req.IncludeContent {
			doc.ContentMD = "" // GO-022: don't carry a body the caller discards
		}
		resp.Results = append(resp.Results, MCPFTSResult{
			Document:     docToMCPDocument(doc),
			Score:        r.Score,
			MatchedTerms: r.MatchedTerms,
			Highlights:   r.Highlights,
		})
	}
	// RAG-001: the budget counts what this answer carries, so it applies
	// after bodies the agent did not ask for have been dropped.
	resp.Results, resp.ContextTruncated = applyContextBudget(c.server, req.Collection, resp.Results,
		func(r MCPFTSResult) int { return approxTokens(r.Document.ContentMD) })

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

	res, err := c.server.reindexCollectionFTS(req.Collection)
	if err != nil {
		return nil, err
	}
	return &MCPFTSReindexResponse{
		Status:    res.Status,
		Reindexed: res.Reindexed,
		Skipped:   res.Skipped + res.Failed,
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
		Alpha:           floatPtr(req.Alpha),
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
	// SRCH-007: the MCP request carries a plain float, so this path cannot
	// distinguish an omitted alpha from an explicit zero either. Left as it
	// was rather than half-fixed — the HTTP surface now honours a zero, and
	// bringing MCP with it means a shape change in the tool schema.
	if httpReq.Strategy == "alpha" && httpReq.Alpha != nil && *httpReq.Alpha == 0 {
		httpReq.Alpha = floatPtr(0.5)
	}

	var merged []HybridSearchResultItem
	switch httpReq.Strategy {
	case "rrf":
		merged = mergeRRF(ftsResults, vectorResults, httpReq.RRFK, httpReq.TopK)
	default:
		merged = mergeAlpha(ftsResults, vectorResults, alphaOrDefault(httpReq.Alpha), httpReq.TopK)
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
	// RAG-001: cap what this collection hands back. The MCP paths resolved
	// topK from the profile and returned its responsePrompt, and then skipped
	// the budget — so the caller the cap exists for, an agent assembling a
	// prompt, was the one path that never had it applied.
	resp.Results, resp.ContextTruncated = applyContextBudget(c.server, req.Collection, resp.Results,
		func(r MCPHybridSearchResult) int { return approxTokens(r.Document.ContentMD) })

	resp.Total = len(resp.Results)

	return resp, nil
}

// SearchAdvisor measures a collection and recommends how to search it
// (SRCH-010).
func (c *DirectClient) SearchAdvisor(ctx context.Context, collection string) (*SearchRecommendation, error) {
	if collection == "" {
		return nil, errors.New("missing required field: collection")
	}
	return c.server.RecommendSearch(collection)
}
