package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"mddb/internal/binlog"
	"mddb/internal/metrics"

	bolt "go.etcd.io/bbolt"
)

// Failure paths of the stored embedding configuration: what each function and
// handler answers when the bucket is gone, an entry is corrupt, the URL is
// refused or the caller is not an admin.

// covEmbPut writes raw bytes under key in the embedding_configs bucket.
func covEmbPut(t *testing.T, s *Server, key string, data []byte) {
	t.Helper()
	if err := s.DBUpdate(func(tx *bolt.Tx) error {
		return tx.Bucket([]byte("embedding_configs")).Put([]byte(key), data)
	}); err != nil {
		t.Fatal(err)
	}
}

// covEmbDropBucket removes the embedding_configs bucket.
func covEmbDropBucket(t *testing.T, s *Server) {
	t.Helper()
	if err := s.DBUpdate(func(tx *bolt.Tx) error {
		return tx.DeleteBucket([]byte("embedding_configs"))
	}); err != nil {
		t.Fatal(err)
	}
}

// covEmbDo runs handler with a request and returns the recorder.
func covEmbDo(handler http.HandlerFunc, method, path, body string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	handler(rec, httptest.NewRequest(method, path, strings.NewReader(body)))
	return rec
}

func TestEmbeddingConfigWithoutBucketFailsEverywhere(t *testing.T) {
	s, cleanup := newTestServerForEmbeddingConfig(t)
	defer cleanup()
	covEmbDropBucket(t, s)

	if err := s.SaveEmbeddingConfig(&EmbeddingConfig{ID: "a"}); err == nil {
		t.Error("Save succeeded without a bucket")
	}
	if _, err := s.GetEmbeddingConfig("a"); err == nil {
		t.Error("Get succeeded without a bucket")
	}
	if _, err := s.GetDefaultEmbeddingConfig(); err == nil {
		t.Error("GetDefault succeeded without a bucket")
	}
	if err := s.DeleteEmbeddingConfig("a"); err == nil {
		t.Error("Delete succeeded without a bucket")
	}
	if rec := covEmbDo(s.handleListEmbeddingConfigs, http.MethodGet, "/v1/embedding-configs", ""); rec.Code != http.StatusInternalServerError {
		t.Errorf("list status %d", rec.Code)
	}
}

func TestEmbeddingConfigCorruptEntry(t *testing.T) {
	s, cleanup := newTestServerForEmbeddingConfig(t)
	defer cleanup()
	covEmbPut(t, s, "broken", []byte("{not json"))

	// Making another config the default skips what it cannot read.
	if err := s.SaveEmbeddingConfig(&EmbeddingConfig{ID: "lex", Provider: "lexical", IsDefault: true}); err != nil {
		t.Fatalf("save next to a corrupt entry: %v", err)
	}
	if _, err := s.ListEmbeddingConfigs(); err == nil {
		t.Error("List hid a corrupt entry")
	}
}

func TestEmbeddingConfigRefusedURL(t *testing.T) {
	s, cleanup := newTestServerForEmbeddingConfig(t)
	defer cleanup()
	s.Metrics = metrics.NewMetrics(false, &serverMetricsStats{s: s})

	if err := s.SaveEmbeddingConfig(&EmbeddingConfig{ID: "x", APIURL: "ftp://example.com"}); err == nil {
		t.Error("Save accepted an ftp URL")
	}
	body := `{"id":"x","name":"X","provider":"ollama","model":"m","dimensions":8,"apiUrl":"ftp://example.com"}`
	if rec := covEmbDo(s.handleCreateEmbeddingConfig, http.MethodPost, "/v1/embedding-configs", body); rec.Code != http.StatusInternalServerError {
		t.Errorf("create status %d", rec.Code)
	}
	update := func(w http.ResponseWriter, r *http.Request) { s.handleUpdateEmbeddingConfig(w, r, "x") }
	if rec := covEmbDo(update, http.MethodPut, "/v1/embedding-configs/x", body); rec.Code != http.StatusInternalServerError {
		t.Errorf("update status %d", rec.Code)
	}

	// A stored entry with a URL the validator now refuses cannot be made default.
	covEmbPut(t, s, "old", []byte(`{"id":"old","provider":"ollama","apiUrl":"ftp://example.com"}`))
	if rec := covEmbDo(s.handleSetDefaultEmbeddingConfig, http.MethodPost, "/v1/embedding-configs/default", `{"id":"old"}`); rec.Code != http.StatusInternalServerError {
		t.Errorf("set-default status %d", rec.Code)
	}
}

func TestEmbeddingConfigHandlersRequireAdmin(t *testing.T) {
	s, cleanup := newTestServerForEmbeddingConfig(t)
	defer cleanup()
	covGEnableAuth(t, s)

	for name, h := range map[string]http.HandlerFunc{
		"collection":  s.handleEmbeddingConfigs,
		"detail":      s.handleEmbeddingConfigDetail,
		"set-default": s.handleSetDefaultEmbeddingConfig,
	} {
		method := http.MethodGet
		if name == "set-default" {
			method = http.MethodPost
		}
		if rec := covEmbDo(h, method, "/v1/embedding-configs/x", ""); rec.Code != http.StatusForbidden {
			t.Errorf("%s without claims: status %d", name, rec.Code)
		}
	}
}

func TestEmbeddingConfigDeleteIsReplicated(t *testing.T) {
	s, cleanup := newTestServerForEmbeddingConfig(t)
	defer cleanup()
	path := t.TempDir() + "/test.binlog"
	bl, err := binlog.NewBinlog(path, binlog.BinlogConfig{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = bl.Close() }()
	s.Binlog = bl

	if err := s.SaveEmbeddingConfig(&EmbeddingConfig{ID: "gone", Provider: "lexical"}); err != nil {
		t.Fatal(err)
	}
	before := bl.CurrentLSN()
	if err := s.DeleteEmbeddingConfig("gone"); err != nil {
		t.Fatal(err)
	}
	if bl.CurrentLSN() == before {
		t.Error("the delete never reached the binlog")
	}
}

func TestInitializeEmbeddingFromConfigRestartsTheWorker(t *testing.T) {
	s, cleanup := newTestServerForEmbeddingConfig(t)
	defer cleanup()
	s.InitializeEmbeddingFromConfig(&EmbeddingConfig{Provider: "lexical", Dimensions: 256})
	first := s.EmbeddingWorker
	s.InitializeEmbeddingFromConfig(&EmbeddingConfig{Provider: "lexical", Dimensions: 512})
	defer s.EmbeddingWorker.Stop()
	if s.EmbeddingWorker == first || s.Embedding.Dimensions() != 512 {
		t.Error("the second configuration did not replace the worker")
	}
}

func TestHandleConfigEncodeFailure(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	w := &covGFailWriter{}
	s.handleConfig(w, httptest.NewRequest(http.MethodGet, "/v1/config", nil))
	if w.code != http.StatusInternalServerError {
		t.Errorf("status %d after a failed write", w.code)
	}
}
