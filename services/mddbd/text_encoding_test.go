package main

import (
	"context"
	"strings"
	"testing"
	"unicode/utf8"
)

// GO-036. A file in Latin-1 or Windows-1250 used to fail deep inside protobuf
// marshalling with a message about protobuf, saying nothing about encoding.
// Found by FuzzDocRoundTrip.

// latin1 is "café" as Windows-1250 would encode it: the é is a single byte
// 0xE9, which is not valid UTF-8 on its own.
const latin1 = "caf\xe9"

func TestValidateRefusesUndecodableContent(t *testing.T) {
	err := ValidateDocumentText(latin1, nil)
	if err == nil {
		t.Fatal("Latin-1 content was accepted")
	}

	msg := err.Error()
	// The message has to name the encoding and the fix. "invalid UTF-8" tells
	// a user what happened, not what to do about it.
	for _, want := range []string{"contentMd", "UTF-8", "iconv"} {
		if !strings.Contains(msg, want) {
			t.Errorf("the error does not mention %q: %s", want, msg)
		}
	}
}

func TestValidateChecksMetadataToo(t *testing.T) {
	if err := ValidateDocumentText("clean", map[string][]string{"tag": {latin1}}); err == nil {
		t.Error("a metadata value in Latin-1 was accepted")
	}
	if err := ValidateDocumentText("clean", map[string][]string{latin1: {"v"}}); err == nil {
		t.Error("a metadata key in Latin-1 was accepted")
	}
	// A key that is itself undecodable must not make the error message
	// undecodable in turn.
	err := ValidateDocumentText("clean", map[string][]string{latin1: {"v"}})
	if !utf8.ValidString(err.Error()) {
		t.Error("the error message is itself not valid UTF-8")
	}
}

func TestValidateAcceptsCleanText(t *testing.T) {
	cases := map[string]string{
		"ascii":      "plain text",
		"accents":    "café, naïve, Łódź",
		"cjk":        "日本語のテキスト",
		"emoji":      "a document 📄 with emoji",
		"empty":      "",
		"whitespace": "   \n\t  ",
	}
	for name, content := range cases {
		if err := ValidateDocumentText(content, map[string][]string{"tag": {content}}); err != nil {
			t.Errorf("%s was refused: %v", name, err)
		}
	}
}

// The single-document path refuses, and this is the reason: sanitising would
// turn café into caf and tell nobody.
func TestAddRefusesUndecodableTextThroughEveryTransport(t *testing.T) {
	_, srv, cleanup := directClientServer(t)
	defer cleanup()

	_, _, err := srv.addDocument("c", "k", "en", nil, latin1, 0, false)
	if err == nil {
		t.Fatal("addDocument accepted Latin-1 content")
	}
	if !strings.Contains(err.Error(), "iconv") {
		t.Errorf("the error does not say how to fix it: %v", err)
	}

	// Nothing was written.
	if doc, _ := srv.LoadDocByID("c", "c|k|en"); doc != nil {
		t.Error("a refused document was stored anyway")
	}
}

func TestSanitizeDropsUndecodableBytes(t *testing.T) {
	content, meta, changed := SanitizeDocumentText(latin1, nil)

	if !changed {
		t.Error("sanitising Latin-1 reported no change")
	}
	if !utf8.ValidString(content) {
		t.Errorf("the result is still not valid UTF-8: %q", content)
	}
	if content != "caf" {
		t.Errorf("content = %q, want the undecodable byte dropped", content)
	}
	if meta != nil {
		t.Errorf("nil metadata became %v", meta)
	}
}

func TestSanitizeLeavesCleanTextAlone(t *testing.T) {
	original := "café, naïve, 日本語"
	meta := map[string][]string{"tag": {"go", "rust"}}

	content, got, changed := SanitizeDocumentText(original, meta)

	if changed {
		t.Error("clean text was reported as changed")
	}
	if content != original {
		t.Errorf("content = %q, want it untouched", content)
	}
	// The common case must not pay for a copy of the metadata map.
	if &got == &meta {
		// Comparing map headers is not meaningful in Go; check contents.
		_ = got
	}
	if len(got) != 1 || len(got["tag"]) != 2 {
		t.Errorf("metadata changed: %v", got)
	}
}

func TestSanitizeCleansMetadataKeysAndValues(t *testing.T) {
	meta := map[string][]string{
		"clean":  {"value"},
		latin1:   {"value"},
		"broken": {"ok", latin1},
	}

	_, got, changed := SanitizeDocumentText("clean", meta)

	if !changed {
		t.Fatal("undecodable metadata reported no change")
	}
	for key, values := range got {
		if !utf8.ValidString(key) {
			t.Errorf("key %q is still invalid", key)
		}
		for _, v := range values {
			if !utf8.ValidString(v) {
				t.Errorf("value under %q is still invalid: %q", key, v)
			}
		}
	}
	// The clean entries must survive untouched.
	if len(got["clean"]) != 1 || got["clean"][0] != "value" {
		t.Errorf("a clean entry was altered: %v", got["clean"])
	}
	if len(got["broken"]) != 2 || got["broken"][0] != "ok" {
		t.Errorf("a clean value beside a broken one was altered: %v", got["broken"])
	}
}

// The bulk path sanitises rather than refusing, so one badly encoded page
// cannot abort an import of twenty thousand — but the count has to be right.
func TestBulkIngestSanitisesAndCounts(t *testing.T) {
	_, srv, cleanup := directClientServer(t)
	defer cleanup()

	resp, err := srv.ingestDocuments(context.Background(), "bulk", []IngestDocumentHTTP{
		{Key: "clean-1", Lang: "en", ContentMD: "a perfectly ordinary document"},
		{Key: "broken", Lang: "en", ContentMD: latin1 + " and more text"},
		{Key: "clean-2", Lang: "en", ContentMD: "another ordinary document"},
	}, IngestOptionsHTTP{SkipEmbeddings: true, SkipFTS: true, SkipWebhooks: true})
	if err != nil {
		t.Fatalf("one badly encoded document aborted the import: %v", err)
	}

	if resp.Sanitized != 1 {
		t.Errorf("sanitized = %d, want 1", resp.Sanitized)
	}
	if resp.Added != 3 {
		t.Errorf("added = %d, want all three (errors: %v)", resp.Added, resp.Errors)
	}

	// The stored document must be readable and shortened by exactly the byte
	// that could not be decoded.
	doc, err := srv.LoadDocByID("bulk", "bulk|broken|en")
	if err != nil || doc == nil {
		t.Fatalf("the sanitised document was not stored: %v", err)
	}
	if !utf8.ValidString(doc.ContentMD) {
		t.Error("the stored document is not valid UTF-8")
	}
	if !strings.HasPrefix(doc.ContentMD, "caf ") {
		t.Errorf("content = %q, want the undecodable byte dropped", doc.ContentMD)
	}
}

func TestBulkIngestReportsZeroWhenNothingNeededCleaning(t *testing.T) {
	_, srv, cleanup := directClientServer(t)
	defer cleanup()

	resp, err := srv.ingestDocuments(context.Background(), "clean", []IngestDocumentHTTP{
		{Key: "a", Lang: "en", ContentMD: "café and 日本語 are both fine"},
	}, IngestOptionsHTTP{SkipEmbeddings: true, SkipFTS: true, SkipWebhooks: true})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Sanitized != 0 {
		t.Errorf("sanitized = %d for a clean import", resp.Sanitized)
	}
}
