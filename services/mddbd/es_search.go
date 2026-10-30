package main

import (
	"context"
	"errors"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"mddb/internal/storage"
)

// Execution of /es/{index}/_search and _count (INT-019). A parsed request is
// sent down exactly one of MDDB's search paths:
//
//	text, no knn  → searchFTS (POST /v1/fts), facetScope "all" for totals and aggs
//	knn, no text  → POST /v1/vector-search, algorithm "auto"
//	knn and text  → POST /v1/hybrid-search, strategy "rrf"
//	neither       → searchDocuments (POST /v1/search)
//
// from+size passes through CapLimit (#368): a capped page is reported with
// X-Limit-Capped/X-Limit, a Warning header the clients surface as a warning,
// and limitCapped/limit in the body.

// esHit is one document of a result page.
type esHit struct {
	Doc       storage.Doc
	Score     *float64
	Highlight []string
}

// esResult is what a search path returned, before paging.
type esResult struct {
	Hits   []esHit
	Total  int
	Facets FacetResult
	Limit  LimitReport
}

// esSearch answers _search, or _count when count is set.
func (s *Server) esSearch(w http.ResponseWriter, r *http.Request, index string, count bool) {
	start := time.Now()
	if !s.permitted(w, r, index, PermRead) {
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		esBodyError(w, err)
		return
	}
	spec, err := parseESSearch(r, body, count)
	if err != nil {
		esFail(w, err)
		return
	}
	if count {
		spec.Size = 0
	}
	res, err := s.esRun(r, index, spec)
	if err != nil {
		esFail(w, err)
		return
	}
	if count {
		esJSON(w, http.StatusOK, map[string]any{"count": res.Total, "_shards": esShards()})
		return
	}
	writeLimitHeaders(w, res.Limit)
	if res.Limit.LimitCapped {
		w.Header().Set("Warning", `299 MDDB "from+size capped to `+strconv.Itoa(res.Limit.Limit)+
			` by MDDB_SEARCH_MAX_LIMIT or the collection's maxTopK"`)
	}
	esJSON(w, http.StatusOK, esSearchResponse(index, spec, res, time.Since(start)))
}

// esBodyError answers a body that could not be read: 413 over the body cap.
func esBodyError(w http.ResponseWriter, err error) {
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		esError(w, http.StatusRequestEntityTooLarge, "content_too_long_exception", err.Error())
		return
	}
	esError(w, http.StatusBadRequest, "illegal_argument_exception", err.Error())
}

// esRun picks the search path and pages its result.
func (s *Server) esRun(r *http.Request, index string, spec *esSearchSpec) (*esResult, error) {
	window, rep := s.CapLimit(index, spec.From+spec.Size, 0)
	q := spec.Query
	if spec.KNN != nil {
		if err := q.mergeFilter(spec.KNN.Filter); err != nil {
			return nil, err
		}
	}
	scored := q.hasText() || spec.KNN != nil
	if spec.SortField != "" && scored {
		return nil, dslErr("sort", "a query with text or knn is ranked by score; field sorts apply to filter-only queries")
	}
	if spec.Highlight != nil && (!q.hasText() || spec.KNN != nil) {
		return nil, dslErr("highlight", "needs a text query without knn")
	}
	var res *esResult
	var err error
	switch {
	case spec.KNN != nil && q.hasText():
		res, err = s.esHybrid(r, index, spec, window)
	case spec.KNN != nil:
		res, err = s.esVector(r, index, spec, window)
	case q.hasText():
		res, err = s.esFTS(r.Context(), index, spec, window)
	default:
		res, err = s.esList(r.Context(), index, spec, window)
	}
	if err != nil {
		return nil, err
	}
	if rep.LimitCapped {
		res.Limit = rep
	}
	lo := min(spec.From, len(res.Hits))
	res.Hits = res.Hits[lo:max(lo, min(window, len(res.Hits)))]
	return res, nil
}

// esFTS runs a text query through searchFTS.
func (s *Server) esFTS(ctx context.Context, index string, spec *esSearchSpec, window int) (*esResult, error) {
	query, mode, algorithm := spec.Query.FTSQuery()
	req := FTSSearchRequest{
		Collection: index, Query: query, Mode: mode, Algorithm: algorithm, Limit: max(window, 1),
		Filter: spec.Query.Filter, RangeMeta: spec.Query.Ranges,
		FacetBy: esAggFields(spec.Aggs), FacetScope: facetScopeAll,
	}
	if h := spec.Highlight; h != nil {
		req.Highlight, req.HighlightTag, req.MaxHighlights, req.FragmentSize = true, h.Tag, h.Fragments, h.FragmentSize
	}
	resp, err := s.shapedFTS(ctx, req, time.Now())
	if err != nil {
		return nil, err
	}
	res := &esResult{Total: resp.Matched, Facets: resp.Facets, Limit: resp.LimitReport}
	for _, it := range resp.Results {
		hit := esHit{Doc: it.Document, Score: esScore(it.Score)}
		for _, h := range it.Highlights {
			hit.Highlight = append(hit.Highlight, h.Fragment)
		}
		res.Hits = append(res.Hits, hit)
	}
	return res, nil
}

