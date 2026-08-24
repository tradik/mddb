package main

import (
	"fmt"
	"mddb/internal/storage"
	"mddb/internal/temporal"
	"net/http"
	"time"

	json "mddb/internal/jsonx"
)

// TemporalQueryRequest is the HTTP request body for querying document events.
type TemporalQueryRequest struct {
	Collection string `json:"collection"`
	Key        string `json:"key"`
	Lang       string `json:"lang"`
	EventType  string `json:"eventType,omitempty"` // "create", "update", "access", or "" for all
	From       int64  `json:"from,omitempty"`      // unix timestamp; 0 = 30 days ago
	To         int64  `json:"to,omitempty"`        // unix timestamp; 0 = now
	Limit      int    `json:"limit,omitempty"`
}

// TemporalQueryResponse is the HTTP response for querying document events.
type TemporalQueryResponse struct {
	Collection string                   `json:"collection"`
	DocID      string                   `json:"docId"`
	Events     []temporal.TemporalEvent `json:"events"`
	Total      int                      `json:"total"`
}

// TemporalHotRequest is the HTTP request body for the hot-docs leaderboard.
type TemporalHotRequest struct {
	Collection string `json:"collection"`
	TopN       int    `json:"topN,omitempty"` // default 10
	Since      int64  `json:"since,omitempty"`
}

// TemporalHotResponse is the HTTP response for the hot-docs leaderboard.
type TemporalHotResponse struct {
	Collection string            `json:"collection"`
	Entries    []HotEntryWithDoc `json:"entries"`
}

// HotEntryWithDoc embeds the full document alongside access stats.
type HotEntryWithDoc struct {
	Document     *storage.Doc `json:"document,omitempty"`
	DocID        string       `json:"docId"`
	AccessCount  uint64       `json:"accessCount"`
	LastAccessAt int64        `json:"lastAccessAt"`
}

// TemporalHistogramRequest is the HTTP request body for activity histograms.
type TemporalHistogramRequest struct {
	Collection string `json:"collection"`
	EventType  string `json:"eventType,omitempty"` // default "access"
	Interval   string `json:"interval,omitempty"`  // "day" (default), "week", "month"
	From       int64  `json:"from,omitempty"`
	To         int64  `json:"to,omitempty"`
}

// TemporalHistogramResponse is the HTTP response for activity histograms.
type TemporalHistogramResponse struct {
	Collection string                             `json:"collection"`
	EventType  string                             `json:"eventType"`
	Interval   string                             `json:"interval"`
	Buckets    []temporal.TemporalHistogramBucket `json:"buckets"`
}

// handleTemporalQuery returns event history for a specific document.
func (s *Server) handleTemporalQuery(w http.ResponseWriter, r *http.Request) {
	var req TemporalQueryRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		bad(w, err)
		return
	}
	if req.Collection == "" || req.Key == "" || req.Lang == "" {
		bad(w, fmt.Errorf("missing required fields: collection, key, lang"))
		return
	}

	if s.AuthManager != nil {
		if err := s.AuthManager.CheckPermission(r.Context(), req.Collection, PermRead); err != nil {
			http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
			return
		}
	}

	if s.TemporalManager == nil {
		bad(w, fmt.Errorf("temporal tracking not available"))
		return
	}

	now := time.Now().Unix()
	if req.To == 0 {
		req.To = now
	}
	if req.From == 0 {
		req.From = now - 30*24*3600 // default: last 30 days
	}

	docID := genID(req.Collection, req.Key, req.Lang)
	events, err := s.TemporalManager.QueryRange(req.Collection, docID, req.From, req.To, req.EventType, req.Limit)
	if err != nil {
		bad(w, err)
		return
	}

	ok(w, TemporalQueryResponse{
		Collection: req.Collection,
		DocID:      docID,
		Events:     events,
		Total:      len(events),
	})
}

// handleTemporalHot returns the top-N most accessed documents in a collection.
func (s *Server) handleTemporalHot(w http.ResponseWriter, r *http.Request) {
	var req TemporalHotRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		bad(w, err)
		return
	}
	if req.Collection == "" {
		bad(w, fmt.Errorf("missing required field: collection"))
		return
	}

	if s.AuthManager != nil {
		if err := s.AuthManager.CheckPermission(r.Context(), req.Collection, PermRead); err != nil {
			http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
			return
		}
	}

	if s.TemporalManager == nil {
		bad(w, fmt.Errorf("temporal tracking not available"))
		return
	}

	entries, err := s.TemporalManager.GetHotDocs(req.Collection, req.TopN, req.Since)
	if err != nil {
		bad(w, err)
		return
	}

	result := make([]HotEntryWithDoc, 0, len(entries))
	for _, e := range entries {
		entry := HotEntryWithDoc{
			DocID:        e.DocID,
			AccessCount:  e.AccessCount,
			LastAccessAt: e.LastAccessAt,
		}
		doc := s.loadDocByID(e.Collection, e.DocID, true)
		if doc != nil {
			entry.Document = doc
		}
		result = append(result, entry)
	}

	ok(w, TemporalHotResponse{
		Collection: req.Collection,
		Entries:    result,
	})
}

// handleTemporalHistogram returns an event-frequency histogram for a collection.
func (s *Server) handleTemporalHistogram(w http.ResponseWriter, r *http.Request) {
	var req TemporalHistogramRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		bad(w, err)
		return
	}
	if req.Collection == "" {
		bad(w, fmt.Errorf("missing required field: collection"))
		return
	}

	if s.AuthManager != nil {
		if err := s.AuthManager.CheckPermission(r.Context(), req.Collection, PermRead); err != nil {
			http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
			return
		}
	}

	if s.TemporalManager == nil {
		bad(w, fmt.Errorf("temporal tracking not available"))
		return
	}

	if req.EventType == "" {
		req.EventType = "access"
	}
	if req.Interval == "" {
		req.Interval = "day"
	}
	now := time.Now().Unix()
	if req.To == 0 {
		req.To = now
	}
	if req.From == 0 {
		req.From = now - 30*24*3600
	}

	buckets, err := s.TemporalManager.ComputeHistogram(req.Collection, req.EventType, req.Interval, req.From, req.To)
	if err != nil {
		bad(w, err)
		return
	}

	ok(w, TemporalHistogramResponse{
		Collection: req.Collection,
		EventType:  req.EventType,
		Interval:   req.Interval,
		Buckets:    buckets,
	})
}
