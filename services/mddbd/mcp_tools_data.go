package main

import (
	"context"
	"fmt"

	json "github.com/goccy/go-json"
)

func (s *MCPToolServer) toolAddDocument(ctx context.Context, args map[string]interface{}) (string, error) {
	req := &MCPAddRequest{
		Collection: mcpGetString(args, "collection"),
		Key:        mcpGetString(args, "key"),
		Lang:       mcpGetString(args, "lang"),
		ContentMD:  mcpGetString(args, "content_md"),
		Meta:       mcpGetMetaMap(args, "meta"),
	}

	doc, err := s.client.Add(ctx, req)
	if err != nil {
		return "", err
	}

	data, _ := json.MarshalIndent(doc, "", "  ")
	return fmt.Sprintf("Document added successfully:\n%s", string(data)), nil
}

func (s *MCPToolServer) toolSearchDocuments(ctx context.Context, args map[string]interface{}) (string, error) {
	// Resolve projection controls up-front so the store can skip loading
	// bodies that the projection would discard anyway (GO-022).
	includeContent, fields := mcpProjectionArgs(args)

	req := &MCPSearchRequest{
		Collection:     mcpGetString(args, "collection"),
		FilterMeta:     mcpGetMetaMap(args, "filter_meta"),
		Sort:           mcpGetString(args, "sort"),
		Limit:          mcpGetInt(args, "limit"),
		Offset:         mcpGetInt(args, "offset"),
		IncludeContent: includeContent,
	}
	if asc, ok := args["asc"].(bool); ok {
		req.Asc = asc
	}

	resp, err := s.client.Search(ctx, req)
	if err != nil {
		return "", err
	}

	if mcpProjectionActive(fields, includeContent) {
		data, _ := json.MarshalIndent(projectSearchResult(resp, fields, includeContent), "", "  ")
		return string(data), nil
	}

	data, _ := json.MarshalIndent(resp, "", "  ")
	return string(data), nil
}

// mcpProjectionArgs reads the shared projection controls from tool args:
// include_content and fields (empty = all meta keys). When include_content is
// not provided it defaults to true for full-shape output, but to FALSE when
// the caller narrowed to specific fields (GO-019): a fields projection exists
// to cut tokens, so the body must not tag along. Explicit include_content
// always wins.
func mcpProjectionArgs(args map[string]interface{}) (includeContent bool, fields []string) {
	fields = mcpGetStringSlice(args, "fields")
	if v, ok := mcpCoerceBool(args["include_content"]); ok {
		return v, fields
	}
	return len(fields) == 0, fields
}

func (s *MCPToolServer) toolDeleteDocument(ctx context.Context, args map[string]interface{}) (string, error) {
	req := &MCPDeleteRequest{
		Collection: mcpGetString(args, "collection"),
		Key:        mcpGetString(args, "key"),
		Lang:       mcpGetString(args, "lang"),
	}

	if err := s.client.Delete(ctx, req); err != nil {
		return "", err
	}

	return fmt.Sprintf("Document deleted: %s/%s (%s)", req.Collection, req.Key, req.Lang), nil
}

func (s *MCPToolServer) toolGetStats(ctx context.Context, args map[string]interface{}) (string, error) {
	stats, err := s.client.Stats(ctx)
	if err != nil {
		return "", err
	}

	data, _ := json.MarshalIndent(stats, "", "  ")
	return string(data), nil
}

func (s *MCPToolServer) toolAddBatch(ctx context.Context, args map[string]interface{}) (string, error) {
	collection := mcpGetString(args, "collection")
	docsRaw, ok := args["documents"].([]interface{})
	if !ok {
		return "", fmt.Errorf("documents must be an array")
	}

	docs := make([]MCPBatchDocument, len(docsRaw))
	for i, d := range docsRaw {
		docMap, ok := d.(map[string]interface{})
		if !ok {
			return "", fmt.Errorf("invalid document at index %d", i)
		}
		docs[i] = MCPBatchDocument{
			Key:       mcpGetString(docMap, "key"),
			Lang:      mcpGetString(docMap, "lang"),
			ContentMD: mcpGetString(docMap, "content_md"),
			Meta:      mcpGetMetaMap(docMap, "meta"),
		}
	}

	resp, err := s.client.AddBatch(ctx, &MCPAddBatchRequest{
		Collection: collection,
		Documents:  docs,
	})
	if err != nil {
		return "", err
	}

	data, _ := json.MarshalIndent(resp, "", "  ")
	return string(data), nil
}

