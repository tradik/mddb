package main

import (
	"bytes"
	"errors"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	json "mddb/internal/jsonx"
)

// FE-022: a pack of several collections — built, inspected, installed as the
// collections it was built from, and removed as a whole — in both layouts.

// seedNotes writes the "notes" collection, with a key "docs" uses too.
func seedNotes(t *testing.T, s *Server) {
	t.Helper()
	for key, text := range map[string]string{
		"backup":  "# Backup notes\n\nKeep three copies, one off site.",
		"meeting": "# Meeting\n\nThe replica question is settled.",
	} {
		doc := addTestDoc(t, s, "notes", key, "en", text, nil)
		waitForEmbedding(t, s, "notes", doc.ID)
	}
}

// multiBuild is the build of "kb": docs under the pack's licence, notes
// under its own.
var multiBuild = corpusBuildRequest{Name: "kb", Version: "2", Description: "two collections", License: "CC-BY-4.0",
	Collections: []corpusBuildCollection{{Name: "docs"},
		{Name: "notes", License: "MIT", Sources: []corpusSource{{Repo: "https://example.org/notes", License: "MIT"}}}}}

// buildMultiPack builds "kb" on s over HTTP.
func buildMultiPack(t *testing.T, s *Server) []byte {
	t.Helper()
	rec := doRequest(t, s.handleCorpusBuild, multiBuild)
	if rec.Code != http.StatusOK {
		t.Fatalf("build: %d %s", rec.Code, rec.Body.String())
	}
	if cd := rec.Header().Get("Content-Disposition"); cd != `attachment; filename="kb-2.mddbpack"` {
		t.Fatalf("Content-Disposition %q", cd)
	}
	return rec.Body.Bytes()
}

// uploadPack posts pack as the "pack" field to h.
func uploadPack(t *testing.T, h http.HandlerFunc, pack []byte) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, _ := mw.CreateFormFile(corpusUploadField, "kb.mddbpack")
	_, _ = fw.Write(pack)
	_ = mw.Close()
	req := httptest.NewRequest(http.MethodPost, "/v1/corpus/inspect", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rec := httptest.NewRecorder()
	h(rec, req)
	return rec
}

func TestCorpusPackOfSeveralCollections(t *testing.T) {
	for _, layout := range corpusLayouts {
		t.Run(layout, func(t *testing.T) {
			t.Setenv("MDDB_STORAGE_LAYOUT", layout)
			src := corpusServer(t, 256)
			seedCorpus(t, src)
			seedNotes(t, src)
			pack := buildMultiPack(t, src)

			dst := corpusServer(t, 256)
			assertInspected(t, dst, pack, 0)
			rec := installPack(t, dst, pack)
			var res corpusInstallResult
			_ = json.Unmarshal(rec.Body.Bytes(), &res)
			if rec.Code != http.StatusOK || res.Name != "kb" || res.DocCount != 5 || strings.Join(res.Collections, ",") != "docs,notes" {
				t.Fatalf("install: %d %s", rec.Code, rec.Body.String())
			}
			assertMultiInstalled(t, dst)
			assertInspected(t, dst, pack, 1)
			wantHTTPCode(t, installPack(t, dst, pack), http.StatusConflict)

			wantHTTPCode(t, doRequest(t, dst.handleCorpusRemove, map[string]any{"name": "kb"}), http.StatusOK)
			if dst.collectionHasData("docs") || dst.collectionHasData("notes") || len(dst.packCollections("kb")) != 0 {
				t.Fatal("the removed pack left a collection behind")
			}
		})
	}
}

// assertMultiInstalled checks both collections are there, read-only, with
// the key they share kept apart and their own licences recorded.
func assertMultiInstalled(t *testing.T, dst *Server) {
	t.Helper()
	for coll, want := range map[string]string{"docs": "tarball", "notes": "off site"} {
		rec := doRequest(t, dst.handleGet, map[string]any{"collection": coll, "key": "backup", "lang": "en"})
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), want) {
			t.Fatalf("%s/backup: %d %s", coll, rec.Code, rec.Body.String())
		}
		if !dst.collectionReadOnly(coll) {
			t.Fatalf("%s is writable", coll)
		}
	}
	docs, _ := dst.CollectionManager.Get("docs")
	notes, _ := dst.CollectionManager.Get("notes")
	if docs.Corpus.Name != "kb" || docs.Corpus.License != "CC-BY-4.0" || notes.Corpus.License != "MIT" ||
		notes.Corpus.PackLicense != "CC-BY-4.0" || len(notes.Corpus.Sources) != 1 || len(docs.Corpus.Sources) != 0 ||
		strings.Join(notes.Corpus.Collections, ",") != "docs,notes" || notes.Corpus.DocCount != 2 || notes.Corpus.Bytes == 0 || notes.Corpus.Description != "two collections" {
		t.Fatalf("configs: %+v / %+v", docs.Corpus, notes.Corpus)
	}
	if hits, _ := dst.FTSIndex.Search("notes", "replica", 10); len(hits) != 1 {
		t.Fatalf("full-text search finds %d notes", len(hits))
	}
}

