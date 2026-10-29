// Package tracing records server spans and exports them as OTLP/HTTP JSON
// (OPS-024).
//
// Prometheus says how many searches ran and how long they took in aggregate;
// it cannot say why one query took 900 ms. A span per stage — full-text,
// query embedding, vector search, fusion, loading content — can, and agent
// frameworks already send their own spans to an OpenTelemetry collector and
// expect the database to add its spans to the same trace (W3C traceparent).
//
// This package is that, without the OpenTelemetry SDK: the SDK and its OTLP
// exporter added 1.5 MB to the stripped binary for a feature most deployments
// leave off (docs/TELEMETRY.md has the measurement). What MDDB needs is small:
// parse and continue a traceparent, time named stages, batch them and POST
// them as OTLP JSON.
//
// Off is free. A nil *Tracer is the disabled tracer: every method on it, and
// on the nil *Span it hands out, returns at once without allocating, so the
// instrumented paths cost a nil check when MDDB_OTEL_ENDPOINT is unset.
package tracing

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"sync"
	"time"
)

// SpanContext identifies a span across process boundaries: the part of a span
// a traceparent header carries.
type SpanContext struct {
	TraceID [16]byte
	SpanID  [8]byte
	// Sampled is the traceparent's sampled flag: an unsampled span is
	// propagated but never exported.
	Sampled bool
}

// IsValid reports whether the trace and span IDs are both non-zero, as W3C
// trace context requires.
func (sc SpanContext) IsValid() bool {
	return sc.TraceID != [16]byte{} && sc.SpanID != [8]byte{}
}

// Traceparent renders the context as a version-00 traceparent header value.
func (sc SpanContext) Traceparent() string {
	flags := "00"
	if sc.Sampled {
		flags = "01"
	}
	return "00-" + hex.EncodeToString(sc.TraceID[:]) + "-" + hex.EncodeToString(sc.SpanID[:]) + "-" + flags
}

// ParseTraceparent reads a W3C traceparent header value
// (version-traceid-parentid-flags). It accepts future versions as the
// specification asks — the first four fields are read and anything after them
// ignored — and refuses version ff, all-zero IDs and malformed hex.
func ParseTraceparent(h string) (SpanContext, bool) {
	var sc SpanContext
	if len(h) < 55 || h[2] != '-' || h[35] != '-' || h[52] != '-' {
		return sc, false
	}
	version := h[:2]
	if version == "ff" || !isLowerHex(version) || (version == "00" && len(h) != 55) || (len(h) > 55 && h[55] != '-') {
		return sc, false
	}
	if !isLowerHex(h[3:35]) || !isLowerHex(h[36:52]) || !isLowerHex(h[53:55]) {
		return sc, false
	}
	_, _ = hex.Decode(sc.TraceID[:], []byte(h[3:35]))
	_, _ = hex.Decode(sc.SpanID[:], []byte(h[36:52]))
	var flags [1]byte
	_, _ = hex.Decode(flags[:], []byte(h[53:55]))
	sc.Sampled = flags[0]&1 == 1
	return sc, sc.IsValid()
}

// isLowerHex reports whether s is lowercase hexadecimal; traceparent forbids
// uppercase.
func isLowerHex(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// Kind is the OTLP span kind.
type Kind int

// OTLP span kinds MDDB records.
const (
	KindInternal Kind = 1
	KindServer   Kind = 2
)

// attr is one span attribute: a string or an int64.
type attr struct {
	key   string
	str   string
	num   int64
	isNum bool
}

// Span is one timed operation. A nil *Span is a span nobody records: every
// method on it does nothing.
type Span struct {
	tracer *Tracer
	sc     SpanContext
	parent [8]byte
	name   string
	kind   Kind
	start  time.Time

	mu     sync.Mutex
	end    time.Time
	attrs  []attr
	errMsg string
	ended  bool
}

// Context is the span's identity, for propagating it — to an embedding job
// that runs after the request has answered, for example.
func (s *Span) Context() SpanContext {
	if s == nil {
		return SpanContext{}
	}
	return s.sc
}

// SetString records a string attribute.
func (s *Span) SetString(key, value string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.attrs = append(s.attrs, attr{key: key, str: value})
	s.mu.Unlock()
}

// SetInt records an integer attribute.
func (s *Span) SetInt(key string, value int) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.attrs = append(s.attrs, attr{key: key, num: int64(value), isNum: true})
	s.mu.Unlock()
}

