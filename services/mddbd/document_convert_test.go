package main

import (
	"strings"
	"testing"
)

// The one conversion dispatch (AGT-001): every format, both modes, and the
// errors a caller sees.

func TestConvertToMarkdownEveryFormat(t *testing.T) {
	docx := buildZip(t, map[string]string{"word/document.xml": `<w:document><w:body><w:p><w:pPr><w:pStyle w:val="Heading1"/></w:pPr><w:r><w:t>Docx title</w:t></w:r></w:p></w:body></w:document>`})
	odt := buildZip(t, map[string]string{"content.xml": `<office:text><text:h text:outline-level="1">Odt title</text:h></office:text>`})
	cases := []struct {
		format, input string
		data          []byte
		textOnly      bool
		converted     bool
		want, notWant string
	}{
		{format: "md", input: "# kept", want: "# kept"},
		{format: "markdown", input: "kept", want: "kept"},
		{format: "txt", input: "kept", want: "kept"},
		{format: "text", input: "kept", want: "kept"},
		{format: "", input: "kept", want: "kept"},
		{format: "YAML", input: "a: 1", converted: true, want: "```yaml\na: 1\n```"},
		{format: ".yml", input: "a: 1", converted: true, want: "```yml\n"},
		{format: "log", input: "line", converted: true, want: "```log\n"},
		{format: "lex", input: "%%", converted: true, want: "```lex\n"},
		{format: "tex", input: `\section{Intro} body`, converted: true, want: "Intro"},
		{format: "latex", input: `\section{Intro} body`, converted: true, want: "Intro"},
		{format: "html", input: "<h1>Head</h1><p>Body</p>", converted: true, want: "# Head", notWant: "<h1>"},
		{format: "htm", input: "<h1>Head</h1>", textOnly: true, converted: true, want: "Head", notWant: "#"},
		{format: "pdf", input: "%PDF-1.4\nstream\nBT (Pdf words) Tj ET\nendstream\n", converted: true, want: "Pdf words"},
		{format: "docx", data: docx, converted: true, want: "# Docx title"},
		{format: "docx", data: docx, textOnly: true, converted: true, want: "Docx title", notWant: "#"},
		{format: "odt", data: odt, converted: true, want: "# Odt title"},
		{format: "odt", data: odt, textOnly: true, converted: true, want: "Odt title", notWant: "#"},
		{format: "rtf", input: `{\rtf1\ansi Rtf words}`, converted: true, want: "Rtf words", notWant: `\rtf`},
	}
	for _, tc := range cases {
		data := tc.data
		if data == nil {
			data = []byte(tc.input)
		}
		got, err := convertToMarkdown(tc.format, data, tc.textOnly)
		if err != nil {
			t.Fatalf("%s: %v", tc.format, err)
		}
		if got.Converted != tc.converted || !strings.Contains(got.Markdown, tc.want) ||
			(tc.notWant != "" && strings.Contains(got.Markdown, tc.notWant)) {
			t.Errorf("%s (textOnly %v): converted %v, %q", tc.format, tc.textOnly, got.Converted, got.Markdown)
		}
	}
}

func TestConvertToMarkdownErrors(t *testing.T) {
	for format, want := range map[string]string{
		"exe":  "unsupported format: exe (supported: ",
		"pdf":  "pdf conversion: ",
		"docx": "docx conversion: ",
		"odt":  "odt conversion: ",
	} {
		if _, err := convertToMarkdown(format, []byte("not this format"), false); err == nil || !strings.HasPrefix(err.Error(), want) {
			t.Errorf("%s: %v, want %q", format, err, want)
		}
	}
	for _, format := range []string{"docx", "odt"} {
		if _, err := convertToMarkdown(format, []byte("not a zip"), true); err == nil {
			t.Errorf("%s text-only accepted a non-zip", format)
		}
	}
}

func TestFormatFromFilename(t *testing.T) {
	for name, want := range map[string]string{"a.HTM": "html", "b.pdf": "pdf", "README": "", "dir.v2/c.Docx": "docx"} {
		if got := formatFromFilename(name); got != want {
			t.Errorf("formatFromFilename(%q) = %q, want %q", name, got, want)
		}
	}
}
