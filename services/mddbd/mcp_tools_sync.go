package main

import (
	"context"
	"fmt"

	json "mddb/internal/jsonx"
)

// The sync_refresh MCP tool (INT-023): POST /v1/sync/refresh for an agent.
// It is gated as a write of the collection it names (access_rules_mcp.go),
// so a key limited to that collection may call it; it runs only the sync
// rules writing to the collection (sync_refresh.go).

// syncRefreshDescription describes the tool and the route.
const syncRefreshDescription = "Refresh a synced collection now: with number, re-read that one issue or pull request " +
	"of the issues sync writing to it; without, start an incremental run of each sync rule writing to it. " +
	"At most once every 30 s per rule; the answer says when each rule last ran."

// syncRefreshTool is the tool's definition.
func syncRefreshTool() MCPTool {
	return MCPTool{
		Name:        "sync_refresh",
		Description: syncRefreshDescription,
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"collection": map[string]interface{}{"type": "string", "description": "The collection a sync rule writes to"},
				"number":     map[string]interface{}{"type": "integer", "description": "The issue or pull request to refresh; omit for an incremental run"},
			},
			"required": []string{"collection"},
		},
	}
}

// toolSyncRefresh refreshes the collection the arguments name.
func (s *MCPToolServer) toolSyncRefresh(ctx context.Context, args map[string]interface{}) (string, error) {
	collection := mcpGetString(args, "collection")
	if collection == "" {
		return "", fmt.Errorf("collection is required")
	}
	res, err := s.client.SyncRefresh(ctx, collection, mcpGetInt(args, "number"))
	if err != nil {
		return "", err
	}
	data, _ := json.MarshalIndent(res, "", "  ")
	return string(data), nil
}

// SyncRefresh refreshes a synced collection via the direct client.
func (c *DirectClient) SyncRefresh(_ context.Context, collection string, number int) (*SyncRefreshResult, error) {
	c.server.Metrics.IncOp("sync_refresh", collection)
	return c.server.syncRefresh(collection, number)
}
