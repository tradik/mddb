package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"mddb/internal/storage"

	bolt "go.etcd.io/bbolt"
)

// INT-019: single-document writes and reads go through MDDB's write path, so
// schemas, quotas and read-only collections answer as they do on /v1 — in
// Elasticsearch's shape.

func TestESDocumentLifecycle(t *testing.T) {
	s, h := esTestServer(t)

	rec, out := esDo(t, h, http.MethodPut, "/es/books/_doc/Fox?refresh=wait_for",
		`{"content":"The quick brown fox","genre":"fable","year":1990,"tags":["a","b"],"draft":false,"note":null}`)
	if rec.Code != http.StatusCreated || out["result"] != "created" || out["_id"] != "Fox" {
		t.Fatalf("index: %d %v", rec.Code, out)
	}
	rec, out = esDo(t, h, http.MethodPost, "/es/books/_doc/fox", `{"content":"The quick red fox","genre":"fable"}`)
	if rec.Code != http.StatusOK || out["result"] != "updated" {
		t.Fatalf("re-index (ids are case-insensitive): %d %v", rec.Code, out)
	}
	rec, out = esDo(t, h, http.MethodPost, "/es/books/_doc", `{"content":"no id","lang":"pl"}`)
	id, _ := out["_id"].(string)
	if rec.Code != http.StatusCreated || len(id) != 20 {
		t.Fatalf("auto id: %d %v", rec.Code, out)
	}
	if rec, out = esDo(t, h, http.MethodGet, "/es/books/_doc/"+id+"?lang=pl", ""); rec.Code != http.StatusOK || out["_source"].(map[string]any)["lang"] != "pl" {
		t.Fatalf("get in its language: %d %v", rec.Code, out)
	}

	rec, out = esDo(t, h, http.MethodGet, "/es/books/_doc/fox", "")
	src, _ := out["_source"].(map[string]any)
	if rec.Code != http.StatusOK || out["found"] != true || src["content"] != "The quick red fox" || src["genre"] != "fable" {
		t.Fatalf("get: %d %v", rec.Code, out)
	}
	if rec, _ = esDo(t, h, http.MethodHead, "/es/books/_doc/fox", ""); rec.Code != http.StatusOK {
		t.Fatalf("exists: %d", rec.Code)
	}

	rec, out = esDo(t, h, http.MethodPut, "/es/books/_create/fox", `{"content":"again"}`)
	if rec.Code != http.StatusConflict || esErrType(out) != "version_conflict_engine_exception" {
		t.Fatalf("create over an existing document: %d %v", rec.Code, out)
	}
	if rec, _ = esDo(t, h, http.MethodPut, "/es/books/_doc/owl?op_type=create", `{"content":"owl"}`); rec.Code != http.StatusCreated {
		t.Fatalf("op_type=create: %d", rec.Code)
	}

	rec, out = esDo(t, h, http.MethodDelete, "/es/books/_doc/fox?refresh", "")
	if rec.Code != http.StatusOK || out["result"] != "deleted" {
		t.Fatalf("delete: %d %v", rec.Code, out)
	}
	rec, out = esDo(t, h, http.MethodDelete, "/es/books/_doc/fox", "")
	if rec.Code != http.StatusNotFound || out["result"] != "not_found" {
		t.Fatalf("delete again: %d %v", rec.Code, out)
	}
	rec, out = esDo(t, h, http.MethodGet, "/es/books/_doc/fox", "")
	if rec.Code != http.StatusNotFound || out["found"] != false {
		t.Fatalf("get deleted: %d %v", rec.Code, out)
	}
	if rec, _ = esDo(t, h, http.MethodHead, "/es/books/_doc/fox", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("exists deleted: %d", rec.Code)
	}

	// An expired document is gone, whatever the TTL reaper has done yet.
	if _, _, err := s.addDocument("books", "old", "en", nil, "x", 1, true); err != nil {
		t.Fatal(err)
	}
	expireDoc(t, s, "books", "old")
	if rec, _ = esDo(t, h, http.MethodGet, "/es/books/_doc/old", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("expired: %d", rec.Code)
	}
}

