package tracing

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// The exporter: spans go into a bounded queue, one goroutine drains it in
// batches and POSTs each batch as OTLP JSON. A request never waits for the
// collector — when the queue is full the span is dropped and counted — and a
// collector that is down costs a log line per batch, not a slow search.

// Tracer is the enabled tracer. Build one with New; a nil *Tracer is tracing
// switched off.
type Tracer struct {
	cfg    Config
	bound  uint64 // sampling threshold: see sampled
	queue  chan *Span
	client *http.Client
	stop   chan struct{}
	done   chan struct{}
	once   sync.Once
}

// New starts a tracer exporting to cfg.Endpoint. It returns nil — tracing off
// — when the endpoint is empty, so a caller can hand its result straight to
// the instrumented code.
func New(cfg Config) *Tracer {
	if cfg.Endpoint == "" {
		return nil
	}
	cfg = cfg.withDefaults()
	t := &Tracer{
		cfg:    cfg,
		bound:  ratioBound(cfg.SampleRatio),
		queue:  make(chan *Span, cfg.QueueSize),
		client: &http.Client{Timeout: cfg.ExportTimeout},
		stop:   make(chan struct{}),
		done:   make(chan struct{}),
	}
	go t.run()
	return t
}

// ratioBound turns a sample ratio into the threshold sampled compares with:
// ratio × 2^63, everything at 1 or above, nothing at 0 or below.
func ratioBound(ratio float64) uint64 {
	switch {
	case ratio >= 1:
		return math.MaxUint64
	case ratio <= 0:
		return 0
	}
	return uint64(ratio * (1 << 63))
}

// enqueue offers an ended span to the exporter without ever blocking.
func (t *Tracer) enqueue(s *Span) {
	select {
	case t.queue <- s:
	default:
		if t.cfg.OnDrop != nil {
			t.cfg.OnDrop()
		}
	}
}

// run batches the queue until Shutdown: a batch goes out when it is full or
// when the flush interval passes, whichever comes first.
func (t *Tracer) run() {
	defer close(t.done)
	ticker := time.NewTicker(t.cfg.FlushInterval)
	defer ticker.Stop()
	batch := make([]*Span, 0, t.cfg.BatchSize)
	for {
		select {
		case s := <-t.queue:
			if batch = append(batch, s); len(batch) >= t.cfg.BatchSize {
				batch = t.flush(batch)
			}
		case <-ticker.C:
			batch = t.flush(batch)
		case <-t.stop:
			for {
				select {
				case s := <-t.queue:
					if batch = append(batch, s); len(batch) >= t.cfg.BatchSize {
						batch = t.flush(batch)
					}
				default:
					t.flush(batch)
					return
				}
			}
		}
	}
}

// flush exports batch and returns it emptied for reuse.
func (t *Tracer) flush(batch []*Span) []*Span {
	if len(batch) == 0 {
		return batch
	}
	if err := t.export(batch); err != nil {
		slog.Debug("OTLP trace export failed", "endpoint", t.cfg.Endpoint, "spans", len(batch), "err", err)
		if t.cfg.OnExportError != nil {
			t.cfg.OnExportError()
		}
	}
	clear(batch)
	return batch[:0]
}

// export POSTs one batch to the collector.
func (t *Tracer) export(batch []*Span) error {
	body, err := json.Marshal(t.encode(batch))
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), t.cfg.ExportTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.cfg.Endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range t.cfg.Headers {
		req.Header.Set(k, v)
	}
	resp, err := t.client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("collector answered %s", resp.Status)
	}
	return nil
}

