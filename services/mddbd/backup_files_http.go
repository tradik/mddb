package main

import (
	"log/slog"
	"mime"
	"net/http"
	"strings"

	"mddb/internal/audit"
)

// GET /v1/backups and GET /v1/backups/{name}/download (FE-024): the backup
// files in MDDB_BACKUP_DIR and one of them as a download, for the panel's
// Backup & Restore page. Both are administration like GET /v1/backup and
// POST /v1/restore — admin on "*", checked by the access gate and again
// here — and neither writes, so read-only mode leaves them open.

// Paging of the backup list.
const (
	backupListDefaultLimit = 100
	backupListMaxLimit     = 1000
)

// backupListNote says what the list leaves out.
const backupListNote = "Backups in folders inside the backup directory, and backups a scheduled rule stores in object storage, are not listed."

// backupsPrefix is the subtree of the per-backup operations.
const backupsPrefix = "/v1/backups/"

// handleBackups serves GET /v1/backups?limit=&offset=.
func (s *Server) handleBackups(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSONError(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		return
	}
	if !s.requireAdmin(w, r) {
		return
	}
	limit := min(parseIntParam(r, "limit", backupListDefaultLimit), backupListMaxLimit)
	offset := max(queryInt(r, "offset"), 0)
	rows, total, err := listBackupFiles(s.Path, offset, limit)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, map[string]any{"error": "cannot read the backup directory"})
		return
	}
	ok(w, map[string]any{"backups": rows, "total": total, "limit": limit, "offset": offset, "note": backupListNote})
}

// handleBackupItem serves GET (and HEAD) /v1/backups/{name}/download.
func (s *Server) handleBackupItem(w http.ResponseWriter, r *http.Request) {
	name, isDownload := strings.CutSuffix(strings.TrimPrefix(r.URL.Path, backupsPrefix), "/download")
	if !isDownload {
		writeJSONError(w, http.StatusNotFound, map[string]any{"error": errBackupNotFound.Error()})
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		writeJSONError(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		return
	}
	if !s.requireAdmin(w, r) {
		return
	}
	s.serveBackupDownload(w, r, name)
}

// serveBackupDownload streams one backup file, with Content-Length and
// ranges from http.ServeContent. Every attempt is audit-logged with the user.
func (s *Server) serveBackupDownload(w http.ResponseWriter, r *http.Request, name string) {
	f, info, err := openBackupFile(name, s.Path)
	s.recordBackupDownload(r, name, err == nil)
	if err != nil {
		writeJSONError(w, http.StatusNotFound, map[string]any{"error": errBackupNotFound.Error()})
		return
	}
	defer func() { _ = f.Close() }()
	// A large backup takes longer to send than the write timeout allows.
	liftWriteDeadline(w, r)
	h := w.Header()
	h.Set(headerContentType, "application/octet-stream")
	h.Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": name}))
	h.Set("X-Content-Type-Options", "nosniff")
	http.ServeContent(w, r, name, info.ModTime(), f)
}

// recordBackupDownload writes a download attempt to the audit log and the
// server log: a backup is the whole database.
func (s *Server) recordBackupDownload(r *http.Request, name string, served bool) {
	actor := ""
	if claims, found := GetClaimsFromContext(r.Context()); found && claims != nil {
		actor = claims.Username
	}
	result := "ok"
	if !served {
		result = "fail"
	}
	s.AuditManager.Record(audit.AuditEvent{
		Actor:     actor,
		Action:    "backup.download",
		Resource:  name,
		Result:    result,
		IP:        ClientIP(r),
		UserAgent: r.UserAgent(),
	})
	slog.Info("backup download", "backup", name, "user", actor, "result", result, "ip", ClientIP(r))
}
