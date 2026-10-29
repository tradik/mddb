package main

import (
	"archive/zip"
	"bytes"
	"strings"
	"testing"
)

// The pre-GO-046 converters (docx, odt, rtf, tex, the HTML helpers), at the
// edges: missing parts, unreadable entries, markup cut short.

// badChecksumZip has one stored entry whose CRC does not match its bytes, so
// it opens and then fails to read.
func badChecksumZip(t *testing.T, name string) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	f, err := w.CreateRaw(&zip.FileHeader{Name: name, Method: zip.Store, CRC32: 1, CompressedSize64: 3, UncompressedSize64: 3})
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.Write([]byte("abc"))
	_ = w.Close()
	return buf.Bytes()
}

func TestDocxAndOdtContainers(t *testing.T) {
	for name, fn := range map[string]func([]byte) (string, error){"word/document.xml": docxToMarkdown, "content.xml": odtToMarkdown} {
		if _, err := fn(fixtureZip("other.xml", "x")); err == nil || !strings.Contains(err.Error(), name+" not found") {
			t.Errorf("%s missing: %v", name, err)
		}
		if _, err := fn(unreadableZip(t, name)); err == nil {
			t.Errorf("%s unreadable: no error", name)
		}
		if _, err := fn(badChecksumZip(t, name)); err == nil {
			t.Errorf("%s bad checksum: no error", name)
		}
	}
}

func TestDocxXMLEdges(t *testing.T) {
	xml := `<w:p><w:pPr><w:pStyle w:val="heading 2"/></w:pPr><w:r><w:t>Head</w:t></w:r></w:p>` +
		`<w:p><w:pPr><w:numPr/></w:pPr><w:r><w:t>item</w:t></w:r></w:p>` +
		`<w:p></w:p>` +
		`<w:p><w:r><w:t>cut</w:p>` +
		`<w:p><w:r><w:t`
	if got := docxXMLToMarkdown(xml); got != "## Head\n\n- item" {
		t.Errorf("docx: %q", got)
	}
	if got := docxXMLToMarkdown(`<w:p><w:r><w:t xml:space="preserve"`); got != "" {
		t.Errorf("unterminated tag: %q", got)
	}
	if got := docxXMLToMarkdown(`<w:p><w:r><w:t xml:space="x"</w:p>`); got != "" {
		t.Errorf("tag without >: %q", got)
	}
}

func TestOdtXMLEdges(t *testing.T) {
	xml := `<text:h text:outline-level="9">Deep</text:h><text:h text:outline-level="2"></text:h><text:p></text:p><text:p>Body</text:p><text:p`
	if got := odtXMLToMarkdown(xml); got != "# Deep\n\nBody" {
		t.Errorf("odt: %q", got)
	}
	if got := odtXMLToMarkdown(`<text:p>never closed`); got != "" {
		t.Errorf("unclosed: %q", got)
	}
}

func TestHTMLHelperEdges(t *testing.T) {
	if got := stripTagBlock("a<script>never closed", "script"); got != "a<script>never closed" {
		t.Errorf("stripTagBlock: %q", got)
	}
	if got := replaceTagContent("<b never", "b", "*", "*"); got != "<b never" {
		t.Errorf("replaceTagContent no >: %q", got)
	}
	if got := replaceTagContent("<b>x", "b", "*", "*"); got != "<b>x" {
		t.Errorf("replaceTagContent no close: %q", got)
	}
	if got := convertLinks(`<a href="u"`); got != `<a href="u"` {
		t.Errorf("convertLinks no >: %q", got)
	}
	if got := convertLinks(`<a href="u">x`); got != `<a href="u">x` {
		t.Errorf("convertLinks no close: %q", got)
	}
	if got := htmlToMarkdown([]byte("<p>a &amp;lt; b &#8217;s&nbsp;x</p>")); got != "a &lt; b ’s x" {
		t.Errorf("entities: %q", got)
	}
}

func TestRTFEdges(t *testing.T) {
	if got := rtfToMarkdown([]byte("not rtf")); got != "not rtf" {
		t.Errorf("plain: %q", got)
	}
	rtf := `{\rtf1{\fonttbl\f0\fswiss-12 Helvetica;\f1 Times;}{\*\generator x}` +
		`\endash\emdash\lquote\rquote\ldblquote\rdblquote\bullet Caf\'e9\tab x\{\}\\\par\par\par\par end\`
	got := rtfToMarkdown([]byte(rtf))
	if got != "–—‘’“”- Caf\tx{}\\\n\nend" {
		t.Errorf("rtf: %q", got)
	}
}

func TestTeXEdges(t *testing.T) {
	tex := "\\usepackage[opt[nested]]{a{b}}\nText \\textbfx \\cite[p. 3]{key} \\emph{open \\begin{x \\end{y"
	got := texToMarkdown([]byte(tex))
	for _, want := range []string{"Text", "[key]"} {
		if !strings.Contains(got, want) {
			t.Errorf("tex %q lacks %q", got, want)
		}
	}
	if strings.Contains(got, "usepackage") || strings.Contains(got, "textbfx") {
		t.Errorf("tex: %q", got)
	}
	if got := texReplaceCmd(`\emph{unbalanced`, `\emph`, "*", "*"); got != "unbalanced" {
		t.Errorf("unbalanced: %q", got)
	}
}
