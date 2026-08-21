package main

import (
	"bufio"
	"compress/bzip2"
	"encoding/xml"
	"errors"
	"fmt"
	json "github.com/goccy/go-json"
	"io"
	"log/slog"
	"mddb/internal/envconf"
	"mddb/internal/wikitext"
	proto "mddb/proto"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// wikiImportBatchSize is the number of pages buffered before flushing to storage.
// Large batch size for performance with multi-million document imports.
const wikiImportBatchSize = 500

// wikiProgressInterval controls how often progress is logged during import.
const wikiProgressInterval = 10000

// SEC-006 defaults: server-side caps on a wiki import. They bound a bz2
// decompression bomb and an unbounded page count; both are overridable via
// MDDB_WIKI_MAX_DECOMPRESSED_BYTES / MDDB_WIKI_MAX_PAGES for full-dump imports.
const (
	wikiDefaultMaxDecompressedBytes int64 = 4 << 30 // 4 GiB of decompressed XML
	wikiDefaultMaxPages                   = 500000
)

// errWikiDecompressedLimit is returned once a wiki import's decompressed byte
// budget is exceeded, so the parse stops with a controlled error instead of
// silently truncating the XML.
var errWikiDecompressedLimit = errors.New("decompressed size limit exceeded")

// cappedReader returns errWikiDecompressedLimit once more than `limit` bytes
// have been read from the wrapped reader (SEC-006).
type cappedReader struct {
	r     io.Reader
	limit int64
	read  int64
}

func (c *cappedReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.read += int64(n)
	if c.read > c.limit {
		return n, errWikiDecompressedLimit
	}
	return n, err
}

// WikiImportRequest is the JSON / multipart request body for /v1/import-wiki.
type WikiImportRequest struct {
	Collection    string `json:"collection"`
	Lang          string `json:"lang"`
	Namespaces    []int  `json:"namespaces,omitempty"` // default [0] (articles only)
	SkipRedirects bool   `json:"skipRedirects,omitempty"`
	SkipFTS       bool   `json:"skipFts,omitempty"`   // skip FTS indexing (faster bulk import)
	MaxPages      int    `json:"maxPages,omitempty"`  // 0 = unlimited
	BatchSize     int    `json:"batchSize,omitempty"` // default 500
}

// WikiImportResponse is the JSON response for /v1/import-wiki.
type WikiImportResponse struct {
	Imported   int      `json:"imported"`
	Skipped    int      `json:"skipped"`
	Failed     int      `json:"failed"`
	Errors     []string `json:"errors,omitempty"`
	Collection string   `json:"collection"`
	DurationMs int64    `json:"durationMs"`
}

// MediaWiki XML structures for streaming parse.
type mwPage struct {
	Title    string      `xml:"title"`
	NS       int         `xml:"ns"`
	ID       int64       `xml:"id"`
	Redirect *mwRedirect `xml:"redirect"`
	Revision mwRevision  `xml:"revision"`
}

type mwRedirect struct {
	Title string `xml:"title,attr"`
}

type mwRevision struct {
	ID          int64         `xml:"id"`
	Timestamp   string        `xml:"timestamp"`
	Text        string        `xml:"text"`
	Comment     string        `xml:"comment"`
	Contributor mwContributor `xml:"contributor"`
}

type mwContributor struct {
	Username string `xml:"username"`
	ID       int64  `xml:"id"`
	IP       string `xml:"ip"`
}

// handleWikiImport handles POST /v1/import-wiki.
//
// Accepts either:
//   - multipart/form-data with a "file" field (XML or .xml.bz2) + JSON params
//   - application/octet-stream body with query params
//
// Query/form params: collection, lang, namespaces (comma-sep), skipRedirects, maxPages, batchSize
func (s *Server) handleWikiImport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	start := time.Now()
	req, reader, filename, err := s.parseWikiImportRequest(r)
	if err != nil {
		bad(w, err)
		return
	}
	defer func() { _ = r.Body.Close() }()

	if req.Collection == "" {
		bad(w, errors.New("missing collection"))
		return
	}
	if req.Lang == "" {
		bad(w, errors.New("missing lang"))
		return
	}

	// Auth check
	if s.AuthManager != nil {
		if err := s.AuthManager.CheckPermission(r.Context(), req.Collection, PermWrite); err != nil {
			http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
			return
		}
	}

	// Defaults
	if len(req.Namespaces) == 0 {
		req.Namespaces = []int{0}
	}
	batchSize := req.BatchSize
	if batchSize <= 0 {
		batchSize = wikiImportBatchSize
	}

	// SEC-006: clamp page count to a server default so a client can't request
	// (or default into) an unbounded import.
	serverMaxPages := envconf.Int("MDDB_WIKI_MAX_PAGES", wikiDefaultMaxPages)
	if req.MaxPages <= 0 || req.MaxPages > serverMaxPages {
		req.MaxPages = serverMaxPages
	}

	// Auto-detect bz2 compression. Wrap the reader in a cappedReader so a
	// decompression bomb (bz2 expands 10–50×) stops at a byte budget with a
	// controlled error instead of silently truncating or exhausting resources.
	maxDecompressed := envconf.Int64("MDDB_WIKI_MAX_DECOMPRESSED_BYTES", wikiDefaultMaxDecompressedBytes)
	rawReader := reader
	if strings.HasSuffix(strings.ToLower(filename), ".bz2") {
		rawReader = bzip2.NewReader(reader)
	}
	capped := &cappedReader{r: rawReader, limit: maxDecompressed}
	var xmlReader io.Reader = capped

	// Build namespace filter set
	nsAllow := make(map[int]bool, len(req.Namespaces))
	for _, ns := range req.Namespaces {
		nsAllow[ns] = true
	}

	resp := WikiImportResponse{Collection: req.Collection}

	// Streaming XML parse
	decoder := xml.NewDecoder(bufio.NewReaderSize(xmlReader, 256*1024)) // #nosec G709 -- trusted wiki XML input, streaming parse
	decoder.CharsetReader = charsetReader

	var batch []*proto.BatchDocument
	totalProcessed := 0
	lastProgress := time.Now()
	flushBatch := func() {
		if len(batch) == 0 {
			return
		}
		_, processed, err := s.processBatchWithDocs(r.Context(), req.Collection, batch)
		if err != nil {
			resp.Failed += len(batch)
			if len(resp.Errors) < 100 {
				resp.Errors = append(resp.Errors, err.Error())
			}
		} else {
			for _, p := range processed {
				if p != nil {
					resp.Imported++
				} else {
					resp.Failed++
				}
			}
			s.firePostBatchHooks(req.Collection, processed, postBatchOptions{
				SkipEmbeddings: true,
				SkipFTS:        req.SkipFTS,
				SkipWebhooks:   true,
			})
		}
		batch = batch[:0]
		totalProcessed = resp.Imported + resp.Failed + resp.Skipped
		if totalProcessed%wikiProgressInterval < batchSize || time.Since(lastProgress) > 30*time.Second {
			elapsed := time.Since(start)
			rate := float64(totalProcessed) / elapsed.Seconds()
			slog.Info("wiki import progress",
				"processed", totalProcessed, "imported", resp.Imported, "skipped", resp.Skipped,
				"failed", resp.Failed, "pagesPerSec", rate, "elapsed", elapsed.Round(time.Second))
			lastProgress = time.Now()
		}
	}

	decompressLimitHit := false
	for {
		tok, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			if errors.Is(err, errWikiDecompressedLimit) {
				decompressLimitHit = true
			}
			resp.Errors = append(resp.Errors, fmt.Sprintf("xml parse: %s", err.Error()))
			break
		}

		se, ok := tok.(xml.StartElement)
		if !ok || se.Name.Local != "page" {
			continue
		}

		var page mwPage
		if err := decoder.DecodeElement(&page, &se); err != nil {
			resp.Failed++
			resp.Errors = append(resp.Errors, fmt.Sprintf("decode page: %s", err.Error()))
			continue
		}

		// Namespace filter
		if !nsAllow[page.NS] {
			resp.Skipped++
			continue
		}

		// Skip redirects if requested
		if req.SkipRedirects && page.Redirect != nil {
			resp.Skipped++
			continue
		}

		// Skip empty pages
		if strings.TrimSpace(page.Revision.Text) == "" {
			resp.Skipped++
			continue
		}

		// Validate UTF-8
		text := page.Revision.Text
		if !utf8.ValidString(text) {
			text = strings.ToValidUTF8(text, "")
		}

		// Convert wikitext to markdown
		markdown := wikitext.ToMarkdown(text)

		// Build key from title
		key := wikiTitleToKey(page.Title)

		// Build metadata
		meta := map[string]*proto.MetaValues{
			"source":      {Values: []string{"wikipedia"}},
			"wiki_id":     {Values: []string{strconv.FormatInt(page.ID, 10)}},
			"wiki_title":  {Values: []string{page.Title}},
			"wiki_ns":     {Values: []string{strconv.Itoa(page.NS)}},
			"wiki_rev_id": {Values: []string{strconv.FormatInt(page.Revision.ID, 10)}},
		}
		if page.Revision.Timestamp != "" {
			meta["wiki_timestamp"] = &proto.MetaValues{Values: []string{page.Revision.Timestamp}}
		}
		if page.Redirect != nil {
			meta["wiki_redirect"] = &proto.MetaValues{Values: []string{page.Redirect.Title}}
		}
		contributor := page.Revision.Contributor.Username
		if contributor == "" {
			contributor = page.Revision.Contributor.IP
		}
		if contributor != "" {
			meta["wiki_contributor"] = &proto.MetaValues{Values: []string{contributor}}
		}

		batch = append(batch, &proto.BatchDocument{
			Key:       key,
			Lang:      req.Lang,
			ContentMd: markdown,
			Meta:      meta,
		})

		if len(batch) >= batchSize {
			flushBatch()
		}

		// Check max pages limit
		if req.MaxPages > 0 && resp.Imported+resp.Failed >= req.MaxPages {
			break
		}
	}

	// Flush remaining
	flushBatch()

	resp.DurationMs = time.Since(start).Milliseconds()
	if len(resp.Errors) > 10 {
		resp.Errors = resp.Errors[:10] // cap errors in response
	}
	if len(resp.Errors) == 0 {
		resp.Errors = nil
	}

	slog.Info("wiki import finished",
		"collection", req.Collection, "imported", resp.Imported, "skipped", resp.Skipped,
		"failed", resp.Failed, "durationMs", resp.DurationMs)

	s.Metrics.IncOp("import-wiki")

	// SEC-006: a decompression-bomb stop is a client/payload error, not a
	// partial success — report it as 413 (with the partial counts) so callers
	// can distinguish it from a clean import.
	if decompressLimitHit {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusRequestEntityTooLarge)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error":    "decompressed size limit exceeded; raise MDDB_WIKI_MAX_DECOMPRESSED_BYTES to import larger dumps",
			"imported": resp.Imported,
			"skipped":  resp.Skipped,
			"failed":   resp.Failed,
		})
		return
	}

	ok(w, resp)
}

