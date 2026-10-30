package main

import (
	"context"
	"errors"
	"fmt"
	"mddb/internal/storage"
	vec "mddb/internal/vector"
	"time"

	bolt "go.etcd.io/bbolt"
)

// VectorSearch performs a vector similarity search via the direct client.
// MCP, gRPC and GraphQL all run it; every answer it gives, passages and image
// queries included, reports a limit the server's cap lowered (#368).
func (c *DirectClient) VectorSearch(ctx context.Context, req *MCPVectorSearchRequest) (*MCPVectorSearchResponse, error) {
	resp, err := c.vectorSearch(ctx, req)
	if resp != nil {
		resp.LimitReport = req.limitReport
		if resp.Passages != nil {
			resp.Passages.LimitReport = req.limitReport
		}
	}
	return resp, err
}

// vectorSearch is VectorSearch before the limit report is attached.
func (c *DirectClient) vectorSearch(ctx context.Context, req *MCPVectorSearchRequest) (*MCPVectorSearchResponse, error) {
	s := c.server
	req.GroupBy = s.ResolveGroupBy(req.Collection, req.GroupBy)
	if err := req.GroupBy.Validate(); err != nil {
		return nil, err
	}
	if err := req.ValidateIndexWait(); err != nil {
		return nil, err
	}
	req.PassageOptions = s.ResolveFormat(req.Collection, req.PassageOptions)
	if err := req.ValidatePassageOptions(); err != nil {
		return nil, err
	}
	if err := req.ValidatePlanOptions(); err != nil {
		return nil, err
	}
	if err := req.ValidateFeedback(); err != nil {
		return nil, err
	}
	if err := req.ValidateDecomposition(); err != nil {
		return nil, err
	}
	includeContent := req.IncludeContent || req.wantsPassages()
	waited := s.waitForIndex(ctx, req.Collection, req.IndexWait)

	if req.Collection == "" {
		return nil, errors.New("missing collection")
	}
	if req.Query == "" && len(req.QueryVector) == 0 && req.QueryImage == "" {
		return nil, errors.New("either query or queryVector is required")
	}

	// RAG-001: request > collection profile > historical default, under the
	// server's cap (#368).
	topK, limitReport := c.server.ResolveTopK(req.Collection, req.TopK, 5)
	req.limitReport = limitReport
	// Oversample for chunk deduplication (SRCH-005).
	searchTopK := OversampledTopK(topK, s.ResolveOversample(req.Collection, req.Oversample), 20)
	allowedIDs, filtered := s.allowedDocIDs(req.Collection, req.FilterMeta, req.Filter)
	if req.QueryImage != "" { // RAG-010: search the image vectors with an image
		return s.mcpImageQuery(ctx, req, topK, allowedIDs, filtered, includeContent, waited)
	}

	// SRCH-013: the named algorithm, the profile's, flat — or the planner's
	// choice for "auto". Disk-only collections exist ONLY in the quantized
	// index, which a flat request is routed to.
	ps, err := s.planSearch(planRequest{Collection: req.Collection, Algorithm: s.ResolveVectorAlgorithm(req.Collection, req.Algorithm),
		QuantizedDefault: true, Filtered: filtered, Allowed: allowedIDs, SearchTopK: searchTopK,
		Dimensions: len(req.QueryVector), LatencyTargetMs: req.LatencyTargetMs})
	if err != nil {
		return nil, err
	}
	algo := ps.Algorithm

	var queryVector []float32
	if len(req.QueryVector) > 0 {
		queryVector = req.QueryVector
	} else if s.Embedding != nil {
		var err error
		queryVector, err = s.embedQuery(ctx, req.Query)
		if err != nil {
			return nil, fmt.Errorf("%w: %w", errEmbedQuery, err)
		}
	} else {
		return nil, fmt.Errorf("%w and no queryVector provided", errNoEmbeddingProvider)
	}
	// SRCH-015: the examples as vectors; query mode corrects the query here.
	fb, err := s.resolveFeedback(req.FeedbackOptions, []string{req.Collection}, len(queryVector))
	if err != nil {
		return nil, err
	}
	queryVector = fb.query(queryVector)
	ps.Plan.Feedback = fb.report()
	// SRCH-020: with queryDecomposition auto, one more vector per part.
	vectors, err := s.queryVectors(ctx, req.DecompositionOptions, textQuery(req.Query, req.QueryVector), queryVector, fb, ps.Plan)
	if err != nil {
		return nil, err
	}

	metric := vec.ResolveSimilarity(req.DistanceMetric)
	metricName := req.DistanceMetric
	if metricName == "" {
		metricName = "cosine"
	}

	if filtered && len(allowedIDs) == 0 {
		resp := &MCPVectorSearchResponse{
			Results:        []MCPVectorSearchResult{},
			Algorithm:      algo,
			DistanceMetric: metricName,
			// Included even with no results: a field that appears only
			// sometimes is a field agents handle inconsistently.
			ResponsePrompt: s.ResponsePrompt(req.Collection, req.Query),
			Plan:           req.explained(ps.Plan),
		}
		if s.Embedding != nil {
			resp.Model = s.Embedding.Model()
			resp.Dimensions = s.Embedding.Dimensions()
		}
		return resp, nil
	}
	vsp := s.Tracer.Stage(ctx, spanSearchVector) // OPS-024
	vsp.SetString("mddb.vector.algorithm", algo)
	// Disk-only collections: each query vector's quantized candidates are
	// rescored from disk before a decomposed query's lists are fused.
	results, diskVecs := s.searchVectorPath(ps, req.Collection, vectors, searchTopK, req.Threshold, allowedIDs, filtered, metric)
	vsp.SetInt("mddb.results", len(results))
	vsp.End()

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
	results = s.rescoreFeedback(fb, req.Collection, results, metric) // SRCH-015, rescore mode
	results = s.applySummaries(summarySearch{Collection: req.Collection, Mode: s.ResolveSummaryMode(req.Collection, req.summaryMode),
		Query: queryVector, SearchTopK: searchTopK, TopK: topK, Threshold: req.Threshold, Allowed: allowedIDs,
		Filtered: filtered, Metric: metric}, results, ps.Plan) // RAG-009

	chunkMode := mode == RetrievalModeChunk || mode == RetrievalModeWindow
	var bestChunk map[string]int
	if req.wantsPassages() {
		bestChunk = map[string]int{}
	}
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

	if !req.noGain {
		s.Gain.Search(gainKind{Type: SearchTypeVector, Algorithm: algo}) // AGT-004
	}
	items := make([]MCPVectorSearchResult, 0, len(results))
	load := s.Tracer.Stage(ctx, spanSearchLoadContent)
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
			if t, ok := groupTags[vr.DocID]; ok {
				item.Group, item.GroupRank = t.Group, t.Rank
			}
			if chunkMode {
				idx := chunkIndex
				item.ChunkIndex = &idx
				item.ChunkText, item.StartLine, item.EndLine =
					chunkPassageWithLinesSize(doc.ContentMD, chunkIndex, windowSize,
						c.server.chunkModeFor(req.Collection, &doc), c.server.chunkSizeFor(req.Collection))
				item.docTokens = approxTokens(doc.ContentMD)
			}
			if !includeContent {
				doc.ContentMD = ""
			}
			item.Document = docToMCPDocument(doc)
			items = append(items, item)
		}
		return nil
	})
	load.SetInt("mddb.documents", len(items))
	load.End()

	if req.ReadLevel == ReadLevelIndexedPlusPending {
		for _, doc := range pendingDocsFor(s, req.Collection, req.Query, req.FilterMeta, req.Filter, topK, items,
			func(it MCPVectorSearchResult) string { return it.Document.ID }) {
			if !includeContent {
				doc.ContentMD = ""
			}
			items = append(items, MCPVectorSearchResult{Document: docToMCPDocument(doc), Rank: len(items) + 1, PendingEmbedding: true})
		}
	}

	// RAG-010: each hit's images, and the images the query matches.
	var imageHits []ImageRef
	if req.IncludeImages {
		imageHits = s.imageHitsFor(ctx, req.Collection, req.Query, topK, req.Threshold, allowedIDs, filtered, ps.Plan)
		attachImages(s, items, func(it *MCPVectorSearchResult) imageOwner { return mcpDocImages(req.Collection, &it.Document) },
			func(it *MCPVectorSearchResult, refs []ImageRef) { it.Images = refs })
	}

	if req.wantsPassages() {
		hits := make([]passageSource, 0, len(items))
		terms := queryTerms(req.Query)
		for _, it := range items {
			best, ok := bestChunk[it.Document.ID]
			if !ok || it.PendingEmbedding {
				best = -1
			}
			hits = append(hits, passageSource{doc: mcpDocumentToDoc(it.Document), score: float64(it.Score),
				chunkText: it.ChunkText, startLine: it.StartLine, endLine: it.EndLine, bestChunk: best, terms: terms,
				chunks: ps.Plan.expandedChunks(it.Document.ID)})
		}
		p := s.finishPassages(req.Collection, s.buildPassages(req.Collection, hits, req.PassageOptions, "vector"), waited)
		s.passageImages(&p, req.IncludeImages, imageHits)
		return &MCPVectorSearchResponse{Passages: &p, Algorithm: algo}, nil
	}

	resp := &MCPVectorSearchResponse{
		Results:   items,
		Total:     len(items),
		Algorithm: algo,
		Indexing:  waited,
		Plan:      req.explained(ps.Plan),
		ImageHits: imageHits,
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
	if !req.noGain {
		countHits(s, resp.Results, func(r MCPVectorSearchResult) gainSize {
			return chunkHit(r.ChunkText, r.Document.ContentMD, r.docTokens)
		})
	}

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

	// #349: counts from the shared snapshot, not a walk per call.
	docCounts, vectorCounts := map[string]int{}, map[string]int{}
	if snap := s.dbStats(statsWait()); snap != nil {
		for name, cc := range snap.Collections {
			if cc.Documents > 0 || cc.Embeddings > 0 {
				docCounts[name], vectorCounts[name] = cc.Documents, cc.Embeddings
			}
		}
	}

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
	updated, err := c.server.setDocumentTTL(req.Collection, req.Key, req.Lang, req.TTL)
	if err != nil {
		return nil, err
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
		Filter:            req.Filter,
		GroupBy:           req.GroupBy,
		DisableStem:       req.DisableStem,
		DisableSynonyms:   req.DisableSynonyms,
		Mode:              req.Mode,
		Distance:          req.Distance,
		FacetBy:           req.FacetBy,
		FacetMaxValues:    req.FacetMaxValues,
		PassageOptions:    req.PassageOptions,
		BM25K1:            req.BM25K1,
		BM25B:             req.BM25B,
		ImageOptions:      req.ImageOptions,
		skipContextBudget: true,
	}, time.Now())
	if err != nil {
		return nil, err
	}

	c.server.Gain.Search(gainKind{Type: SearchTypeFTS, Algorithm: full.Algorithm}) // AGT-004
	if full.passages != nil {
		return &MCPFTSSearchResponse{Passages: full.passages, Algorithm: full.Algorithm, LimitReport: full.LimitReport}, nil
	}

	resp := &MCPFTSSearchResponse{
		Algorithm:      full.Algorithm,
		Fuzzy:          full.Fuzzy,
		Lang:           req.Lang,
		Results:        make([]MCPFTSResult, 0, len(full.Results)),
		Facets:         full.Facets,
		ResponsePrompt: c.server.ResponsePrompt(req.Collection, req.Query),
		LimitReport:    full.LimitReport,
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
			Group:        r.Group,
			GroupRank:    r.GroupRank,
			Images:       r.Images,
		})
	}
	// RAG-001: the budget counts what this answer carries, so it applies
	// after bodies the agent did not ask for have been dropped.
	resp.Results, resp.ContextTruncated = applyContextBudget(c.server, req.Collection, resp.Results,
		func(r MCPFTSResult) int { return approxTokens(r.Document.ContentMD) })
	countHits(c.server, resp.Results, func(r MCPFTSResult) gainSize { return wholeDoc(r.Document.ContentMD) })

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
	req.GroupBy = c.server.ResolveGroupBy(req.Collection, req.GroupBy)
	if err := req.GroupBy.Validate(); err != nil {
		return nil, err
	}
	if err := req.ValidateIndexWait(); err != nil {
		return nil, err
	}
	req.PassageOptions = c.server.ResolveFormat(req.Collection, req.PassageOptions)
	if err := req.ValidatePassageOptions(); err != nil {
		return nil, err
	}
	if err := req.ValidatePlanOptions(); err != nil {
		return nil, err
	}
	if err := req.ValidateFeedback(); err != nil {
		return nil, err
	}
	if err := req.ValidateDecomposition(); err != nil {
		return nil, err
	}
	waited := c.server.waitForIndex(ctx, req.Collection, req.IndexWait)

	httpReq := HybridSearchRequest{
		Collection:           req.Collection,
		Query:                req.Query,
		TopK:                 req.TopK,
		Algorithm:            req.Algorithm,
		VectorAlgorithm:      req.VectorAlgorithm,
		Alpha:                floatPtr(req.Alpha),
		Strategy:             req.Strategy,
		RRFK:                 req.RRFK,
		Fuzzy:                req.Fuzzy,
		Threshold:            req.Threshold,
		DistanceMetric:       req.DistanceMetric,
		FilterMeta:           req.FilterMeta,
		Filter:               req.Filter,
		GroupBy:              req.GroupBy,
		Boost:                req.Boost,
		Sort:                 req.Sort,
		Oversample:           req.Oversample,
		FieldWeights:         req.FieldWeights,
		Lang:                 req.Lang,
		BM25K1:               req.BM25K1,
		BM25B:                req.BM25B,
		IncludeContent:       true,
		PlanOptions:          req.PlanOptions,
		FeedbackOptions:      req.FeedbackOptions,
		DecompositionOptions: req.DecompositionOptions,
		ImageOptions:         req.ImageOptions,
	}

	items, httpReq, err := c.server.hybridItems(ctx, httpReq)
	if err != nil {
		return nil, err
	}
	c.server.Gain.Search(gainKind{Type: SearchTypeHybrid, Algorithm: httpReq.VectorAlgorithm, Fusion: httpReq.Strategy}) // AGT-004
	imageHits := c.server.hybridImages(ctx, httpReq, items)                                                              // RAG-010
	if req.wantsPassages() {
		p := c.server.hybridPassages(req.Collection, req.Query, items, req.PassageOptions, waited, httpReq.plan)
		c.server.passageImages(&p, httpReq.IncludeImages, imageHits)
		p.LimitReport = httpReq.limitReport
		return &MCPHybridSearchResponse{Passages: &p, Strategy: httpReq.Strategy, LimitReport: httpReq.limitReport}, nil
	}

	distMetric := httpReq.DistanceMetric
	if distMetric == "" {
		distMetric = "cosine"
	}
	resp := &MCPHybridSearchResponse{
		Strategy:        httpReq.Strategy,
		FTSAlgorithm:    httpReq.Algorithm,
		VectorAlgorithm: ranVectorAlgorithm(httpReq.VectorAlgorithm, httpReq.plan),
		DistanceMetric:  distMetric,
		Results:         make([]MCPHybridSearchResult, 0, len(items)),
		ResponsePrompt:  c.server.ResponsePrompt(httpReq.Collection, httpReq.Query),
		Indexing:        waited,
		Plan:            req.explained(httpReq.plan),
		ImageHits:       imageHits,
		LimitReport:     httpReq.limitReport,
	}
	for _, item := range items {
		resp.Results = append(resp.Results, MCPHybridSearchResult{
			Document:      docToMCPDocument(item.Document),
			CombinedScore: item.CombinedScore,
			FTSScore:      item.FTSScore,
			VectorScore:   item.VectorScore,
			MatchedTerms:  item.MatchedTerms,
			Rank:          item.Rank,
			Group:         item.Group,
			GroupRank:     item.GroupRank,
			Pinned:        item.Pinned,
			// The full-text half already finds documents still waiting
			// for their embedding; say which ones they are (GO-042).
			PendingEmbedding: c.server.pendingEmbedding(req.Collection, item.Document.ID),
			Images:           item.Images,
		})
	}
	// RAG-001: cap what this collection hands back. The MCP paths resolved
	// topK from the profile and returned its responsePrompt, and then skipped
	// the budget — so the caller the cap exists for, an agent assembling a
	// prompt, was the one path that never had it applied.
	resp.Results, resp.ContextTruncated = applyContextBudget(c.server, req.Collection, resp.Results,
		func(r MCPHybridSearchResult) int { return approxTokens(r.Document.ContentMD) })
	countHits(c.server, resp.Results, func(r MCPHybridSearchResult) gainSize { return wholeDoc(r.Document.ContentMD) })

	resp.Total = len(resp.Results)

	return resp, nil
}

