package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"mddb/internal/tracing"
)

// Benchmarks of the hybrid search path with tracing off and on (OPS-024).
// Off must cost what the path cost before tracing existed: run
// BenchmarkHybridSearchTracingOff here and on the previous release and
// compare ns/op and allocs/op (docs/TELEMETRY.md has the numbers).

// benchHybridServer is an embedded collection of 50 documents.
func benchHybridServer(b *testing.B) *Server {
	b.Helper()
	s := covGVectorServer(b)
	for i := range 50 {
		covGEmbedded(b, s, "kb", "doc"+string(rune('a'+i%26))+string(rune('a'+i/26)),
			"snapshot backup of the database file number "+string(rune('a'+i%26)), nil)
	}
	return s
}

// benchHybrid runs one hybrid search per iteration through h.
func benchHybrid(b *testing.B, h http.Handler) {
	b.Helper()
	const body = `{"collection":"kb","query":"snapshot backup","topK":5}`
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		req := httptest.NewRequest(http.MethodPost, "/v1/hybrid-search", strings.NewReader(body))
		req.Header.Set("traceparent", "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			b.Fatalf("search: %d", rec.Code)
		}
	}
}

func BenchmarkHybridSearchTracingOff(b *testing.B) {
	s := benchHybridServer(b)
	benchHybrid(b, http.HandlerFunc(s.handleHybridSearch))
}

func BenchmarkHybridSearchTracingOn(b *testing.B) {
	collector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	defer collector.Close()
	s := benchHybridServer(b)
	s.Tracer = tracing.New(tracing.Config{Endpoint: collector.URL + "/v1/traces", SampleRatio: 1, FlushInterval: time.Second})
	defer func() { _ = s.Tracer.Shutdown(context.Background()) }()
	benchHybrid(b, s.withTracing(http.HandlerFunc(s.handleHybridSearch)))
}
