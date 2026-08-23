package main

import (
	"mddb/internal/metrics"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	bolt "go.etcd.io/bbolt"
	json "mddb/internal/jsonx"
)

func newTestServerForCollectionConfig(t *testing.T) (*Server, func()) {
	t.Helper()

	dbPath := filepath.Join(t.TempDir(), "test.db")
	db, err := bolt.Open(dbPath, 0600, nil)
	if err != nil {
		t.Fatal(err)
	}

	s := &Server{
		DB:   db,
		Path: dbPath,
		Mode: ModeRW,
		BucketNames: BucketNames{
			Docs:    []byte("docs"),
			IdxMeta: []byte("idxmeta"),
			Rev:     []byte("rev"),
			ByKey:   []byte("bykey"),
		},
		Metrics: metrics.NewMetrics(false, nil),
	}

	if err := s.ensureBuckets(); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}

	s.CollectionManager = NewCollectionManager(db)
	if err := s.CollectionManager.EnsureBucket(); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}

	return s, func() {
		_ = db.Close()
		_ = os.RemoveAll(filepath.Dir(dbPath))
	}
}

func TestHandleCollectionConfigGet_MissingCollection(t *testing.T) {
	s, cleanup := newTestServerForCollectionConfig(t)
	defer cleanup()

	req := httptest.NewRequest(http.MethodGet, "/v1/collection-config", nil)
	w := httptest.NewRecorder()
	s.handleCollectionConfigGet(w, req)

	if w.Result().StatusCode != 400 {
		t.Fatalf("expected 400, got %d", w.Result().StatusCode)
	}
}

func TestHandleCollectionConfigGet_Default(t *testing.T) {
	s, cleanup := newTestServerForCollectionConfig(t)
	defer cleanup()

	req := httptest.NewRequest(http.MethodGet, "/v1/collection-config?collection=blog", nil)
	w := httptest.NewRecorder()
	s.handleCollectionConfigGet(w, req)

	if w.Result().StatusCode != 200 {
		t.Fatalf("expected 200, got %d", w.Result().StatusCode)
	}

	var body map[string]interface{}
	_ = json.NewDecoder(w.Result().Body).Decode(&body)
	if body["configured"] != false {
		t.Error("expected configured=false for unconfigured collection")
	}
}

func TestHandleCollectionConfigSet_Success(t *testing.T) {
	s, cleanup := newTestServerForCollectionConfig(t)
	defer cleanup()

	payload := `{"collection":"blog","type":"website","description":"My blog","icon":"📝"}`
	req := httptest.NewRequest(http.MethodPut, "/v1/collection-config", strings.NewReader(payload))
	w := httptest.NewRecorder()
	s.handleCollectionConfigSet(w, req)

	if w.Result().StatusCode != 200 {
		t.Fatalf("expected 200, got %d", w.Result().StatusCode)
	}

	// Verify it was saved
	cfg, ok := s.CollectionManager.Get("blog")
	if !ok {
		t.Fatal("expected config to be saved")
	}
	if cfg.Type != "website" {
		t.Errorf("expected type 'website', got %q", cfg.Type)
	}
	if cfg.Description != "My blog" {
		t.Errorf("expected description 'My blog', got %q", cfg.Description)
	}
}

func TestHandleCollectionConfigSet_MissingCollection(t *testing.T) {
	s, cleanup := newTestServerForCollectionConfig(t)
	defer cleanup()

	req := httptest.NewRequest(http.MethodPut, "/v1/collection-config", strings.NewReader(`{"type":"website"}`))
	w := httptest.NewRecorder()
	s.handleCollectionConfigSet(w, req)

	if w.Result().StatusCode != 400 {
		t.Fatalf("expected 400, got %d", w.Result().StatusCode)
	}
}

func TestHandleCollectionConfigSet_ReadOnlyMode(t *testing.T) {
	s, cleanup := newTestServerForCollectionConfig(t)
	defer cleanup()
	s.Mode = ModeRead

	payload := `{"collection":"blog","type":"website"}`
	req := httptest.NewRequest(http.MethodPut, "/v1/collection-config", strings.NewReader(payload))
	w := httptest.NewRecorder()
	s.handleCollectionConfigSet(w, req)

	if w.Result().StatusCode != 403 {
		t.Fatalf("expected 403, got %d", w.Result().StatusCode)
	}
}

func TestHandleCollectionConfigDelete_Success(t *testing.T) {
	s, cleanup := newTestServerForCollectionConfig(t)
	defer cleanup()

	// Set first
	_ = s.CollectionManager.Set("blog", &CollectionConfig{Type: "website"})

	req := httptest.NewRequest(http.MethodDelete, "/v1/collection-config?collection=blog", nil)
	w := httptest.NewRecorder()
	s.handleCollectionConfigDelete(w, req)

	if w.Result().StatusCode != 200 {
		t.Fatalf("expected 200, got %d", w.Result().StatusCode)
	}

	_, ok := s.CollectionManager.Get("blog")
	if ok {
		t.Error("expected config to be deleted")
	}
}

