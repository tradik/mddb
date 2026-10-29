package main

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// covJWikiBz2 is a bzip2-compressed dump with one article ("Bz Page",
// "compressed body"). Go has no bzip2 writer, so it was made with bzip2(1).
const covJWikiBz2 = "425a683931415926535901550b3300000c1f804000c085100040003eafddf020007412a6a64f5068c834d326412a9e432834003434acd8067b11d510ae4a051c873e24778a48524d3ce83908b4010a787a88fa1566c4d952248996342c6644431583130faca35d0935b31ea77715511c38e4438bfe2ee48a70a12002aa1666"

// covJWikiPage renders one <page> element.
func covJWikiPage(title string, ns int, id int64, text, extra string) string {
	return fmt.Sprintf(`<page><title>%s</title><ns>%d</ns><id>%d</id>%s<revision><id>%d</id><text>%s</text></revision></page>`,
		title, ns, id, extra, id*10, text)
}

// covJWikiDump wraps pages in a MediaWiki export root.
func covJWikiDump(pages ...string) []byte {
	return []byte(`<?xml version="1.0" encoding="utf-8"?><mediawiki>` + strings.Join(pages, "") + `</mediawiki>`)
}

// covJWikiRaw posts a raw (octet-stream style) dump with query parameters.
func covJWikiRaw(s *Server, query, contentType string, body []byte) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/v1/import-wiki?"+query, bytes.NewReader(body))
	req.Header.Set("Content-Type", contentType)
	rec := httptest.NewRecorder()
	s.handleWikiImport(rec, req)
	return rec
}

// covJWikiForm posts a multipart dump; an empty filename sends no file part.
func covJWikiForm(t *testing.T, s *Server, fields map[string]string, filename string, dump []byte) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	for k, v := range fields {
		if err := w.WriteField(k, v); err != nil {
			t.Fatal(err)
		}
	}
	if filename != "" {
		part, err := w.CreateFormFile("file", filename)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := part.Write(dump); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/import-wiki", &body)
	req.Header.Set("Content-Type", w.FormDataContentType())
	rec := httptest.NewRecorder()
	s.handleWikiImport(rec, req)
	return rec
}

// covJWikiResult decodes a 200 import answer.
func covJWikiResult(t *testing.T, rec *httptest.ResponseRecorder) WikiImportResponse {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("import: %d %s", rec.Code, rec.Body.String())
	}
	var out WikiImportResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v (%s)", err, rec.Body.String())
	}
	return out
}

func TestCovJWikiImportRejectsBadRequests(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	rec := httptest.NewRecorder()
	s.handleWikiImport(rec, httptest.NewRequest(http.MethodGet, "/v1/import-wiki", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET: %d", rec.Code)
	}

	dump := covJWikiDump(covJWikiPage("A", 0, 1, "text", ""))
	cases := []struct {
		name, query, ct, want string
	}{
		{"json", "collection=w&lang=en", "application/json", "use multipart/form-data"},
		{"multipart without boundary", "", "multipart/form-data", "parse multipart"},
		{"no collection", "lang=en", "application/octet-stream", "missing collection"},
		{"no lang", "collection=w", "application/octet-stream", "missing lang"},
		{"unknown profile", "collection=w&lang=en&profile=turbo", "application/octet-stream", "unknown ingest profile"},
	}
	for _, c := range cases {
		rec := covJWikiRaw(s, c.query, c.ct, dump)
		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), c.want) {
			t.Errorf("%s: %d %s", c.name, rec.Code, rec.Body.String())
		}
	}

	rec = covJWikiForm(t, s, map[string]string{"collection": "w", "lang": "en"}, "", nil)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "missing file field") {
		t.Errorf("no file part: %d %s", rec.Code, rec.Body.String())
	}
}

func TestCovJWikiImportForbiddenWithoutClaims(t *testing.T) {
	s, cleanup := covJForbiddenServer(t)
	defer cleanup()

	rec := covJWikiRaw(s, "collection=w&lang=en", "application/octet-stream",
		covJWikiDump(covJWikiPage("A", 0, 1, "text", "")))
	if rec.Code != http.StatusForbidden {
		t.Errorf("no claims: %d, want 403", rec.Code)
	}
}

