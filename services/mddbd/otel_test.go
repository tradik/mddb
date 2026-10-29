package main

import (
	"bytes"
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	json "mddb/internal/jsonx"
	"mddb/internal/metrics"
	"mddb/internal/tracing"
	proto "mddb/proto"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/test/bufconn"
)

// End-to-end tests of OTLP trace export (OPS-024): a request carrying a
// traceparent reaches a test collector as spans in the caller's trace, over
// REST, gRPC, MCP and ingest.

const (
	otelTraceID  = "4bf92f3577b34da6a3ce929d0e0e4736"
	otelParentID = "00f067aa0ba902b7"
	otelParent   = "00-" + otelTraceID + "-" + otelParentID + "-01"
)

// otelSpan is the part of an OTLP JSON span the tests read.
type otelSpan struct {
	TraceID      string `json:"traceId"`
	SpanID       string `json:"spanId"`
	ParentSpanID string `json:"parentSpanId"`
	Name         string `json:"name"`
	Kind         int    `json:"kind"`
	Status       struct {
		Code int `json:"code"`
	} `json:"status"`
}

// otelCollector is a minimal OTLP/HTTP JSON receiver.
type otelCollector struct {
	srv   *httptest.Server
	mu    sync.Mutex
	spans []otelSpan
}

func newOTelCollector(t *testing.T) *otelCollector {
	t.Helper()
	c := &otelCollector{}
	c.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			ResourceSpans []struct {
				ScopeSpans []struct {
					Spans []otelSpan `json:"spans"`
				} `json:"scopeSpans"`
			} `json:"resourceSpans"`
		}
		if r.URL.Path != "/v1/traces" || json.NewDecoder(r.Body).Decode(&body) != nil {
			http.Error(w, "bad export", http.StatusBadRequest)
			return
		}
		c.mu.Lock()
		for _, rs := range body.ResourceSpans {
			for _, ss := range rs.ScopeSpans {
				c.spans = append(c.spans, ss.Spans...)
			}
		}
		c.mu.Unlock()
	}))
	t.Cleanup(c.srv.Close)
	return c
}

// trace waits until the trace holds every named span, and returns them by
// name. Spans of other traces are ignored.
func (c *otelCollector) trace(t *testing.T, traceID string, names ...string) map[string]otelSpan {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		got := map[string]otelSpan{}
		c.mu.Lock()
		for _, sp := range c.spans {
			if sp.TraceID == traceID {
				got[sp.Name] = sp
			}
		}
		c.mu.Unlock()
		missing := ""
		for _, n := range names {
			if _, ok := got[n]; !ok {
				missing = n
			}
		}
		if missing == "" {
			return got
		}
		if time.Now().After(deadline) {
			t.Fatalf("trace %s: no %q span; have %v", traceID, missing, got)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (c *otelCollector) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.spans)
}

// otelServer is an embedded collection behind a tracer exporting to c.
func otelServer(t *testing.T, c *otelCollector) *Server {
	t.Helper()
	s := covGVectorServer(t)
	s.Tracer = tracing.New(tracing.Config{Endpoint: c.srv.URL + "/v1/traces", SampleRatio: 1, FlushInterval: 10 * time.Millisecond})
	s.EmbeddingWorker.tracer = s.Tracer
	t.Cleanup(func() { _ = s.Tracer.Shutdown(context.Background()) })
	covGEmbedded(t, s, "kb", "install.md", "Run the installer with docker compose up.", nil)
	covGEmbedded(t, s, "kb", "backup.md", "Create a snapshot backup of the database file.", nil)
	return s
}

// searchStages are the spans a hybrid search records.
var searchStages = []string{spanSearchFTS, spanSearchEmbedQuery, spanSearchVector, spanSearchFuse, spanSearchLoadContent}

// assertChildren checks that each stage is a child of parent in its trace.
func assertChildren(t *testing.T, got map[string]otelSpan, parent otelSpan, stages ...string) {
	t.Helper()
	for _, n := range stages {
		if got[n].ParentSpanID != parent.SpanID {
			t.Errorf("%s: parent %s, want the server span %s", n, got[n].ParentSpanID, parent.SpanID)
		}
	}
}

