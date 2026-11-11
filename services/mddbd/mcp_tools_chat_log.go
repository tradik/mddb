package main

import (
	"context"
	"fmt"

	json "mddb/internal/jsonx"
)

// The conversation log tools (#406): read-only, in the admin toolset, for
// global administrators — the same as GET /v1/chat/log and /v1/chat/gaps.

// mcpChatLogTools are the conversation log tools.
func mcpChatLogTools() []MCPTool {
	str := func(d string) map[string]interface{} {
		return map[string]interface{}{"type": "string", "description": d}
	}
	when := "Unix seconds, an RFC 3339 time or YYYY-MM-DD"
	window := func(extra map[string]interface{}) map[string]interface{} {
		props := map[string]interface{}{"scenario": str("Only turns of this chat scenario"),
			"from": str("Earliest turn: " + when), "to": str("Latest turn: " + when + " (a date is its whole day)")}
		for k, v := range extra {
			props[k] = v
		}
		return map[string]interface{}{"type": "object", "properties": props}
	}
	return []MCPTool{
		{
			Name: "chat_log_list",
			Description: "List the logged chat turns (mddb-chat scenarios with logging on) across users, newest first: " +
				"question, answer, outcome (answered, noResults, refused, error), rating and comment, tools with queries, " +
				"result keys and scores, model, latency, tokens. Global admin. Read-only.",
			InputSchema: window(map[string]interface{}{
				"rating":  map[string]interface{}{"type": "string", "enum": []string{"up", "down", "none", "any"}, "description": "Only turns rated so (none: unrated, any: rated)"},
				"outcome": map[string]interface{}{"type": "string", "enum": []string{"answered", "noResults", "refused", "error"}},
				"limit":   map[string]interface{}{"type": "integer", "description": "Page size (default 50, max 200)"},
				"cursor":  str("nextCursor of the previous page"),
			}),
		},
		{
			Name: "chat_gaps",
			Description: "Docs gaps: the questions chat visitors asked whose searches found nothing or scored below a " +
				"threshold, grouped by normalised text, most frequent first — what to document next. Global admin. Read-only.",
			InputSchema: window(map[string]interface{}{
				"minCount":  map[string]interface{}{"type": "integer", "description": "Only questions asked at least this often (default 1)"},
				"threshold": map[string]interface{}{"type": "number", "description": "A best score below this is a gap (default MDDB_CHAT_GAP_SCORE or 0.3)"},
				"limit":     map[string]interface{}{"type": "integer", "description": "At most this many groups (default 100, max 1000)"},
			}),
		},
	}
}

// mcpChatWindow reads scenario, from and to.
func mcpChatWindow(args map[string]interface{}) (ChatLogFilter, error) {
	bound := func(name string, end bool) (int64, error) {
		if n, isNum := args[name].(float64); isNum {
			return int64(n), nil
		}
		return parseChatTime(name, mcpGetString(args, name), end)
	}
	from, err := bound("from", false)
	if err != nil {
		return ChatLogFilter{}, err
	}
	to, err := bound("to", true)
	return ChatLogFilter{Scenario: mcpGetString(args, "scenario"), From: from, To: to}, err
}

func (s *MCPToolServer) toolChatLogList(ctx context.Context, args map[string]interface{}) (string, error) {
	f, err := mcpChatWindow(args)
	if err != nil {
		return "", err
	}
	f.Rating, f.Outcome = mcpGetString(args, "rating"), mcpGetString(args, "outcome")
	f.Cursor, f.Limit = mcpGetString(args, "cursor"), mcpGetInt(args, "limit")
	page, err := s.client.ChatLogList(ctx, f)
	if err != nil {
		return "", err
	}
	data, _ := json.MarshalIndent(page, "", "  ")
	return string(data), nil
}

func (s *MCPToolServer) toolChatGaps(ctx context.Context, args map[string]interface{}) (string, error) {
	w, err := mcpChatWindow(args)
	if err != nil {
		return "", err
	}
	res, err := s.client.ChatGaps(ctx, ChatGapsFilter{Scenario: w.Scenario, From: w.From, To: w.To,
		MinCount: mcpGetInt(args, "minCount"), Threshold: mcpGetFloat(args, "threshold"), Limit: mcpGetInt(args, "limit")})
	if err != nil {
		return "", err
	}
	if len(res.Gaps) == 0 {
		return fmt.Sprintf("No docs gaps (threshold %g).", res.Threshold), nil
	}
	data, _ := json.MarshalIndent(res, "", "  ")
	return string(data), nil
}
