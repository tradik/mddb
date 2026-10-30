package main

import (
	"context"
	"errors"
	"fmt"
	"mddb/internal/fts"
	"mddb/internal/geo"
	"mddb/internal/metrics"
	"mddb/internal/storage"
	"mddb/internal/vector"
	"net/http"
	"sort"
	"time"

	json "mddb/internal/jsonx"

	bolt "go.etcd.io/bbolt"
)

// HybridSearchRequest represents an HTTP hybrid search request.
type HybridSearchRequest struct {
	Collection      string `json:"collection"`
	Query           string `json:"query"`
	TopK            int    `json:"topK"`
	Algorithm       string `json:"algorithm"`       // FTS algorithm: "bm25" (default), "bm25f"
	VectorAlgorithm string `json:"vectorAlgorithm"` // vector algorithm: "flat" (default), "auto" (SRCH-013), "hnsw", "ivf", "pq", "opq", "sq", "sq4", "bq"
	// Alpha weights vector against keyword: 0 = keyword only, 1 = semantic
	// only. Default 0.5.
	//
	// A pointer, because zero is meaningful here and JSON cannot tell an
	// omitted number from a zero one (SRCH-007). Before this, a client asking
	// for `alpha: 0` — pure keyword — was silently given 0.5, the opposite of
	// what it asked for, with nothing to indicate it had happened.
	Alpha    *float64 `json:"alpha,omitempty"`
	Strategy string   `json:"strategy"` // "alpha" (default), "rrf" or "weighted"
	// Signals configures the "weighted" strategy (SRCH-002). Ignored by the
	// other strategies, so a request carrying both is not ambiguous — the
	// strategy decides.
	Signals        FusionSignals       `json:"signals,omitempty"`
	RRFK           int                 `json:"rrfK"`           // RRF parameter k (default 60)
	Fuzzy          int                 `json:"fuzzy"`          // typo tolerance: 0, 1, 2
	Threshold      float64             `json:"threshold"`      // min vector similarity 0-1
	DistanceMetric string              `json:"distanceMetric"` // "cosine" (default), "dot_product", "euclidean"
	FilterMeta     map[string][]string `json:"filterMeta"`
	Filter         MetaFilter          `json:"filter,omitempty"` // operator filter (SRCH-017): prefix, contains, gt/gte/lt/lte, not, exists; ANDed with filterMeta
	Geo            *HybridGeoFilter    `json:"geo,omitempty"`    // optional spatial pre-filter
	IncludeContent bool                `json:"includeContent"`
	FieldWeights   map[string]float64  `json:"fieldWeights,omitempty"`
	// BM25K1 and BM25B override the BM25 constants for this query (SRCH-018);
	// nil = the collection profile's, then 1.2 / 0.75. bm25 and bm25f only.
	BM25K1          *float64           `json:"bm25K1,omitempty"`
	BM25B           *float64           `json:"bm25B,omitempty"`
	DisableStem     bool               `json:"disableStem"`
	DisableSynonyms bool               `json:"disableSynonyms"`
	Lang            string             `json:"lang,omitempty"`
	Boost           map[string]float64 `json:"boost,omitempty"` // per-query boost: "metaKey:metaValue" → multiplier
	Sort            string             `json:"sort,omitempty"`  // "" / "combined" (default) | "distance" (requires geo filter)
	// Faceted search (v2.9.14+): per-key value counts aggregated over matched documents.
	FacetBy        []string `json:"facetBy,omitempty"`
	FacetMaxValues int      `json:"facetMaxValues,omitempty"` // 0 = unlimited
	// Oversample is the recall/latency knob (SRCH-005): candidates asked of
	// the index per requested result, before deduplication, merging or
	// rescoring trims them. 1.0-10.0; 0 = use the collection profile, then
	// the default.
	Oversample     float64  `json:"oversample,omitempty"`
	GroupBy        *GroupBy `json:"groupBy,omitempty"` // keep perGroup results per metadata value, document or collection (SRCH-016)
	PassageOptions          // format, passageLines (AGT-002)
	IndexWait
	PlanOptions          // explain, latencyTargetMs (SRCH-013), for the vector half
	FeedbackOptions      // positive, negative, feedbackMode, feedbackWeight, rocchio (SRCH-015), for the vector half
	DecompositionOptions // queryDecomposition (SRCH-020), for the vector half
	ImageOptions         // includeImages (RAG-010)

	// onlyIDs restricts the full-text half to these documents. Internal:
	// used to find matches among documents still waiting for their
	// embedding (GO-042).
	onlyIDs map[string]bool
	// plan is the vector half's plan, set by hybridItems for the response
	// to report (SRCH-013).
	plan *SearchPlan
	// summaryMode overrides the collection's summaryMode (RAG-009) for an
	// evaluation config; "" = the profile's.
	summaryMode string
	// limitReport is set when the server's cap lowered TopK (#368), for the
	// response to say so.
	limitReport LimitReport
}

