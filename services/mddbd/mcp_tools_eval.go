package main

import (
	"context"

	json "mddb/internal/jsonx"
)

// Evaluation and gain tools (SRCH-019, AGT-004): an agent records the queries it cares about
// with the documents that answer them, then measures search configurations
// on them instead of guessing which one is better.

// mcpEvalTools are the tool definitions, appended to the built-in list.
func mcpEvalTools() []MCPTool {
	configItem := map[string]interface{}{"type": "object", "properties": map[string]interface{}{
		"name":            map[string]interface{}{"type": "string"},
		"searchType":      map[string]interface{}{"type": "string", "enum": []string{"fts", "vector", "hybrid"}},
		"algorithm":       map[string]interface{}{"type": "string", "description": "FTS algorithm, e.g. bm25, bm25f"},
		"vectorAlgorithm": map[string]interface{}{"type": "string"},
		"alpha":           map[string]interface{}{"type": "number", "description": "Hybrid weight of the vector half"},
		"strategy":        map[string]interface{}{"type": "string", "description": "Hybrid fusion: alpha or rrf"},
		"retrievalMode":   map[string]interface{}{"type": "string"},
	}}
	return []MCPTool{
		{
			Name: "add_eval_query",
			Description: "Add a query to a collection's evaluation set: the question and the keys of the documents " +
				"that should answer it, optionally graded 1-3. run_eval then scores search configurations on the set. " +
				"Reusing an id replaces that query.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"collection":    map[string]interface{}{"type": "string"},
					"query":         map[string]interface{}{"type": "string"},
					"expected_keys": map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}},
					"grades":        map[string]interface{}{"type": "object", "description": "key -> relevance 0..3 for nDCG"},
					"id":            map[string]interface{}{"type": "string", "description": "Optional; generated when absent"},
				},
				"required": []string{"collection", "query", "expected_keys"},
			},
		},
		{
			Name: "run_eval",
			Description: "Measure retrieval on a collection's evaluation set: recall@k, nDCG@k, MRR, p50/p99 latency " +
				"and returned tokens per search configuration. Without configs it compares fts/bm25, hybrid and vector. " +
				"Read-only; use it to choose a configuration by numbers, then store it with set_collection_config.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"collection": map[string]interface{}{"type": "string"},
					"k":          map[string]interface{}{"type": "integer", "description": "Cut-off, default 10, max 100"},
					"configs":    map[string]interface{}{"type": "array", "items": configItem, "description": "Up to 8"},
					"details":    map[string]interface{}{"type": "boolean", "description": "Add per-query results"},
				},
				"required": []string{"collection"},
			},
		},
	}
}

// toolAddEvalQuery stores one evaluation query.
func (s *MCPToolServer) toolAddEvalQuery(ctx context.Context, args map[string]interface{}) (string, error) {
	q := EvalQuery{ID: mcpGetString(args, "id"), Query: mcpGetString(args, "query"),
		ExpectedKeys: mcpGetStringSlice(args, "expected_keys")}
	if g, ok := args["grades"].(map[string]interface{}); ok {
		q.Grades = map[string]int{}
		for k, v := range g {
			if f, ok := v.(float64); ok {
				q.Grades[k] = int(f)
			}
		}
	}
	saved, err := s.client.AddEvalQuery(ctx, mcpGetString(args, "collection"), q)
	if err != nil {
		return "", err
	}
	data, _ := json.MarshalIndent(saved, "", "  ")
	return string(data), nil
}

// toolRunEval scores search configurations on the evaluation set.
func (s *MCPToolServer) toolRunEval(ctx context.Context, args map[string]interface{}) (string, error) {
	req := EvalRequest{Collection: mcpGetString(args, "collection"), K: mcpGetInt(args, "k"), Details: mcpGetBool(args, "details")}
	if raw, ok := args["configs"]; ok {
		b, _ := json.Marshal(raw)
		if err := json.Unmarshal(b, &req.Configs); err != nil {
			return "", err
		}
	}
	res, err := s.client.RunEval(ctx, req)
	if err != nil {
		return "", err
	}
	data, _ := json.MarshalIndent(res, "", "  ")
	return string(data), nil
}

// AddEvalQuery stores an evaluation query (SRCH-019).
func (c *DirectClient) AddEvalQuery(ctx context.Context, collection string, q EvalQuery) (EvalQuery, error) {
	return c.server.AddEvalQuery(collection, q)
}

// RunEval runs the evaluation (SRCH-019).
func (c *DirectClient) RunEval(ctx context.Context, req EvalRequest) (*EvalResult, error) {
	return c.server.RunEval(ctx, req)
}

// mcpGainTool reports what retrieval saved (AGT-004).
func mcpGainTool() MCPTool {
	return MCPTool{
		Name: "get_gain",
		Description: "What retrieval saved over a window of days, server-wide: searches by type and algorithm, " +
			"tokens returned, tokens avoided (whole documents minus the passages or chunks returned instead) and the " +
			"share saved. Counts only, no queries or keys. Read-only.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"window": map[string]interface{}{"type": "string", "description": "Days, e.g. 7d or 30d (default 30d, max 365d)"},
				"daily":  map[string]interface{}{"type": "boolean", "description": "Add a per-day breakdown"},
			},
		},
	}
}

// toolGetGain returns the gain report.
func (s *MCPToolServer) toolGetGain(ctx context.Context, args map[string]interface{}) (string, error) {
	rep, err := s.client.Gain(ctx, mcpGetString(args, "window"), mcpGetBool(args, "daily"))
	if err != nil {
		return "", err
	}
	data, _ := json.MarshalIndent(rep, "", "  ")
	return string(data), nil
}

// Gain reports retrieval gain over a window (AGT-004).
func (c *DirectClient) Gain(ctx context.Context, window string, daily bool) (*GainReport, error) {
	days, err := parseGainWindow(window)
	if err != nil {
		return nil, err
	}
	return c.server.Gain.Report(days, daily)
}
