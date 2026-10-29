package main

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"math"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mddb/internal/storage"
	"mddb/internal/temporal"

	json "mddb/internal/jsonx"

	bolt "go.etcd.io/bbolt"
)

// covGAnon is the request every handler below refuses once auth is on: no
// claims in its context.
func TestCovGHTTPHandlersRefuseWithoutPermission(t *testing.T) {
	s := covGServer(t)
	addTestDoc(t, s, "blog", "a", "en", "hello", nil)
	covGEnableAuth(t, s)

	for name, call := range map[string]func() *httptest.ResponseRecorder{
		"add": func() *httptest.ResponseRecorder {
			return doRequest(t, s.handleAdd, map[string]any{"collection": "blog", "key": "k", "lang": "en"})
		},
		"get": func() *httptest.ResponseRecorder {
			return doRequest(t, s.handleGet, map[string]any{"collection": "blog", "key": "a", "lang": "en"})
		},
		"search": func() *httptest.ResponseRecorder {
			return doRequest(t, s.handleSearch, map[string]any{"collection": "blog"})
		},
		"export": func() *httptest.ResponseRecorder {
			return doRequest(t, s.handleExport, map[string]any{"collection": "blog"})
		},
		"backup": func() *httptest.ResponseRecorder {
			return covGRaw(s.handleBackup, http.MethodPost, "/", "")
		},
		"restore": func() *httptest.ResponseRecorder {
			return doRequest(t, s.handleRestore, map[string]any{"from": "x.db"})
		},
		"truncate": func() *httptest.ResponseRecorder {
			return doRequest(t, s.handleTruncate, map[string]any{"collection": "blog"})
		},
		"checksum": func() *httptest.ResponseRecorder {
			return covGRaw(s.handleChecksum, http.MethodGet, "/?collection=blog", "")
		},
	} {
		if rec := call(); rec.Code != http.StatusForbidden {
			t.Errorf("%s: status %d, want 403 (%s)", name, rec.Code, rec.Body.String())
		}
	}
}

func TestCovGHTTPGetSkipsDanglingAndCorruptDocs(t *testing.T) {
	s := covGServer(t)
	d := addTestDoc(t, s, "blog", "a", "en", "hello", nil)

	// The by-key index points at a document that is gone.
	if err := s.DB.Update(func(tx *bolt.Tx) error {
		return tx.Bucket([]byte("docs")).Delete(storage.DocKey("blog", d.ID))
	}); err != nil {
		t.Fatal(err)
	}
	rec := doRequest(t, s.handleGet, map[string]any{"collection": "blog", "key": "a", "lang": "en"})
	covGWantCode(t, rec, http.StatusNotFound)
	if !strings.Contains(rec.Body.String(), "not found") {
		t.Errorf("dangling: %s", rec.Body.String())
	}

	// ... and at one that cannot be decoded.
	covGPutRaw(t, s, "blog", d.ID, []byte{0xff, 0xfe})
	rec = doRequest(t, s.handleGet, map[string]any{"collection": "blog", "key": "a", "lang": "en"})
	covGWantCode(t, rec, http.StatusBadRequest)
	if strings.Contains(rec.Body.String(), "not found") {
		t.Errorf("a corrupt document is a decode error, not a missing one: %s", rec.Body.String())
	}
}

