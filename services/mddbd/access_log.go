package main

import (
	"log/slog"
	"net/http"
	"time"
)

// Access logging (GO-028).
//
// The server had no request log at all: an operator could see that a handler
// failed, never that a request arrived. It is off by default because it is one
// line per request and the volume is the operator's call, not ours —
// MDDB_ACCESS_LOG=true turns it on.

// accessLogRecorder captures what the handler decided, so the log line can
// report it after the fact. A handler that never calls WriteHeader still
// returns 200, which is why status starts there.
type accessLogRecorder struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (r *accessLogRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func (r *accessLogRecorder) Write(b []byte) (int, error) {
	n, err := r.ResponseWriter.Write(b)
	r.bytes += n
	return n, err
}

// Unwrap lets http.ResponseController reach the underlying writer, so
// streaming handlers (SSE, MCP transports) keep their flush and hijack.
func (r *accessLogRecorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }

// Flush forwards http.Flusher for handlers that assert the interface directly
// instead of going through http.ResponseController (GO-040).
func (r *accessLogRecorder) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// accessLogLevel keeps the log readable at a glance: server errors are errors,
// client errors are warnings, everything else is routine.
func accessLogLevel(status int) slog.Level {
	switch {
	case status >= 500:
		return slog.LevelError
	case status >= 400:
		return slog.LevelWarn
	default:
		return slog.LevelInfo
	}
}

// withAccessLog logs one line per request. The query string is deliberately
// omitted: search endpoints carry user content there, and an access log is not
// the place for it.
func withAccessLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &accessLogRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		slog.Log(r.Context(), accessLogLevel(rec.status), "http request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", rec.status,
			"bytes", rec.bytes,
			"elapsed", time.Since(start),
			"remote", r.RemoteAddr,
		)
	})
}