// HybridGeoFilter restricts hybrid search to docs within radius of a point.
// When set, only documents present in the in-memory R-tree AND within the
// given radius are considered by FTS and vector search; the distance is
// attached to each result item.
type HybridGeoFilter struct {
	Lat          float64 `json:"lat"`
	Lng          float64 `json:"lng"`
	RadiusMeters float64 `json:"radiusMeters"`
}

// HybridSearchResultItem represents a single hybrid search result.
type HybridSearchResultItem struct {
	Document       storage.Doc `json:"document"`
	CombinedScore  float64     `json:"combinedScore"`
	FTSScore       float64     `json:"ftsScore"`
	VectorScore    float64     `json:"vectorScore"`
	DistanceMeters float64     `json:"distanceMeters,omitempty"`
	MatchedTerms   []string    `json:"matchedTerms,omitempty"`
	Rank           int         `json:"rank"`
	Pinned         bool        `json:"pinned,omitempty"` // set by curation rules (v2.9.14+)
	// Group and GroupRank are set when the request had groupBy (SRCH-016):
	// the group this result counted against and its 1-based place in it.
	Group     string `json:"group,omitempty"`
	GroupRank int    `json:"groupRank,omitempty"`
	// PendingEmbedding is true for a document whose embedding is still queued:
	// found by its text, not yet by its meaning (GO-042).
	PendingEmbedding bool `json:"pendingEmbedding,omitempty"`
	// Images are the document's images, with includeImages (RAG-010).
	Images []ImageRef `json:"images,omitempty"`
}

// HybridSearchResponse represents the response from hybrid search.
type HybridSearchResponse struct {
	Results []HybridSearchResultItem `json:"results"`
	Total   int                      `json:"total"`
	// Signals echoes the weights the "weighted" strategy applied, defaults
	// filled in, so a caller can see what it actually got (SRCH-002).
	Signals FusionSignals `json:"signals,omitempty"`
	// SignalBreakdown reports each signal's contribution per result, in the
	// same order. A reranking nobody can explain is a reranking nobody trusts.
	SignalBreakdown []SignalBreakdown `json:"signalBreakdown,omitempty"`
	Strategy        string            `json:"strategy"`
	Alpha           float64           `json:"alpha,omitempty"`
	RRFK            int               `json:"rrfK,omitempty"`
	FTSAlgorithm    string            `json:"ftsAlgorithm"`
	VectorAlgorithm string            `json:"vectorAlgorithm"`
	DistanceMetric  string            `json:"distanceMetric"`
	// ContextTruncated reports that the collection's contextTokenBudget
	// dropped results from the tail (RAG-001).
	ContextTruncated bool         `json:"contextTruncated,omitempty"`
	Stats            *SearchStats `json:"searchStats,omitempty"`
	Facets           FacetResult  `json:"facets,omitempty"` // populated when request.facetBy is set (v2.9.14+)
	// Indexing reports the wait when the request set waitForIndex (GO-042).
	Indexing *IndexWaitResult `json:"indexing,omitempty"`
	// Plan is how the vector search ran and why, when the request set explain
	// (SRCH-013).
	Plan *SearchPlan `json:"plan,omitempty"`
	// ImageHits are the images the query matched by their picture, with
	// includeImages and a provider that embeds images (RAG-010).
	ImageHits   []ImageRef `json:"imageHits,omitempty"`
	LimitReport            // limit, limitCapped: set when the server's cap lowered the request's limit (#368)
}

