package main

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

// ---- MCP Prompts (2025-11-25) ----

// MCPPrompt represents a server-provided prompt template.
type MCPPrompt struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Arguments   []MCPPromptArg `json:"arguments,omitempty"`
}

// MCPPromptArg describes a prompt argument.
type MCPPromptArg struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Required    bool   `json:"required,omitempty"`
}

// MCPPromptMessage represents a message in a prompt result.
type MCPPromptMessage struct {
	Role    string                 `json:"role"`
	Content map[string]interface{} `json:"content"`
}

// mcpBuiltinPrompts returns all built-in prompt definitions.
func mcpBuiltinPrompts() []MCPPrompt {
	return []MCPPrompt{
		{
			Name:        "analyze-collection",
			Description: "Analyze a collection: document count, metadata keys, content patterns, and suggestions for improvement.",
			Arguments: []MCPPromptArg{
				{Name: "collection", Description: "Collection name to analyze", Required: true},
			},
		},
		{
			Name:        "search-help",
			Description: "Get guidance on which MDDB search method to use (metadata filter, full-text, semantic, hybrid) based on your use case.",
			Arguments: []MCPPromptArg{
				{Name: "use_case", Description: "Describe what you're trying to find", Required: true},
			},
		},
		{
			Name:        "summarize-collection",
			Description: "Summarize all documents in a collection, generating an overview of topics, key themes, and document distribution.",
			Arguments: []MCPPromptArg{
				{Name: "collection", Description: "Collection name to summarize", Required: true},
				{Name: "limit", Description: "Max documents to sample (default: 20)", Required: false},
			},
		},
		{
			Name:        "import-guide",
			Description: "Step-by-step instructions for importing content into MDDB from various sources (WordPress, URLs, files, APIs).",
			Arguments: []MCPPromptArg{
				{Name: "source", Description: "Content source: wordpress, url, file, api, scraping", Required: true},
			},
		},
		{
			Name:        "rag-pipeline",
			Description: "Design a RAG (Retrieval-Augmented Generation) pipeline using MDDB as the knowledge base, with embedding strategy and search configuration.",
			Arguments: []MCPPromptArg{
				{Name: "collection", Description: "Collection to use as knowledge base", Required: true},
				{Name: "model", Description: "Target LLM: claude, gpt4, ollama, deepseek", Required: false},
			},
		},
	}
}

// mcpGetPrompt generates the prompt messages for a named prompt.
func mcpGetPrompt(ctx context.Context, client MCPClient, name string, args map[string]interface{}) ([]MCPPromptMessage, string, error) {
	switch name {
	case "analyze-collection":
		collection, _ := args["collection"].(string)
		if collection == "" {
			return nil, "", fmt.Errorf("collection argument required")
		}
		return []MCPPromptMessage{
			{Role: "user", Content: map[string]interface{}{
				"type": "text",
				"text": fmt.Sprintf("Analyze the MDDB collection %q. Use the following tools in sequence:\n"+
					"1. search_documents with collection=%q to get document count and samples\n"+
					"2. get_meta_keys with collection=%q to see available metadata\n"+
					"3. get_collection_config with collection=%q to check configuration\n"+
					"4. vector_stats to check embedding coverage\n\n"+
					"Provide: document count, metadata key summary, content pattern analysis, and improvement suggestions.",
					collection, collection, collection, collection),
			}},
		}, "Analyze collection: " + collection, nil

	case "search-help":
		useCase, _ := args["use_case"].(string)
		if useCase == "" {
			return nil, "", fmt.Errorf("use_case argument required")
		}
		return []MCPPromptMessage{
			{Role: "user", Content: map[string]interface{}{
				"type": "text",
				"text": fmt.Sprintf("I need help choosing the right MDDB search method for this use case: %q\n\n"+
					"Available search methods:\n"+
					"- search_documents: Filter by metadata (tags, dates, categories). Best for exact matches.\n"+
					"- full_text_search: Keyword matching with TF-IDF/BM25 scoring. Best for text queries.\n"+
					"- semantic_search: Vector similarity using embeddings. Best for meaning-based search.\n"+
					"- hybrid_search: Combined FTS + vector with alpha blending or RRF. Best of both worlds.\n"+
					"- cross_search: Search across multiple collections. Best for cross-referencing.\n\n"+
					"Recommend the best approach and provide an example tool call.", useCase),
			}},
		}, "Search help for: " + useCase, nil

	case "summarize-collection":
		collection, _ := args["collection"].(string)
		if collection == "" {
			return nil, "", fmt.Errorf("collection argument required")
		}
		return []MCPPromptMessage{
			{Role: "user", Content: map[string]interface{}{
				"type": "text",
				"text": fmt.Sprintf("Summarize the contents of MDDB collection %q.\n"+
					"Use search_documents to sample documents (limit 20) and get_meta_keys for metadata overview.\n"+
					"Provide:\n- Total document count\n- Key topics and themes\n- Document distribution by metadata\n- Notable patterns",
					collection),
			}},
		}, "Summarize collection: " + collection, nil

	case "import-guide":
		source, _ := args["source"].(string)
		if source == "" {
			return nil, "", fmt.Errorf("source argument required")
		}
		return []MCPPromptMessage{
			{Role: "user", Content: map[string]interface{}{
				"type": "text",
				"text": fmt.Sprintf("Give me step-by-step instructions for importing content from %q into MDDB.\n"+
					"Include: tool calls, required parameters, best practices for metadata, and how to verify the import.\n"+
					"Available tools: import_url, upload_file, add_document, add_documents_batch, ingest_documents.",
					source),
			}},
		}, "Import guide: " + source, nil

	case "rag-pipeline":
		collection, _ := args["collection"].(string)
		if collection == "" {
			return nil, "", fmt.Errorf("collection argument required")
		}
		model, _ := args["model"].(string)
		if model == "" {
			model = "claude"
		}

		text := fmt.Sprintf("Design a RAG pipeline using MDDB collection %q as the knowledge base, targeting %s.\n"+
			"Check: vector_stats (embedding coverage), get_collection_config (settings).\n"+
			"Recommend: embedding model, search strategy (semantic vs hybrid), chunk strategy, and example queries.",
			collection, model)

		// RAG-002: a collection that states how its answers should be
		// formatted has already answered part of this question, and designing
		// a pipeline against it without knowing that produces advice the
		// operator has to undo.
		if prompt := collectionResponsePrompt(ctx, client, collection); prompt != "" {
			text += fmt.Sprintf("\n\nThis collection states how answers drawn from it should be formatted. "+
				"Treat it as a requirement of the pipeline, not a suggestion:\n\n%s", prompt)
		}

		return []MCPPromptMessage{
			{Role: "user", Content: map[string]interface{}{"type": "text", "text": text}},
		}, "RAG pipeline for: " + collection, nil

	default:
		return nil, "", fmt.Errorf("unknown prompt: %s", name)
	}
}

