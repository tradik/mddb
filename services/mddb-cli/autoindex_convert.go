package main

import (
	"archive/zip"
	"bytes"
	"io"
	"os"
	"path"
	"strings"
)

// Recognising the files the server converts (AGT-001).
//
// The server owns the converters; the CLI only has to know which files to
// hand over as files rather than as text. It decides by content where the
// format has a signature — a PDF named .txt is still a PDF, and a .docx that
// is not a zip is not a Word file — and by extension for markup formats that
// are text (HTML, TeX, CSV, mail), where sending them raw would index their
// markup.

// markupExtensions are text formats the server converts: sent as they are,
// their markup would land in the index as words, a table as a run of
// separators, and a mailbox as one document with every header in it.
var markupExtensions = map[string]string{
	".html": "html", ".htm": "html", ".xhtml": "html",
	".tex": "tex", ".latex": "tex",
	".csv": "csv", ".tsv": "tsv",
	".eml": "eml", ".mbox": "mbox",
}

// odfMimeTypes maps the first entry of an OpenDocument or EPUB zip to the
// format the server converts it from.
var odfMimeTypes = map[string]string{
	"application/vnd.oasis.opendocument.text":         "odt",
	"application/vnd.oasis.opendocument.spreadsheet":  "ods",
	"application/vnd.oasis.opendocument.presentation": "odp",
	"application/epub+zip":                            "epub",
}

// ooxmlParts maps the main part of an Office Open XML zip to its format.
var ooxmlParts = map[string]string{
	"word/document.xml": "docx", "xl/workbook.xml": "xlsx", "ppt/presentation.xml": "pptx",
}

// sniffFile reads the start of a file and says what it is: the format the
// server converts it from ("" for plain text), or binary when it is neither
// text nor a format the server knows.
func sniffFile(abs string) (format string, binary bool) {
	f, err := os.Open(abs) // #nosec G304 -- a file under the directory the user asked to index
	if err != nil {
		return "", true
	}
	defer func() { _ = f.Close() }()
	head := make([]byte, sniffBytes)
	n, err := io.ReadFull(f, head)
	if err != nil && err != io.ErrUnexpectedEOF && err != io.EOF {
		return "", true
	}
	head = head[:n]
	switch {
	case bytes.HasPrefix(head, []byte("%PDF-")):
		return "pdf", false
	case bytes.HasPrefix(head, []byte(`{\rtf`)):
		return "rtf", false
	case bytes.HasPrefix(head, []byte("PK\x03\x04")):
		format := zipFormat(abs)
		return format, format == ""
	case bytes.IndexByte(head, 0) >= 0:
		// A NUL byte: binary. The extension is not trusted — a .txt can be a
		// dump and a script can have none.
		return "", true
	}
	return markupExtensions[strings.ToLower(path.Ext(abs))], false
}

// zipFormat names the document format a zip holds, or "" for any other zip.
func zipFormat(abs string) string {
	r, err := zip.OpenReader(abs)
	if err != nil {
		return ""
	}
	defer func() { _ = r.Close() }()
	for _, f := range r.File {
		if format := ooxmlParts[f.Name]; format != "" {
			return format
		}
		if f.Name == "mimetype" {
			return odfMimeTypes[strings.TrimSpace(string(readSmall(f)))]
		}
	}
	return ""
}

// readSmall reads a zip entry of at most 256 bytes — enough for a mimetype,
// and a bound on what a hostile archive can make the CLI inflate.
func readSmall(f *zip.File) []byte {
	rc, err := f.Open()
	if err != nil {
		return nil
	}
	defer func() { _ = rc.Close() }()
	data, _ := io.ReadAll(io.LimitReader(rc, 256))
	return data
}