// ExpandTargets resolves "role:<role>" selectors to collection names (AGT-003).
func (c *DirectClient) ExpandTargets(_ context.Context, targets []string) []string {
	return c.server.expandTargets(targets)
}

// CodeSymbol looks a symbol up (CODE-008).
func (c *DirectClient) CodeSymbol(ctx context.Context, req *SymbolRequest) (*SymbolResult, error) {
	return c.server.LookupSymbol(*req)
}

// IndexingStatus reports queued indexing work for a collection, or for the
// whole server when collection is "" (GO-042).
func (c *DirectClient) IndexingStatus(ctx context.Context, collection string) (*IndexingStatus, error) {
	st := c.server.IndexingStatus(collection)
	return &st, nil
}

// SearchAdvisor measures a collection and recommends how to search it
// (SRCH-010).
func (c *DirectClient) SearchAdvisor(ctx context.Context, collection string) (*SearchRecommendation, error) {
	if collection == "" {
		return nil, errors.New("missing required field: collection")
	}
	return c.server.RecommendSearch(collection)
}

// hybridItems is hybrid search over one collection as MCP hybrid_search and
// cross-collection hybrid search (AGT-003) run it: full-text and vector
// halves, fused, boosted, loaded and grouped. It returns the request with its
// defaults resolved, for the response to report.
func (s *Server) hybridItems(ctx context.Context, httpReq HybridSearchRequest) ([]HybridSearchResultItem, HybridSearchRequest, error) {
	// #368: an explicit topK is capped before either half runs, so neither
	// fetches candidates for a limit the answer cannot have. An absent one
	// stays absent here, as both halves expect, and is resolved below.
	if httpReq.TopK > 0 {
		httpReq.TopK, httpReq.limitReport = s.ResolveTopK(httpReq.Collection, httpReq.TopK, 0)
	}
	// Run FTS
	ftsResults, err := s.tracedFTS(ctx, httpReq)
	if err != nil {
		return nil, httpReq, err
	}

	// Run vector. SRCH-013: request, then collection profile, then flat.
	httpReq.VectorAlgorithm = s.ResolveVectorAlgorithm(httpReq.Collection, httpReq.VectorAlgorithm)
	vectorResults, plan, err := s.vectorHalf(ctx, httpReq)
	if err != nil {
		return nil, httpReq, err
	}
	httpReq.plan = plan

	// Defaults. RAG-001: request > collection profile > historical default.
	// An explicit topK is already under the cap, so only a resolved default
	// can be capped here; resolving again keeps the first report.
	var rep LimitReport
	if httpReq.TopK, rep = s.ResolveTopK(httpReq.Collection, httpReq.TopK, 10); rep.LimitCapped {
		httpReq.limitReport = rep
	}
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

	mergeTopK := httpReq.TopK
	if httpReq.GroupBy != nil {
		mergeTopK = groupedCandidates(httpReq.TopK)
	}
	var merged []HybridSearchResultItem
	fuse := s.Tracer.Stage(ctx, spanSearchFuse) // OPS-024
	fuse.SetString("mddb.fusion.strategy", httpReq.Strategy)
	switch httpReq.Strategy {
	case "rrf":
		merged = mergeRRF(ftsResults, vectorResults, httpReq.RRFK, mergeTopK)
	default:
		merged = mergeAlpha(ftsResults, vectorResults, alphaOrDefault(httpReq.Alpha), mergeTopK)
	}

	// Apply per-query boosts/demotions (metadata-based score multipliers).
	// Boosting reorders; it never adds, so merged is still at most mergeTopK.
	merged = s.applyBoostHybrid(httpReq.Collection, merged, httpReq.Boost)
	fuse.SetInt("mddb.results", len(merged))
	fuse.End()

	items := s.tracedLoadHybridDocs(ctx, httpReq.Collection, merged, true)
	// Curation rules (pin and hide) apply to every hybrid search, as they do
	// over REST; the MCP and gRPC paths used to differ here.
	items = s.applyCurationHybrid(httpReq.Collection, httpReq.Query, items)
	if len(items) > mergeTopK {
		items = items[:mergeTopK]
	}
	items = groupHybridItems(httpReq.Collection, items, httpReq.GroupBy, httpReq.TopK)
	return items, httpReq, nil
}
