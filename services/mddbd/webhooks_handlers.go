package main

import (
	"errors"
	"net/http"

	json "mddb/internal/jsonx"
)

// --- HTTP handlers ---

// RegisterWebhookRequest is the HTTP request body for registering a new webhook.
type RegisterWebhookRequest struct {
	URL        string   `json:"url"`
	Events     []string `json:"events"`
	Collection string   `json:"collection,omitempty"`
}

// DeleteWebhookRequest is the HTTP request body for deleting a webhook by ID.
type DeleteWebhookRequest struct {
	ID string `json:"id"`
}

func (s *Server) handleWebhooks(w http.ResponseWriter, r *http.Request) {
	if s.WebhookManager == nil {
		bad(w, errors.New("webhooks not initialized"))
		return
	}

	switch r.Method {
	case "GET":
		hooks := s.WebhookManager.List()
		ok(w, hooks)

	case "POST":
		if s.Mode == ModeRead {
			http.Error(w, `{"error":"read-only mode"}`, http.StatusForbidden)
			return
		}
		var req RegisterWebhookRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			bad(w, err)
			return
		}
		wh, err := s.WebhookManager.Register(req.URL, req.Events, req.Collection)
		if err != nil {
			bad(w, err)
			return
		}
		ok(w, wh)

	default:
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleWebhookDelete(w http.ResponseWriter, r *http.Request) {
	if s.WebhookManager == nil {
		bad(w, errors.New("webhooks not initialized"))
		return
	}

	var req DeleteWebhookRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		bad(w, err)
		return
	}
	if req.ID == "" {
		bad(w, errors.New("missing id"))
		return
	}

	if err := s.WebhookManager.Delete(req.ID); err != nil {
		bad(w, err)
		return
	}
	ok(w, map[string]string{"status": "deleted", "id": req.ID})
}
