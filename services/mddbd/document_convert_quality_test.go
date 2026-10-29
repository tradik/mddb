package main

import (
	"archive/zip"
	"bytes"
	"errors"
	"strings"
	"testing"
)

// The confidence heuristic (GO-046), the per-document meta it writes, and the
// content sniffing that decides a file's format.

func TestAssessConversion(t *testing.T) {
	cases := []struct {
		name     string
		data     []byte
		md       string
		sig      conversionSignals
		want     float64
		warnings []string
	}{
		{"clean text", []byte("abc"), "plain words", conversionSignals{}, 1, nil},
		{"empty file", nil, "", conversionSignals{}, 1, nil},
		{"no text at all", []byte("x"), "| --- |", conversionSignals{}, 0, []string{"no text extracted"}},
		{"half the pages scanned", []byte("x"), "text", conversionSignals{Pages: 4, PagesWithoutText: 2}, 0.5, []string{"2 of 4 pages"}},
		{"an empty sheet", []byte("x"), "text", conversionSignals{Sheets: 2, EmptySheets: 1}, 0.75, []string{"1 of 2 sheets are empty"}},
		{"garbled", []byte("x"), "ab\x01\x02�" + strings.Repeat("c", 15), conversionSignals{}, 0.4, []string{"15.0% of characters"}},
		{"one odd character is no warning", []byte("x"), "\x01" + strings.Repeat("c", 199), conversionSignals{}, 0.98, nil},
		{"mostly pictures", bytes.Repeat([]byte("x"), 100000), strings.Repeat("w", 100), conversionSignals{Container: true}, 0.6, []string{"100 bytes of text from a 100000-byte file"}},
		{"converter remarks kept", []byte("x"), "text", conversionSignals{Warnings: []string{"2 streams"}}, 1, []string{"2 streams"}},
	}
	for _, tc := range cases {
		got, warnings := assessConversion(tc.data, tc.md, tc.sig)
		if got != tc.want || len(warnings) != len(tc.warnings) {
			t.Errorf("%s: %.2f %v, want %.2f %v", tc.name, got, warnings, tc.want, tc.warnings)
			continue
		}
		for i, w := range tc.warnings {
			if !strings.Contains(warnings[i], w) {
				t.Errorf("%s: warning %q, want %q", tc.name, warnings[i], w)
			}
		}
	}
	if badCharShare("") != 0 || badCharShare("\xff") != 1 {
		t.Error("bad character share")
	}
}

func TestConversionMetaAndDocs(t *testing.T) {
	meta := map[string][]string{}
	conversionMeta(meta, conversion{Markdown: "x"})
	if len(meta) != 0 {
		t.Fatalf("an unconverted file has no score: %v", meta)
	}

	c := conversion{Format: "eml", Markdown: "body", Converted: true, Confidence: 0.42, Warnings: []string{"w"},
		Meta: map[string][]string{"subject": {"From the file"}, "from": {"a@b"}},
		Parts: []conversionPart{
			{Suffix: "/attachments/a.pdf", conversion: conversion{Format: "pdf", Markdown: "pdf text", Converted: true, Confidence: 1}},
			{Suffix: "/message-1/attachments/b.csv", ParentSuffix: "/message-1", conversion: conversion{Format: "csv", Markdown: "t", Converted: true, Confidence: 0.9}},
		}}
	docs := convertedDocs(c, "inbox", map[string][]string{"subject": {"From the caller"}, "path": {"inbox"}})
	if len(docs) != 3 {
		t.Fatalf("docs: %+v", docs)
	}
	main := docs[0]
	if main.Key != "inbox" || main.Meta["conversionConfidence"][0] != "0.42" || main.Meta["conversionWarnings"][0] != "w" ||
		main.Meta["subject"][0] != "From the caller" || main.Meta["from"][0] != "a@b" || main.Meta["parent"] != nil {
		t.Errorf("main: %+v", main)
	}
	if d := docs[1]; d.Key != "inbox/attachments/a.pdf" || d.Meta["parent"][0] != "inbox" || d.Meta["path"][0] != "inbox" ||
		d.Meta["conversionConfidence"][0] != "1.00" || d.Meta["conversionWarnings"] != nil {
		t.Errorf("attachment: %+v", d)
	}
	if d := docs[2]; d.Key != "inbox/message-1/attachments/b.csv" || d.Meta["parent"][0] != "inbox/message-1" {
		t.Errorf("nested attachment: %+v", d)
	}
}

