package logging

import (
	"bytes"
	"encoding/json"
	"log"
	"log/slog"
	"strings"
	"testing"
	"time"
)

func TestParseFormat(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want Format
	}{
		{"json", FormatJSON},
		{"JSON", FormatJSON},
		{"  json  ", FormatJSON},
		{"text", FormatText},
		{"", FormatText},
		{"yaml", FormatText}, // unrecognised must not disable readable output
	} {
		if got := ParseFormat(tc.in); got != tc.want {
			t.Errorf("ParseFormat(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestParseLevel(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want slog.Level
	}{
		{"debug", slog.LevelDebug},
		{"DEBUG", slog.LevelDebug},
		{"info", slog.LevelInfo},
		{"warn", slog.LevelWarn},
		{"warning", slog.LevelWarn},
		{"error", slog.LevelError},
		{"", slog.LevelInfo},
		{"loud", slog.LevelInfo},
	} {
		if got := ParseLevel(tc.in); got != tc.want {
			t.Errorf("ParseLevel(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestJSONHandlerEmitsStructuredFields(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(NewHandler(&buf, FormatJSON, slog.LevelInfo))
	logger.Error("embedding failed", "collection", "docs", "key", "intro", "attempts", 3)

	var rec map[string]any
	if err := json.Unmarshal(buf.Bytes(), &rec); err != nil {
		t.Fatalf("output is not one JSON object per line: %v (%q)", err, buf.String())
	}
	if rec["level"] != "ERROR" {
		t.Errorf("level = %v, want ERROR", rec["level"])
	}
	if rec["msg"] != "embedding failed" {
		t.Errorf("msg = %v", rec["msg"])
	}
	if rec["collection"] != "docs" || rec["key"] != "intro" {
		t.Errorf("structured fields missing: %v", rec)
	}
	if rec["attempts"] != float64(3) {
		t.Errorf("attempts = %v, want 3", rec["attempts"])
	}
	// The timestamp must be machine-readable, which the log package's
	// "2026/08/21 08:42:13" local format never was.
	ts, ok := rec["time"].(string)
	if !ok {
		t.Fatalf("no time field: %v", rec)
	}
	if _, err := time.Parse(time.RFC3339Nano, ts); err != nil {
		t.Errorf("time %q is not RFC 3339: %v", ts, err)
	}
}

func TestTextHandlerIsTheDefaultShape(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(NewHandler(&buf, FormatText, slog.LevelInfo))
	logger.Warn("queue full", "collection", "docs")

	out := buf.String()
	for _, want := range []string{"level=WARN", `msg="queue full"`, "collection=docs"} {
		if !strings.Contains(out, want) {
			t.Errorf("text output %q is missing %q", out, want)
		}
	}
}

func TestLevelFiltering(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(NewHandler(&buf, FormatJSON, slog.LevelWarn))
	logger.Info("routine")
	logger.Debug("noisy")
	if buf.Len() != 0 {
		t.Errorf("info/debug should be filtered at warn level, got %q", buf.String())
	}
	logger.Warn("interesting")
	if !strings.Contains(buf.String(), "interesting") {
		t.Errorf("warn should pass at warn level, got %q", buf.String())
	}
}

func TestSetupInstallsDefaultAndCapturesLogPackage(t *testing.T) {
	prev := slog.Default()
	prevFlags := log.Flags()
	t.Cleanup(func() {
		slog.SetDefault(prev)
		log.SetFlags(prevFlags)
	})

	var buf bytes.Buffer
	logger := setupWith(&buf, "json", "info")
	if slog.Default() != logger {
		t.Error("Setup should install the logger as slog's default")
	}

	// Anything still using the log package — a dependency, a straggler — has
	// to end up in the same stream and encoding, not as loose text.
	log.Printf("from a third-party package")
	var rec map[string]any
	if err := json.Unmarshal(buf.Bytes(), &rec); err != nil {
		t.Fatalf("log package output did not become JSON: %v (%q)", err, buf.String())
	}
	if !strings.Contains(rec["msg"].(string), "from a third-party package") {
		t.Errorf("unexpected msg: %v", rec["msg"])
	}
}

func TestSetupHonoursLevelFromEnvValue(t *testing.T) {
	prev := slog.Default()
	t.Cleanup(func() { slog.SetDefault(prev) })

	var buf bytes.Buffer
	logger := setupWith(&buf, "json", "error")
	logger.Warn("should not appear")
	if buf.Len() != 0 {
		t.Errorf("warn should be filtered at error level, got %q", buf.String())
	}
	logger.Error("should appear")
	if !strings.Contains(buf.String(), "should appear") {
		t.Error("error should pass at error level")
	}
}

func TestSetupReadsTheEnvironment(t *testing.T) {
	prev := slog.Default()
	t.Cleanup(func() { slog.SetDefault(prev) })

	t.Setenv("MDDB_LOG_FORMAT", "json")
	t.Setenv("MDDB_LOG_LEVEL", "debug")
	if got := Setup(); got == nil {
		t.Fatal("Setup returned nil")
	}
	if !slog.Default().Enabled(t.Context(), slog.LevelDebug) {
		t.Error("MDDB_LOG_LEVEL=debug should enable debug output")
	}
}

func TestFatalLogsAtErrorAndExitsNonZero(t *testing.T) {
	prevLogger := slog.Default()
	prevExit := exit
	t.Cleanup(func() {
		slog.SetDefault(prevLogger)
		exit = prevExit
	})

	var buf bytes.Buffer
	slog.SetDefault(slog.New(NewHandler(&buf, FormatJSON, slog.LevelInfo)))

	var code int
	called := false
	exit = func(c int) { code, called = c, true }

	Fatal("cannot open database", "path", "/data/mddb.db", "err", "permission denied")

	if !called {
		t.Fatal("Fatal must terminate the process")
	}
	if code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	var rec map[string]any
	if err := json.Unmarshal(buf.Bytes(), &rec); err != nil {
		t.Fatalf("Fatal did not emit one JSON record: %v (%q)", err, buf.String())
	}
	if rec["level"] != "ERROR" {
		t.Errorf("level = %v, want ERROR", rec["level"])
	}
	if rec["path"] != "/data/mddb.db" {
		t.Errorf("structured fields lost: %v", rec)
	}
}
