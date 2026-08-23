package main

import (
	"context"
	"fmt"
	"time"

	json "mddb/internal/jsonx"
)

// toolMemoryStartSession creates a new memory/conversation session via MCP.
func (s *MCPToolServer) toolMemoryStartSession(ctx context.Context, args map[string]interface{}) (string, error) {
	userID := mcpGetString(args, "user_id")
	if userID == "" {
		return "", fmt.Errorf("user_id is required")
	}

	sessionID := generateSessionID()
	title := mcpGetString(args, "title")
	if title == "" {
		title = "Session " + sessionID[:8]
	}

	scenario := mcpGetString(args, "scenario")
	meta := map[string][]string{
		"userId":   {userID},
		"type":     {"session"},
		"status":   {"active"},
		"scenario": {scenario},
	}

	// Merge extra meta
	if extraMeta := mcpGetMetaMap(args, "meta"); len(extraMeta) > 0 {
		for k, v := range extraMeta {
			meta[k] = v
		}
	}

	content := fmt.Sprintf("# Session: %s\n\nUser: %s\nScenario: %s", title, userID, scenario)

	doc, err := s.client.Add(ctx, &MCPAddRequest{
		Collection: memorySessionsCollection,
		Key:        sessionID,
		Lang:       "en",
		Meta:       meta,
		ContentMD:  content,
	})
	if err != nil {
		return "", fmt.Errorf("failed to create session: %w", err)
	}

	resp := map[string]interface{}{
		"sessionId": sessionID,
		"userId":    userID,
		"scenario":  scenario,
		"title":     title,
		"createdAt": doc.AddedAt,
	}
	data, _ := json.MarshalIndent(resp, "", "  ")
	return fmt.Sprintf("Memory session created:\n%s", string(data)), nil
}

// toolMemoryAddMessage adds a message to a memory session via MCP.
func (s *MCPToolServer) toolMemoryAddMessage(ctx context.Context, args map[string]interface{}) (string, error) {
	sessionID := mcpGetString(args, "session_id")
	role := mcpGetString(args, "role")
	content := mcpGetString(args, "content")

	if sessionID == "" || role == "" || content == "" {
		return "", fmt.Errorf("session_id, role, and content are required")
	}

	validRoles := map[string]bool{"user": true, "assistant": true, "system": true, "tool": true}
	if !validRoles[role] {
		return "", fmt.Errorf("invalid role: must be user, assistant, system, or tool")
	}

	msgID := generateMemoryMessageID()
	now := fmt.Sprintf("%d", time.Now().Unix())

	meta := map[string][]string{
		"sessionId": {sessionID},
		"role":      {role},
		"type":      {"message"},
		"timestamp": {now},
	}

	if extraMeta := mcpGetMetaMap(args, "meta"); len(extraMeta) > 0 {
		for k, v := range extraMeta {
			meta[k] = v
		}
	}

	msgKey := fmt.Sprintf("%s/%s-%s", sessionID, now, msgID)

	doc, err := s.client.Add(ctx, &MCPAddRequest{
		Collection: memoryMessagesCollection,
		Key:        msgKey,
		Lang:       "en",
		Meta:       meta,
		ContentMD:  content,
	})
	if err != nil {
		return "", fmt.Errorf("failed to add message: %w", err)
	}

	resp := map[string]interface{}{
		"messageId": doc.ID,
		"sessionId": sessionID,
		"role":      role,
		"createdAt": doc.AddedAt,
	}
	data, _ := json.MarshalIndent(resp, "", "  ")
	return fmt.Sprintf("Message added to session:\n%s", string(data)), nil
}

// toolMemoryRecall performs semantic/hybrid recall across memory via MCP.
func (s *MCPToolServer) toolMemoryRecall(ctx context.Context, args map[string]interface{}) (string, error) {
	query := mcpGetString(args, "query")
	if query == "" {
		return "", fmt.Errorf("query is required")
	}

	topK := mcpGetInt(args, "top_k")
	if topK <= 0 {
		topK = defaultRecallTopK
	}
	threshold := mcpGetFloat(args, "threshold")
	strategy := mcpGetString(args, "strategy")
	if strategy == "" {
		strategy = "hybrid"
	}

	filterMeta := map[string][]string{
		"type": {"message"},
	}
	if userID := mcpGetString(args, "user_id"); userID != "" {
		filterMeta["userId"] = []string{userID}
	}
	if sessionID := mcpGetString(args, "session_id"); sessionID != "" {
		filterMeta["sessionId"] = []string{sessionID}
	}
	if role := mcpGetString(args, "role"); role != "" {
		filterMeta["role"] = []string{role}
	}

	includeContent := false
	if v, ok := args["include_content"].(bool); ok {
		includeContent = v
	}

	switch strategy {
	case "semantic":
		return s.memoryRecallViaSemantic(ctx, query, topK, threshold, filterMeta, includeContent)
	case "keyword":
		return s.memoryRecallViaFTS(ctx, query, topK, filterMeta, includeContent)
	default: // hybrid
		return s.memoryRecallViaHybrid(ctx, query, topK, threshold, filterMeta, includeContent)
	}
}

