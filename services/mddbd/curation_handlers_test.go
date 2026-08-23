package main

import (
	"bytes"
	"mddb/internal/metrics"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	bolt "go.etcd.io/bbolt"
	json "mddb/internal/jsonx"
)

func newCurationTestServer(t *testing.T) (*Server, func()) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "cur.db")
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
	s.CurationManager = NewCurationManager(db)
	if err := s.CurationManager.EnsureBucket(); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	return s, func() {
		_ = db.Close()
		_ = os.RemoveAll(filepath.Dir(dbPath))
	}
}

func TestHandleCurationCreateAndGet(t *testing.T) {
	s, cleanup := newCurationTestServer(t)
	defer cleanup()

	body := CurationRule{
		Collection: "blog",
		Query:      "rust",
		Enabled:    true,
		Pins:       []PinnedDoc{{Key: "rust-best", Position: 1}},
		Hides:      []string{"legacy-post"},
	}
	buf, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/v1/curation", bytes.NewReader(buf))
	w := httptest.NewRecorder()
	s.handleCuration(w, req)
	if w.Result().StatusCode != 200 {
		t.Fatalf("create status=%d body=%s", w.Result().StatusCode, w.Body.String())
	}
	var created CurationRule
	_ = json.NewDecoder(w.Result().Body).Decode(&created)
	if created.ID == "" {
		t.Fatal("expected id in response")
	}

	// GET by id
	req2 := httptest.NewRequest(http.MethodGet, "/v1/curation?id="+created.ID, nil)
	w2 := httptest.NewRecorder()
	s.handleCuration(w2, req2)
	if w2.Result().StatusCode != 200 {
		t.Fatalf("get status=%d", w2.Result().StatusCode)
	}

	// LIST by collection
	req3 := httptest.NewRequest(http.MethodGet, "/v1/curation?collection=blog", nil)
	w3 := httptest.NewRecorder()
	s.handleCuration(w3, req3)
	var list struct {
		Rules []*CurationRule `json:"rules"`
		Total int             `json:"total"`
	}
	_ = json.NewDecoder(w3.Result().Body).Decode(&list)
	if list.Total != 1 {
		t.Errorf("expected 1 rule in list, got %d", list.Total)
	}
}

func TestHandleCurationCreateRejectsInvalid(t *testing.T) {
	s, cleanup := newCurationTestServer(t)
	defer cleanup()

	req := httptest.NewRequest(http.MethodPost, "/v1/curation", bytes.NewReader([]byte(`{"query":"q"}`)))
	w := httptest.NewRecorder()
	s.handleCuration(w, req)
	if w.Result().StatusCode == 200 {
		t.Error("missing collection must be rejected")
	}
}

func TestHandleCurationReadOnlyMode(t *testing.T) {
	s, cleanup := newCurationTestServer(t)
	defer cleanup()
	s.Mode = ModeRead

	body := CurationRule{Collection: "blog", Query: "q"}
	buf, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/v1/curation", bytes.NewReader(buf))
	w := httptest.NewRecorder()
	s.handleCuration(w, req)
	if w.Result().StatusCode != http.StatusForbidden {
		t.Errorf("expected 403, got %d", w.Result().StatusCode)
	}
}

func TestHandleCurationUpdate(t *testing.T) {
	s, cleanup := newCurationTestServer(t)
	defer cleanup()

	rule := &CurationRule{Collection: "c", Query: "q", Enabled: true}
	_ = s.CurationManager.Set(rule)

	rule.Query = "q2"
	buf, _ := json.Marshal(rule)
	req := httptest.NewRequest(http.MethodPut, "/v1/curation", bytes.NewReader(buf))
	w := httptest.NewRecorder()
	s.handleCuration(w, req)
	if w.Result().StatusCode != 200 {
		t.Fatalf("update status=%d body=%s", w.Result().StatusCode, w.Body.String())
	}
	got, _ := s.CurationManager.Get(rule.ID)
	if got.Query != "q2" {
		t.Errorf("update did not persist: got %q", got.Query)
	}
}

func TestHandleCurationDelete(t *testing.T) {
	s, cleanup := newCurationTestServer(t)
	defer cleanup()

	rule := &CurationRule{Collection: "c", Query: "q", Enabled: true}
	_ = s.CurationManager.Set(rule)

	req := httptest.NewRequest(http.MethodDelete, "/v1/curation?id="+rule.ID, nil)
	w := httptest.NewRecorder()
	s.handleCuration(w, req)
	if w.Result().StatusCode != 200 {
		t.Fatalf("delete status=%d", w.Result().StatusCode)
	}
	if _, ok := s.CurationManager.Get(rule.ID); ok {
		t.Error("rule should be deleted")
	}
}

func TestHandleCurationDeleteNonExistent(t *testing.T) {
	s, cleanup := newCurationTestServer(t)
	defer cleanup()
	req := httptest.NewRequest(http.MethodDelete, "/v1/curation?id=nope", nil)
	w := httptest.NewRecorder()
	s.handleCuration(w, req)
	if w.Result().StatusCode != 404 {
		t.Errorf("expected 404, got %d", w.Result().StatusCode)
	}
}

func TestHandleCurationMethodNotAllowed(t *testing.T) {
	s, cleanup := newCurationTestServer(t)
	defer cleanup()
	req := httptest.NewRequest(http.MethodPatch, "/v1/curation", nil)
	w := httptest.NewRecorder()
	s.handleCuration(w, req)
	if w.Result().StatusCode != 405 {
		t.Errorf("expected 405, got %d", w.Result().StatusCode)
	}
}
