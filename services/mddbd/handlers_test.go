package main

import (
	"bytes"
	"fmt"
	"io"
	"mddb/internal/cache"
	"mddb/internal/fts"
	"mddb/internal/metrics"
	"mddb/internal/schema"
	"mddb/internal/storage"
	"mddb/internal/ttl"
	"mddb/internal/vector"
	"mddb/internal/webhooks"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	json "mddb/internal/jsonx"
	bolt "go.etcd.io/bbolt"
)

// newHandlerTestServer creates a fully-initialised Server suitable for HTTP
// handler tests.  Every subsystem that the handlers touch (Cache, FTSIndex,
// TTLManager, WebhookManager, schema.SchemaManager, Metrics, VectorStore,
// VectorIndex) is wired up so that no nil-pointer panics occur.
func newHandlerTestServer(t *testing.T) (*Server, func()) {
	t.Helper()

	f, err := os.CreateTemp("", "handler_test_*.db")
	if err != nil {
		t.Fatal(err)
	}
	_ = f.Close()

	db, err := bolt.Open(f.Name(), 0600, nil)
	if err != nil {
		_ = os.Remove(f.Name())
		t.Fatal(err)
	}

	s := &Server{
		DB:    db,
		Path:  f.Name(),
		Mode:  ModeRW,
		Ready: true,
		BucketNames: BucketNames{
			Docs:    []byte("docs"),
			IdxMeta: []byte("idxmeta"),
			Rev:     []byte("rev"),
			ByKey:   []byte("bykey"),
		},
		Cache: cache.NewDocumentCache(100, 60),
	}

	if err := s.ensureBuckets(); err != nil {
		_ = db.Close()
		_ = os.Remove(f.Name())
		t.Fatal(err)
	}

	// Vector subsystem
	s.VectorStore = vector.NewVectorStore(db)
	if err := s.VectorStore.EnsureBucket(); err != nil {
		_ = db.Close()
		_ = os.Remove(f.Name())
		t.Fatal(err)
	}
	s.VectorIndex = vector.NewVectorIndex()

	// Collection configs (quantization, disk-only, attributes)
	s.CollectionManager = NewCollectionManager(db)
	if err := s.CollectionManager.EnsureBucket(); err != nil {
		_ = db.Close()
		_ = os.Remove(f.Name())
		t.Fatal(err)
	}

	s.QuantizedVecIndex = vector.NewQuantizedVectorIndex(func(collection string) vector.QuantizationType {
		if s.CollectionManager == nil {
			return vector.QuantNone
		}
		cfg, ok := s.CollectionManager.Get(collection)
		if !ok || cfg.Quantization == "" {
			return vector.QuantNone
		}
		return vector.ParseQuantization(cfg.Quantization)
	})
	s.QuantizedVecIndex.SetReady()

	s.VectorSearchers = map[string]vector.VectorSearcher{
		"flat":      s.VectorIndex,
		"quantized": s.QuantizedVecIndex,
	}

	// TTL
	s.TTLManager = ttl.NewTTLManager(db, serverTTLReaper{s: s})
	if err := s.TTLManager.EnsureBuckets(); err != nil {
		_ = db.Close()
		_ = os.Remove(f.Name())
		t.Fatal(err)
	}

	// FTS
	s.FTSIndex = fts.NewFTSIndex(db)
	if err := s.FTSIndex.EnsureBuckets(); err != nil {
		_ = db.Close()
		_ = os.Remove(f.Name())
		t.Fatal(err)
	}

	// Webhooks
	s.WebhookManager = webhooks.NewWebhookManager(db)
	if err := s.WebhookManager.EnsureBucket(); err != nil {
		_ = db.Close()
		_ = os.Remove(f.Name())
		t.Fatal(err)
	}
	if err := s.WebhookManager.LoadAll(); err != nil {
		_ = db.Close()
		_ = os.Remove(f.Name())
		t.Fatal(err)
	}

	// Schema
	s.SchemaManager = schema.NewSchemaManager(db)
	if err := s.SchemaManager.EnsureBucket(); err != nil {
		_ = db.Close()
		_ = os.Remove(f.Name())
		t.Fatal(err)
	}
	if err := s.SchemaManager.LoadAll(); err != nil {
		_ = db.Close()
		_ = os.Remove(f.Name())
		t.Fatal(err)
	}

	// Metrics (disabled in tests — avoids goroutines)
	s.Metrics = metrics.NewMetrics(false, &serverMetricsStats{s: s})

	cleanup := func() {
		_ = db.Close()
		_ = os.Remove(f.Name())
	}
	return s, cleanup
}

// addTestDoc is a convenience helper that creates a document via the internal
// addDocument method (bypassing HTTP) so that other handlers can be tested
// against pre-existing data.
func addTestDoc(t *testing.T, s *Server, coll, key, lang, content string, meta map[string][]string) storage.Doc {
	t.Helper()
	doc, _, err := s.addDocument(coll, key, lang, meta, content, 0, true)
	if err != nil {
		t.Fatalf("addTestDoc(%s/%s/%s): %v", coll, key, lang, err)
	}
	return doc
}

// doRequest fires a JSON body against a handler and returns the recorder.
func doRequest(t *testing.T, handler http.HandlerFunc, body interface{}) *httptest.ResponseRecorder {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	handler(rec, req)
	return rec
}

