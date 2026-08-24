package main

import (
	"fmt"
	"strings"
	"testing"
)

// RAG-004 claims text-only ingest is faster. These measure it, because a
// trade-off nobody has measured is a guess with a name.

func benchHTML(paragraphs int) []byte {
	var b strings.Builder
	b.WriteString("<html><head><style>body{margin:0}</style></head><body>")
	for i := range paragraphs {
		fmt.Fprintf(&b, `<h2>Section %d</h2><p>Some <strong>bold</strong> and <em>italic</em> text with `+
			`a <a href="https://example.com/%d">link</a> and <code>inline code</code>.</p>`+
			`<ul><li>First item</li><li>Second item</li></ul>`, i, i)
	}
	b.WriteString("</body></html>")
	return []byte(b.String())
}

func benchDocxXML(paragraphs int) string {
	var b strings.Builder
	b.WriteString("<w:document><w:body>")
	for i := range paragraphs {
		fmt.Fprintf(&b, `<w:p><w:pPr><w:pStyle w:val="Heading2"/></w:pPr><w:r><w:t>Section %d</w:t></w:r></w:p>`, i)
		b.WriteString(`<w:p><w:r><w:rPr><w:b/></w:rPr><w:t>Bold run </w:t></w:r>` +
			`<w:r><w:t>and ordinary text in the same paragraph.</w:t></w:r></w:p>`)
	}
	b.WriteString("</w:body></w:document>")
	return b.String()
}

func BenchmarkHTMLToMarkdown(b *testing.B) {
	data := benchHTML(200)
	b.SetBytes(int64(len(data)))
	b.ResetTimer()
	for range b.N {
		_ = htmlToMarkdown(data)
	}
}

func BenchmarkHTMLToText(b *testing.B) {
	data := benchHTML(200)
	b.SetBytes(int64(len(data)))
	b.ResetTimer()
	for range b.N {
		_ = htmlToText(data)
	}
}

func BenchmarkDocxXMLToMarkdown(b *testing.B) {
	xmlDoc := benchDocxXML(200)
	b.SetBytes(int64(len(xmlDoc)))
	b.ResetTimer()
	for range b.N {
		_ = docxXMLToMarkdown(xmlDoc)
	}
}

func BenchmarkDocxXMLToText(b *testing.B) {
	xmlDoc := benchDocxXML(200)
	b.SetBytes(int64(len(xmlDoc)))
	b.ResetTimer()
	for range b.N {
		_ = docxXMLToText(xmlDoc)
	}
}
