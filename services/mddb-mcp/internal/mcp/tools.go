package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/tradik/mddb/services/mddb-mcp/internal/mddb"
)

// callTool invokes MCP tool.
func (s *Server) callTool(ctx context.Context, name string, args map[string]interface{}) (string, error) {
	switch name {
	case "add_document":
		return s.toolAddDocument(ctx, args)
	case "search_documents":
		return s.toolSearchDocuments(ctx, args)
	case "delete_document":
		return s.toolDeleteDocument(ctx, args)
	case "get_stats":
		return s.toolGetStats(ctx, args)
	case "add_documents_batch":
		return s.toolAddBatch(ctx, args)
	case "delete_documents_batch":
		return s.toolDeleteBatch(ctx, args)
	case "export_documents":
		return s.toolExport(ctx, args)
	case "create_backup":
		return s.toolBackup(ctx, args)
	case "restore_backup":
		return s.toolRestore(ctx, args)
	default:
		return "", fmt.Errorf("unknown tool: %s", name)
	}
}

// toolAddDocument dodaje dokument.
func (s *Server) toolAddDocument(ctx context.Context, args map[string]interface{}) (string, error) {
	req := &mddb.AddRequest{
		Collection: getString(args, "collection"),
		Key:        getString(args, "key"),
		Lang:       getString(args, "lang"),
		ContentMD:  getString(args, "content_md"),
		Meta:       getMetaMap(args, "meta"),
	}

	doc, err := s.client.Add(ctx, req)
	if err != nil {
		return "", err
	}

	data, _ := json.MarshalIndent(doc, "", "  ")
	return fmt.Sprintf("Document added successfully:\n%s", string(data)), nil
}

// toolSearchDocuments wyszukuje dokumenty.
func (s *Server) toolSearchDocuments(ctx context.Context, args map[string]interface{}) (string, error) {
	req := &mddb.SearchRequest{
		Collection: getString(args, "collection"),
		FilterMeta: getMetaMap(args, "filter_meta"),
		Sort:       getString(args, "sort"),
		Limit:      getInt(args, "limit"),
		Offset:     getInt(args, "offset"),
	}

	resp, err := s.client.Search(ctx, req)
	if err != nil {
		return "", err
	}

	data, _ := json.MarshalIndent(resp, "", "  ")
	return string(data), nil
}

// toolDeleteDocument usuwa dokument.
func (s *Server) toolDeleteDocument(ctx context.Context, args map[string]interface{}) (string, error) {
	req := &mddb.DeleteRequest{
		Collection: getString(args, "collection"),
		Key:        getString(args, "key"),
		Lang:       getString(args, "lang"),
	}

	if err := s.client.Delete(ctx, req); err != nil {
		return "", err
	}

	return fmt.Sprintf("Document deleted: %s/%s (%s)", req.Collection, req.Key, req.Lang), nil
}

// toolGetStats returns statistics.
func (s *Server) toolGetStats(ctx context.Context, args map[string]interface{}) (string, error) {
	stats, err := s.client.Stats(ctx)
	if err != nil {
		return "", err
	}

	data, _ := json.MarshalIndent(stats, "", "  ")
	return string(data), nil
}

// toolAddBatch adds multiple documents.
func (s *Server) toolAddBatch(ctx context.Context, args map[string]interface{}) (string, error) {
	collection := getString(args, "collection")
	docsRaw, ok := args["documents"].([]interface{})
	if !ok {
		return "", fmt.Errorf("documents must be an array")
	}

	docs := make([]mddb.BatchDocument, len(docsRaw))
	for i, d := range docsRaw {
		docMap, ok := d.(map[string]interface{})
		if !ok {
			return "", fmt.Errorf("invalid document at index %d", i)
		}
		docs[i] = mddb.BatchDocument{
			Key:       getString(docMap, "key"),
			Lang:      getString(docMap, "lang"),
			ContentMD: getString(docMap, "content_md"),
			Meta:      getMetaMap(docMap, "meta"),
		}
	}

	resp, err := s.client.AddBatch(ctx, &mddb.AddBatchRequest{
		Collection: collection,
		Documents:  docs,
	})
	if err != nil {
		return "", err
	}

	data, _ := json.MarshalIndent(resp, "", "  ")
	return string(data), nil
}

// toolDeleteBatch deletes multiple documents.
func (s *Server) toolDeleteBatch(ctx context.Context, args map[string]interface{}) (string, error) {
	collection := getString(args, "collection")
	docsRaw, ok := args["documents"].([]interface{})
	if !ok {
		return "", fmt.Errorf("documents must be an array")
	}

	docs := make([]mddb.DeleteDocument, len(docsRaw))
	for i, d := range docsRaw {
		docMap, ok := d.(map[string]interface{})
		if !ok {
			return "", fmt.Errorf("invalid document at index %d", i)
		}
		docs[i] = mddb.DeleteDocument{
			Key:  getString(docMap, "key"),
			Lang: getString(docMap, "lang"),
		}
	}

	resp, err := s.client.DeleteBatch(ctx, &mddb.DeleteBatchRequest{
		Collection: collection,
		Documents:  docs,
	})
	if err != nil {
		return "", err
	}

	data, _ := json.MarshalIndent(resp, "", "  ")
	return string(data), nil
}

// toolExport eksportuje dokumenty.
func (s *Server) toolExport(ctx context.Context, args map[string]interface{}) (string, error) {
	req := &mddb.ExportRequest{
		Collection: getString(args, "collection"),
		FilterMeta: getMetaMap(args, "filter_meta"),
		Format:     getString(args, "format"),
	}

	stream, err := s.client.Export(ctx, req)
	if err != nil {
		return "", err
	}
	defer func() {
		_ = stream.Close()
	}()

	return "Export started (stream not fully implemented in MCP yet)", nil
}

// toolBackup creates backup.
func (s *Server) toolBackup(ctx context.Context, args map[string]interface{}) (string, error) {
	req := &mddb.BackupRequest{
		To: getString(args, "to"),
	}

	resp, err := s.client.Backup(ctx, req)
	if err != nil {
		return "", err
	}

	return fmt.Sprintf("Backup created: %s", resp.Backup), nil
}

// toolRestore przywraca backup.
func (s *Server) toolRestore(ctx context.Context, args map[string]interface{}) (string, error) {
	req := &mddb.RestoreRequest{
		From: getString(args, "from"),
	}

	resp, err := s.client.Restore(ctx, req)
	if err != nil {
		return "", err
	}

	return fmt.Sprintf("Database restored from: %s", resp.Restored), nil
}

// Helper functions for parsing arguments

func getString(m map[string]interface{}, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

func getInt(m map[string]interface{}, key string) int {
	if v, ok := m[key].(float64); ok {
		return int(v)
	}
	return 0
}

func getMetaMap(m map[string]interface{}, key string) map[string][]string {
	result := make(map[string][]string)
	if meta, ok := m[key].(map[string]interface{}); ok {
		for k, v := range meta {
			switch val := v.(type) {
			case string:
				result[k] = []string{val}
			case []interface{}:
				strs := make([]string, len(val))
				for i, item := range val {
					if s, ok := item.(string); ok {
						strs[i] = s
					}
				}
				result[k] = strs
			}
		}
	}
	return result
}