// ---------- 1. Health endpoint ----------

func TestHandleHealth(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	s.handleHealth(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"status":"healthy"`) {
		t.Errorf("unexpected body: %s", body)
	}
	if !strings.Contains(body, `"mode":"wr"`) {
		t.Errorf("expected mode wr in body: %s", body)
	}
}

// ---------- 2. Add document - success ----------

func TestHandleAdd_Success(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	payload := AddRequest{
		Collection: "blog",
		Key:        "hello-world",
		Lang:       "en",
		Meta:       map[string][]string{"author": {"alice"}},
		ContentMD:  "# Hello World",
	}
	rec := doRequest(t, s.handleAdd, payload)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body=%s", rec.Code, rec.Body.String())
	}

	var doc storage.Doc
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Key != "hello-world" {
		t.Errorf("expected key hello-world, got %s", doc.Key)
	}
	if doc.Lang != "en" {
		t.Errorf("expected lang en, got %s", doc.Lang)
	}
	if doc.ContentMD != "# Hello World" {
		t.Errorf("unexpected content: %s", doc.ContentMD)
	}
	if doc.AddedAt == 0 {
		t.Error("expected non-zero addedAt")
	}
	if doc.UpdatedAt == 0 {
		t.Error("expected non-zero updatedAt")
	}
}

// ---------- 3. Add document - missing fields ----------

func TestHandleAdd_MissingFields(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	cases := []struct {
		name    string
		payload AddRequest
	}{
		{"missing collection", AddRequest{Key: "k", Lang: "en"}},
		{"missing key", AddRequest{Collection: "c", Lang: "en"}},
		{"missing lang", AddRequest{Collection: "c", Key: "k"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := doRequest(t, s.handleAdd, tc.payload)
			if rec.Code != http.StatusBadRequest {
				t.Errorf("expected 400, got %d; body=%s", rec.Code, rec.Body.String())
			}
		})
	}
}

// ---------- 4. Add document - read-only mode ----------

func TestHandleAdd_ReadOnlyMode(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	s.Mode = ModeRead

	handler := s.guardWrite(s.handleAdd)
	payload := AddRequest{
		Collection: "blog",
		Key:        "post",
		Lang:       "en",
		ContentMD:  "content",
	}
	rec := doRequest(t, handler, payload)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d; body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "read-only") {
		t.Errorf("expected read-only message, got: %s", rec.Body.String())
	}
}

// ---------- 5. Add document - update existing ----------

func TestHandleAdd_UpdateExisting(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	// Create initial document
	payload := AddRequest{
		Collection: "blog",
		Key:        "post1",
		Lang:       "en",
		Meta:       map[string][]string{"tag": {"go"}},
		ContentMD:  "version 1",
	}
	rec1 := doRequest(t, s.handleAdd, payload)
	if rec1.Code != http.StatusOK {
		t.Fatalf("first add failed: %d %s", rec1.Code, rec1.Body.String())
	}
	var doc1 storage.Doc
	_ = json.Unmarshal(rec1.Body.Bytes(), &doc1)

	// Update the same document
	payload.ContentMD = "version 2"
	payload.Meta = map[string][]string{"tag": {"go", "rust"}}
	rec2 := doRequest(t, s.handleAdd, payload)
	if rec2.Code != http.StatusOK {
		t.Fatalf("second add failed: %d %s", rec2.Code, rec2.Body.String())
	}
	var doc2 storage.Doc
	_ = json.Unmarshal(rec2.Body.Bytes(), &doc2)

	if doc2.ID != doc1.ID {
		t.Errorf("expected same ID on update, got %s vs %s", doc1.ID, doc2.ID)
	}
	if doc2.ContentMD != "version 2" {
		t.Errorf("expected updated content, got %s", doc2.ContentMD)
	}
	if doc2.AddedAt != doc1.AddedAt {
		t.Errorf("addedAt should stay the same on update: %d vs %d", doc1.AddedAt, doc2.AddedAt)
	}
	if doc2.UpdatedAt < doc1.UpdatedAt {
		t.Errorf("updatedAt should be >= original: %d vs %d", doc2.UpdatedAt, doc1.UpdatedAt)
	}
}

// ---------- 6. Get document - success ----------

func TestHandleGet_Success(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	addTestDoc(t, s, "blog", "mypost", "en", "# My Post", map[string][]string{"author": {"bob"}})

	payload := GetRequest{
		Collection: "blog",
		Key:        "mypost",
		Lang:       "en",
	}
	rec := doRequest(t, s.handleGet, payload)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body=%s", rec.Code, rec.Body.String())
	}

	var doc storage.Doc
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Key != "mypost" {
		t.Errorf("expected key mypost, got %s", doc.Key)
	}
	if doc.ContentMD != "# My Post" {
		t.Errorf("unexpected content: %s", doc.ContentMD)
	}
}

// ---------- 7. Get document - not found ----------

func TestHandleGet_NotFound(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	payload := GetRequest{
		Collection: "blog",
		Key:        "nonexistent",
		Lang:       "en",
	}
	rec := doRequest(t, s.handleGet, payload)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "not found") {
		t.Errorf("expected 'not found' in body, got: %s", rec.Body.String())
	}
}

// ---------- 8. Get document - missing fields ----------

func TestHandleGet_MissingFields(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	payload := GetRequest{Collection: "blog", Key: "k"} // missing lang
	rec := doRequest(t, s.handleGet, payload)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d; body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "missing fields") {
		t.Errorf("expected missing-fields error, got: %s", rec.Body.String())
	}
}

// ---------- 9. Get document with env templating ----------

func TestHandleGet_EnvTemplating(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	addTestDoc(t, s, "pages", "home", "en", "Welcome %%user%% to %%site%%!", nil)

	payload := GetRequest{
		Collection: "pages",
		Key:        "home",
		Lang:       "en",
		Env:        map[string]string{"user": "Alice", "site": "MDDB"},
	}
	rec := doRequest(t, s.handleGet, payload)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var doc storage.Doc
	_ = json.Unmarshal(rec.Body.Bytes(), &doc)
	if doc.ContentMD != "Welcome Alice to MDDB!" {
		t.Errorf("expected templated content, got: %s", doc.ContentMD)
	}
}

// ---------- 10. Delete document - success ----------

func TestHandleDelete_Success(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	addTestDoc(t, s, "blog", "to-delete", "en", "bye", nil)

	payload := DeleteRequest{Collection: "blog", Key: "to-delete", Lang: "en"}
	rec := doRequest(t, s.handleDelete, payload)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"status":"deleted"`) {
		t.Errorf("unexpected body: %s", rec.Body.String())
	}

	// Confirm gone
	getRec := doRequest(t, s.handleGet, GetRequest{Collection: "blog", Key: "to-delete", Lang: "en"})
	if getRec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 after delete, got %d", getRec.Code)
	}
}