func (s *MCPToolServer) toolDeleteBatch(ctx context.Context, args map[string]interface{}) (string, error) {
	collection := mcpGetString(args, "collection")
	docsRaw, ok := args["documents"].([]interface{})
	if !ok {
		return "", fmt.Errorf("documents must be an array")
	}

	docs := make([]MCPDeleteDocument, len(docsRaw))
	for i, d := range docsRaw {
		docMap, ok := d.(map[string]interface{})
		if !ok {
			return "", fmt.Errorf("invalid document at index %d", i)
		}
		docs[i] = MCPDeleteDocument{
			Key:  mcpGetString(docMap, "key"),
			Lang: mcpGetString(docMap, "lang"),
		}
	}

	resp, err := s.client.DeleteBatch(ctx, &MCPDeleteBatchRequest{
		Collection: collection,
		Documents:  docs,
	})
	if err != nil {
		return "", err
	}

	data, _ := json.MarshalIndent(resp, "", "  ")
	return string(data), nil
}

func (s *MCPToolServer) toolExport(ctx context.Context, args map[string]interface{}) (string, error) {
	req := &MCPExportRequest{
		Collection: mcpGetString(args, "collection"),
		FilterMeta: mcpGetMetaMap(args, "filter_meta"),
		Format:     mcpGetString(args, "format"),
	}

	stream, err := s.client.Export(ctx, req)
	if err != nil {
		return "", err
	}
	defer func() { _ = stream.Close() }()

	return "Export started (stream not fully implemented in MCP yet)", nil
}

func (s *MCPToolServer) toolBackup(ctx context.Context, args map[string]interface{}) (string, error) {
	req := &MCPBackupRequest{
		To: mcpGetString(args, "to"),
	}

	resp, err := s.client.Backup(ctx, req)
	if err != nil {
		return "", err
	}

	return fmt.Sprintf("Backup created: %s", resp.Backup), nil
}

func (s *MCPToolServer) toolRestore(ctx context.Context, args map[string]interface{}) (string, error) {
	req := &MCPRestoreRequest{
		From: mcpGetString(args, "from"),
	}

	resp, err := s.client.Restore(ctx, req)
	if err != nil {
		return "", err
	}

	return fmt.Sprintf("Database restored from: %s", resp.Restored), nil
}

func (s *MCPToolServer) toolSemanticSearch(ctx context.Context, args map[string]interface{}) (string, error) {
	includeContent, fields := mcpProjectionArgs(args)
	req := &MCPVectorSearchRequest{
		Collection:     mcpGetString(args, "collection"),
		Query:          mcpGetString(args, "query"),
		TopK:           mcpGetInt(args, "top_k"),
		IncludeContent: includeContent,
		FilterMeta:     mcpGetMetaMap(args, "filter_meta"),
		Algorithm:      mcpGetString(args, "algorithm"),
		DistanceMetric: mcpGetString(args, "distance_metric"),
		RetrievalMode:  mcpGetString(args, "retrieval_mode"),
		WindowSize:     mcpGetInt(args, "window_size"),
		Oversample:     mcpGetFloat(args, "oversample"),
	}
	if mmr, ok := args["mmr"].(bool); ok {
		req.MMR = mmr
	}
	if lambda, ok := args["mmr_lambda"].(float64); ok {
		req.MMRLambda = lambda
	}

	if threshold, ok := args["threshold"].(float64); ok {
		req.Threshold = threshold
	}

	resp, err := s.client.VectorSearch(ctx, req)
	if err != nil {
		return "", err
	}

	if mcpProjectionActive(fields, includeContent) {
		data, _ := json.MarshalIndent(projectVectorResult(resp, fields, includeContent), "", "  ")
		return string(data), nil
	}

	data, _ := json.MarshalIndent(resp, "", "  ")
	return string(data), nil
}