func TestHandleCollectionConfigDelete_ReadOnlyMode(t *testing.T) {
	s, cleanup := newTestServerForCollectionConfig(t)
	defer cleanup()
	s.Mode = ModeRead

	req := httptest.NewRequest(http.MethodDelete, "/v1/collection-config?collection=blog", nil)
	w := httptest.NewRecorder()
	s.handleCollectionConfigDelete(w, req)

	if w.Result().StatusCode != 403 {
		t.Fatalf("expected 403, got %d", w.Result().StatusCode)
	}
}

func TestHandleCollectionConfigDelete_MissingCollection(t *testing.T) {
	s, cleanup := newTestServerForCollectionConfig(t)
	defer cleanup()

	req := httptest.NewRequest(http.MethodDelete, "/v1/collection-config", nil)
	w := httptest.NewRecorder()
	s.handleCollectionConfigDelete(w, req)

	if w.Result().StatusCode != 400 {
		t.Fatalf("expected 400, got %d", w.Result().StatusCode)
	}
}

func TestHandleCollectionConfigList_Empty(t *testing.T) {
	s, cleanup := newTestServerForCollectionConfig(t)
	defer cleanup()

	req := httptest.NewRequest(http.MethodGet, "/v1/collection-configs", nil)
	w := httptest.NewRecorder()
	s.handleCollectionConfigList(w, req)

	if w.Result().StatusCode != 200 {
		t.Fatalf("expected 200, got %d", w.Result().StatusCode)
	}

	var body map[string]interface{}
	_ = json.NewDecoder(w.Result().Body).Decode(&body)
	total := int(body["total"].(float64))
	if total != 0 {
		t.Errorf("expected 0 configs, got %d", total)
	}
}

func TestHandleCollectionConfigList_WithConfigs(t *testing.T) {
	s, cleanup := newTestServerForCollectionConfig(t)
	defer cleanup()

	_ = s.CollectionManager.Set("blog", &CollectionConfig{Type: "website"})
	_ = s.CollectionManager.Set("images", &CollectionConfig{Type: "images"})

	req := httptest.NewRequest(http.MethodGet, "/v1/collection-configs", nil)
	w := httptest.NewRecorder()
	s.handleCollectionConfigList(w, req)

	if w.Result().StatusCode != 200 {
		t.Fatalf("expected 200, got %d", w.Result().StatusCode)
	}

	var body map[string]interface{}
	_ = json.NewDecoder(w.Result().Body).Decode(&body)
	total := int(body["total"].(float64))
	if total != 2 {
		t.Errorf("expected 2 configs, got %d", total)
	}
}

func TestHandleCollectionConfig_MethodNotAllowed(t *testing.T) {
	s, cleanup := newTestServerForCollectionConfig(t)
	defer cleanup()

	req := httptest.NewRequest(http.MethodPatch, "/v1/collection-config", nil)
	w := httptest.NewRecorder()
	s.handleCollectionConfig(w, req)

	if w.Result().StatusCode != 405 {
		t.Fatalf("expected 405, got %d", w.Result().StatusCode)
	}
}

func TestHandleCollectionConfig_Dispatch(t *testing.T) {
	s, cleanup := newTestServerForCollectionConfig(t)
	defer cleanup()

	// Test GET dispatch
	req := httptest.NewRequest(http.MethodGet, "/v1/collection-config?collection=blog", nil)
	w := httptest.NewRecorder()
	s.handleCollectionConfig(w, req)
	if w.Result().StatusCode != 200 {
		t.Fatalf("GET dispatch expected 200, got %d", w.Result().StatusCode)
	}

	// Test PUT dispatch
	req = httptest.NewRequest(http.MethodPut, "/v1/collection-config",
		strings.NewReader(`{"collection":"blog","type":"website"}`))
	w = httptest.NewRecorder()
	s.handleCollectionConfig(w, req)
	if w.Result().StatusCode != 200 {
		t.Fatalf("PUT dispatch expected 200, got %d", w.Result().StatusCode)
	}

	// Test DELETE dispatch
	req = httptest.NewRequest(http.MethodDelete, "/v1/collection-config?collection=blog", nil)
	w = httptest.NewRecorder()
	s.handleCollectionConfig(w, req)
	if w.Result().StatusCode != 200 {
		t.Fatalf("DELETE dispatch expected 200, got %d", w.Result().StatusCode)
	}
}
