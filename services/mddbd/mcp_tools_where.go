package main

import (
	"context"

	json "mddb/internal/jsonx"
)

// MCP tools for operations by metadata filter (GO-045). delete_where is
// destructive; run it with dry_run first.

func mcpWhereTools() []MCPTool {
	common := func() map[string]interface{} {
		return map[string]interface{}{
			"collection":  map[string]interface{}{"type": "string"},
			"filter":      map[string]interface{}{"type": "object", "description": "Operator filter per metadata key: {in, prefix, contains, gt, gte, lt, lte, not, exists}"},
			"filter_meta": map[string]interface{}{"type": "object", "description": "Exact metadata match: {key: [values]}"},
			"limit":       map[string]interface{}{"type": "integer", "description": "Documents affected by this call (default 1000, max 10000)"},
			"cursor":      map[string]interface{}{"type": "string", "description": "next_cursor of the previous call, to continue"},
			"dry_run":     map[string]interface{}{"type": "boolean", "description": "Report what would be affected and change nothing. Do this first."},
		}
	}
	update, ttl := common(), common()
	update["meta"] = map[string]interface{}{"type": "object", "description": "Metadata merged into each document: these keys replace the document's values"}
	ttl["ttl"] = map[string]interface{}{"type": "integer", "description": "Seconds from now; 0 removes the expiry"}
	return []MCPTool{
		{Name: "delete_where", Description: "Delete every document a metadata filter matches, through the normal delete " +
			"(revisions, indexes, webhooks). Returns matched, affected, a sample of keys and next_cursor. Run with dry_run first.",
			InputSchema: map[string]interface{}{"type": "object", "properties": common(), "required": []string{"collection"}}},
		{Name: "update_where", Description: "Merge metadata into every document a metadata filter matches; content is not changed. " +
			"Run with dry_run first.",
			InputSchema: map[string]interface{}{"type": "object", "properties": update, "required": []string{"collection", "meta"}}},
		{Name: "set_ttl_where", Description: "Set the time to live of every document a metadata filter matches (0 removes it). " +
			"Run with dry_run first.",
			InputSchema: map[string]interface{}{"type": "object", "properties": ttl, "required": []string{"collection", "ttl"}}},
	}
}

// toolWhere runs one of the three operations.
func (s *MCPToolServer) toolWhere(ctx context.Context, op WhereOp, args map[string]interface{}) (string, error) {
	filter, err := mcpGetFilter(args)
	if err != nil {
		return "", err
	}
	req := WhereRequest{Collection: mcpGetString(args, "collection"), FilterMeta: mcpGetMetaMap(args, "filter_meta"),
		Filter: filter, Limit: mcpGetInt(args, "limit"), Cursor: mcpGetString(args, "cursor"), DryRun: mcpGetBool(args, "dry_run")}
	if m := mcpGetMetaMap(args, "meta"); len(m) > 0 {
		req.Meta = m
	}
	if v, ok := args["ttl"].(float64); ok {
		t := int64(v)
		req.TTL = &t
	}
	res, err := s.client.Where(ctx, op, req)
	if err != nil {
		return "", err
	}
	data, _ := json.MarshalIndent(res, "", "  ")
	return string(data), nil
}

// Where runs an operation by metadata filter (GO-045).
func (c *DirectClient) Where(ctx context.Context, op WhereOp, req WhereRequest) (*WhereResult, error) {
	return c.server.RunWhere(op, req)
}