func TestResolveFormat(t *testing.T) {
	docx := fixtureZip("[Content_Types].xml", "<Types/>", "word/document.xml", "<w:document/>")
	otherZip := fixtureZip("a.txt", "x")
	cases := []struct {
		declared string
		data     []byte
		want     string
		err      string
	}{
		{"txt", []byte("%PDF-1.4 ..."), "pdf", ""},              // content wins
		{"md", []byte(`{\rtf1 x}`), "rtf", ""},                  // so does RTF's signature
		{"xlsx", docx, "docx", ""},                              // a Word file sent as a workbook
		{"docx", otherZip, "docx", ""},                          // left to the docx converter to refuse
		{"", otherZip, "", "binary content"},                    // a zip that is no document
		{"txt", []byte("a\x00b"), "txt", "binary content"},      // NUL in text
		{"exe", []byte("MZ"), "exe", "unsupported format: exe"}, // no converter
		{".HTM", []byte("<p>x</p>"), "html", ""},
	}
	for _, tc := range cases {
		got, err := resolveFormat(tc.declared, tc.data)
		if got != tc.want || (tc.err == "") != (err == nil) || (err != nil && !strings.Contains(err.Error(), tc.err)) {
			t.Errorf("resolveFormat(%q): %q %v, want %q %q", tc.declared, got, err, tc.want, tc.err)
		}
	}
	if _, err := resolveFormat("exe", nil); !errors.Is(err, errUnsupportedFormat) {
		t.Error("unsupported is recognisable")
	}
}

func TestSniffZip(t *testing.T) {
	for want, data := range map[string][]byte{
		"zip":  []byte("PK\x03\x04 not a zip"),
		"xlsx": fixtureZip("xl/workbook.xml", "<workbook/>"),
		"pptx": fixtureZip("ppt/presentation.xml", "<p/>"),
		"ods":  fixtureZip("mimetype", "application/vnd.oasis.opendocument.spreadsheet"),
		"odp":  fixtureZip("mimetype", "application/vnd.oasis.opendocument.presentation"),
		"epub": fixtureZip("mimetype", "application/epub+zip"),
	} {
		if got := sniffFormat(data); got != want {
			t.Errorf("sniffFormat = %q, want %q", got, want)
		}
	}
	if got := sniffFormat(fixtureZip("mimetype", "application/x-other")); got != "zip" {
		t.Errorf("unknown mimetype: %q", got)
	}
}

// unreadableZip has one entry whose compression method no reader knows.
func unreadableZip(t *testing.T, name string) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	f, err := w.CreateRaw(&zip.FileHeader{Name: name, Method: 99, CompressedSize64: 1, UncompressedSize64: 1})
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.Write([]byte("x"))
	_ = w.Close()
	return buf.Bytes()
}

func TestZipDocRead(t *testing.T) {
	if _, err := openZipDoc([]byte("nope"), "xlsx"); err == nil || !strings.Contains(err.Error(), "invalid xlsx file") {
		t.Fatalf("not a zip: %v", err)
	}
	z, err := openZipDoc(fixtureZip("a.xml", "<a>", "big", strings.Repeat("x", 100)), "ods")
	if err != nil {
		t.Fatal(err)
	}
	if !z.has("a.xml") || z.has("b") {
		t.Error("has")
	}
	if _, err := z.read("missing"); err == nil || !strings.Contains(err.Error(), "missing not found") {
		t.Errorf("missing: %v", err)
	}
	var v struct{}
	if err := z.readXML("a.xml", &v); err == nil || !strings.Contains(err.Error(), "invalid ods: a.xml") {
		t.Errorf("bad xml: %v", err)
	}
	old := maxZipEntry
	maxZipEntry = 10
	defer func() { maxZipEntry = old }()
	if _, err := z.read("big"); err == nil || !strings.Contains(err.Error(), "inflates past 10 bytes") {
		t.Errorf("bomb: %v", err)
	}

	bad, err := openZipDoc(unreadableZip(t, "x.xml"), "pptx")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bad.read("x.xml"); err == nil || !strings.Contains(err.Error(), "invalid pptx: x.xml") {
		t.Errorf("unreadable entry: %v", err)
	}
	if got := sniffFormat(unreadableZip(t, "mimetype")); got != "zip" {
		t.Errorf("unreadable mimetype: %q", got)
	}
	if byID, byType := z.relTargets("a.xml"); len(byID)+len(byType) != 0 {
		t.Error("no rels part, no relationships")
	}
}
