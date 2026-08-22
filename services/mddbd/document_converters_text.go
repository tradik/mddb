package main

import (
	"archive/zip"
	"bytes"
	"errors"
	"fmt"
	"io"
	"strings"
)

// Text-only conversion (RAG-004).
//
// The default converters rebuild a document's structure as Markdown: headings
// from `<h2>` and docx `pStyle`, bold from `<strong>`, lists, tables, links.
// That is the right default — structure is what makes a document readable
// after retrieval — but it is also where the time goes and where odd documents
// break the parser.
//
// Text-only extracts the words and drops the shape. For a bulk load where the
// corpus matters more than any individual document's formatting, that is a
// trade worth naming.
//
// Note what is NOT here: PDF. `pdfToMarkdown` already extracts raw text from
// content streams and builds no Markdown structure at all, so a "text-only PDF"
// would be the same function under a second name. Claiming a speedup there
// would be a lie told by an API.

// htmlToText strips markup and returns the readable text.
func htmlToText(data []byte) string {
	s := string(data)
	// Script and style contents are not readable text; without this their
	// source ends up in the document.
	s = stripTagBlock(s, "script")
	s = stripTagBlock(s, "style")

	// Block-level tags mark where text must not run together. Without this,
	// "</p><p>" would glue two paragraphs into one word.
	for _, tag := range []string{"p", "div", "br", "li", "tr", "h1", "h2", "h3", "h4", "h5", "h6"} {
		s = replaceAllFold(s, "<"+tag+">", "\n")
		s = replaceAllFold(s, "</"+tag+">", "\n")
		s = replaceAllFold(s, "<"+tag+" ", "\n<"+tag+" ")
	}

	s = stripAllTags(s)
	s = decodeCommonEntities(s)
	return collapseBlankLines(s)
}

// docxToText extracts the text runs from a .docx without rebuilding structure.
func docxToText(data []byte) (string, error) {
	xmlData, err := readZipEntry(data, "word/document.xml", "docx")
	if err != nil {
		return "", err
	}
	return docxXMLToText(string(xmlData)), nil
}

// docxXMLToText pulls <w:t> runs, breaking a line per paragraph.
//
// A single case-sensitive forward walk, matching how docxXMLToMarkdown reads
// the same file. OOXML fixes its element names in lower case, so folding case
// buys nothing and costs a full copy of the document.
//
// Both mistakes were caught by benchmarking rather than by reading: the first
// version split on every tag (each split lowercasing the whole string again)
// and ran seven times SLOWER than the Markdown converter it was meant to beat;
// removing the splits but keeping one lowercase copy was still six times
// slower. A "fast" path slower than the normal one is worse than no fast path,
// because the caller pays for it believing the opposite.
func docxXMLToText(xmlDoc string) string {
	var out strings.Builder

	remaining := xmlDoc
	for {
		pStart := strings.Index(remaining, "<w:p")
		if pStart < 0 {
			break
		}
		remaining = remaining[pStart:]

		pEnd := strings.Index(remaining, "</w:p>")
		para := remaining
		if pEnd >= 0 {
			para = remaining[:pEnd]
			remaining = remaining[pEnd+len("</w:p>"):]
		} else {
			remaining = ""
		}

		if text := docxParagraphText(para); text != "" {
			out.WriteString(text)
			out.WriteString("\n\n")
		}
		if remaining == "" {
			break
		}
	}

	return strings.TrimSpace(out.String())
}

// docxParagraphText joins the <w:t> runs of one paragraph.
//
// Runs split a sentence wherever formatting changes, so "First **bold** word"
// arrives as three runs and must come back as one line.
func docxParagraphText(para string) string {
	var line strings.Builder

	rest := para
	for {
		tStart := strings.Index(rest, "<w:t")
		if tStart < 0 {
			break
		}
		rest = rest[tStart:]

		open := strings.Index(rest, ">")
		if open < 0 {
			break
		}
		// `<w:tab/>` and `<w:tbl>` also begin with "<w:t"; only a real text
		// run has a matching close tag.
		content := rest[open+1:]
		tEnd := strings.Index(content, "</w:t>")
		if tEnd < 0 {
			rest = content
			continue
		}
		line.WriteString(content[:tEnd])
		rest = content[tEnd+len("</w:t>"):]
	}

	text := strings.TrimSpace(line.String())
	if text == "" {
		return ""
	}
	return decodeCommonEntities(text)
}

// odtToText extracts the text from an ODT without rebuilding structure.
func odtToText(data []byte) (string, error) {
	xmlData, err := readZipEntry(data, "content.xml", "odt")
	if err != nil {
		return "", err
	}
	return odtXMLToText(string(xmlData)), nil
}

// odtXMLToText breaks a line per <text:p> / <text:h> and strips the rest.
func odtXMLToText(xmlDoc string) string {
	s := xmlDoc
	for _, tag := range []string{"text:p", "text:h", "text:list-item"} {
		s = replaceAllFold(s, "</"+tag+">", "\n\n")
	}
	s = stripAllTags(s)
	return collapseBlankLines(decodeCommonEntities(s))
}

// rtfToText reuses the RTF reader, which already yields plain text.
//
// Unlike html and docx, the RTF path never built Markdown structure — so this
// is a rename, kept for a uniform call site rather than a separate
// implementation nobody would remember to keep in step.
func rtfToText(data []byte) string {
	return rtfToMarkdown(data)
}

// readZipEntry opens one file inside a zip container.
//
// Both docx and odt are zips with a single interesting entry; the default
// converters each opened theirs by hand, and duplicating that a third and
// fourth time is how the two paths drift.
func readZipEntry(data []byte, name, format string) ([]byte, error) {
	r, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("invalid %s file: %w", format, err)
	}
	for _, f := range r.File {
		if f.Name != name {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		defer func() { _ = rc.Close() }()
		return io.ReadAll(rc)
	}
	return nil, errors.New("invalid " + format + ": " + name + " not found")
}

// decodeCommonEntities resolves the handful of XML/HTML entities that appear in
// ordinary prose. Not a full entity table: a rare unresolved entity is a
// cosmetic flaw, while a wrong substitution changes what a document says.
func decodeCommonEntities(s string) string {
	return entityDecoder.Replace(s)
}

// entityDecoder is built once: it is called per paragraph, and compiling the
// replacer each time cost more than the replacement itself.
//
// Ampersand comes last because strings.Replacer matches in order — decoding it
// first would turn "&amp;lt;" into "<", changing what the document says.
var entityDecoder = strings.NewReplacer(
	"&lt;", "<",
	"&gt;", ">",
	"&quot;", `"`,
	"&#39;", "'",
	"&apos;", "'",
	"&nbsp;", " ",
	"&amp;", "&",
)

// collapseBlankLines trims each line and reduces runs of blank lines to one, so
// stripped markup does not leave a document that is mostly whitespace.
func collapseBlankLines(s string) string {
	lines := strings.Split(s, "\n")
	out := make([]string, 0, len(lines))
	blank := false

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			if !blank && len(out) > 0 {
				out = append(out, "")
			}
			blank = true
			continue
		}
		blank = false
		out = append(out, trimmed)
	}

	return strings.TrimSpace(strings.Join(out, "\n"))
}
