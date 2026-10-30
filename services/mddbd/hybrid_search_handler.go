package main

import (
	"cmp"
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
)

// handleHybridSearch handles POST /v1/hybrid-search. Each step lives in a
// named helper below, in the order the handler runs them (#379).
func (s *Server) handleHybridSearch(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	var req HybridSearchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		bad(w, err)
		return
	}
	if reply, err := s.checkHybridRequest(&req); err != nil {
		reply(w, err)
		return
	}
	waited := s.waitForIndex(r.Context(), req.Collection, req.IndexWait)
	alpha := s.applyHybridDefaults(&req)
	if err := checkHybridStrategy(req.Strategy); err != nil {
		bad(w, err)
		return
	}
	s.Gain.Search(gainKind{Type: SearchTypeHybrid, Algorithm: req.VectorAlgorithm, Fusion: req.Strategy}) // AGT-004
	if err := normalizeHybridSort(&req); err != nil {
		bad(w, err)
		return
	}

	// ---- Step 0: Geo pre-filter ----
	area, err := s.geoAreaFor(&req)
	if err != nil {
		bad(w, err)
		return
	}
	if area.empty() {
		ok(w, emptyHybridResponse(&req))
		return
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
	merged, signalBreakdowns := s.fuseHybrid(r.Context(), &req, alpha, area != nil, ftsResults, vectorResults)
	merged = area.restrict(merged, &req)

	// ---- Step 4: Load full documents ----
	items := s.tracedLoadHybridDocs(r.Context(), req.Collection, merged, req.IncludeContent || req.wantsPassages())
	area.attachDistances(items) // loadHybridDocs may drop DistanceMeters
	items = s.finishHybridItems(&req, items)
	req.plan = plan
	s.recordSearch(r.Context(), vectorShape(vectorShapeInput{Mode: metrics.ModeHybrid, Collection: req.Collection,
		Requested: req.VectorAlgorithm, Ran: ranVectorAlgorithm(req.VectorAlgorithm, plan),
		FilterMeta: req.FilterMeta, Filter: req.Filter, Plan: plan, Results: len(items), Limit: req.limitReport})) // OPS-025
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

	resp := hybridResponse(&req, items, plan, alpha, signalBreakdowns)
	resp.Indexing = waited
	resp.ImageHits = imageHits
	resp.ContextTruncated = contextTruncated
	s.decorateHybridResponse(&resp, &req, start)
	ok(w, resp)
}

// checkHybridRequest validates a decoded request and resolves the parts the
// collection may default (groupBy, passage format). On failure it returns the
// reply to send: 400 for missing fields, 422 (SRCH-005) for a body that parsed
// but asks for something out of range.
func (s *Server) checkHybridRequest(req *HybridSearchRequest) (func(http.ResponseWriter, error), error) {
	if req.Collection == "" || req.Query == "" {
		return bad, errors.New("missing required fields: collection, query")
	}
	if err := ValidateOversample(req.Oversample); err != nil {
		return unprocessable, err
	}
	req.GroupBy = s.ResolveGroupBy(req.Collection, req.GroupBy)
	if err := req.GroupBy.Validate(); err != nil {
		return unprocessable, err
	}
	if err := req.ValidateIndexWait(); err != nil {
		return unprocessable, err
	}
	req.PassageOptions = s.ResolveFormat(req.Collection, req.PassageOptions)
	for _, validate := range []func() error{req.ValidatePassageOptions, req.ValidatePlanOptions,
		req.ValidateFeedback, req.ValidateDecomposition} {
		if err := validate(); err != nil {
			return unprocessable, err
		}
	}
	return nil, nil
}

// applyHybridDefaults fills in what the request left out and returns the
// alpha to blend with. RAG-001 inserts the collection profile between the
// request and each historical default, so a collection without one is
// unchanged.
func (s *Server) applyHybridDefaults(req *HybridSearchRequest) float64 {
	req.TopK, req.limitReport = s.ResolveTopK(req.Collection, req.TopK, 10)
	req.Algorithm = cmp.Or(req.Algorithm, "bm25")
	// SRCH-013: request, then collection profile, then flat.
	req.VectorAlgorithm = cmp.Or(s.ResolveVectorAlgorithm(req.Collection, req.VectorAlgorithm), "flat")
	req.Strategy = s.ResolveHybridStrategy(req.Collection, req.Strategy, "alpha")
	if req.RRFK <= 0 {
		req.RRFK = 60
	}
	req.Fuzzy = min(max(req.Fuzzy, 0), 2)
	return s.hybridAlpha(req)
}

