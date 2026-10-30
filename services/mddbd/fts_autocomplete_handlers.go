package main

import (
	"errors"
	"mddb/internal/fts"
	"net/http"
)

// AutocompleteResponse is returned by the HTTP handler.
type AutocompleteResponse struct {
	Items       []fts.AutocompleteItem `json:"items"`
	Total       int                    `json:"total"`
	Query       string                 `json:"query"`
	Field       string                 `json:"field,omitempty"`
	LimitReport                        // limit, limitCapped: set when the server's cap lowered the request's limit (#368)
}

// handleAutocomplete serves GET /v1/autocomplete?collection=X&q=mar&field=Y&topN=10.
func (s *Server) handleAutocomplete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.FTSIndex == nil {
		bad(w, errors.New("full-text search not initialized"))
		return
	}

	collection := r.URL.Query().Get("collection")
	query := r.URL.Query().Get("q")
	field := r.URL.Query().Get("field")
	topN := parseIntParam(r, "topN", 10)

	if collection == "" {
		bad(w, errors.New("missing collection"))
		return
	}
	if query == "" {
		// Empty prefix returns empty result set rather than 400 — saves
		// client-side guards on every keystroke that clears the input.
		ok(w, AutocompleteResponse{Items: []fts.AutocompleteItem{}, Query: query, Field: field})
		return
	}

	resp, err := s.autocomplete(collection, query, field, topN)
	if err != nil {
		bad(w, err)
		return
	}
	if s.Metrics != nil {
		s.Metrics.IncOp("autocomplete", field)
	}
	ok(w, resp)
}

// autocomplete is the prefix lookup behind GET /v1/autocomplete and the MCP
// autocomplete tool. topN is capped like every search limit (#368), and the
// response says when it was.
func (s *Server) autocomplete(collection, query, field string, topN int) (AutocompleteResponse, error) {
	topN, limitReport := s.CapLimit(collection, topN, 0)
	items, err := s.FTSIndex.Autocomplete(collection, query, field, topN)
	if err != nil {
		return AutocompleteResponse{}, err
	}
	return AutocompleteResponse{Items: items, Total: len(items), Query: query, Field: field, LimitReport: limitReport}, nil
}

// parseIntParam reads a URL query parameter as int with a fallback. Used for
// numeric knobs (topN, limit) where zero/negative means "use default".
func parseIntParam(r *http.Request, key string, def int) int {
	raw := r.URL.Query().Get(key)
	if raw == "" {
		return def
	}
	n := 0
	for _, ch := range raw {
		if ch < '0' || ch > '9' {
			return def
		}
		n = n*10 + int(ch-'0')
	}
	if n <= 0 {
		return def
	}
	return n
}
