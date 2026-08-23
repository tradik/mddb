package main

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	proto "mddb/proto"

	json "mddb/internal/jsonx"
)

// BulkIngestSubmitRequest is the HTTP body for POST /v1/bulk-ingest-job.
type BulkIngestSubmitRequest struct {
	Collection  string             `json:"collection"`
	Documents   []AddBatchDocument `json:"documents"`
	CallbackURL string             `json:"callbackUrl,omitempty"`
}

// handleBulkIngestSubmit accepts a long-running bulk ingest job and returns
// immediately with a job identifier. Clients should poll /v1/bulk-ingest-job/{id}
// for progress or supply a callbackUrl to receive a webhook on completion.
func (s *Server) handleBulkIngestSubmit(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.BulkIngest == nil {
		bad(w, errors.New("bulk ingest not initialized"))
		return
	}

	var req BulkIngestSubmitRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		bad(w, err)
		return
	}
	if req.Collection == "" {
		bad(w, errors.New("missing collection"))
		return
	}
	if len(req.Documents) == 0 {
		bad(w, errors.New("no documents provided"))
		return
	}

	docs := make([]*proto.BatchDocument, len(req.Documents))
	for i, d := range req.Documents {
		docs[i] = &proto.BatchDocument{
			Key:          d.Key,
			Lang:         d.Lang,
			Meta:         toProtoMeta(d.Meta),
			ContentMd:    d.ContentMD,
			SaveRevision: d.SaveRevision,
		}
	}

	job, err := s.BulkIngest.Submit(req.Collection, docs, req.CallbackURL)
	if err != nil {
		bad(w, err)
		return
	}
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(job)
}

// handleBulkIngestStatus returns the latest status record for a job. The path
// must be /v1/bulk-ingest-job/{id}.
func (s *Server) handleBulkIngestStatus(w http.ResponseWriter, r *http.Request) {
	if s.BulkIngest == nil {
		bad(w, errors.New("bulk ingest not initialized"))
		return
	}
	jobID := strings.TrimPrefix(r.URL.Path, "/v1/bulk-ingest-job/")
	jobID = strings.TrimSuffix(jobID, "/")
	if jobID == "" {
		bad(w, errors.New("missing job id in path"))
		return
	}

	switch r.Method {
	case http.MethodGet:
		job, err := s.BulkIngest.Get(jobID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		ok(w, job)
	case http.MethodDelete:
		if err := s.BulkIngest.Cancel(jobID); err != nil {
			bad(w, err)
			return
		}
		ok(w, map[string]string{"status": "cancelled", "id": jobID})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleBulkIngestList returns all jobs, optionally filtered by collection
// via ?collection=X.
func (s *Server) handleBulkIngestList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.BulkIngest == nil {
		bad(w, errors.New("bulk ingest not initialized"))
		return
	}
	collection := r.URL.Query().Get("collection")
	jobs, err := s.BulkIngest.List(collection)
	if err != nil {
		bad(w, err)
		return
	}
	ok(w, map[string]interface{}{
		"jobs":  jobs,
		"total": len(jobs),
	})
}

// helper for tests and docs — format a completed job as a short status line.
func (j *BulkIngestJob) String() string {
	return fmt.Sprintf("bulk[%s] %s %d/%d (+%d ~%d ✗%d)",
		j.ID, j.Status, j.Processed, j.Total, j.Added, j.Updated, j.Failed)
}
