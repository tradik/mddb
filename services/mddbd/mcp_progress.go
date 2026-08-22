package main

import ()

// MCPProgressSender sends progress notifications.
//
// The writer comes from the transport, which is the only part that knows where
// a notification has to go: stdout for stdio, the session's SSE stream for the
// HTTP transports.
type MCPProgressSender struct {
	progressToken interface{}
	writer        func(notification map[string]interface{})
}

// NewCallbackProgressSender creates a progress sender with a custom callback.
func NewCallbackProgressSender(progressToken interface{}, cb func(map[string]interface{})) *MCPProgressSender {
	return &MCPProgressSender{
		progressToken: progressToken,
		writer:        cb,
	}
}

// Send sends a progress notification.
func (p *MCPProgressSender) Send(progress, total int, message string) {
	if p == nil || p.progressToken == nil {
		return
	}

	params := map[string]interface{}{
		"progressToken": p.progressToken,
		"progress":      progress,
	}
	if total > 0 {
		params["total"] = total
	}
	if message != "" {
		params["message"] = message
	}

	notification := map[string]interface{}{
		"jsonrpc": "2.0",
		"method":  "notifications/progress",
		"params":  params,
	}

	p.writer(notification)
}

// LongRunningTools lists tools that support progress tokens.
var LongRunningTools = map[string]bool{
	"vector_reindex":         true,
	"fts_reindex":            true,
	"ingest_documents":       true,
	"export_documents":       true,
	"add_documents_batch":    true,
	"delete_documents_batch": true,
	"create_backup":          true,
	"restore_backup":         true,
	"find_duplicates":        true,
	"cross_search":           true,
}

// ExtractProgressToken extracts _meta.progressToken from request params.
func ExtractProgressToken(req map[string]interface{}) interface{} {
	params, _ := req["params"].(map[string]interface{})
	if params == nil {
		return nil
	}
	meta, _ := params["_meta"].(map[string]interface{})
	if meta == nil {
		return nil
	}
	return meta["progressToken"]
}