// handleHybridSearch handles POST /v1/hybrid-search
func (s *Server) handleHybridSearch(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	var req HybridSearchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		bad(w, err)
		return
	}
	if req.Collection == "" || req.Query == "" {
		bad(w, errors.New("missing required fields: collection, query"))
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
	req.PassageOptions = s.ResolveFormat(req.Collection, req.PassageOptions)
	if err := req.ValidatePassageOptions(); err != nil {
		unprocessable(w, err)
		return
	}
	if err := req.ValidatePlanOptions(); err != nil {
		unprocessable(w, err)
		return
	}
	if err := req.ValidateFeedback(); err != nil {
		unprocessable(w, err)
		return
	}
	if err := req.ValidateDecomposition(); err != nil {
		unprocessable(w, err)
		return
	}
	waited := s.waitForIndex(r.Context(), req.Collection, req.IndexWait)

	// Defaults. RAG-001 inserts the collection profile between the request
	// and each historical default, so a collection without one is unchanged.
	req.TopK, req.limitReport = s.ResolveTopK(req.Collection, req.TopK, 10)
	if req.Algorithm == "" {
		req.Algorithm = "bm25"
	}
	// SRCH-013: request, then collection profile, then flat.
	if req.VectorAlgorithm = s.ResolveVectorAlgorithm(req.Collection, req.VectorAlgorithm); req.VectorAlgorithm == "" {
		req.VectorAlgorithm = "flat"
	}
	req.Strategy = s.ResolveHybridStrategy(req.Collection, req.Strategy, "alpha")
	// alpha is resolved once here and used everywhere below, so the request
	// struct stays the caller's words and never becomes a scratch pad.
	var alpha float64
	// Precedence: an explicit request alpha wins, then the collection's
	// profile, then 0.5. An explicit 0 is a request, not an absence.
	if req.Alpha != nil {
		alpha = *req.Alpha
	} else {
		alpha = s.ResolveHybridAlpha(req.Collection, 0, false, 0.5)
	}
	if alpha < 0 {
		alpha = 0
	}
	if alpha > 1 {
		alpha = 1
	}
	if req.RRFK <= 0 {
		req.RRFK = 60
	}
	if req.Fuzzy < 0 {
		req.Fuzzy = 0
	}
	if req.Fuzzy > 2 {
		req.Fuzzy = 2
	}

	// Validate strategy
	if req.Strategy != "alpha" && req.Strategy != "rrf" && req.Strategy != "weighted" {
		bad(w, fmt.Errorf("unknown strategy: %s, available: alpha, rrf", req.Strategy))
		return
	}
	s.Gain.Search(gainKind{Type: SearchTypeHybrid, Algorithm: req.VectorAlgorithm, Fusion: req.Strategy}) // AGT-004

	// Normalize + validate sort mode. "distance" only makes sense when the
	// request also carries a geo filter (otherwise every result's
	// distanceMeters is zero and the ordering would be meaningless).
	switch req.Sort {
	case "", "combined":
		req.Sort = "combined"
	case "distance":
		if req.Geo == nil {
			bad(w, errors.New("sort=distance requires a geo filter"))
			return
		}
	default:
		bad(w, fmt.Errorf("unknown sort: %s, available: combined, distance", req.Sort))
		return
	}

	// ---- Step 0: Geo pre-filter ----
	// If the request has a geo filter, ask the in-memory R-tree for docIDs
	// within the radius and remember each one's exact distance. We don't
	// inject these into FilterMeta (which is backed by on-disk meta indices);
	// instead we keep them as a set used both to short-circuit and to
	// attach distanceMeters to the final result items.
	var geoAllowed map[string]bool
	var geoDist map[string]float64
	if req.Geo != nil {
		if s.GeoIndex == nil || !s.GeoIndex.IsReady() {
			bad(w, errors.New("geo index not ready"))
			return
		}
		if !geo.ValidLatLng(req.Geo.Lat, req.Geo.Lng) || req.Geo.RadiusMeters <= 0 {
			bad(w, errors.New("invalid geo filter: lat/lng/radiusMeters"))
			return
		}
		// A large topK is intentional — we want every candidate in the radius,
		// not just the TopK by distance, so the FTS/vector ranking can reorder
		// freely within the spatial envelope.
		hits := s.GeoIndex.Search(req.Collection, req.Geo.Lat, req.Geo.Lng, req.Geo.RadiusMeters, 1_000_000, nil)
		if len(hits) == 0 {
			ok(w, HybridSearchResponse{
				Results:         []HybridSearchResultItem{},
				Total:           0,
				Strategy:        req.Strategy,
				FTSAlgorithm:    req.Algorithm,
				VectorAlgorithm: req.VectorAlgorithm,
				DistanceMetric:  "cosine",
				LimitReport:     req.limitReport,
			})
			return
		}
		geoAllowed = make(map[string]bool, len(hits))
		geoDist = make(map[string]float64, len(hits))
		for _, h := range hits {
			geoAllowed[h.DocID] = true
			geoDist[h.DocID] = h.DistanceMeters
		}
	}

	// ---- Step 1: Run FTS search ----
	ftsResults, err := s.tracedFTS(r.Context(), req)
	if err != nil {
		bad(w, fmt.Errorf("FTS search failed: %w", err))
		return
	}

	// ---- Step 2: Run vector search ----
	vectorResults, plan, err := s.vectorHalf(r.Context(), req)
	if errors.Is(err, errFeedbackExample) {
		unprocessable(w, err) // SRCH-015: an example that names nothing usable
		return
	}
	if err != nil {
		bad(w, fmt.Errorf("vector search failed: %w", err))
		return
	}

	// ---- Step 3: Merge results ----
	// When a geo filter is active, merge with an inflated TopK so the spatial
	// intersection below still has enough candidates. The caller-requested
	// TopK is applied again after the spatial pass.
	mergeTopK := req.TopK
	if geoAllowed != nil || req.GroupBy != nil {
		mergeTopK = groupedCandidates(req.TopK)
	}
	var merged []HybridSearchResultItem
	var signalBreakdowns []SignalBreakdown
	fuse := s.Tracer.Stage(r.Context(), spanSearchFuse) // OPS-024
	fuse.SetString("mddb.fusion.strategy", req.Strategy)
	switch req.Strategy {
	case "rrf":
		merged = mergeRRF(ftsResults, vectorResults, req.RRFK, mergeTopK)
	case "weighted":
		// SRCH-002: the base blend is alpha's, and the signals adjust it.
		// Replacing the blend rather than adjusting it would throw away the
		// keyword and vector scores that decide most of the ranking.
		merged = mergeAlpha(ftsResults, vectorResults, alpha, mergeTopK)
		merged, signalBreakdowns = applyWeightedSignals(merged, req.Signals, time.Now())
	default: // "alpha"
		merged = mergeAlpha(ftsResults, vectorResults, alpha, mergeTopK)
	}

	// Apply per-query boosts/demotions after merging — operates on CombinedScore.
	// Boosting reorders; it never adds, so merged is still at most mergeTopK.
	merged = s.applyBoostHybrid(req.Collection, merged, req.Boost)
	fuse.SetInt("mddb.results", len(merged))
	fuse.End()

	// Spatial post-filter: keep only results inside the geo radius, attach
	// distanceMeters from the pre-computed lookup, optionally re-sort by
	// proximity, and truncate back to TopK.
	if geoAllowed != nil {
		filtered := merged[:0]
		for _, m := range merged {
			if !geoAllowed[m.Document.ID] {
				continue
			}
			m.DistanceMeters = geoDist[m.Document.ID]
			filtered = append(filtered, m)
		}
		merged = filtered
		if req.Sort == "distance" {
			sort.Slice(merged, func(i, j int) bool {
				return merged[i].DistanceMeters < merged[j].DistanceMeters
			})
		}
		if len(merged) > req.TopK && req.GroupBy == nil {
			merged = merged[:req.TopK]
		}
		for i := range merged {
			merged[i].Rank = i + 1
		}
	}

	// ---- Step 4: Load full documents ----
	items := s.tracedLoadHybridDocs(r.Context(), req.Collection, merged, req.IncludeContent || req.wantsPassages())
	// loadHybridDocs copies merged items but may drop DistanceMeters; re-attach.
	if geoAllowed != nil {
		for i := range items {
			items[i].DistanceMeters = geoDist[items[i].Document.ID]
		}
	}

	distMetric := req.DistanceMetric
	if distMetric == "" {
		distMetric = "cosine"
	}
	items = s.applyCurationHybrid(req.Collection, req.Query, items)
	items = groupHybridItems(req.Collection, items, req.GroupBy, req.TopK)
	for i := range items {
		items[i].PendingEmbedding = s.pendingEmbedding(req.Collection, items[i].Document.ID)
	}
	if req.TopK > 0 && len(items) > req.TopK {
		items = items[:req.TopK]
	}
	req.plan = plan
	s.recordSearch(r.Context(), vectorShape(metrics.ModeHybrid, req.Collection, req.VectorAlgorithm, ranVectorAlgorithm(req.VectorAlgorithm, plan),
		req.FilterMeta, req.Filter, plan, len(items), req.limitReport)) // OPS-025
	imageHits := s.hybridImages(r.Context(), req, items) // RAG-010

	if req.wantsPassages() {
		p := s.hybridPassages(req.Collection, req.Query, items, req.PassageOptions, waited, plan)
		s.passageImages(&p, req.IncludeImages, imageHits)
		p.LimitReport = req.limitReport
		ok(w, p)
		return
	}

	// RAG-001: cap the total context this collection hands back, after topK
	// so the budget trims the tail of an already-ranked list.
	items, contextTruncated := applyContextBudget(s, req.Collection, items,
		func(it HybridSearchResultItem) int { return approxTokens(it.Document.ContentMD) })
	countHits(s, items, func(it HybridSearchResultItem) gainSize { return wholeDoc(it.Document.ContentMD) })

	resp := HybridSearchResponse{
		Results:         items,
		Total:           len(items),
		Strategy:        req.Strategy,
		FTSAlgorithm:    req.Algorithm,
		VectorAlgorithm: ranVectorAlgorithm(req.VectorAlgorithm, plan),
		DistanceMetric:  distMetric,
		Indexing:        waited,
		Plan:            req.explained(plan),
		ImageHits:       imageHits,
		LimitReport:     req.limitReport,
	}
	if req.Strategy == "alpha" {
		resp.Alpha = alpha
	}
	if req.Strategy == "rrf" {
		resp.RRFK = req.RRFK
	}
	if req.Strategy == "weighted" {
		resp.Alpha = alpha
		resp.Signals = req.Signals.Defaults()
		// The breakdown is trimmed alongside the results it explains: a
		// breakdown for a result the caller never received explains nothing.
		if len(signalBreakdowns) >= len(items) {
			resp.SignalBreakdown = signalBreakdowns[:len(items)]
		}
	}
	resp.ContextTruncated = contextTruncated

	if len(req.FacetBy) > 0 && len(items) > 0 {
		docs := make([]storage.Doc, len(items))
		for i, it := range items {
			docs[i] = it.Document
		}
		resp.Facets = computeFacets(docs, req.FacetBy, req.FacetMaxValues)
	}

	// Track hybrid search operation
	if s.Metrics != nil {
		s.Metrics.IncOp("hybrid_search", req.Strategy)
	}

	if searchStatsEnabled() && s.FTSIndex != nil {
		tokens := s.FTSIndex.TokenizeQueryLang(req.Collection, req.Query, req.Lang)
		terms := make([]string, 0, len(tokens))
		for t := range tokens {
			terms = append(terms, t)
		}
		resp.Stats = &SearchStats{
			DurationMs:  float64(time.Since(start).Microseconds()) / 1000.0,
			QueryTerms:  terms,
			IndexSize:   resp.Total,
			TotalTokens: len(terms),
		}
	}

	ok(w, resp)
}

