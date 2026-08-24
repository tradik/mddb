package main

import (
	"errors"
	"net/http"

	json "mddb/internal/jsonx"
)

// handleCuration multiplexes GET/POST/PUT/DELETE on /v1/curation.
// Query params drive method-specific behaviour: GET with ?id= returns a
// single rule, GET with ?collection= lists rules for a collection; POST
// creates, PUT updates, DELETE removes by id.
func (s *Server) handleCuration(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.handleCurationGet(w, r)
	case http.MethodPost:
		s.handleCurationCreate(w, r)
	case http.MethodPut:
		s.handleCurationUpdate(w, r)
	case http.MethodDelete:
		s.handleCurationDelete(w, r)
	default:
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleCurationGet(w http.ResponseWriter, r *http.Request) {
	if s.CurationManager == nil {
		bad(w, errors.New("curation not initialized"))
		return
	}
	id := r.URL.Query().Get("id")
	if id != "" {
		rule, found := s.CurationManager.Get(id)
		if !found {
			http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
			return
		}
		if s.AuthManager != nil {
			if err := s.AuthManager.CheckPermission(r.Context(), rule.Collection, PermRead); err != nil {
				http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
				return
			}
		}
		ok(w, rule)
		return
	}

	coll := r.URL.Query().Get("collection")
	if coll == "" {
		// No filter → list everything (admin-only)
		if s.AuthManager != nil {
			if err := s.AuthManager.CheckPermission(r.Context(), "*", PermRead); err != nil {
				http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
				return
			}
		}
		rules := s.CurationManager.ListAll()
		if tenant := TenantFromContext(r.Context()); tenant != "" {
			scoped := rules[:0:0]
			for _, rule := range rules {
				if CollectionInTenant(tenant, rule.Collection) {
					scoped = append(scoped, rule)
				}
			}
			rules = scoped
		}
		ok(w, map[string]any{"rules": rules, "total": len(rules)})
		return
	}
	if s.AuthManager != nil {
		if err := s.AuthManager.CheckPermission(r.Context(), coll, PermRead); err != nil {
			http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
			return
		}
	}
	rules := s.CurationManager.ListByCollection(coll)
	ok(w, map[string]any{"rules": rules, "total": len(rules), "collection": coll})
}

func (s *Server) handleCurationCreate(w http.ResponseWriter, r *http.Request) {
	if s.Mode == ModeRead {
		http.Error(w, `{"error":"read-only mode"}`, http.StatusForbidden)
		return
	}
	if s.CurationManager == nil {
		bad(w, errors.New("curation not initialized"))
		return
	}
	var rule CurationRule
	if err := json.NewDecoder(r.Body).Decode(&rule); err != nil {
		bad(w, err)
		return
	}
	if s.AuthManager != nil {
		if err := s.AuthManager.CheckPermission(r.Context(), rule.Collection, PermWrite); err != nil {
			http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
			return
		}
	}
	// Force a new ID on POST so clients can't collide with existing rules by passing one in.
	rule.ID = ""
	if err := s.CurationManager.Set(&rule); err != nil {
		bad(w, err)
		return
	}
	if s.Metrics != nil {
		s.Metrics.IncOp("curation_create", rule.Collection)
	}
	ok(w, rule)
}

func (s *Server) handleCurationUpdate(w http.ResponseWriter, r *http.Request) {
	if s.Mode == ModeRead {
		http.Error(w, `{"error":"read-only mode"}`, http.StatusForbidden)
		return
	}
	if s.CurationManager == nil {
		bad(w, errors.New("curation not initialized"))
		return
	}
	var rule CurationRule
	if err := json.NewDecoder(r.Body).Decode(&rule); err != nil {
		bad(w, err)
		return
	}
	if rule.ID == "" {
		bad(w, errors.New("missing id on update"))
		return
	}
	prev, exists := s.CurationManager.Get(rule.ID)
	if !exists {
		http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
		return
	}
	// Preserve CreatedAt, disallow collection moves to keep rule attribution stable.
	if rule.Collection == "" {
		rule.Collection = prev.Collection
	}
	rule.CreatedAt = prev.CreatedAt
	if s.AuthManager != nil {
		if err := s.AuthManager.CheckPermission(r.Context(), rule.Collection, PermWrite); err != nil {
			http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
			return
		}
	}
	if err := s.CurationManager.Set(&rule); err != nil {
		bad(w, err)
		return
	}
	if s.Metrics != nil {
		s.Metrics.IncOp("curation_update", rule.Collection)
	}
	ok(w, rule)
}

func (s *Server) handleCurationDelete(w http.ResponseWriter, r *http.Request) {
	if s.Mode == ModeRead {
		http.Error(w, `{"error":"read-only mode"}`, http.StatusForbidden)
		return
	}
	if s.CurationManager == nil {
		bad(w, errors.New("curation not initialized"))
		return
	}
	id := r.URL.Query().Get("id")
	if id == "" {
		bad(w, errors.New("missing id"))
		return
	}
	rule, exists := s.CurationManager.Get(id)
	if !exists {
		http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
		return
	}
	if s.AuthManager != nil {
		if err := s.AuthManager.CheckPermission(r.Context(), rule.Collection, PermWrite); err != nil {
			http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
			return
		}
	}
	if err := s.CurationManager.Delete(id); err != nil {
		bad(w, err)
		return
	}
	if s.Metrics != nil {
		s.Metrics.IncOp("curation_delete", rule.Collection)
	}
	ok(w, map[string]string{"status": "deleted", "id": id})
}
