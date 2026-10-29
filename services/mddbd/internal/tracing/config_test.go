package tracing

import (
	"testing"
	"time"
)

func TestFromEnv(t *testing.T) {
	t.Setenv("MDDB_OTEL_ENDPOINT", "http://collector:4318/")
	t.Setenv("MDDB_OTEL_HEADERS", "x-api-key=a%20b, authorization=Bearer t,=nokey,novalue,bad=%zz")
	t.Setenv("MDDB_OTEL_SAMPLE_RATIO", "0.25")
	t.Setenv("MDDB_OTEL_SERVICE_NAME", "search")
	t.Setenv("MDDB_OTEL_QUEUE_SIZE", "64")
	c := FromEnv("2.18.9")
	if c.Endpoint != "http://collector:4318/v1/traces" || c.SampleRatio != 0.25 || c.ServiceName != "search" ||
		c.ServiceVersion != "2.18.9" || c.QueueSize != 64 {
		t.Fatalf("config: %+v", c)
	}
	if len(c.Headers) != 2 || c.Headers["x-api-key"] != "a b" || c.Headers["authorization"] != "Bearer t" {
		t.Fatalf("headers: %v", c.Headers)
	}
}

func TestConfigParsing(t *testing.T) {
	for in, want := range map[string]string{
		"":                               "",
		"  ":                             "",
		"http://c:4318":                  "http://c:4318/v1/traces",
		"https://otlp.example/v1/traces": "https://otlp.example/v1/traces",
	} {
		if got := tracesURL(in); got != want {
			t.Errorf("tracesURL(%q) = %q, want %q", in, got, want)
		}
	}
	if parseHeaders("") != nil {
		t.Error("empty headers")
	}
	for in, want := range map[string]float64{"": 1, "x": 1, "NaN": 1, "-2": 0, "7": 1, "0.5": 0.5} {
		if got := sampleRatio(in); got != want {
			t.Errorf("sampleRatio(%q) = %v, want %v", in, got, want)
		}
	}
	d := Config{}.withDefaults()
	if d.ServiceName != "mddb" || d.QueueSize != defaultQueueSize || d.BatchSize != defaultBatchSize ||
		d.FlushInterval != defaultFlushInterval || d.ExportTimeout != defaultExportTimeout {
		t.Fatalf("defaults: %+v", d)
	}
	kept := Config{ServiceName: "s", QueueSize: 1, BatchSize: 1, FlushInterval: time.Second, ExportTimeout: time.Second}.withDefaults()
	if kept.ServiceName != "s" || kept.QueueSize != 1 || kept.BatchSize != 1 {
		t.Fatalf("explicit knobs replaced: %+v", kept)
	}
}
