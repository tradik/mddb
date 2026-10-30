package main

import (
	"context"
	"errors"
	"fmt"
	"mddb/internal/metrics"
	"mddb/internal/storage"
	vec "mddb/internal/vector"

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

// vectorRun carries one direct-client vector search through its stages:
// what the request resolved to, the plan, and what the index search found.
type vectorRun struct {
	s              *Server
	req            *MCPVectorSearchRequest
	includeContent bool
	waited         *IndexWaitResult
	topK           int
	searchTopK     int
	allowed        map[string]bool
	filtered       bool
	ps             plannedSearch
	fb             *feedback
	queryVector    []float32
	metric         vec.SimilarityFunc
	chunkMode      bool
	windowSize     int
	bestChunk      map[string]int
	groupTags      map[string]groupTag
}

// vectorSearch is VectorSearch before the limit report is attached.
func (c *DirectClient) vectorSearch(ctx context.Context, req *MCPVectorSearchRequest) (*MCPVectorSearchResponse, error) {
	s := c.server
	if err := s.resolveVectorRequest(req); err != nil {
		return nil, err
	}
	r := &vectorRun{s: s, req: req, includeContent: req.IncludeContent || req.wantsPassages()}
	r.waited = s.waitForIndex(ctx, req.Collection, req.IndexWait)
	if err := req.requireQuery(); err != nil {
		return nil, err
	}

	// RAG-001: request > collection profile > historical default, under the
	// server's cap (#368).
	r.topK, req.limitReport = s.ResolveTopK(req.Collection, req.TopK, 5)
	// Oversample for chunk deduplication (SRCH-005).
	r.searchTopK = OversampledTopK(r.topK, s.ResolveOversample(req.Collection, req.Oversample), 20)
	r.allowed, r.filtered = s.allowedDocIDs(req.Collection, req.FilterMeta, req.Filter)
	if req.QueryImage != "" { // RAG-010: search the image vectors with an image
		return s.mcpImageQuery(ctx, req, r.topK, r.allowed, r.filtered, r.includeContent, r.waited)
	}
	vectors, err := r.plan(ctx)
	if err != nil {
		return nil, err
	}
	r.metric = vec.ResolveSimilarity(req.DistanceMetric)
	if r.filtered && len(r.allowed) == 0 {
		return r.noMatch(ctx), nil
	}
	results, err := r.search(ctx, vectors)
	if err != nil {
		return nil, err
	}
	if !req.noGain { // a measurement (SRCH-019 eval) is neither gain nor query shape
		s.Gain.Search(gainKind{Type: SearchTypeVector, Algorithm: r.ps.Algorithm}) // AGT-004
		r.record(ctx, len(results))                                                // OPS-025
	}
	items := r.addPending(r.loadItems(ctx, results))

	// RAG-010: each hit's images, and the images the query matches.
	var imageHits []ImageRef
	if req.IncludeImages {
		imageHits = s.imageHitsFor(ctx, req.Collection, req.Query, r.topK, req.Threshold, r.allowed, r.filtered, r.ps.Plan)
		attachImages(s, items, func(it *MCPVectorSearchResult) imageOwner { return mcpDocImages(req.Collection, &it.Document) },
			func(it *MCPVectorSearchResult, refs []ImageRef) { it.Images = refs })
	}
	if req.wantsPassages() {
		return r.passages(items, imageHits), nil
	}
	return r.response(items, imageHits), nil
}

// resolveVectorRequest fills req's grouping and passage options from the
// collection's profile and validates the request.
func (s *Server) resolveVectorRequest(req *MCPVectorSearchRequest) error {
	req.GroupBy = s.ResolveGroupBy(req.Collection, req.GroupBy)
	if err := req.GroupBy.Validate(); err != nil {
		return err
	}
	if err := req.ValidateIndexWait(); err != nil {
		return err
	}
	req.PassageOptions = s.ResolveFormat(req.Collection, req.PassageOptions)
	for _, validate := range []func() error{req.ValidatePassageOptions, req.ValidatePlanOptions,
		req.ValidateFeedback, req.ValidateDecomposition} {
		if err := validate(); err != nil {
			return err
		}
	}
	return nil
}

// requireQuery reports a request without a collection or anything to search
// with.
func (req *MCPVectorSearchRequest) requireQuery() error {
	if req.Collection == "" {
		return errors.New("missing collection")
	}
	if req.Query == "" && len(req.QueryVector) == 0 && req.QueryImage == "" {
		return errors.New("either query or queryVector is required")
	}
	return nil
}

// plan picks the algorithm, resolves the query vector, applies feedback and
// returns the vectors to search: the query's, plus one per decomposed part.
func (r *vectorRun) plan(ctx context.Context) ([][]float32, error) {
	s, req := r.s, r.req
	// SRCH-013: the named algorithm, the profile's, flat — or the planner's
	// choice for "auto". Disk-only collections exist ONLY in the quantized
	// index, which a flat request is routed to.
	ps, err := s.planSearch(planRequest{Collection: req.Collection, Algorithm: s.ResolveVectorAlgorithm(req.Collection, req.Algorithm),
		QuantizedDefault: true, Filtered: r.filtered, Allowed: r.allowed, SearchTopK: r.searchTopK,
		Dimensions: len(req.QueryVector), LatencyTargetMs: req.LatencyTargetMs})
	if err != nil {
		return nil, err
	}
	r.ps = ps
	queryVector, err := s.requestVector(ctx, req)
	if err != nil {
		return nil, err
	}
	// SRCH-015: the examples as vectors; query mode corrects the query here.
	r.fb, err = s.resolveFeedback(req.FeedbackOptions, []string{req.Collection}, len(queryVector))
	if err != nil {
		return nil, err
	}
	r.queryVector = r.fb.query(queryVector)
	r.ps.Plan.Feedback = r.fb.report()
	// SRCH-020: with queryDecomposition auto, one more vector per part.
	return s.queryVectors(ctx, req.DecompositionOptions, textQuery(req.Query, req.QueryVector), r.queryVector, r.fb, r.ps.Plan)
}

// requestVector is the request's query vector, or its query embedded.
func (s *Server) requestVector(ctx context.Context, req *MCPVectorSearchRequest) ([]float32, error) {
	if len(req.QueryVector) > 0 {
		return req.QueryVector, nil
	}
	if s.Embedding == nil {
		return nil, fmt.Errorf("%w and no queryVector provided", errNoEmbeddingProvider)
	}
	queryVector, err := s.embedQuery(ctx, req.Query)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errEmbedQuery, err)
	}
	return queryVector, nil
}

