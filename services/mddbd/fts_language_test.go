package main

import (
	"mddb/internal/cache"
	"mddb/internal/fts"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	json "mddb/internal/jsonx"

	bolt "go.etcd.io/bbolt"
)

func newTestServerForLang(t *testing.T) (*Server, func()) {
	t.Helper()
	f, err := os.CreateTemp("", "fts_lang_test_*.db")
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
		DB:   db,
		Path: f.Name(),
		Mode: ModeRW,
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

	s.FTSIndex = fts.NewFTSIndex(db)
	if err := s.FTSIndex.EnsureBuckets(); err != nil {
		_ = db.Close()
		_ = os.Remove(f.Name())
		t.Fatal(err)
	}

	// Set up multi-language support
	langReg := fts.NewLangRegistry("en")
	fts.RegisterDefaultLanguages(langReg)
	s.FTSIndex.SetStemmer(fts.NewPorterStemmer())
	s.FTSIndex.SetLangRegistry(langReg)

	cleanup := func() {
		_ = db.Close()
		_ = os.Remove(f.Name())
	}
	return s, cleanup
}

// --- fts.LangRegistry tests ---

// --- fts.Stemmer interface tests ---

// --- Polish stemmer tests ---

// --- Polish stop words tests ---

// --- German stemming tests ---

// --- French stemming tests ---

// --- Spanish stemming tests ---

// --- Russian stemming tests ---

// --- TokenizeLang tests ---

// --- IndexWithLang + Search round-trip ---

// --- IndexPositionsWithLang ---

// --- IndexFieldsWithLang ---

// --- resolveLang ---

// --- Stop words per language ---

// --- FTSLanguages handler ---

// --- StopWordManager.ListLang tests ---

// --- HTTP handler test for stopwords?lang= ---

func TestHTTPStopWordsList_WithLang(t *testing.T) {
	s, cleanup := newTestServerForLang(t)
	defer cleanup()

	s.StopWordManager = fts.NewStopWordManager(s.DB)
	_ = s.StopWordManager.EnsureBucket()
	_ = s.StopWordManager.LoadAll()
	s.StopWordManager.SetLangRegistry(s.FTSIndex.LangRegistry())

	handler := http.HandlerFunc(s.handleStopWords)

	// Test with lang=pl
	req := httptest.NewRequest("GET", "/v1/stopwords?collection=test&lang=pl", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var resp StopWordListResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	if resp.Lang != "pl" {
		t.Errorf("expected lang 'pl', got %q", resp.Lang)
	}
	if resp.Defaults == 0 {
		t.Errorf("expected non-zero Polish defaults, got %d", resp.Defaults)
	}

	// Test without lang (should default to "en")
	req2 := httptest.NewRequest("GET", "/v1/stopwords?collection=test", nil)
	rr2 := httptest.NewRecorder()
	handler.ServeHTTP(rr2, req2)

	var resp2 StopWordListResponse
	_ = json.Unmarshal(rr2.Body.Bytes(), &resp2)
	if resp2.Lang != "en" {
		t.Errorf("expected default lang 'en', got %q", resp2.Lang)
	}
	// Black-box: English defaults must exist and differ from the Polish set,
	// proving per-language stop-word resolution rather than asserting against
	// the (now package-internal) default maps.
	if resp2.Defaults == 0 {
		t.Errorf("expected non-zero English defaults, got %d", resp2.Defaults)
	}
	if resp2.Defaults == resp.Defaults {
		t.Errorf("expected English (%d) and Polish (%d) default counts to differ", resp2.Defaults, resp.Defaults)
	}
}

// --- Helpers ---

func openTestDB(t *testing.T) *bolt.DB {
	t.Helper()
	f, err := os.CreateTemp("", "fts_test_*.db")
	if err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	db, err := bolt.Open(f.Name(), 0600, nil)
	if err != nil {
		_ = os.Remove(f.Name())
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close(); _ = os.Remove(f.Name()) })
	return db
}
