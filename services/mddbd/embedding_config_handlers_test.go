package main

import (
	"mddb/internal/metrics"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	json "mddb/internal/jsonx"
)

func TestHandleListEmbeddingConfigs_Empty(t *testing.T) {
	s, cleanup := newTestServerForEmbeddingConfig(t)
	defer cleanup()
	s.Metrics = metrics.NewMetrics(false, &serverMetricsStats{s: s})

	req := httptest.NewRequest(http.MethodGet, "/v1/embedding-configs", nil)
	w := httptest.NewRecorder()
	s.handleListEmbeddingConfigs(w, req)

	if w.Result().StatusCode != 200 {
		t.Fatalf("expected 200, got %d", w.Result().StatusCode)
	}

	var body map[string]interface{}
	_ = json.NewDecoder(w.Result().Body).Decode(&body)
	configs := body["configs"]
	if configs != nil {
		if arr, ok := configs.([]interface{}); ok && len(arr) > 0 {
			t.Errorf("expected empty configs, got %d", len(arr))
		}
	}
}

func TestHandleCreateEmbeddingConfig_Success(t *testing.T) {
	s, cleanup := newTestServerForEmbeddingConfig(t)
	defer cleanup()
	s.Metrics = metrics.NewMetrics(false, &serverMetricsStats{s: s})

	payload := `{"id":"cfg-1","name":"Test","provider":"openai","model":"ada","dimensions":1536,"apiKey":"sk-test"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/embedding-configs", strings.NewReader(payload))
	w := httptest.NewRecorder()
	s.handleCreateEmbeddingConfig(w, req)

	if w.Result().StatusCode != 201 {
		t.Fatalf("expected 201, got %d", w.Result().StatusCode)
	}
}

func TestHandleCreateEmbeddingConfig_MissingFields(t *testing.T) {
	s, cleanup := newTestServerForEmbeddingConfig(t)
	defer cleanup()
	s.Metrics = metrics.NewMetrics(false, &serverMetricsStats{s: s})

	req := httptest.NewRequest(http.MethodPost, "/v1/embedding-configs",
		strings.NewReader(`{"id":"cfg-1"}`))
	w := httptest.NewRecorder()
	s.handleCreateEmbeddingConfig(w, req)

	if w.Result().StatusCode != 400 {
		t.Fatalf("expected 400, got %d", w.Result().StatusCode)
	}
}

func TestHandleCreateEmbeddingConfig_InvalidProvider(t *testing.T) {
	s, cleanup := newTestServerForEmbeddingConfig(t)
	defer cleanup()
	s.Metrics = metrics.NewMetrics(false, &serverMetricsStats{s: s})

	payload := `{"id":"cfg-1","name":"Test","provider":"invalid","model":"ada","dimensions":1536}`
	req := httptest.NewRequest(http.MethodPost, "/v1/embedding-configs", strings.NewReader(payload))
	w := httptest.NewRecorder()
	s.handleCreateEmbeddingConfig(w, req)

	if w.Result().StatusCode != 400 {
		t.Fatalf("expected 400, got %d", w.Result().StatusCode)
	}
}

func TestHandleCreateEmbeddingConfig_ReadOnlyMode(t *testing.T) {
	s, cleanup := newTestServerForEmbeddingConfig(t)
	defer cleanup()
	s.Mode = ModeRead
	s.Metrics = metrics.NewMetrics(false, &serverMetricsStats{s: s})

	payload := `{"id":"cfg-1","name":"Test","provider":"openai","model":"ada","dimensions":1536}`
	req := httptest.NewRequest(http.MethodPost, "/v1/embedding-configs", strings.NewReader(payload))
	w := httptest.NewRecorder()
	s.handleCreateEmbeddingConfig(w, req)

	if w.Result().StatusCode != 403 {
		t.Fatalf("expected 403, got %d", w.Result().StatusCode)
	}
}

func TestHandleGetEmbeddingConfig_Success(t *testing.T) {
	s, cleanup := newTestServerForEmbeddingConfig(t)
	defer cleanup()
	s.Metrics = metrics.NewMetrics(false, &serverMetricsStats{s: s})

	_ = s.SaveEmbeddingConfig(&EmbeddingConfig{
		ID: "cfg-1", Name: "Test", Provider: "openai", Model: "ada", Dimensions: 1536,
	})

	req := httptest.NewRequest(http.MethodGet, "/v1/embedding-configs/cfg-1", nil)
	w := httptest.NewRecorder()
	s.handleGetEmbeddingConfig(w, req, "cfg-1")

	if w.Result().StatusCode != 200 {
		t.Fatalf("expected 200, got %d", w.Result().StatusCode)
	}
}

func TestHandleGetEmbeddingConfig_NotFound(t *testing.T) {
	s, cleanup := newTestServerForEmbeddingConfig(t)
	defer cleanup()
	s.Metrics = metrics.NewMetrics(false, &serverMetricsStats{s: s})

	req := httptest.NewRequest(http.MethodGet, "/v1/embedding-configs/nonexistent", nil)
	w := httptest.NewRecorder()
	s.handleGetEmbeddingConfig(w, req, "nonexistent")

	if w.Result().StatusCode != 404 {
		t.Fatalf("expected 404, got %d", w.Result().StatusCode)
	}
}

func TestHandleUpdateEmbeddingConfig_ReadOnlyMode(t *testing.T) {
	s, cleanup := newTestServerForEmbeddingConfig(t)
	defer cleanup()
	s.Mode = ModeRead
	s.Metrics = metrics.NewMetrics(false, &serverMetricsStats{s: s})

	payload := `{"name":"Updated","provider":"openai","model":"ada","dimensions":1536}`
	req := httptest.NewRequest(http.MethodPut, "/v1/embedding-configs/cfg-1", strings.NewReader(payload))
	w := httptest.NewRecorder()
	s.handleUpdateEmbeddingConfig(w, req, "cfg-1")

	if w.Result().StatusCode != 403 {
		t.Fatalf("expected 403, got %d", w.Result().StatusCode)
	}
}

func TestHandleDeleteEmbeddingConfig_ReadOnlyMode(t *testing.T) {
	s, cleanup := newTestServerForEmbeddingConfig(t)
	defer cleanup()
	s.Mode = ModeRead
	s.Metrics = metrics.NewMetrics(false, &serverMetricsStats{s: s})

	req := httptest.NewRequest(http.MethodDelete, "/v1/embedding-configs/cfg-1", nil)
	w := httptest.NewRecorder()
	s.handleDeleteEmbeddingConfig(w, req, "cfg-1")

	if w.Result().StatusCode != 403 {
		t.Fatalf("expected 403, got %d", w.Result().StatusCode)
	}
}

func TestHandleDeleteEmbeddingConfig_CannotDeleteDefault(t *testing.T) {
	s, cleanup := newTestServerForEmbeddingConfig(t)
	defer cleanup()
	s.Metrics = metrics.NewMetrics(false, &serverMetricsStats{s: s})

	_ = s.SaveEmbeddingConfig(&EmbeddingConfig{
		ID: "cfg-1", Name: "Default", Provider: "openai", Model: "ada", Dimensions: 1536, IsDefault: true,
	})

	req := httptest.NewRequest(http.MethodDelete, "/v1/embedding-configs/cfg-1", nil)
	w := httptest.NewRecorder()
	s.handleDeleteEmbeddingConfig(w, req, "cfg-1")

	if w.Result().StatusCode != 400 {
		t.Fatalf("expected 400, got %d", w.Result().StatusCode)
	}
}

func TestHandleSetDefaultEmbeddingConfig_ReadOnlyMode(t *testing.T) {
	s, cleanup := newTestServerForEmbeddingConfig(t)
	defer cleanup()
	s.Mode = ModeRead
	s.Metrics = metrics.NewMetrics(false, &serverMetricsStats{s: s})

	req := httptest.NewRequest(http.MethodPost, "/v1/embedding-configs/set-default",
		strings.NewReader(`{"id":"cfg-1"}`))
	w := httptest.NewRecorder()
	s.handleSetDefaultEmbeddingConfig(w, req)

	if w.Result().StatusCode != 403 {
		t.Fatalf("expected 403, got %d", w.Result().StatusCode)
	}
}
