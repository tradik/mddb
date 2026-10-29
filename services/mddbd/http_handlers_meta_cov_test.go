package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"mddb/internal/storage"

	json "mddb/internal/jsonx"

	bolt "go.etcd.io/bbolt"
)

// covGWithClaims attaches JWT claims to a request, as the auth middleware does.
func covGWithClaims(r *http.Request, claims *JWTClaims) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), authContextKey, claims))
}

func TestCovGMetaHandlersRefuseWithoutPermission(t *testing.T) {
	s := covGServer(t)
	addTestDoc(t, s, "blog", "a", "en", "x", nil)
	covGEnableAuth(t, s)
	for name, rec := range map[string]*httptest.ResponseRecorder{
		"update":   covGRaw(s.handleUpdate, http.MethodPatch, "/", `{"collection":"blog","key":"a","lang":"en","contentMd":"y"}`),
		"docMeta":  covGRaw(s.handleDocMeta, http.MethodGet, "/?collection=blog&key=a", ""),
		"metaKeys": covGRaw(s.handleMetaKeys, http.MethodGet, "/?collection=blog", ""),
		"stats":    covGRaw(s.handleStats, http.MethodGet, "/", ""),
	} {
		if rec.Code != http.StatusForbidden {
			t.Errorf("%s: status %d, want 403", name, rec.Code)
		}
	}
}

func TestCovGUpdateRejectsMalformedBody(t *testing.T) {
	s := covGServer(t)
	covGWantCode(t, covGRaw(s.handleUpdate, http.MethodPatch, "/", "{"), http.StatusBadRequest)
}

func TestCovGDocMetaEdgeCases(t *testing.T) {
	s := covGServer(t)
	d := addTestDoc(t, s, "blog", "a", "en", "x", map[string][]string{"tag": {"go"}})
	get := func() *httptest.ResponseRecorder {
		return covGRaw(s.handleDocMeta, http.MethodGet, "/?collection=blog&key=a", "")
	}

	// A document with a TTL reports when it expires.
	future := time.Now().Add(time.Hour).Unix()
	covGPutDoc(t, s, "blog", &storage.Doc{ID: d.ID, Key: "a", Lang: "en", Meta: d.Meta, ExpiresAt: future})
	rec := get()
	covGWantCode(t, rec, http.StatusOK)
	var out map[string]any
	covGDecode(t, rec, &out)
	if int64(out["expiresAt"].(float64)) != future {
		t.Errorf("expiresAt = %v, want %d", out["expiresAt"], future)
	}
	if _, has := out["contentMd"]; has {
		t.Error("doc-meta must not return content")
	}

	// Expired: gone.
	covGPutDoc(t, s, "blog", &storage.Doc{ID: d.ID, Key: "a", Lang: "en", ExpiresAt: time.Now().Add(-time.Hour).Unix()})
	covGWantCode(t, get(), http.StatusNotFound)

	// Corrupt: a decode error, not a 404.
	covGPutRaw(t, s, "blog", d.ID, []byte{0xff, 0xfe})
	covGWantCode(t, get(), http.StatusBadRequest)

	// The by-key entry outlived its document.
	if err := s.DB.Update(func(tx *bolt.Tx) error {
		return tx.Bucket([]byte("docs")).Delete(storage.DocKey("blog", d.ID))
	}); err != nil {
		t.Fatal(err)
	}
	covGWantCode(t, get(), http.StatusNotFound)
}

func TestCovGMetaKeysSkipsMalformedEntriesAndMissingBucket(t *testing.T) {
	s := covGServer(t)
	addTestDoc(t, s, "blog", "a", "en", "x", map[string][]string{"tag": {"go", "db"}})
	addTestDoc(t, s, "blog", "b", "en", "x", map[string][]string{"tag": {"go"}})
	if err := s.DB.Update(func(tx *bolt.Tx) error {
		return tx.Bucket([]byte("idxmeta")).Put([]byte("meta|blog|keyonly"), []byte("1"))
	}); err != nil {
		t.Fatal(err)
	}
	rec := covGRaw(s.handleMetaKeys, http.MethodGet, "/?collection=blog", "")
	var out struct{ Meta map[string][]string }
	covGDecode(t, rec, &out)
	if len(out.Meta) != 1 || len(out.Meta["tag"]) != 2 {
		t.Errorf("meta keys = %+v; the malformed key must be skipped and values deduplicated", out.Meta)
	}

	if err := s.DB.Update(func(tx *bolt.Tx) error { return tx.DeleteBucket([]byte("idxmeta")) }); err != nil {
		t.Fatal(err)
	}
	rec = covGRaw(s.handleMetaKeys, http.MethodGet, "/?collection=blog", "")
	if strings.TrimSpace(rec.Body.String()) != `{"meta":{}}` {
		t.Errorf("no index, no keys: %s", rec.Body.String())
	}
}

func TestCovGStatsTenantScopeAndConfig(t *testing.T) {
	s := covGServer(t)
	mine := "acme" + TenantSeparator + "docs"
	addTestDoc(t, s, mine, "a", "en", "x", nil)
	addTestDoc(t, s, "other", "b", "en", "y", nil)
	covGSetConfig(t, s, mine, &CollectionConfig{Description: "acme docs", Icon: "book", Color: "#123456"})

	req := covGWithClaims(httptest.NewRequest(http.MethodGet, "/", nil), &JWTClaims{Username: "u", Tenant: "acme"})
	rec := httptest.NewRecorder()
	s.handleStats(rec, req)
	covGWantCode(t, rec, http.StatusOK)
	var out struct {
		Collections []struct {
			Name, Description, Icon, Color string
			DocumentCount                  int
		}
		TotalDocuments int
		Indexing       *json.RawMessage
	}
	covGDecode(t, rec, &out)
	if len(out.Collections) != 1 || out.Collections[0].Name != mine {
		t.Fatalf("a tenant must see only its own collections: %+v", out.Collections)
	}
	c := out.Collections[0]
	if c.Description != "acme docs" || c.Icon != "book" || c.Color != "#123456" || out.TotalDocuments != 1 {
		t.Errorf("collection stats = %+v total %d", c, out.TotalDocuments)
	}
	if out.Indexing != nil {
		t.Error("server-wide indexing status must be hidden from tenants")
	}
}
