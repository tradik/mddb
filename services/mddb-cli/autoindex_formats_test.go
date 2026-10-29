package main

import (
	"path/filepath"
	"strings"
	"testing"
)

// GO-046: the formats added in v2.18.9 are recognised too, and the report
// carries the confidence the server measured.

func TestAutoindex_SniffsSheetsSlidesBooksAndMail(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string][]byte{
		"book.xlsx":  zipOf(t, "[Content_Types].xml", "<Types/>", "xl/workbook.xml", "<workbook/>"),
		"deck.pptx":  zipOf(t, "[Content_Types].xml", "<Types/>", "ppt/presentation.xml", "<p:presentation/>"),
		"sheet.ods":  zipOf(t, "mimetype", "application/vnd.oasis.opendocument.spreadsheet"),
		"talk.odp":   zipOf(t, "mimetype", "application/vnd.oasis.opendocument.presentation"),
		"novel.epub": zipOf(t, "mimetype", "application/epub+zip"),
		"data.csv":   []byte("a,b\n1,2\n"),
		"data.tsv":   []byte("a\tb\n"),
		"mail.eml":   []byte("From: a@b\n\nhi\n"),
		"inbox.mbox": []byte("From a@b Mon Oct 26 08:00:00 2026\nFrom: a@b\n\nhi\n"),
	})
	for name, want := range map[string]string{"book.xlsx": "xlsx", "deck.pptx": "pptx", "sheet.ods": "ods", "talk.odp": "odp",
		"novel.epub": "epub", "data.csv": "csv", "data.tsv": "tsv", "mail.eml": "eml", "inbox.mbox": "mbox"} {
		if format, binary := sniffFile(filepath.Join(dir, name)); format != want || binary {
			t.Errorf("%s: %q %v, want %q", name, format, binary, want)
		}
	}
}

func TestAutoindex_ReportsConversionConfidence(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string][]byte{
		"scan.pdf":   []byte("%PDF-1.4\n%%EOF"),
		"inbox.mbox": []byte("From a@b Mon Oct 26 08:00:00 2026\nFrom: a@b\n\nhi\n"),
	})
	routes := autoindexRoutes()
	routes["/v1/ingest"] = `{"added":3,"collection":"x","conversions":[` +
		`{"key":"scan.pdf","format":"pdf","conversionConfidence":0,"conversionWarnings":["1 of 1 pages have no extractable text"]},` +
		`{"key":"inbox.mbox","format":"mbox","conversionConfidence":1},` +
		`{"key":"inbox.mbox/message-1","format":"eml","conversionConfidence":0.95}]}`
	fs := newFakeServer(t, routes)
	out, err := runCLI(t, fs.URL, "autoindex", "-c", "x", dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"~ scan.pdf: converted from pdf (confidence 0.00: 1 of 1 pages have no extractable text)",
		"~ inbox.mbox: converted from mbox (confidence 1.00)",
		"~ inbox.mbox/message-1: converted from eml (confidence 0.95)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("report misses %q:\n%s", want, out)
		}
	}
	if (autoConverted{Rel: "x", Format: "pdf"}).quality() != "" {
		t.Error("no confidence, no note")
	}
}
