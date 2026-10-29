package main

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"mddb/internal/binlog"
	"mddb/internal/storage"

	bolt "go.etcd.io/bbolt"
)

type aliasErrReader struct{}

func (aliasErrReader) Read([]byte) (int, error) { return 0, errors.New("read failed") }

func TestAlias_StorageAndPermissions(t *testing.T) {
	s, cleanup := aliasServer(t)
	defer cleanup()
	// Binlog and FollowDB are wired like the collection manager's.
	bl, err := binlog.NewBinlog(filepath.Join(t.TempDir(), "bl.db"), binlog.BinlogConfig{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = bl.Close() }()
	s.Aliases.SetBinlog(bl)
	s.Aliases.FollowDB(func() *bolt.DB { return s.DB })
	if err := s.SetAlias("b", "docs-v1"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetAlias("a", "docs-v2"); err != nil {
		t.Fatal(err)
	}
	if l := s.Aliases.List(); len(l) != 2 || l[0].Alias != "a" {
		t.Fatalf("sorted list: %+v", l)
	}
	if err := s.DeleteAlias("b"); err != nil {
		t.Fatal(err)
	}
	if bl.CurrentLSN() < 3 {
		t.Fatalf("binlog entries: %d", bl.CurrentLSN())
	}
	// A configured collection without documents is still a collection.
	if err := s.CollectionManager.Set("empty", &CollectionConfig{}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetAlias("empty", "docs-v1"); err == nil {
		t.Fatal("alias over a configured collection")
	}

	// Storage failures surface, and the in-memory map keeps its state.
	closed, _ := bolt.Open(filepath.Join(t.TempDir(), "c.db"), 0o600, nil)
	_ = closed.Close()
	broken := &AliasManager{db: closed, m: map[string]string{"x": "y"}}
	if broken.LoadAll() == nil || broken.set("z", "docs-v1", func(string) bool { return false }) == nil || broken.remove("x") == nil {
		t.Fatal("closed database")
	}
	if broken.Resolve("x") != "y" {
		t.Fatal("failed remove kept the alias")
	}

	// Permissions: moving or deleting an alias needs write on its collections.
	t.Setenv("MDDB_AUTH_ENABLED", "true")
	am, amCleanup := authMwSetup(t)
	defer amCleanup()
	s.AuthManager = am
	for _, c := range []struct{ method, path, body string }{
		{http.MethodPut, "/v1/aliases/a", `{"collection":"docs-v1"}`},
		{http.MethodDelete, "/v1/aliases/a", ""},
	} {
		if rec := aliasREST(s, c.method, c.path, c.body); rec.Code != http.StatusForbidden {
			t.Fatalf("%s anonymous: %d", c.method, rec.Code)
		}
	}
	if rec := aliasREST(s, http.MethodGet, "/v1/aliases", ""); strings.Contains(rec.Body.String(), `"a"`) {
		t.Fatalf("anonymous list shows aliases: %s", rec.Body.String())
	}
}

func TestAlias_PutMoveNeedsTheOldTarget(t *testing.T) {
	s, cleanup := aliasServer(t)
	defer cleanup()
	if err := s.SetAlias("docs", "docs-v1"); err != nil {
		t.Fatal(err)
	}
	s.Aliases.m["broken"] = "docs-v1"
	// Delete through the API, and a store failure on delete.
	if rec := aliasREST(s, http.MethodPut, "/v1/aliases/docs", `{"collection":"docs-v2"}`); rec.Code != 200 {
		t.Fatalf("move: %d %s", rec.Code, rec.Body.String())
	}
	closed, _ := bolt.Open(filepath.Join(t.TempDir(), "c.db"), 0o600, nil)
	_ = closed.Close()
	s.Aliases.db = closed
	if rec := aliasREST(s, http.MethodDelete, "/v1/aliases/broken", ""); rec.Code != 400 {
		t.Fatalf("store failure: %d", rec.Code)
	}
	if rec := aliasREST(s, http.MethodPut, "/v1/aliases/x", `{"collection":"docs-v1"}`); rec.Code != 400 {
		t.Fatalf("put store failure: %d", rec.Code)
	}
}

func TestAlias_BodyEdges(t *testing.T) {
	s, cleanup := aliasServer(t)
	defer cleanup()
	if err := s.SetAlias("docs", "docs-v2"); err != nil {
		t.Fatal(err)
	}
	body := func(raw string) string {
		r := httptest.NewRequest(http.MethodPost, "/v1/search", strings.NewReader(raw))
		s.resolveAliasBody(r)
		b := new(strings.Builder)
		_, _ = strings.NewReader("").WriteTo(b)
		buf := make([]byte, 1024)
		n, _ := r.Body.Read(buf)
		return string(buf[:n])
	}
	if got := body(`{"collection":"docs","n":1.5}`); !strings.Contains(got, `"docs-v2"`) || !strings.Contains(got, `1.5`) {
		t.Fatalf("rewrite: %s", got)
	}
	if got := body(`{"collection":"other"}`); got != `{"collection":"other"}` {
		t.Fatalf("untouched: %s", got)
	}
	if got := body(`{not json`); got != `{not json` {
		t.Fatalf("invalid json: %s", got)
	}
	r := httptest.NewRequest(http.MethodPost, "/v1/search", aliasErrReader{})
	s.resolveAliasBody(r)
	if n, _ := r.Body.Read(make([]byte, 8)); n != 0 {
		t.Fatal("unreadable body")
	}
}

func TestWhere_SkipsAndDocumentsWithoutMeta(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	addTestDoc(t, s, "c", "keep", "en", "x", map[string][]string{"kind": {"log"}})
	gone := addTestDoc(t, s, "c", "gone", "en", "x", map[string][]string{"kind": {"log"}})
	// The meta index still names a document whose record is gone.
	_ = s.DBUpdateC("c", func(tx *bolt.Tx) error { return tx.Bucket([]byte("docs")).Delete(storage.DocKey("c", gone.ID)) })
	res, err := s.RunWhere(WhereUpdate, WhereRequest{Collection: "c", FilterMeta: map[string][]string{"kind": {"log"}},
		Meta: map[string][]string{"a": {"1"}}})
	if err != nil || res.Matched != 2 || res.Affected != 1 {
		t.Fatalf("missing record: %v %+v", err, res)
	}
	// applyWhere on a document that has no metadata at all.
	doc, _ := s.loadDocByRef("c", "keep", "en")
	doc.Meta = nil
	if err := s.applyWhere(WhereUpdate, WhereRequest{Collection: "c", Meta: map[string][]string{"b": {"2"}}}, doc); err != nil {
		t.Fatal(err)
	}
	if d, _ := s.loadDocByRef("c", "keep", "en"); d.Meta["b"][0] != "2" {
		t.Fatalf("merge into nothing: %+v", d.Meta)
	}
}