func (s *MCPToolServer) toolVectorReindex(ctx context.Context, args map[string]interface{}) (string, error) {
	req := &MCPVectorReindexRequest{
		Collection: mcpGetString(args, "collection"),
	}
	if force, ok := args["force"].(bool); ok {
		req.Force = force
	}

	resp, err := s.client.VectorReindex(ctx, req)
	if err != nil {
		return "", err
	}

	data, _ := json.MarshalIndent(resp, "", "  ")
	return fmt.Sprintf("Reindex complete:\n%s", string(data)), nil
}

func (s *MCPToolServer) toolVectorStats(ctx context.Context, args map[string]interface{}) (string, error) {
	resp, err := s.client.VectorStats(ctx)
	if err != nil {
		return "", err
	}

	data, _ := json.MarshalIndent(resp, "", "  ")
	return string(data), nil
}

func (s *MCPToolServer) toolImportURL(ctx context.Context, args map[string]interface{}) (string, error) {
	req := &MCPImportURLRequest{
		Collection: mcpGetString(args, "collection"),
		URL:        mcpGetString(args, "url"),
		Key:        mcpGetString(args, "key"),
		Lang:       mcpGetString(args, "lang"),
		Meta:       mcpGetMetaMap(args, "meta"),
		TTL:        int64(mcpGetInt(args, "ttl")),
	}

	doc, err := s.client.ImportURL(ctx, req)
	if err != nil {
		return "", err
	}

	data, _ := json.MarshalIndent(doc, "", "  ")
	return fmt.Sprintf("Document imported from URL:\n%s", string(data)), nil
}

func (s *MCPToolServer) toolSetTTL(ctx context.Context, args map[string]interface{}) (string, error) {
	req := &MCPSetTTLRequest{
		Collection: mcpGetString(args, "collection"),
		Key:        mcpGetString(args, "key"),
		Lang:       mcpGetString(args, "lang"),
		TTL:        int64(mcpGetInt(args, "ttl")),
	}

	doc, err := s.client.SetTTL(ctx, req)
	if err != nil {
		return "", err
	}

	data, _ := json.MarshalIndent(doc, "", "  ")
	return fmt.Sprintf("TTL updated:\n%s", string(data)), nil
}

func (s *MCPToolServer) toolFTSSearch(ctx context.Context, args map[string]interface{}) (string, error) {
	// Resolve projection controls up-front so the store can skip loading
	// bodies that the projection would discard anyway (GO-022).
	includeContent, fields := mcpProjectionArgs(args)

	req := &MCPFTSSearchRequest{
		Collection:     mcpGetString(args, "collection"),
		Query:          mcpGetString(args, "query"),
		Limit:          mcpGetInt(args, "limit"),
		Algorithm:      mcpGetString(args, "algorithm"),
		Fuzzy:          mcpGetInt(args, "fuzzy"),
		Lang:           mcpGetString(args, "lang"),
		Boost:          mcpGetFloat64Map(args, "boost"),
		IncludeContent: includeContent,
		// CODE-002: highlights carry the line range, which is what lets an
		// agent edit a place instead of reading a file.
		Highlight:     mcpGetBool(args, "highlight"),
		HighlightTag:  mcpGetString(args, "highlight_tag"),
		MaxHighlights: mcpGetInt(args, "max_highlights"),
		FragmentSize:  mcpGetInt(args, "fragment_size"),
	}

	resp, err := s.client.FTSSearch(ctx, req)
	if err != nil {
		return "", err
	}

	if mcpProjectionActive(fields, includeContent) {
		data, _ := json.MarshalIndent(projectFTSResult(resp, fields, includeContent), "", "  ")
		return string(data), nil
	}

	data, _ := json.MarshalIndent(resp, "", "  ")
	return string(data), nil
}

func (s *MCPToolServer) toolFTSReindex(ctx context.Context, args map[string]interface{}) (string, error) {
	req := &MCPFTSReindexRequest{
		Collection: mcpGetString(args, "collection"),
	}
	resp, err := s.client.FTSReindex(ctx, req)
	if err != nil {
		return "", err
	}
	data, _ := json.MarshalIndent(resp, "", "  ")
	return string(data), nil
}

