package main

import (
	"fmt"
	"path"
	"strings"
)

// One conversion path (AGT-001).
//
// Upload, the MCP upload_file tool and ingest each used to decide for
// themselves what a file's format meant. Two of them carried a copy of the
// same switch, the copies drifted (MCP never learned text-only), and ingest
// had no converter at all, so `mddb-cli autoindex` sent PDFs and Word files as
// "binary" to nowhere and HTML with its tags into the index. convertToMarkdown
// is the one switch; every surface calls it.

// supportedFormatsHint is the list an "unsupported format" error names.
const supportedFormatsHint = "md, txt, html, pdf, docx, odt, rtf, yaml, log, lex, tex"

// conversion is what the converters made of one file.
type conversion struct {
	// Markdown is the document body to store.
	Markdown string
	// Converted is false for formats stored as given (md, txt): only those
	// may still carry frontmatter worth extracting.
	Converted bool
}

// formatFromFilename names a file's format from its extension: lower case,
// without the dot, with the aliases folded ("htm" is "html").
func formatFromFilename(filename string) string {
	return normalizeFormat(path.Ext(filename))
}

// normalizeFormat folds a format name or extension to the name responses and
// meta report: lower case, no dot, "htm" as "html".
func normalizeFormat(format string) string {
	format = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(format), "."))
	if format == "htm" {
		return "html"
	}
	return format
}

// convertToMarkdown turns a file of the given format into Markdown.
//
// textOnly (RAG-004) extracts the words without rebuilding structure; it
// changes html, docx and odt. pdf and rtf build no structure to begin with, so
// they are the same either way.
func convertToMarkdown(format string, data []byte, textOnly bool) (conversion, error) {
	var md string
	var err error
	format = normalizeFormat(format)
	switch format {
	case "md", "markdown", "txt", "text", "":
		return conversion{Markdown: string(data)}, nil
	case "yaml", "yml", "log", "lex":
		// Text-based formats: fenced, so the structure survives rendering.
		md = "```" + format + "\n" + string(data) + "\n```"
	case "tex", "latex":
		md = texToMarkdown(data)
	case "html":
		if textOnly {
			md = htmlToText(data)
		} else {
			md = htmlToMarkdown(data)
		}
	case "pdf":
		md, err = pdfToMarkdown(data)
	case "docx":
		if textOnly {
			md, err = docxToText(data)
		} else {
			md, err = docxToMarkdown(data)
		}
	case "odt":
		if textOnly {
			md, err = odtToText(data)
		} else {
			md, err = odtToMarkdown(data)
		}
	case "rtf":
		md = rtfToMarkdown(data)
	default:
		return conversion{}, fmt.Errorf("unsupported format: %s (supported: %s)", format, supportedFormatsHint)
	}
	if err != nil {
		return conversion{}, fmt.Errorf("%s conversion: %w", format, err)
	}
	return conversion{Markdown: md, Converted: true}, nil
}
