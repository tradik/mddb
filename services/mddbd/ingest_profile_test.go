package main

import (
	"archive/zip"
	"bytes"
	"strings"
	"testing"
)

// RAG-004. The flags always existed; what did not was a name for the
// trade-off, so callers reverse-engineered it from a combination of booleans
// and wiki-import made the same choice a third separate way.

func TestNoProfileKeepsTodaysBehaviour(t *testing.T) {
	got, err := ResolveIngestProfile(&IngestOptionsHTTP{})
	if err != nil {
		t.Fatal(err)
	}
	want := IngestProfile{Name: IngestProfileDefault}
	if got != want {
		t.Errorf("an empty request resolved to %+v, want every flag off", got)
	}

	// A nil options block is the same as an empty one.
	if got, err := ResolveIngestProfile(nil); err != nil || got != want {
		t.Errorf("nil options resolved to %+v (%v)", got, err)
	}

	// The explicit default name means the same thing.
	if got, err := ResolveIngestProfile(&IngestOptionsHTTP{Profile: IngestProfileDefault}); err != nil || got != want {
		t.Errorf("profile %q resolved to %+v (%v)", IngestProfileDefault, got, err)
	}
}

func TestFastProfileExpandsToFlags(t *testing.T) {
	got, err := ResolveIngestProfile(&IngestOptionsHTTP{Profile: IngestProfileFast})
	if err != nil {
		t.Fatal(err)
	}

	if !got.TextOnly {
		t.Error("fast did not select text-only parsing")
	}
	if !got.SkipWebhooks {
		t.Error("fast did not skip webhooks")
	}
	if !got.SkipDuplicates {
		t.Error("fast did not skip duplicates")
	}
	if got.SaveRevision {
		t.Error("fast kept per-document revisions")
	}

	// The important non-inclusion: fast means cheaper parsing and less
	// bookkeeping, not a collection nobody can search semantically.
	if got.SkipEmbeddings {
		t.Error("fast disabled embeddings, which is a separate decision with its own flag")
	}
	if got.SkipFTS {
		t.Error("fast disabled full-text indexing, which the caller did not ask for")
	}
}

