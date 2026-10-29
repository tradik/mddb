package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"mddb/internal/ttl"
	proto "mddb/proto"

	bolt "go.etcd.io/bbolt"
)

// Ingest converts files (AGT-001): what `mddb-cli autoindex` sends for a
// folder of md, html, pdf, docx, odt and rtf is stored as Markdown, and a
// random binary is refused rather than indexed as bytes.

// autoindexFolder is the documents autoindex sends for such a folder.
func autoindexFolder(t *testing.T) []IngestDocumentHTTP {
	t.Helper()
	return []IngestDocumentHTTP{
		{Key: "README.md", Lang: "en", ContentMD: "# Read me\n\nPlain words."},
		{Key: "page.html", Lang: "en", Format: "html", Content: []byte("<html><body><h1>Html title</h1><p>Html body</p><script>alert(1)</script></body></html>")},
		{Key: "report.pdf", Lang: "en", Format: "pdf", Content: []byte("%PDF-1.4\n1 0 obj\n<<>>\nstream\nBT (Pdf body text) Tj ET\nendstream\n%%EOF")},
		{Key: "letter.docx", Lang: "en", Format: "docx", Content: buildZip(t, map[string]string{
			"[Content_Types].xml": "<Types/>",
			"word/document.xml":   `<w:document><w:body><w:p><w:r><w:t>Docx body text</w:t></w:r></w:p></w:body></w:document>`,
		})},
		{Key: "notes.odt", Lang: "en", Format: "odt", Content: buildZip(t, map[string]string{
			"mimetype":    "application/vnd.oasis.opendocument.text",
			"content.xml": `<office:text><text:p>Odt body text</text:p></office:text>`,
		})},
		{Key: "memo.rtf", Lang: "en", Format: "rtf", Content: []byte(`{\rtf1\ansi Rtf body text}`)},
		// No format: the key's extension names it, and .bin is not a format.
		{Key: "random.bin", Lang: "en", Content: []byte{0, 0x9f, 0x13, 0x77, 0, 0xfe}},
	}
}

func TestIngestConvertsEveryConvertibleFile(t *testing.T) {
	_, srv, cleanup := directClientServer(t)
	defer cleanup()

	rec := doRequest(t, srv.handleIngest, IngestRequestHTTP{Collection: "folder", Documents: autoindexFolder(t),
		Options: IngestOptionsHTTP{SkipDuplicates: true, SkipEmbeddings: true, SkipFTS: true, SkipWebhooks: true}})
	if rec.Code != http.StatusOK {
		t.Fatalf("ingest: %d %s", rec.Code, rec.Body.String())
	}
	var resp IngestResponseHTTP
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Added != 6 || resp.Failed != 1 || len(resp.Errors) != 1 || !strings.Contains(resp.Errors[0], "random.bin") ||
		!strings.Contains(resp.Errors[0], "unsupported format: bin") {
		t.Fatalf("response: %+v", resp)
	}
	for key, want := range map[string]string{"README.md": "Plain words", "page.html": "# Html title", "report.pdf": "Pdf body text",
		"letter.docx": "Docx body text", "notes.odt": "Odt body text", "memo.rtf": "Rtf body text"} {
		doc, err := srv.loadDocByRef("folder", key, "en")
		if err != nil {
			t.Fatalf("%s: %v", key, err)
		}
		if !strings.Contains(doc.ContentMD, want) || strings.ContainsAny(doc.ContentMD, "<\x00") || strings.Contains(doc.ContentMD, `\rtf`) {
			t.Errorf("%s stored as %q", key, doc.ContentMD)
		}
		if format := doc.Meta["sourceFormat"]; key != "README.md" && (len(format) != 1 || !strings.HasSuffix(key, "."+format[0])) {
			t.Errorf("%s: sourceFormat %v", key, format)
		}
	}

	// A second run over the unchanged folder writes nothing: the converted
	// text is the same, so its hash is.
	again, err := srv.ingestDocuments(context.Background(), "folder", autoindexFolder(t), IngestOptionsHTTP{SkipDuplicates: true, SkipEmbeddings: true, SkipFTS: true, SkipWebhooks: true})
	if err != nil {
		t.Fatal(err)
	}
	if again.Skipped != 6 || again.Added+again.Updated != 0 {
		t.Fatalf("second run: %+v", again)
	}
}

