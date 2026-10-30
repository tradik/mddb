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
	req.CustomMeta = mcpCustomMeta(args)
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
	req.FTS = mcpCollectionFTS(args)
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

// mcpCustomMeta reads set_collection_config's custom_meta: its string
// values, nil when the argument is absent.
func mcpCustomMeta(args map[string]interface{}) map[string]string {
	cm, ok := args["custom_meta"].(map[string]interface{})
	if !ok {
		return nil
	}
	out := make(map[string]string, len(cm))
	for k, v := range cm {
		if str, ok := v.(string); ok {
			out[k] = str
		}
	}
	return out
}

// mcpCollectionFTS reads set_collection_config's fts block; nil when it is
// absent or sets neither field.
func mcpCollectionFTS(args map[string]interface{}) *CollectionFTSDef {
	f, ok := args["fts"].(map[string]interface{})
	if !ok {
		return nil
	}
	def := &CollectionFTSDef{ExcludeMetaKeys: mcpGetStringSlice(f, "exclude_meta_keys")}
	if v, set := f["enabled"].(bool); set {
		def.Enabled = &v
	}
	// The block replaces the stored one: an empty exclude_meta_keys
	// clears the list.
	if _, listed := f["exclude_meta_keys"]; listed || def.Enabled != nil {
		return def
	}
	return nil
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
		ImageOptions:     mcpGetImageOptions(args), // RAG-010
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
		images, err := mcpGetImageFiles(docMap)
		if err != nil {
			return "", fmt.Errorf("document at index %d: %w", i, err)
		}
		docs[i].Images = images
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
		opts.ExtractImages = mcpGetBool(optsRaw, "extract_images")
		opts.FetchRemoteImages = mcpGetBool(optsRaw, "fetch_remote_images")
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

// mcpGetImageFiles reads a document's "images": the files its Markdown image
// links name, each {path, content_base64} (RAG-010).
func mcpGetImageFiles(m map[string]interface{}) ([]IngestImage, error) {
	raw, _ := m["images"].([]interface{})
	out := make([]IngestImage, 0, len(raw))
	for j, it := range raw {
		im, _ := it.(map[string]interface{})
		data, err := mcpDecodeBase64(mcpGetString(im, "content_base64"))
		if err != nil {
			return nil, fmt.Errorf("image %d: %w", j, err)
		}
		out = append(out, IngestImage{Path: mcpGetString(im, "path"), Content: data})
	}
	return out, nil
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
	req := &MCPUploadFileRequest{
		Collection:        mcpGetString(args, "collection"),
		Filename:          mcpGetString(args, "filename"),
		Lang:              mcpGetString(args, "lang"),
		Key:               mcpGetString(args, "key"),
		Meta:              mcpGetMetaMap(args, "meta"),
		TextOnly:          mcpGetBool(args, "text_only"),
		ExtractImages:     mcpGetBool(args, "extract_images"),
		FetchRemoteImages: mcpGetBool(args, "fetch_remote_images"),
	}
	contentB64 := mcpGetString(args, "content")
	if req.Collection == "" || req.Filename == "" || contentB64 == "" || req.Lang == "" {
		return "", fmt.Errorf("missing required fields: collection, filename, content, lang")
	}
	data, err := mcpDecodeBase64(contentB64)
	if err != nil {
		return "", err
	}
	req.Content = data
	if req.Images, err = mcpGetImageFiles(args); err != nil {
		return "", err
	}
	// TTL is set via /v1/set-ttl or the set_ttl tool separately.
	result, err := s.client.UploadFile(ctx, req)
	if err != nil {
		return "", err
	}
	out, _ := json.MarshalIndent(result, "", "  ")
	return string(out), nil
}
