package main

import (
	"errors"
	"net/http"
	"strings"

	json "mddb/internal/jsonx"
)

// POST /v1/web-search (#449): the external web search engine contract of
// Open WebUI — {"query", "count"} in, a JSON array of {"link", "title",
// "snippet"} out — answered from the collections the caller may read.
//
// It is cross-collection search as /v1/cross-search runs it (fts by default,
// hybrid with ?searchType=hybrid), not an engine of its own. The admin of the
// calling application sets only a URL and a key, so the URL's query string is
// the per-deployment knob: ?collections=docs,kb picks the collections.

// webSearchCollectionsEnv narrows the default collections server-wide.
const webSearchCollectionsEnv = "MDDB_WEBSEARCH_COLLECTIONS"

// webSearchRequest is the body Open WebUI sends.
type webSearchRequest struct {
	Query string `json:"query"`
	Count int    `json:"count"`
}

// webSearchResult is one entry of the answer.
type webSearchResult struct {
	Link    string `json:"link"`
	Title   string `json:"title"`
	Snippet string `json:"snippet"`
}

// handleWebSearch answers POST /v1/web-search.
func (s *Server) handleWebSearch(w http.ResponseWriter, r *http.Request) {
	if !allowMethod(w, r, http.MethodPost) {
		return
	}
	var req webSearchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	searchType, err := webSearchType(r.URL.Query().Get("searchType"))
	if err != nil || strings.TrimSpace(req.Query) == "" {
		writeAPIError(w, http.StatusBadRequest, errors.Join(err, emptyQueryError(req.Query)).Error())
		return
	}
	targets, status, err := s.webSearchTargets(r)
	if err != nil {
		writeWebSearchRefusal(w, status, err)
		return
	}
	ctx, release, err := s.admitRequest(r.Context(), PermRead) // GO-054: the tenant's request limits
	if err != nil {
		writeTenantLimit(w, err) // a read is refused only by a rate limit: 429
		return
	}
	defer release()
	topK, _ := s.crossTopK(targets, req.Count)
	items := s.crossTextItems(ctx, crossTextParams{SearchType: searchType, Query: req.Query, Targets: targets,
		TopK: topK, IncludeContent: true})
	ok(w, s.webSearchResults(r, req.Query, items))
}

// webSearchType is the URL's searchType: fts (the default) or hybrid.
func webSearchType(v string) (string, error) {
	switch v {
	case "", CrossSearchFTS:
		return CrossSearchFTS, nil
	case CrossSearchHybrid:
		return CrossSearchHybrid, nil
	}
	return "", errors.New("searchType must be fts or hybrid")
}

// emptyQueryError is the error for a blank query, nil otherwise.
func emptyQueryError(q string) error {
	if strings.TrimSpace(q) == "" {
		return errors.New("query is required")
	}
	return nil
}

// writeWebSearchRefusal answers a target selection that failed: the access
// gate's bodies for 401 and 403, {"error"} otherwise.
func writeWebSearchRefusal(w http.ResponseWriter, status int, err error) {
	if status == http.StatusUnauthorized || status == http.StatusForbidden {
		writeAccessDenied(w, err)
		return
	}
	writeAPIError(w, status, err.Error())
}