func (s *MCPToolServer) memoryRecallViaSemantic(ctx context.Context, query string, topK int, threshold float64, filterMeta map[string][]string, includeContent bool) (string, error) {
	resp, err := s.client.VectorSearch(ctx, &MCPVectorSearchRequest{
		Collection:     memoryMessagesCollection,
		Query:          query,
		TopK:           topK,
		Threshold:      threshold,
		FilterMeta:     filterMeta,
		IncludeContent: includeContent,
		Algorithm:      "flat",
		DistanceMetric: "cosine",
	})
	if err != nil {
		return "", fmt.Errorf("semantic recall failed: %w", err)
	}

	results := make([]map[string]interface{}, 0, len(resp.Results))
	for i, r := range resp.Results {
		item := map[string]interface{}{
			"rank":      i + 1,
			"score":     r.Score,
			"sessionId": metaFirst(r.Document.Meta, "sessionId"),
			"role":      metaFirst(r.Document.Meta, "role"),
			"key":       r.Document.Key,
		}
		if includeContent {
			item["content"] = r.Document.ContentMD
		}
		results = append(results, item)
	}

	data, _ := json.MarshalIndent(map[string]interface{}{
		"strategy": "semantic",
		"total":    len(results),
		"results":  results,
	}, "", "  ")
	return string(data), nil
}

func (s *MCPToolServer) memoryRecallViaFTS(ctx context.Context, query string, topK int, filterMeta map[string][]string, includeContent bool) (string, error) {
	resp, err := s.client.FTSSearch(ctx, &MCPFTSSearchRequest{
		Collection:     memoryMessagesCollection,
		Query:          query,
		Limit:          topK,
		Algorithm:      "bm25",
		Fuzzy:          1,
		IncludeContent: true,
	})
	if err != nil {
		return "", fmt.Errorf("keyword recall failed: %w", err)
	}

	results := make([]map[string]interface{}, 0)
	for i, r := range resp.Results {
		// Apply meta filter manually
		if !matchesMeta(r.Document.Meta, filterMeta) {
			continue
		}
		item := map[string]interface{}{
			"rank":      i + 1,
			"score":     r.Score,
			"sessionId": metaFirst(r.Document.Meta, "sessionId"),
			"role":      metaFirst(r.Document.Meta, "role"),
			"key":       r.Document.Key,
		}
		if includeContent {
			item["content"] = r.Document.ContentMD
		}
		results = append(results, item)
		if len(results) >= topK {
			break
		}
	}

	data, _ := json.MarshalIndent(map[string]interface{}{
		"strategy": "keyword",
		"total":    len(results),
		"results":  results,
	}, "", "  ")
	return string(data), nil
}

func (s *MCPToolServer) memoryRecallViaHybrid(ctx context.Context, query string, topK int, threshold float64, filterMeta map[string][]string, includeContent bool) (string, error) {
	resp, err := s.client.HybridSearch(ctx, &MCPHybridSearchRequest{
		Collection: memoryMessagesCollection,
		Query:      query,
		TopK:       topK,
		Strategy:   "rrf",
		RRFK:       60,
		Threshold:  threshold,
		FilterMeta: filterMeta,
	})
	if err != nil {
		return "", fmt.Errorf("hybrid recall failed: %w", err)
	}

	results := make([]map[string]interface{}, 0, len(resp.Results))
	for i, r := range resp.Results {
		item := map[string]interface{}{
			"rank":      i + 1,
			"score":     r.CombinedScore,
			"ftsScore":  r.FTSScore,
			"vecScore":  r.VectorScore,
			"sessionId": metaFirst(r.Document.Meta, "sessionId"),
			"role":      metaFirst(r.Document.Meta, "role"),
			"key":       r.Document.Key,
		}
		if includeContent {
			item["content"] = r.Document.ContentMD
		}
		results = append(results, item)
	}

	data, _ := json.MarshalIndent(map[string]interface{}{
		"strategy": "hybrid",
		"total":    len(results),
		"results":  results,
	}, "", "  ")
	return string(data), nil
}

