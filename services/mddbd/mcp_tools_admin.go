package main

import (
	"context"
	"fmt"

	json "mddb/internal/jsonx"
)

// --- delete_collection / truncate ---

func (s *MCPToolServer) toolDeleteCollection(ctx context.Context, args map[string]interface{}) (string, error) {
	collection := mcpGetString(args, "collection")
	if collection == "" {
		return "", fmt.Errorf("collection is required")
	}
	resp, err := s.client.DeleteCollection(ctx, &MCPDeleteCollectionRequest{Collection: collection})
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("Collection %q deleted (%d documents removed)", collection, resp.Deleted), nil
}

func (s *MCPToolServer) toolTruncateRevisions(ctx context.Context, args map[string]interface{}) (string, error) {
	req := &MCPTruncateRequest{
		Collection: mcpGetString(args, "collection"),
		KeepRevs:   mcpGetInt(args, "keep_revs"),
	}
	if dc, ok := args["drop_cache"].(bool); ok {
		req.DropCache = dc
	}
	resp, err := s.client.Truncate(ctx, req)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("Revision history truncated: %s", resp.Status), nil
}

// --- revisions ---

func (s *MCPToolServer) toolListRevisions(ctx context.Context, args map[string]interface{}) (string, error) {
	collection := mcpGetString(args, "collection")
	key := mcpGetString(args, "key")
	lang := mcpGetString(args, "lang")
	if collection == "" || key == "" || lang == "" {
		return "", fmt.Errorf("collection, key, and lang are required")
	}
	resp, err := s.client.ListRevisions(ctx, collection, key, lang)
	if err != nil {
		return "", err
	}
	data, _ := json.MarshalIndent(resp, "", "  ")
	return string(data), nil
}

func (s *MCPToolServer) toolRestoreRevision(ctx context.Context, args map[string]interface{}) (string, error) {
	collection := mcpGetString(args, "collection")
	key := mcpGetString(args, "key")
	lang := mcpGetString(args, "lang")
	timestamp := int64(mcpGetInt(args, "timestamp"))
	if collection == "" || key == "" || lang == "" || timestamp == 0 {
		return "", fmt.Errorf("collection, key, lang, and timestamp are required")
	}
	doc, err := s.client.RestoreRevision(ctx, collection, key, lang, timestamp)
	if err != nil {
		return "", err
	}
	data, _ := json.MarshalIndent(doc, "", "  ")
	return fmt.Sprintf("Revision restored successfully:\n%s", string(data)), nil
}

// --- synonyms ---

func (s *MCPToolServer) toolListSynonyms(ctx context.Context, args map[string]interface{}) (string, error) {
	collection := mcpGetString(args, "collection")
	if collection == "" {
		return "", fmt.Errorf("collection is required")
	}
	resp, err := s.client.ListSynonyms(ctx, collection)
	if err != nil {
		return "", err
	}
	data, _ := json.MarshalIndent(resp, "", "  ")
	return string(data), nil
}

func (s *MCPToolServer) toolAddSynonym(ctx context.Context, args map[string]interface{}) (string, error) {
	collection := mcpGetString(args, "collection")
	term := mcpGetString(args, "term")
	if collection == "" || term == "" {
		return "", fmt.Errorf("collection and term are required")
	}
	var synonyms []string
	if raw, ok := args["synonyms"].([]interface{}); ok {
		for _, v := range raw {
			if str, ok := v.(string); ok {
				synonyms = append(synonyms, str)
			}
		}
	}
	if len(synonyms) == 0 {
		return "", fmt.Errorf("synonyms array is required and must not be empty")
	}
	if err := s.client.SetSynonym(ctx, collection, term, synonyms); err != nil {
		return "", err
	}
	return fmt.Sprintf("Synonym group set: %q -> %v", term, synonyms), nil
}