// ---------- 11. Delete document - not found ----------

func TestHandleDelete_NotFound(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	payload := DeleteRequest{Collection: "blog", Key: "ghost", Lang: "en"}
	rec := doRequest(t, s.handleDelete, payload)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "not found") {
		t.Errorf("expected not-found error, got: %s", rec.Body.String())
	}
}

// ---------- 12. Delete document - missing fields ----------

func TestHandleDelete_MissingFields(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	payload := DeleteRequest{Collection: "blog"} // missing key+lang
	rec := doRequest(t, s.handleDelete, payload)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d; body=%s", rec.Code, rec.Body.String())
	}
}

// ---------- 13. Delete document - read-only mode ----------

func TestHandleDelete_ReadOnlyMode(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	s.Mode = ModeRead

	handler := s.guardWrite(s.handleDelete)
	payload := DeleteRequest{Collection: "blog", Key: "k", Lang: "en"}
	rec := doRequest(t, handler, payload)

	if rec.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d", rec.Code)
	}
}

// ---------- 14. Delete collection ----------

func TestHandleDeleteCollection_Success(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	// Seed several docs
	addTestDoc(t, s, "notes", "n1", "en", "note 1", nil)
	addTestDoc(t, s, "notes", "n2", "en", "note 2", nil)
	addTestDoc(t, s, "notes", "n3", "en", "note 3", nil)
	// Different collection, should be untouched
	addTestDoc(t, s, "blog", "b1", "en", "blog 1", nil)

	payload := DeleteCollectionRequest{Collection: "notes"}
	rec := doRequest(t, s.handleDeleteCollection, payload)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body=%s", rec.Code, rec.Body.String())
	}

	var resp map[string]interface{}
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp["deletedCount"] != float64(3) {
		t.Errorf("expected 3 deleted, got %v", resp["deletedCount"])
	}

	// Confirm notes are gone
	getRec := doRequest(t, s.handleGet, GetRequest{Collection: "notes", Key: "n1", Lang: "en"})
	if getRec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for deleted doc, got %d", getRec.Code)
	}

	// Confirm blog still exists
	blogRec := doRequest(t, s.handleGet, GetRequest{Collection: "blog", Key: "b1", Lang: "en"})
	if blogRec.Code != http.StatusOK {
		t.Errorf("expected blog doc to still exist, got %d", blogRec.Code)
	}
}

// ---------- 15. Delete collection - missing collection ----------

func TestHandleDeleteCollection_MissingCollection(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	payload := DeleteCollectionRequest{}
	rec := doRequest(t, s.handleDeleteCollection, payload)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d; body=%s", rec.Code, rec.Body.String())
	}
}

// ---------- 16. Delete collection - empty collection (no docs) ----------

func TestHandleDeleteCollection_EmptyCollection(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	payload := DeleteCollectionRequest{Collection: "empty"}
	rec := doRequest(t, s.handleDeleteCollection, payload)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var resp map[string]interface{}
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp["deletedCount"] != float64(0) {
		t.Errorf("expected 0 deleted, got %v", resp["deletedCount"])
	}
}

// ---------- 17. Search - no filter (full scan) ----------

func TestHandleSearch_NoFilter(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	addTestDoc(t, s, "blog", "a", "en", "alpha", nil)
	addTestDoc(t, s, "blog", "b", "en", "beta", nil)
	addTestDoc(t, s, "blog", "c", "en", "gamma", nil)

	payload := SearchRequest{Collection: "blog"}
	rec := doRequest(t, s.handleSearch, payload)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body=%s", rec.Code, rec.Body.String())
	}

	var docs []storage.Doc
	if err := json.Unmarshal(rec.Body.Bytes(), &docs); err != nil {
		t.Fatal(err)
	}
	if len(docs) != 3 {
		t.Errorf("expected 3 docs, got %d", len(docs))
	}
}