// SetError marks the span failed with err's message. A nil err changes
// nothing, so a call site can pass whatever error it has.
func (s *Span) SetError(err error) {
	if s == nil || err == nil {
		return
	}
	s.mu.Lock()
	s.errMsg = err.Error()
	s.mu.Unlock()
}

// End stops the clock and hands a sampled span to the exporter. Ending a span
// twice records it once.
func (s *Span) End() {
	if s == nil {
		return
	}
	s.mu.Lock()
	if s.ended {
		s.mu.Unlock()
		return
	}
	s.ended, s.end = true, time.Now()
	s.mu.Unlock()
	if s.sc.Sampled {
		s.tracer.enqueue(s)
	}
}

// spanKey is the context key of the current span.
type spanKey struct{}

// FromContext returns the span ctx carries, or nil.
func FromContext(ctx context.Context) *Span {
	sp, _ := ctx.Value(spanKey{}).(*Span)
	return sp
}

// StartServer begins the span of an incoming request. traceparent is the
// header the caller sent, "" when there was none: a valid one makes this span
// its child in the caller's trace, otherwise a new trace begins, sampled at
// the configured ratio. The returned context carries the span for Stage.
func (t *Tracer) StartServer(ctx context.Context, name, traceparent string) (context.Context, *Span) {
	if t == nil {
		return ctx, nil
	}
	var sp *Span
	if remote, ok := ParseTraceparent(traceparent); ok {
		sp = t.newSpan(remote, name, KindServer)
	} else {
		var root SpanContext
		root.TraceID = newTraceID()
		root.Sampled = t.sampled(root.TraceID)
		sp = t.newSpan(root, name, KindServer) // root.SpanID is zero: no parent
	}
	return context.WithValue(ctx, spanKey{}, sp), sp
}

// Stage begins a child of the span ctx carries: one stage of the request,
// such as "search.fts". It returns nil — a span that records nothing — when
// tracing is off, when ctx carries no span, or when the trace is not sampled,
// so a stage never starts a trace of its own.
func (t *Tracer) Stage(ctx context.Context, name string) *Span {
	if t == nil {
		return nil
	}
	parent := FromContext(ctx)
	if parent == nil {
		return nil
	}
	return t.Child(parent.sc, name)
}

// Child begins a span under parent, for work that outlives the request that
// caused it: the embedding of an ingested document runs on a worker after the
// response. Nil when tracing is off or parent is invalid or unsampled.
func (t *Tracer) Child(parent SpanContext, name string) *Span {
	if t == nil || !parent.IsValid() || !parent.Sampled {
		return nil
	}
	return t.newSpan(parent, name, KindInternal)
}

// newSpan starts a span in parent's trace with a fresh span ID; parent's span
// ID becomes its parent.
func (t *Tracer) newSpan(parent SpanContext, name string, kind Kind) *Span {
	sp := &Span{tracer: t, name: name, kind: kind, start: time.Now(), parent: parent.SpanID}
	sp.sc = SpanContext{TraceID: parent.TraceID, SpanID: newSpanID(), Sampled: parent.Sampled}
	return sp
}

// sampled decides a new trace by its ID, the way OpenTelemetry's
// TraceIDRatioBased sampler does: the low 63 bits of the ID's second half
// against ratio × 2^63. Deciding by ID rather than by coin keeps the decision
// the same for every process that sees the trace.
func (t *Tracer) sampled(id [16]byte) bool {
	return binary.BigEndian.Uint64(id[8:])>>1 < t.bound
}

// newTraceID returns 16 random bytes, never all zero.
func newTraceID() [16]byte {
	var id [16]byte
	for id == ([16]byte{}) {
		_, _ = rand.Read(id[:])
	}
	return id
}

// newSpanID returns 8 random bytes, never all zero.
func newSpanID() [8]byte {
	var id [8]byte
	for id == ([8]byte{}) {
		_, _ = rand.Read(id[:])
	}
	return id
}
