package main

import (
	"fmt"

	json "mddb/internal/jsonx"
	"mddb/internal/rerank"
)

// The rerank argument of the search tools (RAG-011): one schema, one reader,
// shared by semantic_search, hybrid_search, full_text_search and cross_search.

// mcpRerankSchema describes the rerank argument.
var mcpRerankSchema = map[string]interface{}{
	"type": "object",
	"description": "Order the best candidates with the server's reranking model, when one is configured: it reads the " +
		"query and each candidate passage together. Slower and a provider call; off unless enabled here or in the " +
		"collection's retrieval profile. On a timeout or provider error the search's own order is returned with rerankSkipped",
	"properties": map[string]interface{}{
		"enabled": map[string]interface{}{"type": "boolean"},
		"candidates": map[string]interface{}{"type": "integer", "minimum": 0, "maximum": rerank.MaxCandidatesCeiling,
			"description": "results the model orders (default: the profile, then the provider's maxCandidates)"},
		"top_n":   map[string]interface{}{"type": "integer", "minimum": 0, "description": "results kept after reranking (default: top_k)"},
		"explain": map[string]interface{}{"type": "boolean", "description": "report each candidate's rank before and after"},
	},
}

// mcpGetRerank reads the rerank argument; absent is a request without one.
func mcpGetRerank(args map[string]interface{}) (RerankRequest, error) {
	raw, ok := args["rerank"]
	if !ok || raw == nil {
		return RerankRequest{}, nil
	}
	// The argument came in as JSON, so it marshals back.
	data, _ := json.Marshal(raw)
	var wire struct {
		Enabled    *bool `json:"enabled"`
		Candidates int   `json:"candidates"`
		TopN       int   `json:"top_n"`
		Explain    bool  `json:"explain"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return RerankRequest{}, fmt.Errorf("rerank must be an object {enabled, candidates, top_n, explain}: %w", err)
	}
	return RerankRequest{Rerank: &RerankOptions{Enabled: wire.Enabled, Candidates: wire.Candidates, TopN: wire.TopN,
		Explain: wire.Explain}}, nil
}
