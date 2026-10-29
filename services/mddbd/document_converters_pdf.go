package main

import (
	"compress/zlib"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"unicode/utf8"
)

// PDF text extraction: reads the content streams (inflating FlateDecode,
// which is how nearly every PDF written since 2000 stores them) and takes the
// strings of the text operators Tj, TJ and '. Best-effort for text-based
// PDFs; scanned pages have no text to take, and the converter now counts them
// rather than failing or pretending (GO-046).

// maxPDFInflate bounds what one stream may inflate to: a hostile file can
// claim a few bytes expand to gigabytes. A variable only so a test can lower
// it.
var maxPDFInflate int64 = 32 << 20

// pdfPageRe matches a page object's type, not the page tree's (/Pages).
var pdfPageRe = regexp.MustCompile(`/Type\s*/Page\b`)

// pdfToMarkdown extracts the text of a PDF and reports how many pages it saw
// and how many of them gave no text.
func pdfToMarkdown(data []byte) (string, conversionSignals, error) {
	sig := conversionSignals{Container: true}
	if len(data) < 5 || string(data[:5]) != "%PDF-" {
		return "", sig, errors.New("not a valid PDF file")
	}

	s := string(data)
	var result strings.Builder
	// Pages are counted outside streams, where page objects are written as
	// text, and inside object streams; a stream's bytes are not searched,
	// where a stored (uncompressed) block could repeat them.
	pages, textStreams, undecoded := 0, 0, 0

	idx := 0
	for {
		streamStart := strings.Index(s[idx:], "stream\n")
		if alt := strings.Index(s[idx:], "stream\r\n"); alt >= 0 && (streamStart < 0 || alt < streamStart) {
			streamStart = alt
		}
		if streamStart < 0 {
			break
		}
		streamStart += idx

		contentStart := streamStart + 7 // len("stream\n")
		if s[streamStart+6] == '\r' {
			contentStart = streamStart + 8
		}
		endStream := strings.Index(s[contentStart:], "endstream")
		if endStream < 0 {
			break
		}
		dict := pdfStreamDict(s[idx:streamStart])
		pages += len(pdfPageRe.FindAllString(s[idx:streamStart], -1))
		idx = contentStart + endStream + 9

		content, ok := pdfStreamContent(dict, s[contentStart:contentStart+endStream])
		if !ok {
			undecoded++
			continue
		}
		if strings.Contains(dict, "/ObjStm") {
			// Page objects of a compressed object stream count as pages.
			pages += len(pdfPageRe.FindAllString(content, -1))
			continue
		}
		if !pdfHasTextOperators(content) {
			continue
		}
		before := result.Len()
		extractPDFText(content, &result)
		if strings.TrimSpace(result.String()[before:]) != "" {
			textStreams++
			result.WriteByte('\n')
		}
	}
	pages += len(pdfPageRe.FindAllString(s[idx:], -1))

	if pages < textStreams {
		pages = textStreams // a page may have several content streams
	}
	if pages == 0 {
		pages = 1 // a PDF has at least one page, even when none is declared
	}
	sig.Pages = pages
	sig.PagesWithoutText = max(0, pages-textStreams)
	if undecoded > 0 {
		sig.Warnings = append(sig.Warnings, fmt.Sprintf("%d streams use a filter the converter cannot decode", undecoded))
	}
	return latin1Fallback(strings.TrimSpace(result.String())), sig, nil
}

// pdfStreamDict returns the dictionary of the stream that follows: the text
// after the last "obj" keyword before it.
func pdfStreamDict(before string) string {
	if i := strings.LastIndex(before, "obj"); i >= 0 {
		return before[i:]
	}
	return before
}

// pdfStreamContent decodes a stream's bytes. Images and fonts are skipped
// (reported as decoded, with nothing in them), FlateDecode is inflated, and
// any other filter cannot be read.
func pdfStreamContent(dict, raw string) (string, bool) {
	switch {
	case strings.Contains(dict, "/Image"), strings.Contains(dict, "/FontFile"), strings.Contains(dict, "/Length1"):
		return "", true
	case strings.Contains(dict, "/FlateDecode"):
		return pdfInflate(raw)
	case strings.Contains(dict, "/Filter"):
		return "", false
	}
	return raw, true
}

// pdfInflate inflates a FlateDecode stream, bounded by maxPDFInflate. A
// stream cut short still gives what was inflated before the cut.
func pdfInflate(raw string) (string, bool) {
	zr, err := zlib.NewReader(strings.NewReader(raw))
	if err != nil {
		return "", false
	}
	defer func() { _ = zr.Close() }()
	out, err := io.ReadAll(io.LimitReader(zr, maxPDFInflate))
	if err != nil && len(out) == 0 {
		return "", false
	}
	return string(out), true
}

// pdfHasTextOperators reports whether a content stream draws text: a BT ... ET
// block. Without it, parenthesised strings are not text — they are names in a
// font program or data in some other stream.
func pdfHasTextOperators(content string) bool {
	return strings.Contains(content, "BT") && strings.Contains(content, "ET")
}

// latin1Fallback keeps valid UTF-8 as it is and reads anything else byte by
// byte as Latin-1: PDF strings are bytes in a font's encoding, most often
// close to Latin-1, and a stored document must be UTF-8.
func latin1Fallback(s string) string {
	if utf8.ValidString(s) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		b.WriteRune(rune(s[i]))
	}
	return b.String()
}

// extractPDFText extracts text from a PDF content stream (between parentheses in text operators).
func extractPDFText(content string, result *strings.Builder) {
	i := 0
	for i < len(content) {
		if content[i] == '(' {
			// Find matching closing paren (handle escaped parens)
			depth := 1
			start := i + 1
			j := start
			for j < len(content) && depth > 0 {
				if content[j] == '\\' {
					j += 2
					continue
				}
				switch content[j] {
				case '(':
					depth++
				case ')':
					depth--
				}
				if depth > 0 {
					j++
				}
			}
			if depth == 0 {
				text := content[start:j]
				// Unescape
				text = strings.ReplaceAll(text, "\\(", "(")
				text = strings.ReplaceAll(text, "\\)", ")")
				text = strings.ReplaceAll(text, "\\\\", "\\")
				text = strings.ReplaceAll(text, "\\n", "\n")
				text = strings.ReplaceAll(text, "\\r", "\r")
				text = strings.ReplaceAll(text, "\\t", "\t")
				if strings.TrimSpace(text) != "" {
					result.WriteString(text)
				}
				i = j + 1
				continue
			}
		}

		// Check for Td/TD (text positioning — likely a new line)
		if i+2 < len(content) && (content[i] == 'T' && (content[i+1] == 'd' || content[i+1] == 'D')) {
			if i+2 >= len(content) || content[i+2] == ' ' || content[i+2] == '\n' || content[i+2] == '\r' {
				result.WriteByte('\n')
			}
		}

		i++
	}
}
