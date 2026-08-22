package main

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TEST-002. The upload pipeline sat at 6% coverage while handling every file
// format MDDB accepts — including three container formats parsed by hand. These
// test the behaviour a caller sees: what key a file lands under, whether its
// text survives conversion, and what happens when the file is not what its
// extension claims.

// uploadFile posts one file through the real multipart handler.
func uploadFile(t *testing.T, srv *Server, filename string, content []byte, fields map[string]string) *httptest.ResponseRecorder {
	t.Helper()

	var body bytes.Buffer
	w := multipart.NewWriter(&body)

	for k, v := range fields {
		if err := w.WriteField(k, v); err != nil {
			t.Fatal(err)
		}
	}
	part, err := w.CreateFormFile("file", filename)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/upload", &body)
	req.Header.Set("Content-Type", w.FormDataContentType())

	rec := httptest.NewRecorder()
	srv.handleUpload(rec, req)
	return rec
}

func uploadServer(t *testing.T) (*Server, func()) {
	t.Helper()
	srv, cleanup := newTestServerForBatch(t)
	srv.CollectionManager = NewCollectionManager(srv.DB)
	if err := srv.CollectionManager.EnsureBucket(); err != nil {
		cleanup()
		t.Fatal(err)
	}
	return srv, cleanup
}

func decodeUpload(t *testing.T, rec *httptest.ResponseRecorder) UploadResponse {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("upload failed with %d: %s", rec.Code, rec.Body.String())
	}
	var out UploadResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("response is not an upload result: %v (%s)", err, rec.Body.String())
	}
	return out
}

func TestUploadMarkdownIsStoredAsIs(t *testing.T) {
	srv, cleanup := uploadServer(t)
	defer cleanup()

	content := "# Title\n\nSome **markdown** content."
	got := decodeUpload(t, uploadFile(t, srv, "notes.md", []byte(content),
		map[string]string{"collection": "docs", "lang": "en"}))

	if got.Converted {
		t.Error("markdown was reported as converted; it is stored as-is")
	}
	if got.Key != "notes" {
		t.Errorf("key = %q, want notes (extension stripped)", got.Key)
	}
	if got.Doc.ContentMD != content {
		t.Errorf("markdown was altered on the way in:\n want %q\n got  %q", content, got.Doc.ContentMD)
	}

	// And it is actually retrievable, not just echoed back.
	stored, err := srv.loadDocByRef("docs", "notes", "en")
	if err != nil {
		t.Fatalf("the uploaded document cannot be read back: %v", err)
	}
	if stored.ContentMD != content {
		t.Errorf("stored content differs from what was uploaded")
	}
}

func TestUploadHTMLIsConvertedToMarkdown(t *testing.T) {
	srv, cleanup := uploadServer(t)
	defer cleanup()

	got := decodeUpload(t, uploadFile(t, srv, "page.html",
		[]byte(`<h1>Heading</h1><p>Body with <strong>bold</strong>.</p>`),
		map[string]string{"collection": "docs", "lang": "en"}))

	if !got.Converted {
		t.Error("HTML was not reported as converted")
	}
	if got.Format != "html" {
		t.Errorf("format = %q, want html", got.Format)
	}
	if !strings.Contains(got.Doc.ContentMD, "Heading") || !strings.Contains(got.Doc.ContentMD, "bold") {
		t.Errorf("text was lost in conversion: %q", got.Doc.ContentMD)
	}
	if strings.Contains(got.Doc.ContentMD, "<h1>") {
		t.Errorf("markup survived conversion: %q", got.Doc.ContentMD)
	}
}

// `.htm` and `.html` are the same format; treating them differently would put
// two spellings of one file type on different paths.
func TestUploadTreatsHtmAndHtmlAlike(t *testing.T) {
	srv, cleanup := uploadServer(t)
	defer cleanup()

	body := []byte("<h2>Same</h2>")
	a := decodeUpload(t, uploadFile(t, srv, "a.htm", body,
		map[string]string{"collection": "docs", "lang": "en"}))
	b := decodeUpload(t, uploadFile(t, srv, "b.html", body,
		map[string]string{"collection": "docs", "lang": "en"}))

	if a.Format != b.Format {
		t.Errorf(".htm reported as %q but .html as %q", a.Format, b.Format)
	}
	if a.Doc.ContentMD != b.Doc.ContentMD {
		t.Errorf("the same markup converted differently by extension")
	}
}

