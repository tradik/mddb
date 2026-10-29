package main

import (
	"context"
	"encoding/base64"
	"fmt"

	json "mddb/internal/jsonx"
)

// --- Collection Config Tools ---

func (s *MCPToolServer) toolGetCollectionConfig(ctx context.Context, args map[string]interface{}) (string, error) {
	collection := mcpGetString(args, "collection")
	if collection == "" {
		return "", fmt.Errorf("collection is required")
	}
	resp, err := s.client.GetCollectionConfig(ctx, collection)
	if err != nil {
		return "", err
	}
	data, _ := json.MarshalIndent(resp, "", "  ")
	return string(data), nil
}

func (s *MCPToolServer) toolSetCollectionConfig(ctx context.Context, args map[string]interface{}) (string, error) {
	collection := mcpGetString(args, "collection")
	if collection == "" {
		return "", fmt.Errorf("collection is required")
	}
	req := &MCPSetCollectionConfigRequest{
		Collection:  collection,
		Type:        mcpGetString(args, "type"),
		Description: mcpGetString(args, "description"),
		Icon:        mcpGetString(args, "icon"),
		Color:       mcpGetString(args, "color"),
	}
	if cm, ok := args["custom_meta"].(map[string]interface{}); ok {
		req.CustomMeta = make(map[string]string, len(cm))
		for k, v := range cm {
			if str, ok := v.(string); ok {
				req.CustomMeta[k] = str
			}
		}
	}
	if wp, ok := args["wordpress"].(map[string]interface{}); ok {
		req.WordPress = &WordPressTargetConfig{
			URL:    mcpGetString(wp, "url"),
			APIKey: mcpGetString(wp, "api_key"),
		}
	}
	// Advertised in the schema since v2.9.14 and never read until now.
	if v, ok := args["max_revisions"].(float64); ok {
		n := int(v)
		req.MaxRevisions = &n
	}
	if code, ok := args["code"].(map[string]interface{}); ok {
		req.Code = &CodeConfigDef{IncludePaths: mcpGetStringSlice(code, "include_paths")}
	}
	if f, ok := args["fts"].(map[string]interface{}); ok {
		if v, set := f["enabled"].(bool); set {
			req.FTS = &CollectionFTSDef{Enabled: &v}
		}
	}
	if emb, ok := args["embedding"].(map[string]interface{}); ok {
		req.Embedding = &EmbeddingChunkingDef{
			ChunkSize: mcpGetInt(emb, "chunk_size"),
			ChunkMode: mcpGetString(emb, "chunk_mode"),
		}
	}
	if q, ok := args["quota"].(map[string]interface{}); ok {
		req.Quota = &CollectionQuota{
			MaxDocuments:          int64(mcpGetInt(q, "max_documents")),
			MaxBytes:              int64(mcpGetInt(q, "max_bytes")),
			MaxVectors:            int64(mcpGetInt(q, "max_vectors")),
			EmbeddingCallsPerDay:  int64(mcpGetInt(q, "embedding_calls_per_day")),
			EmbeddingTokensPerDay: int64(mcpGetInt(q, "embedding_tokens_per_day")),
		}
	}
	if err := s.client.SetCollectionConfig(ctx, req); err != nil {
		return "", err
	}
	return fmt.Sprintf("Collection %q config updated", collection), nil
}

func (s *MCPToolServer) toolListCollectionConfigs(ctx context.Context, args map[string]interface{}) (string, error) {
	resp, err := s.client.ListCollectionConfigs(ctx)
	if err != nil {
		return "", err
	}
	data, _ := json.MarshalIndent(resp, "", "  ")
	return string(data), nil
}

// --- Cross-Search Tool ---

func (s *MCPToolServer) toolCrossSearch(ctx context.Context, args map[string]interface{}) (string, error) {
	req := &MCPCrossSearchRequest{
		SourceCollection: mcpGetString(args, "source_collection"),
		SourceDocID:      mcpGetString(args, "source_doc_id"),
		Query:            mcpGetString(args, "query"),
		TopK:             mcpGetInt(args, "top_k"),
		Threshold:        mcpGetFloat(args, "threshold"),
		Algorithm:        mcpGetString(args, "algorithm"),
		DistanceMetric:   mcpGetString(args, "distance_metric"),
		Oversample:       mcpGetFloat(args, "oversample"),
		SearchType:       mcpGetString(args, "search_type"),
		FilterMeta:       mcpGetMetaMap(args, "filter_meta"),
	}
	filter, filterErr := mcpGetFilter(args)
	if filterErr != nil {
		return "", filterErr
	}
	req.Filter = filter
	groupBy, groupErr := mcpGetGroupBy(args)
	if groupErr != nil {
		return "", groupErr
	}
	req.GroupBy = groupBy
	req.IndexWait = mcpGetIndexWait(args)
	if ic, ok := args["include_content"].(bool); ok {
		req.IncludeContent = ic
	}
	// Parse target_collections array
	if tc, ok := args["target_collections"].([]interface{}); ok {
		for _, v := range tc {
			if str, ok := v.(string); ok {
				req.TargetCollections = append(req.TargetCollections, str)
			}
		}
	}
	if len(req.TargetCollections) == 0 {
		return "", fmt.Errorf("target_collections is required")
	}
	req.PassageOptions = mcpGetPassageOptions(args)
	req.PlanOptions = mcpGetPlanOptions(args)
	feedbackOpts, feedbackErr := mcpGetFeedback(args)
	if feedbackErr != nil {
		return "", feedbackErr
	}
	req.FeedbackOptions = feedbackOpts
	resp, err := s.client.CrossSearch(ctx, req)
	if err != nil {
		return "", err
	}
	if resp.Passages != nil {
		return mcpPassagesJSON(resp.Passages), nil
	}
	data, _ := json.MarshalIndent(resp, "", "  ")
	return string(data), nil
}