// hybridAlpha resolves alpha once, so the request struct stays the caller's
// words and never becomes a scratch pad. Precedence: an explicit request
// alpha wins, then the collection's profile, then 0.5; an explicit 0 is a
// request, not an absence. The result is clamped to [0, 1].
func (s *Server) hybridAlpha(req *HybridSearchRequest) float64 {
	var alpha float64
	if req.Alpha != nil {
		alpha = *req.Alpha
	} else {
		alpha = s.ResolveHybridAlpha(req.Collection, 0, false, 0.5)
	}
	return min(max(alpha, 0), 1)
}

// checkHybridStrategy refuses a fusion strategy the handler cannot run.
func checkHybridStrategy(strategy string) error {
	switch strategy {
	case "alpha", "rrf", "weighted":
		return nil
	}
	return fmt.Errorf("unknown strategy: %s, available: alpha, rrf", strategy)
}

// normalizeHybridSort normalises and validates the sort mode. "distance" only
// makes sense when the request also carries a geo filter (otherwise every
// result's distanceMeters is zero and the ordering would be meaningless).
func normalizeHybridSort(req *HybridSearchRequest) error {
	switch req.Sort {
	case "", "combined":
		req.Sort = "combined"
	case "distance":
		if req.Geo == nil {
			return errors.New("sort=distance requires a geo filter")
		}
	default:
		return fmt.Errorf("unknown sort: %s, available: combined, distance", req.Sort)
	}
	return nil
}

// fuseHybrid merges the full-text and vector halves with the request's
// strategy, then applies per-query boosts. When a geo filter or grouping is
// active it merges an inflated TopK, so the spatial intersection and the
// grouping still have enough candidates; TopK is applied again after them.
func (s *Server) fuseHybrid(ctx context.Context, req *HybridSearchRequest, alpha float64, geoActive bool,
	ftsResults []fts.FTSResult, vectorResults []vector.VectorResult) ([]HybridSearchResultItem, []SignalBreakdown) {
	mergeTopK := req.TopK
	if geoActive || req.GroupBy != nil {
		mergeTopK = groupedCandidates(req.TopK)
	}
	var merged []HybridSearchResultItem
	var signalBreakdowns []SignalBreakdown
	fuse := s.Tracer.Stage(ctx, spanSearchFuse) // OPS-024
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
	return merged, signalBreakdowns
}

// finishHybridItems applies curation and grouping, marks documents whose
// embedding is still pending, and trims to TopK.
func (s *Server) finishHybridItems(req *HybridSearchRequest, items []HybridSearchResultItem) []HybridSearchResultItem {
	items = s.applyCurationHybrid(req.Collection, req.Query, items)
	items = groupHybridItems(req.Collection, items, req.GroupBy, req.TopK)
	for i := range items {
		items[i].PendingEmbedding = s.pendingEmbedding(req.Collection, items[i].Document.ID)
	}
	if req.TopK > 0 && len(items) > req.TopK {
		items = items[:req.TopK]
	}
	return items
}

// hybridGeoArea holds the documents inside a request's geo radius and each
// one's exact distance. A nil area means the request has no geo filter.
type hybridGeoArea struct {
	allowed map[string]bool
	dist    map[string]float64
}