func TestIngestConversionDetails(t *testing.T) {
	_, srv, cleanup := directClientServer(t)
	defer cleanup()
	srv.TTLManager = ttl.NewTTLManager(srv.DB, nil)
	if err := srv.TTLManager.EnsureBuckets(); err != nil {
		t.Fatal(err)
	}

	resp, err := srv.ingestDocuments(context.Background(), "c", []IngestDocumentHTTP{
		// HTML sent as text, with a format to convert it from.
		{Key: "as-text", Lang: "en", Format: "html", ContentMD: "<p>Text <b>html</b></p>"},
		// The format named by the key; text-only drops the heading marks.
		{Key: "page.htm", Lang: "en", Content: []byte("<h2>Only text</h2>")},
		// A file of format md keeps its frontmatter extraction.
		{Key: "fm", Lang: "en", Format: "md", Content: []byte("---\ntitle: Front\n---\nBody"), ExtractFrontmatter: true},
		// A converted file has no frontmatter of its own to extract.
		{Key: "conv", Lang: "en", Format: "html", Content: []byte("---\ntitle: x\n---\n<p>y</p>"), ExtractFrontmatter: true},
		// A failing document before the one with a TTL must not move it.
		{Key: "bad.pdf", Lang: "en", Content: []byte("not a pdf"), TTL: 60},
		{Key: "expiring", Lang: "en", ContentMD: "short-lived", TTL: 3600},
	}, IngestOptionsHTTP{TextOnly: true, SkipEmbeddings: true, SkipFTS: true, SkipWebhooks: true})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Added != 5 || resp.Failed != 1 || !strings.Contains(resp.Errors[0], "pdf conversion") {
		t.Fatalf("response: %+v", resp)
	}
	load := func(key string) (string, map[string][]string) {
		doc, err := srv.loadDocByRef("c", key, "en")
		if err != nil {
			t.Fatalf("%s: %v", key, err)
		}
		return doc.ContentMD, doc.Meta
	}
	if md, _ := load("as-text"); md != "Text html" {
		t.Errorf("html as text: %q", md)
	}
	if md, _ := load("page.htm"); md != "Only text" {
		t.Errorf("text-only: %q", md)
	}
	if md, meta := load("fm"); md != "Body" || meta["title"][0] != "Front" || meta["sourceFormat"] != nil {
		t.Errorf("md file: %q %v", md, meta)
	}
	if _, meta := load("conv"); meta["title"] != nil {
		t.Errorf("frontmatter extracted from a converted file: %v", meta)
	}
	var ttlKeys []string
	_ = srv.DBViewC("c", func(tx *bolt.Tx) error {
		return tx.Bucket([]byte("ttlrev")).ForEach(func(k, _ []byte) error {
			ttlKeys = append(ttlKeys, string(k))
			return nil
		})
	})
	if len(ttlKeys) != 1 || !strings.Contains(ttlKeys[0], "expiring") {
		t.Fatalf("TTL set on %v, want only the expiring document", ttlKeys)
	}
}

func TestIngestTTLsSkipFailedDocuments(t *testing.T) {
	_, srv, cleanup := directClientServer(t)
	defer cleanup()
	srv.applyIngestTTLs("c", nil, nil) // no TTL manager: nothing to do
	srv.TTLManager = ttl.NewTTLManager(srv.DB, nil)
	if err := srv.TTLManager.EnsureBuckets(); err != nil {
		t.Fatal(err)
	}
	srv.applyIngestTTLs("c", map[string]int64{"a": 10, "b": 10}, []*ProcessedDoc{nil, {DocID: "a", Error: context.Canceled}, {DocID: "c"}})
	n := 0
	_ = srv.DBViewC("c", func(tx *bolt.Tx) error {
		return tx.Bucket([]byte("ttlrev")).ForEach(func(_, _ []byte) error { n++; return nil })
	})
	if n != 0 {
		t.Fatalf("%d TTLs set for failed or TTL-less documents", n)
	}
}

func TestIngestFromProtoCarriesFiles(t *testing.T) {
	_, srv, cleanup := directClientServer(t)
	defer cleanup()
	resp, err := srv.ingestDocumentsFromProto(context.Background(), &proto.IngestRequest{Collection: "g", Documents: []*proto.IngestDocument{
		{Key: "x.rtf", Lang: "en", Format: "rtf", Content: []byte(`{\rtf1 Grpc words}`)},
	}, Options: &proto.IngestOptions{SkipEmbeddings: true, SkipFts: true, SkipWebhooks: true}})
	if err != nil || resp.Added != 1 {
		t.Fatalf("%+v %v", resp, err)
	}
	doc, err := srv.loadDocByRef("g", "x.rtf", "en")
	if err != nil || doc.ContentMD != "Grpc words" {
		t.Fatalf("%v %v", doc, err)
	}
}

// The content hash map reads only documents that exist, and survives a
// collection with no documents bucket.
func TestIngestContentHashMap(t *testing.T) {
	_, srv, cleanup := directClientServer(t)
	defer cleanup()
	seedDocs(t, srv, "h", "a")
	got := srv.buildContentHashMap("h", []*proto.BatchDocument{{Key: "a", Lang: "en"}, {Key: "missing", Lang: "en"}})
	if len(got) != 1 {
		t.Fatalf("hashes: %v", got)
	}
	srv.BucketNames.Docs = []byte("no-such-bucket")
	if got := srv.buildContentHashMap("h", []*proto.BatchDocument{{Key: "a", Lang: "en"}}); len(got) != 0 {
		t.Fatalf("hashes without a bucket: %v", got)
	}
}