func TestUploadWrapsTextualFormatsInACodeBlock(t *testing.T) {
	srv, cleanup := uploadServer(t)
	defer cleanup()

	for _, ext := range []string{"yaml", "yml", "log"} {
		got := decodeUpload(t, uploadFile(t, srv, "conf."+ext, []byte("key: value"),
			map[string]string{"collection": "docs", "lang": "en"}))

		if !strings.HasPrefix(got.Doc.ContentMD, "```") {
			t.Errorf(".%s was not wrapped in a code block: %q", ext, got.Doc.ContentMD)
		}
		if !strings.Contains(got.Doc.ContentMD, "key: value") {
			t.Errorf(".%s lost its content: %q", ext, got.Doc.ContentMD)
		}
	}
}

func TestUploadConvertsDocx(t *testing.T) {
	srv, cleanup := uploadServer(t)
	defer cleanup()

	docx := zipBytes(t, map[string]string{
		"word/document.xml": `<w:document><w:body>
<w:p><w:r><w:t>Deployment guide</w:t></w:r></w:p>
<w:p><w:r><w:t>Run the migration first.</w:t></w:r></w:p>
</w:body></w:document>`,
	})

	got := decodeUpload(t, uploadFile(t, srv, "guide.docx", docx,
		map[string]string{"collection": "docs", "lang": "en"}))

	if !got.Converted {
		t.Error("docx was not reported as converted")
	}
	for _, want := range []string{"Deployment guide", "Run the migration first."} {
		if !strings.Contains(got.Doc.ContentMD, want) {
			t.Errorf("docx lost %q:\n%s", want, got.Doc.ContentMD)
		}
	}
}

func TestUploadConvertsOdt(t *testing.T) {
	srv, cleanup := uploadServer(t)
	defer cleanup()

	odt := zipBytes(t, map[string]string{
		"content.xml": `<office:text><text:p>Runbook body</text:p></office:text>`,
	})

	got := decodeUpload(t, uploadFile(t, srv, "runbook.odt", odt,
		map[string]string{"collection": "docs", "lang": "en"}))

	if !strings.Contains(got.Doc.ContentMD, "Runbook body") {
		t.Errorf("odt lost its text: %q", got.Doc.ContentMD)
	}
}

// A file whose bytes do not match its extension is the common case for a
// corrupt or mislabelled upload. It must fail with a message about the file,
// not with a panic or a silently empty document.
func TestUploadRejectsAContainerThatIsNotOne(t *testing.T) {
	srv, cleanup := uploadServer(t)
	defer cleanup()

	for _, ext := range []string{"docx", "odt", "pdf"} {
		rec := uploadFile(t, srv, "broken."+ext, []byte("this is not a "+ext),
			map[string]string{"collection": "docs", "lang": "en"})

		if rec.Code == http.StatusOK {
			t.Errorf(".%s: a file that is not a %s was accepted: %s", ext, ext, rec.Body.String())
			continue
		}
		if !strings.Contains(strings.ToLower(rec.Body.String()), ext) {
			t.Errorf(".%s: the error does not mention the format: %s", ext, rec.Body.String())
		}
	}
}

func TestUploadRejectsAnUnsupportedFormat(t *testing.T) {
	srv, cleanup := uploadServer(t)
	defer cleanup()

	rec := uploadFile(t, srv, "photo.jpeg", []byte{0xff, 0xd8, 0xff},
		map[string]string{"collection": "docs", "lang": "en"})

	if rec.Code == http.StatusOK {
		t.Fatalf("an unsupported format was accepted: %s", rec.Body.String())
	}
	// The message must name what is supported, or the caller has to guess.
	if !strings.Contains(rec.Body.String(), "supported") {
		t.Errorf("the error does not say what is supported: %s", rec.Body.String())
	}
}

func TestUploadRequiresCollectionAndLang(t *testing.T) {
	srv, cleanup := uploadServer(t)
	defer cleanup()

	cases := []map[string]string{
		{"lang": "en"},
		{"collection": "docs"},
		{},
	}
	for _, fields := range cases {
		rec := uploadFile(t, srv, "a.md", []byte("x"), fields)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("fields %v gave %d, want 400", fields, rec.Code)
		}
	}
}

func TestUploadRejectsNonPost(t *testing.T) {
	srv, cleanup := uploadServer(t)
	defer cleanup()

	req := httptest.NewRequest(http.MethodGet, "/v1/upload", nil)
	rec := httptest.NewRecorder()
	srv.handleUpload(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET gave %d, want 405", rec.Code)
	}
}

func TestUploadHonoursAnExplicitKey(t *testing.T) {
	srv, cleanup := uploadServer(t)
	defer cleanup()

	got := decodeUpload(t, uploadFile(t, srv, "some-file-name.md", []byte("content"),
		map[string]string{"collection": "docs", "lang": "en", "key": "chosen/key"}))

	if got.Key != "chosen/key" {
		t.Errorf("key = %q, want the explicit chosen/key", got.Key)
	}
}

