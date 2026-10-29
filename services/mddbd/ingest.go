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

	json "mddb/internal/jsonx"

	bolt "go.etcd.io/bbolt"
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
	// Format and Content carry a file the server converts to Markdown
	// (AGT-001), through the same converters /v1/upload uses. Content is the
	// file's bytes (base64 in JSON); Format is its format ("pdf", "docx",
	// "html", ...) and, when empty, comes from the key's extension. A
	// document with neither stores ContentMD as before.
	Format  string `json:"format,omitempty"`
	Content []byte `json:"contentBase64,omitempty"`
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
	// Conversions reports each document converted from a file (GO-046):
	// its key, the format it was converted from, and how well it converted,
	// so a scanned PDF is named in the response instead of found later.
	Conversions []IngestConversion `json:"conversions,omitempty"`
}

// IngestConversion is one converted document of an ingest response.
type IngestConversion struct {
	Key        string   `json:"key"`
	Format     string   `json:"format"`
	Confidence float64  `json:"conversionConfidence"`
	Warnings   []string `json:"conversionWarnings,omitempty"`
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

	// Phase 1: Pre-process documents (key derivation, conversion,
	// frontmatter, meta injection)
	convert := s.Tracer.Stage(ctx, spanIngestConvert) // OPS-024
	plan := s.preProcessIngest(collection, docs, opts)
	convert.SetString("mddb.collection", collection)
	convert.SetInt("mddb.documents", len(plan.docs))
	convert.End()

	resp := &IngestResponseHTTP{
		Collection: collection,
		Skipped:    plan.skipped,
		Failed:     len(plan.errs),
		Errors:     plan.errs,
		Profile:    profile.Name,
		// Set here rather than on the way out, so it is reported on every
		// path — the first version assigned it only in the early return
		// below, and every successful import reported zero.
		Sanitized: plan.sanitized,
		// DOC-016: reported on every path, including the empty one below.
		KeyCollisions: describeKeyCollisions(docs),
		Conversions:   plan.conversions,
	}

	if len(plan.docs) == 0 {
		resp.DurationMs = time.Since(start).Milliseconds()
		return resp, nil
	}

	// Phase 2: Batch process via existing processors. ingest.index spans the
	// write and the indexing after it, phases 2 to 4.
	index := s.Tracer.Stage(ctx, spanIngestIndex)
	defer index.End()
	index.SetString("mddb.collection", collection)
	index.SetInt("mddb.documents", len(plan.docs))
	batchResp, processed, err := s.processBatchWithDocs(ctx, collection, plan.docs)
	if err != nil {
		index.SetError(err)
		return nil, err
	}

	resp.Added = int(batchResp.Added)
	resp.Updated = int(batchResp.Updated)
	resp.Failed += int(batchResp.Failed)
	resp.Errors = append(resp.Errors, batchResp.Errors...)

	// Phase 3: Apply the TTLs the documents asked for
	s.applyIngestTTLs(collection, plan.ttls, processed)

	// Phase 4: Post-commit hooks
	s.firePostBatchHooks(collection, processed, postBatchOptions{
		SkipEmbeddings: opts.SkipEmbeddings,
		SkipFTS:        opts.SkipFTS,
		SkipWebhooks:   opts.SkipWebhooks,
		Trace:          s.requestTrace(ctx),
	})

	resp.DurationMs = time.Since(start).Milliseconds()
	if len(resp.Errors) == 0 {
		resp.Errors = nil
	}
	return resp, nil
}

// ingestPlan is what pre-processing decided: the documents to write, the
// TTL each asks for (by document ID), and what to report.
type ingestPlan struct {
	docs        []*proto.BatchDocument
	ttls        map[string]int64
	skipped     int
	errs        []string
	sanitized   int
	conversions []IngestConversion
}

