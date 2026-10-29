package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"mddb/internal/storage"

	bolt "go.etcd.io/bbolt"
)

// covJRevisionList posts a revision-list request and decodes the answer.
func covJRevisionList(t *testing.T, s *Server, coll, key, lang string) (int, RevisionListResponse) {
	t.Helper()
	rec := doRequest(t, s.handleRevisions, RevisionListRequest{Collection: coll, Key: key, Lang: lang})
	var out RevisionListResponse
	if rec.Code == http.StatusOK {
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("decode revisions: %v (%s)", err, rec.Body.String())
		}
	}
	return rec.Code, out
}

// covJStoredDoc reads a document's current state straight from storage.
func covJStoredDoc(t *testing.T, s *Server, coll, key, lang string) *storage.Doc {
	t.Helper()
	var doc *storage.Doc
	err := s.DBViewC(coll, func(tx *bolt.Tx) error {
		v := tx.Bucket([]byte("docs")).Get(storage.DocKey(coll, genID(coll, key, lang)))
		if v == nil {
			return errDocNotFound
		}
		var err error
		doc, err = loadDoc(v)
		return err
	})
	if err != nil {
		t.Fatalf("read %s/%s/%s: %v", coll, key, lang, err)
	}
	return doc
}

// covJStoredContent is the stored document's Markdown.
func covJStoredContent(t *testing.T, s *Server, coll, key, lang string) string {
	t.Helper()
	return covJStoredDoc(t, s, coll, key, lang).ContentMD
}

// covJForbiddenServer is a handler server whose auth manager is on and whose
// requests carry no claims, so every permission check fails.
func covJForbiddenServer(t *testing.T) (*Server, func()) {
	t.Helper()
	t.Setenv("MDDB_AUTH_ENABLED", "true")
	s, cleanup := newHandlerTestServer(t)
	am, amCleanup := authMwSetup(t)
	s.AuthManager = am
	return s, func() { amCleanup(); cleanup() }
}

func TestCovJRevisionsRejectsBadRequests(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	for name, h := range map[string]http.HandlerFunc{"list": s.handleRevisions, "restore": s.handleRevisionRestore} {
		rec := httptest.NewRecorder()
		h(rec, httptest.NewRequest(http.MethodGet, "/", nil))
		if rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s GET: %d, want 405", name, rec.Code)
		}

		rec = httptest.NewRecorder()
		h(rec, httptest.NewRequest(http.MethodPost, "/", strings.NewReader("{not json")))
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s bad json: %d, want 400", name, rec.Code)
		}
	}

	rec := doRequest(t, s.handleRevisions, RevisionListRequest{Collection: "c", Key: "k"})
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "missing required fields") {
		t.Errorf("list without lang: %d %s", rec.Code, rec.Body.String())
	}
	// A timestamp of zero is no revision: restore needs all four fields.
	rec = doRequest(t, s.handleRevisionRestore, RevisionRestoreRequest{Collection: "c", Key: "k", Lang: "en"})
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "timestamp") {
		t.Errorf("restore without timestamp: %d %s", rec.Code, rec.Body.String())
	}
}

func TestCovJRevisionsForbiddenWithoutClaims(t *testing.T) {
	s, cleanup := covJForbiddenServer(t)
	defer cleanup()
	addTestDoc(t, s, "c", "k", "en", "v1", nil)

	rec := doRequest(t, s.handleRevisions, RevisionListRequest{Collection: "c", Key: "k", Lang: "en"})
	if rec.Code != http.StatusForbidden {
		t.Errorf("list: %d, want 403", rec.Code)
	}
	rec = doRequest(t, s.handleRevisionRestore, RevisionRestoreRequest{Collection: "c", Key: "k", Lang: "en", Timestamp: 1})
	if rec.Code != http.StatusForbidden {
		t.Errorf("restore: %d, want 403", rec.Code)
	}
	if got := covJStoredContent(t, s, "c", "k", "en"); got != "v1" {
		t.Errorf("a forbidden restore changed the document: %q", got)
	}
}

func TestCovJRevisionsListNewestFirstAndSkipsMalformedKeys(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	addTestDoc(t, s, "c", "k", "en", "first", map[string][]string{"v": {"1"}})
	addTestDoc(t, s, "c", "k", "en", "second", map[string][]string{"v": {"2"}})

	// Entries a scan of the prefix must step over rather than fail on: a key
	// ending in the separator, a non-numeric suffix and a value that is not a
	// document.
	prefix := storage.RevPrefix("c", genID("c", "k", "en"))
	err := s.DB.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte("rev"))
		for k, v := range map[string][]byte{
			string(prefix):                          []byte("x"),
			string(prefix) + "not-a-number":         []byte("x"),
			string(prefix) + "00000000000000000001": []byte("{broken"),
		} {
			if err := b.Put([]byte(k), v); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	code, out := covJRevisionList(t, s, "c", "k", "en")
	if code != http.StatusOK {
		t.Fatalf("list: %d", code)
	}
	if out.Total != 2 || len(out.Revisions) != 2 {
		t.Fatalf("want the 2 real revisions, got %d: %+v", out.Total, out.Revisions)
	}
	if out.Revisions[0].Timestamp <= out.Revisions[1].Timestamp {
		t.Errorf("not newest first: %d then %d", out.Revisions[0].Timestamp, out.Revisions[1].Timestamp)
	}
	if out.Revisions[0].ContentMD != "second" || out.Revisions[1].ContentMD != "first" {
		t.Errorf("revision contents: %q, %q", out.Revisions[0].ContentMD, out.Revisions[1].ContentMD)
	}
	if out.Revisions[1].Meta["v"][0] != "1" {
		t.Errorf("revision meta lost: %+v", out.Revisions[1].Meta)
	}
	if out.Collection != "c" || out.Key != "k" || out.Lang != "en" {
		t.Errorf("echoed identity: %+v", out)
	}
}

