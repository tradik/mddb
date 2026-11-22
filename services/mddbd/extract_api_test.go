package main

import (
	"bytes"
	"net/http"
	"os"
	"strings"
	"testing"

	json "mddb/internal/jsonx"
)

// #451: PUT /v1/extract/process as Open WebUI's external document loader
// calls it — the raw file, Content-Type and X-Filename in; page_content and
// metadata out.

// extractCall puts the file at path (or body when path is "") as c.
func extractCall(t *testing.T, s *Server, c *JWTClaims, target, path string, body []byte, headers map[string]string) (int, string) {
	t.Helper()
	if path != "" {
		var err error
		if body, err = os.ReadFile(path); err != nil { // #nosec G304 -- a testdata fixture named by the test
			t.Fatal(err)
		}
	}
	rec := owuiCall(s, c, http.MethodPut, target, bytes.NewReader(body), headers)
	return rec.Code, rec.Body.String()
}

func TestExtractConvertsWithoutStoring(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	switchModules(t, s, FeatureExtractAPI)

	code, body := extractCall(t, s, nil, "/v1/extract/process", "testdata/convert/sample.pdf", nil,
		map[string]string{"Content-Type": "application/pdf", "X-Filename": "Report%20Q1.pdf"})
	var doc extractDocument
	if code != http.StatusOK || json.Unmarshal([]byte(body), &doc) != nil {
		t.Fatalf("pdf: %d %s", code, body)
	}
	golden, _ := os.ReadFile("testdata/convert/sample.pdf.md")
	want, _, _ := strings.Cut(string(golden), "--- format=") // the golden file's trailer is the converter's report
	if strings.TrimSpace(doc.PageContent) != strings.TrimSpace(want) {
		t.Errorf("page_content:\n%s\nwant:\n%s", doc.PageContent, want)
	}
	m := doc.Metadata
	if m["source"] != "Report Q1.pdf" || m["mime"] != "application/pdf" || m["converter"] != "pdf" {
		t.Errorf("metadata: %v", m)
	}
	if _, isNumber := m["conversionConfidence"].(float64); !isNumber {
		t.Errorf("conversionConfidence is not a number: %#v", m["conversionConfidence"])
	}
	if st := s.dbStats(statsWait()); st != nil && len(st.Collections) != 0 {
		t.Errorf("something was stored: %v", st.Collections)
	}

	// A DOCX sent as bytes is recognised by its content; plain text keeps no score.
	if code, body := extractCall(t, s, nil, "/v1/extract/process", "testdata/convert/sample.docx", nil,
		map[string]string{"Content-Type": "application/octet-stream", "X-Filename": "sample.docx"}); code != http.StatusOK ||
		!strings.Contains(body, `"converter":"docx"`) {
		t.Errorf("docx: %d %s", code, body)
	}
	if code, body := extractCall(t, s, nil, "/v1/extract/process", "", []byte("plain words"),
		map[string]string{"X-Filename": "notes.txt"}); code != http.StatusOK || strings.Contains(body, "conversionConfidence") ||
		strings.Contains(body, `"mime"`) || !strings.Contains(body, `"page_content":"plain words"`) {
		t.Errorf("txt: %d %s", code, body)
	}

	// A mailbox is a list: the file, then each message.
	code, body = extractCall(t, s, nil, "/v1/extract/process", "testdata/convert/sample.mbox", nil,
		map[string]string{"Content-Type": "application/mbox", "X-Filename": "box.mbox"})
	var docs []extractDocument
	if code != http.StatusOK || json.Unmarshal([]byte(body), &docs) != nil || len(docs) < 2 || docs[1].Metadata["parent"] != "box.mbox" {
		t.Errorf("mbox: %d %s", code, body)
	}
}

func TestExtractRefusals(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	switchModules(t, s, FeatureExtractAPI)
	random, _ := os.ReadFile("testdata/convert/random.bin")
	cases := []struct {
		name, target string
		body         []byte
		headers      map[string]string
		code         int
		want         string
	}{
		{"empty", "/v1/extract/process", nil, nil, http.StatusBadRequest, "the request body is empty"},
		{"image", "/v1/extract/process", []byte("\x89PNG"), map[string]string{"Content-Type": "image/png", "X-Filename": "a.png"},
			http.StatusUnsupportedMediaType, "unsupported format: png (supported: md, txt"},
		{"bytes", "/v1/extract/process", random, nil, http.StatusUnsupportedMediaType, "binary content that is no format the server converts"},
		{"broken docx", "/v1/extract/process", []byte("not a zip"), map[string]string{"X-Filename": "a.docx"},
			http.StatusUnprocessableEntity, "docx conversion"},
		{"too large", "/v1/extract/process?maxSize=4", []byte("five!"), nil, http.StatusRequestEntityTooLarge, "request body too large"},
	}
	for _, tc := range cases {
		if code, body := extractCall(t, s, nil, tc.target, "", tc.body, tc.headers); code != tc.code || !strings.Contains(body, tc.want) {
			t.Errorf("%s: %d %s", tc.name, code, body)
		}
	}
	if rec := owuiCall(s, nil, http.MethodPost, "/v1/extract/process", nil, nil); rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST: %d", rec.Code)
	}
}

func TestExtractIsForAuthenticatedCallers(t *testing.T) {
	s := accessServer(t)
	switchModules(t, s, FeatureExtractAPI)
	headers := map[string]string{"X-Filename": "a.txt"}
	if code, body := extractCall(t, s, nil, "/v1/extract/process", "", []byte("x"), headers); code != http.StatusUnauthorized {
		t.Errorf("anonymous: %d %s", code, body)
	}
	if code, body := extractCall(t, s, tina, "/v1/extract/process", "", []byte("x"), headers); code != http.StatusOK {
		t.Errorf("tenant user: %d %s", code, body)
	}
}

func TestExtractHelpers(t *testing.T) {
	names := map[string]string{"dir%2Fa%20b.pdf": "a b.pdf", `C:\tmp\x.docx`: "x.docx", "%zz.txt": "%zz.txt", "": "", "/": ""}
	for raw, want := range names {
		if got := extractFilename(raw); got != want {
			t.Errorf("extractFilename(%q) = %q, want %q", raw, got, want)
		}
	}
	formats := map[[2]string]string{
		{"application/vnd.openxmlformats-officedocument.wordprocessingml.document; charset=binary", ""}: "docx",
		{"text/html; charset=utf-8", "x.pdf"}: "html",
		{"application/octet-stream", "x.ODT"}: "odt",
		{"", "noext"}:                         "",
	}
	for in, want := range formats {
		if got := extractFormat(in[0], in[1]); got != want {
			t.Errorf("extractFormat(%q, %q) = %q, want %q", in[0], in[1], got, want)
		}
	}
	sizes := map[string]int64{"": defaultMaxUploadSize, "abc": defaultMaxUploadSize, "-1": defaultMaxUploadSize, "5": 5,
		"999999999999": maxUploadSizeLimit}
	for in, want := range sizes {
		if got := extractMaxSize(in); got != want {
			t.Errorf("extractMaxSize(%q) = %d, want %d", in, got, want)
		}
	}
}
