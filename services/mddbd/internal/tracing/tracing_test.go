package tracing

import (
	"context"
	"errors"
	"strings"
	"testing"
)

const (
	testTraceID = "4bf92f3577b34da6a3ce929d0e0e4736"
	testSpanID  = "00f067aa0ba902b7"
	testParent  = "00-" + testTraceID + "-" + testSpanID + "-01"
)

func TestParseTraceparent(t *testing.T) {
	sc, ok := ParseTraceparent(testParent)
	if !ok || !sc.Sampled || sc.Traceparent() != testParent {
		t.Fatalf("valid header: %+v %v", sc, ok)
	}
	unsampled, ok := ParseTraceparent("00-" + testTraceID + "-" + testSpanID + "-00")
	if !ok || unsampled.Sampled || !strings.HasSuffix(unsampled.Traceparent(), "-00") {
		t.Fatalf("unsampled: %+v %v", unsampled, ok)
	}
	// A future version may append fields after a dash; the first four count.
	if _, ok := ParseTraceparent("01-" + testTraceID + "-" + testSpanID + "-01-extra"); !ok {
		t.Fatal("future version with extra field refused")
	}
	for name, h := range map[string]string{
		"empty":            "",
		"short":            "00-abc",
		"version ff":       "ff-" + testTraceID + "-" + testSpanID + "-01",
		"bad version":      "0g-" + testTraceID + "-" + testSpanID + "-01",
		"v00 too long":     testParent + "-x",
		"future no dash":   "01-" + testTraceID + "-" + testSpanID + "-01x",
		"uppercase":        "00-" + strings.ToUpper(testTraceID) + "-" + testSpanID + "-01",
		"bad span":         "00-" + testTraceID + "-" + "zz" + testSpanID[2:] + "-01",
		"bad flags":        "00-" + testTraceID + "-" + testSpanID + "-0z",
		"zero trace":       "00-" + strings.Repeat("0", 32) + "-" + testSpanID + "-01",
		"zero span":        "00-" + testTraceID + "-" + strings.Repeat("0", 16) + "-01",
		"misplaced dashes": "00_" + testTraceID + "-" + testSpanID + "-01",
	} {
		if _, ok := ParseTraceparent(h); ok {
			t.Errorf("%s: accepted %q", name, h)
		}
	}
}

func TestNilTracerAndSpanDoNothing(t *testing.T) {
	var tr *Tracer
	ctx := context.Background()
	got, sp := tr.StartServer(ctx, "x", testParent)
	if got != ctx || sp != nil {
		t.Fatal("nil tracer started a span")
	}
	if tr.Stage(ctx, "x") != nil || tr.Child(SpanContext{}, "x") != nil {
		t.Fatal("nil tracer staged a span")
	}
	sp.SetString("k", "v")
	sp.SetInt("k", 1)
	sp.SetError(errors.New("x"))
	sp.End()
	if sp.Context().IsValid() || FromContext(ctx) != nil {
		t.Fatal("nil span has a context")
	}
	if err := tr.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
}

// TestDisabledIsFree is the zero-cost promise: with tracing off, starting and
// ending a stage allocates nothing.
func TestDisabledIsFree(t *testing.T) {
	var tr *Tracer
	ctx := context.Background()
	allocs := testing.AllocsPerRun(1000, func() {
		sp := tr.Stage(ctx, "search.fts")
		sp.SetString("mddb.collection", "c")
		sp.SetInt("mddb.results", 3)
		sp.End()
		_, srv := tr.StartServer(ctx, "GET /", "")
		srv.End()
	})
	if allocs != 0 {
		t.Fatalf("disabled tracing allocated %v per run", allocs)
	}
}

func TestSpansNestUnderTheServerSpan(t *testing.T) {
	tr := &Tracer{bound: ratioBound(1), queue: make(chan *Span, 16)}
	ctx, srv := tr.StartServer(context.Background(), "POST /v1/search", testParent)
	if FromContext(ctx) != srv || srv.kind != KindServer {
		t.Fatal("server span not in context")
	}
	if got := srv.Context(); hexOf(got.TraceID[:]) != testTraceID || hexOf(srv.parent[:]) != testSpanID || !got.Sampled {
		t.Fatalf("server span did not continue the caller's trace: %+v", got)
	}
	st := tr.Stage(ctx, "search.fts")
	if st.sc.TraceID != srv.sc.TraceID || st.parent != srv.sc.SpanID || st.kind != KindInternal {
		t.Fatal("stage not a child of the server span")
	}
	if tr.Stage(context.Background(), "orphan") != nil {
		t.Fatal("a stage without a parent started a trace")
	}
	st.End()
	st.End() // twice records once
	srv.End()
	if len(tr.queue) != 2 {
		t.Fatalf("queued %d spans, want 2", len(tr.queue))
	}

	// No traceparent: a new root.
	_, root := tr.StartServer(context.Background(), "GET /", "")
	if !root.sc.IsValid() || root.parent != ([8]byte{}) || !root.sc.Sampled {
		t.Fatalf("root span: %+v", root)
	}

	// An unsampled caller: propagated, never exported, and no stages.
	uctx, us := tr.StartServer(context.Background(), "GET /", "00-"+testTraceID+"-"+testSpanID+"-00")
	if us.sc.Sampled || tr.Stage(uctx, "x") != nil || tr.Child(us.Context(), "x") != nil {
		t.Fatal("unsampled trace recorded stages")
	}
	us.End()
	if len(tr.queue) != 2 {
		t.Fatal("unsampled span was queued")
	}
}

func TestSampling(t *testing.T) {
	never := &Tracer{bound: ratioBound(0)}
	always := &Tracer{bound: ratioBound(1)}
	half := &Tracer{bound: ratioBound(0.5)}
	var lo, hi [16]byte
	hi[8] = 0xff
	if never.sampled(hi) || never.sampled(lo) || !always.sampled(hi) || !half.sampled(lo) || half.sampled(hi) {
		t.Fatal("ratio decision")
	}
	if ratioBound(-1) != 0 || ratioBound(2) != ratioBound(1) {
		t.Fatal("ratio clamp")
	}
}

func hexOf(b []byte) string {
	const digits = "0123456789abcdef"
	out := make([]byte, 0, len(b)*2)
	for _, c := range b {
		out = append(out, digits[c>>4], digits[c&15])
	}
	return string(out)
}