// esList answers a filter-only query from the metadata listing. Timestamp
// ranges and aggs need every match, which the listing loads anyway to sort.
func (s *Server) esList(ctx context.Context, index string, spec *esSearchSpec, window int) (*esResult, error) {
	filter, docRanges := rangeMetaAsFilter(spec.Query.Filter, spec.Query.Ranges)
	whole := len(docRanges) > 0 || len(spec.Aggs) > 0
	limit := window
	if whole {
		limit = math.MaxInt
	}
	docs, total, err := s.shapedListing(ctx, index, filter, spec.SortField, spec.SortAsc, 0, limit, LimitReport{})
	if err != nil {
		return nil, err
	}
	res := &esResult{Total: total}
	if whole {
		kept := docs[:0]
		for i := range docs {
			if esMatchRanges(&docs[i], docRanges) {
				kept = append(kept, docs[i])
			}
		}
		res.Total = len(kept)
		res.Facets = computeFacets(kept, esAggFields(spec.Aggs), 0)
		docs = kept[:min(window, len(kept))]
	}
	for _, d := range docs {
		hit := esHit{Doc: d}
		if spec.SortField == "" {
			hit.Score = esScore(1)
		}
		res.Hits = append(res.Hits, hit)
	}
	return res, nil
}

// esMatchRanges reports whether doc meets every range.
func esMatchRanges(doc *storage.Doc, ranges []RangeFilter) bool {
	for _, rf := range ranges {
		if !matchRangeFilter(doc, rf) {
			return false
		}
	}
	return true
}

// esVectorFilter is the filter a vector or hybrid search can take: metadata
// conditions only, as those paths have no timestamp ranges.
func esVectorFilter(q *esTranslated) (MetaFilter, error) {
	filter, docRanges := rangeMetaAsFilter(q.Filter, q.Ranges)
	if len(docRanges) > 0 {
		return nil, dslErr("range", "a range on addedAt or updatedAt cannot be combined with knn")
	}
	return filter, nil
}

// esVector runs a knn-only search through POST /v1/vector-search.
func (s *Server) esVector(r *http.Request, index string, spec *esSearchSpec, window int) (*esResult, error) {
	filter, err := esVectorFilter(spec.Query)
	if err != nil {
		return nil, err
	}
	req := VectorSearchRequest{
		Collection: index, Query: spec.KNN.Text, QueryVector: spec.KNN.Vector, TopK: esTopK(spec.KNN, window),
		Filter: filter, IncludeContent: true, Algorithm: "auto", RetrievalMode: RetrievalModeParent,
	}
	var resp VectorSearchResponseHTTP
	if err := esCall(r, s.handleVectorSearch, req, &resp); err != nil {
		return nil, err
	}
	res := &esResult{Total: len(resp.Results), Limit: resp.LimitReport}
	docs := make([]storage.Doc, 0, len(resp.Results))
	for _, it := range resp.Results {
		res.Hits = append(res.Hits, esHit{Doc: it.Document, Score: esScore(float64(it.Score))})
		docs = append(docs, it.Document)
	}
	res.Facets = computeFacets(docs, esAggFields(spec.Aggs), 0)
	return res, nil
}

// esHybrid runs knn plus a text query through POST /v1/hybrid-search. The
// hybrid path embeds its one query text, so the text must be a single match
// and the knn must ask for the same text to be embedded.
func (s *Server) esHybrid(r *http.Request, index string, spec *esSearchSpec, window int) (*esResult, error) {
	q, knn := spec.Query, spec.KNN
	switch {
	case q.Simple == "":
		return nil, dslErr("knn", "with a text query needs that query to be a single match: hybrid search takes one plain query")
	case knn.Vector != nil:
		return nil, dslErr("knn.query_vector", "with a text query MDDB embeds the query text itself; use query_vector_builder")
	case strings.Join(esWords(knn.Text), " ") != q.Simple:
		return nil, dslErr("knn.query_vector_builder", "model_text must be the match text: hybrid search embeds the one query")
	}
	filter, err := esVectorFilter(q)
	if err != nil {
		return nil, err
	}
	req := HybridSearchRequest{
		Collection: index, Query: q.Simple, TopK: esTopK(knn, window), Strategy: "rrf",
		VectorAlgorithm: "auto", Filter: filter, IncludeContent: true,
	}
	var resp HybridSearchResponse
	if err := esCall(r, s.handleHybridSearch, req, &resp); err != nil {
		return nil, err
	}
	res := &esResult{Total: len(resp.Results), Limit: resp.LimitReport}
	docs := make([]storage.Doc, 0, len(resp.Results))
	for _, it := range resp.Results {
		res.Hits = append(res.Hits, esHit{Doc: it.Document, Score: esScore(it.CombinedScore)})
		docs = append(docs, it.Document)
	}
	res.Facets = computeFacets(docs, esAggFields(spec.Aggs), 0)
	return res, nil
}

// esTopK is k when the request set one, else the page window.
func esTopK(knn *esKNN, window int) int {
	if knn.K > 0 {
		return knn.K
	}
	return window
}

// esScore boxes a score for a hit.
func esScore(v float64) *float64 { return &v }

// esAggFields lists the metadata keys the aggregations count.
func esAggFields(aggs []esTermsAgg) []string {
	fields := make([]string, 0, len(aggs))
	for _, a := range aggs {
		fields = append(fields, a.Field)
	}
	return fields
}