func (s *MCPToolServer) toolFTSLanguages(ctx context.Context, args map[string]interface{}) (string, error) {
	resp, err := s.client.FTSLanguages(ctx)
	if err != nil {
		return "", err
	}
	data, _ := json.MarshalIndent(resp, "", "  ")
	return string(data), nil
}

func (s *MCPToolServer) toolHybridSearch(ctx context.Context, args map[string]interface{}) (string, error) {
	req := &MCPHybridSearchRequest{
		Collection:      mcpGetString(args, "collection"),
		Query:           mcpGetString(args, "query"),
		TopK:            mcpGetInt(args, "top_k"),
		Algorithm:       mcpGetString(args, "algorithm"),
		VectorAlgorithm: mcpGetString(args, "vector_algorithm"),
		Strategy:        mcpGetString(args, "strategy"),
		RRFK:            mcpGetInt(args, "rrf_k"),
		Fuzzy:           mcpGetInt(args, "fuzzy"),
		Oversample:      mcpGetFloat(args, "oversample"),
		DistanceMetric:  mcpGetString(args, "distance_metric"),
		FilterMeta:      mcpGetMetaMap(args, "filter_meta"),
		Boost:           mcpGetFloat64Map(args, "boost"),
		Sort:            mcpGetString(args, "sort"),
	}
	if alpha, ok := args["alpha"].(float64); ok {
		req.Alpha = alpha
	}
	if threshold, ok := args["threshold"].(float64); ok {
		req.Threshold = threshold
	}

	resp, err := s.client.HybridSearch(ctx, req)
	if err != nil {
		return "", err
	}

	data, _ := json.MarshalIndent(resp, "", "  ")
	return string(data), nil
}

func (s *MCPToolServer) toolRegisterWebhook(ctx context.Context, args map[string]interface{}) (string, error) {
	req := &MCPRegisterWebhookRequest{
		URL:        mcpGetString(args, "url"),
		Collection: mcpGetString(args, "collection"),
	}

	if eventsRaw, ok := args["events"].([]interface{}); ok {
		for _, e := range eventsRaw {
			if str, ok := e.(string); ok {
				req.Events = append(req.Events, str)
			}
		}
	}

	wh, err := s.client.RegisterWebhook(ctx, req)
	if err != nil {
		return "", err
	}

	data, _ := json.MarshalIndent(wh, "", "  ")
	return fmt.Sprintf("Webhook registered:\n%s", string(data)), nil
}

func (s *MCPToolServer) toolListWebhooks(ctx context.Context, args map[string]interface{}) (string, error) {
	hooks, err := s.client.ListWebhooks(ctx)
	if err != nil {
		return "", err
	}

	data, _ := json.MarshalIndent(hooks, "", "  ")
	return string(data), nil
}

func (s *MCPToolServer) toolDeleteWebhook(ctx context.Context, args map[string]interface{}) (string, error) {
	req := &MCPDeleteWebhookRequest{
		ID: mcpGetString(args, "id"),
	}

	if err := s.client.DeleteWebhook(ctx, req); err != nil {
		return "", err
	}

	return fmt.Sprintf("Webhook deleted: %s", req.ID), nil
}

func (s *MCPToolServer) toolSetSchema(ctx context.Context, args map[string]interface{}) (string, error) {
	req := &MCPSetSchemaRequest{
		Collection: mcpGetString(args, "collection"),
		Schema:     mcpGetString(args, "schema"),
	}

	if err := s.client.SetSchema(ctx, req); err != nil {
		return "", err
	}

	return fmt.Sprintf("Schema set for collection: %s", req.Collection), nil
}

func (s *MCPToolServer) toolGetSchema(ctx context.Context, args map[string]interface{}) (string, error) {
	collection := mcpGetString(args, "collection")

	resp, err := s.client.GetSchema(ctx, collection)
	if err != nil {
		return "", err
	}

	data, _ := json.MarshalIndent(resp, "", "  ")
	return string(data), nil
}

func (s *MCPToolServer) toolDeleteSchema(ctx context.Context, args map[string]interface{}) (string, error) {
	collection := mcpGetString(args, "collection")

	if err := s.client.DeleteSchema(ctx, collection); err != nil {
		return "", err
	}

	return fmt.Sprintf("Schema deleted for collection: %s", collection), nil
}