// Precedence, as in RAG-001: an explicit flag wins over the preset, so a caller
// wanting fast ingest with revisions kept does not have to abandon the profile.
func TestExplicitFlagsOverrideThePreset(t *testing.T) {
	got, err := ResolveIngestProfile(&IngestOptionsHTTP{
		Profile:      IngestProfileFast,
		SaveRevision: true,
		SkipFTS:      true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !got.SaveRevision {
		t.Error("an explicit saveRevision was overridden by the preset")
	}
	if !got.SkipFTS {
		t.Error("an explicit skipFts was lost")
	}
	// The rest of the preset still applies.
	if !got.TextOnly || !got.SkipWebhooks {
		t.Errorf("overriding one flag discarded the rest of the profile: %+v", got)
	}
}

func TestFlagsWorkWithoutAProfile(t *testing.T) {
	got, err := ResolveIngestProfile(&IngestOptionsHTTP{
		SkipEmbeddings:          true,
		AutoConfigureCollection: true,
		TextOnly:                true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !got.SkipEmbeddings || !got.AutoConfigureCollection || !got.TextOnly {
		t.Errorf("flags were dropped without a profile: %+v", got)
	}
	if got.Name != IngestProfileDefault {
		t.Errorf("name = %q, want default", got.Name)
	}
}

// A typo must not silently fall back: a caller who asked for "fast" and got the
// default would see a slow load and no reason why.
func TestUnknownProfileIsRejected(t *testing.T) {
	for _, name := range []string{"quick", "FAST", "fastest", "none"} {
		if err := ValidateIngestProfile(name); err == nil {
			t.Errorf("profile %q was accepted", name)
		}
		if _, err := ResolveIngestProfile(&IngestOptionsHTTP{Profile: name}); err == nil {
			t.Errorf("resolving %q did not fail", name)
		}
	}
	for _, name := range []string{"", IngestProfileDefault, IngestProfileFast} {
		if err := ValidateIngestProfile(name); err != nil {
			t.Errorf("profile %q was rejected: %v", name, err)
		}
	}
}

// --- text-only converters ---

func TestHTMLToTextDropsStructureAndKeepsWords(t *testing.T) {
	html := []byte(`<html><head><style>p{color:red}</style><script>alert(1)</script></head>
<body><h2>Setup</h2><p>Run <code>make build</code> first.</p>
<ul><li>Step one</li><li>Step two</li></ul>
<a href="https://example.com">the docs</a> &amp; more</body></html>`)

	got := htmlToText(html)

	for _, want := range []string{"Setup", "make build", "Step one", "Step two", "the docs"} {
		if !strings.Contains(got, want) {
			t.Errorf("text %q is missing from the output:\n%s", want, got)
		}
	}
	// Structure is what text-only drops.
	for _, unwanted := range []string{"##", "<p>", "](", "alert(1)", "color:red"} {
		if strings.Contains(got, unwanted) {
			t.Errorf("%q survived into the text output:\n%s", unwanted, got)
		}
	}
	// Entities are resolved, and the ampersand is decoded last so it cannot
	// re-open another entity.
	if !strings.Contains(got, "& more") {
		t.Errorf("entities were not decoded:\n%s", got)
	}
}

// Block tags must break lines, or two paragraphs run into one word.
func TestHTMLToTextSeparatesBlocks(t *testing.T) {
	got := htmlToText([]byte("<p>first</p><p>second</p>"))
	if strings.Contains(got, "firstsecond") {
		t.Errorf("adjacent paragraphs were glued together: %q", got)
	}
	if !strings.Contains(got, "first") || !strings.Contains(got, "second") {
		t.Errorf("text was lost: %q", got)
	}
}

func TestDocxXMLToText(t *testing.T) {
	xmlDoc := `<w:document><w:body>
<w:p><w:pPr><w:pStyle w:val="Heading1"/></w:pPr><w:r><w:t>Title</w:t></w:r></w:p>
<w:p><w:r><w:t>First </w:t></w:r><w:r><w:t>sentence.</w:t></w:r></w:p>
<w:p></w:p>
</w:body></w:document>`

	got := docxXMLToText(xmlDoc)

	if !strings.Contains(got, "Title") {
		t.Errorf("heading text was lost:\n%s", got)
	}
	// Runs within a paragraph belong to one sentence.
	if !strings.Contains(got, "First sentence.") {
		t.Errorf("runs were not joined:\n%s", got)
	}
	// Structure markers are what text-only drops.
	if strings.Contains(got, "#") || strings.Contains(got, "w:t") {
		t.Errorf("markup survived:\n%s", got)
	}
	// An empty paragraph must not become a blank entry.
	if strings.Contains(got, "\n\n\n") {
		t.Errorf("empty paragraphs left blank runs:\n%q", got)
	}
}

func TestODTXMLToText(t *testing.T) {
	xmlDoc := `<office:document><office:body><office:text>
<text:h text:outline-level="1">Chapter</text:h>
<text:p>A paragraph with <text:span>a span</text:span> inside.</text:p>
</office:text></office:body></office:document>`

	got := odtXMLToText(xmlDoc)

	if !strings.Contains(got, "Chapter") || !strings.Contains(got, "a span") {
		t.Errorf("text was lost:\n%s", got)
	}
	if strings.Contains(got, "text:p") || strings.Contains(got, "<") {
		t.Errorf("markup survived:\n%s", got)
	}
}

func TestCollapseBlankLines(t *testing.T) {
	got := collapseBlankLines("  first  \n\n\n\n  second  \n\n")
	if got != "first\n\nsecond" {
		t.Errorf("collapse produced %q", got)
	}
	if got := collapseBlankLines("\n\n\n"); got != "" {
		t.Errorf("all-blank input produced %q", got)
	}
}

// The ampersand must decode last: doing it first would turn "&amp;lt;" into
// "<", changing what the document says.
func TestDecodeCommonEntitiesOrder(t *testing.T) {
	if got := decodeCommonEntities("&amp;lt;"); got != "&lt;" {
		t.Errorf("got %q, want &lt;", got)
	}
	if got := decodeCommonEntities("a &lt;b&gt; &quot;c&quot;"); got != `a <b> "c"` {
		t.Errorf("got %q", got)
	}
}

func TestReadZipEntryRejectsNonZip(t *testing.T) {
	if _, err := readZipEntry([]byte("not a zip"), "content.xml", "odt"); err == nil {
		t.Error("a non-zip was accepted")
	}
}

func TestDocxAndOdtToTextRejectInvalidContainers(t *testing.T) {
	if _, err := docxToText([]byte("not a docx")); err == nil {
		t.Error("an invalid docx was accepted")
	}
	if _, err := odtToText([]byte("not an odt")); err == nil {
		t.Error("an invalid odt was accepted")
	}
}

// RTF never built Markdown structure, so its text-only path is the same
// function under a uniform name rather than a second implementation.
func TestRTFToTextMatchesTheDefaultPath(t *testing.T) {
	rtf := []byte(`{\rtf1\ansi Hello world}`)
	if rtfToText(rtf) != rtfToMarkdown(rtf) {
		t.Error("the RTF text-only path diverged from the default one")
	}
}

// Real containers, because a docx is a zip and the failure modes that matter —
// a missing entry, an unreadable archive — only exist at that layer.

func buildZip(t *testing.T, entries map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range entries {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestDocxToTextReadsARealContainer(t *testing.T) {
	docx := buildZip(t, map[string]string{
		"word/document.xml": `<w:document><w:body>
<w:p><w:r><w:t>Deployment steps</w:t></w:r></w:p>
<w:p><w:r><w:t>Run the migration first.</w:t></w:r></w:p>
</w:body></w:document>`,
		"[Content_Types].xml": "<Types/>",
	})

	got, err := docxToText(docx)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "Deployment steps") || !strings.Contains(got, "Run the migration first.") {
		t.Errorf("text was lost:\n%s", got)
	}
}

func TestOdtToTextReadsARealContainer(t *testing.T) {
	odt := buildZip(t, map[string]string{
		"content.xml": `<office:text><text:p>Runbook body</text:p></office:text>`,
		"mimetype":    "application/vnd.oasis.opendocument.text",
	})

	got, err := odtToText(odt)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "Runbook body") {
		t.Errorf("text was lost:\n%s", got)
	}
}

// A zip that is a valid archive but not the document it claims to be must fail
// clearly, not return an empty document that looks like a successful ingest.
func TestMissingEntryIsAnError(t *testing.T) {
	empty := buildZip(t, map[string]string{"unrelated.txt": "hello"})

	if _, err := docxToText(empty); err == nil {
		t.Error("a zip without word/document.xml was accepted")
	}
	if _, err := odtToText(empty); err == nil {
		t.Error("a zip without content.xml was accepted")
	}
}

// The text-only path must produce the same words as the default one — it drops
// structure, not content.
func TestTextOnlyKeepsTheSameWordsAsTheDefaultPath(t *testing.T) {
	docx := buildZip(t, map[string]string{
		"word/document.xml": `<w:document><w:body>
<w:p><w:pPr><w:pStyle w:val="Heading1"/></w:pPr><w:r><w:t>Rollback</w:t></w:r></w:p>
<w:p><w:r><w:t>Restore the snapshot.</w:t></w:r></w:p>
</w:body></w:document>`,
	})

	fast, err := docxToText(docx)
	if err != nil {
		t.Fatal(err)
	}
	full, err := docxToMarkdown(docx)
	if err != nil {
		t.Fatal(err)
	}

	for _, word := range []string{"Rollback", "Restore the snapshot."} {
		if !strings.Contains(fast, word) {
			t.Errorf("text-only lost %q", word)
		}
		if !strings.Contains(full, word) {
			t.Errorf("the default path lost %q", word)
		}
	}
	// And it really did drop the structure the default path builds.
	if strings.Contains(fast, "#") && strings.Contains(full, "#") {
		t.Error("text-only kept the heading markup")
	}
}

// Malformed OOXML is what a "robust" fast path is for: a document from an
// exporter that closed its tags carelessly must yield its words, not a panic
// and not silence.
func TestDocxTextSurvivesMalformedXML(t *testing.T) {
	cases := map[string]string{
		"unclosed paragraph": `<w:p><w:r><w:t>orphan text</w:t></w:r>`,
		"unclosed text run":  `<w:p><w:r><w:t>never closed</w:p>`,
		"tab element":        `<w:p><w:r><w:tab/><w:t>after a tab</w:t></w:r></w:p>`,
		"no text at all":     `<w:p><w:r><w:br/></w:r></w:p>`,
		"truncated tag":      `<w:p><w:r><w:t`,
		"empty document":     ``,
	}
	for name, xmlDoc := range cases {
		got := docxXMLToText(xmlDoc) // must not panic
		switch name {
		case "unclosed paragraph":
			if !strings.Contains(got, "orphan text") {
				t.Errorf("%s: text was lost: %q", name, got)
			}
		case "tab element":
			if !strings.Contains(got, "after a tab") {
				t.Errorf("%s: text was lost: %q", name, got)
			}
		case "no text at all", "empty document":
			if got != "" {
				t.Errorf("%s: produced %q, want empty", name, got)
			}
		}
	}
}

func TestExplicitSkipDuplicatesAndWebhooksWithoutAProfile(t *testing.T) {
	got, err := ResolveIngestProfile(&IngestOptionsHTTP{
		SkipDuplicates: true,
		SkipWebhooks:   true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !got.SkipDuplicates || !got.SkipWebhooks {
		t.Errorf("explicit flags were dropped: %+v", got)
	}
	if got.TextOnly {
		t.Error("flags alone selected text-only parsing, which only the fast profile does")
	}
}
