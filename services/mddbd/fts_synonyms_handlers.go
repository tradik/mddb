package main

import (
	"fmt"
	"net/http"

	json "mddb/internal/jsonx"
)

// SynonymRequest is the HTTP request for synonym CRUD.
type SynonymRequest struct {
	Collection string   `json:"collection"`
	Term       string   `json:"term"`
	Synonyms   []string `json:"synonyms"`
}

// SynonymEntry represents a single synonym mapping for API responses.
type SynonymEntry struct {
	Term     string   `json:"term"`
	Synonyms []string `json:"synonyms"`
}

// SynonymListResponse is the HTTP response for listing synonyms.
type SynonymListResponse struct {
	Collection string         `json:"collection"`
	Entries    []SynonymEntry `json:"entries"`
	Total      int            `json:"total"`
}

// --- HTTP handlers ---

func (s *Server) handleSynonyms(w http.ResponseWriter, r *http.Request) {
	if s.SynonymManager == nil {
		bad(w, fmt.Errorf("synonym manager not initialized"))
		return
	}

	switch r.Method {
	case http.MethodGet:
		s.handleSynonymsList(w, r)
	case http.MethodPost:
		s.handleSynonymsSet(w, r)
	case http.MethodDelete:
		s.handleSynonymsDelete(w, r)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleSynonymsList(w http.ResponseWriter, r *http.Request) {
	collection := r.URL.Query().Get("collection")
	if collection == "" {
		bad(w, fmt.Errorf("missing required parameter: collection"))
		return
	}

	entries := s.SynonymManager.List(collection)
	resp := SynonymListResponse{
		Collection: collection,
		Entries:    make([]SynonymEntry, 0, len(entries)),
		Total:      len(entries),
	}
	for term, syns := range entries {
		resp.Entries = append(resp.Entries, SynonymEntry{Term: term, Synonyms: syns})
	}
	ok(w, resp)
}

func (s *Server) handleSynonymsSet(w http.ResponseWriter, r *http.Request) {
	var req SynonymRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		bad(w, err)
		return
	}
	if req.Collection == "" || req.Term == "" {
		bad(w, fmt.Errorf("missing required fields: collection, term"))
		return
	}
	if len(req.Synonyms) == 0 {
		bad(w, fmt.Errorf("synonyms list cannot be empty"))
		return
	}

	if err := s.SynonymManager.Set(req.Collection, req.Term, req.Synonyms); err != nil {
		bad(w, err)
		return
	}
	ok(w, map[string]string{"status": "ok"})
}

func (s *Server) handleSynonymsDelete(w http.ResponseWriter, r *http.Request) {
	var req SynonymRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		bad(w, err)
		return
	}
	if req.Collection == "" || req.Term == "" {
		bad(w, fmt.Errorf("missing required fields: collection, term"))
		return
	}

	if err := s.SynonymManager.Delete(req.Collection, req.Term); err != nil {
		bad(w, err)
		return
	}
	ok(w, map[string]string{"status": "ok"})
}
