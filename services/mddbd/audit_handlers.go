package main

import (
	"errors"
	"fmt"
	"mddb/internal/audit"
	"net/http"
	"strconv"
	"strings"
	"time"

	json "mddb/internal/jsonx"
)

// proxy (currently any — hardening tightens this later).
func ClientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if comma := strings.Index(xff, ","); comma > 0 {
			return strings.TrimSpace(xff[:comma])
		}
		return strings.TrimSpace(xff)
	}
	host, _, err := splitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func splitHostPort(addr string) (string, string, error) {
	if addr == "" {
		return "", "", errors.New("empty addr")
	}
	i := strings.LastIndex(addr, ":")
	if i < 0 {
		return addr, "", nil
	}
	return addr[:i], addr[i+1:], nil
}

// --- HTTP ---

func (s *Server) handleAudit(w http.ResponseWriter, r *http.Request) {
	if s.AuditManager == nil || !s.AuditManager.Enabled() {
		http.Error(w, `{"error":"audit disabled"}`, http.StatusNotFound)
		return
	}
	if !requireAdmin(w, r, s) {
		return
	}
	q := r.URL.Query()
	f := audit.QueryFilter{
		Actor:  q.Get("actor"),
		Action: q.Get("action"),
		Result: q.Get("result"),
	}
	if v := q.Get("fromNanos"); v != "" {
		f.FromNanos, _ = strconv.ParseInt(v, 10, 64)
	}
	if v := q.Get("toNanos"); v != "" {
		f.ToNanos, _ = strconv.ParseInt(v, 10, 64)
	}
	if v := q.Get("from"); v != "" && f.FromNanos == 0 {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			f.FromNanos = t.UnixNano()
		}
	}
	if v := q.Get("to"); v != "" && f.ToNanos == 0 {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			f.ToNanos = t.UnixNano()
		}
	}
	if v := q.Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			f.Limit = n
		}
	}
	events, err := s.AuditManager.Query(f)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"events":  events,
		"count":   len(events),
		"dropped": s.AuditManager.Dropped(),
	})
}

// handleAuditExporters serves GET /v1/audit/exporters — per-sink
// counters so an operator can confirm SIEM / syslog delivery is
// healthy. Admin-only because the response leaks the sink targets.
func (s *Server) handleAuditExporters(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	if s.AuditManager == nil || !s.AuditManager.Enabled() {
		http.Error(w, `{"error":"audit disabled"}`, http.StatusNotFound)
		return
	}
	if !requireAdmin(w, r, s) {
		return
	}
	exs := s.AuditManager.Exporters()
	stats := make([]audit.ExporterStats, 0, len(exs))
	for _, e := range exs {
		stats = append(stats, e.Stats())
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"exporters": stats,
		"count":     len(stats),
	})
}

// requireAdmin writes a 401/403 response and returns false if the
// request does not carry admin claims.
func requireAdmin(w http.ResponseWriter, r *http.Request, s *Server) bool {
	if s.AuthManager == nil || !s.AuthManager.enabled {
		return true
	}
	claims, ok := r.Context().Value(authContextKey).(*JWTClaims)
	if !ok || claims == nil {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return false
	}
	if !claims.Admin {
		http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
		return false
	}
	return true
}
