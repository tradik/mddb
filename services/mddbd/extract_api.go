package main

import (
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
)

// PUT /v1/extract/process (#451): the external document loader contract of
// Open WebUI — the raw file as the body, its type in Content-Type, its name
// in X-Filename — answered with {"page_content", "metadata"}, or a list of
// them when the file holds several documents (a mailbox, an email's
// attachments). The admin enters the URL without /process; the caller adds it.
//
// It is the conversion /v1/upload runs (convertToMarkdown, convertedDocs),
// without the write: nothing is stored.

// extractMediaTypes maps the media types of the formats the converters read
// to their names; syncContentTypes has the web ones. Anything else falls
// back to the file name's extension, and the converter still recognises PDF
// and the zip formats by their content.
var extractMediaTypes = map[string]string{
	"application/vnd.openxmlformats-officedocument.wordprocessingml.document":   "docx",
	"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet":         "xlsx",
	"application/vnd.openxmlformats-officedocument.presentationml.presentation": "pptx",
	"application/vnd.oasis.opendocument.text":                                   "odt",
	"application/vnd.oasis.opendocument.spreadsheet":                            "ods",
	"application/vnd.oasis.opendocument.presentation":                           "odp",
	"application/epub+zip": "epub", "application/rtf": "rtf", "text/rtf": "rtf",
	"message/rfc822": "eml", "application/mbox": "mbox", "text/tab-separated-values": "tsv",
	"application/yaml": "yaml", "text/yaml": "yaml", "application/x-tex": "tex",
}

// extractDocument is one document of the answer.
type extractDocument struct {
	PageContent string         `json:"page_content"`
	Metadata    map[string]any `json:"metadata"`
}

// handleExtract answers PUT /v1/extract/process.
func (s *Server) handleExtract(w http.ResponseWriter, r *http.Request) {
	if !allowMethod(w, r, http.MethodPut) {
		return
	}
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, extractMaxSize(r.URL.Query().Get("maxSize"))))
	if err != nil {
		writeBodyError(w, err) // 413 over the limit
		return
	}
	if len(data) == 0 {
		writeAPIError(w, http.StatusBadRequest, "the request body is empty: send the file's bytes")
		return
	}
	contentType := r.Header.Get(headerContentType)
	name := extractFilename(r.Header.Get("X-Filename"))
	format, err := resolveFormat(extractFormat(contentType, name), data)
	if err != nil {
		writeAPIError(w, http.StatusUnsupportedMediaType, unsupportedMessage(err))
		return
	}
	conv, err := convertToMarkdown(format, data, false)
	if err != nil {
		writeAPIError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	docs := extractDocuments(conv, name, contentType)
	if len(docs) == 1 {
		ok(w, docs[0])
		return
	}
	ok(w, docs)
}

// unsupportedMessage is a format refusal that names what is supported.
func unsupportedMessage(err error) string {
	if errors.Is(err, errUnsupportedFormat) {
		return err.Error() // already lists the formats
	}
	return fmt.Sprintf("%v (supported: %s)", err, supportedFormatsHint)
}

// extractMaxSize is the upload limit: 10 MB, or ?maxSize up to the 100 MB
// hard cap. MDDB_MAX_BODY_BYTES still bounds every request body.
func extractMaxSize(v string) int64 {
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil || n <= 0 {
		return defaultMaxUploadSize
	}
	return min(n, maxUploadSizeLimit)
}

// extractFilename is X-Filename unquoted, without any directory.
func extractFilename(raw string) string {
	if name, err := url.PathUnescape(raw); err == nil {
		raw = name
	}
	if raw = path.Base(strings.ReplaceAll(raw, `\`, "/")); raw == "." || raw == "/" {
		return ""
	}
	return raw
}

// extractFormat names the file's format: its media type when the converters
// know it, else its name's extension.
func extractFormat(contentType, name string) string {
	mt, _, _ := mime.ParseMediaType(contentType)
	if f, ok := syncContentTypes[mt]; ok {
		return f
	}
	if f, ok := extractMediaTypes[mt]; ok {
		return f
	}
	return formatFromFilename(name)
}

// extractDocuments is the conversion as the answer's documents: the file's
// own, then its parts, with the stored documents' meta flattened to strings
// (a list joins with ", ") and what the caller needs to cite it.
func extractDocuments(conv conversion, name, contentType string) []extractDocument {
	stored := convertedDocs(conv, name, nil)
	out := make([]extractDocument, 0, len(stored))
	for _, d := range stored {
		meta := map[string]any{"source": d.Key, "converter": d.Format}
		if contentType != "" {
			meta["mime"] = contentType
		}
		for k, v := range d.Meta {
			meta[k] = strings.Join(v, ", ")
		}
		if d.Converted {
			meta["conversionConfidence"] = d.Confidence // a number, not the stored string
		}
		out = append(out, extractDocument{PageContent: d.Markdown, Metadata: meta})
	}
	return out
}