// ---------- 18. Search - with metadata filter ----------

func TestHandleSearch_WithMetaFilter(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	addTestDoc(t, s, "blog", "p1", "en", "post 1", map[string][]string{"tag": {"go"}})
	addTestDoc(t, s, "blog", "p2", "en", "post 2", map[string][]string{"tag": {"rust"}})
	addTestDoc(t, s, "blog", "p3", "en", "post 3", map[string][]string{"tag": {"go", "rust"}})

	payload := SearchRequest{
		Collection: "blog",
		FilterMeta: map[string][]string{"tag": {"go"}},
	}
	rec := doRequest(t, s.handleSearch, payload)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body=%s", rec.Code, rec.Body.String())
	}

	var docs []storage.Doc
	_ = json.Unmarshal(rec.Body.Bytes(), &docs)
	if len(docs) != 2 {
		t.Errorf("expected 2 docs with tag=go, got %d", len(docs))
	}
}

// ---------- 19. Search - sort by key asc ----------

func TestHandleSearch_SortByKeyAsc(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	addTestDoc(t, s, "blog", "cherry", "en", "c", nil)
	addTestDoc(t, s, "blog", "apple", "en", "a", nil)
	addTestDoc(t, s, "blog", "banana", "en", "b", nil)

	payload := SearchRequest{
		Collection: "blog",
		Sort:       "key",
		Asc:        true,
	}
	rec := doRequest(t, s.handleSearch, payload)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var docs []storage.Doc
	_ = json.Unmarshal(rec.Body.Bytes(), &docs)
	if len(docs) != 3 {
		t.Fatalf("expected 3, got %d", len(docs))
	}
	if docs[0].Key != "apple" || docs[1].Key != "banana" || docs[2].Key != "cherry" {
		t.Errorf("expected apple, banana, cherry; got %s, %s, %s", docs[0].Key, docs[1].Key, docs[2].Key)
	}
}

// ---------- 20. Search - pagination (limit + offset) ----------

func TestHandleSearch_Pagination(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	for i := 0; i < 10; i++ {
		key := "doc-" + string(rune('a'+i))
		addTestDoc(t, s, "pages", key, "en", "content", nil)
	}

	payload := SearchRequest{
		Collection: "pages",
		Limit:      3,
		Offset:     2,
		Sort:       "key",
		Asc:        true,
	}
	rec := doRequest(t, s.handleSearch, payload)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var docs []storage.Doc
	_ = json.Unmarshal(rec.Body.Bytes(), &docs)
	if len(docs) != 3 {
		t.Errorf("expected 3 docs (limit=3), got %d", len(docs))
	}
}

// ---------- 21. Search - empty collection ----------

func TestHandleSearch_EmptyCollection(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	payload := SearchRequest{Collection: "nonexistent"}
	rec := doRequest(t, s.handleSearch, payload)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var docs []storage.Doc
	_ = json.Unmarshal(rec.Body.Bytes(), &docs)
	if len(docs) != 0 {
		t.Errorf("expected 0 docs, got %d", len(docs))
	}
}

// ---------- 22. Search - default limit applied ----------

func TestHandleSearch_DefaultLimit(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	// Add 60 docs to exceed the default limit of 50
	for i := 0; i < 60; i++ {
		key := "d" + strings.Repeat("0", 3-len(string(rune('0'+i/10)))) + string(rune('0'+i/10)) + string(rune('0'+i%10))
		addTestDoc(t, s, "bulk", key, "en", "content", nil)
	}

	payload := SearchRequest{Collection: "bulk"} // Limit defaults to 50
	rec := doRequest(t, s.handleSearch, payload)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var docs []storage.Doc
	_ = json.Unmarshal(rec.Body.Bytes(), &docs)
	if len(docs) != 50 {
		t.Errorf("expected default limit of 50, got %d", len(docs))
	}
}

// ---------- 23. Stats endpoint ----------

