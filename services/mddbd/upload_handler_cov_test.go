package main

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mddb/internal/storage"

	bolt "go.etcd.io/bbolt"
)

// covJFile is one file part of a multipart upload.
type covJFile struct {
	name    string
	content []byte
}

// covJMultipart builds a multipart body from fields and files.
func covJMultipart(t *testing.T, fields map[string]string, files []covJFile) (*bytes.Buffer, string) {
	t.Helper()
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	for k, v := range fields {
		if err := w.WriteField(k, v); err != nil {
			t.Fatal(err)
		}
	}
	for _, f := range files {
		part, err := w.CreateFormFile("files[]", f.name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := part.Write(f.content); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return &body, w.FormDataContentType()
}

// covJUploadMany posts several files through the real handler.
func covJUploadMany(t *testing.T, s *Server, fields map[string]string, files []covJFile) *httptest.ResponseRecorder {
	t.Helper()
	body, ct := covJMultipart(t, fields, files)
	req := httptest.NewRequest(http.MethodPost, "/v1/upload", body)
	req.Header.Set("Content-Type", ct)
	rec := httptest.NewRecorder()
	s.handleUpload(rec, req)
	return rec
}

// covJFileHeader parses a one-file multipart form and returns its header,
// as the handler would see it. maxMemory 1 forces the file onto disk.
func covJFileHeader(t *testing.T, name string, content []byte, maxMemory int64) *multipart.FileHeader {
	t.Helper()
	body, ct := covJMultipart(t, nil, []covJFile{{name, content}})
	req := httptest.NewRequest(http.MethodPost, "/", body)
	req.Header.Set("Content-Type", ct)
	if err := req.ParseMultipartForm(maxMemory); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = req.MultipartForm.RemoveAll() })
	return req.MultipartForm.File["files[]"][0]
}

// covJDocExists reports whether coll/key/lang is stored.
func covJDocExists(t *testing.T, s *Server, coll, key, lang string) bool {
	t.Helper()
	found := false
	_ = s.DBViewC(coll, func(tx *bolt.Tx) error {
		found = tx.Bucket([]byte("bykey")).Get(storage.ByKeyKey(coll, key, lang)) != nil
		return nil
	})
	return found
}

var covJFields = map[string]string{"collection": "docs", "lang": "en"}

// The body cap must be in place before the first form read; it used to be
// applied after r.FormValue had already consumed the whole body.
func TestCovJUploadBodyLimitAppliesBeforeTheFormIsRead(t *testing.T) {
	s, cleanup := uploadServer(t)
	defer cleanup()
	old := uploadBodyLimit
	uploadBodyLimit = 1024
	defer func() { uploadBodyLimit = old }()

	rec := uploadFile(t, s, "big.md", bytes.Repeat([]byte("x"), 8192), covJFields)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("over-limit body: %d, want 400 (%s)", rec.Code, rec.Body.String())
	}
	if covJDocExists(t, s, "docs", "big", "en") {
		t.Error("an over-limit upload was stored")
	}
}

func TestCovJUploadNotMultipart(t *testing.T) {
	s, cleanup := uploadServer(t)
	defer cleanup()
	req := httptest.NewRequest(http.MethodPost, "/v1/upload", strings.NewReader("plain"))
	req.Header.Set("Content-Type", "text/plain")
	rec := httptest.NewRecorder()
	s.handleUpload(rec, req)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "parse multipart") {
		t.Errorf("non-multipart body: %d %s", rec.Code, rec.Body.String())
	}
}

func TestCovJUploadForbiddenWithoutClaims(t *testing.T) {
	t.Setenv("MDDB_AUTH_ENABLED", "true")
	s, cleanup := uploadServer(t)
	defer cleanup()
	am, amCleanup := authMwSetup(t)
	defer amCleanup()
	s.AuthManager = am

	rec := uploadFile(t, s, "a.md", []byte("x"), covJFields)
	if rec.Code != http.StatusForbidden {
		t.Errorf("no claims: %d, want 403", rec.Code)
	}
	if covJDocExists(t, s, "docs", "a", "en") {
		t.Error("a forbidden upload was stored")
	}
}

// An oversized maxSize is clamped to the hard cap rather than refused, and a
// TTL field sets the document's expiry; an unparsable TTL is ignored.
func TestCovJUploadMaxSizeClampAndTTL(t *testing.T) {
	s, cleanup := uploadServer(t)
	defer cleanup()

	got := decodeUpload(t, uploadFile(t, s, "a.md", []byte("body"),
		map[string]string{"collection": "docs", "lang": "en", "maxSize": "999999999999", "ttl": "3600"}))
	if got.Doc.ExpiresAt == 0 {
		t.Error("ttl=3600 left no expiry")
	}
	got = decodeUpload(t, uploadFile(t, s, "b.md", []byte("body"),
		map[string]string{"collection": "docs", "lang": "en", "ttl": "soon"}))
	if got.Doc.ExpiresAt != 0 {
		t.Errorf("an unparsable ttl set an expiry: %d", got.Doc.ExpiresAt)
	}
}

