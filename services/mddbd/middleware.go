package main

import (
	"fmt"
	"mddb/internal/audit"
	"net/http"
)

// --- middleware

func withCORS(h http.Handler) http.Handler {
	// SEC-008: resolve the origin policy once. Prefer the MDDB_CORS_ORIGINS
	// allowlist over a wildcard so a hostile site can't read responses from a
	// user's local/intranet MDDB instance through their browser.
	cfg := envCORSConfig()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cfg.applyOrigin(w, r.Header.Get("Origin"))
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-API-Key")
		w.Header().Set("Access-Control-Expose-Headers", "X-Total-Count")
		w.Header().Set("Access-Control-Max-Age", "86400")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		h.ServeHTTP(w, r)
	})
}

func withJSON(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		h.ServeHTTP(w, r)
	})
}

// withMaxBody caps the request body size (SEC-005). A declared Content-Length
// over the limit is rejected immediately with 413; otherwise the body is
// wrapped in http.MaxBytesReader so reads can never allocate more than `limit`
// even when Content-Length is absent or lies. Paths in `exempt` (large
// file uploads / wiki imports that stream from disk and enforce their own
// caps) are left untouched. Configurable via MDDB_MAX_BODY_BYTES.
func withMaxBody(limit int64, exempt map[string]bool, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if limit > 0 && !exempt[r.URL.Path] {
			if r.ContentLength > limit {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusRequestEntityTooLarge)
				_, _ = w.Write([]byte(`{"error":"request body too large"}`))
				return
			}
			if r.Body != nil {
				r.Body = http.MaxBytesReader(w, r.Body, limit)
			}
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) guardWrite(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		mode := effectiveMode(s.Mode, s.Config.HTTP.Mode)
		if mode == ModeRead {
			http.Error(w, `{"error":"read-only mode"}`, http.StatusForbidden)
			return
		}
		if err := s.writeBlocked(); err != nil { // #333
			s.writeUnavailable(w, err)
			return
		}
		if s.AuditManager == nil || !s.AuditManager.Enabled() {
			next(w, r)
			return
		}
		aw := &auditResponseWriter{ResponseWriter: w, status: 200}
		next(aw, r)
		result := "ok"
		if aw.status >= 400 {
			result = "fail"
		}
		actor := ""
		if claims, ok := r.Context().Value(authContextKey).(*JWTClaims); ok && claims != nil {
			actor = claims.Username
		}
		s.AuditManager.Record(audit.AuditEvent{
			Actor:     actor,
			Action:    "write." + r.URL.Path,
			Resource:  r.URL.Path,
			Result:    result,
			IP:        ClientIP(r),
			UserAgent: r.UserAgent(),
			Detail:    fmt.Sprintf("status=%d", aw.status),
		})
	}
}

// auditResponseWriter captures the first status code written so the
// guardWrite wrapper can classify the outcome as ok/fail.
type auditResponseWriter struct {
	http.ResponseWriter
	status  int
	written bool
}

func (a *auditResponseWriter) WriteHeader(code int) {
	if !a.written {
		a.status = code
		a.written = true
	}
	a.ResponseWriter.WriteHeader(code)
}

func (a *auditResponseWriter) Write(b []byte) (int, error) {
	if !a.written {
		a.written = true
	}
	return a.ResponseWriter.Write(b)
}

// effectiveMode returns the per-protocol mode if set, otherwise falls back to the global mode.
func effectiveMode(global, perProtocol AccessMode) AccessMode {
	if perProtocol != "" {
		return perProtocol
	}
	return global
}