func TestHandleStats(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	addTestDoc(t, s, "blog", "p1", "en", "content1", map[string][]string{"author": {"alice"}})
	addTestDoc(t, s, "blog", "p2", "en", "content2", map[string][]string{"author": {"bob"}})
	addTestDoc(t, s, "wiki", "page1", "en", "wiki content", nil)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/stats", nil)
	s.handleStats(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body=%s", rec.Code, rec.Body.String())
	}

	var stats struct {
		DatabasePath     string `json:"databasePath"`
		Mode             string `json:"mode"`
		TotalDocuments   int    `json:"totalDocuments"`
		TotalRevisions   int    `json:"totalRevisions"`
		TotalMetaIndices int    `json:"totalMetaIndices"`
		Collections      []struct {
			Name          string `json:"name"`
			DocumentCount int    `json:"documentCount"`
		} `json:"collections"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &stats); err != nil {
		t.Fatal(err)
	}

	if stats.TotalDocuments != 3 {
		t.Errorf("expected 3 total docs, got %d", stats.TotalDocuments)
	}
	if stats.Mode != "wr" {
		t.Errorf("expected mode wr, got %s", stats.Mode)
	}
	if len(stats.Collections) != 2 {
		t.Errorf("expected 2 collections, got %d", len(stats.Collections))
	}
	// collections are sorted alphabetically
	if len(stats.Collections) >= 2 {
		if stats.Collections[0].Name != "blog" {
			t.Errorf("expected first collection=blog, got %s", stats.Collections[0].Name)
		}
		if stats.Collections[0].DocumentCount != 2 {
			t.Errorf("expected blog to have 2 docs, got %d", stats.Collections[0].DocumentCount)
		}
		if stats.Collections[1].Name != "wiki" {
			t.Errorf("expected second collection=wiki, got %s", stats.Collections[1].Name)
		}
	}
	if stats.TotalRevisions != 3 {
		t.Errorf("expected 3 total revisions, got %d", stats.TotalRevisions)
	}
	if stats.TotalMetaIndices != 2 {
		t.Errorf("expected 2 meta indices (author=alice, author=bob), got %d", stats.TotalMetaIndices)
	}
}

// ---------- 24. Stats - empty database ----------

func TestHandleStats_EmptyDB(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/stats", nil)
	s.handleStats(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var stats struct {
		TotalDocuments int `json:"totalDocuments"`
		Collections    []struct {
			Name string `json:"name"`
		} `json:"collections"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &stats)
	if stats.TotalDocuments != 0 {
		t.Errorf("expected 0 docs, got %d", stats.TotalDocuments)
	}
	if len(stats.Collections) != 0 {
		t.Errorf("expected 0 collections, got %d", len(stats.Collections))
	}
}

// ---------- 25. Truncate revisions ----------

func TestHandleTruncate_Success(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	// Create the document first via the handler
	payload := AddRequest{
		Collection: "blog",
		Key:        "rev-test",
		Lang:       "en",
		ContentMD:  "version 0",
	}
	rec := doRequest(t, s.handleAdd, payload)
	if rec.Code != http.StatusOK {
		t.Fatalf("initial add failed: %d", rec.Code)
	}

	var doc storage.Doc
	_ = json.Unmarshal(rec.Body.Bytes(), &doc)

	// Manually insert additional revision entries with distinct timestamps
	// because addDocument uses time.Now().Unix() which has second resolution
	// and all updates within the same second share the same revision key.
	_ = s.DB.Update(func(tx *bolt.Tx) error {
		bRev := tx.Bucket([]byte("rev"))
		for i := 1; i <= 4; i++ {
			ts := doc.AddedAt + int64(i)
			rkey := append(storage.RevPrefix("blog", doc.ID), []byte(fmt.Sprintf("%020d", ts))...)
			buf, _ := json.Marshal(storage.Doc{
				ID: doc.ID, Key: "rev-test", Lang: "en",
				ContentMD: "version " + string(rune('0'+i)),
				AddedAt:   doc.AddedAt, UpdatedAt: ts,
			})
			_ = bRev.Put(rkey, buf)
		}
		return nil
	})

	// Count revisions before truncation
	var revsBefore int
	_ = s.DB.View(func(tx *bolt.Tx) error {
		c := tx.Bucket([]byte("rev")).Cursor()
		prefix := storage.RevPrefix("blog", doc.ID)
		for k, _ := c.Seek(prefix); k != nil && bytes.HasPrefix(k, prefix); k, _ = c.Next() {
			revsBefore++
		}
		return nil
	})
	if revsBefore != 5 {
		t.Fatalf("expected 5 revisions before truncate, got %d", revsBefore)
	}

	// Truncate keeping last 2
	truncPayload := TruncateRequest{Collection: "blog", KeepRevs: 2}
	truncRec := doRequest(t, s.handleTruncate, truncPayload)
	if truncRec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body=%s", truncRec.Code, truncRec.Body.String())
	}

	// Count revisions after
	var revsAfter int
	_ = s.DB.View(func(tx *bolt.Tx) error {
		c := tx.Bucket([]byte("rev")).Cursor()
		prefix := storage.RevPrefix("blog", doc.ID)
		for k, _ := c.Seek(prefix); k != nil && bytes.HasPrefix(k, prefix); k, _ = c.Next() {
			revsAfter++
		}
		return nil
	})
	if revsAfter != 2 {
		t.Errorf("expected 2 revisions after truncate, got %d", revsAfter)
	}
}

// ---------- 26. Truncate - missing collection ----------

func TestHandleTruncate_MissingCollection(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	payload := TruncateRequest{KeepRevs: 1}
	rec := doRequest(t, s.handleTruncate, payload)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d; body=%s", rec.Code, rec.Body.String())
	}
}

// ---------- 27. Truncate - read-only mode ----------

func TestHandleTruncate_ReadOnlyMode(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	s.Mode = ModeRead

	handler := s.guardWrite(s.handleTruncate)
	payload := TruncateRequest{Collection: "blog", KeepRevs: 1}
	rec := doRequest(t, handler, payload)

	if rec.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d", rec.Code)
	}
}

// ---------- 28. Backup ----------

func TestHandleBackup_Success(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	bdir := t.TempDir()
	t.Setenv("MDDB_BACKUP_DIR", bdir)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/backup?to=snap.db", nil)
	s.handleBackup(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body=%s", rec.Code, rec.Body.String())
	}

	if _, err := os.Stat(filepath.Join(bdir, "snap.db")); os.IsNotExist(err) {
		t.Error("backup file was not created")
	}

	if !strings.Contains(rec.Body.String(), "snap.db") {
		t.Errorf("expected backup path in response, got: %s", rec.Body.String())
	}
}

