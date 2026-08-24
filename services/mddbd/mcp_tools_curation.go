package main

import (
	"context"
	"fmt"

	json "mddb/internal/jsonx"
)

// --- Curation MCP tools (v2.9.14+) ---
// These tools expose CRUD over curation rules (pinned + hidden results).
// They're dispatched from mcp_tools.go switch in MCPToolServer.CallTool and
// invoke the underlying CurationManager via the MCPClient.

func (s *MCPToolServer) toolListCurationRules(ctx context.Context, args map[string]interface{}) (string, error) {
	collection := mcpGetString(args, "collection")
	rules, err := s.client.ListCurationRules(ctx, collection)
	if err != nil {
		return "", err
	}
	data, _ := json.MarshalIndent(map[string]any{
		"rules":      rules,
		"total":      len(rules),
		"collection": collection,
	}, "", "  ")
	return string(data), nil
}

func (s *MCPToolServer) toolCreateCurationRule(ctx context.Context, args map[string]interface{}) (string, error) {
	rule, err := parseCurationArgs(args)
	if err != nil {
		return "", err
	}
	created, err := s.client.CreateCurationRule(ctx, rule)
	if err != nil {
		return "", err
	}
	data, _ := json.MarshalIndent(created, "", "  ")
	return string(data), nil
}

func (s *MCPToolServer) toolUpdateCurationRule(ctx context.Context, args map[string]interface{}) (string, error) {
	rule, err := parseCurationArgs(args)
	if err != nil {
		return "", err
	}
	if rule.ID == "" {
		return "", fmt.Errorf("id is required for update")
	}
	updated, err := s.client.UpdateCurationRule(ctx, rule)
	if err != nil {
		return "", err
	}
	data, _ := json.MarshalIndent(updated, "", "  ")
	return string(data), nil
}

func (s *MCPToolServer) toolDeleteCurationRule(ctx context.Context, args map[string]interface{}) (string, error) {
	id := mcpGetString(args, "id")
	if id == "" {
		return "", fmt.Errorf("id is required")
	}
	if err := s.client.DeleteCurationRule(ctx, id); err != nil {
		return "", err
	}
	return fmt.Sprintf(`{"status":"deleted","id":%q}`, id), nil
}

// parseCurationArgs unwraps a tool call into a CurationRule. Pins can be
// passed either as a list of objects (`{key, lang, position}`) or as a flat
// ordered list of keys — the latter is what small LLMs tend to emit, so we
// normalize it. Hides must be a list of strings.
func parseCurationArgs(args map[string]interface{}) (*CurationRule, error) {
	rule := &CurationRule{
		ID:         mcpGetString(args, "id"),
		Collection: mcpGetString(args, "collection"),
		Query:      mcpGetString(args, "query"),
		MatchMode:  mcpGetString(args, "match_mode"),
	}
	if _, set := args["enabled"]; set {
		if b, ok := args["enabled"].(bool); ok {
			rule.Enabled = b
		}
	} else {
		rule.Enabled = true // sensible default for tool-authored rules
	}

	if pinsAny, ok := args["pins"].([]interface{}); ok {
		for _, item := range pinsAny {
			switch v := item.(type) {
			case string:
				rule.Pins = append(rule.Pins, PinnedDoc{Key: v, Position: len(rule.Pins) + 1})
			case map[string]interface{}:
				rule.Pins = append(rule.Pins, PinnedDoc{
					Key:      mcpGetString(v, "key"),
					Lang:     mcpGetString(v, "lang"),
					Position: mcpGetInt(v, "position"),
				})
			}
		}
	}
	if hidesAny, ok := args["hides"].([]interface{}); ok {
		for _, item := range hidesAny {
			if str, ok := item.(string); ok {
				rule.Hides = append(rule.Hides, str)
			}
		}
	}
	return rule, nil
}
