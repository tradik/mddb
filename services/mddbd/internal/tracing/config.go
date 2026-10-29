package tracing

import (
	"math"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"mddb/internal/envconf"
)

// Config is how the tracer exports. FromEnv reads it from MDDB_OTEL_*.
type Config struct {
	// Endpoint is the collector's OTLP/HTTP traces URL. Empty = tracing off.
	Endpoint string
	// Headers are sent with every export — an API key for a hosted backend.
	Headers map[string]string
	// SampleRatio is the share of new traces recorded, 0-1. A trace the
	// caller started keeps the caller's decision (the traceparent flag).
	// Zero records no new trace; FromEnv defaults it to 1.
	SampleRatio float64
	// ServiceName and ServiceVersion are the resource attributes every span
	// carries.
	ServiceName    string
	ServiceVersion string
	// QueueSize bounds the spans waiting for export; a span ended while it is
	// full is dropped and OnDrop called.
	QueueSize int
	// BatchSize is the most spans one export carries; FlushInterval is the
	// longest a span waits for its batch to fill.
	BatchSize     int
	FlushInterval time.Duration
	// ExportTimeout bounds one POST to the collector.
	ExportTimeout time.Duration
	// OnDrop and OnExportError count what was lost: a span the full queue
	// refused, a batch the collector did not take. Optional.
	OnDrop        func()
	OnExportError func()
}

// Defaults for the knobs FromEnv does not read.
const (
	defaultQueueSize     = 2048
	defaultBatchSize     = 512
	defaultFlushInterval = 5 * time.Second
	defaultExportTimeout = 10 * time.Second
	// tracesPath is OTLP/HTTP's traces path, appended to an endpoint that
	// names only the collector.
	tracesPath = "/v1/traces"
)

// withDefaults fills every zero knob.
func (c Config) withDefaults() Config {
	if c.ServiceName == "" {
		c.ServiceName = "mddb"
	}
	if c.QueueSize <= 0 {
		c.QueueSize = defaultQueueSize
	}
	if c.BatchSize <= 0 {
		c.BatchSize = defaultBatchSize
	}
	if c.FlushInterval <= 0 {
		c.FlushInterval = defaultFlushInterval
	}
	if c.ExportTimeout <= 0 {
		c.ExportTimeout = defaultExportTimeout
	}
	return c
}

// FromEnv reads the MDDB_OTEL_* variables:
//
//	MDDB_OTEL_ENDPOINT      collector URL; "/v1/traces" is appended unless the
//	                        path already ends in it. Unset = tracing off.
//	MDDB_OTEL_HEADERS       comma-separated key=value pairs, values
//	                        percent-decoded (the OTEL_EXPORTER_OTLP_HEADERS form)
//	MDDB_OTEL_SAMPLE_RATIO  share of new traces recorded, 0-1 (default 1)
//	MDDB_OTEL_SERVICE_NAME  service.name (default mddb)
//	MDDB_OTEL_QUEUE_SIZE    spans waiting for export before new ones drop (2048)
func FromEnv(version string) Config {
	return Config{
		Endpoint:       tracesURL(os.Getenv("MDDB_OTEL_ENDPOINT")),
		Headers:        parseHeaders(os.Getenv("MDDB_OTEL_HEADERS")),
		SampleRatio:    sampleRatio(os.Getenv("MDDB_OTEL_SAMPLE_RATIO")),
		ServiceName:    envconf.String("MDDB_OTEL_SERVICE_NAME", "mddb"),
		ServiceVersion: version,
		QueueSize:      envconf.Int("MDDB_OTEL_QUEUE_SIZE", defaultQueueSize),
	}
}

// tracesURL completes an endpoint to the traces URL.
func tracesURL(endpoint string) string {
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" || strings.HasSuffix(endpoint, tracesPath) {
		return endpoint
	}
	return strings.TrimRight(endpoint, "/") + tracesPath
}

// parseHeaders reads "k1=v1,k2=v2". A pair without "=" or with an empty key is
// skipped, as is a value that does not percent-decode.
func parseHeaders(raw string) map[string]string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	out := map[string]string{}
	for _, pair := range strings.Split(raw, ",") {
		k, v, ok := strings.Cut(pair, "=")
		k = strings.TrimSpace(k)
		if !ok || k == "" {
			continue
		}
		dv, err := url.PathUnescape(strings.TrimSpace(v))
		if err != nil {
			continue
		}
		out[k] = dv
	}
	return out
}

// sampleRatio parses MDDB_OTEL_SAMPLE_RATIO; unset or unparseable is 1,
// everything else clamps to 0-1.
func sampleRatio(raw string) float64 {
	r, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
	if err != nil || math.IsNaN(r) {
		return 1
	}
	return min(max(r, 0), 1)
}