// ---- MCP Completion / Autocomplete (2025-11-25) ----

// mcpComplete handles completion/complete requests.
func mcpComplete(ctx context.Context, client MCPClient, ref map[string]interface{}, argument map[string]interface{}) ([]string, int, bool) {
	argName, _ := argument["name"].(string)
	argValue, _ := argument["value"].(string)

	refType, _ := ref["type"].(string)

	switch {
	case refType == "ref/prompt":
		return mcpCompletePromptArg(argName, argValue)
	case argName == "collection":
		return mcpCompleteCollection(ctx, client, argValue)
	default:
		return nil, 0, false
	}
}

func mcpCompletePromptArg(argName, argValue string) ([]string, int, bool) {
	switch argName {
	case "source":
		options := []string{"wordpress", "url", "file", "api", "scraping"}
		return filterPrefix(options, argValue), len(options), false
	case "model":
		options := []string{"claude", "gpt4", "ollama", "deepseek"}
		return filterPrefix(options, argValue), len(options), false
	case "algorithm":
		options := []string{"tfidf", "bm25", "bm25f", "pmisparse"}
		return filterPrefix(options, argValue), len(options), false
	default:
		return nil, 0, false
	}
}

func mcpCompleteCollection(ctx context.Context, client MCPClient, prefix string) ([]string, int, bool) {
	stats, err := client.Stats(ctx)
	if err != nil {
		return nil, 0, false
	}

	var names []string
	for _, c := range stats.Collections {
		names = append(names, c.Name)
	}
	sort.Strings(names)

	filtered := filterPrefix(names, prefix)
	if len(filtered) > 100 {
		return filtered[:100], len(names), true
	}
	return filtered, len(names), len(filtered) < len(names)
}

func filterPrefix(items []string, prefix string) []string {
	if prefix == "" {
		return items
	}
	var result []string
	lp := strings.ToLower(prefix)
	for _, item := range items {
		if strings.HasPrefix(strings.ToLower(item), lp) {
			result = append(result, item)
		}
	}
	return result
}

// ---- MCP Logging (2025-11-25) ----

// MCPLogLevel represents MCP log severity per RFC 5424.
type MCPLogLevel string

const (
	MCPLogDebug     MCPLogLevel = "debug"
	MCPLogInfo      MCPLogLevel = "info"
	MCPLogNotice    MCPLogLevel = "notice"
	MCPLogWarning   MCPLogLevel = "warning"
	MCPLogError     MCPLogLevel = "error"
	MCPLogCritical  MCPLogLevel = "critical"
	MCPLogAlert     MCPLogLevel = "alert"
	MCPLogEmergency MCPLogLevel = "emergency"
)

var mcpLogLevelOrder = map[MCPLogLevel]int{
	MCPLogDebug:     0,
	MCPLogInfo:      1,
	MCPLogNotice:    2,
	MCPLogWarning:   3,
	MCPLogError:     4,
	MCPLogCritical:  5,
	MCPLogAlert:     6,
	MCPLogEmergency: 7,
}

// MCPLogMessage creates a log notification for sending to clients.
func MCPLogMessage(level MCPLogLevel, logger, message string) map[string]interface{} {
	return map[string]interface{}{
		"jsonrpc": "2.0",
		"method":  "notifications/message",
		"params": map[string]interface{}{
			"level":  string(level),
			"logger": logger,
			"data":   message,
		},
	}
}

// mcpShouldLog returns true if the given level meets the minimum threshold.
func mcpShouldLog(level, minLevel MCPLogLevel) bool {
	return mcpLogLevelOrder[level] >= mcpLogLevelOrder[minLevel]
}

// collectionResponsePrompt reads a collection's formatting instruction
// (RAG-002), returning "" when there is none or the lookup fails.
//
// A prompt is an enhancement: failing to fetch it must degrade the answer, not
// the request.
func collectionResponsePrompt(ctx context.Context, client MCPClient, collection string) string {
	if client == nil || collection == "" {
		return ""
	}
	resp, err := client.GetCollectionConfig(ctx, collection)
	if err != nil || resp == nil || resp.Config == nil {
		return ""
	}
	return ExpandTemplate(resp.Config.ResponsePrompt, map[string]string{
		"collection": collection,
	})
}