// runFTSSearch executes the FTS portion of hybrid search.
func (s *Server) runFTSSearch(req HybridSearchRequest) ([]fts.FTSResult, error) {
	if s.FTSIndex == nil {
		return nil, nil
	}
	bm25, err := s.ResolveBM25(req.Collection, req.BM25K1, req.BM25B)
	if err != nil {
		return nil, err
	}

	// Per-query stemming/synonym control on a view of the index, never on
	// the shared one (see FTSIndex.QueryView).
	idx := s.FTSIndex.QueryView(req.DisableStem, req.DisableSynonyms)

	// Pre-filter by metadata if provided
	var allowed map[string]bool
	if ids, active := s.allowedDocIDs(req.Collection, req.FilterMeta, req.Filter); active {
		allowed = ids
		if len(allowed) == 0 {
			return nil, nil
		}
	}
	if req.onlyIDs != nil {
		if allowed == nil {
			allowed = req.onlyIDs
		} else {
			allowed = intersectSets(allowed, req.onlyIDs)
		}
		if len(allowed) == 0 {
			return nil, nil
		}
	}

	// Oversample: get more results for better merging (SRCH-005). A higher
	// factor gives the fusion more to work with on both sides.
	searchLimit := OversampledTopK(req.TopK, s.ResolveOversample(req.Collection, req.Oversample), 50)
	// A metadata filter applies to every text match, not to the top
	// searchLimit of the unfiltered ranking (#311): search them all, filter,
	// then keep searchLimit.
	candidates := searchLimit
	if allowed != nil {
		candidates = 0
	}

	tokens := idx.TokenizeQueryLang(req.Collection, req.Query, req.Lang)

	var results []fts.FTSResult

	switch req.Algorithm {
	case "bm25f":
		// RAG-009: meta.summary joins the fields while summaries are on.
		weights := s.summaryFieldWeights(req.Collection, s.ResolveSummaryMode(req.Collection, req.summaryMode), req.FieldWeights)
		if req.Fuzzy > 0 {
			results, err = idx.SearchBM25FFuzzy(req.Collection, tokens, candidates, req.Fuzzy, weights, bm25)
		} else {
			results, err = idx.SearchBM25F(req.Collection, tokens, candidates, weights, bm25)
		}
	case "bm25":
		if req.Fuzzy > 0 {
			results, err = idx.SearchBM25Fuzzy(req.Collection, req.Query, candidates, req.Fuzzy, bm25)
		} else {
			results, err = idx.SearchBM25(req.Collection, req.Query, candidates, bm25)
		}
	case "pmisparse":
		if req.Fuzzy > 0 {
			results, err = idx.SearchPMISparseFuzzy(req.Collection, req.Query, candidates, req.Fuzzy)
		} else {
			results, err = idx.SearchPMISparse(req.Collection, req.Query, candidates)
		}
	default:
		if req.Fuzzy > 0 {
			results, err = idx.SearchBM25Fuzzy(req.Collection, req.Query, candidates, req.Fuzzy, bm25)
		} else {
			results, err = idx.SearchBM25(req.Collection, req.Query, candidates, bm25)
		}
	}

	if err != nil {
		return nil, err
	}

	// Apply metadata filter if provided
	if allowed != nil {
		filtered := results[:0]
		for _, r := range results {
			if allowed[r.DocID] {
				filtered = append(filtered, r)
			}
		}
		results = filtered
		if searchLimit > 0 && len(results) > searchLimit {
			results = results[:searchLimit]
		}
	}

	return results, nil
}

