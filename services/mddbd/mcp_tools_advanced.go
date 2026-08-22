package main

import (
	"context"
	"encoding/base64"
	"fmt"
	"path"
	"strings"

	json "github.com/goccy/go-json"
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
	if err := s.client.SetCollectionConfig(ctx, req); err != nil {
		return "", err
	}
	return fmt.Sprintf("Collection %q config updated (type=%s)", collection, req.Type), nil
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
		FilterMeta:       mcpGetMetaMap(args, "filter_meta"),
	}
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
	resp, err := s.client.CrossSearch(ctx, req)
	if err != nil {
		return "", err
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

	// Decode base64 content
	data, err := base64.StdEncoding.DecodeString(contentB64)
	if err != nil {
		// Try URL-safe base64
		data, err = base64.URLEncoding.DecodeString(contentB64)
		if err != nil {
			// Try raw (no padding)
			data, err = base64.RawStdEncoding.DecodeString(contentB64)
			if err != nil {
				return "", fmt.Errorf("invalid base64 content: %w", err)
			}
		}
	}

	// Determine format from filename extension
	ext := strings.ToLower(path.Ext(filename))
	format := strings.TrimPrefix(ext, ".")
	if format == "htm" {
		format = "html"
	}

	// Convert to markdown
	var contentMD string
	var converted bool

	switch format {
	case "md", "markdown", "txt", "text", "":
		contentMD = string(data)
	case "yaml", "yml", "log", "lex":
		contentMD = "```" + format + "\n" + string(data) + "\n```"
		converted = true
	case "tex", "latex":
		contentMD = texToMarkdown(data)
		converted = true
	case "html":
		contentMD = htmlToMarkdown(data)
		converted = true
	case "pdf":
		contentMD, err = pdfToMarkdown(data)
		if err != nil {
			return "", fmt.Errorf("pdf conversion: %w", err)
		}
		converted = true
	case "docx":
		contentMD, err = docxToMarkdown(data)
		if err != nil {
			return "", fmt.Errorf("docx conversion: %w", err)
		}
		converted = true
	case "odt":
		contentMD, err = odtToMarkdown(data)
		if err != nil {
			return "", fmt.Errorf("odt conversion: %w", err)
		}
		converted = true
	case "rtf":
		contentMD = rtfToMarkdown(data)
		converted = true
	default:
		return "", fmt.Errorf("unsupported format: %s (supported: md, txt, html, pdf, docx, odt, rtf, yaml, log, lex, tex)", format)
	}

	// Extract frontmatter for md/txt
	if !converted {
		fmMeta, body := parseFrontmatter(contentMD)
		if fmMeta != nil {
			contentMD = body
			if meta == nil {
				meta = fmMeta
			} else {
				for k, v := range fmMeta {
					if _, exists := meta[k]; !exists {
						meta[k] = v
					}
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
	if meta == nil {
		meta = make(map[string][]string)
	}
	meta["upload_format"] = []string{format}
	meta["upload_filename"] = []string{filename}
	if converted {
		meta["upload_converted"] = []string{"true"}
	}

	// Store via MCPClient.Add
	doc, err := s.client.Add(ctx, &MCPAddRequest{
		Collection: collection,
		Key:        key,
		Lang:       lang,
		ContentMD:  contentMD,
		Meta:       meta,
	})
	_ = ttl // TTL is set via /v1/set-ttl or SetTTL MCP tool separately
	if err != nil {
		return "", err
	}

	result := map[string]interface{}{
		"key":       key,
		"format":    format,
		"converted": converted,
		"document":  doc,
	}
	out, _ := json.MarshalIndent(result, "", "  ")
	return string(out), nil
}
