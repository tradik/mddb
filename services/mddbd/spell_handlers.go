package main

import (
	"fmt"
	"mddb/internal/spell"
	"net/http"

	json "mddb/internal/jsonx"
)

// SpellSuggestRequest is the HTTP request body for spell suggestions.
type SpellSuggestRequest struct {
	Collection     string `json:"collection"`
	Text           string `json:"text"`
	Lang           string `json:"lang"`
	MaxSuggestions int    `json:"maxSuggestions,omitempty"`
}

// SpellSuggestResponse is the HTTP response for spell suggestions.
type SpellSuggestResponse struct {
	OriginalText     string                  `json:"originalText"`
	SuggestedText    string                  `json:"suggestedText"`
	TokenSuggestions []spell.SpellSuggestion `json:"tokenSuggestions"`
}

// SpellCleanupRequest is the HTTP request body for document content cleanup.
type SpellCleanupRequest struct {
	Collection string `json:"collection"`
	Text       string `json:"text"`
	Lang       string `json:"lang"`
}

// SpellCleanupResponse is the HTTP response for content cleanup.
type SpellCleanupResponse struct {
	Original           string `json:"original"`
	Cleaned            string `json:"cleaned"`
	CorrectionsApplied int    `json:"correctionsApplied"`
}

// SpellDictionaryRequest is the HTTP request body for managing custom dictionaries.
type SpellDictionaryRequest struct {
	Collection string   `json:"collection"`
	Lang       string   `json:"lang"`
	Words      []string `json:"words"`
	Frequency  uint32   `json:"frequency,omitempty"`
}

// SpellDictionaryResponse is the HTTP response for dictionary operations.
type SpellDictionaryResponse struct {
	Collection string   `json:"collection"`
	Lang       string   `json:"lang"`
	Words      []string `json:"words"`
}

// handleSpellSuggest returns spell correction suggestions for a text string.
func (s *Server) handleSpellSuggest(w http.ResponseWriter, r *http.Request) {
	var req SpellSuggestRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		bad(w, err)
		return
	}
	if req.Text == "" || req.Lang == "" {
		bad(w, fmt.Errorf("missing required fields: text, lang"))
		return
	}

	if s.SpellManager == nil || !s.SpellManager.Ready() {
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "spell index loading, please retry"})
		return
	}

	suggestedText, suggestions := s.SpellManager.Suggest(req.Collection, req.Lang, req.Text, req.MaxSuggestions)

	ok(w, SpellSuggestResponse{
		OriginalText:     req.Text,
		SuggestedText:    suggestedText,
		TokenSuggestions: suggestions,
	})
}

// handleSpellCleanup applies the best corrections to a text string and returns it.
func (s *Server) handleSpellCleanup(w http.ResponseWriter, r *http.Request) {
	var req SpellCleanupRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		bad(w, err)
		return
	}
	if req.Text == "" || req.Lang == "" {
		bad(w, fmt.Errorf("missing required fields: text, lang"))
		return
	}

	if s.SpellManager == nil || !s.SpellManager.Ready() {
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "spell index loading, please retry"})
		return
	}

	_, suggestions := s.SpellManager.Suggest(req.Collection, req.Lang, req.Text, 100)
	cleaned := s.SpellManager.Cleanup(req.Collection, req.Lang, req.Text)

	ok(w, SpellCleanupResponse{
		Original:           req.Text,
		Cleaned:            cleaned,
		CorrectionsApplied: len(suggestions),
	})
}

// handleSpellDictionary handles GET/PUT/DELETE for per-collection custom dictionaries.
func (s *Server) handleSpellDictionary(w http.ResponseWriter, r *http.Request) {
	if s.SpellManager == nil {
		bad(w, fmt.Errorf("spell manager not available"))
		return
	}

	switch r.Method {
	case http.MethodGet:
		collection := r.URL.Query().Get("collection")
		lang := r.URL.Query().Get("lang")
		if lang == "" {
			bad(w, fmt.Errorf("missing required query param: lang"))
			return
		}
		if s.AuthManager != nil && collection != "" {
			if err := s.AuthManager.CheckPermission(r.Context(), collection, PermRead); err != nil {
				http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
				return
			}
		}
		words, err := s.SpellManager.ListWords(collection, lang)
		if err != nil {
			bad(w, err)
			return
		}
		if words == nil {
			words = []string{}
		}
		ok(w, SpellDictionaryResponse{Collection: collection, Lang: lang, Words: words})

	case http.MethodPut:
		var req SpellDictionaryRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			bad(w, err)
			return
		}
		if req.Lang == "" || len(req.Words) == 0 {
			bad(w, fmt.Errorf("missing required fields: lang, words"))
			return
		}
		if s.AuthManager != nil && req.Collection != "" {
			if err := s.AuthManager.CheckPermission(r.Context(), req.Collection, PermWrite); err != nil {
				http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
				return
			}
		}
		freq := req.Frequency
		if freq == 0 {
			freq = 100 // default frequency for custom words
		}
		for _, word := range req.Words {
			if err := s.SpellManager.AddWord(req.Collection, req.Lang, word, freq); err != nil {
				bad(w, err)
				return
			}
		}
		ok(w, map[string]interface{}{"added": len(req.Words)})

	case http.MethodDelete:
		var req SpellDictionaryRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			bad(w, err)
			return
		}
		if req.Lang == "" || len(req.Words) == 0 {
			bad(w, fmt.Errorf("missing required fields: lang, words"))
			return
		}
		if s.AuthManager != nil && req.Collection != "" {
			if err := s.AuthManager.CheckPermission(r.Context(), req.Collection, PermWrite); err != nil {
				http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
				return
			}
		}
		for _, word := range req.Words {
			if err := s.SpellManager.RemoveWord(req.Collection, req.Lang, word); err != nil {
				bad(w, err)
				return
			}
		}
		ok(w, map[string]interface{}{"removed": len(req.Words)})

	default:
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
	}
}