// Shutdown stops the exporter after sending what is queued, waiting at most
// until ctx ends. Spans ended afterwards are dropped. Safe to call twice and
// on a nil tracer.
func (t *Tracer) Shutdown(ctx context.Context) error {
	if t == nil {
		return nil
	}
	t.once.Do(func() { close(t.stop) })
	select {
	case <-t.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// OTLP/HTTP JSON (opentelemetry-proto, trace/v1): IDs are hex, not base64,
// and 64-bit integers are strings, as the OTLP JSON mapping specifies.

type otlpRequest struct {
	ResourceSpans []otlpResourceSpans `json:"resourceSpans"`
}

type otlpResourceSpans struct {
	Resource   otlpResource     `json:"resource"`
	ScopeSpans []otlpScopeSpans `json:"scopeSpans"`
}

type otlpResource struct {
	Attributes []otlpKeyValue `json:"attributes"`
}

type otlpScopeSpans struct {
	Scope otlpScope  `json:"scope"`
	Spans []otlpSpan `json:"spans"`
}

type otlpScope struct {
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
}

type otlpSpan struct {
	TraceID           string         `json:"traceId"`
	SpanID            string         `json:"spanId"`
	ParentSpanID      string         `json:"parentSpanId,omitempty"`
	Flags             uint32         `json:"flags"`
	Name              string         `json:"name"`
	Kind              Kind           `json:"kind"`
	StartTimeUnixNano string         `json:"startTimeUnixNano"`
	EndTimeUnixNano   string         `json:"endTimeUnixNano"`
	Attributes        []otlpKeyValue `json:"attributes,omitempty"`
	Status            otlpStatus     `json:"status"`
}

type otlpKeyValue struct {
	Key   string    `json:"key"`
	Value otlpValue `json:"value"`
}

type otlpValue struct {
	StringValue *string `json:"stringValue,omitempty"`
	IntValue    *string `json:"intValue,omitempty"`
}

type otlpStatus struct {
	Code    int    `json:"code,omitempty"` // 0 unset, 2 error
	Message string `json:"message,omitempty"`
}

// stringAttr is a string attribute in OTLP form.
func stringAttr(key, value string) otlpKeyValue {
	return otlpKeyValue{Key: key, Value: otlpValue{StringValue: &value}}
}

// encode turns a batch into one OTLP request: one resource (this process),
// one instrumentation scope, every span.
func (t *Tracer) encode(batch []*Span) otlpRequest {
	spans := make([]otlpSpan, 0, len(batch))
	for _, s := range batch {
		spans = append(spans, encodeSpan(s))
	}
	return otlpRequest{ResourceSpans: []otlpResourceSpans{{
		Resource: otlpResource{Attributes: []otlpKeyValue{
			stringAttr("service.name", t.cfg.ServiceName),
			stringAttr("service.version", t.cfg.ServiceVersion),
		}},
		ScopeSpans: []otlpScopeSpans{{Scope: otlpScope{Name: "mddb", Version: t.cfg.ServiceVersion}, Spans: spans}},
	}}}
}

// encodeSpan is one span in OTLP form.
func encodeSpan(s *Span) otlpSpan {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := otlpSpan{
		TraceID:           hex.EncodeToString(s.sc.TraceID[:]),
		SpanID:            hex.EncodeToString(s.sc.SpanID[:]),
		Flags:             1, // sampled: only sampled spans are exported
		Name:              s.name,
		Kind:              s.kind,
		StartTimeUnixNano: strconv.FormatInt(s.start.UnixNano(), 10),
		EndTimeUnixNano:   strconv.FormatInt(s.end.UnixNano(), 10),
	}
	if s.parent != ([8]byte{}) {
		out.ParentSpanID = hex.EncodeToString(s.parent[:])
	}
	for _, a := range s.attrs {
		if a.isNum {
			n := strconv.FormatInt(a.num, 10)
			out.Attributes = append(out.Attributes, otlpKeyValue{Key: a.key, Value: otlpValue{IntValue: &n}})
			continue
		}
		out.Attributes = append(out.Attributes, stringAttr(a.key, a.str))
	}
	if s.errMsg != "" {
		out.Status = otlpStatus{Code: 2, Message: s.errMsg}
	}
	return out
}
