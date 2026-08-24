package main

import (
	"context"
	"errors"

	proto "mddb/proto"

	json "mddb/internal/jsonx"
)

// resolveBulkManager extracts the *BulkIngestManager from the underlying
// DirectClient. Bulk ingest is an in-process feature — exposing it via the
// remote MCPClient interface would require four new RPCs with no real-world
// cross-process use case, so we reach for the manager directly here.
func (s *MCPToolServer) resolveBulkManager() (*BulkIngestManager, error) {
	dc, ok := s.client.(*DirectClient)
	if !ok || dc.server == nil {
		return nil, errors.New("bulk ingest tools require direct (in-process) MCP mode")
	}
	if dc.server.BulkIngest == nil {
		return nil, errors.New("bulk ingest not initialized")
	}
	return dc.server.BulkIngest, nil
}

// toolBulkIngestSubmit queues a long-running bulk ingest job and returns the
// job record. The client should poll bulk_ingest_status or pass callback_url
// to be notified on completion.
func (s *MCPToolServer) toolBulkIngestSubmit(_ context.Context, args map[string]interface{}) (string, error) {
	mgr, err := s.resolveBulkManager()
	if err != nil {
		return "", err
	}
	collection := mcpGetString(args, "collection")
	if collection == "" {
		return "", errors.New("missing collection")
	}

	rawDocs, ok := args["documents"].([]interface{})
	if !ok || len(rawDocs) == 0 {
		return "", errors.New("documents must be a non-empty array")
	}
	docs := make([]*proto.BatchDocument, 0, len(rawDocs))
	for _, raw := range rawDocs {
		m, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		docs = append(docs, &proto.BatchDocument{
			Key:          mcpGetString(m, "key"),
			Lang:         mcpGetString(m, "lang"),
			Meta:         toProtoMeta(mcpGetMetaMap(m, "meta")),
			ContentMd:    mcpGetString(m, "contentMd"),
			SaveRevision: mcpGetBool(m, "saveRevision"),
		})
	}

	job, err := mgr.Submit(collection, docs, mcpGetString(args, "callback_url"))
	if err != nil {
		return "", err
	}
	data, _ := json.MarshalIndent(job, "", "  ")
	return string(data), nil
}

// toolBulkIngestStatus returns the current status record for a job.
func (s *MCPToolServer) toolBulkIngestStatus(_ context.Context, args map[string]interface{}) (string, error) {
	mgr, err := s.resolveBulkManager()
	if err != nil {
		return "", err
	}
	id := mcpGetString(args, "id")
	if id == "" {
		return "", errors.New("missing id")
	}
	job, err := mgr.Get(id)
	if err != nil {
		return "", err
	}
	data, _ := json.MarshalIndent(job, "", "  ")
	return string(data), nil
}

// toolBulkIngestList returns jobs newest-first, optionally filtered by collection.
func (s *MCPToolServer) toolBulkIngestList(_ context.Context, args map[string]interface{}) (string, error) {
	mgr, err := s.resolveBulkManager()
	if err != nil {
		return "", err
	}
	jobs, err := mgr.List(mcpGetString(args, "collection"))
	if err != nil {
		return "", err
	}
	data, _ := json.MarshalIndent(map[string]interface{}{
		"jobs":  jobs,
		"total": len(jobs),
	}, "", "  ")
	return string(data), nil
}

// toolBulkIngestCancel cancels a pending job.
func (s *MCPToolServer) toolBulkIngestCancel(_ context.Context, args map[string]interface{}) (string, error) {
	mgr, err := s.resolveBulkManager()
	if err != nil {
		return "", err
	}
	id := mcpGetString(args, "id")
	if id == "" {
		return "", errors.New("missing id")
	}
	if err := mgr.Cancel(id); err != nil {
		return "", err
	}
	return `{"status":"cancelled","id":"` + id + `"}`, nil
}

// toolAutocomplete returns prefix-match suggestions ranked by document
// frequency. Reuses the FTS inverted index — no dedicated store needed.
func (s *MCPToolServer) toolAutocomplete(_ context.Context, args map[string]interface{}) (string, error) {
	dc, ok := s.client.(*DirectClient)
	if !ok || dc.server == nil || dc.server.FTSIndex == nil {
		return "", errors.New("autocomplete requires direct (in-process) MCP mode with FTS initialized")
	}
	collection := mcpGetString(args, "collection")
	if collection == "" {
		return "", errors.New("missing collection")
	}
	query := mcpGetString(args, "q")
	field := mcpGetString(args, "field")
	topN := mcpGetInt(args, "top_n")
	if topN <= 0 {
		topN = 10
	}
	items, err := dc.server.FTSIndex.Autocomplete(collection, query, field, topN)
	if err != nil {
		return "", err
	}
	data, _ := json.MarshalIndent(map[string]interface{}{
		"items": items,
		"total": len(items),
		"query": query,
		"field": field,
	}, "", "  ")
	return string(data), nil
}