// ---------- 29. Restore ----------

func TestHandleRestore_Success(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	// Add a document so the DB has data
	addTestDoc(t, s, "blog", "original", "en", "original content", nil)

	bdir := t.TempDir()
	t.Setenv("MDDB_BACKUP_DIR", bdir)

	recBackup := httptest.NewRecorder()
	reqBackup := httptest.NewRequest(http.MethodGet, "/v1/backup?to=snap.db", nil)
	s.handleBackup(recBackup, reqBackup)
	if recBackup.Code != http.StatusOK {
		t.Fatalf("backup failed: %d", recBackup.Code)
	}

	// Restore from backup
	restoreBody := map[string]string{"from": "snap.db"}
	rec := doRequest(t, s.handleRestore, restoreBody)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "restored") {
		t.Errorf("expected 'restored' in body, got: %s", rec.Body.String())
	}

	// Verify we can still read from the restored DB
	getRec := doRequest(t, s.handleGet, GetRequest{Collection: "blog", Key: "original", Lang: "en"})
	if getRec.Code != http.StatusOK {
		t.Errorf("expected to read doc from restored DB, got %d; body=%s", getRec.Code, getRec.Body.String())
	}
}

// ---------- 30. Restore - missing from field ----------

func TestHandleRestore_MissingFrom(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	rec := doRequest(t, s.handleRestore, map[string]string{})
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "missing from") {
		t.Errorf("expected 'missing from' error, got: %s", rec.Body.String())
	}
}

// ---------- 31. Add with TTL ----------

func TestHandleAdd_WithTTL(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	payload := AddRequest{
		Collection: "temp",
		Key:        "expiring",
		Lang:       "en",
		ContentMD:  "temporary content",
		TTL:        3600, // 1 hour
	}
	rec := doRequest(t, s.handleAdd, payload)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body=%s", rec.Code, rec.Body.String())
	}

	var doc storage.Doc
	_ = json.Unmarshal(rec.Body.Bytes(), &doc)
	if doc.ExpiresAt == 0 {
		t.Error("expected non-zero expiresAt with TTL")
	}
	if doc.ExpiresAt < doc.AddedAt {
		t.Error("expiresAt should be after addedAt")
	}
}

// ---------- 32. Add with invalid JSON body ----------

