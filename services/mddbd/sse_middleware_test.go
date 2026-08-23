package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"mddb/internal/metrics"
)

// GO-040: the metrics middleware's statusRecorder (and the access-log
// recorder) hid http.Flusher, so enabling metrics turned every streaming
// endpoint into `{"error":"streaming not supported"}` 500. The stream must
// keep working through the full wrapper chain.
func TestSSEThroughMetricsAndAccessLogMiddleware(t *testing.T) {
	s := &Server{SSEHub: NewSSEHub(true, 10, 5)}

	handler := http.Handler(http.HandlerFunc(s.handleSSE))
	handler = metrics.NewMetrics(true, nil).Middleware(handler)
	handler = withAccessLog(handler)

	srv := httptest.NewServer(handler)
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/v1/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 — flusher lost behind middleware wrappers", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("Content-Type = %q, want text/event-stream", ct)
	}

	// The connected event must actually arrive — proves Flush reaches the
	// underlying writer instead of dying inside a wrapper.
	buf := make([]byte, 1)
	if _, err := resp.Body.Read(buf); err != nil {
		t.Fatalf("no bytes arrived on the stream: %v", err)
	}
	cancel()
}

// httpFlusher must unwrap chains where an inner wrapper exposes only
// Unwrap(), the same way http.ResponseController resolves them.
func TestHTTPFlusherWalksUnwrapChain(t *testing.T) {
	rec := httptest.NewRecorder() // implements http.Flusher
	wrapped := &accessLogRecorder{ResponseWriter: unwrapOnly{&accessLogRecorder{ResponseWriter: rec}}}
	if _, ok := httpFlusher(wrapped); !ok {
		t.Fatal("flusher not found through Unwrap chain")
	}
	if _, ok := httpFlusher(plainWriter{httptest.NewRecorder()}); ok {
		t.Fatal("flusher reported for a writer that cannot flush")
	}
}

// unwrapOnly hides Flush and exposes only Unwrap, like a third-party wrapper.
type unwrapOnly struct{ inner http.ResponseWriter }

func (u unwrapOnly) Header() http.Header         { return u.inner.Header() }
func (u unwrapOnly) Write(b []byte) (int, error) { return u.inner.Write(b) }
func (u unwrapOnly) WriteHeader(code int)        { u.inner.WriteHeader(code) }
func (u unwrapOnly) Unwrap() http.ResponseWriter { return u.inner }

// plainWriter neither flushes nor unwraps.
type plainWriter struct{ inner http.ResponseWriter }

func (p plainWriter) Header() http.Header         { return p.inner.Header() }
func (p plainWriter) Write(b []byte) (int, error) { return p.inner.Write(b) }
func (p plainWriter) WriteHeader(code int)        { p.inner.WriteHeader(code) }
