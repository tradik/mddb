package main

import (
	"archive/zip"
	"bytes"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// docxToMarkdown extracts text from a DOCX file (which is a ZIP containing XML).
func docxToMarkdown(data []byte) (string, error) {
	r, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", fmt.Errorf("invalid docx file: %w", err)
	}

	// Find word/document.xml
	var docFile *zip.File
	for _, f := range r.File {
		if f.Name == "word/document.xml" {
			docFile = f
			break
		}
	}
	if docFile == nil {
		return "", errors.New("invalid docx: word/document.xml not found")
	}

	rc, err := docFile.Open()
	if err != nil {
		return "", err
	}
	defer func() { _ = rc.Close() }()

	xmlData, err := io.ReadAll(rc)
	if err != nil {
		return "", err
	}

	return docxXMLToMarkdown(string(xmlData)), nil
}

// docxXMLToMarkdown converts word/document.xml content to markdown.
// Handles paragraphs, runs, headings (pStyle), bold, italic, and lists.
func docxXMLToMarkdown(xml string) string {
	var result strings.Builder
	remaining := xml
	for {
		para, rest, ok := nextDocxParagraph(remaining)
		if !ok {
			break
		}
		remaining = rest
		writeDocxParagraph(&result, para)
	}
	return strings.TrimSpace(squeezeBlankLines(result.String()))
}

// nextDocxParagraph cuts the first <w:p ...>...</w:p> out of s and returns it
// with what follows it; ok is false when s holds no complete paragraph.
func nextDocxParagraph(s string) (para, rest string, ok bool) {
	const closeTag = "</w:p>"
	pStart := strings.Index(s, "<w:p")
	if pStart < 0 {
		return "", "", false
	}
	pEnd := strings.Index(s[pStart:], closeTag)
	if pEnd < 0 {
		return "", "", false
	}
	end := pStart + pEnd + len(closeTag)
	return s[pStart:end], s[end:], true
}

// writeDocxParagraph writes one paragraph as a heading, a list item, a plain
// paragraph, or — empty and not a heading — a blank line.
func writeDocxParagraph(out *strings.Builder, para string) {
	level := docxHeadingLevel(para)
	text := docxRunsText(para)
	switch {
	case text == "" && level == 0:
		out.WriteByte('\n')
	case level > 0:
		out.WriteString(strings.Repeat("#", level))
		out.WriteByte(' ')
		out.WriteString(text)
		out.WriteString("\n\n")
	case strings.Contains(para, "<w:numPr"):
		out.WriteString("- ")
		out.WriteString(text)
		out.WriteByte('\n')
	default:
		out.WriteString(text)
		out.WriteString("\n\n")
	}
}

// docxHeadingLevel is the heading level a paragraph's style names —
// Heading1 or "heading 1" through 6 — or 0 for anything else.
func docxHeadingLevel(para string) int {
	const marker = `<w:pStyle w:val="`
	styleIdx := strings.Index(para, marker)
	if styleIdx < 0 {
		return 0
	}
	valStart := styleIdx + len(marker)
	valEnd := strings.Index(para[valStart:], `"`)
	if valEnd <= 0 {
		return 0
	}
	n, ok := strings.CutPrefix(strings.ToLower(para[valStart:valStart+valEnd]), "heading")
	if !ok {
		return 0
	}
	level, err := strconv.Atoi(strings.TrimSpace(n))
	if err != nil || level < 1 || level > 6 {
		return 0
	}
	return level
}

// docxRunsText joins the text of a paragraph's runs:
// <w:t ...>text</w:t> or <w:t>text</w:t>.
func docxRunsText(para string) string {
	const closeTag = "</w:t>"
	var text strings.Builder
	rest := para
	for {
		tStart := strings.Index(rest, "<w:t")
		if tStart < 0 {
			break
		}
		gtIdx := strings.Index(rest[tStart:], ">") // past the opening tag
		if gtIdx < 0 {
			break
		}
		textStart := tStart + gtIdx + 1
		tEnd := strings.Index(rest[textStart:], closeTag)
		if tEnd < 0 {
			break
		}
		text.WriteString(rest[textStart : textStart+tEnd])
		rest = rest[textStart+tEnd+len(closeTag):]
	}
	return text.String()
}
