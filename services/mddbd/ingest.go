package main

import (
	"context"
	"errors"
	"fmt"
	"hash/crc32"
	"net/http"
	"strconv"
	"time"

	"mddb/internal/storage"
	proto "mddb/proto"

	bolt "go.etcd.io/bbolt"
	json "mddb/internal/jsonx"
)

// ---- HTTP types for /v1/ingest ----

// IngestRequestHTTP is the HTTP request body for bulk document ingestion.
type IngestRequestHTTP struct {
	Collection string               `json:"collection"`
	Documents  []IngestDocumentHTTP `json:"documents"`
	Options    IngestOptionsHTTP    `json:"options,omitempty"`
}

// IngestDocumentHTTP represents a single document in an ingest request.
type IngestDocumentHTTP struct {
	URL                string              `json:"url"`
	Key                string              `json:"key,omitempty"`
	Lang               string              `json:"lang"`
	ContentMD          string              `json:"contentMd"`
	Meta               map[string][]string `json:"meta,omitempty"`
	ExtractFrontmatter bool                `json:"extractFrontmatter,omitempty"`
	ScrapedAt          int64               `json:"scrapedAt,omitempty"`
	Scraper            string              `json:"scraper,omitempty"`
	TTL                int64               `json:"ttl,omitempty"`
}

// IngestOptionsHTTP controls optional behavior during ingestion.
type IngestOptionsHTTP struct {
	SkipDuplicates          bool `json:"skipDuplicates,omitempty"`
	SkipEmbeddings          bool `json:"skipEmbeddings,omitempty"`
	SkipFTS                 bool `json:"skipFts,omitempty"`
	SkipWebhooks            bool `json:"skipWebhooks,omitempty"`
	AutoConfigureCollection bool `json:"autoConfigureCollection,omitempty"`
	SaveRevision            bool `json:"saveRevision,omitempty"`
	// Profile names a preset of the flags above (RAG-004): "" or "default"
	// keeps every step, "fast" trades parsing fidelity and bookkeeping for
	// throughput. An explicitly-set flag always overrides the preset.
	Profile string `json:"profile,omitempty"`
	// TextOnly extracts plain text from heavy formats instead of rebuilding
	// their structure as Markdown. It changes html, docx and odt; pdf and rtf
	// build no structure to begin with, so they are unaffected. Implied by
	// profile "fast".
	TextOnly bool `json:"textOnly,omitempty"`
}

// IngestResponseHTTP is the HTTP response body for a bulk ingest operation.
type IngestResponseHTTP struct {
	Added      int      `json:"added"`
	Updated    int      `json:"updated"`
	Skipped    int      `json:"skipped"`
	Failed     int      `json:"failed"`
	Errors     []string `json:"errors,omitempty"`
	Collection string   `json:"collection"`
	DurationMs int64    `json:"durationMs"`
	// Profile records which ingest profile actually applied (RAG-004), so a
	// corpus loaded months ago can be explained without guessing which flags
	// the caller happened to send.
	Profile string `json:"profile,omitempty"`
	// Sanitized counts documents whose text was not valid UTF-8 and had the
	// undecodable bytes dropped (GO-036).
	//
	// A bulk import must not fail because one page of a 20 GB dump is in
	// Windows-1250, but silently altering documents and reporting nothing is
	// how a corpus quietly loses characters. Single-document writes refuse
	// instead; see text_encoding.go.
	Sanitized int `json:"sanitized,omitempty"`
	// KeyCollisions reports input keys that differ only in letter case and
	// therefore name one document (DOC-016).
	//
	// Document identifiers are case-insensitive, the key index is not, and the
	// gap is silent: the later write replaces the earlier one's content, both
	// spellings still resolve, and the response otherwise reports two
	// successful writes. Silence is the worst available behaviour here, so the
	// collisions are named — importing a repository holding both `README.md`
	// and `readme.md` should not be something you find out about later.
	KeyCollisions []string `json:"keyCollisions,omitempty"`
}

// handleIngest handles POST /v1/ingest
func (s *Server) handleIngest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req IngestRequestHTTP
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		bad(w, err)
		return
	}
	if req.Collection == "" {
		bad(w, errors.New("missing collection"))
		return
	}
	if len(req.Documents) == 0 {
		ok(w, IngestResponseHTTP{Collection: req.Collection})
		return
	}

	resp, err := s.ingestDocuments(r.Context(), req.Collection, req.Documents, req.Options)
	if err != nil {
		bad(w, err)
		return
	}

	s.Metrics.IncOp("ingest")
	ok(w, resp)
}