func TestCovJUploadWithoutFiles(t *testing.T) {
	s, cleanup := uploadServer(t)
	defer cleanup()
	rec := covJUploadMany(t, s, covJFields, nil)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "no files uploaded") {
		t.Errorf("no files: %d %s", rec.Code, rec.Body.String())
	}
}

func TestCovJUploadManyFilesReportsEachOutcome(t *testing.T) {
	s, cleanup := uploadServer(t)
	defer cleanup()

	rec := covJUploadMany(t, s, covJFields, []covJFile{
		{"one.md", []byte("first")},
		{"two.txt", []byte("second")},
		{"three.exe", []byte{0x4d, 0x5a}},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("batch: %d %s", rec.Code, rec.Body.String())
	}
	var out UploadBatchResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Added != 2 || out.Failed != 1 || len(out.Results) != 2 {
		t.Errorf("batch counts: %+v", out)
	}
	if len(out.Errors) != 1 || !strings.HasPrefix(out.Errors[0], "three.exe: ") {
		t.Errorf("batch errors: %v", out.Errors)
	}
	if !covJDocExists(t, s, "docs", "one", "en") || !covJDocExists(t, s, "docs", "two", "en") {
		t.Error("batch documents not stored")
	}

	// All good: no errors field at all.
	rec = covJUploadMany(t, s, covJFields, []covJFile{{"x.md", []byte("x")}, {"y.md", []byte("y")}})
	if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), `"errors"`) {
		t.Errorf("clean batch: %d %s", rec.Code, rec.Body.String())
	}
}

// The upload form's collection goes through aliases like every JSON route
// (GO-044).
func TestCovJUploadResolvesAlias(t *testing.T) {
	s, cleanup := uploadServer(t)
	defer cleanup()
	s.Aliases = NewAliasManager(s.DB)
	if err := s.Aliases.LoadAll(); err != nil {
		t.Fatal(err)
	}
	if err := s.Aliases.set("current", "docs-v3", func(string) bool { return false }); err != nil {
		t.Fatal(err)
	}

	got := decodeUpload(t, uploadFile(t, s, "page.md", []byte("aliased"),
		map[string]string{"collection": "current", "lang": "en"}))
	if got.Doc.Key != "page" || !covJDocExists(t, s, "docs-v3", "page", "en") {
		t.Errorf("alias not resolved: %+v", got.Doc)
	}
	if covJDocExists(t, s, "current", "page", "en") {
		t.Error("document stored under the alias name")
	}
}

func TestCovJUploadConvertsRemainingFormats(t *testing.T) {
	s, cleanup := uploadServer(t)
	defer cleanup()

	tex := decodeUpload(t, uploadFile(t, s, "paper.tex", []byte(`\section{Intro}Hello tex`), covJFields))
	if !tex.Converted || tex.Format != "tex" || !strings.Contains(tex.Doc.ContentMD, "Intro") {
		t.Errorf("tex: %+v", tex)
	}

	pdf := decodeUpload(t, uploadFile(t, s, "doc.pdf",
		[]byte("%PDF-1.4\nstream\nBT (Hello covJ PDF) Tj ET\nendstream\n%%EOF"), covJFields))
	if !pdf.Converted || !strings.Contains(pdf.Doc.ContentMD, "Hello covJ PDF") {
		t.Errorf("pdf: %+v", pdf)
	}

	rtf := decodeUpload(t, uploadFile(t, s, "note.rtf", []byte(`{\rtf1\ansi Hello rtf}`), covJFields))
	if !rtf.Converted || !strings.Contains(rtf.Doc.ContentMD, "Hello rtf") {
		t.Errorf("rtf: %+v", rtf)
	}

	odt := zipBytes(t, map[string]string{"content.xml": `<office:text><text:p>Fast odt</text:p></office:text>`})
	got := decodeUpload(t, uploadFile(t, s, "fast.odt", odt,
		map[string]string{"collection": "docs", "lang": "en", "profile": "fast"}))
	if !strings.Contains(got.Doc.ContentMD, "Fast odt") || got.Doc.Meta["upload_converted"][0] != "true" {
		t.Errorf("odt text-only: %+v", got.Doc)
	}

	docx := zipBytes(t, map[string]string{"word/document.xml": `<w:document><w:body><w:p><w:r><w:t>Fast docx</w:t></w:r></w:p></w:body></w:document>`})
	got = decodeUpload(t, uploadFile(t, s, "fast.docx", docx,
		map[string]string{"collection": "docs", "lang": "en", "profile": "fast"}))
	if !strings.Contains(got.Doc.ContentMD, "Fast docx") || !got.Converted {
		t.Errorf("docx text-only: %+v", got.Doc)
	}

	rec := uploadFile(t, s, "broken.odt", []byte("not a zip"),
		map[string]string{"collection": "docs", "lang": "en", "profile": "fast"})
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "odt conversion") {
		t.Errorf("broken odt text-only: %d %s", rec.Code, rec.Body.String())
	}
}