func (s *MCPToolServer) toolAggregate(ctx context.Context, args map[string]interface{}) (string, error) {
	req := &AggregateRequest{
		Collection:   mcpGetString(args, "collection"),
		FilterMeta:   mcpGetMetaMap(args, "filter_meta"),
		MaxFacetSize: mcpGetInt(args, "max_facet_size"),
	}
	filter, filterErr := mcpGetFilter(args)
	if filterErr != nil {
		return "", filterErr
	}
	req.Filter = filter
	// Parse facets array
	if facetsRaw, ok := args["facets"].([]interface{}); ok {
		for _, f := range facetsRaw {
			if fm, ok := f.(map[string]interface{}); ok {
				req.Facets = append(req.Facets, FacetRequest{
					Field:   mcpGetString(fm, "field"),
					OrderBy: mcpGetString(fm, "order_by"),
				})
			} else if fs, ok := f.(string); ok {
				req.Facets = append(req.Facets, FacetRequest{Field: fs})
			}
		}
	}
	// Parse histograms array
	if histRaw, ok := args["histograms"].([]interface{}); ok {
		for _, h := range histRaw {
			if hm, ok := h.(map[string]interface{}); ok {
				req.Histograms = append(req.Histograms, HistogramRequest{
					Field:    mcpGetString(hm, "field"),
					Interval: mcpGetString(hm, "interval"),
				})
			} else if hs, ok := h.(string); ok {
				req.Histograms = append(req.Histograms, HistogramRequest{Field: hs})
			}
		}
	}
	resp, err := s.client.Aggregate(ctx, req)
	if err != nil {
		return "", err
	}
	data, _ := json.MarshalIndent(resp, "", "  ")
	return string(data), nil
}

func (s *MCPToolServer) toolFindDuplicates(ctx context.Context, args map[string]interface{}) (string, error) {
	req := &MCPFindDuplicatesRequest{
		Collection:     mcpGetString(args, "collection"),
		Mode:           mcpGetString(args, "mode"),
		MaxDocs:        mcpGetInt(args, "max_docs"),
		DistanceMetric: mcpGetString(args, "distance_metric"),
		Threshold:      mcpGetFloat(args, "threshold"),
	}
	if ic, ok := args["include_content"].(bool); ok {
		req.IncludeContent = ic
	}
	resp, err := s.client.FindDuplicates(ctx, req)
	if err != nil {
		return "", err
	}
	data, _ := json.MarshalIndent(resp, "", "  ")
	return string(data), nil
}