func (s *MCPToolServer) toolDeleteSynonym(ctx context.Context, args map[string]interface{}) (string, error) {
	collection := mcpGetString(args, "collection")
	term := mcpGetString(args, "term")
	if collection == "" || term == "" {
		return "", fmt.Errorf("collection and term are required")
	}
	if err := s.client.DeleteSynonym(ctx, collection, term); err != nil {
		return "", err
	}
	return fmt.Sprintf("Synonym group deleted for term: %q", term), nil
}

// --- stopwords ---

func (s *MCPToolServer) toolListStopWords(ctx context.Context, args map[string]interface{}) (string, error) {
	collection := mcpGetString(args, "collection")
	if collection == "" {
		return "", fmt.Errorf("collection is required")
	}
	resp, err := s.client.ListStopWords(ctx, collection)
	if err != nil {
		return "", err
	}
	data, _ := json.MarshalIndent(resp, "", "  ")
	return string(data), nil
}

func (s *MCPToolServer) toolAddStopWords(ctx context.Context, args map[string]interface{}) (string, error) {
	collection := mcpGetString(args, "collection")
	if collection == "" {
		return "", fmt.Errorf("collection is required")
	}
	var words []string
	if raw, ok := args["words"].([]interface{}); ok {
		for _, v := range raw {
			if str, ok := v.(string); ok {
				words = append(words, str)
			}
		}
	}
	if len(words) == 0 {
		return "", fmt.Errorf("words array is required and must not be empty")
	}
	if err := s.client.AddStopWords(ctx, collection, words); err != nil {
		return "", err
	}
	return fmt.Sprintf("Added %d stop words to %q", len(words), collection), nil
}

func (s *MCPToolServer) toolDeleteStopWords(ctx context.Context, args map[string]interface{}) (string, error) {
	collection := mcpGetString(args, "collection")
	if collection == "" {
		return "", fmt.Errorf("collection is required")
	}
	var words []string
	if raw, ok := args["words"].([]interface{}); ok {
		for _, v := range raw {
			if str, ok := v.(string); ok {
				words = append(words, str)
			}
		}
	}
	if len(words) == 0 {
		return "", fmt.Errorf("words array is required and must not be empty")
	}
	if err := s.client.DeleteStopWords(ctx, collection, words); err != nil {
		return "", err
	}
	return fmt.Sprintf("Removed %d stop words from %q", len(words), collection), nil
}

// --- meta-keys / checksum ---

func (s *MCPToolServer) toolGetMetaKeys(ctx context.Context, args map[string]interface{}) (string, error) {
	collection := mcpGetString(args, "collection")
	if collection == "" {
		return "", fmt.Errorf("collection is required")
	}
	resp, err := s.client.GetMetaKeys(ctx, collection)
	if err != nil {
		return "", err
	}
	data, _ := json.MarshalIndent(resp, "", "  ")
	return string(data), nil
}

func (s *MCPToolServer) toolGetChecksum(ctx context.Context, args map[string]interface{}) (string, error) {
	collection := mcpGetString(args, "collection")
	if collection == "" {
		return "", fmt.Errorf("collection is required")
	}
	resp, err := s.client.GetChecksum(ctx, collection)
	if err != nil {
		return "", err
	}
	data, _ := json.MarshalIndent(resp, "", "  ")
	return string(data), nil
}

// --- automation ---

func (s *MCPToolServer) toolListAutomation(ctx context.Context, args map[string]interface{}) (string, error) {
	filterType := mcpGetString(args, "type")
	resp, err := s.client.ListAutomation(ctx, filterType)
	if err != nil {
		return "", err
	}
	data, _ := json.MarshalIndent(resp, "", "  ")
	return string(data), nil
}

func (s *MCPToolServer) toolCreateAutomation(ctx context.Context, args map[string]interface{}) (string, error) {
	rule := mcpArgsToAutomationRule(args)
	created, err := s.client.CreateAutomation(ctx, rule)
	if err != nil {
		return "", err
	}
	data, _ := json.MarshalIndent(created, "", "  ")
	return fmt.Sprintf("Automation rule created:\n%s", string(data)), nil
}