// assertInspected inspects the first part of pack and wants n conflicts.
func assertInspected(t *testing.T, s *Server, pack []byte, n int) {
	t.Helper()
	rec := uploadPack(t, s.handleCorpusInspect, pack[:min(len(pack), 1<<20)])
	var res corpusInspectResult
	_ = json.Unmarshal(rec.Body.Bytes(), &res)
	if rec.Code != http.StatusOK || res.Manifest.Format != corpusFormatMulti || len(res.Manifest.Collections) != 2 || len(res.Conflicts) != n {
		t.Fatalf("inspect: %d %s", rec.Code, rec.Body.String())
	}
}

func TestCorpusMultiInstallUndoesAPartialInstall(t *testing.T) {
	t.Setenv("MDDB_STORAGE_LAYOUT", "per-collection")
	src := corpusServer(t, 256)
	seedCorpus(t, src)
	seedNotes(t, src)
	pack := buildMultiPack(t, src)
	dst := corpusServer(t, 512) // another model: the warning names the collection
	// A stray file where notes would go: docs attaches, notes does not.
	dir := dst.collectionStore().dir // opened first, so the stray file is not read as a collection
	if err := os.WriteFile(filepath.Join(dir, collectionFileName("notes")), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := dst.installCorpus(bytes.NewReader(pack)); err == nil {
		t.Fatal("installed over a stray file")
	}
	if dst.collectionHasData("docs") || len(dst.packCollections("kb")) != 0 {
		t.Fatal("the failed install left docs installed")
	}
	_ = os.Remove(filepath.Join(dir, collectionFileName("notes")))
	res, err := dst.installCorpus(bytes.NewReader(pack))
	if err != nil || len(res.Warnings) != 2 || !strings.HasPrefix(res.Warnings[1], "collection notes: ") {
		t.Fatalf("install: %+v, %v", res, err)
	}
	// The undo of a collection that cannot be deleted is logged, not fatal.
	_ = dst.collectionDB("docs").Close()
	dst.undoCorpusInstall([]string{"docs"})
}

func TestCorpusInspectRefuses(t *testing.T) {
	s := corpusServer(t, 256)
	wantHTTPCode(t, corpusCall(s.handleCorpusInspect, http.MethodGet, "", nil), http.StatusMethodNotAllowed)
	wantHTTPCode(t, corpusCall(s.handleCorpusInspect, http.MethodPost, "", nil), http.StatusBadRequest)
	wantHTTPCode(t, uploadPack(t, s.handleCorpusInspect, []byte("not a pack")), http.StatusBadRequest)
}

func TestCorpusBuildPlan(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string // "" plans; "format 1"/"format 2" checks the format
	}{
		{"single", `{"collection":"a","license":"MIT"}`, "format 1"},
		{"one in collections", `{"collections":["a"],"name":"a","license":"MIT"}`, "format 1"},
		{"one with its own licence", `{"collections":[{"name":"a","license":"MIT"}],"name":"p","license":"MIT"}`, "format 2"},
		{"several", `{"collections":["a",{"name":"b","license":"Apache-2.0"}],"name":"p","license":"MIT"}`, "format 2"},
		{"both", `{"collection":"a","collections":["b"],"license":"MIT"}`, "not both"},
		{"renamed single", `{"collections":["a"],"name":"p","license":"MIT"}`, "installs under the collection's name"},
		{"no pack name", `{"collections":["a","b"],"license":"MIT"}`, "needs a name of its own"},
		{"twice", `{"collections":["a","a"],"name":"p","license":"MIT"}`, "named twice"},
		{"bad collection name", `{"collections":["a","../b"],"name":"p","license":"MIT"}`, "invalid pack name"},
		{"no licence", `{"collections":["a","b"],"name":"p"}`, "needs a licence"},
		{"source without licence", `{"collections":["a",{"name":"b","sources":[{"repo":"r"}]}],"name":"p","license":"MIT"}`, "has no licence"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var req corpusBuildRequest
			if err := json.Unmarshal([]byte(tc.body), &req); err != nil {
				t.Fatal(err)
			}
			m, _, err := req.plan()
			switch tc.want {
			case "format 1", "format 2":
				if err != nil || (tc.want == "format 1") != (m.Format == corpusFormat) {
					t.Fatalf("plan = %+v, %v", m, err)
				}
			default:
				if !errors.Is(err, errCorpusInvalid) || !strings.Contains(err.Error(), tc.want) {
					t.Fatalf("plan err = %v, want %q", err, tc.want)
				}
			}
		})
	}
	var c corpusBuildCollection
	if err := json.Unmarshal([]byte(`{"name":1}`), &c); err == nil {
		t.Fatal("a number was taken as a name")
	}
}

func TestCorpusMultiBuildRefusals(t *testing.T) {
	t.Setenv("MDDB_STORAGE_LAYOUT", "single")
	s := corpusServer(t, 256)
	seedCorpus(t, s)
	_ = s.CollectionManager.Set("secret", &CollectionConfig{Encrypted: true})
	for coll, want := range map[string]error{"secret": errCorpusInvalid, "missing": errNotFound} {
		req := multiBuild
		req.Collections = []corpusBuildCollection{{Name: "docs"}, {Name: coll}}
		if b, err := s.prepareCorpus(req); !errors.Is(err, want) {
			if b != nil {
				b.close()
			}
			t.Fatalf("%s: err = %v, want %v", coll, err, want)
		}
	}
}
