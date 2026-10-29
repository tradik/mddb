package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mddb/internal/binlog"
	"mddb/internal/encryption"
	json "mddb/internal/jsonx"

	bolt "go.etcd.io/bbolt"
)

// covHConfigRequest sends one request to the collection-config handler.
func covHConfigRequest(s *Server, method, target, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	rec := httptest.NewRecorder()
	s.handleCollectionConfig(rec, req)
	return rec
}

// covHBinlog opens a binlog next to a temporary database and subscribes to it.
func covHBinlog(t *testing.T) (*binlog.Binlog, <-chan *binlog.BinlogEntry) {
	t.Helper()
	bl, err := binlog.NewBinlog(filepath.Join(t.TempDir(), "x.db"), binlog.BinlogConfig{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = bl.Close() })
	return bl, bl.Subscribe("covH")
}

// covHNextEntry waits for the next binlog entry.
func covHNextEntry(t *testing.T, ch <-chan *binlog.BinlogEntry) *binlog.BinlogEntry {
	t.Helper()
	select {
	case e := <-ch:
		return e
	case <-time.After(5 * time.Second):
		t.Fatal("no binlog entry")
		return nil
	}
}

func TestCovHCollectionManagerBinlogAndEncryptor(t *testing.T) {
	s, cleanup := newTestServerForCollectionConfig(t)
	defer cleanup()
	cm := s.CollectionManager
	bl, ch := covHBinlog(t)
	cm.SetBinlog(bl)

	// A config stored before the encryptor is wired is mirrored into it.
	if err := cm.Set("secret", &CollectionConfig{Encrypted: true}); err != nil {
		t.Fatal(err)
	}
	if e := covHNextEntry(t, ch); e.Type != binlog.BinlogPut || e.BucketName != "colmeta" || string(e.Key) != "secret" {
		t.Fatalf("put entry: %+v", e)
	}
	if err := cm.Set("plain", &CollectionConfig{}); err != nil {
		t.Fatal(err)
	}
	covHNextEntry(t, ch)
	enc, _ := encryption.NewEncryptor()
	cm.SetEncryptor(enc)
	if !enc.CollectionEnabled("secret") || enc.CollectionEnabled("plain") {
		t.Fatal("existing configs were not mirrored into the encryptor")
	}

	// Later writes keep the encryptor in step.
	if err := cm.Set("plain", &CollectionConfig{Encrypted: true}); err != nil {
		t.Fatal(err)
	}
	covHNextEntry(t, ch)
	if !enc.CollectionEnabled("plain") {
		t.Fatal("set did not reach the encryptor")
	}
	if err := cm.Delete("plain"); err != nil {
		t.Fatal(err)
	}
	if e := covHNextEntry(t, ch); e.Type != binlog.BinlogDelete || string(e.Key) != "plain" {
		t.Fatalf("delete entry: %+v", e)
	}
	if enc.CollectionEnabled("plain") {
		t.Fatal("delete did not reach the encryptor")
	}
	if _, found := cm.Get("plain"); found {
		t.Fatal("deleted config still cached")
	}
}

func TestCovHCollectionManagerLoadAll(t *testing.T) {
	db, err := bolt.Open(filepath.Join(t.TempDir(), "c.db"), 0600, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	cm := NewCollectionManager(db)

	// No bucket yet: nothing to load and no error.
	if err := cm.LoadAll(); err != nil || len(cm.ListAll()) != 0 {
		t.Fatalf("empty load: %v %v", err, cm.ListAll())
	}

	if err := cm.EnsureBucket(); err != nil {
		t.Fatal(err)
	}
	if err := cm.Set("good", &CollectionConfig{Description: "kept"}); err != nil {
		t.Fatal(err)
	}
	if err := db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket(bucketColMeta).Put([]byte("bad"), []byte("{not json"))
	}); err != nil {
		t.Fatal(err)
	}
	if err := cm.LoadAll(); err != nil {
		t.Fatal(err)
	}
	all := cm.ListAll()
	if len(all) != 1 || all["good"].Description != "kept" || all["good"].Type != "default" {
		t.Fatalf("corrupt entries must be skipped, good ones loaded: %+v", all)
	}
}