// record counts the search for the query-shape metrics.
func (r *vectorRun) record(ctx context.Context, results int) {
	req := r.req
	r.s.recordSearch(ctx, vectorShape(vectorShapeInput{Mode: metrics.ModeVector, Collection: req.Collection,
		Requested: r.s.ResolveVectorAlgorithm(req.Collection, req.Algorithm), Ran: r.ps.Algorithm,
		FilterMeta: req.FilterMeta, Filter: req.Filter, Plan: r.ps.Plan, Results: results, Limit: req.limitReport}))
}

// noMatch answers a search whose filter matched no document.
func (r *vectorRun) noMatch(ctx context.Context) *MCPVectorSearchResponse {
	s, req := r.s, r.req
	if !req.noGain {
		r.record(ctx, 0) // OPS-025: the filter matched nothing
	}
	metricName := req.DistanceMetric
	if metricName == "" {
		metricName = "cosine"
	}
	resp := &MCPVectorSearchResponse{
		Results:        []MCPVectorSearchResult{},
		Algorithm:      r.ps.Algorithm,
		DistanceMetric: metricName,
		// Included even with no results: a field that appears only
		// sometimes is a field agents handle inconsistently.
		ResponsePrompt: s.ResponsePrompt(req.Collection, req.Query),
		Plan:           req.explained(r.ps.Plan),
	}
	s.setModel(resp)
	return resp
}

// setModel names the embedding model in resp, when there is one.
func (s *Server) setModel(resp *MCPVectorSearchResponse) {
	if s.Embedding != nil {
		resp.Model = s.Embedding.Model()
		resp.Dimensions = s.Embedding.Dimensions()
	}
}

// search runs the index search and shapes its results: feedback rescoring,
// summaries, chunk deduplication, grouping and MMR.
func (r *vectorRun) search(ctx context.Context, vectors [][]float32) ([]vec.VectorResult, error) {
	s, req := r.s, r.req
	vsp := s.Tracer.Stage(ctx, spanSearchVector) // OPS-024
	vsp.SetString("mddb.vector.algorithm", r.ps.Algorithm)
	// Disk-only collections: each query vector's quantized candidates are
	// rescored from disk before a decomposed query's lists are fused.
	results, diskVecs := s.searchVectorPath(r.ps, indexQuery{Collection: req.Collection, TopK: r.searchTopK,
		Threshold: req.Threshold, Allowed: r.allowed, Filtered: r.filtered, Metric: r.metric}, vectors)
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
	results = s.rescoreFeedback(r.fb, req.Collection, results, r.metric) // SRCH-015, rescore mode
	results = s.applySummaries(summarySearch{Collection: req.Collection, Mode: s.ResolveSummaryMode(req.Collection, req.summaryMode),
		Query: r.queryVector, SearchTopK: r.searchTopK, TopK: r.topK, Threshold: req.Threshold, Allowed: r.allowed,
		Filtered: r.filtered, Metric: r.metric}, results, r.ps.Plan) // RAG-009

	r.chunkMode = mode == RetrievalModeChunk || mode == RetrievalModeWindow
	if req.wantsPassages() {
		r.bestChunk = map[string]int{}
	}
	results, r.groupTags = s.shapeVectorResults(vectorShaping{
		Collection: req.Collection, ChunkMode: r.chunkMode, GroupBy: req.GroupBy,
		MMR: req.MMR, MMRLambda: req.MMRLambda, TopK: r.topK, DiskVecs: diskVecs, BestChunk: r.bestChunk,
	}, results)
	if mode == RetrievalModeWindow {
		r.windowSize = max(req.WindowSize, 1)
	}
	return results, nil
}