// runVectorSearch executes the vector portion of hybrid search, for callers
// that report no plan (automation triggers).
func (s *Server) runVectorSearch(ctx context.Context, req HybridSearchRequest) ([]vector.VectorResult, error) {
	results, _, err := s.vectorHalf(ctx, req)
	return results, err
}

// vectorHalf executes the vector portion of hybrid search and returns its
// plan (SRCH-013) — nil when the vector half did not run.
func (s *Server) vectorHalf(ctx context.Context, req HybridSearchRequest) ([]vector.VectorResult, *SearchPlan, error) {
	if s.Embedding == nil || len(s.VectorSearchers) == 0 {
		return nil, nil, nil
	}

	algo := req.VectorAlgorithm
	if _, ok := s.VectorSearchers[algo]; !ok && algo != AlgorithmAuto {
		algo = "flat"
	}
	searchTopK := OversampledTopK(req.TopK, s.ResolveOversample(req.Collection, req.Oversample), 20)
	allowedIDs, filtered := s.allowedDocIDs(req.Collection, req.FilterMeta, req.Filter)
	ps, err := s.planSearch(planRequest{Collection: req.Collection, Algorithm: algo, Filtered: filtered,
		Allowed: allowedIDs, SearchTopK: searchTopK, LatencyTargetMs: req.LatencyTargetMs})
	if err != nil {
		return nil, nil, nil // the full-text half still answers (#346: per collection now)
	}

	queryVector, err := s.embedQuery(ctx, req.Query)
	if err != nil {
		return nil, nil, fmt.Errorf("embedding failed: %w", err)
	}
	// SRCH-015: feedback corrects the vector half only; the full-text half
	// keeps the query's own words.
	fb, err := s.resolveFeedback(req.FeedbackOptions, []string{req.Collection}, len(queryVector))
	if err != nil {
		return nil, nil, err
	}
	queryVector = fb.query(queryVector)
	ps.Plan.Feedback = fb.report()
	// SRCH-020: decomposition splits the vector half only; the full-text half
	// already matches each part's words.
	vectors, err := s.queryVectors(ctx, req.DecompositionOptions, req.Query, queryVector, fb, ps.Plan)
	if err != nil {
		return nil, nil, err
	}

	metric := vector.ResolveSimilarity(req.DistanceMetric)
	if filtered && len(allowedIDs) == 0 {
		return nil, ps.Plan, nil
	}
	sp := s.Tracer.Stage(ctx, spanSearchVector) // OPS-024
	defer sp.End()
	sp.SetString("mddb.vector.algorithm", ps.Algorithm)
	results := searchQueryVectors(vectors, searchTopK, func(v []float32) []vector.VectorResult {
		return s.runPlanned(ps, req.Collection, v, searchTopK, req.Threshold, allowedIDs, filtered, metric)
	})
	results = s.rescoreFeedback(fb, req.Collection, results, metric)
	results = s.applySummaries(summarySearch{Collection: req.Collection, Mode: s.ResolveSummaryMode(req.Collection, req.summaryMode),
		Query: queryVector, SearchTopK: searchTopK, TopK: req.TopK, Threshold: req.Threshold, Allowed: allowedIDs,
		Filtered: filtered, Metric: metric}, results, ps.Plan) // RAG-009
	results = vector.DeduplicateChunkResults(results)
	sp.SetInt("mddb.results", len(results))
	return results, ps.Plan, nil
}