// One multipart import through every per-page branch: allowed and filtered
// namespaces, redirects, empty pages, an undecodable page and a title that
// yields no key, in batches of two.
func TestCovJWikiImportMultipartFiltersAndCounts(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	s.Aliases = NewAliasManager(s.DB)
	if err := s.Aliases.LoadAll(); err != nil {
		t.Fatal(err)
	}
	if err := s.Aliases.set("wiki", "wiki-v2", func(string) bool { return false }); err != nil {
		t.Fatal(err)
	}

	dump := covJWikiDump(
		covJWikiPage("Alpha Page", 0, 1, "'''Alpha''' body", ""),
		`<page><title>Broken</title><ns>zero</ns><id>2</id></page>`,
		covJWikiPage("Talk:Alpha", 1, 3, "talk", ""),
		covJWikiPage("Old Name", 0, 4, "#REDIRECT [[Alpha Page]]", `<redirect title="Alpha Page"/>`),
		covJWikiPage("Blank", 0, 5, "   ", ""),
		`<page><title>Portal Beta</title><ns>4</ns><id>6</id><revision><id>60</id><timestamp>2026-01-02T03:04:05Z</timestamp>`+
			`<contributor><ip>10.0.0.1</ip></contributor><text>beta body</text></revision></page>`,
		`<page><title>Gamma</title><ns>0</ns><id>7</id><revision><id>70</id>`+
			`<contributor><username>Editor</username></contributor><text>gamma body</text></revision></page>`,
		covJWikiPage("...", 0, 8, "keyless body", ""),
	)
	rec := covJWikiForm(t, s, map[string]string{
		"collection": "wiki", "lang": "en", "namespaces": "0, 4, x", "skipRedirects": "true",
		"batchSize": "2", "maxPages": "lots", "profile": "fast",
	}, "dump.xml", dump)
	out := covJWikiResult(t, rec)

	// Imported: Alpha Page, Portal Beta, Gamma. Skipped: talk, redirect,
	// blank. Failed: the undecodable page and the keyless one.
	if out.Imported != 3 || out.Skipped != 3 || out.Failed != 2 {
		t.Errorf("counts: %+v", out)
	}
	if out.Collection != "wiki-v2" || out.Profile != IngestProfileFast {
		t.Errorf("collection/profile: %+v", out)
	}
	joined := strings.Join(out.Errors, "\n")
	if !strings.Contains(joined, "decode page") || !strings.Contains(joined, "missing key or lang") {
		t.Errorf("errors: %v", out.Errors)
	}

	alpha := covJStoredDoc(t, s, "wiki-v2", "alpha-page", "en")
	if !strings.Contains(alpha.ContentMD, "Alpha") || alpha.Meta["source"][0] != "wikipedia" || alpha.Meta["wiki_id"][0] != "1" {
		t.Errorf("alpha: %q %+v", alpha.ContentMD, alpha.Meta)
	}
	beta := covJStoredDoc(t, s, "wiki-v2", "portal-beta", "en")
	if beta.Meta["wiki_contributor"][0] != "10.0.0.1" || beta.Meta["wiki_timestamp"][0] != "2026-01-02T03:04:05Z" || beta.Meta["wiki_ns"][0] != "4" {
		t.Errorf("beta meta: %+v", beta.Meta)
	}
	if gamma := covJStoredDoc(t, s, "wiki-v2", "gamma", "en"); gamma.Meta["wiki_contributor"][0] != "Editor" {
		t.Errorf("gamma meta: %+v", gamma.Meta)
	}
}

// Redirects are kept unless asked otherwise, and carry their target.
func TestCovJWikiImportKeepsRedirects(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	rec := covJWikiRaw(s, "collection=w&lang=en&skipFts=true", "application/octet-stream", covJWikiDump(
		covJWikiPage("Old Name", 0, 4, "#REDIRECT [[Alpha]]", `<redirect title="Alpha"/>`)))
	out := covJWikiResult(t, rec)
	if out.Imported != 1 || out.Errors != nil {
		t.Fatalf("redirect import: %+v", out)
	}
	if d := covJStoredDoc(t, s, "w", "old-name", "en"); d.Meta["wiki_redirect"][0] != "Alpha" {
		t.Errorf("redirect meta: %+v", d.Meta)
	}
}

// A batch in which every page fails is reported as failed with the batch's
// own error.
func TestCovJWikiImportWholeBatchFails(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	out := covJWikiResult(t, covJWikiRaw(s, "collection=w&lang=en", "application/octet-stream",
		covJWikiDump(covJWikiPage("...", 0, 1, "body", ""))))
	if out.Imported != 0 || out.Failed != 1 || len(out.Errors) != 1 || !strings.Contains(out.Errors[0], "all documents failed") {
		t.Errorf("all failed: %+v", out)
	}
}

