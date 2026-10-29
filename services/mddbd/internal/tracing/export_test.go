package tracing

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// receiver is a minimal OTLP/HTTP JSON collector.
type receiver struct {
	srv     *httptest.Server
	mu      sync.Mutex
	reqs    []otlpRequest
	headers []http.Header
	status  int
}

func newReceiver(t *testing.T) *receiver {
	t.Helper()
	r := &receiver{status: http.StatusOK}
	r.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		var body otlpRequest
		if req.URL.Path != "/v1/traces" || req.Header.Get("Content-Type") != "application/json" {
			http.Error(w, "wrong path or type", http.StatusNotFound)
			return
		}
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		r.mu.Lock()
		r.reqs = append(r.reqs, body)
		r.headers = append(r.headers, req.Header.Clone())
		status := r.status
		r.mu.Unlock()
		w.WriteHeader(status)
	}))
	t.Cleanup(r.srv.Close)
	return r
}

func (r *receiver) spans() []otlpSpan {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []otlpSpan
	for _, req := range r.reqs {
		for _, rs := range req.ResourceSpans {
			for _, ss := range rs.ScopeSpans {
				out = append(out, ss.Spans...)
			}
		}
	}
	return out
}

func TestExportOTLPJSON(t *testing.T) {
	rcv := newReceiver(t)
	tr := New(Config{SampleRatio: 1, Endpoint: rcv.srv.URL + "/v1/traces", Headers: map[string]string{"X-Api-Key": "k"},
		ServiceVersion: "9.9.9", BatchSize: 2, FlushInterval: time.Hour})
	ctx, srv := tr.StartServer(context.Background(), "POST /v1/hybrid-search", testParent)
	st := tr.Stage(ctx, "search.fts")
	st.SetString("mddb.collection", "kb")
	st.SetInt("mddb.results", 7)
	st.SetError(errors.New("index gone"))
	st.End()
	srv.End() // the second span fills the batch: exported without waiting
	deadline := time.Now().Add(5 * time.Second)
	for len(rcv.spans()) < 2 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if err := tr.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	spans := rcv.spans()
	if len(spans) != 2 {
		t.Fatalf("exported %d spans, want 2", len(spans))
	}
	fts, server := spans[0], spans[1]
	if fts.TraceID != testTraceID || server.TraceID != testTraceID || server.ParentSpanID != testSpanID ||
		fts.ParentSpanID != server.SpanID || fts.Name != "search.fts" || server.Kind != KindServer || fts.Kind != KindInternal {
		t.Fatalf("ids or names: %+v %+v", fts, server)
	}
	if fts.StartTimeUnixNano == "" || fts.EndTimeUnixNano < fts.StartTimeUnixNano || fts.Status.Code != 2 || fts.Status.Message != "index gone" {
		t.Fatalf("times or status: %+v", fts)
	}
	if len(fts.Attributes) != 2 || *fts.Attributes[0].Value.StringValue != "kb" || *fts.Attributes[1].Value.IntValue != "7" {
		t.Fatalf("attributes: %+v", fts.Attributes)
	}
	res := rcv.reqs[0].ResourceSpans[0]
	if *res.Resource.Attributes[0].Value.StringValue != "mddb" || *res.Resource.Attributes[1].Value.StringValue != "9.9.9" ||
		res.ScopeSpans[0].Scope.Name != "mddb" {
		t.Fatalf("resource: %+v", res)
	}
	if rcv.headers[0].Get("X-Api-Key") != "k" {
		t.Fatal("configured header not sent")
	}
}

func TestExportOnTickAndShutdownFlush(t *testing.T) {
	rcv := newReceiver(t)
	tr := New(Config{SampleRatio: 1, Endpoint: rcv.srv.URL + "/v1/traces", FlushInterval: 10 * time.Millisecond})
	_, sp := tr.StartServer(context.Background(), "tick", "")
	sp.End()
	deadline := time.Now().Add(5 * time.Second)
	for len(rcv.spans()) < 1 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if len(rcv.spans()) != 1 {
		t.Fatal("the flush interval did not export")
	}

	// Shutdown sends what is still queued, filling batches as it drains.
	// The spans are queued before the exporter starts, so its drain finds them.
	cfg := Config{Endpoint: rcv.srv.URL + "/v1/traces", FlushInterval: time.Hour, BatchSize: 2}.withDefaults()
	tr3 := &Tracer{cfg: cfg, bound: ratioBound(1), queue: make(chan *Span, 8), client: http.DefaultClient,
		stop: make(chan struct{}), done: make(chan struct{})}
	if err := tr.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		_, s := tr3.StartServer(context.Background(), "drain", "")
		s.End()
	}
	go tr3.run()
	if err := tr3.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := tr3.Shutdown(context.Background()); err != nil { // twice is fine
		t.Fatal(err)
	}
	if got := len(rcv.spans()); got != 4 {
		t.Fatalf("after shutdown %d spans, want 4", got)
	}
}

func TestFullQueueDropsAndFailuresCount(t *testing.T) {
	var drops, failures atomic.Int32
	rcv := newReceiver(t)
	rcv.status = http.StatusServiceUnavailable
	tr := New(Config{SampleRatio: 1, Endpoint: rcv.srv.URL + "/v1/traces", QueueSize: 1, FlushInterval: time.Hour,
		OnDrop: func() { drops.Add(1) }, OnExportError: func() { failures.Add(1) }})
	_, sp := tr.StartServer(context.Background(), "a", "")
	sp.End()
	if err := tr.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if failures.Load() != 1 {
		t.Fatalf("a 503 counted %d failures", failures.Load())
	}
	// After shutdown nothing drains: the first span fills the queue of one,
	// the second is dropped.
	for range 2 {
		_, s := tr.StartServer(context.Background(), "late", "")
		s.End()
	}
	if drops.Load() != 1 {
		t.Fatalf("dropped %d, want 1", drops.Load())
	}

	// Without hooks a drop and a failure are silent, never a panic.
	quiet := New(Config{SampleRatio: 1, Endpoint: "http://127.0.0.1:1/v1/traces", QueueSize: 1, ExportTimeout: time.Second})
	_, q := quiet.StartServer(context.Background(), "q", "")
	q.End()
	_ = quiet.Shutdown(context.Background())
	for range 2 {
		_, s := quiet.StartServer(context.Background(), "late", "")
		s.End()
	}
}

func TestExportErrors(t *testing.T) {
	tr := &Tracer{cfg: Config{Endpoint: "http://bad host/", ExportTimeout: time.Second}.withDefaults(), client: http.DefaultClient}
	sp := &Span{name: "x", start: time.Now(), end: time.Now()}
	if err := tr.export([]*Span{sp}); err == nil {
		t.Fatal("an invalid URL exported")
	}
	tr.cfg.Endpoint = "http://127.0.0.1:1/v1/traces"
	if err := tr.export([]*Span{sp}); err == nil {
		t.Fatal("a refused connection exported")
	}
}

func TestShutdownHonoursContext(t *testing.T) {
	block := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		<-block
	}))
	defer srv.Close()
	defer close(block)
	tr := New(Config{SampleRatio: 1, Endpoint: srv.URL, FlushInterval: time.Hour})
	_, sp := tr.StartServer(context.Background(), "slow", "")
	sp.End()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := tr.Shutdown(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("shutdown against a stuck collector: %v", err)
	}
}

func TestNewWithoutEndpointIsOff(t *testing.T) {
	if New(Config{}) != nil {
		t.Fatal("no endpoint, yet a tracer")
	}
}