// ranVectorAlgorithm is the vector algorithm a hybrid response reports: the
// planner's choice for "auto", otherwise what was requested, as before.
func ranVectorAlgorithm(requested string, plan *SearchPlan) string {
	if requested == AlgorithmAuto && plan != nil {
		return plan.Algorithm
	}
	return requested
}

// mergeAlpha combines FTS and vector results using alpha blending.
// combined = alpha * normalizedFTS + (1-alpha) * vectorScore
func mergeAlpha(ftsResults []fts.FTSResult, vectorResults []vector.VectorResult, alpha float64, topK int) []HybridSearchResultItem {
	// Normalize FTS scores to 0-1 range
	var ftsMin, ftsMax float64
	if len(ftsResults) > 0 {
		ftsMin = ftsResults[0].Score
		ftsMax = ftsResults[0].Score
		for _, r := range ftsResults[1:] {
			if r.Score < ftsMin {
				ftsMin = r.Score
			}
			if r.Score > ftsMax {
				ftsMax = r.Score
			}
		}
	}

	normalizeFTS := func(score float64) float64 {
		if ftsMax == ftsMin {
			if ftsMax > 0 {
				return 1.0
			}
			return 0.0
		}
		return (score - ftsMin) / (ftsMax - ftsMin)
	}

	// Build combined map
	type combinedEntry struct {
		ftsScore     float64
		vectorScore  float64
		matchedTerms []string
	}
	combined := make(map[string]*combinedEntry)

	for _, r := range ftsResults {
		combined[r.DocID] = &combinedEntry{
			ftsScore:     normalizeFTS(r.Score),
			matchedTerms: r.MatchedTerms,
		}
	}
	for _, r := range vectorResults {
		e, ok := combined[r.DocID]
		if !ok {
			e = &combinedEntry{}
			combined[r.DocID] = e
		}
		e.vectorScore = float64(r.Score)
	}

	// Calculate combined scores
	results := make([]HybridSearchResultItem, 0, len(combined))
	for docID, e := range combined {
		combinedScore := (1-alpha)*e.ftsScore + alpha*e.vectorScore
		results = append(results, HybridSearchResultItem{
			Document:      storage.Doc{ID: docID},
			CombinedScore: combinedScore,
			FTSScore:      e.ftsScore,
			VectorScore:   e.vectorScore,
			MatchedTerms:  e.matchedTerms,
		})
	}

	sortByCombinedScore(results)

	if topK > 0 && len(results) > topK {
		results = results[:topK]
	}
	return results
}