func TestCovGHTTPGetRecordsAccessWithActor(t *testing.T) {
	s := covGServer(t)
	d := addTestDoc(t, s, "blog", "a", "en", "hello", nil)
	tm := temporal.NewTemporalManager(s.DB)
	if err := tm.EnsureBuckets(); err != nil {
		t.Fatal(err)
	}
	tm.Start()
	t.Cleanup(tm.Stop)
	s.TemporalManager = tm
	covGSetConfig(t, s, "blog", &CollectionConfig{TrackAccess: true})

	b, _ := json.Marshal(map[string]any{"collection": "blog", "key": "a", "lang": "en"})
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(b))
	req = req.WithContext(context.WithValue(req.Context(), authContextKey, &JWTClaims{Username: "alice"}))
	rec := httptest.NewRecorder()
	s.handleGet(rec, req)
	covGWantCode(t, rec, http.StatusOK)

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		events, err := tm.QueryRange("blog", d.ID, 0, math.MaxInt64, "", 10)
		if err == nil && len(events) > 0 {
			if events[0].Actor != "alice" {
				t.Errorf("actor = %q, want alice", events[0].Actor)
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("the access was never recorded")
}

func TestCovGHTTPSearchFilterSkipsBadEntries(t *testing.T) {
	s := covGServer(t)
	good := addTestDoc(t, s, "blog", "good", "en", "x", map[string][]string{"tag": {"go"}})
	// A meta-index entry whose document is gone is skipped.
	if err := s.DB.Update(func(tx *bolt.Tx) error {
		return tx.Bucket([]byte("idxmeta")).Put(append(storage.MetaKeyPrefix("blog", "tag", "go"), []byte("ghost")...), []byte("1"))
	}); err != nil {
		t.Fatal(err)
	}
	rec := doRequest(t, s.handleSearch, map[string]any{"collection": "blog", "filterMeta": map[string][]string{"tag": {"go"}}})
	covGWantCode(t, rec, http.StatusOK)
	var docs []storage.Doc
	covGDecode(t, rec, &docs)
	if len(docs) != 1 || docs[0].ID != good.ID {
		t.Errorf("filtered search = %+v", docs)
	}

	// A corrupt document fails the request, with or without a filter.
	covGPutRaw(t, s, "blog", good.ID, []byte{0xff, 0xfe})
	covGWantCode(t, doRequest(t, s.handleSearch, map[string]any{"collection": "blog",
		"filterMeta": map[string][]string{"tag": {"go"}}}), http.StatusBadRequest)
	covGWantCode(t, doRequest(t, s.handleSearch, map[string]any{"collection": "blog"}), http.StatusBadRequest)
}

func TestCovGHTTPExport(t *testing.T) {
	s := covGServer(t)
	addTestDoc(t, s, "blog", "a", "en", "hello", nil)
	covGWantCode(t, covGRaw(s.handleExport, http.MethodPost, "/", "{"), http.StatusBadRequest)

	// A client that disconnects mid-stream is logged, not fatal.
	fw := &covGFailWriter{}
	s.handleExport(fw, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"collection":"blog"}`)))
	if fw.header.Get("Content-Type") != "application/x-ndjson" {
		t.Errorf("ndjson content type = %q", fw.header.Get("Content-Type"))
	}

	// The store failing is reported before anything is streamed.
	_ = s.DB.Close()
	for _, format := range []string{"ndjson", "zip"} {
		rec := doRequest(t, s.handleExport, map[string]any{"collection": "blog", "format": format})
		covGWantCode(t, rec, http.StatusBadRequest)
	}
}

// covGAlphabet is what the incompressible export body is drawn from.
const covGAlphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789!#$%&()*+,-./:;<=>?@[]^_{}~"

// covGExportZipTo exports blog as a zip into a writer that always fails.
func covGExportZipTo(s *Server) *covGFailWriter {
	fw := &covGFailWriter{}
	s.handleExport(fw, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"collection":"blog","format":"zip"}`)))
	return fw
}

func TestCovGHTTPExportZipWriteFailures(t *testing.T) {
	// One large, incompressible entry fails while its content is written.
	s := covGServer(t)
	rng := rand.New(rand.NewSource(1)) // #nosec G404 -- test data, not security
	var big strings.Builder
	for big.Len() < 200_000 {
		big.WriteByte(covGAlphabet[rng.Intn(len(covGAlphabet))])
	}
	addTestDoc(t, s, "blog", "big", "en", big.String(), nil)
	if fw := covGExportZipTo(s); fw.header.Get("Content-Type") != "application/zip" {
		t.Errorf("content type = %q", fw.header.Get("Content-Type"))
	}

	// Many small entries fail when a new entry's header is flushed.
	s = covGServer(t)
	for i := 0; i < 300; i++ {
		addTestDoc(t, s, "blog", fmt.Sprintf("doc-%03d", i), "en", "tiny", nil)
	}
	if fw := covGExportZipTo(s); fw.header.Get("Content-Type") != "application/zip" {
		t.Errorf("content type = %q", fw.header.Get("Content-Type"))
	}

	// And a working writer gets a readable archive.
	rec := doRequest(t, s.handleExport, map[string]any{"collection": "blog", "format": "zip"})
	zr, err := zip.NewReader(bytes.NewReader(rec.Body.Bytes()), int64(rec.Body.Len()))
	if err != nil || len(zr.File) != 300 {
		t.Fatalf("zip: %v, %d entries", err, len(zr.File))
	}
}