func TestUploadAttachesSuppliedMetadata(t *testing.T) {
	srv, cleanup := uploadServer(t)
	defer cleanup()

	got := decodeUpload(t, uploadFile(t, srv, "a.md", []byte("content"), map[string]string{
		"collection": "docs", "lang": "en",
		"meta": `{"tag":["one","two"],"author":["me"]}`,
	}))

	if tags := got.Doc.Meta["tag"]; len(tags) != 2 || tags[0] != "one" {
		t.Errorf("metadata was lost: %v", got.Doc.Meta)
	}
}

func TestUploadRejectsMalformedMetadata(t *testing.T) {
	srv, cleanup := uploadServer(t)
	defer cleanup()

	rec := uploadFile(t, srv, "a.md", []byte("content"), map[string]string{
		"collection": "docs", "lang": "en", "meta": `{not json`,
	})
	if rec.Code != http.StatusBadRequest {
		t.Errorf("malformed meta gave %d, want 400: %s", rec.Code, rec.Body.String())
	}
}

// RAG-004 made text-only parsing reachable from the upload form.
func TestUploadFastProfileUsesTextOnlyParsing(t *testing.T) {
	srv, cleanup := uploadServer(t)
	defer cleanup()

	html := []byte(`<h1>Heading</h1><p>Body text.</p>`)

	full := decodeUpload(t, uploadFile(t, srv, "a.html", html,
		map[string]string{"collection": "docs", "lang": "en"}))
	fast := decodeUpload(t, uploadFile(t, srv, "b.html", html,
		map[string]string{"collection": "docs", "lang": "en", "profile": "fast"}))

	// Both keep the words; only the default rebuilds Markdown structure.
	for _, got := range []UploadResponse{full, fast} {
		if !strings.Contains(got.Doc.ContentMD, "Heading") || !strings.Contains(got.Doc.ContentMD, "Body text.") {
			t.Errorf("text was lost: %q", got.Doc.ContentMD)
		}
	}
	if !strings.Contains(full.Doc.ContentMD, "#") {
		t.Errorf("the default path did not produce a heading: %q", full.Doc.ContentMD)
	}
	if strings.Contains(fast.Doc.ContentMD, "#") {
		t.Errorf("text-only produced Markdown structure: %q", fast.Doc.ContentMD)
	}
}

func TestUploadRejectsAnUnknownProfile(t *testing.T) {
	srv, cleanup := uploadServer(t)
	defer cleanup()

	rec := uploadFile(t, srv, "a.md", []byte("x"),
		map[string]string{"collection": "docs", "lang": "en", "profile": "quick"})

	if rec.Code != http.StatusBadRequest {
		t.Errorf("an unknown profile gave %d, want 400: %s", rec.Code, rec.Body.String())
	}
}

// A file over the limit must be refused rather than read into memory.
func TestUploadEnforcesMaxSize(t *testing.T) {
	srv, cleanup := uploadServer(t)
	defer cleanup()

	rec := uploadFile(t, srv, "big.md", bytes.Repeat([]byte("x"), 4096),
		map[string]string{"collection": "docs", "lang": "en", "maxSize": "512"})

	if rec.Code == http.StatusOK {
		t.Errorf("a file over maxSize was accepted: %s", rec.Body.String())
	}
}

// A filename with nothing usable in it cannot become a key, and guessing one
// would put the document somewhere the caller cannot find it.
func TestUploadRefusesAnUnusableFilename(t *testing.T) {
	srv, cleanup := uploadServer(t)
	defer cleanup()

	rec := uploadFile(t, srv, "...", []byte("content"),
		map[string]string{"collection": "docs", "lang": "en"})

	if rec.Code == http.StatusOK {
		t.Errorf("a file with no derivable key was accepted: %s", rec.Body.String())
	}
}

func TestDeriveKeyFromFilenameShapes(t *testing.T) {
	cases := map[string]string{
		"README.md":         "readme",
		"My Document.docx":  "my-document",
		"path/to/notes.txt": "notes",
		"UPPER.MD":          "upper",
		"weird  spacing.md": "weird--spacing",
		// Path operators are not names: a consumer writing one file per
		// document key would resolve them against its output directory.
		"...":          "",
		"..":           "",
		".":            "",
		"no-extension": "no-extension",
	}
	for in, want := range cases {
		if got := deriveKeyFromFilename(in); got != want {
			t.Errorf("deriveKeyFromFilename(%q) = %q, want %q", in, got, want)
		}
	}
}

// zipBytes builds an in-memory zip, which is what docx and odt actually are.
func zipBytes(t *testing.T, entries map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range entries {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fmt.Fprint(w, content); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}