func (s *MCPToolServer) toolListSchemas(ctx context.Context, args map[string]interface{}) (string, error) {
	resp, err := s.client.ListSchemas(ctx)
	if err != nil {
		return "", err
	}

	data, _ := json.MarshalIndent(resp, "", "  ")
	return string(data), nil
}

func (s *MCPToolServer) toolValidateDocument(ctx context.Context, args map[string]interface{}) (string, error) {
	req := &MCPValidateRequest{
		Collection: mcpGetString(args, "collection"),
		Meta:       mcpGetMetaMap(args, "meta"),
	}

	resp, err := s.client.ValidateDocument(ctx, req)
	if err != nil {
		return "", err
	}

	data, _ := json.MarshalIndent(resp, "", "  ")
	return string(data), nil
}

func (s *MCPToolServer) toolUpdateDocument(ctx context.Context, args map[string]interface{}) (string, error) {
	req := &MCPUpdateDocumentRequest{
		Collection: mcpGetString(args, "collection"),
		Key:        mcpGetString(args, "key"),
		Lang:       mcpGetString(args, "lang"),
	}

	if meta := mcpGetMetaMap(args, "meta"); len(meta) > 0 {
		req.Meta = meta
	} else if _, ok := args["meta"]; ok {
		// Explicitly provided empty meta = clear all
		empty := make(map[string][]string)
		req.Meta = empty
	}

	if content, ok := args["content_md"].(string); ok {
		req.ContentMD = &content
	}

	if ttl, ok := args["ttl"].(float64); ok {
		ttlInt := int64(ttl)
		req.TTL = &ttlInt
	}

	doc, err := s.client.UpdateDocument(ctx, req)
	if err != nil {
		return "", err
	}

	data, _ := json.MarshalIndent(doc, "", "  ")
	return fmt.Sprintf("Document updated:\n%s", string(data)), nil
}

func (s *MCPToolServer) toolGetDocumentMeta(ctx context.Context, args map[string]interface{}) (string, error) {
	req := &MCPGetDocMetaRequest{
		Collection: mcpGetString(args, "collection"),
		Key:        mcpGetString(args, "key"),
		Lang:       mcpGetString(args, "lang"),
	}

	resp, err := s.client.GetDocumentMeta(ctx, req)
	if err != nil {
		return "", err
	}

	data, _ := json.MarshalIndent(resp, "", "  ")
	return string(data), nil
}

func (s *MCPToolServer) toolCodeGraph(ctx context.Context, args map[string]interface{}) (string, error) {
	resp, err := s.client.CodeGraph(ctx, &MCPCodeGraphRequest{
		Collection: mcpGetString(args, "collection"),
		Key:        mcpGetString(args, "key"),
		Direction:  mcpGetString(args, "direction"),
		Depth:      mcpGetInt(args, "depth"),
		MaxDegree:  mcpGetInt(args, "max_degree"),
		Lines:      mcpGetBool(args, "lines"),
	})
	if err != nil {
		return "", err
	}

	data, _ := json.MarshalIndent(resp, "", "  ")
	return string(data), nil
}

func (s *MCPToolServer) toolClassifyDocument(ctx context.Context, args map[string]interface{}) (string, error) {
	req := &MCPClassifyRequest{
		Collection: mcpGetString(args, "collection"),
		Key:        mcpGetString(args, "key"),
		Lang:       mcpGetString(args, "lang"),
		Text:       mcpGetString(args, "text"),
		TopK:       mcpGetInt(args, "top_k"),
	}

	if labelsRaw, ok := args["labels"].([]interface{}); ok {
		for _, l := range labelsRaw {
			if str, ok := l.(string); ok {
				req.Labels = append(req.Labels, str)
			}
		}
	}

	if multi, ok := args["multi"].(bool); ok {
		req.Multi = multi
	}
	if threshold, ok := args["threshold"].(float64); ok {
		req.Threshold = threshold
	}

	resp, err := s.client.Classify(ctx, req)
	if err != nil {
		return "", err
	}

	data, _ := json.MarshalIndent(resp, "", "  ")
	return string(data), nil
}