func (s *MCPToolServer) toolIngest(ctx context.Context, args map[string]interface{}) (string, error) {
	collection := mcpGetString(args, "collection")
	docsRaw, ok := args["documents"].([]interface{})
	if !ok {
		return "", fmt.Errorf("documents must be an array")
	}

	docs := make([]MCPIngestDocument, len(docsRaw))
	for i, d := range docsRaw {
		docMap, ok := d.(map[string]interface{})
		if !ok {
			return "", fmt.Errorf("invalid document at index %d", i)
		}
		docs[i] = MCPIngestDocument{
			URL:       mcpGetString(docMap, "url"),
			Key:       mcpGetString(docMap, "key"),
			Lang:      mcpGetString(docMap, "lang"),
			ContentMD: mcpGetString(docMap, "content_md"),
			Meta:      mcpGetMetaMap(docMap, "meta"),
			Scraper:   mcpGetString(docMap, "scraper"),
			ScrapedAt: int64(mcpGetInt(docMap, "scraped_at")),
			TTL:       int64(mcpGetInt(docMap, "ttl")),
		}
		if ef, ok := docMap["extract_frontmatter"].(bool); ok {
			docs[i].ExtractFrontmatter = ef
		}
		// AGT-001: a file to convert rather than Markdown to store.
		docs[i].Format = mcpGetString(docMap, "format")
		if b64 := mcpGetString(docMap, "content_base64"); b64 != "" {
			data, err := mcpDecodeBase64(b64)
			if err != nil {
				return "", fmt.Errorf("document at index %d: %w", i, err)
			}
			docs[i].Content = data
		}
	}

	var opts MCPIngestOptions
	if optsRaw, ok := args["options"].(map[string]interface{}); ok {
		if v, ok := optsRaw["skip_duplicates"].(bool); ok {
			opts.SkipDuplicates = v
		}
		if v, ok := optsRaw["skip_embeddings"].(bool); ok {
			opts.SkipEmbeddings = v
		}
		if v, ok := optsRaw["skip_fts"].(bool); ok {
			opts.SkipFTS = v
		}
		if v, ok := optsRaw["skip_webhooks"].(bool); ok {
			opts.SkipWebhooks = v
		}
		if v, ok := optsRaw["auto_configure_collection"].(bool); ok {
			opts.AutoConfigureCollection = v
		}
		if v, ok := optsRaw["save_revision"].(bool); ok {
			opts.SaveRevision = v
		}
		opts.TextOnly = mcpGetBool(optsRaw, "text_only")
	}

	resp, err := s.client.Ingest(ctx, &MCPIngestRequest{
		Collection: collection,
		Documents:  docs,
		Options:    opts,
	})
	if err != nil {
		return "", err
	}

	data, _ := json.MarshalIndent(resp, "", "  ")
	return string(data), nil
}

// mcpDecodeBase64 decodes file content an agent sent as base64: standard,
// URL-safe or unpadded, since LLM clients emit all three.
func mcpDecodeBase64(content string) ([]byte, error) {
	data, err := base64.StdEncoding.DecodeString(content)
	if err != nil {
		data, err = base64.URLEncoding.DecodeString(content)
	}
	if err != nil {
		data, err = base64.RawStdEncoding.DecodeString(content)
	}
	if err != nil {
		return nil, fmt.Errorf("invalid base64 content: %w", err)
	}
	return data, nil
}

func (s *MCPToolServer) toolUploadFile(ctx context.Context, args map[string]interface{}) (string, error) {
	collection := mcpGetString(args, "collection")
	filename := mcpGetString(args, "filename")
	contentB64 := mcpGetString(args, "content")
	lang := mcpGetString(args, "lang")
	key := mcpGetString(args, "key")
	meta := mcpGetMetaMap(args, "meta")
	ttl := int64(mcpGetInt(args, "ttl"))

	if collection == "" || filename == "" || contentB64 == "" || lang == "" {
		return "", fmt.Errorf("missing required fields: collection, filename, content, lang")
	}

	data, err := mcpDecodeBase64(contentB64)
	if err != nil {
		return "", err
	}

	// One conversion path for every surface (AGT-001); text_only is the
	// RAG-004 option upload and ingest already had.
	conv, err := convertToMarkdown(formatFromFilename(filename), data, mcpGetBool(args, "text_only"))
	if err != nil {
		return "", err
	}
	contentMD, converted := conv.Markdown, conv.Converted

	// Extract frontmatter for md/txt. meta is never nil (mcpGetMetaMap always
	// returns a map); explicitly passed keys win over frontmatter.
	if !converted {
		fmMeta, body := parseFrontmatter(contentMD)
		if fmMeta != nil {
			contentMD = body
			for k, v := range fmMeta {
				if _, exists := meta[k]; !exists {
					meta[k] = v
				}
			}
		}
	}

	// Derive key from filename if not provided
	if key == "" {
		key = deriveKeyFromFilename(filename)
	}
	if key == "" {
		return "", fmt.Errorf("cannot derive key from filename; provide key explicitly")
	}

	// Add upload metadata
	meta["upload_format"] = []string{conv.Format}
	meta["upload_filename"] = []string{filename}
	if converted {
		meta["upload_converted"] = []string{"true"}
	}
	_ = ttl // TTL is set via /v1/set-ttl or SetTTL MCP tool separately

	// The file's own document first, then what it held (GO-046).
	conv.Markdown = contentMD
	var result map[string]interface{}
	var parts []string
	for _, d := range convertedDocs(conv, key, meta) {
		doc, err := s.client.Add(ctx, &MCPAddRequest{Collection: collection, Key: d.Key, Lang: lang, ContentMD: d.Markdown, Meta: d.Meta})
		if err != nil {
			return "", err
		}
		if result != nil {
			parts = append(parts, d.Key)
			continue
		}
		result = map[string]interface{}{
			"key":       d.Key,
			"format":    d.Format,
			"converted": d.Converted,
			"document":  doc,
		}
		if d.Converted {
			result["conversionConfidence"] = d.Confidence
			result["conversionWarnings"] = d.Warnings
		}
	}
	if len(parts) > 0 {
		result["parts"] = parts
	}
	out, _ := json.MarshalIndent(result, "", "  ")
	return string(out), nil
}
