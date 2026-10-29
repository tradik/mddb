package main

import (
	"archive/zip"
	"bytes"
	"errors"
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
const supportedFormatsHint = "md, txt, html, pdf, docx, odt, rtf, xlsx, ods, csv, tsv, pptx, odp, epub, eml, mbox, yaml, log, lex, tex"

// errUnsupportedFormat is wrapped by the error for a format with no converter.
var errUnsupportedFormat = errors.New("unsupported format")

// conversion is what the converters made of one file.
type conversion struct {
	// Format is the format the file was converted from: the declared one, or
	// what its content says it is.
	Format string
	// Markdown is the document body to store.
	Markdown string
	// Converted is false for formats stored as given (md, txt): only those
	// may still carry frontmatter worth extracting.
	Converted bool
	// Confidence (0–1) and Warnings measure a converted file (GO-046).
	Confidence float64
	Warnings   []string
	// Meta is what the file says about itself: an email's headers.
	Meta map[string][]string
	// Parts are further documents the file holds: a mailbox's messages, an
	// email's convertible attachments.
	Parts []conversionPart
}

// conversionPart is a document found inside another. Its key is the
// containing document's key plus Suffix; its meta.parent is that key plus
// ParentSuffix (empty: the containing document itself).
type conversionPart struct {
	Suffix, ParentSuffix string
	conversion
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

// textFormats are stored or converted as text; binaryFormats are containers
// whose converter reports what is wrong with a file that is not one.
var (
	textFormats = map[string]bool{"md": true, "markdown": true, "txt": true, "text": true, "": true,
		"yaml": true, "yml": true, "log": true, "lex": true, "tex": true, "latex": true, "html": true,
		"rtf": true, "csv": true, "tsv": true, "eml": true, "mbox": true}
	binaryFormats = map[string]bool{"pdf": true, "docx": true, "odt": true, "xlsx": true, "ods": true,
		"pptx": true, "odp": true, "epub": true}
)

// convertToMarkdown turns a file of the given format into Markdown, scored
// (GO-046), with any documents it holds as parts.
//
// textOnly (RAG-004) extracts the words without rebuilding structure; it
// changes html, docx, odt, epub and email HTML bodies.
func convertToMarkdown(format string, data []byte, textOnly bool) (conversion, error) {
	return convertDepth(format, data, textOnly, 0)
}

// convertDepth is convertToMarkdown at a nesting depth: an email attachment
// is converted one level down, and the depth bounds how far that recurses.
func convertDepth(format string, data []byte, textOnly bool, depth int) (conversion, error) {
	format, err := resolveFormat(format, data)
	if err != nil {
		return conversion{Format: format}, err
	}
	c := conversion{Format: format, Converted: true}
	var sig conversionSignals
	switch format {
	case "md", "markdown", "txt", "text", "":
		return conversion{Format: format, Markdown: string(data)}, nil
	case "yaml", "yml", "log", "lex":
		// Text-based formats: fenced, so the structure survives rendering.
		c.Markdown = "```" + format + "\n" + string(data) + "\n```"
	case "tex", "latex":
		c.Markdown = texToMarkdown(data)
	case "html":
		if textOnly {
			c.Markdown = htmlToText(data)
		} else {
			c.Markdown = htmlToMarkdown(data)
		}
	case "rtf":
		c.Markdown = rtfToMarkdown(data)
	case "csv", "tsv":
		c.Markdown, sig = csvToMarkdown(data, format)
	case "eml":
		err = emlConvert(&c, data, textOnly, depth)
		sig.Warnings = c.Warnings
	case "mbox":
		err = mboxConvert(&c, data, textOnly, depth)
		sig.Warnings = c.Warnings
	default:
		c.Markdown, sig, err = convertContainer(format, data, textOnly)
	}
	if err != nil {
		return conversion{Format: format}, fmt.Errorf("%s conversion: %w", format, err)
	}
	c.Confidence, c.Warnings = assessConversion(data, c.Markdown, sig)
	return c, nil
}

// convertContainer converts the zip and PDF formats.
func convertContainer(format string, data []byte, textOnly bool) (string, conversionSignals, error) {
	sig := conversionSignals{Container: true}
	var md string
	var err error
	switch format {
	case "pdf":
		return pdfToMarkdown(data)
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
	case "xlsx":
		return xlsxToMarkdown(data)
	case "ods":
		return odsToMarkdown(data)
	case "pptx":
		return pptxToMarkdown(data)
	case "odp":
		return odpToMarkdown(data)
	case "epub":
		return epubToMarkdown(data, textOnly)
	}
	return md, sig, err
}

// resolveFormat decides what a file is. Content wins where the format has a
// signature: a PDF named .txt is a PDF, and an XLSX sent as "docx" is a
// workbook. A declared container format without its signature is left to its
// converter, which says what is wrong; text with a NUL byte that no converter
// recognises is refused rather than indexed as bytes.
func resolveFormat(declared string, data []byte) (string, error) {
	declared = normalizeFormat(declared)
	if sniffed := sniffFormat(data); sniffed != "" && sniffed != "zip" {
		return sniffed, nil
	}
	switch {
	case binaryFormats[declared]:
		return declared, nil
	case !textFormats[declared]:
		return declared, fmt.Errorf("%w: %s (supported: %s)", errUnsupportedFormat, declared, supportedFormatsHint)
	case bytes.IndexByte(data[:min(len(data), 8<<10)], 0) >= 0:
		return declared, fmt.Errorf("binary content that is no format the server converts (declared %q)", declared)
	}
	return declared, nil
}

// sniffFormat names a format by its signature, "zip" for a zip that is no
// document format, or "" when the content has no signature.
func sniffFormat(data []byte) string {
	switch {
	case bytes.HasPrefix(data, []byte("%PDF-")):
		return "pdf"
	case bytes.HasPrefix(data, []byte(`{\rtf`)):
		return "rtf"
	case bytes.HasPrefix(data, []byte("PK\x03\x04")):
		return sniffZip(data)
	}
	return ""
}

// zipMimeTypes maps the mimetype entry of OpenDocument and EPUB files, and
// zipMainParts the main part of OOXML files, to their format.
var (
	zipMimeTypes = map[string]string{
		"application/vnd.oasis.opendocument.text":         "odt",
		"application/vnd.oasis.opendocument.spreadsheet":  "ods",
		"application/vnd.oasis.opendocument.presentation": "odp",
		"application/epub+zip":                            "epub",
	}
	zipMainParts = map[string]string{"word/document.xml": "docx", "xl/workbook.xml": "xlsx", "ppt/presentation.xml": "pptx"}
)

// sniffZip names the document format a zip holds, or "zip".
func sniffZip(data []byte) string {
	r, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "zip"
	}
	z := &zipDoc{format: "zip", files: map[string]*zip.File{}}
	for _, f := range r.File {
		z.files[f.Name] = f
		if format := zipMainParts[f.Name]; format != "" {
			return format
		}
	}
	if mt, err := z.read("mimetype"); err == nil && zipMimeTypes[strings.TrimSpace(string(mt))] != "" {
		return zipMimeTypes[strings.TrimSpace(string(mt))]
	}
	return "zip"
}
