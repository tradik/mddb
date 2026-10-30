package main

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	json "mddb/internal/jsonx"
)

// HTTP endpoints of corpus packs (AGT-005). Building, installing and removing
// a pack are database-wide operations: they need the admin permission, as
// backup and restore do. An install here takes the pack as an upload, never
// as a server path. A client that cannot upload — an MCP agent — names an
// https URL instead, which the server fetches through its SSRF guard
// (corpus_fetch.go, #378). The handlers, the MCP tools and the gRPC methods
// call the same functions: corpusPacks, installCorpus, removeCorpus and
// prepareCorpus.

// corpusUploadField is the multipart field holding the pack.
const corpusUploadField = "pack"

// requireAdmin answers 403 and reports false when the caller is not a
// global admin.
func (s *Server) requireAdmin(w http.ResponseWriter, r *http.Request) bool {
	if s.AuthManager == nil {
		return true
	}
	if err := s.AuthManager.CheckPermission(r.Context(), "*", PermAdmin); err != nil {
		http.Error(w, `{"error":"admin access required"}`, http.StatusForbidden)
		return false
	}
	return true
}

// handleCorpusList is GET /v1/corpus: the installed packs.
func (s *Server) handleCorpusList(w http.ResponseWriter, r *http.Request) {
	if s.AuthManager != nil {
		if err := s.AuthManager.CheckPermission(r.Context(), "*", PermRead); err != nil {
			http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
			return
		}
	}
	packs := s.corpusPacks(r.Context())
	ok(w, map[string]any{"packs": packs, "total": len(packs)})
}

// handleCorpusBuild is POST /v1/corpus/build: the pack of a collection,
// streamed as application/zstd.
func (s *Server) handleCorpusBuild(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	if !s.requireAdmin(w, r) {
		return
	}
	liftWriteDeadline(w, r) // a large collection outlasts the write timeout
	var req corpusBuildRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		bad(w, err)
		return
	}
	b, err := s.prepareCorpus(req)
	if err != nil {
		corpusError(w, err)
		return
	}
	defer b.close()
	name := b.manifest.Name
	if b.manifest.Version != "" {
		name += "-" + b.manifest.Version
	}
	w.Header().Set("Content-Type", "application/zstd")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", name+".mddbpack"))
	w.Header().Set("X-Mddb-Pack-Docs", fmt.Sprint(b.manifest.DocCount))
	w.WriteHeader(http.StatusOK)
	if err := b.writeTo(w); err != nil {
		// The status is out; the client finds the pack truncated, and an
		// install refuses it by its checksum.
		slog.Error("streaming a corpus pack failed", "pack", b.manifest.Name, "err", err)
	}
}

// handleCorpusInstall is POST /v1/corpus/install: a multipart upload with
// the pack in the field "pack".
func (s *Server) handleCorpusInstall(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	if !s.requireAdmin(w, r) {
		return
	}
	// Uploading and checking a large pack outlasts both timeouts.
	liftWriteDeadline(w, r)
	_ = http.NewResponseController(w).SetReadDeadline(time.Time{})
	part, err := corpusUpload(r)
	if err != nil {
		bad(w, err)
		return
	}
	res, err := s.installCorpus(part)
	if err != nil {
		corpusError(w, err)
		return
	}
	ok(w, res)
}

// corpusUpload is the multipart part holding the pack, read as it arrives.
func corpusUpload(r *http.Request) (io.Reader, error) {
	mr, err := r.MultipartReader()
	if err != nil {
		return nil, fmt.Errorf("upload the pack as multipart/form-data in the field %q: %w", corpusUploadField, err)
	}
	for {
		part, err := mr.NextPart()
		if err != nil {
			return nil, fmt.Errorf("no %q field in the upload: %w", corpusUploadField, err)
		}
		if part.FormName() == corpusUploadField {
			return part, nil
		}
	}
}

// handleCorpusRemove is POST /v1/corpus/remove {"name": "..."}.
func (s *Server) handleCorpusRemove(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	if !s.requireAdmin(w, r) {
		return
	}
	var req struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		bad(w, err)
		return
	}
	n, err := s.removeCorpus(req.Name)
	if err != nil {
		corpusError(w, err)
		return
	}
	ok(w, map[string]any{"status": "removed", "name": req.Name, "deleted": n})
}

// corpusError maps a corpus failure to its status: 400 for a request or pack
// refused as it is, 404 for nothing to act on, 409 for a name already taken,
// 500 for the server's own failure.
func corpusError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errCorpusConflict):
		w.WriteHeader(http.StatusConflict)
	case errors.Is(err, errCorpusInvalid), errors.Is(err, errNotFound):
		bad(w, err)
		return
	default:
		w.WriteHeader(http.StatusInternalServerError)
	}
	_, _ = fmt.Fprintf(w, `{"error":%q}`, err.Error()) // #nosec G705 -- response write to http.ResponseWriter
}