func TestOTel_RESTHybridSearchJoinsTheCallersTrace(t *testing.T) {
	c := newOTelCollector(t)
	s := otelServer(t, c)
	h := s.withTracing(http.HandlerFunc(s.handleHybridSearch))
	req := httptest.NewRequest(http.MethodPost, "/v1/hybrid-search", strings.NewReader(`{"collection":"kb","query":"snapshot backup"}`))
	req.Header.Set("traceparent", otelParent)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("search: %d %s", rec.Code, rec.Body)
	}
	got := c.trace(t, otelTraceID, append([]string{"POST /v1/hybrid-search"}, searchStages...)...)
	server := got["POST /v1/hybrid-search"]
	if server.ParentSpanID != otelParentID || server.Kind != int(tracing.KindServer) {
		t.Fatalf("server span not parented to the caller: %+v", server)
	}
	assertChildren(t, got, server, searchStages...)
}

func TestOTel_GRPCSearchesCarryMetadataTraceparent(t *testing.T) {
	c := newOTelCollector(t)
	s := otelServer(t, c)
	lis := bufconn.Listen(1 << 20)
	srv := grpc.NewServer(grpc.ChainUnaryInterceptor(s.tracingUnaryInterceptor()))
	proto.RegisterMDDBServer(srv, NewGRPCServer(s))
	go func() { _ = srv.Serve(lis) }()
	defer srv.Stop()
	conn, err := grpc.NewClient("passthrough:///bufnet", grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	client := proto.NewMDDBClient(conn)

	ctx := metadata.AppendToOutgoingContext(context.Background(), "traceparent", otelParent)
	if _, err := client.HybridSearch(ctx, &proto.HybridSearchRequest{Collection: "kb", Query: "snapshot backup"}); err != nil {
		t.Fatal(err)
	}
	got := c.trace(t, otelTraceID, append([]string{"/mddb.MDDB/HybridSearch"}, searchStages...)...)
	server := got["/mddb.MDDB/HybridSearch"]
	if server.ParentSpanID != otelParentID {
		t.Fatalf("gRPC span not parented to the caller: %+v", server)
	}
	assertChildren(t, got, server, searchStages...)

	// Vector search in another trace: its stages, and a failing call marks
	// the server span.
	vecParent := "00-" + strings.Repeat("a", 32) + "-" + otelParentID + "-01"
	ctx = metadata.AppendToOutgoingContext(context.Background(), "traceparent", vecParent)
	if _, err := client.VectorSearch(ctx, &proto.VectorSearchRequest{Collection: "kb", Query: "installer"}); err != nil {
		t.Fatal(err)
	}
	vecStages := []string{spanSearchEmbedQuery, spanSearchVector, spanSearchLoadContent}
	got = c.trace(t, strings.Repeat("a", 32), append([]string{"/mddb.MDDB/VectorSearch"}, vecStages...)...)
	assertChildren(t, got, got["/mddb.MDDB/VectorSearch"], vecStages...)

	failParent := "00-" + strings.Repeat("b", 32) + "-" + otelParentID + "-01"
	ctx = metadata.AppendToOutgoingContext(context.Background(), "traceparent", failParent)
	if _, err := client.VectorSearch(ctx, &proto.VectorSearchRequest{}); err == nil {
		t.Fatal("a search without a collection succeeded")
	}
	if got := c.trace(t, strings.Repeat("b", 32), "/mddb.MDDB/VectorSearch"); got["/mddb.MDDB/VectorSearch"].Status.Code != 2 {
		t.Fatal("the failed call's span is not an error")
	}

	// No metadata at all: a new trace, rooted at the server span.
	before := c.count()
	if _, err := client.HybridSearch(context.Background(), &proto.HybridSearchRequest{Collection: "kb", Query: "backup"}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for c.count() < before+1+len(searchStages) {
		if time.Now().After(deadline) {
			t.Fatalf("an untraced call exported %d spans, want %d", c.count()-before, 1+len(searchStages))
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestOTel_MCPOverHTTPJoinsTheCallersTrace(t *testing.T) {
	c := newOTelCollector(t)
	s := otelServer(t, c)
	h := s.withTracing(http.HandlerFunc(s.newMCPHTTPServer().handleToolCall))
	req := httptest.NewRequest(http.MethodPost, "/tools/call",
		strings.NewReader(`{"name":"hybrid_search","arguments":{"collection":"kb","query":"snapshot backup"}}`))
	req.Header.Set("traceparent", otelParent)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("tool call: %d %s", rec.Code, rec.Body)
	}
	got := c.trace(t, otelTraceID, append([]string{"POST /tools/call"}, searchStages...)...)
	assertChildren(t, got, got["POST /tools/call"], searchStages...)
}

func TestOTel_MCPStdioStartsATracePerRequest(t *testing.T) {
	c := newOTelCollector(t)
	s := otelServer(t, c)
	var out bytes.Buffer
	before := c.count()
	s.serveMCPStdio(strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"hybrid_search","arguments":{"collection":"kb","query":"backup"}}}`+"\n"), &out)
	if !strings.Contains(out.String(), `"id":1`) {
		t.Fatalf("stdio answer: %s", out.String())
	}
	deadline := time.Now().Add(10 * time.Second)
	for c.count() < before+1+len(searchStages) && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	var root *otelSpan
	for i := range c.spans {
		if c.spans[i].Name == "mcp.stdio" {
			root = &c.spans[i]
		}
	}
	if root == nil || root.ParentSpanID != "" {
		t.Fatalf("stdio root span: %+v", root)
	}
}

func TestOTel_IngestConvertIndexAndEmbed(t *testing.T) {
	c := newOTelCollector(t)
	s := otelServer(t, c)
	h := s.withTracing(http.HandlerFunc(s.handleIngest))
	req := httptest.NewRequest(http.MethodPost, "/v1/ingest",
		strings.NewReader(`{"collection":"kb","documents":[{"key":"new.md","lang":"en","contentMd":"Freshly ingested text."}]}`))
	req.Header.Set("traceparent", otelParent)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("ingest: %d %s", rec.Code, rec.Body)
	}
	stages := []string{spanIngestConvert, spanIngestIndex, spanIngestEmbed}
	got := c.trace(t, otelTraceID, append([]string{"POST /v1/ingest"}, stages...)...)
	assertChildren(t, got, got["POST /v1/ingest"], stages...)

	// A batch the quota refuses marks ingest.index.
	setTestQuota(t, s, "kb", CollectionQuota{MaxDocuments: 1})
	ctx, sp := s.Tracer.StartServer(context.Background(), "ingest-fail", "00-"+strings.Repeat("c", 32)+"-"+otelParentID+"-01")
	if _, err := s.ingestDocuments(ctx, "kb", []IngestDocumentHTTP{{Key: "x.md", Lang: "en", ContentMD: "x"}}, IngestOptionsHTTP{}); err == nil {
		t.Fatal("the quota let the batch in")
	}
	sp.End()
	got = c.trace(t, strings.Repeat("c", 32), spanIngestIndex)
	if got[spanIngestIndex].Status.Code != 2 {
		t.Fatal("a failed write is not an error span")
	}
}

func TestOTel_EmbedFailureMarksTheSpan(t *testing.T) {
	c := newOTelCollector(t)
	s := otelServer(t, c)
	_, sp := s.Tracer.StartServer(context.Background(), "embed-fail", "00-"+strings.Repeat("d", 32)+"-"+otelParentID+"-01")
	w := NewEmbeddingWorker(covGFailEmbedding{&mockEmbedding{dims: 3, model: "m"}}, s.VectorStore, s.VectorIndex, 4)
	w.tracer = s.Tracer
	old := embeddingRetryDelay
	embeddingRetryDelay = time.Millisecond
	defer func() { embeddingRetryDelay = old }()
	w.run(EmbeddingJob{Collection: "kb", DocID: "nope", ContentMD: "text", trace: sp.Context()})
	sp.End()
	got := c.trace(t, strings.Repeat("d", 32), spanIngestEmbed)
	if got[spanIngestEmbed].Status.Code != 2 {
		t.Fatal("a failed embedding is not an error span")
	}
}

func TestOTel_UnsampledAndUntracedRequests(t *testing.T) {
	c := newOTelCollector(t)
	s := otelServer(t, c)
	h := s.withTracing(http.HandlerFunc(s.handleHybridSearch))

	// The caller decided not to sample: nothing is exported.
	before := c.count()
	req := httptest.NewRequest(http.MethodPost, "/v1/hybrid-search", strings.NewReader(`{"collection":"kb","query":"backup"}`))
	req.Header.Set("traceparent", "00-"+strings.Repeat("e", 32)+"-"+otelParentID+"-00")
	h.ServeHTTP(httptest.NewRecorder(), req)

	// No traceparent: a new trace begins at the server span.
	req = httptest.NewRequest(http.MethodPost, "/v1/hybrid-search", strings.NewReader(`{"collection":"kb","query":"backup"}`))
	h.ServeHTTP(httptest.NewRecorder(), req)
	deadline := time.Now().Add(10 * time.Second)
	for c.count() < before+1+len(searchStages) && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(50 * time.Millisecond)
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.spans)-before != 1+len(searchStages) {
		t.Fatalf("exported %d spans, want only the untraced request's %d", len(c.spans)-before, 1+len(searchStages))
	}
	for _, sp := range c.spans[before:] {
		if sp.TraceID == strings.Repeat("e", 32) {
			t.Fatal("an unsampled trace was exported")
		}
		if sp.Name == "POST /v1/hybrid-search" && sp.ParentSpanID != "" {
			t.Fatal("an untraced request's server span has a parent")
		}
	}
}

func TestOTel_ServerErrorsMarkTheHTTPSpan(t *testing.T) {
	c := newOTelCollector(t)
	s := otelServer(t, c)
	h := s.withTracing(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	req := httptest.NewRequest(http.MethodGet, "/v1/boom", nil)
	req.Header.Set("traceparent", otelParent)
	h.ServeHTTP(httptest.NewRecorder(), req)
	if got := c.trace(t, otelTraceID, "GET /v1/boom"); got["GET /v1/boom"].Status.Code != 2 {
		t.Fatal("a 500 is not an error span")
	}
	if errHTTPStatus(503).Error() != "Service Unavailable" {
		t.Fatal("status text")
	}
}

// TestOTel_OffChangesNothing: with no endpoint there is no tracer, the
// chains are not wrapped and no stage records.
func TestOTel_OffChangesNothing(t *testing.T) {
	t.Setenv("MDDB_OTEL_ENDPOINT", "")
	s := covGVectorServer(t)
	s.Metrics = metrics.NewMetrics(true, &serverMetricsStats{s: s})
	s.initTracing()
	if s.Tracer != nil || s.EmbeddingWorker.tracer != nil {
		t.Fatal("tracing on without an endpoint")
	}
	next := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
	if h := s.withTracing(next); h == nil || s.requestTrace(context.Background()).IsValid() {
		t.Fatal("off is not a no-op")
	}
	opts, _ := s.grpcServerOptions(false)
	if len(opts) != 2 { // alias chains only
		t.Fatalf("gRPC options with tracing off: %d", len(opts))
	}
	s.shutdownTracing() // a nil tracer shuts down cleanly
}

// TestOTel_InitFromEnvCountsDropsAndFailures wires the tracer the way the
// server does and checks its losses reach /metrics.
func TestOTel_InitFromEnvCountsDropsAndFailures(t *testing.T) {
	dead := httptest.NewServer(http.NotFoundHandler())
	dead.Close()
	t.Setenv("MDDB_OTEL_ENDPOINT", dead.URL)
	t.Setenv("MDDB_OTEL_QUEUE_SIZE", "1")
	s := covGVectorServer(t)
	s.Metrics = metrics.NewMetrics(true, &serverMetricsStats{s: s})
	s.RateLimiter = NewRateLimiter()
	s.initTracing()
	if s.Tracer == nil || s.EmbeddingWorker.tracer != s.Tracer {
		t.Fatal("tracing not wired")
	}
	if opts, _ := s.grpcServerOptions(false); len(opts) != 2 {
		t.Fatalf("gRPC options: %d", len(opts))
	}
	if steps := s.shutdownSteps(); !hasShutdownStep(steps, "tracing") {
		t.Fatal("no tracing shutdown step")
	}
	_, sp := s.Tracer.StartServer(context.Background(), "lost", "")
	sp.End()
	s.shutdownTracing() // the export fails: counted
	for range 2 {       // after shutdown the queue of one fills, then drops
		_, late := s.Tracer.StartServer(context.Background(), "late", "")
		late.End()
	}
	rec := httptest.NewRecorder()
	s.Metrics.HandleMetrics(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := rec.Body.String()
	if !strings.Contains(body, `operation="otel_spans_dropped"`) || !strings.Contains(body, `operation="otel_export_failed"`) {
		t.Fatalf("losses not counted:\n%s", body)
	}
}

func hasShutdownStep(steps []shutdownStep, name string) bool {
	for _, st := range steps {
		if st.name == name {
			return true
		}
	}
	return false
}

func TestOTel_ShutdownTracingReportsATimeout(t *testing.T) {
	block := make(chan struct{})
	stuck := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { <-block }))
	defer stuck.Close()
	defer close(block)
	s := &Server{Tracer: tracing.New(tracing.Config{Endpoint: stuck.URL, SampleRatio: 1, FlushInterval: time.Millisecond})}
	_, sp := s.Tracer.StartServer(context.Background(), "x", "")
	sp.End()
	time.Sleep(20 * time.Millisecond) // the exporter is now stuck in its POST
	old := tracerShutdownTimeout
	tracerShutdownTimeout = 20 * time.Millisecond
	defer func() { tracerShutdownTimeout = old }()
	done := make(chan struct{})
	go func() { s.shutdownTracing(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("shutdownTracing did not give up")
	}
}
