package main

import (
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"

	json "mddb/internal/jsonx"
)

// ---------- 1. handleSystemInfo - GET success ----------

func TestHandleSystemInfo_Success(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/system/info", nil)
	s.handleSystemInfo(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	ct := rec.Header().Get("Content-Type")
	if ct != "application/json" {
		t.Errorf("expected Content-Type application/json, got %q", ct)
	}

	var resp SystemInfoResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp.OS != runtime.GOOS {
		t.Errorf("expected OS=%q, got %q", runtime.GOOS, resp.OS)
	}
	if resp.Arch != runtime.GOARCH {
		t.Errorf("expected Arch=%q, got %q", runtime.GOARCH, resp.Arch)
	}
	if resp.NumCPU != runtime.NumCPU() {
		t.Errorf("expected NumCPU=%d, got %d", runtime.NumCPU(), resp.NumCPU)
	}
	if resp.GoVersion != runtime.Version() {
		t.Errorf("expected GoVersion=%q, got %q", runtime.Version(), resp.GoVersion)
	}
	if resp.Version != VERSION {
		t.Errorf("expected Version=%q, got %q", VERSION, resp.Version)
	}
	if resp.Hostname == "" {
		t.Error("expected non-empty hostname")
	}
}

// ---------- 2. handleSystemInfo - wrong method ----------

func TestHandleSystemInfo_MethodNotAllowed(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	methods := []string{http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch}
	for _, method := range methods {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(method, "/v1/system/info", nil)
		s.handleSystemInfo(rec, req)

		if rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("method %s: expected 405, got %d", method, rec.Code)
		}
	}
}

// ---------- 3. handleSystemInfo - memory stats are populated ----------

func TestHandleSystemInfo_MemoryStats(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/system/info", nil)
	s.handleSystemInfo(rec, req)

	var resp SystemInfoResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}

	// Memory fields should be > 0 in a running process
	if resp.MemoryUsed == 0 {
		t.Error("expected MemoryUsed > 0")
	}
	if resp.MemorySystem == 0 {
		t.Error("expected MemorySystem > 0")
	}
	if resp.NumGoroutines == 0 {
		t.Error("expected NumGoroutines > 0")
	}
}

// ---------- 4. handleSystemInfo - uptime is non-negative ----------

func TestHandleSystemInfo_Uptime(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/system/info", nil)
	s.handleSystemInfo(rec, req)

	var resp SystemInfoResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if resp.UptimeSeconds < 0 {
		t.Errorf("expected UptimeSeconds >= 0, got %d", resp.UptimeSeconds)
	}
}

// ---------- 5. handleHealth - GET returns healthy ----------

func TestSystemHandleHealth_GETHealthy(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/health", nil)
	s.handleHealth(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"status":"healthy"`) {
		t.Errorf("expected healthy status, got %s", body)
	}
}

// ---------- 6. handleHealth - POST also works ----------

func TestSystemHandleHealth_POSTWorks(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/health", nil)
	s.handleHealth(rec, req)

	// handleHealth does not check method, so POST should also work
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}

// ---------- 7. handleHealth - reports mode ----------

func TestSystemHandleHealth_ReportsMode(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/health", nil)
	s.handleHealth(rec, req)

	body := rec.Body.String()
	if !strings.Contains(body, `"mode":"wr"`) {
		t.Errorf("expected mode wr, got %s", body)
	}
}

// ---------- 8. handleStats - empty database returns zero counts ----------

func TestSystemHandleStats_EmptyDatabase(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/stats", nil)
	s.handleStats(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}

	totalDocs, _ := resp["totalDocuments"].(float64)
	if totalDocs != 0 {
		t.Errorf("expected 0 total documents, got %v", totalDocs)
	}

	collections, _ := resp["collections"].([]interface{})
	if len(collections) != 0 {
		t.Errorf("expected 0 collections, got %d", len(collections))
	}
}

// ---------- 9. handleStats - with documents ----------

func TestSystemHandleStats_WithDocuments(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	addTestDoc(t, s, "blog", "post1", "en", "# Post 1", map[string][]string{"author": {"alice"}})
	addTestDoc(t, s, "blog", "post2", "en", "# Post 2", map[string][]string{"author": {"bob"}})
	addTestDoc(t, s, "wiki", "page1", "en", "# Page 1", nil)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/stats", nil)
	s.handleStats(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}

	totalDocs, _ := resp["totalDocuments"].(float64)
	if totalDocs != 3 {
		t.Errorf("expected 3 total documents, got %v", totalDocs)
	}

	mode, _ := resp["mode"].(string)
	if mode != "wr" {
		t.Errorf("expected mode wr, got %q", mode)
	}
}

// ---------- 10. handleStats - reports database path ----------

func TestSystemHandleStats_ReportsPath(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/stats", nil)
	s.handleStats(rec, req)

	var resp map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}

	dbPath, _ := resp["databasePath"].(string)
	if dbPath == "" {
		t.Error("expected non-empty database path")
	}
	if dbPath != s.Path {
		t.Errorf("expected path %q, got %q", s.Path, dbPath)
	}
}
