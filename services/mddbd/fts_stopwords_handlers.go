package main

import (
	"fmt"
	"net/http"

	json "mddb/internal/jsonx"
)

// StopWordRequest is the HTTP request for stop word CRUD.
type StopWordRequest struct {
	Collection string   `json:"collection"`
	Words      []string `json:"words"`
}

// StopWordEntry represents a single stop word for API responses.
type StopWordEntry struct {
	Word      string `json:"word"`
	IsDefault bool   `json:"isDefault"`
}

// StopWordListResponse is the HTTP response for listing stop words.
type StopWordListResponse struct {
	Collection string          `json:"collection"`
	Lang       string          `json:"lang"`
	Entries    []StopWordEntry `json:"entries"`
	Total      int             `json:"total"`
	Defaults   int             `json:"defaults"`
	Custom     int             `json:"custom"`
}

func (s *Server) handleStopWords(w http.ResponseWriter, r *http.Request) {
	if s.StopWordManager == nil {
		bad(w, fmt.Errorf("stop word manager not initialized"))
		return
	}

	switch r.Method {
	case http.MethodGet:
		s.handleStopWordsList(w, r)
	case http.MethodPost:
		s.handleStopWordsAdd(w, r)
	case http.MethodDelete:
		s.handleStopWordsDelete(w, r)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleStopWordsList(w http.ResponseWriter, r *http.Request) {
	collection := r.URL.Query().Get("collection")
	if collection == "" {
		bad(w, fmt.Errorf("missing required parameter: collection"))
		return
	}
	lang := r.URL.Query().Get("lang")

	defaults, custom, resolvedLang := s.StopWordManager.ListLang(collection, lang)

	entries := make([]StopWordEntry, 0, len(defaults)+len(custom))
	for _, w := range custom {
		entries = append(entries, StopWordEntry{Word: w, IsDefault: false})
	}
	for _, w := range defaults {
		entries = append(entries, StopWordEntry{Word: w, IsDefault: true})
	}

	resp := StopWordListResponse{
		Collection: collection,
		Lang:       resolvedLang,
		Entries:    entries,
		Total:      len(entries),
		Defaults:   len(defaults),
		Custom:     len(custom),
	}
	ok(w, resp)
}

func (s *Server) handleStopWordsAdd(w http.ResponseWriter, r *http.Request) {
	var req StopWordRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		bad(w, err)
		return
	}
	if req.Collection == "" {
		bad(w, fmt.Errorf("missing required field: collection"))
		return
	}
	if len(req.Words) == 0 {
		bad(w, fmt.Errorf("words list cannot be empty"))
		return
	}

	if err := s.StopWordManager.Add(req.Collection, req.Words); err != nil {
		bad(w, err)
		return
	}
	ok(w, map[string]interface{}{
		"status": "ok",
		"added":  len(req.Words),
	})
}

func (s *Server) handleStopWordsDelete(w http.ResponseWriter, r *http.Request) {
	var req StopWordRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		bad(w, err)
		return
	}
	if req.Collection == "" {
		bad(w, fmt.Errorf("missing required field: collection"))
		return
	}
	if len(req.Words) == 0 {
		bad(w, fmt.Errorf("words list cannot be empty"))
		return
	}

	var errs []string
	deleted := 0
	for _, w := range req.Words {
		if err := s.StopWordManager.Delete(req.Collection, w); err != nil {
			errs = append(errs, err.Error())
		} else {
			deleted++
		}
	}

	resp := map[string]interface{}{
		"status":  "ok",
		"deleted": deleted,
	}
	if len(errs) > 0 {
		resp["errors"] = errs
	}
	ok(w, resp)
}
