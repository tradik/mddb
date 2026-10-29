package main

import (
	"errors"
	"fmt"
	"io"
	"mddb/internal/storage"
	"mime/multipart"
	"net/http"
	"path"
	"strconv"
	"strings"

	json "mddb/internal/jsonx"
)

const (
	defaultMaxUploadSize = 10 << 20  // 10 MB
	maxUploadSizeLimit   = 100 << 20 // 100 MB hard cap
)

// uploadBodyLimit caps a whole /v1/upload request body: the per-file hard cap
// times ten, for multi-file uploads and multipart overhead. A variable only so
// a test can lower it.
var uploadBodyLimit int64 = maxUploadSizeLimit * 10

// UploadResponse is the JSON response for a single uploaded file.
type UploadResponse struct {
	Key       string      `json:"key"`
	Format    string      `json:"format"`
	Converted bool        `json:"converted"`
	Doc       storage.Doc `json:"document"`
	// ConversionConfidence (0–1) and ConversionWarnings say how well a
	// converted file converted (GO-046); absent for Markdown and text.
	ConversionConfidence *float64 `json:"conversionConfidence,omitempty"`
	ConversionWarnings   []string `json:"conversionWarnings,omitempty"`
	// Parts are the further documents the file held: a mailbox's messages,
	// an email's convertible attachments, each with meta.parent.
	Parts []UploadResponse `json:"parts,omitempty"`
}

// UploadBatchResponse is the JSON response for multi-file upload.
type UploadBatchResponse struct {
	Added   int              `json:"added"`
	Updated int              `json:"updated"`
	Failed  int              `json:"failed"`
	Errors  []string         `json:"errors,omitempty"`
	Results []UploadResponse `json:"results,omitempty"`
}

// handleUpload handles POST /v1/upload (multipart/form-data).
//
// Form fields:
//
//	file / files[]  – one or more files (required)
//	collection      – target collection (required)
//	lang            – document language (required)
//	key             – document key; derived from filename when empty (optional)
//	meta            – JSON-encoded metadata map (optional)
//	ttl             – TTL in seconds (optional)
//	maxSize         – per-file size limit in bytes (optional, default 10 MB, max 100 MB)
//
// Supported file types: see supportedFormatsHint (document_convert.go).
// Default (md/txt) is stored as-is; others are converted to markdown.
func (s *Server) handleUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Cap the body before anything reads it. The FormValue("maxSize") below
	// parses the whole multipart body, so a cap applied after it (as this
	// handler used to) wrapped a body that had already been read to the end:
	// an upload of any size reached the multipart temp files on disk.
	r.Body = http.MaxBytesReader(w, r.Body, uploadBodyLimit)

	// Determine max upload size
	maxSize := int64(defaultMaxUploadSize)
	if v := r.FormValue("maxSize"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n > 0 {
			maxSize = n
		}
	}
	if maxSize > maxUploadSizeLimit {
		maxSize = maxUploadSizeLimit
	}

	if err := r.ParseMultipartForm(maxSize); err != nil { // #nosec G120 -- bounded by http.MaxBytesReader above
		bad(w, fmt.Errorf("parse multipart: %w", err))
		return
	}
	defer func() {
		if r.MultipartForm != nil {
			_ = r.MultipartForm.RemoveAll()
		}
	}()

	collection := s.Aliases.Resolve(r.FormValue("collection")) // GO-044: multipart bodies skip the alias middleware
	lang := r.FormValue("lang")
	if collection == "" || lang == "" {
		bad(w, errors.New("missing required fields: collection, lang"))
		return
	}

	// Check write permission
	if s.AuthManager != nil {
		if err := s.AuthManager.CheckPermission(r.Context(), collection, PermWrite); err != nil {
			http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
			return
		}
	}

	// Parse optional meta JSON
	var meta map[string][]string
	if raw := r.FormValue("meta"); raw != "" {
		if err := json.Unmarshal([]byte(raw), &meta); err != nil {
			bad(w, fmt.Errorf("invalid meta JSON: %w", err))
			return
		}
	}

	keyOverride := r.FormValue("key")

	var ttl int64
	if v := r.FormValue("ttl"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			ttl = n
		}
	}

	// RAG-004: the ingest profile, as a multipart field. A typo must not fall
	// back silently — a caller who asked for "fast" and got the default would
	// see a slow upload and no reason why. ResolveIngestProfile validates the
	// name and returns that error.
	resolved, err := ResolveIngestProfile(&IngestOptionsHTTP{
		Profile:  r.FormValue("profile"),
		TextOnly: r.FormValue("textOnly") == "true",
	})
	if err != nil {
		bad(w, err)
		return
	}

	// Collect files from both "file" and "files[]" field names
	var files []*multipart.FileHeader
	for _, name := range []string{"file", "files[]", "files"} {
		if fhs, found := r.MultipartForm.File[name]; found {
			files = append(files, fhs...)
		}
	}

	if len(files) == 0 {
		bad(w, errors.New("no files uploaded; use field name 'file' or 'files[]'"))
		return
	}

	// Single file → simple response; multiple → batch response
	if len(files) == 1 {
		res, err := s.processUploadedFile(files[0], collection, lang, keyOverride, meta, ttl, maxSize, resolved.TextOnly)
		if err != nil {
			bad(w, err)
			return
		}
		s.Metrics.IncOp("upload")
		ok(w, res)
		return
	}

	// Multi-file upload
	resp := UploadBatchResponse{}
	for _, fh := range files {
		res, err := s.processUploadedFile(fh, collection, lang, "", meta, ttl, maxSize, resolved.TextOnly)
		if err != nil {
			resp.Failed++
			resp.Errors = append(resp.Errors, fmt.Sprintf("%s: %s", fh.Filename, err.Error()))
			continue
		}
		resp.Results = append(resp.Results, *res)
		resp.Added++ // simplified: treats all as adds
	}
	if len(resp.Errors) == 0 {
		resp.Errors = nil
	}

	s.Metrics.IncOp("upload")
	ok(w, resp)
}