func (s *MCPToolServer) toolGetAutomation(ctx context.Context, args map[string]interface{}) (string, error) {
	id := mcpGetString(args, "id")
	if id == "" {
		return "", fmt.Errorf("id is required")
	}
	rule, err := s.client.GetAutomation(ctx, id)
	if err != nil {
		return "", err
	}
	data, _ := json.MarshalIndent(rule, "", "  ")
	return string(data), nil
}

func (s *MCPToolServer) toolUpdateAutomation(ctx context.Context, args map[string]interface{}) (string, error) {
	id := mcpGetString(args, "id")
	if id == "" {
		return "", fmt.Errorf("id is required")
	}
	rule := mcpArgsToAutomationRule(args)
	updated, err := s.client.UpdateAutomation(ctx, id, rule)
	if err != nil {
		return "", err
	}
	data, _ := json.MarshalIndent(updated, "", "  ")
	return fmt.Sprintf("Automation rule updated:\n%s", string(data)), nil
}

func (s *MCPToolServer) toolDeleteAutomation(ctx context.Context, args map[string]interface{}) (string, error) {
	id := mcpGetString(args, "id")
	if id == "" {
		return "", fmt.Errorf("id is required")
	}
	if err := s.client.DeleteAutomation(ctx, id); err != nil {
		return "", err
	}
	return fmt.Sprintf("Automation rule deleted: %s", id), nil
}

func (s *MCPToolServer) toolTestAutomation(ctx context.Context, args map[string]interface{}) (string, error) {
	id := mcpGetString(args, "id")
	if id == "" {
		return "", fmt.Errorf("id is required")
	}
	result, err := s.client.TestAutomation(ctx, id)
	if err != nil {
		return "", err
	}
	return result, nil
}

func (s *MCPToolServer) toolGetAutomationLogs(ctx context.Context, args map[string]interface{}) (string, error) {
	limit := mcpGetInt(args, "limit")
	cursor := mcpGetString(args, "cursor")
	ruleID := mcpGetString(args, "rule_id")
	status := mcpGetString(args, "status")
	resp, err := s.client.ListAutomationLogs(ctx, limit, cursor, ruleID, status)
	if err != nil {
		return "", err
	}
	data, _ := json.MarshalIndent(resp, "", "  ")
	return string(data), nil
}

// mcpArgsToAutomationRule converts MCP tool arguments to an AutomationRule.
func mcpArgsToAutomationRule(args map[string]interface{}) AutomationRule {
	rule := AutomationRule{
		Type:       mcpGetString(args, "type"),
		Name:       mcpGetString(args, "name"),
		Enabled:    true,
		URL:        mcpGetString(args, "url"),
		Method:     mcpGetString(args, "method"),
		Collection: mcpGetString(args, "collection"),
		SearchType: mcpGetString(args, "searchType"),
		Query:      mcpGetString(args, "query"),
		WebhookID:  mcpGetString(args, "webhookId"),
		Schedule:   mcpGetString(args, "schedule"),
		TriggerID:  mcpGetString(args, "triggerId"),
	}
	if enabled, ok := args["enabled"].(bool); ok {
		rule.Enabled = enabled
	}
	if threshold, ok := args["threshold"].(float64); ok {
		rule.Threshold = threshold
	}
	if eventsRaw, ok := args["events"].([]interface{}); ok {
		for _, e := range eventsRaw {
			if str, ok := e.(string); ok {
				rule.Events = append(rule.Events, str)
			}
		}
	}
	if headers, ok := args["headers"].(map[string]interface{}); ok {
		rule.Headers = make(map[string]string)
		for k, v := range headers {
			if str, ok := v.(string); ok {
				rule.Headers[k] = str
			}
		}
	}
	return rule
}