// ingestDocuments is the core ingest logic shared by HTTP, gRPC, and MCP handlers.
func (s *Server) ingestDocuments(ctx context.Context, collection string, docs []IngestDocumentHTTP, opts IngestOptionsHTTP) (*IngestResponseHTTP, error) {
	start := time.Now()

	// RAG-004: expand the named profile into the flags that already existed,
	// so there is one place that decides what "fast" means and every explicit
	// flag still overrides it.
	profile, err := ResolveIngestProfile(&opts)
	if err != nil {
		return nil, err
	}
	opts.SkipDuplicates = profile.SkipDuplicates
	opts.SkipEmbeddings = profile.SkipEmbeddings
	opts.SkipFTS = profile.SkipFTS
	opts.SkipWebhooks = profile.SkipWebhooks
	opts.AutoConfigureCollection = profile.AutoConfigureCollection
	opts.SaveRevision = profile.SaveRevision

	// Auto-configure collection as "scraping" type
	if opts.AutoConfigureCollection && s.CollectionManager != nil {
		existing, _ := s.CollectionManager.Get(collection)
		if existing == nil {
			_ = s.CollectionManager.Set(collection, &CollectionConfig{
				Type:        "scraping",
				Description: "Bulk ingest collection (auto-configured)",
			})
		}
	}

	// Phase 1: Pre-process documents (key derivation, frontmatter, meta injection)
	protoDocs, skippedIdx, errs, sanitized := s.preProcessIngest(collection, docs, opts)

	resp := &IngestResponseHTTP{
		Collection: collection,
		Skipped:    len(skippedIdx),
		Failed:     len(errs),
		Errors:     errs,
		Profile:    profile.Name,
		// Set here rather than on the way out, so it is reported on every
		// path — the first version assigned it only in the early return
		// below, and every successful import reported zero.
		Sanitized: sanitized,
		// DOC-016: reported on every path, including the empty one below.
		KeyCollisions: describeKeyCollisions(docs),
	}

	if len(protoDocs) == 0 {
		resp.DurationMs = time.Since(start).Milliseconds()
		return resp, nil
	}

	// Phase 2: Batch process via existing processors
	batchResp, processed, err := s.processBatchWithDocs(ctx, collection, protoDocs)
	if err != nil {
		return nil, err
	}

	resp.Added = int(batchResp.Added)
	resp.Updated = int(batchResp.Updated)
	resp.Failed += int(batchResp.Failed)
	resp.Errors = append(resp.Errors, batchResp.Errors...)

	// Phase 3: Apply TTL from original docs to processed docs
	s.applyIngestTTLs(collection, docs, processed, skippedIdx)

	// Phase 4: Post-commit hooks
	s.firePostBatchHooks(collection, processed, postBatchOptions{
		SkipEmbeddings: opts.SkipEmbeddings,
		SkipFTS:        opts.SkipFTS,
		SkipWebhooks:   opts.SkipWebhooks,
	})

	resp.DurationMs = time.Since(start).Milliseconds()
	if len(resp.Errors) == 0 {
		resp.Errors = nil
	}
	return resp, nil
}

// preProcessIngest transforms IngestDocumentHTTP into proto.BatchDocument,
// applying URL key derivation, frontmatter extraction, metadata injection,
// and optional deduplication.
func (s *Server) preProcessIngest(collection string, docs []IngestDocumentHTTP, opts IngestOptionsHTTP) ([]*proto.BatchDocument, map[int]bool, []string, int) {
	var protoDocs []*proto.BatchDocument
	var errs []string
	var sanitized int
	skippedIdx := make(map[int]bool)

	// Build dedup map if needed
	var existingHashes map[string]uint32
	if opts.SkipDuplicates {
		existingHashes = s.buildContentHashMap(collection, docs)
	}

	for i, d := range docs {
		// Derive key from URL if not provided
		key := d.Key
		if key == "" && d.URL != "" {
			key = deriveKeyFromURL(d.URL)
		}
		if key == "" {
			errs = append(errs, fmt.Sprintf("doc[%d]: cannot derive key (provide key or url)", i))
			continue
		}
		if d.Lang == "" {
			errs = append(errs, fmt.Sprintf("doc[%d] (%s): missing lang", i, key))
			continue
		}

		content := d.ContentMD
		meta := d.Meta
		if meta == nil {
			meta = make(map[string][]string)
		}

		// GO-036: drop bytes protobuf cannot store, and count it. Sanitising
		// here rather than refusing, because one badly encoded page must not
		// abort an import of the other twenty thousand — but the response says
		// how many were changed, which it previously did not.
		if sanitizedContent, sanitizedMeta, changed := SanitizeDocumentText(content, meta); changed {
			content = sanitizedContent
			meta = sanitizedMeta
			sanitized++
		}

		// Extract frontmatter
		if d.ExtractFrontmatter && content != "" {
			fmMeta, body := parseFrontmatter(content)
			if fmMeta != nil {
				// Frontmatter as base, request meta overrides
				merged := fmMeta
				for k, v := range meta {
					merged[k] = v
				}
				meta = merged
			}
			content = body
		}

		// Auto-inject scraping metadata
		if d.URL != "" {
			meta["source_url"] = []string{d.URL}
		}
		if d.ScrapedAt > 0 {
			meta["scraped_at"] = []string{strconv.FormatInt(d.ScrapedAt, 10)}
		}
		if d.Scraper != "" {
			meta["scraper"] = []string{d.Scraper}
		}

		// Deduplication check
		if opts.SkipDuplicates && existingHashes != nil {
			docID := genID(collection, key, d.Lang)
			if existingHash, found := existingHashes[docID]; found {
				newHash := crc32.ChecksumIEEE([]byte(content))
				if newHash == existingHash {
					skippedIdx[i] = true
					continue
				}
			}
		}

		protoDocs = append(protoDocs, &proto.BatchDocument{
			Key:          key,
			Lang:         d.Lang,
			Meta:         toProtoMeta(meta),
			ContentMd:    content,
			SaveRevision: opts.SaveRevision,
		})
	}

	return protoDocs, skippedIdx, errs, sanitized
}

