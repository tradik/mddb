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

	json "github.com/goccy/go-json"
)

const (
	defaultMaxUploadSize = 10 << 20  // 10 MB
	maxUploadSizeLimit   = 100 << 20 // 100 MB hard cap
)

// UploadResponse is the JSON response for a single uploaded file.
type UploadResponse struct {
	Key       string      `json:"key"`
	Format    string      `json:"format"`
	Converted bool        `json:"converted"`
	Doc       storage.Doc `json:"document"`
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
// Supported file types: .md .txt .html .htm .pdf .docx .odt .rtf .yaml .yml .log .lex .tex .latex
// Default (md/txt) is stored as-is; others are converted to markdown.
func (s *Server) handleUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

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
	r.Body = http.MaxBytesReader(w, r.Body, maxSize*10) // account for multipart overhead

	if err := r.ParseMultipartForm(maxSize); err != nil { // #nosec G120 -- bounded by http.MaxBytesReader above
		bad(w, fmt.Errorf("parse multipart: %w", err))
		return
	}
	defer func() {
		if r.MultipartForm != nil {
			_ = r.MultipartForm.RemoveAll()
		}
	}()

	collection := r.FormValue("collection")
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
	// see a slow upload and no reason why.
	profile := r.FormValue("profile")
	if err := ValidateIngestProfile(profile); err != nil {
		bad(w, err)
		return
	}
	resolved, err := ResolveIngestProfile(&IngestOptionsHTTP{
		Profile:  profile,
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

	ext := strings.ToLower(path.Ext(fh.Filename))
	format := strings.TrimPrefix(ext, ".")
	if format == "htm" {
		format = "html"
	}

	// Convert to markdown based on format
	var contentMD string
	var converted bool

	switch format {
	case "md", "markdown", "txt", "text", "":
		// Already markdown / plain text — store as-is
		contentMD = string(data)
	case "yaml", "yml", "log", "lex":
		// Text-based formats — wrap in code block for structure
		contentMD = "```" + format + "\n" + string(data) + "\n```"
		converted = true
	case "tex", "latex":
		contentMD = texToMarkdown(data)
		converted = true
	case "html":
		// RAG-004: text-only keeps the words and drops the shape, which is
		// both faster and far more tolerant of malformed markup.
		if textOnly {
			contentMD = htmlToText(data)
		} else {
			contentMD = htmlToMarkdown(data)
		}
		converted = true
	case "pdf":
		// No text-only variant: pdfToMarkdown already extracts raw text from
		// content streams and builds no structure, so a second name for it
		// would promise a speedup that does not exist.
		contentMD, err = pdfToMarkdown(data)
		if err != nil {
			return nil, fmt.Errorf("pdf conversion: %w", err)
		}
		converted = true
	case "docx":
		if textOnly {
			contentMD, err = docxToText(data)
		} else {
			contentMD, err = docxToMarkdown(data)
		}
		if err != nil {
			return nil, fmt.Errorf("docx conversion: %w", err)
		}
		converted = true
	case "odt":
		if textOnly {
			contentMD, err = odtToText(data)
		} else {
			contentMD, err = odtToMarkdown(data)
		}
		if err != nil {
			return nil, fmt.Errorf("odt conversion: %w", err)
		}
		converted = true
	case "rtf":
		contentMD = rtfToMarkdown(data)
		converted = true
	default:
		return nil, fmt.Errorf("unsupported file format: %s (supported: md, txt, html, pdf, docx, odt, rtf, yaml, log, lex, tex)", format)
	}

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
	docMeta["upload_format"] = []string{format}
	docMeta["upload_filename"] = []string{fh.Filename}
	if converted {
		docMeta["upload_converted"] = []string{"true"}
	}

	// Validate schema
	if err := s.SchemaManager.Validate(collection, docMeta); err != nil {
		return nil, err
	}

	// Store
	saved, _, err := s.addDocument(collection, key, lang, docMeta, contentMD, ttl, true)
	if err != nil {
		return nil, err
	}

	return &UploadResponse{
		Key:       key,
		Format:    format,
		Converted: converted,
		Doc:       saved,
	}, nil
}

// deriveKeyFromFilename strips the extension and returns a URL-safe key.
func deriveKeyFromFilename(filename string) string {
	base := path.Base(filename)
	ext := path.Ext(base)
	if ext != "" {
		base = base[:len(base)-len(ext)]
	}
	base = strings.TrimSpace(base)
	if base == "" || base == "." {
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
