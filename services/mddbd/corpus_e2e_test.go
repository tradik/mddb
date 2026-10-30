package main

import (
	"bytes"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"mddb/internal/embedding"
	"mddb/internal/fts"
	"mddb/internal/geo"
	json "mddb/internal/jsonx"
)

// AGT-005 acceptance: a pack built on one server and installed on another
// answers hybrid search exactly as the original does, without re-embedding
// or reindexing — in both storage layouts.

var corpusLayouts = []string{"single", "per-collection"}

// corpusServer is a server with full-text search, geo indexes and the
// lexical embedding provider of dims dimensions, wired as main wires it.
func corpusServer(t *testing.T, dims int) *Server {
	t.Helper()
	s, cleanup := newHandlerTestServer(t)
	t.Cleanup(func() {
		s.closeCollectionFiles()
		cleanup()
		_ = os.RemoveAll(collectionDir(s.Path))
	})
	reg := fts.NewLangRegistry("en")
	fts.RegisterDefaultLanguages(reg)
	s.FTSIndex.SetStemmer(fts.NewPorterStemmer())
	s.FTSIndex.SetLangRegistry(reg)
	s.GeoIndex, s.GeoHashIndex, s.GeoStore = geo.NewGeoIndex(), geo.NewGeoHashIndex(), geo.NewGeoStore(s.DB)
	if err := s.GeoStore.EnsureBucket(); err != nil {
		t.Fatal(err)
	}
	s.GeoIndex.SetReady()
	s.GeoHashIndex.SetReady()
	s.wireRouters()
	s.VectorIndex.SetReady()
	s.Embedding = embedding.NewLexicalProvider(dims)
	s.startEmbeddingWorker(s.Embedding)
	t.Cleanup(s.EmbeddingWorker.Stop)
	return s
}

// seedCorpus writes the "docs" collection the packs are built from.
func seedCorpus(t *testing.T, s *Server) {
	t.Helper()
	for key, text := range map[string]string{
		"replication": "# Replication\n\nA follower replica copies every write from the leader.",
		"backup":      "# Backup\n\nBackups are written to a tarball and restored with one command.",
		"tls":         "# TLS\n\nServe the API over HTTPS with your own certificate and key.",
	} {
		var meta map[string][]string
		if key == "tls" {
			meta = map[string][]string{"geo_lat": {"52.52"}, "geo_lng": {"13.405"}}
		}
		doc := addTestDoc(t, s, "docs", key, "en", text, meta)
		waitForEmbedding(t, s, "docs", doc.ID)
	}
}

// buildPack asks s for the pack of collection over HTTP.
func buildPack(t *testing.T, s *Server, collection string) []byte {
	t.Helper()
	rec := doRequest(t, s.handleCorpusBuild, corpusBuildRequest{Collection: collection, Version: "1.0",
		License: "CC-BY-4.0", Sources: []corpusSource{{Repo: "https://example.org/docs", Commit: "abc123", License: "CC-BY-4.0"}}})
	if rec.Code != http.StatusOK {
		t.Fatalf("build: %d %s", rec.Code, rec.Body.String())
	}
	if cd := rec.Header().Get("Content-Disposition"); cd != `attachment; filename="`+collection+`-1.0.mddbpack"` {
		t.Fatalf("Content-Disposition %q", cd)
	}
	return rec.Body.Bytes()
}

// installPack uploads pack to s as a multipart form.
func installPack(t *testing.T, s *Server, pack []byte) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	_ = mw.WriteField("note", "fields before the pack are skipped")
	fw, err := mw.CreateFormFile(corpusUploadField, "docs.mddbpack")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = fw.Write(pack)
	_ = mw.Close()
	req := httptest.NewRequest(http.MethodPost, "/v1/corpus/install", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rec := httptest.NewRecorder()
	s.handleCorpusInstall(rec, req)
	return rec
}

// hybridAnswer is a hybrid search's results as comparable text.
func hybridAnswer(t *testing.T, s *Server, query string) string {
	t.Helper()
	rec := doRequest(t, s.handleHybridSearch, map[string]any{"collection": "docs", "query": query, "topK": 3})
	if rec.Code != http.StatusOK {
		t.Fatalf("hybrid search: %d %s", rec.Code, rec.Body.String())
	}
	var resp HybridSearchResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	out := ""
	for _, r := range resp.Results {
		out += fmt.Sprintf("%s fts=%.6f vec=%.6f combined=%.6f\n", r.Document.Key, r.FTSScore, r.VectorScore, r.CombinedScore)
	}
	return out
}