// loadItems reads the documents of results in rank order; a hit whose
// document is gone or unreadable is skipped.
func (r *vectorRun) loadItems(ctx context.Context, results []vec.VectorResult) []MCPVectorSearchResult {
	s, collection := r.s, r.req.Collection
	items := make([]MCPVectorSearchResult, 0, len(results))
	load := s.Tracer.Stage(ctx, spanSearchLoadContent)
	_ = s.DBViewC(collection, func(tx *bolt.Tx) error {
		bDocs := tx.Bucket([]byte("docs"))
		if bDocs == nil {
			return nil
		}
		for rank, vr := range results {
			docID, chunkIndex := splitChunkKey(vr.DocID)
			v := bDocs.Get(storage.DocKey(collection, docID))
			if v == nil {
				continue
			}
			docPtr, err := loadDoc(v)
			if err != nil {
				continue
			}
			items = append(items, r.item(*docPtr, vr, rank, chunkIndex))
		}
		return nil
	})
	load.SetInt("mddb.documents", len(items))
	load.End()
	return items
}

// item is one result: the hit's document, its group, and in chunk modes the
// matching passage.
func (r *vectorRun) item(doc storage.Doc, vr vec.VectorResult, rank, chunkIndex int) MCPVectorSearchResult {
	s, collection := r.s, r.req.Collection
	item := MCPVectorSearchResult{Score: vr.Score, Rank: rank + 1}
	if t, ok := r.groupTags[vr.DocID]; ok {
		item.Group, item.GroupRank = t.Group, t.Rank
	}
	if r.chunkMode {
		idx := chunkIndex
		item.ChunkIndex = &idx
		item.ChunkText, item.StartLine, item.EndLine =
			chunkPassageWithLinesSize(doc.ContentMD, chunkIndex, r.windowSize,
				s.chunkModeFor(collection, &doc), s.chunkSizeFor(collection))
		item.docTokens = approxTokens(doc.ContentMD)
	}
	if !r.includeContent {
		doc.ContentMD = ""
	}
	item.Document = docToMCPDocument(doc)
	return item
}

// addPending appends, for readLevel indexed+pending, the matching documents
// still waiting for their embedding.
func (r *vectorRun) addPending(items []MCPVectorSearchResult) []MCPVectorSearchResult {
	req := r.req
	if req.ReadLevel != ReadLevelIndexedPlusPending {
		return items
	}
	for _, doc := range pendingDocsFor(r.s, req.Collection, req.Query, req.FilterMeta, req.Filter, r.topK, items,
		func(it MCPVectorSearchResult) string { return it.Document.ID }) {
		if !r.includeContent {
			doc.ContentMD = ""
		}
		items = append(items, MCPVectorSearchResult{Document: docToMCPDocument(doc), Rank: len(items) + 1, PendingEmbedding: true})
	}
	return items
}

// passages answers a format=passages search.
func (r *vectorRun) passages(items []MCPVectorSearchResult, imageHits []ImageRef) *MCPVectorSearchResponse {
	s, req := r.s, r.req
	hits := make([]passageSource, 0, len(items))
	terms := queryTerms(req.Query)
	for _, it := range items {
		best, ok := r.bestChunk[it.Document.ID]
		if !ok || it.PendingEmbedding {
			best = -1
		}
		hits = append(hits, passageSource{doc: mcpDocumentToDoc(it.Document), score: float64(it.Score),
			chunkText: it.ChunkText, startLine: it.StartLine, endLine: it.EndLine, bestChunk: best, terms: terms,
			chunks: r.ps.Plan.expandedChunks(it.Document.ID)})
	}
	p := s.finishPassages(req.Collection, s.buildPassages(req.Collection, hits, req.PassageOptions, "vector"), r.waited)
	s.passageImages(&p, req.IncludeImages, imageHits)
	return &MCPVectorSearchResponse{Passages: &p, Algorithm: r.ps.Algorithm}
}

// response answers a search with its results, under the collection's context
// budget.
func (r *vectorRun) response(items []MCPVectorSearchResult, imageHits []ImageRef) *MCPVectorSearchResponse {
	s, req := r.s, r.req
	resp := &MCPVectorSearchResponse{
		Results:   items,
		Total:     len(items),
		Algorithm: r.ps.Algorithm,
		Indexing:  r.waited,
		Plan:      req.explained(r.ps.Plan),
		ImageHits: imageHits,
		// RAG-002: the collection's formatting instruction travels with the
		// results, so an agent does not need a second call to learn how this
		// collection wants its answers shaped.
		ResponsePrompt: s.ResponsePrompt(req.Collection, req.Query),
	}
	s.setModel(resp)

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
	return resp
}