// parseWikiImportRequest extracts parameters and returns a reader for the XML data.
func (s *Server) parseWikiImportRequest(r *http.Request) (WikiImportRequest, io.Reader, string, error) {
	var req WikiImportRequest
	contentType := r.Header.Get("Content-Type")

	if strings.HasPrefix(contentType, "multipart/form-data") {
		// Cap multipart memory at 1GB (wiki dumps stream from disk, not memory)
		if err := r.ParseMultipartForm(1 << 30); err != nil { // #nosec G120 -- wiki dumps are large files, 1GB limit is intentional
			return req, nil, "", fmt.Errorf("parse multipart: %w", err)
		}

		req.Collection = r.FormValue("collection")
		req.Lang = r.FormValue("lang")
		req.SkipRedirects = r.FormValue("skipRedirects") == "true"
		req.SkipFTS = r.FormValue("skipFts") == "true"

		if v := r.FormValue("maxPages"); v != "" {
			if n, err := strconv.Atoi(v); err == nil {
				req.MaxPages = n
			}
		}
		if v := r.FormValue("batchSize"); v != "" {
			if n, err := strconv.Atoi(v); err == nil {
				req.BatchSize = n
			}
		}
		if v := r.FormValue("namespaces"); v != "" {
			for _, s := range strings.Split(v, ",") {
				if n, err := strconv.Atoi(strings.TrimSpace(s)); err == nil {
					req.Namespaces = append(req.Namespaces, n)
				}
			}
		}

		// Get file
		file, header, err := r.FormFile("file")
		if err != nil {
			return req, nil, "", fmt.Errorf("missing file field: %w", err)
		}
		return req, file, header.Filename, nil
	}

	// JSON body with params or query params + raw body
	if strings.HasPrefix(contentType, "application/json") {
		// Expect JSON with base64 data or stream follows
		bad := errors.New("for JSON requests use multipart/form-data with file upload")
		return req, nil, "", bad
	}

	// application/octet-stream or application/x-bzip2 — params from query string
	q := r.URL.Query()
	req.Collection = q.Get("collection")
	req.Lang = q.Get("lang")
	req.SkipRedirects = q.Get("skipRedirects") == "true"
	req.SkipFTS = q.Get("skipFts") == "true"
	if v := q.Get("maxPages"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			req.MaxPages = n
		}
	}
	if v := q.Get("batchSize"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			req.BatchSize = n
		}
	}
	if v := q.Get("namespaces"); v != "" {
		for _, s := range strings.Split(v, ",") {
			if n, err := strconv.Atoi(strings.TrimSpace(s)); err == nil {
				req.Namespaces = append(req.Namespaces, n)
			}
		}
	}

	filename := q.Get("filename")
	if filename == "" {
		// Infer from content type
		if strings.Contains(contentType, "bzip2") || strings.Contains(contentType, "bz2") {
			filename = "dump.xml.bz2"
		} else {
			filename = "dump.xml"
		}
	}

	return req, r.Body, filename, nil
}

// wikiTitleToKey converts a Wikipedia page title to a URL-safe document key.
func wikiTitleToKey(title string) string {
	key := strings.ToLower(title)
	key = strings.ReplaceAll(key, " ", "-")
	key = strings.ReplaceAll(key, "/", "-")
	key = strings.ReplaceAll(key, "'", "")
	key = strings.ReplaceAll(key, "\"", "")
	key = strings.ReplaceAll(key, "(", "")
	key = strings.ReplaceAll(key, ")", "")
	key = strings.ReplaceAll(key, ",", "")
	key = strings.ReplaceAll(key, ".", "")
	// Collapse multiple dashes
	for strings.Contains(key, "--") {
		key = strings.ReplaceAll(key, "--", "-")
	}
	key = strings.Trim(key, "-")
	return key
}

// charsetReader handles non-UTF-8 encodings in XML declarations.
func charsetReader(charset string, input io.Reader) (io.Reader, error) {
	if strings.EqualFold(charset, "utf-8") || strings.EqualFold(charset, "utf8") {
		return input, nil
	}
	// MediaWiki dumps are always UTF-8, but handle gracefully
	return input, nil
}
