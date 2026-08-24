package main

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"mddb/internal/logging"
)

// captureLogs installs a JSON logger writing into a buffer for the duration of
// a test, and returns the decoded records.
func captureLogs(t *testing.T, fn func()) []map[string]any {
	t.Helper()
	prev := slog.Default()
	t.Cleanup(func() { slog.SetDefault(prev) })

	var buf bytes.Buffer
	slog.SetDefault(slog.New(logging.NewHandler(&buf, logging.FormatJSON, slog.LevelDebug)))
	fn()

	var out []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		if line == "" {
			continue
		}
		var rec map[string]any
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("log line is not JSON: %v (%q)", err, line)
		}
		out = append(out, rec)
	}
	return out
}

func TestAccessLogRecordsTheRequest(t *testing.T) {
	handler := withAccessLog(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))

	recs := captureLogs(t, func() {
		req := httptest.NewRequest(http.MethodPost, "/v1/add?token=secret", nil)
		req.RemoteAddr = "10.0.0.7:5555"
		handler.ServeHTTP(httptest.NewRecorder(), req)
	})

	if len(recs) != 1 {
		t.Fatalf("expected one access log line, got %d", len(recs))
	}
	rec := recs[0]
	if rec["msg"] != "http request" || rec["method"] != "POST" {
		t.Errorf("unexpected record: %v", rec)
	}
	if rec["status"] != float64(http.StatusCreated) {
		t.Errorf("status = %v, want 201", rec["status"])
	}
	if rec["bytes"] != float64(len(`{"ok":true}`)) {
		t.Errorf("bytes = %v", rec["bytes"])
	}
	if rec["remote"] != "10.0.0.7:5555" {
		t.Errorf("remote = %v", rec["remote"])
	}
	if _, ok := rec["elapsed"]; !ok {
		t.Error("elapsed is missing")
	}
	// Search endpoints carry user content in the query string; an access log
	// is the wrong place to persist it.
	if got, _ := rec["path"].(string); got != "/v1/add" {
		t.Errorf("path = %q, want the path without its query string", got)
	}
	if strings.Contains(buffered(rec), "secret") {
		t.Error("the query string must not reach the log")
	}
}

func buffered(rec map[string]any) string {
	b, _ := json.Marshal(rec)
	return string(b)
}

func TestAccessLogDefaultsToTwoHundredWhenHandlerNeverWrites(t *testing.T) {
	handler := withAccessLog(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	recs := captureLogs(t, func() {
		handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/health", nil))
	})
	if len(recs) != 1 || recs[0]["status"] != float64(http.StatusOK) {
		t.Fatalf("a handler that writes nothing still returns 200: %v", recs)
	}
}

func TestAccessLogLevelFollowsStatus(t *testing.T) {
	for _, tc := range []struct {
		status int
		want   slog.Level
	}{
		{200, slog.LevelInfo},
		{304, slog.LevelInfo},
		{404, slog.LevelWarn},
		{429, slog.LevelWarn},
		{500, slog.LevelError},
		{503, slog.LevelError},
	} {
		if got := accessLogLevel(tc.status); got != tc.want {
			t.Errorf("accessLogLevel(%d) = %v, want %v", tc.status, got, tc.want)
		}
	}
}

func TestAccessLogEmitsTheLevelItPromises(t *testing.T) {
	handler := withAccessLog(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	recs := captureLogs(t, func() {
		handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/v1/search", nil))
	})
	if len(recs) != 1 || recs[0]["level"] != "ERROR" {
		t.Fatalf("a 500 must be logged at error level: %v", recs)
	}
}

// Streaming handlers reach the flusher through http.ResponseController, which
// walks Unwrap; without it SSE and the MCP transports would buffer.
func TestAccessLogRecorderKeepsFlushReachable(t *testing.T) {
	var flushed bool
	handler := withAccessLog(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if err := http.NewResponseController(w).Flush(); err != nil {
			t.Errorf("flush through the recorder failed: %v", err)
			return
		}
		flushed = true
	}))
	captureLogs(t, func() {
		handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/v1/sse", nil))
	})
	if !flushed {
		t.Error("handler could not flush through the access log wrapper")
	}
}