func TestCovJRevisionsListWithoutRevisionBucket(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	addTestDoc(t, s, "c", "k", "en", "v1", nil)
	if err := s.DB.Update(func(tx *bolt.Tx) error { return tx.DeleteBucket([]byte("rev")) }); err != nil {
		t.Fatal(err)
	}

	code, out := covJRevisionList(t, s, "c", "k", "en")
	if code != http.StatusOK || out.Total != 0 || len(out.Revisions) != 0 {
		t.Errorf("no bucket: %d %+v", code, out)
	}
}

func TestCovJRevisionsListStorageError(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	if err := s.DB.Close(); err != nil {
		t.Fatal(err)
	}
	if code, _ := covJRevisionList(t, s, "c", "k", "en"); code != http.StatusBadRequest {
		t.Errorf("closed database: %d, want 400", code)
	}
}

func TestCovJRevisionRestoreBringsBackOldContent(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	addTestDoc(t, s, "c", "k", "en", "original", map[string][]string{"v": {"1"}})
	addTestDoc(t, s, "c", "k", "en", "edited", map[string][]string{"v": {"2"}})

	_, list := covJRevisionList(t, s, "c", "k", "en")
	if len(list.Revisions) != 2 {
		t.Fatalf("want 2 revisions, got %+v", list)
	}
	oldest := list.Revisions[1]

	rec := doRequest(t, s.handleRevisionRestore, RevisionRestoreRequest{Collection: "c", Key: "k", Lang: "en", Timestamp: oldest.Timestamp})
	if rec.Code != http.StatusOK {
		t.Fatalf("restore: %d %s", rec.Code, rec.Body.String())
	}
	var doc storage.Doc
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	if doc.ContentMD != "original" || doc.Meta["v"][0] != "1" {
		t.Errorf("restored doc: %q %+v", doc.ContentMD, doc.Meta)
	}
	if got := covJStoredContent(t, s, "c", "k", "en"); got != "original" {
		t.Errorf("stored after restore: %q", got)
	}
	// The restore is itself a write and leaves a revision behind.
	if _, after := covJRevisionList(t, s, "c", "k", "en"); after.Total != 3 {
		t.Errorf("revisions after restore: %d, want 3", after.Total)
	}
}

func TestCovJRevisionRestoreMissingRevisionIs404(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	addTestDoc(t, s, "c", "k", "en", "only", nil)

	rec := doRequest(t, s.handleRevisionRestore, RevisionRestoreRequest{Collection: "c", Key: "k", Lang: "en", Timestamp: 42})
	if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), "not found") {
		t.Errorf("unknown timestamp: %d %s", rec.Code, rec.Body.String())
	}
	rec = doRequest(t, s.handleRevisionRestore, RevisionRestoreRequest{Collection: "c", Key: "absent", Lang: "en", Timestamp: 42})
	if rec.Code != http.StatusNotFound {
		t.Errorf("unknown document: %d, want 404", rec.Code)
	}
}

// A revision the collection's current schema rejects is refused, and the
// document keeps its present content.
func TestCovJRevisionRestoreRefusedBySchema(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	addTestDoc(t, s, "c", "k", "en", "untagged", nil)
	addTestDoc(t, s, "c", "k", "en", "tagged", map[string][]string{"author": {"ann"}})
	_, list := covJRevisionList(t, s, "c", "k", "en")
	if len(list.Revisions) != 2 {
		t.Fatalf("want 2 revisions, got %+v", list)
	}
	if err := s.SchemaManager.Set("c", `{"required":["author"]}`); err != nil {
		t.Fatal(err)
	}

	rec := doRequest(t, s.handleRevisionRestore, RevisionRestoreRequest{Collection: "c", Key: "k", Lang: "en", Timestamp: list.Revisions[1].Timestamp})
	if rec.Code != http.StatusBadRequest {
		t.Errorf("schema-violating restore: %d %s", rec.Code, rec.Body.String())
	}
	if got := covJStoredContent(t, s, "c", "k", "en"); got != "tagged" {
		t.Errorf("document changed by a refused restore: %q", got)
	}
}