// preProcessIngest transforms IngestDocumentHTTP into proto.BatchDocument,
// applying URL key derivation, file conversion, frontmatter extraction,
// metadata injection, and optional deduplication. A converted file may yield
// several documents (an email and its attachments, GO-046).
func (s *Server) preProcessIngest(collection string, docs []IngestDocumentHTTP, opts IngestOptionsHTTP) ingestPlan {
	plan := ingestPlan{ttls: make(map[string]int64)}

	for i, d := range docs {
		// Derive key from URL if not provided
		key := d.Key
		if key == "" && d.URL != "" {
			key = deriveKeyFromURL(d.URL)
		}
		if key == "" {
			plan.errs = append(plan.errs, fmt.Sprintf("doc[%d]: cannot derive key (provide key or url)", i))
			continue
		}
		if d.Lang == "" {
			plan.errs = append(plan.errs, fmt.Sprintf("doc[%d] (%s): missing lang", i, key))
			continue
		}

		meta := d.Meta
		if meta == nil {
			meta = make(map[string][]string)
		}

		// AGT-001: a file goes through the converters /v1/upload uses, so
		// a PDF sent by `mddb-cli autoindex` is stored as its text, not
		// refused as binary or indexed as bytes.
		out := []convertedDoc{{Key: key, Markdown: d.ContentMD, Meta: meta}}
		if len(d.Content) > 0 || d.Format != "" {
			conv, err := convertIngestFile(d, key, opts.TextOnly)
			if err != nil {
				plan.errs = append(plan.errs, fmt.Sprintf("doc[%d] (%s): %s", i, key, err.Error()))
				continue
			}
			out = convertedDocs(conv, key, meta)
		}

		for j, cd := range out {
			content, docMeta := cd.Markdown, cd.Meta

			// GO-036: drop bytes protobuf cannot store, and count it.
			// Sanitising here rather than refusing, because one badly encoded
			// page must not abort an import of the other twenty thousand —
			// but the response says how many were changed.
			if sanitizedContent, sanitizedMeta, changed := SanitizeDocumentText(content, docMeta); changed {
				content = sanitizedContent
				docMeta = sanitizedMeta
				plan.sanitized++
			}

			// Extract frontmatter; a converted file has none of its own.
			if j == 0 && d.ExtractFrontmatter && !cd.Converted && content != "" {
				fmMeta, body := parseFrontmatter(content)
				if fmMeta != nil {
					// Frontmatter as base, request meta overrides
					merged := fmMeta
					for k, v := range docMeta {
						merged[k] = v
					}
					docMeta = merged
				}
				content = body
			}

			// Auto-inject scraping metadata
			if d.URL != "" {
				docMeta["source_url"] = []string{d.URL}
			}
			if d.ScrapedAt > 0 {
				docMeta["scraped_at"] = []string{strconv.FormatInt(d.ScrapedAt, 10)}
			}
			if d.Scraper != "" {
				docMeta["scraper"] = []string{d.Scraper}
			}
			if cd.Converted {
				docMeta["sourceFormat"] = []string{cd.Format}
				plan.conversions = append(plan.conversions, IngestConversion{Key: cd.Key, Format: cd.Format,
					Confidence: cd.Confidence, Warnings: cd.Warnings})
			}

			if d.TTL > 0 {
				plan.ttls[genID(collection, cd.Key, d.Lang)] = d.TTL
			}
			plan.docs = append(plan.docs, &proto.BatchDocument{
				Key:          cd.Key,
				Lang:         d.Lang,
				Meta:         toProtoMeta(docMeta),
				ContentMd:    content,
				SaveRevision: opts.SaveRevision,
			})
		}
	}

	if opts.SkipDuplicates {
		plan.docs, plan.skipped = s.dropUnchanged(collection, plan.docs)
	}
	return plan
}

// convertIngestFile converts an ingest document that carries a file. The
// format comes from the document, or from its key's extension; the bytes are
// Content, or ContentMD when a caller sent text with a format to convert it
// from (HTML as a string, say).
func convertIngestFile(d IngestDocumentHTTP, key string, textOnly bool) (conversion, error) {
	format := normalizeFormat(d.Format)
	if format == "" {
		format = formatFromFilename(key)
	}
	data := d.Content
	if len(data) == 0 {
		data = []byte(d.ContentMD)
	}
	return convertToMarkdown(format, data, textOnly)
}

// dropUnchanged removes the documents whose stored content is the same as
// the new content, and returns how many it removed.
//
// The comparison runs after conversion, on the Markdown that would be stored,
// so an unchanged file converts to the same text and is skipped: the
// converters are deterministic, which is what keeps a second autoindex run
// cheap.
func (s *Server) dropUnchanged(collection string, protoDocs []*proto.BatchDocument) ([]*proto.BatchDocument, int) {
	existing := s.buildContentHashMap(collection, protoDocs)
	kept := protoDocs[:0]
	skipped := 0
	for _, d := range protoDocs {
		if h, found := existing[genID(collection, d.Key, d.Lang)]; found && h == crc32.ChecksumIEEE([]byte(d.ContentMd)) {
			skipped++
			continue
		}
		kept = append(kept, d)
	}
	return kept, skipped
}

// buildContentHashMap builds a map of docID → CRC32 content hash for the
// stored documents among docs.
func (s *Server) buildContentHashMap(collection string, docs []*proto.BatchDocument) map[string]uint32 {
	hashes := make(map[string]uint32)

	_ = s.DBViewC(collection, func(tx *bolt.Tx) error {
		bDocs := tx.Bucket(s.BucketNames.Docs)
		if bDocs == nil {
			return nil
		}
		for _, d := range docs {
			docID := genID(collection, d.Key, d.Lang)
			if v := bDocs.Get(storage.DocKey(collection, docID)); v != nil {
				if existing, err := unmarshalDoc(v); err == nil {
					hashes[docID] = crc32.ChecksumIEEE([]byte(existing.ContentMD))
				}
			}
		}
		return nil
	})

	return hashes
}

// applyIngestTTLs sets the TTL each written document asked for.
//
// Keyed by document ID, not by position: a skipped or failed document used
// to shift every later TTL onto the wrong document.
func (s *Server) applyIngestTTLs(collection string, ttls map[string]int64, processed []*ProcessedDoc) {
	if s.TTLManager == nil || len(ttls) == 0 {
		return
	}
	for _, p := range processed {
		if p == nil || p.Error != nil {
			continue
		}
		if ttl := ttls[p.DocID]; ttl > 0 {
			_ = s.TTLManager.Set(collection, p.DocID, time.Now().Unix()+ttl)
		}
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
			Format:             d.Format,
			Content:            d.Content,
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
	conversions := make([]*proto.IngestConversion, 0, len(resp.Conversions))
	for _, c := range resp.Conversions {
		conversions = append(conversions, &proto.IngestConversion{Key: c.Key, Format: c.Format,
			ConversionConfidence: c.Confidence, ConversionWarnings: c.Warnings})
	}
	return &proto.IngestResponse{
		Added:       safeInt32(resp.Added),
		Updated:     safeInt32(resp.Updated),
		Skipped:     safeInt32(resp.Skipped),
		Failed:      safeInt32(resp.Failed),
		Errors:      errs,
		Collection:  resp.Collection,
		DurationMs:  resp.DurationMs,
		Conversions: conversions,
	}
}
