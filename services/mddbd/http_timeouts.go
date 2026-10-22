package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"sync/atomic"
	"time"
)

// HTTP read and write timeouts (#315).
//
// net/http's WriteTimeout does not stop a handler. It closes the connection
// when the response is written after the deadline, so a request that took
// longer still did all its work and the client got EOF instead of the answer,
// with nothing in the log. The timeouts are now configurable, batch writes
// lift the deadline for their own response, and a response that misses it is
// logged.

const defaultHTTPTimeout = 30 * time.Second

// httpTimeouts reads MDDB_HTTP_READ_TIMEOUT and MDDB_HTTP_WRITE_TIMEOUT as Go
// durations ("90s", "5m"). "0" means no limit; an unset or unusable value
// keeps the 30-second default.
func httpTimeouts() (read, write time.Duration) {
	return envTimeout("MDDB_HTTP_READ_TIMEOUT"), envTimeout("MDDB_HTTP_WRITE_TIMEOUT")
}

func envTimeout(name string) time.Duration {
	raw := os.Getenv(name)
	if raw == "" {
		return defaultHTTPTimeout
	}
	if raw == "0" {
		return 0
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d < 0 {
		slog.Warn("ignoring an unusable HTTP timeout", "var", name, "value", raw, "using", defaultHTTPTimeout)
		return defaultHTTPTimeout
	}
	return d
}

type deadlineStateKey struct{}

// deadlineState records that a handler lifted its write deadline, so the
// warning below does not report a response that was in fact delivered.
type deadlineState struct{ lifted atomic.Bool }

// liftWriteDeadline removes the write deadline for this response. For
// handlers whose work legitimately outlasts the server's WriteTimeout — batch
// writes, backups — and whose work is not cancelled by it anyway.
func liftWriteDeadline(w http.ResponseWriter, r *http.Request) {
	_ = http.NewResponseController(w).SetWriteDeadline(time.Time{})
	if st, ok := r.Context().Value(deadlineStateKey{}).(*deadlineState); ok {
		st.lifted.Store(true)
	}
}

// withDeadlineWarning logs every response that finished after the write
// deadline, which net/http drops without a word. writeTimeout 0 disables it.
func withDeadlineWarning(next http.Handler, writeTimeout time.Duration) http.Handler {
	if writeTimeout <= 0 {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		st := &deadlineState{}
		start := time.Now()
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), deadlineStateKey{}, st)))
		if elapsed := time.Since(start); elapsed >= writeTimeout && !st.lifted.Load() {
			slog.Warn("response not delivered: the handler finished after the HTTP write timeout",
				"method", r.Method, "path", r.URL.Path, "elapsed", elapsed, "writeTimeout", writeTimeout,
				"remedy", "raise MDDB_HTTP_WRITE_TIMEOUT, or send smaller requests")
		}
	})
}
