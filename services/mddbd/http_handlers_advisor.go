package main

import (
	"net/http"

	json "mddb/internal/jsonx"
)

// SRCH-010: an endpoint that answers "how should I search this collection?"
//
// Read-only, and it changes nothing by itself. A caller that agrees with the
// answer can PUT the returned retrievalProfile to /v1/collection-config, and
// then every client of that collection inherits it (RAG-001).

// SearchAdvisorRequest asks for advice about one collection.
type SearchAdvisorRequest struct {
	Collection string `json:"collection"`
	// Apply writes the recommendation into the collection's retrieval profile
	// instead of only returning it. Off by default: advice that reconfigures
	// your server on the way past is not advice.
	Apply bool `json:"apply,omitempty"`
}

// handleSearchAdvisor answers GET/POST /v1/search-advisor.
func (s *Server) handleSearchAdvisor(w http.ResponseWriter, r *http.Request) {
	var req SearchAdvisorRequest

	switch r.Method {
	case http.MethodGet:
		req.Collection = r.URL.Query().Get("collection")
		req.Apply = r.URL.Query().Get("apply") == "true"
	case http.MethodPost:
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			bad(w, err)
			return
		}
	default:
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	if req.Collection == "" {
		bad(w, errMissingCollection)
		return
	}

	if s.AuthManager != nil {
		if err := s.AuthManager.CheckPermission(r.Context(), req.Collection, PermRead); err != nil {
			http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
			return
		}
	}
	if s.Metrics != nil {
		s.Metrics.IncOp("search_advisor", req.Collection)
	}

	rec, err := s.RecommendSearch(req.Collection)
	if err != nil {
		bad(w, err)
		return
	}

	if req.Apply {
		// Writing a profile is a write, and needs the permission a write needs
		// — reading advice does not.
		if s.Mode == ModeRead {
			http.Error(w, `{"error":"read-only mode"}`, http.StatusForbidden)
			return
		}
		if s.AuthManager != nil {
			if err := s.AuthManager.CheckPermission(r.Context(), req.Collection, PermWrite); err != nil {
				http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
				return
			}
		}
		if err := s.applyRecommendation(req.Collection, rec); err != nil {
			bad(w, err)
			return
		}
	}

	ok(w, rec)
}

// applyRecommendation stores the advice as the collection's retrieval profile.
//
// Merges into the existing configuration rather than replacing it: a
// collection carries an encryption flag, a response prompt and a WordPress
// target that have nothing to do with retrieval, and rebuilding the struct
// from a partial view is how those get silently cleared.
func (s *Server) applyRecommendation(collection string, rec *SearchRecommendation) error {
	if s.CollectionManager == nil || rec == nil || rec.RetrievalProfile == nil {
		return nil
	}

	cfg, found := s.CollectionManager.Get(collection)
	if !found || cfg == nil {
		cfg = &CollectionConfig{Type: "default"}
	} else {
		// Copy, so a failed write cannot leave the cached config half-changed.
		clone := *cfg
		cfg = &clone
	}

	cfg.Retrieval = rec.RetrievalProfile
	return s.CollectionManager.Set(collection, cfg)
}
