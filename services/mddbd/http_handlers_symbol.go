package main

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	json "mddb/internal/jsonx"
)

// handleSymbol serves GET/POST /v1/symbol (CODE-008): where a symbol is
// defined, used, what calls it and what it calls, its tests and neighbours.
// GET for curl and browsers, POST for names that do not travel well in a URL.
func (s *Server) handleSymbol(w http.ResponseWriter, r *http.Request) {
	var req SymbolRequest
	switch r.Method {
	case http.MethodGet:
		q := r.URL.Query()
		req.Collection, req.Name = q.Get("collection"), q.Get("name")
		if inc := q.Get("include"); inc != "" {
			req.Include = strings.Split(inc, ",")
		}
		req.Limit, _ = strconv.Atoi(q.Get("limit"))
		req.IncludeContent = q.Get("includeContent") == "true"
	case http.MethodPost:
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			bad(w, err)
			return
		}
	default:
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	if s.AuthManager != nil && req.Collection != "" {
		if err := s.AuthManager.CheckPermission(r.Context(), req.Collection, PermRead); err != nil {
			http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
			return
		}
	}
	if s.Metrics != nil {
		s.Metrics.IncOp("code_symbol", req.Collection)
	}
	res, err := s.LookupSymbol(req)
	if err != nil {
		if errors.Is(err, errSymbolRequest) {
			bad(w, err)
			return
		}
		unprocessable(w, err)
		return
	}
	ok(w, res)
}
