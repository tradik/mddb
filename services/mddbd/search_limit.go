package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

// Server-side cap on how many results one search may return (#368).
//
// A search used to return as many results as its caller asked for. One public
// POST /v1/fts with limit 100000 returned 21 MB, and a rate limit that counts
// requests does not count bytes. Every endpoint that takes a result limit —
// limit, topK, topN — now passes it through CapLimit, so one ceiling holds on
// REST, MCP, gRPC and GraphQL alike.
//
// The ceiling is the smallest of three numbers:
//   - MDDB_SEARCH_MAX_LIMIT, the operator's server-wide cap (default 1000);
//   - the collection's retrieval.maxTopK, when set;
//   - an endpoint's own bound, where it has one (code symbol lookup: 200 per
//     relation).
//
// A collection's maxTopK can only LOWER the server-wide cap, never raise it.
// The server value is the operator's safety limit for a public deployment,
// while a collection config can be written by anyone holding config write
// access on that collection; letting that raise the ceiling would let a
// collection owner switch off the operator's protection.

// searchMaxLimitEnv names the environment variable holding the server-wide cap.
const searchMaxLimitEnv = "MDDB_SEARCH_MAX_LIMIT"

const (
	// defaultSearchMaxLimit is the server-wide cap when the variable is unset.
	defaultSearchMaxLimit = 1000
	// searchMaxLimitCeiling bounds what an operator may configure, and what a
	// collection's maxTopK may hold. It keeps every limit inside int32 for
	// the gRPC fields that carry it.
	searchMaxLimitCeiling = 1_000_000
	// maxHighlightFragments and maxHighlightFragmentSize bound a full-text
	// search's highlights per result: the fragments asked for and the
	// characters in each. The defaults (3 and 150) are well inside them.
	maxHighlightFragments    = 20
	maxHighlightFragmentSize = 1000
)

// LimitReport tells a caller that the result count it asked for was lowered
// to the server's cap. Both fields are omitted when nothing was capped, so a
// response under the cap is byte-for-byte what it was before.
type LimitReport struct {
	// Limit is the result count the search actually used; set only when
	// LimitCapped is true.
	Limit int `json:"limit,omitempty"`
	// LimitCapped reports that the requested (or default) limit was above
	// the cap and was lowered to Limit.
	LimitCapped bool `json:"limitCapped,omitempty"`
}

// parseSearchMaxLimit reads MDDB_SEARCH_MAX_LIMIT. Empty means the default; any
// other value must be a whole number from 1 to searchMaxLimitCeiling. A bad
// value is an error that names it — guessing would either remove the
// protection or break every search.
func parseSearchMaxLimit(raw string) (int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return defaultSearchMaxLimit, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 || n > searchMaxLimitCeiling {
		return 0, fmt.Errorf("invalid %s %q: must be a whole number from 1 to %d", searchMaxLimitEnv, raw, searchMaxLimitCeiling)
	}
	return n, nil
}

// searchMaxLimit is the server-wide cap; a Server built without one (tests,
// embedded use) gets the default.
func (s *Server) searchMaxLimit() int {
	if s == nil || s.SearchMaxLimit <= 0 {
		return defaultSearchMaxLimit
	}
	return s.SearchMaxLimit
}

// limitCeiling is the most results a search of collection may return: the
// server-wide cap, lowered by the collection's maxTopK and by endpointMax
// (0 = the endpoint has no bound of its own).
func (s *Server) limitCeiling(collection string, endpointMax int) int {
	ceiling := s.searchMaxLimit()
	if p := s.RetrievalProfile(collection); p != nil && p.MaxTopK > 0 {
		ceiling = min(ceiling, p.MaxTopK)
	}
	if endpointMax > 0 {
		ceiling = min(ceiling, endpointMax)
	}
	return ceiling
}

// CapLimit bounds an already-resolved result limit n by the ceiling for
// collection and reports whether it had to. It is the one cap implementation:
// ResolveTopK calls it, and so does every endpoint that resolves its default
// differently (document listing, autocomplete, geo, code symbols, eval).
func (s *Server) CapLimit(collection string, n, endpointMax int) (int, LimitReport) {
	ceiling := s.limitCeiling(collection, endpointMax)
	if n <= ceiling {
		return n, LimitReport{}
	}
	return ceiling, LimitReport{Limit: ceiling, LimitCapped: true}
}

// positiveOr is n, or fallback when n is not positive — for a request field
// where 0 means "no limit".
func positiveOr(n, fallback int) int {
	if n > 0 {
		return n
	}
	return fallback
}

// capCrossLimit is CapLimit for a search over several collections: the
// strictest target's ceiling applies, so a collection that lowered its
// maxTopK cannot be read past it through a cross-collection search.
func (s *Server) capCrossLimit(targets []string, n int) (int, LimitReport) {
	var rep LimitReport
	for _, coll := range append([]string{""}, targets...) {
		var r LimitReport
		if n, r = s.CapLimit(coll, n, 0); r.LimitCapped {
			rep = r
		}
	}
	return n, rep
}

// writeLimitHeaders reports a capped limit on a response whose body is a bare
// JSON array (GET/POST /v1/search), where there is no object to carry the
// fields.
func writeLimitHeaders(w http.ResponseWriter, rep LimitReport) {
	if !rep.LimitCapped {
		return
	}
	w.Header().Set("X-Limit-Capped", "true")
	w.Header().Set("X-Limit", strconv.Itoa(rep.Limit))
}

// mcpLimitContent is the MCP structuredContent for a tool result whose limit
// the server's cap lowered: {"limitCapped": true, "limit": n}. Nil for any
// other result. The substring test keeps the common, uncapped answer from
// being parsed a second time.
func mcpLimitContent(result string) map[string]any {
	if !strings.Contains(result, `"limitCapped"`) {
		return nil
	}
	var rep LimitReport
	if json.Unmarshal([]byte(result), &rep) != nil || !rep.LimitCapped {
		return nil
	}
	return map[string]any{"limitCapped": true, "limit": rep.Limit}
}