func TestCovGHTTPBackupAndRestoreFailures(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MDDB_BACKUP_DIR", dir)
	s := covGServer(t)

	// A file that is not a database is refused before anything is replaced.
	if err := os.WriteFile(filepath.Join(dir, "junk.db"), []byte("not a bolt file"), 0o600); err != nil {
		t.Fatal(err)
	}
	rec := doRequest(t, s.handleRestore, map[string]any{"from": "junk.db"})
	covGWantCode(t, rec, http.StatusInternalServerError)
	if !strings.Contains(rec.Body.String(), "error") {
		t.Errorf("restore body: %s", rec.Body.String())
	}
	addTestDoc(t, s, "blog", "still", "en", "here", nil) // the live database is untouched

	_ = s.DB.Close()
	rec = covGRaw(s.handleBackup, http.MethodPost, "/?to=b.db", "")
	covGWantCode(t, rec, http.StatusInternalServerError)
}

func TestCovGHTTPTruncateFailures(t *testing.T) {
	s := covGServer(t)
	covGPutRaw(t, s, "blog", "corrupt", []byte{0xff, 0xfe})
	rec := doRequest(t, s.handleTruncate, map[string]any{"collection": "blog"})
	covGWantCode(t, rec, http.StatusBadRequest)
}

func TestCovGHTTPHealthWriteFailureAndCompliance(t *testing.T) {
	s := covGServer(t)
	fw := &covGFailWriter{}
	s.handleHealth(fw, httptest.NewRequest(http.MethodGet, "/health", nil))
	if fw.code != http.StatusOK {
		t.Errorf("health status = %d", fw.code)
	}

	t.Setenv("MDDB_PRODUCTION", "true")
	rec := covGRaw(s.handleComplianceStatus, http.MethodGet, "/", "")
	var out struct {
		Compliant    bool
		MissingCount int
		Missing      []struct{ EnvVar string }
	}
	covGDecode(t, rec, &out)
	if out.MissingCount != len(out.Missing) {
		t.Errorf("missingCount %d vs %d entries", out.MissingCount, len(out.Missing))
	}
	if out.Compliant != (out.MissingCount == 0) {
		t.Error("compliant must mean nothing is missing")
	}
}

func TestCovGHTTPChecksum(t *testing.T) {
	s := covGServer(t)
	addTestDoc(t, s, "blog", "a", "en", "Quartz jumbles vex fickle gnomes; sphinx of black onyx, judge my vow. Pack five dozen liquor jugs, then whiz by quick daft zebras.", nil)
	covGPutRaw(t, s, "blog", "short", []byte("tiny")) // a value under 64 bytes is hashed whole

	rec := covGRaw(s.handleChecksum, http.MethodGet, "/?collection=blog", "")
	covGWantCode(t, rec, http.StatusOK)
	var out struct {
		Checksum      string
		DocumentCount int
	}
	covGDecode(t, rec, &out)
	if out.DocumentCount != 2 || len(out.Checksum) != 8 {
		t.Errorf("checksum = %+v", out)
	}

	// Without a docs bucket there is nothing to count.
	if err := s.DB.Update(func(tx *bolt.Tx) error { return tx.DeleteBucket([]byte("docs")) }); err != nil {
		t.Fatal(err)
	}
	if sum, n := s.collectionChecksum("blog"); n != 0 || sum != "00000000" {
		t.Errorf("no bucket: %s %d", sum, n)
	}
}