// geoAreaFor asks the in-memory R-tree for the docIDs within the request's
// radius. They are not injected into FilterMeta (which is backed by on-disk
// meta indices); the set is used both to short-circuit and to attach
// distanceMeters to the final result items. It returns nil without a geo
// filter.
func (s *Server) geoAreaFor(req *HybridSearchRequest) (*hybridGeoArea, error) {
	if req.Geo == nil {
		return nil, nil
	}
	if s.GeoIndex == nil || !s.GeoIndex.IsReady() {
		return nil, errors.New("geo index not ready")
	}
	if !geo.ValidLatLng(req.Geo.Lat, req.Geo.Lng) || req.Geo.RadiusMeters <= 0 {
		return nil, errors.New("invalid geo filter: lat/lng/radiusMeters")
	}
	// A large topK is intentional — we want every candidate in the radius,
	// not just the TopK by distance, so the FTS/vector ranking can reorder
	// freely within the spatial envelope.
	hits := s.GeoIndex.Search(req.Collection, req.Geo.Lat, req.Geo.Lng, req.Geo.RadiusMeters, 1_000_000, nil)
	area := &hybridGeoArea{allowed: make(map[string]bool, len(hits)), dist: make(map[string]float64, len(hits))}
	for _, h := range hits {
		area.allowed[h.DocID] = true
		area.dist[h.DocID] = h.DistanceMeters
	}
	return area, nil
}

// empty reports a geo filter that matched no document at all.
func (a *hybridGeoArea) empty() bool {
	return a != nil && len(a.allowed) == 0
}

// restrict keeps only results inside the geo radius, attaches distanceMeters,
// optionally re-sorts by proximity, truncates back to TopK and re-ranks.
// Without a geo filter it returns merged unchanged.
func (a *hybridGeoArea) restrict(merged []HybridSearchResultItem, req *HybridSearchRequest) []HybridSearchResultItem {
	if a == nil {
		return merged
	}
	filtered := merged[:0]
	for _, m := range merged {
		if !a.allowed[m.Document.ID] {
			continue
		}
		m.DistanceMeters = a.dist[m.Document.ID]
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
	return merged
}

// attachDistances re-attaches distanceMeters to loaded items.
func (a *hybridGeoArea) attachDistances(items []HybridSearchResultItem) {
	if a == nil {
		return
	}
	for i := range items {
		items[i].DistanceMeters = a.dist[items[i].Document.ID]
	}
}

// emptyHybridResponse is the answer when the geo filter matched nothing.
func emptyHybridResponse(req *HybridSearchRequest) HybridSearchResponse {
	return HybridSearchResponse{
		Results:         []HybridSearchResultItem{},
		Total:           0,
		Strategy:        req.Strategy,
		FTSAlgorithm:    req.Algorithm,
		VectorAlgorithm: req.VectorAlgorithm,
		DistanceMetric:  "cosine",
		LimitReport:     req.limitReport,
	}
}

// hybridResponse builds the response body and echoes the parameters of the
// strategy that ran.
func hybridResponse(req *HybridSearchRequest, items []HybridSearchResultItem, plan *SearchPlan, alpha float64,
	signalBreakdowns []SignalBreakdown) HybridSearchResponse {
	resp := HybridSearchResponse{
		Results:         items,
		Total:           len(items),
		Strategy:        req.Strategy,
		FTSAlgorithm:    req.Algorithm,
		VectorAlgorithm: ranVectorAlgorithm(req.VectorAlgorithm, plan),
		DistanceMetric:  cmp.Or(req.DistanceMetric, "cosine"),
		Plan:            req.explained(plan),
		LimitReport:     req.limitReport,
	}
	switch req.Strategy {
	case "alpha":
		resp.Alpha = alpha
	case "rrf":
		resp.RRFK = req.RRFK
	case "weighted":
		resp.Alpha = alpha
		resp.Signals = req.Signals.Defaults()
		// The breakdown is trimmed alongside the results it explains: a
		// breakdown for a result the caller never received explains nothing.
		if len(signalBreakdowns) >= len(items) {
			resp.SignalBreakdown = signalBreakdowns[:len(items)]
		}
	}
	return resp
}

// decorateHybridResponse adds facets and search statistics, and counts the
// operation.
func (s *Server) decorateHybridResponse(resp *HybridSearchResponse, req *HybridSearchRequest, start time.Time) {
	if len(req.FacetBy) > 0 && len(resp.Results) > 0 {
		docs := make([]storage.Doc, len(resp.Results))
		for i, it := range resp.Results {
			docs[i] = it.Document
		}
		resp.Facets = computeFacets(docs, req.FacetBy, req.FacetMaxValues)
	}
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
}
