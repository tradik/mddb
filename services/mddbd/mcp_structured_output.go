package main

// mcpToolOutputSchemas maps tool name → JSON Schema for structured output.
// Only tools with well-defined output shapes are included.
var mcpToolOutputSchemas = map[string]map[string]interface{}{
	"get_stats": {
		"type": "object",
		"properties": map[string]interface{}{
			"database_path":      map[string]interface{}{"type": "string"},
			"database_size":      map[string]interface{}{"type": "integer"},
			"mode":               map[string]interface{}{"type": "string"},
			"total_documents":    map[string]interface{}{"type": "integer"},
			"total_revisions":    map[string]interface{}{"type": "integer"},
			"total_meta_indices": map[string]interface{}{"type": "integer"},
			"collections": map[string]interface{}{
				"type": "array",
				"items": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"name":             map[string]interface{}{"type": "string"},
						"document_count":   map[string]interface{}{"type": "integer"},
						"revision_count":   map[string]interface{}{"type": "integer"},
						"meta_index_count": map[string]interface{}{"type": "integer"},
					},
				},
			},
		},
	},
	"search_documents": {
		"type": "object",
		"properties": map[string]interface{}{
			"total": map[string]interface{}{"type": "integer"},
			"documents": map[string]interface{}{
				"type": "array",
				"items": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"id":         map[string]interface{}{"type": "string"},
						"key":        map[string]interface{}{"type": "string"},
						"lang":       map[string]interface{}{"type": "string"},
						"meta":       map[string]interface{}{"type": "object"},
						"content_md": map[string]interface{}{"type": "string"},
						"added_at":   map[string]interface{}{"type": "string"},
						"updated_at": map[string]interface{}{"type": "string"},
						"revision":   map[string]interface{}{"type": "string"},
					},
				},
			},
		},
	},
	"semantic_search": {
		"type": "object",
		"properties": map[string]interface{}{
			"total":          map[string]interface{}{"type": "integer"},
			"model":          map[string]interface{}{"type": "string"},
			"dimensions":     map[string]interface{}{"type": "integer"},
			"algorithm":      map[string]interface{}{"type": "string"},
			"distanceMetric": map[string]interface{}{"type": "string"},
			"results": map[string]interface{}{
				"type": "array",
				"items": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"score":    map[string]interface{}{"type": "number"},
						"rank":     map[string]interface{}{"type": "integer"},
						"document": map[string]interface{}{"type": "object"},
					},
				},
			},
		},
	},
	"full_text_search": {
		"type": "object",
		"properties": map[string]interface{}{
			"total":     map[string]interface{}{"type": "integer"},
			"algorithm": map[string]interface{}{"type": "string"},
			"fuzzy":     map[string]interface{}{"type": "integer"},
			"results": map[string]interface{}{
				"type": "array",
				"items": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"score":        map[string]interface{}{"type": "number"},
						"matchedTerms": map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}},
						"document":     map[string]interface{}{"type": "object"},
					},
				},
			},
		},
	},
	"hybrid_search": {
		"type": "object",
		"properties": map[string]interface{}{
			"total":           map[string]interface{}{"type": "integer"},
			"strategy":        map[string]interface{}{"type": "string"},
			"ftsAlgorithm":    map[string]interface{}{"type": "string"},
			"vectorAlgorithm": map[string]interface{}{"type": "string"},
			"results": map[string]interface{}{
				"type": "array",
				"items": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"combinedScore": map[string]interface{}{"type": "number"},
						"ftsScore":      map[string]interface{}{"type": "number"},
						"vectorScore":   map[string]interface{}{"type": "number"},
						"rank":          map[string]interface{}{"type": "integer"},
						"document":      map[string]interface{}{"type": "object"},
					},
				},
			},
		},
	},
	"vector_stats": {
		"type": "object",
		"properties": map[string]interface{}{
			"provider":   map[string]interface{}{"type": "string"},
			"model":      map[string]interface{}{"type": "string"},
			"dimensions": map[string]interface{}{"type": "integer"},
			"enabled":    map[string]interface{}{"type": "boolean"},
			"collections": map[string]interface{}{
				"type": "object",
				"additionalProperties": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"total_documents":    map[string]interface{}{"type": "integer"},
						"embedded_documents": map[string]interface{}{"type": "integer"},
					},
				},
			},
		},
	},
	"get_checksum": {
		"type": "object",
		"properties": map[string]interface{}{
			"collection":    map[string]interface{}{"type": "string"},
			"checksum":      map[string]interface{}{"type": "string"},
			"documentCount": map[string]interface{}{"type": "integer"},
		},
	},
	"classify_document": {
		"type": "object",
		"properties": map[string]interface{}{
			"model":      map[string]interface{}{"type": "string"},
			"dimensions": map[string]interface{}{"type": "integer"},
			"results": map[string]interface{}{
				"type": "array",
				"items": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"label": map[string]interface{}{"type": "string"},
						"score": map[string]interface{}{"type": "number"},
					},
				},
			},
		},
	},
	"aggregate": {
		"type": "object",
		"properties": map[string]interface{}{
			"collection": map[string]interface{}{"type": "string"},
			"facets":     map[string]interface{}{"type": "object"},
			"histograms": map[string]interface{}{"type": "object"},
			"total":      map[string]interface{}{"type": "integer"},
		},
	},
}

// applyOutputSchemas adds outputSchema to tools that have structured output definitions.
func applyOutputSchemas(tools []MCPTool) []MCPTool {
	for i := range tools {
		if schema, ok := mcpToolOutputSchemas[tools[i].Name]; ok {
			if tools[i].InputSchema == nil {
				tools[i].InputSchema = map[string]interface{}{"type": "object"}
			}
			// Store output schema in a way compatible with the tool definition.
			// The MCP spec 2025-11-25 adds outputSchema as a top-level field on Tool.
			tools[i].OutputSchema = schema
		}
	}
	return tools
}
