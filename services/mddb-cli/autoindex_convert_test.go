package main

import (
	"archive/zip"
	"bytes"
	"encoding/base64"
	"path/filepath"
	"strings"
	"testing"
)

// zipOf builds a zip archive from name → content, in the order given.
func zipOf(t *testing.T, entries ...string) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for i := 0; i+1 < len(entries); i += 2 {
		f, err := w.Create(entries[i])
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.Write([]byte(entries[i+1])); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// convertibleTree is a folder with one file of every kind autoindex treats
// differently: Markdown, markup, the binary formats the server converts, and
// a random binary it cannot.
func convertibleTree(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeTree(t, dir, map[string][]byte{
		"README.md":       []byte("# Read me\n"),
		"page.html":       []byte("<html><body><h1>Title</h1><p>Body</p></body></html>"),
		"paper.tex":       []byte(`\section{Intro} text`),
		"report.pdf":      []byte("%PDF-1.4\n1 0 obj\n<<>>\nstream\nBT (Hello) Tj ET\nendstream\n%%EOF"),
		"letter.docx":     zipOf(t, "[Content_Types].xml", "<Types/>", "word/document.xml", "<w:document/>"),
		"notes.odt":       zipOf(t, "mimetype", "application/vnd.oasis.opendocument.text", "content.xml", "<office:document-content/>"),
		"memo.rtf":        []byte(`{\rtf1\ansi Hello}`),
		"misnamed.txt":    []byte("%PDF-1.4\nBT (x) Tj ET"),
		"random.bin":      {0x00, 0x9f, 0x13, 0x77, 0x00, 0xfe},
		"archive.zip":     zipOf(t, "a.txt", "x"),
		"broken.docx":     []byte("PK\x03\x04 not really a zip"),
		"unknown-odf.odx": zipOf(t, "mimetype", "application/x-unknown"),
	})
	return dir
}

func TestAutoindex_SniffsConvertibleFiles(t *testing.T) {
	dir := convertibleTree(t)
	want := map[string]struct {
		format string
		binary bool
	}{
		"README.md": {"", false}, "page.html": {"html", false}, "paper.tex": {"tex", false},
		"report.pdf": {"pdf", false}, "letter.docx": {"docx", false}, "notes.odt": {"odt", false},
		"memo.rtf": {"rtf", false}, "misnamed.txt": {"pdf", false}, "random.bin": {"", true},
		"archive.zip": {"", true}, "broken.docx": {"", true}, "unknown-odf.odx": {"", true},
	}
	// A directory opens but cannot be read: not text.
	if _, binary := sniffFile(dir); !binary {
		t.Error("an unreadable file is binary")
	}
	for name, w := range want {
		format, binary := sniffFile(filepath.Join(dir, name))
		if format != w.format || binary != w.binary {
			t.Errorf("%s: format %q binary %v, want %q %v", name, format, binary, w.format, w.binary)
		}
	}
}

func TestAutoindex_SendsConvertibleFilesForConversion(t *testing.T) {
	dir := convertibleTree(t)
	fs := newFakeServer(t, autoindexRoutes())
	out, err := runCLI(t, fs.URL, "autoindex", "-c", "docs", dir)
	if err != nil {
		t.Fatal(err)
	}
	ingest := callsTo(fs, "POST", "/v1/ingest")
	if len(ingest) != 1 {
		t.Fatalf("ingest calls: %d", len(ingest))
	}
	byKey := map[string]map[string]interface{}{}
	for _, d := range ingest[0].Body["documents"].([]interface{}) {
		doc := asMap(d)
		byKey[asString(doc["key"])] = doc
	}
	for _, name := range []string{"random.bin", "archive.zip", "broken.docx", "unknown-odf.odx"} {
		if _, sent := byKey[name]; sent {
			t.Errorf("binary %s was sent", name)
		}
	}
	md := byKey["README.md"]
	if md["contentMd"] != "# Read me\n" || md["format"] != nil || md["contentBase64"] != nil {
		t.Fatalf("markdown is sent as text: %v", md)
	}
	for name, format := range map[string]string{"page.html": "html", "paper.tex": "tex", "report.pdf": "pdf",
		"letter.docx": "docx", "notes.odt": "odt", "memo.rtf": "rtf", "misnamed.txt": "pdf"} {
		doc := byKey[name]
		if doc == nil {
			t.Fatalf("%s not sent", name)
		}
		raw, err := base64.StdEncoding.DecodeString(asString(doc["contentBase64"]))
		if err != nil || len(raw) == 0 || doc["format"] != format || asString(doc["contentMd"]) != "" {
			t.Errorf("%s: format %v, %d bytes, err %v", name, doc["format"], len(raw), err)
		}
		if kind := asMap(doc["meta"])["kind"]; asString(kind.([]interface{})[0]) != "doc" {
			t.Errorf("%s: kind %v", name, kind)
		}
		if !strings.Contains(out, "~ "+name+": converted from "+format) {
			t.Errorf("report misses %s:\n%s", name, out)
		}
	}
}

func TestAutoindex_DryRunShowsFormat(t *testing.T) {
	fs := newFakeServer(t, autoindexRoutes())
	out, err := runCLI(t, fs.URL, "autoindex", "--dry-run", convertibleTree(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"+ report.pdf (doc pdf)", "+ page.html (doc html)", "- random.bin: binary"} {
		if !strings.Contains(out, want) {
			t.Errorf("dry run misses %q:\n%s", want, out)
		}
	}
}

func TestAutoindex_ConvertedFilesCountBase64InBatchSize(t *testing.T) {
	f := autoFile{Format: "pdf"}
	if wireSize(f, make([]byte, 3), nil) != 4 || wireSize(autoFile{}, make([]byte, 3), nil) != 3 {
		t.Fatal("wire size")
	}
}

func TestAutoindex_ZipEntryUnreadable(t *testing.T) {
	// A stored entry whose method the reader does not know cannot be opened.
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	f, err := w.CreateRaw(&zip.FileHeader{Name: "mimetype", Method: 99, CompressedSize64: 1, UncompressedSize64: 1})
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.Write([]byte("x"))
	_ = w.Close()
	r, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatal(err)
	}
	if readSmall(r.File[0]) != nil {
		t.Fatal("an unreadable entry reads as nothing")
	}
}
