package main

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// autoindex --images (RAG-010): a Markdown file's linked images travel with
// it, within the indexed tree and the server's limits.

func TestMarkdownImageFiles(t *testing.T) {
	root := t.TempDir()
	png := []byte("\x89PNG\r\n\x1a\nfake")
	writeTree(t, root, map[string][]byte{
		"docs/guide.md":     []byte(""),
		"docs/img/a.png":    png,
		"docs/img/b c.jpg":  []byte("jpeg"),
		"shared/logo.gif":   []byte("gif"),
		"docs/img/big.webp": make([]byte, maxImageFile+1),
		"docs/img/x.svg":    []byte("<svg/>"),
		"outside.png":       png,
	})
	if err := os.MkdirAll(filepath.Join(root, "docs", "img", "dir.png"), 0o750); err != nil {
		t.Fatal(err)
	}
	md := strings.Join([]string{
		"![a](img/a.png) ![again](img/a.png?v=2)",
		"![spaced](<img/b c.jpg>)",
		"![up](../shared/logo.gif)",
		"![escape](../../outside.png)",
		"![abs](/etc/a.png) ![remote](https://example.com/a.png) ![data](data:image/png;base64,AAAA)",
		"![big](img/big.webp) ![vector](img/x.svg) ![missing](img/none.png) ![dir](img/dir.png)",
	}, "\n")
	// A link out of the tree through a linked directory is not followed.
	elsewhere := t.TempDir()
	writeTree(t, elsewhere, map[string][]byte{"secret.png": png})
	if err := os.Symlink(elsewhere, filepath.Join(root, "docs", "ext")); err == nil {
		md += "\n![linked](ext/secret.png)"
	}
	got := markdownImageFiles(root, "docs/guide.md", []byte(md))
	var paths []string
	for _, im := range got {
		paths = append(paths, im.Path)
	}
	if strings.Join(paths, "|") != "img/a.png|img/b c.jpg|../shared/logo.gif" || string(got[0].Content) != string(png) {
		t.Fatalf("images: %v", paths)
	}
	if markdownImageFiles(filepath.Join(root, "missing"), "a.md", []byte("![](a.png)")) != nil {
		t.Fatal("a root that does not exist")
	}
}

func TestAutoindex_SendsImagesWithMarkdown(t *testing.T) {
	dir := t.TempDir()
	png := []byte("\x89PNG\r\n\x1a\nfake")
	writeTree(t, dir, map[string][]byte{
		"guide.md":     []byte("# Guide\n\n![Seal](img/seal.png)\n"),
		"img/seal.png": png,
	})
	fs := newFakeServer(t, autoindexRoutes())
	if _, err := runCLI(t, fs.URL, "autoindex", "--images", "-c", "imgs", dir); err != nil {
		t.Fatal(err)
	}
	ingest := callsTo(fs, "POST", "/v1/ingest")
	if len(ingest) != 1 || asMap(ingest[0].Body["options"])["extractImages"] != true {
		t.Fatalf("options: %+v", ingest)
	}
	var sent []interface{}
	for _, d := range ingest[0].Body["documents"].([]interface{}) {
		if doc := asMap(d); doc["key"] == "guide.md" {
			sent, _ = doc["images"].([]interface{})
		}
	}
	if len(sent) != 1 || asMap(sent[0])["path"] != "img/seal.png" || asMap(sent[0])["contentBase64"] != base64.StdEncoding.EncodeToString(png) {
		t.Fatalf("images sent: %v", sent)
	}
	if n := wireSize(autoFile{}, []byte("abc"), nil); n != 3 {
		t.Fatalf("wire size: %d", n)
	}
}