// sortByCombinedScore orders fused results best first, and equal scores by
// document ID. The results come out of a map, so without the second key ties
// (common under RRF, where ranks add up to the same sum) came back in a
// different order on every call — and a topK cut inside a tie kept different
// documents each time.
func sortByCombinedScore(results []HybridSearchResultItem) {
	sort.Slice(results, func(i, j int) bool {
		if results[i].CombinedScore != results[j].CombinedScore {
			return results[i].CombinedScore > results[j].CombinedScore
		}
		return results[i].Document.ID < results[j].Document.ID
	})
}

// mergeRRF combines FTS and vector results using Reciprocal Rank Fusion.
// score = 1/(k + rank_fts) + 1/(k + rank_vector)
func mergeRRF(ftsResults []fts.FTSResult, vectorResults []vector.VectorResult, rrfK int, topK int) []HybridSearchResultItem {
	type combinedEntry struct {
		rrfScore     float64
		ftsScore     float64
		vectorScore  float64
		matchedTerms []string
	}
	combined := make(map[string]*combinedEntry)

	k := float64(rrfK)

	for rank, r := range ftsResults {
		e := &combinedEntry{
			ftsScore:     r.Score,
			matchedTerms: r.MatchedTerms,
		}
		e.rrfScore = 1.0 / (k + float64(rank+1))
		combined[r.DocID] = e
	}

	for rank, r := range vectorResults {
		e, ok := combined[r.DocID]
		if !ok {
			e = &combinedEntry{}
			combined[r.DocID] = e
		}
		e.vectorScore = float64(r.Score)
		e.rrfScore += 1.0 / (k + float64(rank+1))
	}

	results := make([]HybridSearchResultItem, 0, len(combined))
	for docID, e := range combined {
		results = append(results, HybridSearchResultItem{
			Document:      storage.Doc{ID: docID},
			CombinedScore: e.rrfScore,
			FTSScore:      e.ftsScore,
			VectorScore:   e.vectorScore,
			MatchedTerms:  e.matchedTerms,
		})
	}

	sortByCombinedScore(results)

	if topK > 0 && len(results) > topK {
		results = results[:topK]
	}
	return results
}