// toolMemorySummarize generates and stores a session summary via MCP.
func (s *MCPToolServer) toolMemorySummarize(ctx context.Context, args map[string]interface{}) (string, error) {
	sessionID := mcpGetString(args, "session_id")
	if sessionID == "" {
		return "", fmt.Errorf("session_id is required")
	}

	// Fetch session messages
	resp, err := s.client.Search(ctx, &MCPSearchRequest{
		Collection: memoryMessagesCollection,
		FilterMeta: map[string][]string{
			"sessionId": {sessionID},
			"type":      {"message"},
		},
		Sort:           "addedAt",
		Asc:            true,
		Limit:          500,
		IncludeContent: true,
	})
	if err != nil {
		return "", fmt.Errorf("failed to fetch messages: %w", err)
	}
	if resp.Total == 0 {
		return "", fmt.Errorf("no messages found for session %s", sessionID)
	}

	// Build summary
	summary := fmt.Sprintf("# Session Summary: %s\n\nMessages: %d\n\n## Conversation\n\n", sessionID[:8], resp.Total)
	for _, msg := range resp.Documents {
		role := metaFirst(msg.Meta, "role")
		content := msg.ContentMD
		if len(content) > 200 {
			content = content[:200] + "..."
		}
		summary += fmt.Sprintf("**%s**: %s\n\n", role, content)
	}

	now := fmt.Sprintf("%d", time.Now().Unix())
	summaryKey := fmt.Sprintf("%s/%s", sessionID, now)

	meta := map[string][]string{
		"sessionId": {sessionID},
		"type":      {"summary"},
		"timestamp": {now},
		"messages":  {fmt.Sprintf("%d", resp.Total)},
	}
	if userID := mcpGetString(args, "user_id"); userID != "" {
		meta["userId"] = []string{userID}
	}

	doc, err := s.client.Add(ctx, &MCPAddRequest{
		Collection: memorySummariesCollection,
		Key:        summaryKey,
		Lang:       "en",
		Meta:       meta,
		ContentMD:  summary,
	})
	if err != nil {
		return "", fmt.Errorf("failed to store summary: %w", err)
	}

	result := map[string]interface{}{
		"summaryId": doc.ID,
		"sessionId": sessionID,
		"messages":  resp.Total,
		"createdAt": doc.AddedAt,
		"summary":   summary,
	}
	data, _ := json.MarshalIndent(result, "", "  ")
	return fmt.Sprintf("Session summarized:\n%s", string(data)), nil
}

// toolMemoryListSessions lists memory sessions via MCP.
func (s *MCPToolServer) toolMemoryListSessions(ctx context.Context, args map[string]interface{}) (string, error) {
	filterMeta := map[string][]string{
		"type": {"session"},
	}
	if userID := mcpGetString(args, "user_id"); userID != "" {
		filterMeta["userId"] = []string{userID}
	}
	if scenario := mcpGetString(args, "scenario"); scenario != "" {
		filterMeta["scenario"] = []string{scenario}
	}

	limit := mcpGetInt(args, "limit")
	if limit <= 0 {
		limit = 50
	}
	offset := mcpGetInt(args, "offset")
	sortField := mcpGetString(args, "sort")
	if sortField == "" {
		sortField = "addedAt"
	}

	resp, err := s.client.Search(ctx, &MCPSearchRequest{
		Collection:     memorySessionsCollection,
		FilterMeta:     filterMeta,
		Sort:           sortField,
		Limit:          limit,
		Offset:         offset,
		IncludeContent: true,
	})
	if err != nil {
		return "", fmt.Errorf("failed to list sessions: %w", err)
	}

	sessions := make([]map[string]interface{}, 0, len(resp.Documents))
	for _, doc := range resp.Documents {
		sessions = append(sessions, map[string]interface{}{
			"sessionId": doc.Key,
			"userId":    metaFirst(doc.Meta, "userId"),
			"scenario":  metaFirst(doc.Meta, "scenario"),
			"status":    metaFirst(doc.Meta, "status"),
			"createdAt": doc.AddedAt,
			"updatedAt": doc.UpdatedAt,
		})
	}

	data, _ := json.MarshalIndent(map[string]interface{}{
		"total":    resp.Total,
		"sessions": sessions,
	}, "", "  ")
	return string(data), nil
}

// toolMemorySessionHistory returns the message history for a session via MCP.
func (s *MCPToolServer) toolMemorySessionHistory(ctx context.Context, args map[string]interface{}) (string, error) {
	sessionID := mcpGetString(args, "session_id")
	if sessionID == "" {
		return "", fmt.Errorf("session_id is required")
	}

	limit := mcpGetInt(args, "limit")
	if limit <= 0 {
		limit = 100
	}
	offset := mcpGetInt(args, "offset")

	resp, err := s.client.Search(ctx, &MCPSearchRequest{
		Collection: memoryMessagesCollection,
		FilterMeta: map[string][]string{
			"sessionId": {sessionID},
			"type":      {"message"},
		},
		Sort:           "addedAt",
		Asc:            true,
		Limit:          limit,
		Offset:         offset,
		IncludeContent: true,
	})
	if err != nil {
		return "", fmt.Errorf("failed to fetch history: %w", err)
	}

	messages := make([]map[string]interface{}, 0, len(resp.Documents))
	for _, doc := range resp.Documents {
		messages = append(messages, map[string]interface{}{
			"role":      metaFirst(doc.Meta, "role"),
			"content":   doc.ContentMD,
			"createdAt": doc.AddedAt,
			"key":       doc.Key,
		})
	}

	data, _ := json.MarshalIndent(map[string]interface{}{
		"sessionId": sessionID,
		"total":     resp.Total,
		"messages":  messages,
	}, "", "  ")
	return string(data), nil
}

// matchesMeta checks if a document's meta matches all filter criteria.
func matchesMeta(docMeta, filterMeta map[string][]string) bool {
	for key, filterVals := range filterMeta {
		docVals, exists := docMeta[key]
		if !exists {
			return false
		}
		matched := false
		for _, fv := range filterVals {
			for _, dv := range docVals {
				if dv == fv {
					matched = true
					break
				}
			}
			if matched {
				break
			}
		}
		if !matched {
			return false
		}
	}
	return true
}