func TestCorpusPackAnswersLikeItsSource(t *testing.T) {
	for _, layout := range corpusLayouts {
		t.Run(layout, func(t *testing.T) {
			t.Setenv("MDDB_STORAGE_LAYOUT", layout)
			t.Setenv("MDDB_EMBEDDING_PROVIDER", embedding.ProviderLexical) // the manifest names the provider
			t.Setenv("MDDB_EMBEDDING_DIMENSIONS", "256")
			src := corpusServer(t, 256)
			seedCorpus(t, src)
			pack := buildPack(t, src, "docs")

			dst := corpusServer(t, 256)
			assertPackInstalled(t, dst, pack)
			assertSameHybridAnswers(t, src, dst)
			if n := dst.GeoIndex.Len("docs"); n != 1 {
				t.Fatalf("geo index holds %d points of the pack, want 1", n)
			}
			wantHTTPCode(t, doRequest(t, dst.handleAdd, map[string]any{"collection": "docs", "key": "x", "lang": "en", "contentMd": "x"}), http.StatusForbidden)

			// Installed twice: the name is taken.
			wantHTTPCode(t, installPack(t, dst, pack), http.StatusConflict)
			assertPackListed(t, dst)
			assertPackRemovedAndReinstallable(t, dst, pack)
		})
	}
}

// assertPackInstalled installs pack on dst and checks the install result
// describes the "docs" pack: three documents, vectors on, its license.
func assertPackInstalled(t *testing.T, dst *Server, pack []byte) {
	t.Helper()
	rec := installPack(t, dst, pack)
	if rec.Code != http.StatusOK {
		t.Fatalf("install: %d %s", rec.Code, rec.Body.String())
	}
	var res corpusInstallResult
	_ = json.Unmarshal(rec.Body.Bytes(), &res)
	if res.Name != "docs" || res.DocCount != 3 || res.VectorsDisabled || res.License != "CC-BY-4.0" {
		t.Fatalf("install result %+v", res)
	}
}

// assertSameHybridAnswers checks the installed pack answers hybrid searches
// exactly like its source.
func assertSameHybridAnswers(t *testing.T, src, dst *Server) {
	t.Helper()
	for _, q := range []string{"follower replica leader", "certificate https", "tarball restore"} {
		want, got := hybridAnswer(t, src, q), hybridAnswer(t, dst, q)
		if want == "" || got != want {
			t.Fatalf("hybrid %q differs after install:\nsource\n%s\ninstalled\n%s", q, want, got)
		}
	}
}

// assertPackListed checks GET /v1/corpus lists the one installed pack.
func assertPackListed(t *testing.T, dst *Server) {
	t.Helper()
	list := httptest.NewRecorder()
	dst.handleCorpusList(list, httptest.NewRequest(http.MethodGet, "/v1/corpus", nil))
	if !bytes.Contains(list.Body.Bytes(), []byte(`"name":"docs"`)) || !bytes.Contains(list.Body.Bytes(), []byte(`"total":1`)) {
		t.Fatalf("list: %s", list.Body.String())
	}
}

// assertPackRemovedAndReinstallable removes the pack and checks it is gone,
// writable again, and installable again.
func assertPackRemovedAndReinstallable(t *testing.T, dst *Server, pack []byte) {
	t.Helper()
	wantHTTPCode(t, doRequest(t, dst.handleCorpusRemove, map[string]any{"name": "docs"}), http.StatusOK)
	if dst.collectionHasData("docs") || dst.collectionReadOnly("docs") {
		t.Fatal("the removed pack left data or its read-only flag")
	}
	if hits, _ := dst.FTSIndex.Search("docs", "replica", 10); len(hits) != 0 {
		t.Fatalf("full-text search still finds %d documents of the removed pack", len(hits))
	}
	wantHTTPCode(t, installPack(t, dst, pack), http.StatusOK)
}

func TestCorpusPackFromAnotherModelSearchesFullTextOnly(t *testing.T) {
	for _, layout := range corpusLayouts {
		t.Run(layout, func(t *testing.T) {
			t.Setenv("MDDB_STORAGE_LAYOUT", layout)
			src := corpusServer(t, 256)
			seedCorpus(t, src)
			pack := buildPack(t, src, "docs")

			dst := corpusServer(t, 512)
			rec := installPack(t, dst, pack)
			var res corpusInstallResult
			_ = json.Unmarshal(rec.Body.Bytes(), &res)
			if rec.Code != http.StatusOK || !res.VectorsDisabled || len(res.Warnings) != 1 {
				t.Fatalf("install: %d %s", rec.Code, rec.Body.String())
			}
			if n := dst.VectorIndex.CollectionSize("docs"); n != 0 {
				t.Fatalf("%d vectors of another model were loaded", n)
			}
			if got := hybridAnswer(t, dst, "follower replica leader"); !bytes.HasPrefix([]byte(got), []byte("replication ")) {
				t.Fatalf("full-text half did not answer: %q", got)
			}
			// A restart skips them too.
			if _, loaded := dst.loadCollectionVectors("docs", newVectorLoadState(map[string]int{"docs": 3})); loaded {
				t.Fatal("the startup load loaded disabled vectors")
			}
		})
	}
}

func wantHTTPCode(t *testing.T, rec *httptest.ResponseRecorder, code int) {
	t.Helper()
	if rec.Code != code {
		t.Fatalf("status %d, want %d: %s", rec.Code, code, rec.Body.String())
	}
}