// loadHybridDocs loads full documents for hybrid search results.
func (s *Server) loadHybridDocs(collection string, items []HybridSearchResultItem, includeContent bool) []HybridSearchResultItem {
	results := make([]HybridSearchResultItem, 0, len(items))
	_ = s.DBViewC(collection, func(tx *bolt.Tx) error {
		bDocs := tx.Bucket([]byte("docs"))
		if bDocs == nil {
			return nil
		}
		rank := 0
		for _, item := range items {
			v := bDocs.Get(storage.DocKey(collection, item.Document.ID))
			if v == nil {
				continue
			}
			docPtr, err := loadDoc(v)
			if err != nil {
				continue
			}
			if docPtr.ExpiresAt > 0 && docPtr.ExpiresAt < currentUnix() {
				continue
			}
			doc := *docPtr
			if !includeContent {
				doc.ContentMD = ""
			}
			rank++
			results = append(results, HybridSearchResultItem{
				Document:      doc,
				CombinedScore: item.CombinedScore,
				FTSScore:      item.FTSScore,
				VectorScore:   item.VectorScore,
				MatchedTerms:  item.MatchedTerms,
				Rank:          rank,
			})
		}
		return nil
	})
	return results
}

// floatPtr is how an internal caller states an alpha it means, as opposed to
// one it left unset (SRCH-007).
func floatPtr(v float64) *float64 { return &v }

// alphaOrDefault reads an optional alpha, falling back to the historical 0.5.
//
// Used by callers that build a request in code rather than parsing one, where
// "unset" is a programming choice rather than a client's silence.
func alphaOrDefault(v *float64) float64 {
	if v == nil {
		return 0.5
	}
	return *v
}