// buildContentHashMap builds a map of docID → CRC32 content hash for existing docs.
func (s *Server) buildContentHashMap(collection string, docs []IngestDocumentHTTP) map[string]uint32 {
	hashes := make(map[string]uint32)

	_ = s.DBView(func(tx *bolt.Tx) error {
		bDocs := tx.Bucket(s.BucketNames.Docs)
		if bDocs == nil {
			return nil
		}

		for _, d := range docs {
			key := d.Key
			if key == "" && d.URL != "" {
				key = deriveKeyFromURL(d.URL)
			}
			if key == "" || d.Lang == "" {
				continue
			}

			docID := genID(collection, key, d.Lang)
			docKey := storage.DocKey(collection, docID)
			if v := bDocs.Get(docKey); v != nil {
				existing, err := unmarshalDoc(v)
				if err == nil {
					hashes[docID] = crc32.ChecksumIEEE([]byte(existing.ContentMD))
				}
			}
		}

		return nil
	})

	return hashes
}

// applyIngestTTLs sets TTL for documents that had TTL specified in the ingest request.
func (s *Server) applyIngestTTLs(collection string, docs []IngestDocumentHTTP, processed []*ProcessedDoc, skippedIdx map[int]bool) {
	if s.TTLManager == nil {
		return
	}

	// Map processed docs back to original indices to find TTL values
	procIdx := 0
	for i, d := range docs {
		if skippedIdx[i] {
			continue
		}
		if d.Key == "" && d.URL != "" {
			d.Key = deriveKeyFromURL(d.URL)
		}
		if d.Key == "" || d.Lang == "" {
			continue
		}

		if procIdx < len(processed) && processed[procIdx] != nil && processed[procIdx].Error == nil && d.TTL > 0 {
			expiresAt := time.Now().Unix() + d.TTL
			_ = s.TTLManager.Set(collection, processed[procIdx].DocID, expiresAt)
		}
		procIdx++
	}
}

// ingestDocumentsFromProto converts proto IngestRequest to internal types and calls ingestDocuments.
func (s *Server) ingestDocumentsFromProto(ctx context.Context, req *proto.IngestRequest) (*IngestResponseHTTP, error) {
	docs := make([]IngestDocumentHTTP, len(req.Documents))
	for i, d := range req.Documents {
		meta := make(map[string][]string)
		for k, v := range d.Meta {
			meta[k] = v.Values
		}
		docs[i] = IngestDocumentHTTP{
			URL:                d.Url,
			Key:                d.Key,
			Lang:               d.Lang,
			ContentMD:          d.ContentMd,
			Meta:               meta,
			ExtractFrontmatter: d.ExtractFrontmatter,
			ScrapedAt:          d.ScrapedAt,
			Scraper:            d.Scraper,
			TTL:                d.Ttl,
		}
	}

	var opts IngestOptionsHTTP
	if req.Options != nil {
		opts = IngestOptionsHTTP{
			SkipDuplicates:          req.Options.SkipDuplicates,
			SkipEmbeddings:          req.Options.SkipEmbeddings,
			SkipFTS:                 req.Options.SkipFts,
			SkipWebhooks:            req.Options.SkipWebhooks,
			AutoConfigureCollection: req.Options.AutoConfigureCollection,
			SaveRevision:            req.Options.SaveRevision,
		}
	}

	return s.ingestDocuments(ctx, req.Collection, docs, opts)
}

// protoFromIngestResponse converts IngestResponseHTTP to proto.IngestResponse.
func protoFromIngestResponse(resp *IngestResponseHTTP) *proto.IngestResponse {
	var errs []string
	if resp.Errors != nil {
		errs = resp.Errors
	}
	return &proto.IngestResponse{
		Added:      safeInt32(resp.Added),
		Updated:    safeInt32(resp.Updated),
		Skipped:    safeInt32(resp.Skipped),
		Failed:     safeInt32(resp.Failed),
		Errors:     errs,
		Collection: resp.Collection,
		DurationMs: resp.DurationMs,
	}
}