func TestHandleAdd_InvalidJSON(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/add", strings.NewReader("not json"))
	s.handleAdd(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

// ---------- 33. Get with invalid JSON body ----------

func TestHandleGet_InvalidJSON(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/get", strings.NewReader("{invalid"))
	s.handleGet(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

// ---------- 34. Search with invalid JSON body ----------

func TestHandleSearch_InvalidJSON(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/search", strings.NewReader(""))
	s.handleSearch(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

// ---------- 35. guardWrite with write mode ----------

func TestGuardWrite_WriteMode(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	s.Mode = ModeWrite
	called := false
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	handler := s.guardWrite(inner)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	handler(rec, req)

	if !called {
		t.Error("expected inner handler to be called in write mode")
	}
}

// ---------- 36. guardWrite with RW mode ----------

func TestGuardWrite_RWMode(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	s.Mode = ModeRW
	called := false
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	handler := s.guardWrite(inner)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	handler(rec, req)

	if !called {
		t.Error("expected inner handler to be called in RW mode")
	}
}

// ---------- 37. withJSON middleware ----------

func TestWithJSON_SetsContentType(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
	handler := withJSON(inner)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	handler.ServeHTTP(rec, req)

	ct := rec.Header().Get("Content-Type")
	if ct != "application/json; charset=utf-8" {
		t.Errorf("expected JSON content type, got: %s", ct)
	}
}

// ---------- 38. Search with meta filter intersection ----------

func TestHandleSearch_MetaFilterIntersection(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	addTestDoc(t, s, "blog", "p1", "en", "post 1", map[string][]string{
		"tag":    {"go"},
		"author": {"alice"},
	})
	addTestDoc(t, s, "blog", "p2", "en", "post 2", map[string][]string{
		"tag":    {"go"},
		"author": {"bob"},
	})
	addTestDoc(t, s, "blog", "p3", "en", "post 3", map[string][]string{
		"tag":    {"rust"},
		"author": {"alice"},
	})

	// Filter: tag=go AND author=alice
	payload := SearchRequest{
		Collection: "blog",
		FilterMeta: map[string][]string{
			"tag":    {"go"},
			"author": {"alice"},
		},
	}
	rec := doRequest(t, s.handleSearch, payload)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body=%s", rec.Code, rec.Body.String())
	}

	var docs []storage.Doc
	_ = json.Unmarshal(rec.Body.Bytes(), &docs)
	if len(docs) != 1 {
		t.Errorf("expected 1 doc (tag=go AND author=alice), got %d", len(docs))
	}
	if len(docs) == 1 && docs[0].Key != "p1" {
		t.Errorf("expected p1, got %s", docs[0].Key)
	}
}

// ---------- 39. Sort by addedAt desc (default) ----------

func TestHandleSearch_SortByAddedAtDesc(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	addTestDoc(t, s, "blog", "first", "en", "first", nil)
	addTestDoc(t, s, "blog", "second", "en", "second", nil)
	addTestDoc(t, s, "blog", "third", "en", "third", nil)

	payload := SearchRequest{
		Collection: "blog",
		Sort:       "addedAt",
		Asc:        false,
	}
	rec := doRequest(t, s.handleSearch, payload)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var docs []storage.Doc
	_ = json.Unmarshal(rec.Body.Bytes(), &docs)
	if len(docs) != 3 {
		t.Fatalf("expected 3, got %d", len(docs))
	}
	// addedAt desc = latest first
	for i := 1; i < len(docs); i++ {
		if docs[i].AddedAt > docs[i-1].AddedAt {
			t.Errorf("expected descending addedAt order at index %d", i)
		}
	}
}

// ---------- 40. Multiple languages for same key ----------

func TestMultiLanguageDocuments(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	addTestDoc(t, s, "pages", "home", "en", "Hello", nil)
	addTestDoc(t, s, "pages", "home", "pl", "Czesc", nil)
	addTestDoc(t, s, "pages", "home", "de", "Hallo", nil)

	// Get English
	recEn := doRequest(t, s.handleGet, GetRequest{Collection: "pages", Key: "home", Lang: "en"})
	if recEn.Code != http.StatusOK {
		t.Fatalf("expected 200 for en, got %d", recEn.Code)
	}
	var docEn storage.Doc
	_ = json.Unmarshal(recEn.Body.Bytes(), &docEn)
	if docEn.ContentMD != "Hello" {
		t.Errorf("expected 'Hello', got %s", docEn.ContentMD)
	}

	// Get Polish
	recPl := doRequest(t, s.handleGet, GetRequest{Collection: "pages", Key: "home", Lang: "pl"})
	if recPl.Code != http.StatusOK {
		t.Fatalf("expected 200 for pl, got %d", recPl.Code)
	}
	var docPl storage.Doc
	_ = json.Unmarshal(recPl.Body.Bytes(), &docPl)
	if docPl.ContentMD != "Czesc" {
		t.Errorf("expected 'Czesc', got %s", docPl.ContentMD)
	}

	// Search should return all 3
	searchRec := doRequest(t, s.handleSearch, SearchRequest{Collection: "pages"})
	var allDocs []storage.Doc
	_ = json.Unmarshal(searchRec.Body.Bytes(), &allDocs)
	if len(allDocs) != 3 {
		t.Errorf("expected 3 language variants, got %d", len(allDocs))
	}

	// Delete only English
	delRec := doRequest(t, s.handleDelete, DeleteRequest{Collection: "pages", Key: "home", Lang: "en"})
	if delRec.Code != http.StatusOK {
		t.Fatalf("delete en failed: %d", delRec.Code)
	}

	// Polish should still exist
	recPl2 := doRequest(t, s.handleGet, GetRequest{Collection: "pages", Key: "home", Lang: "pl"})
	if recPl2.Code != http.StatusOK {
		t.Error("expected Polish doc to survive English deletion")
	}
}

// ---------- 41. Schema validation on add ----------

func TestHandleAdd_SchemaValidation(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	// Set a schema requiring "author" meta field
	schema := `{"required":["author"],"properties":{"author":{"type":"string"}}}`
	if err := s.SchemaManager.Set("strict-blog", schema); err != nil {
		t.Fatal(err)
	}

	// Add without required field should fail
	payload := AddRequest{
		Collection: "strict-blog",
		Key:        "post1",
		Lang:       "en",
		ContentMD:  "test",
		Meta:       map[string][]string{}, // missing "author"
	}
	rec := doRequest(t, s.handleAdd, payload)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for missing required meta, got %d; body=%s", rec.Code, rec.Body.String())
	}

	// Add with required field should succeed
	payload.Meta = map[string][]string{"author": {"alice"}}
	rec2 := doRequest(t, s.handleAdd, payload)
	if rec2.Code != http.StatusOK {
		t.Errorf("expected 200 with valid meta, got %d; body=%s", rec2.Code, rec2.Body.String())
	}
}

// ---------- 42. Backup with default path ----------

func TestHandleBackup_DefaultPath(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/backup", nil) // no ?to= param
	s.handleBackup(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body=%s", rec.Code, rec.Body.String())
	}

	var resp map[string]string
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	backupPath := resp["backup"]
	if backupPath == "" {
		t.Fatal("expected backup path in response")
	}
	// Clean up the auto-generated backup file
	defer func() { _ = os.Remove(backupPath) }()

	if _, err := os.Stat(backupPath); os.IsNotExist(err) {
		t.Error("default backup file was not created")
	}
}

// ---------- 43. Export handler - unsupported format ----------

func TestHandleExport_UnsupportedFormat(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	payload := ExportRequest{
		Collection: "blog",
		Format:     "csv", // unsupported
	}
	rec := doRequest(t, s.handleExport, payload)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for unsupported format, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "unsupported format") {
		t.Errorf("expected unsupported format error, got: %s", rec.Body.String())
	}
}

// ---------- 44. Delete then re-add ----------

func TestDeleteThenReAdd(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	// Add
	addTestDoc(t, s, "blog", "comeback", "en", "version 1", nil)

	// Delete
	delRec := doRequest(t, s.handleDelete, DeleteRequest{Collection: "blog", Key: "comeback", Lang: "en"})
	if delRec.Code != http.StatusOK {
		t.Fatalf("delete failed: %d", delRec.Code)
	}

	// Re-add
	payload := AddRequest{
		Collection: "blog",
		Key:        "comeback",
		Lang:       "en",
		ContentMD:  "version 2",
	}
	addRec := doRequest(t, s.handleAdd, payload)
	if addRec.Code != http.StatusOK {
		t.Fatalf("re-add failed: %d; body=%s", addRec.Code, addRec.Body.String())
	}

	var doc storage.Doc
	_ = json.Unmarshal(addRec.Body.Bytes(), &doc)
	if doc.ContentMD != "version 2" {
		t.Errorf("expected 'version 2', got %s", doc.ContentMD)
	}

	// Verify via get
	getRec := doRequest(t, s.handleGet, GetRequest{Collection: "blog", Key: "comeback", Lang: "en"})
	if getRec.Code != http.StatusOK {
		t.Errorf("expected to read re-added doc, got %d", getRec.Code)
	}
}

// ---------- 45. Truncate with KeepRevs=0 drops all history ----------

func TestHandleTruncate_DropAllRevisions(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	// Create and update doc to generate revisions
	for i := 0; i < 3; i++ {
		payload := AddRequest{
			Collection: "blog",
			Key:        "doc-trunc",
			Lang:       "en",
			ContentMD:  "v" + string(rune('0'+i)),
		}
		doRequest(t, s.handleAdd, payload)
	}

	// Truncate with KeepRevs=0
	truncPayload := TruncateRequest{Collection: "blog", KeepRevs: 0}
	rec := doRequest(t, s.handleTruncate, truncPayload)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	// Verify all revisions are gone
	var count int
	_ = s.DB.View(func(tx *bolt.Tx) error {
		c := tx.Bucket([]byte("rev")).Cursor()
		prefix := []byte("rev|blog|")
		for k, _ := c.Seek(prefix); k != nil && bytes.HasPrefix(k, prefix); k, _ = c.Next() {
			count++
		}
		return nil
	})
	if count != 0 {
		t.Errorf("expected 0 revisions after KeepRevs=0, got %d", count)
	}

	// The document itself should still be accessible
	getRec := doRequest(t, s.handleGet, GetRequest{Collection: "blog", Key: "doc-trunc", Lang: "en"})
	if getRec.Code != http.StatusOK {
		t.Errorf("expected doc to still exist after truncate, got %d", getRec.Code)
	}
}

// ---------- 46. Search - OR semantics within a meta value list ----------

func TestHandleSearch_MetaFilterORWithinValues(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	addTestDoc(t, s, "blog", "p1", "en", "post 1", map[string][]string{"tag": {"go"}})
	addTestDoc(t, s, "blog", "p2", "en", "post 2", map[string][]string{"tag": {"rust"}})
	addTestDoc(t, s, "blog", "p3", "en", "post 3", map[string][]string{"tag": {"python"}})

	// OR over values: tag = ["go", "rust"] should match p1 and p2 (and p3 is excluded)
	payload := SearchRequest{
		Collection: "blog",
		FilterMeta: map[string][]string{"tag": {"go", "rust"}},
	}
	rec := doRequest(t, s.handleSearch, payload)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var docs []storage.Doc
	_ = json.Unmarshal(rec.Body.Bytes(), &docs)
	if len(docs) != 2 {
		t.Errorf("expected 2 docs (tag in [go,rust]), got %d", len(docs))
	}
}

// ---------- 47. ok / bad response helpers ----------

func TestOkHelper(t *testing.T) {
	rec := httptest.NewRecorder()
	ok(rec, map[string]string{"foo": "bar"})
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"foo":"bar"`) {
		t.Errorf("unexpected body: %s", rec.Body.String())
	}
}

func TestBadHelper(t *testing.T) {
	rec := httptest.NewRecorder()
	bad(rec, io.ErrUnexpectedEOF)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "unexpected EOF") {
		t.Errorf("expected error message in body, got: %s", rec.Body.String())
	}
}

// ---------- 48. genID determinism and case insensitivity ----------

func TestGenID(t *testing.T) {
	id1 := genID("Blog", "Hello-World", "EN")
	id2 := genID("blog", "hello-world", "en")
	if id1 != id2 {
		t.Errorf("genID should be case-insensitive: %s vs %s", id1, id2)
	}
	if !strings.Contains(id1, "blog|hello-world|en") {
		t.Errorf("unexpected genID format: %s", id1)
	}
}

// ---------- 49. applyEnv ----------

func TestApplyEnv(t *testing.T) {
	result := applyEnv("Hello %%name%%, welcome to %%place%%!", map[string]string{
		"name":  "Alice",
		"place": "MDDB",
	})
	if result != "Hello Alice, welcome to MDDB!" {
		t.Errorf("unexpected result: %s", result)
	}
}

// ---------- 50. safe filename sanitizer ----------

func TestSafe(t *testing.T) {
	cases := []struct {
		in, out string
	}{
		{"hello-world", "hello-world"},
		{"hello world", "hello-world"},
		{"foo/bar.baz", "foo-bar-baz"},
		{"UPPERCASE_ok", "UPPERCASE_ok"},
	}
	for _, tc := range cases {
		got := safe(tc.in)
		if got != tc.out {
			t.Errorf("safe(%q)=%q, want %q", tc.in, got, tc.out)
		}
	}
}
