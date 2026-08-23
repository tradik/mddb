package main

import (
	"errors"
	"mddb/internal/audit"
	"net/http"
	"strings"

	json "mddb/internal/jsonx"
)

// handleEncryptionStatus serves GET /v1/encryption/status — a
// read-only summary of the encryptor configuration plus per-collection
// counters (how many docs were sealed with the primary key vs. a
// previous one). Admin-only because the response leaks key IDs that
// hint at rotation history.
func (s *Server) handleEncryptionStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	if !requireAdmin(w, r, s) {
		return
	}
	if s.RotationManager == nil {
		ok(w, &RotationStatus{Enabled: false})
		return
	}
	st, err := s.RotationManager.Status()
	if err != nil {
		bad(w, err)
		return
	}
	ok(w, st)
}

// rotateRequest is the body for POST /v1/encryption/rotate.
type rotateRequest struct {
	// Collection scopes the job to a single collection. Empty means
	// "all collections that hold encrypted documents."
	Collection string `json:"collection,omitempty"`
}

// handleEncryptionRotate starts a re-encryption job. Returns the job
// ID immediately; clients poll GET /v1/encryption/jobs/{id} for
// progress. Admin-only and requires a write-capable mode — rotation
// rewrites every encrypted document under the new primary key.
func (s *Server) handleEncryptionRotate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	if s.Mode == ModeRead {
		http.Error(w, `{"error":"read-only mode"}`, http.StatusForbidden)
		return
	}
	if !requireAdmin(w, r, s) {
		return
	}
	if s.RotationManager == nil {
		bad(w, errors.New("encryption not configured"))
		return
	}
	var req rotateRequest
	if r.ContentLength > 0 {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			bad(w, err)
			return
		}
	}
	job, err := s.RotationManager.Start(r.Context(), req.Collection)
	if err != nil {
		bad(w, err)
		return
	}
	if s.AuditManager != nil {
		s.AuditManager.Record(audit.AuditEvent{
			Action:     "encryption.rotation_requested",
			Resource:   "encryption",
			Collection: req.Collection,
			Result:     "ok",
			Detail:     job.ID,
			IP:         ClientIP(r),
		})
	}
	ok(w, job)
}

// handleEncryptionJob serves GET /v1/encryption/jobs/{id} — single
// job status — or GET /v1/encryption/jobs for the full list.
func (s *Server) handleEncryptionJob(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	if !requireAdmin(w, r, s) {
		return
	}
	if s.RotationManager == nil {
		http.Error(w, `{"error":"encryption not configured"}`, http.StatusNotFound)
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/v1/encryption/jobs/")
	id = strings.TrimSuffix(id, "/")
	if id == "" || id == "/v1/encryption/jobs" {
		ok(w, map[string]interface{}{"jobs": s.RotationManager.List()})
		return
	}
	job := s.RotationManager.Get(id)
	if job == nil {
		http.Error(w, `{"error":"job not found"}`, http.StatusNotFound)
		return
	}
	ok(w, job)
}