func TestCovHCollectionManagerClosedDB(t *testing.T) {
	s, cleanup := newTestServerForCollectionConfig(t)
	defer cleanup()
	if err := s.CollectionManager.Set("c", &CollectionConfig{Description: "before"}); err != nil {
		t.Fatal(err)
	}
	_ = s.DB.Close()

	if err := s.CollectionManager.Set("c", &CollectionConfig{Description: "after"}); err == nil {
		t.Fatal("set on a closed database must fail")
	}
	if cfg, _ := s.CollectionManager.Get("c"); cfg.Description != "before" {
		t.Fatalf("a failed set changed the cache: %+v", cfg)
	}
	if err := s.CollectionManager.Delete("c"); err == nil {
		t.Fatal("delete on a closed database must fail")
	}
	if _, found := s.CollectionManager.Get("c"); !found {
		t.Fatal("a failed delete dropped the cached config")
	}

	// The handlers report both failures as 400.
	if rec := covHConfigRequest(s, http.MethodPut, "/v1/collection-config", `{"collection":"c"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("put on closed db: %d %s", rec.Code, rec.Body)
	}
	if rec := covHConfigRequest(s, http.MethodDelete, "/v1/collection-config?collection=c", ""); rec.Code != http.StatusBadRequest {
		t.Fatalf("delete on closed db: %d %s", rec.Code, rec.Body)
	}
}

func TestCovHCollectionConfigForbidden(t *testing.T) {
	s, cleanup := newTestServerForCollectionConfig(t)
	defer cleanup()
	am, amCleanup := authMwSetup(t)
	defer amCleanup()
	s.AuthManager = am // enabled; the requests carry no claims

	for _, tc := range []struct{ method, target, body string }{
		{http.MethodGet, "/v1/collection-config?collection=c", ""},
		{http.MethodPut, "/v1/collection-config", `{"collection":"c","description":"x"}`},
		{http.MethodDelete, "/v1/collection-config?collection=c", ""},
	} {
		if rec := covHConfigRequest(s, tc.method, tc.target, tc.body); rec.Code != http.StatusForbidden {
			t.Fatalf("%s: %d, want 403", tc.method, rec.Code)
		}
	}
	rec := httptest.NewRecorder()
	s.handleCollectionConfigList(rec, httptest.NewRequest(http.MethodGet, "/v1/collection-configs", nil))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("list: %d, want 403", rec.Code)
	}
	if _, found := s.CollectionManager.Get("c"); found {
		t.Fatal("a refused put stored a config")
	}
}

func TestCovHCollectionConfigSetValidation(t *testing.T) {
	s, cleanup := newTestServerForCollectionConfig(t)
	defer cleanup()

	for name, body := range map[string]string{
		"undecodable":      `{"collection":`,
		"unknown backend":  `{"collection":"c","storageBackend":"tape"}`,
		"s3 without conf":  `{"collection":"c","storageBackend":"s3"}`,
		"s3 without bkt":   `{"collection":"c","storageBackend":"s3","storageConfig":{"endpoint":"e"}}`,
		"bad quantization": `{"collection":"c","quantization":"int3"}`,
		"negative revs":    `{"collection":"c","maxRevisions":-1}`,
		"disk-only float":  `{"collection":"c","diskOnlyVectors":true,"quantization":"float32"}`,
		"disk-only none":   `{"collection":"c","diskOnlyVectors":true}`,
		"wordpress no url": `{"collection":"c","wordpress":{"apiKey":"k"}}`,
	} {
		rec := covHConfigRequest(s, http.MethodPut, "/v1/collection-config", body)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s: %d %s, want 400", name, rec.Code, rec.Body)
		}
	}
	if len(s.CollectionManager.ListAll()) != 0 {
		t.Fatal("a refused config was stored")
	}

	// The valid combination the refusals guard is accepted.
	rec := covHConfigRequest(s, http.MethodPut, "/v1/collection-config",
		`{"collection":"c","quantization":"int8","diskOnlyVectors":true,"maxRevisions":3}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("valid config: %d %s", rec.Code, rec.Body)
	}
	if cfg, _ := s.CollectionManager.Get("c"); !cfg.DiskOnlyVectors || cfg.MaxRevisions != 3 || cfg.Quantization != "int8" {
		t.Fatalf("stored: %+v", cfg)
	}
}

func TestCovHCollectionConfigListTenant(t *testing.T) {
	s, cleanup := newTestServerForCollectionConfig(t)
	defer cleanup()
	for _, c := range []string{"acme/docs", "other/docs", "global"} {
		if err := s.CollectionManager.Set(c, &CollectionConfig{}); err != nil {
			t.Fatal(err)
		}
	}
	ctx := context.WithValue(context.Background(), authContextKey, &JWTClaims{Username: "u", Tenant: "acme"})
	req := httptest.NewRequest(http.MethodGet, "/v1/collection-configs", nil).WithContext(ctx)
	rec := httptest.NewRecorder()
	s.handleCollectionConfigList(rec, req)
	var out struct {
		Configs []struct {
			Collection string `json:"collection"`
		} `json:"configs"`
		Total int `json:"total"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Total != 1 || out.Configs[0].Collection != "acme/docs" {
		t.Fatalf("a tenant sees only its own collections: %s", rec.Body)
	}
}