// processUploadedFile reads a single multipart file, converts to markdown if needed,
// and stores via addDocument.
func (s *Server) processUploadedFile(fh *multipart.FileHeader, collection, lang, keyOverride string, baseMeta map[string][]string, ttl, maxSize int64, textOnly bool) (*UploadResponse, error) {
	if fh.Size > maxSize {
		return nil, fmt.Errorf("file %q exceeds max size (%d bytes)", fh.Filename, maxSize)
	}

	f, err := fh.Open()
	if err != nil {
		return nil, fmt.Errorf("open file: %w", err)
	}
	defer func() { _ = f.Close() }()

	data, err := io.ReadAll(io.LimitReader(f, maxSize+1))
	if err != nil {
		return nil, fmt.Errorf("read file: %w", err)
	}
	if int64(len(data)) > maxSize {
		return nil, fmt.Errorf("file %q exceeds max size (%d bytes)", fh.Filename, maxSize)
	}

	conv, err := convertToMarkdown(formatFromFilename(fh.Filename), data, textOnly)
	if err != nil {
		return nil, err
	}
	contentMD, converted := conv.Markdown, conv.Converted

	// Extract frontmatter for md/txt files
	if !converted {
		fmMeta, body := parseFrontmatter(contentMD)
		if fmMeta != nil {
			contentMD = body
			// Merge frontmatter (base meta overrides)
			if baseMeta == nil {
				baseMeta = fmMeta
			} else {
				for k, v := range fmMeta {
					if _, exists := baseMeta[k]; !exists {
						baseMeta[k] = v
					}
				}
			}
		}
	}

	// Derive key
	key := keyOverride
	if key == "" {
		key = deriveKeyFromFilename(fh.Filename)
	}
	if key == "" {
		return nil, errors.New("cannot derive key from filename; provide key explicitly")
	}

	// Clone meta and add upload metadata
	docMeta := make(map[string][]string)
	for k, v := range baseMeta {
		docMeta[k] = v
	}
	docMeta["upload_format"] = []string{conv.Format}
	docMeta["upload_filename"] = []string{fh.Filename}
	if converted {
		docMeta["upload_converted"] = []string{"true"}
	}

	// The file's own document first, then what it held (GO-046).
	conv.Markdown = contentMD
	var res *UploadResponse
	for _, d := range convertedDocs(conv, key, docMeta) {
		if err := s.SchemaManager.Validate(collection, d.Meta); err != nil {
			return res, fmt.Errorf("%s: %w", d.Key, err)
		}
		saved, _, err := s.addDocument(collection, d.Key, lang, d.Meta, d.Markdown, ttl, true)
		if err != nil {
			return res, fmt.Errorf("%s: %w", d.Key, err)
		}
		r := UploadResponse{Key: d.Key, Format: d.Format, Converted: d.Converted, Doc: saved}
		if d.Converted {
			confidence := d.Confidence
			r.ConversionConfidence, r.ConversionWarnings = &confidence, d.Warnings
		}
		if res == nil {
			res = &r
		} else {
			res.Parts = append(res.Parts, r)
		}
	}
	return res, nil
}

// deriveKeyFromFilename strips the extension and returns a URL-safe key.
func deriveKeyFromFilename(filename string) string {
	base := path.Base(filename)
	ext := path.Ext(base)
	if ext != "" {
		base = base[:len(base)-len(ext)]
	}
	base = strings.TrimSpace(base)
	// "." and ".." are not names — they are path operators, and consumers
	// that write a file per document key (ssg, wpexporter) would resolve them
	// against their output directory. A filename that yields one of these has
	// no usable key, and inventing one puts the document somewhere the caller
	// cannot find it (TEST-002).
	if base == "" || base == "." || base == ".." {
		return ""
	}
	// Replace spaces with hyphens, lowercase
	base = strings.ToLower(base)
	base = strings.ReplaceAll(base, " ", "-")
	return base
}

// ---------------------------------------------------------------------------
// Lightweight format converters (zero external dependencies)
// ---------------------------------------------------------------------------
