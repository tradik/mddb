// Format converters and the byte helpers they use.
//
// Renamed from coverage_boost2_test.go (TEST-002): the old name said these
// existed to move a number, and a name that describes its own motive rather
// than its subject invites more of the same. The tests themselves were fine —
// 122 of the 133 across the three files carried real assertions. The eleven
// that did not were given them and moved to lifecycle_and_wiring_test.go.
package main

import (
	"mddb/internal/vector"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// bytes_utils.go — pure byte manipulation utilities
// ---------------------------------------------------------------------------

func TestBytesSplit(t *testing.T) {
	tests := []struct {
		data []byte
		sep  byte
		want int
	}{
		{[]byte("a|b|c"), '|', 3},
		{[]byte("hello"), '|', 1},
		{[]byte(""), '|', 0},
		{[]byte("|"), '|', 2},
		{[]byte("a|b|c|d|e"), '|', 5},
	}
	for _, tc := range tests {
		parts := BytesSplit(tc.data, tc.sep)
		if tc.want == 0 && parts == nil {
			continue
		}
		if len(parts) != tc.want {
			t.Errorf("BytesSplit(%q, %q) got %d parts, want %d", tc.data, string(tc.sep), len(parts), tc.want)
		}
	}
}

func TestBytesHasPrefix(t *testing.T) {
	if !BytesHasPrefix([]byte("hello world"), []byte("hello")) {
		t.Error("expected true")
	}
	if BytesHasPrefix([]byte("hi"), []byte("hello")) {
		t.Error("expected false")
	}
	if !BytesHasPrefix([]byte("abc"), []byte("")) {
		t.Error("empty prefix should match")
	}
}

func TestBytesIndexByte(t *testing.T) {
	if got := BytesIndexByte([]byte("hello"), 'l'); got != 2 {
		t.Errorf("expected 2, got %d", got)
	}
	if got := BytesIndexByte([]byte("hello"), 'z'); got != -1 {
		t.Errorf("expected -1, got %d", got)
	}
	if got := BytesIndexByte([]byte(""), 'a'); got != -1 {
		t.Errorf("expected -1 for empty, got %d", got)
	}
}

func TestBytesLastIndexByte(t *testing.T) {
	if got := BytesLastIndexByte([]byte("hello"), 'l'); got != 3 {
		t.Errorf("expected 3, got %d", got)
	}
	if got := BytesLastIndexByte([]byte("hello"), 'z'); got != -1 {
		t.Errorf("expected -1, got %d", got)
	}
}

func TestExtractPart(t *testing.T) {
	data := []byte("doc|blog|post1")
	tests := []struct {
		idx  int
		want string
	}{
		{0, "doc"},
		{1, "blog"},
		{2, "post1"},
		{3, ""},
	}
	for _, tc := range tests {
		got := ExtractPart(data, tc.idx)
		if tc.want == "" && got == nil {
			continue
		}
		if string(got) != tc.want {
			t.Errorf("ExtractPart(%q, %d) = %q, want %q", data, tc.idx, got, tc.want)
		}
	}
	if got := ExtractPart([]byte(""), 0); got != nil {
		t.Errorf("expected nil for empty data, got %q", got)
	}
}

func TestAppendBytes(t *testing.T) {
	result := AppendBytes([]byte("hello"), []byte(" "), []byte("world"))
	if string(result) != "hello world" {
		t.Errorf("got %q", string(result))
	}
	result2 := AppendBytes(nil, []byte("a"), []byte("b"))
	if string(result2) != "ab" {
		t.Errorf("got %q", string(result2))
	}
}

func TestBytesToLowerCB2(t *testing.T) {
	b := []byte("Hello WORLD 123")
	BytesToLower(b)
	if string(b) != "hello world 123" {
		t.Errorf("got %q", string(b))
	}
}

func TestCompareBytesCB2(t *testing.T) {
	if CompareBytes([]byte("abc"), []byte("abc")) != 0 {
		t.Error("equal should be 0")
	}
	if CompareBytes([]byte("abc"), []byte("abd")) >= 0 {
		t.Error("abc < abd")
	}
	if CompareBytes([]byte("abd"), []byte("abc")) <= 0 {
		t.Error("abd > abc")
	}
	if CompareBytes([]byte("ab"), []byte("abc")) >= 0 {
		t.Error("ab < abc")
	}
}

func TestCopyBytes(t *testing.T) {
	orig := []byte("hello")
	cp := CopyBytes(orig)
	if string(cp) != "hello" {
		t.Errorf("got %q", string(cp))
	}
	cp[0] = 'X'
	if string(orig) != "hello" {
		t.Error("CopyBytes did not make independent copy")
	}
	if CopyBytes(nil) != nil {
		t.Error("nil should return nil")
	}
}

// ---------------------------------------------------------------------------
// upload_handler.go — format converters (pure functions)
// ---------------------------------------------------------------------------

func TestDeriveKeyFromFilename(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"My Document.md", "my-document"},
		{"hello.txt", "hello"},
		{"path/to/file.pdf", "file"},
		{".hidden", ""},
		{"no-ext", "no-ext"},
		{"UPPER.MD", "upper"},
	}
	for _, tc := range tests {
		got := deriveKeyFromFilename(tc.in)
		if got != tc.want {
			t.Errorf("deriveKeyFromFilename(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestHtmlToMarkdown(t *testing.T) {
	tests := []struct {
		name string
		html string
		want string
	}{
		{
			"headings",
			"<h1>Title</h1><h2>Sub</h2>",
			"# Title\n\n## Sub",
		},
		{
			"bold_italic",
			"<strong>bold</strong> and <em>italic</em>",
			"**bold** and *italic*",
		},
		{
			"links",
			`<a href="https://example.com">click</a>`,
			"[click](https://example.com)",
		},
		{
			"lists",
			"<ul><li>one</li><li>two</li></ul>",
			"- one\n- two",
		},
		{
			"strip_script",
			"<script>alert('x')</script>hello",
			"hello",
		},
		{
			"entities",
			"&amp; &lt; &gt; &quot;",
			"& < > \"",
		},
		{
			"br_tags",
			"line1<br>line2<BR/>line3",
			"line1\nline2\nline3",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := htmlToMarkdown([]byte(tc.html))
			got = strings.TrimSpace(got)
			want := strings.TrimSpace(tc.want)
			if got != want {
				t.Errorf("\ngot:  %q\nwant: %q", got, want)
			}
		})
	}
}

func TestStripTagBlock(t *testing.T) {
	got := stripTagBlock("<div>hello<script>evil</script>world</div>", "script")
	if strings.Contains(got, "evil") {
		t.Errorf("script not stripped: %q", got)
	}
}

func TestReplaceTagContent(t *testing.T) {
	got := replaceTagContent("<b>hello</b> world", "b", "**", "**")
	if got != "**hello** world" {
		t.Errorf("got %q", got)
	}
}

func TestConvertLinks(t *testing.T) {
	got := convertLinks(`<a href="http://x.com">link</a>`)
	if got != "[link](http://x.com)" {
		t.Errorf("got %q", got)
	}
	// Link without href
	got2 := convertLinks(`<a class="foo">text</a>`)
	if got2 != "text" {
		t.Errorf("got %q", got2)
	}
}

func TestStripAllTags(t *testing.T) {
	got := stripAllTags("<p>hello <b>world</b></p>")
	if got != "hello world" {
		t.Errorf("got %q", got)
	}
}

func TestReplaceAllFold(t *testing.T) {
	got := replaceAllFold("Hello HELLO hello", "hello", "X")
	if got != "X X X" {
		t.Errorf("got %q", got)
	}
}

func TestIndexFold(t *testing.T) {
	if indexFold("Hello World", "world") != 6 {
		t.Error("expected 6")
	}
	if indexFold("abc", "xyz") != -1 {
		t.Error("expected -1")
	}
}

func TestRtfToMarkdown(t *testing.T) {
	rtf := `{\rtf1\ansi{\fonttbl\f0 Arial;}\f0 Hello {\b bold} world\par Second line}`
	got := rtfToMarkdown([]byte(rtf))
	if !strings.Contains(got, "Hello") {
		t.Errorf("expected Hello in output: %q", got)
	}
	if !strings.Contains(got, "world") {
		t.Errorf("expected world in output: %q", got)
	}
	// Not valid RTF
	got2 := rtfToMarkdown([]byte("plain text"))
	if got2 != "plain text" {
		t.Errorf("plain text pass-through failed: %q", got2)
	}
}

func TestDocxXMLToMarkdown(t *testing.T) {
	xml := `<w:body>
		<w:p><w:pPr><w:pStyle w:val="Heading1"/></w:pPr><w:r><w:t>Title</w:t></w:r></w:p>
		<w:p><w:r><w:t>Paragraph text here.</w:t></w:r></w:p>
	</w:body>`
	got := docxXMLToMarkdown(xml)
	if !strings.Contains(got, "# Title") {
		t.Errorf("expected heading: %q", got)
	}
	if !strings.Contains(got, "Paragraph text here.") {
		t.Errorf("expected paragraph: %q", got)
	}
}

func TestOdtXMLToMarkdown(t *testing.T) {
	xml := `<office:body><office:text>
		<text:h text:outline-level="2">Section</text:h>
		<text:p>Body text</text:p>
	</office:text></office:body>`
	got := odtXMLToMarkdown(xml)
	if !strings.Contains(got, "## Section") {
		t.Errorf("expected heading: %q", got)
	}
	if !strings.Contains(got, "Body text") {
		t.Errorf("expected body: %q", got)
	}
}

func TestPdfToMarkdownInvalid(t *testing.T) {
	_, err := pdfToMarkdown([]byte("not a pdf"))
	if err == nil {
		t.Error("expected error for invalid PDF")
	}
}

func TestPdfToMarkdownNoText(t *testing.T) {
	// Valid PDF header but no extractable text
	_, err := pdfToMarkdown([]byte("%PDF-1.4\n%%EOF"))
	if err == nil {
		t.Error("expected error for empty PDF")
	}
}

func TestExtractPDFText(t *testing.T) {
	var buf strings.Builder
	extractPDFText(`(Hello) Tj (World) Tj`, &buf)
	got := buf.String()
	if !strings.Contains(got, "Hello") || !strings.Contains(got, "World") {
		t.Errorf("expected Hello World, got %q", got)
	}
}

func TestExtractPDFTextEscaped(t *testing.T) {
	var buf strings.Builder
	extractPDFText(`(Hello \(world\)) Tj`, &buf)
	got := buf.String()
	if !strings.Contains(got, "Hello (world)") {
		t.Errorf("expected unescaped parens, got %q", got)
	}
}

// ---------------------------------------------------------------------------
// vector_index.go — similarity functions
// ---------------------------------------------------------------------------

func TestCosineSimilarityCB2(t *testing.T) {
	a := []float32{1, 0, 0}
	b := []float32{0, 1, 0}
	sim := vector.CosineSimilarity(a, b)
	if sim > 0.01 || sim < -0.01 {
		t.Errorf("orthogonal: got %f, want ~0", sim)
	}
	same := vector.CosineSimilarity(a, a)
	if same < 0.99 {
		t.Errorf("identical: got %f, want ~1", same)
	}
	// Empty/mismatched
	if vector.CosineSimilarity(nil, nil) != 0 {
		t.Error("nil should return 0")
	}
	if vector.CosineSimilarity([]float32{1}, []float32{1, 2}) != 0 {
		t.Error("mismatched lengths should return 0")
	}
	// Zero vectors
	if vector.CosineSimilarity([]float32{0, 0}, []float32{1, 1}) != 0 {
		t.Error("zero vector should return 0")
	}
}

func TestResolveSimilarity(t *testing.T) {
	fn := vector.ResolveSimilarity("dot_product")
	if fn == nil {
		t.Fatal("dot_product should resolve")
	}
	fn = vector.ResolveSimilarity("euclidean")
	if fn == nil {
		t.Fatal("euclidean should resolve")
	}
	fn = vector.ResolveSimilarity("cosine")
	if fn == nil {
		t.Fatal("cosine (default) should resolve")
	}
	fn = vector.ResolveSimilarity("")
	if fn == nil {
		t.Fatal("empty should resolve to default")
	}
}

func TestBaseDocIDCB2(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"doc1#0", "doc1"},
		{"doc1#5", "doc1"},
		{"doc1", "doc1"},
		{"", ""},
	}
	for _, tc := range tests {
		if got := vector.BaseDocID(tc.in); got != tc.want {
			t.Errorf("vector.BaseDocID(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestDeduplicateChunkResultsCB2(t *testing.T) {
	results := []vector.VectorResult{
		{DocID: "doc1#0", Score: 0.9},
		{DocID: "doc1#1", Score: 0.95},
		{DocID: "doc2#0", Score: 0.8},
		{DocID: "doc2#1", Score: 0.7},
	}
	deduped := vector.DeduplicateChunkResults(results)
	if len(deduped) != 2 {
		t.Fatalf("expected 2, got %d", len(deduped))
	}
	// Should be sorted by score descending
	if deduped[0].DocID != "doc1" || deduped[0].Score != 0.95 {
		t.Errorf("first should be doc1@0.95, got %s@%f", deduped[0].DocID, deduped[0].Score)
	}
	if deduped[1].DocID != "doc2" || deduped[1].Score != 0.8 {
		t.Errorf("second should be doc2@0.8, got %s@%f", deduped[1].DocID, deduped[1].Score)
	}
	// Empty input
	if len(vector.DeduplicateChunkResults(nil)) != 0 {
		t.Error("nil should return empty")
	}
}

// ---------------------------------------------------------------------------
// metrics.go — pure helper functions
// ---------------------------------------------------------------------------

func TestExtractCollectionCB2(t *testing.T) {
	tests := []struct {
		in   []byte
		want string
	}{
		{[]byte("doc|blog|post1"), "blog"},
		{[]byte("idx|test|abc"), "test"},
		{[]byte("nopipe"), ""},
		{[]byte("one|only"), "only"},
	}
	for _, tc := range tests {
		if got := metricsExtractCollection(tc.in); got != tc.want {
			t.Errorf("metricsExtractCollection(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// ---------------------------------------------------------------------------
// storage_memory.go — key helper
// ---------------------------------------------------------------------------

func TestMemKeyIndex(t *testing.T) {
	got := memKeyIndex("col", "key1", "en")
	if got != "col|key1|en" {
		t.Errorf("got %q", got)
	}
}