// maxPages stops the import once that many pages are written; the batch size
// decides when that is known.
func TestCovJWikiImportStopsAtMaxPages(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	out := covJWikiResult(t, covJWikiRaw(s, "collection=w&lang=en&namespaces=0,x&maxPages=1&batchSize=1", "application/octet-stream",
		covJWikiDump(covJWikiPage("One", 0, 1, "a", ""), covJWikiPage("Two", 0, 2, "b", ""), covJWikiPage("Three", 0, 3, "c", ""))))
	if out.Imported != 1 {
		t.Errorf("maxPages=1 imported %d", out.Imported)
	}

	// Unparsable numbers fall back to the defaults: everything is imported.
	out = covJWikiResult(t, covJWikiRaw(s, "collection=w2&lang=en&maxPages=many&batchSize=big", "application/octet-stream",
		covJWikiDump(covJWikiPage("One", 0, 1, "a", ""), covJWikiPage("Two", 0, 2, "b", ""))))
	if out.Imported != 2 {
		t.Errorf("defaults imported %d", out.Imported)
	}
}

func TestCovJWikiImportBzip2(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	bz, err := hex.DecodeString(covJWikiBz2)
	if err != nil {
		t.Fatal(err)
	}

	// Detected from the content type, from the filename parameter and from
	// the uploaded file's name.
	for name, rec := range map[string]*httptest.ResponseRecorder{
		"content type": covJWikiRaw(s, "collection=a&lang=en", "application/x-bzip2", bz),
		"filename":     covJWikiRaw(s, "collection=b&lang=en&filename=enwiki.xml.bz2", "application/octet-stream", bz),
		"multipart":    covJWikiForm(t, s, map[string]string{"collection": "c", "lang": "en", "maxPages": "5"}, "dump.XML.BZ2", bz),
	} {
		if out := covJWikiResult(t, rec); out.Imported != 1 {
			t.Errorf("%s: %+v", name, out)
		}
	}
	if d := covJStoredDoc(t, s, "a", "bz-page", "en"); !strings.Contains(d.ContentMD, "compressed body") {
		t.Errorf("bz2 content: %q", d.ContentMD)
	}
}

// SEC-006: a dump past the decompressed budget answers 413 with the partial
// counts, not a 200 that looks like a clean import.
func TestCovJWikiImportDecompressedLimitIs413(t *testing.T) {
	t.Setenv("MDDB_WIKI_MAX_DECOMPRESSED_BYTES", "64")
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	rec := covJWikiRaw(s, "collection=w&lang=en", "application/octet-stream",
		covJWikiDump(covJWikiPage("A", 0, 1, strings.Repeat("long text ", 50), "")))
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("over budget: %d %s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	_, hasImported := body["imported"]
	if !strings.Contains(body["error"].(string), "MDDB_WIKI_MAX_DECOMPRESSED_BYTES") || !hasImported {
		t.Errorf("413 body: %v", body)
	}
}

// A truncated dump keeps what was read before the break and reports the
// parse error; the error list is capped at ten.
func TestCovJWikiImportParseErrorsAndErrorCap(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	out := covJWikiResult(t, covJWikiRaw(s, "collection=w&lang=en", "application/octet-stream",
		[]byte(`<mediawiki>`+covJWikiPage("Kept", 0, 1, "kept body", "")+`<page><title>Cut`)))
	// The cut page fails to decode, then the stream itself ends mid-element.
	if out.Imported != 1 || out.Failed != 1 || len(out.Errors) != 2 || !strings.HasPrefix(out.Errors[1], "xml parse:") {
		t.Errorf("truncated: %+v", out)
	}

	var pages []string
	for i := 0; i < 12; i++ {
		pages = append(pages, fmt.Sprintf(`<page><title>P%d</title><ns>bad</ns></page>`, i))
	}
	out = covJWikiResult(t, covJWikiRaw(s, "collection=w&lang=en", "application/octet-stream", covJWikiDump(pages...)))
	if out.Failed != 12 || len(out.Errors) != 10 {
		t.Errorf("error cap: failed %d, %d errors", out.Failed, len(out.Errors))
	}
}