func TestESDocumentRefusals(t *testing.T) {
	s, h := esTestServer(t)
	for _, c := range []struct{ method, path, body, typ string }{
		{http.MethodPut, "/es/books/_doc/a?if_seq_no=1", `{}`, "parsing_exception"},
		{http.MethodDelete, "/es/books/_doc/a?version=2", ``, "parsing_exception"},
		{http.MethodPut, "/es/books/_doc/a", `[]`, "parsing_exception"},
		{http.MethodPut, "/es/books/_doc/a", `{"content":1}`, "parsing_exception"},
		{http.MethodPut, "/es/books/_doc/a", `{"meta":{"nested":true}}`, "parsing_exception"},
	} {
		rec, out := esDo(t, h, c.method, c.path, c.body)
		if rec.Code != http.StatusBadRequest || esErrType(out) != c.typ {
			t.Errorf("%s %s %s: %d %v", c.method, c.path, c.body, rec.Code, out)
		}
	}

	if err := s.SchemaManager.Set("strict", `{"required":["author"]}`); err != nil {
		t.Fatal(err)
	}
	if rec, out := esDo(t, h, http.MethodPut, "/es/strict/_doc/a", `{"content":"x"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("schema: %d %v", rec.Code, out)
	}

	if err := s.CollectionManager.Set("full", &CollectionConfig{Quota: &CollectionQuota{MaxDocuments: 1}}); err != nil {
		t.Fatal(err)
	}
	esDo(t, h, http.MethodPut, "/es/full/_doc/a", `{"content":"one"}`)
	if rec, out := esDo(t, h, http.MethodPut, "/es/full/_doc/b", `{"content":"two"}`); rec.Code != http.StatusInsufficientStorage || esErrType(out) != "quota_exceeded_exception" {
		t.Fatalf("quota: %d %v", rec.Code, out)
	}

	addTestDoc(t, s, "pack", "a", "en", "x", nil)
	if err := s.CollectionManager.put("pack", &CollectionConfig{ReadOnly: true}); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ method, path, body string }{
		{http.MethodPut, "/es/pack/_doc/b", `{"content":"x"}`},
		{http.MethodDelete, "/es/pack/_doc/a", ``},
	} {
		if rec, out := esDo(t, h, c.method, c.path, c.body); rec.Code != http.StatusForbidden || esErrType(out) != "cluster_block_exception" {
			t.Fatalf("read-only %s: %d %v", c.method, rec.Code, out)
		}
	}

	s.Mode = ModeRead
	if rec, _ := esDo(t, h, http.MethodPut, "/es/books/_doc/a", `{"content":"x"}`); rec.Code != http.StatusForbidden {
		t.Fatalf("read-only server: %d", rec.Code)
	}
}

func TestESDocumentsNeedPermissions(t *testing.T) {
	s, _ := esTestServer(t)
	am, cleanup := authMwSetup(t)
	defer cleanup()
	s.AuthManager = am
	for name, h := range map[string]func(w http.ResponseWriter, r *http.Request){
		"index":   func(w http.ResponseWriter, r *http.Request) { s.esIndexDoc(w, r, "books", "a", false) },
		"get":     func(w http.ResponseWriter, r *http.Request) { s.esGetDoc(w, r, "books", "a") },
		"delete":  func(w http.ResponseWriter, r *http.Request) { s.esDeleteDoc(w, r, "books", "a") },
		"exists":  func(w http.ResponseWriter, r *http.Request) { s.esIndexExists(w, r, "books") },
		"refresh": func(w http.ResponseWriter, r *http.Request) { s.esRefresh(w, r, "books") },
	} {
		rec := httptest.NewRecorder()
		h(rec, httptest.NewRequest(http.MethodPut, "/es/books/_doc/a", strings.NewReader(`{}`)))
		if rec.Code != http.StatusForbidden {
			t.Errorf("%s without credentials: %d", name, rec.Code)
		}
	}
}

func TestESGetReportsStorageErrors(t *testing.T) {
	s, h := esTestServer(t)
	addTestDoc(t, s, "books", "a", "en", "x", nil)
	corruptDoc(t, s, "books", "a")
	if rec, out := esDo(t, h, http.MethodGet, "/es/books/_doc/a", ""); rec.Code != http.StatusBadRequest || out["error"] == nil {
		t.Fatalf("an unreadable document: %d %v", rec.Code, out)
	}
}

// esPutRaw overwrites a stored document's bytes.
func esPutRaw(t *testing.T, s *Server, coll, key string, buf []byte) {
	t.Helper()
	if err := s.DBUpdateC(coll, func(tx *bolt.Tx) error {
		return tx.Bucket([]byte("docs")).Put(storage.DocKey(coll, genID(coll, key, "en")), buf)
	}); err != nil {
		t.Fatal(err)
	}
}

// expireDoc moves a document's expiry into the past.
func expireDoc(t *testing.T, s *Server, coll, key string) {
	t.Helper()
	doc, err := s.loadDocByRef(coll, key, "en")
	if err != nil {
		t.Fatal(err)
	}
	doc.ExpiresAt = 1
	buf, err := marshalAndEncrypt(doc, coll)
	if err != nil {
		t.Fatal(err)
	}
	esPutRaw(t, s, coll, key, buf)
}

// corruptDoc replaces a document's bytes with ones that do not decode.
func corruptDoc(t *testing.T, s *Server, coll, key string) {
	t.Helper()
	esPutRaw(t, s, coll, key, []byte{0xff, 'n', 'o', 't'})
}