// Front matter in a Markdown upload becomes metadata; a field the form also
// supplies keeps the form's value.
func TestCovJUploadFrontmatterMerge(t *testing.T) {
	s, cleanup := uploadServer(t)
	defer cleanup()

	md := []byte("---\ntitle: From file\nauthor: fm\n---\nBody text")

	only := decodeUpload(t, uploadFile(t, s, "a.md", md, covJFields))
	if only.Doc.ContentMD != "Body text" || only.Doc.Meta["title"][0] != "From file" {
		t.Errorf("frontmatter only: %q %+v", only.Doc.ContentMD, only.Doc.Meta)
	}

	merged := decodeUpload(t, uploadFile(t, s, "b.md", md,
		map[string]string{"collection": "docs", "lang": "en", "meta": `{"author":["form"]}`}))
	if merged.Doc.Meta["author"][0] != "form" || merged.Doc.Meta["title"][0] != "From file" {
		t.Errorf("merge: %+v", merged.Doc.Meta)
	}
}

func TestCovJUploadRefusedBySchemaAndByStorage(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	if err := s.SchemaManager.Set("strict", `{"required":["author"]}`); err != nil {
		t.Fatal(err)
	}
	rec := uploadFile(t, s, "a.md", []byte("x"), map[string]string{"collection": "strict", "lang": "en"})
	if rec.Code != http.StatusBadRequest || covJDocExists(t, s, "strict", "a", "en") {
		t.Errorf("schema violation: %d %s", rec.Code, rec.Body.String())
	}

	// Invalid UTF-8 passes conversion untouched and is refused by the store.
	rec = uploadFile(t, s, "bad.md", []byte{'o', 'k', 0xff, 0xfe}, covJFields)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "UTF-8") {
		t.Errorf("invalid utf-8: %d %s", rec.Code, rec.Body.String())
	}
	if covJDocExists(t, s, "docs", "bad", "en") {
		t.Error("invalid UTF-8 was stored")
	}
}

func TestCovJProcessUploadedFileOpenAndReadErrors(t *testing.T) {
	s, cleanup := uploadServer(t)
	defer cleanup()

	// A header with neither content nor a temp file cannot be opened.
	if _, err := s.processUploadedFile(&multipart.FileHeader{Filename: "a.md"}, "docs", "en", "", nil, 0, 100, false); err == nil ||
		!strings.Contains(err.Error(), "open file") {
		t.Errorf("open: %v", err)
	}

	// A temp file replaced by a directory opens but cannot be read.
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	fh := covJFileHeader(t, "b.md", []byte("on disk"), 1)
	entries, err := os.ReadDir(tmp)
	if err != nil || len(entries) != 1 {
		t.Fatalf("expected one multipart temp file in %s: %v %v", tmp, entries, err)
	}
	p := filepath.Join(tmp, entries[0].Name())
	if err := os.Remove(p); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(p, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := s.processUploadedFile(fh, "docs", "en", "", nil, 0, 100, false); err == nil ||
		!strings.Contains(err.Error(), "read file") {
		t.Errorf("read: %v", err)
	}
}

// The declared size is a hint; the bytes actually read are what is limited.
func TestCovJProcessUploadedFileChecksBytesRead(t *testing.T) {
	s, cleanup := uploadServer(t)
	defer cleanup()

	fh := covJFileHeader(t, "c.md", bytes.Repeat([]byte("y"), 64), 1<<20)
	fh.Size = 1
	if _, err := s.processUploadedFile(fh, "docs", "en", "", nil, 0, 16, false); err == nil ||
		!strings.Contains(err.Error(), "exceeds max size") {
		t.Errorf("understated size: %v", err)
	}
	if covJDocExists(t, s, "docs", "c", "en") {
		t.Error("an oversized file was stored")
	}
}
