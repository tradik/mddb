package main

import (
	"context"
	"fmt"
	"mddb/internal/fts"
	"mddb/internal/storage"
	"mddb/internal/vector"
	"sort"

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
		return s.runPlanned(ps, indexQuery{Collection: req.Collection, Vector: v, TopK: searchTopK, Threshold: req.Threshold,
			Allowed: allowedIDs, Filtered: filtered, Metric: metric})
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
		for _, item := range items {
			doc, ok := liveHybridDoc(bDocs, collection, item.Document.ID)
			if !ok {
				continue
			}
			if !includeContent {
				doc.ContentMD = ""
			}
			results = append(results, HybridSearchResultItem{
				Document:      doc,
				CombinedScore: item.CombinedScore,
				FTSScore:      item.FTSScore,
				VectorScore:   item.VectorScore,
				MatchedTerms:  item.MatchedTerms,
				Rank:          len(results) + 1,
			})
		}
		return nil
	})
	return results
}

// liveHybridDoc reads a hit's document; false when it is gone, unreadable
// or expired.
func liveHybridDoc(bDocs *bolt.Bucket, collection, id string) (storage.Doc, bool) {
	v := bDocs.Get(storage.DocKey(collection, id))
	if v == nil {
		return storage.Doc{}, false
	}
	docPtr, err := loadDoc(v)
	if err != nil || (docPtr.ExpiresAt > 0 && docPtr.ExpiresAt < currentUnix()) {
		return storage.Doc{}, false
	}
	return *docPtr, true
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
