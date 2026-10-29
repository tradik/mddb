package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"sort"
	"strings"
)

// MCP toolsets (MCP-003).
//
// Every tool is a schema in the model's context on every turn, and a long menu
// makes the choice worse as well as more expensive. The only lever was
// MDDB_MCP_BUILTIN_TOOLS=false, which hid everything. Toolsets let a server —
// and each MCP API key on it — expose only the groups a client needs: an agent
// that reads code gets search and code, not geo, webhooks and backups.
//
// A toolset is a name for a group of built-in tools. Each built-in tool is in
// exactly one group (TestEveryBuiltinToolHasAToolset enforces it). Custom
// tools are an operator's explicit choice and are never filtered.
//
// Filtering is applied twice, deliberately: tools/list hides what is out of
// set, and tools/call refuses it with -32601. A list is not an authorisation.

// Toolset names.
const (
	ToolsetSearch     = "search"
	ToolsetCode       = "code"
	ToolsetDocuments  = "documents"
	ToolsetIngest     = "ingest"
	ToolsetMemory     = "memory"
	ToolsetSchema     = "schema"
	ToolsetAdmin      = "admin"
	ToolsetGeo        = "geo"
	ToolsetAutomation = "automation"
	ToolsetWebhooks   = "webhooks"
	ToolsetWordPress  = "wordpress"
	toolsetAll        = "all"
)

// mcpToolsets lists the tools of each group.
var mcpToolsets = map[string][]string{
	ToolsetSearch: {"search_documents", "aggregate", "semantic_search", "full_text_search", "hybrid_search",
		"cross_search", "autocomplete", "fts_languages", "search_advisor", "get_indexing_status", "run_eval", "add_eval_query",
		"find_duplicates", "classify_document", "get_meta_keys"},
	ToolsetCode: {"code_graph", "code_symbol", "reference_search", "reference_symbol", "reference_examples", "reference_dependencies"},
	ToolsetDocuments: {"add_document", "update_document", "delete_document", "get_document_meta", "set_ttl",
		"add_documents_batch", "delete_documents_batch", "export_documents", "list_revisions", "restore_revision"},
	ToolsetIngest: {"import_url", "upload_file", "ingest_documents", "bulk_ingest_submit", "bulk_ingest_status",
		"bulk_ingest_list", "bulk_ingest_cancel"},
	ToolsetMemory: {"memory_start_session", "memory_add_message", "memory_recall", "memory_summarize",
		"memory_list_sessions", "memory_session_history"},
	ToolsetSchema: {"set_schema", "get_schema", "delete_schema", "list_schemas", "validate_document"},
	ToolsetAdmin: {"get_stats", "vector_stats", "vector_reindex", "fts_reindex", "create_backup", "restore_backup",
		"delete_collection", "truncate_revisions", "get_checksum", "list_synonyms", "add_synonym", "delete_synonym",
		"list_stopwords", "add_stopwords", "delete_stopwords", "get_collection_config", "set_collection_config",
		"list_collection_configs", "list_curation_rules", "create_curation_rule", "update_curation_rule",
		"delete_curation_rule"},
	ToolsetGeo:        {"geo_search", "geo_within", "geo_polygon", "geo_stats", "geo_encode", "geo_decode"},
	ToolsetAutomation: {"list_automation", "create_automation", "get_automation", "update_automation", "delete_automation", "test_automation", "get_automation_logs"},
	ToolsetWebhooks:   {"register_webhook", "list_webhooks", "delete_webhook"},
	ToolsetWordPress:  {"wordpress_publish", "wordpress_set_status"},
}

// mcpToolGroup maps a built-in tool to its toolset.
var mcpToolGroup = func() map[string]string {
	out := map[string]string{}
	for set, tools := range mcpToolsets {
		for _, t := range tools {
			out[t] = set
		}
	}
	return out
}()

// toolsetNames returns the known toolset names, sorted.
func toolsetNames() []string {
	names := make([]string, 0, len(mcpToolsets))
	for n := range mcpToolsets {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// parseToolsets turns a list of names into a set. "all" (or nothing) is nil,
// meaning no restriction. An unknown name is an error, so a typo in a key or
// an environment variable fails loudly instead of hiding every tool.
func parseToolsets(names []string) (map[string]bool, error) {
	set := map[string]bool{}
	for _, n := range names {
		n = strings.ToLower(strings.TrimSpace(n))
		switch {
		case n == "":
			continue
		case n == toolsetAll:
			return nil, nil
		case mcpToolsets[n] == nil:
			return nil, fmt.Errorf("unknown MCP toolset %q (known: %s, all)", n, strings.Join(toolsetNames(), ", "))
		}
		set[n] = true
	}
	if len(set) == 0 {
		return nil, nil
	}
	return set, nil
}

// serverToolsets reads MDDB_MCP_TOOLSETS. MDDB_MCP_BUILTIN_TOOLS=false is the
// older switch and means no group at all. An invalid value is reported and
// treated as "all", which is how the server behaved before toolsets existed.
func serverToolsets() map[string]bool {
	if os.Getenv("MDDB_MCP_BUILTIN_TOOLS") == "false" {
		return map[string]bool{}
	}
	set, err := parseToolsets(strings.Split(os.Getenv("MDDB_MCP_TOOLSETS"), ","))
	if err != nil {
		slog.Warn("MDDB_MCP_TOOLSETS is invalid; exposing all toolsets", "err", err)
		return nil
	}
	return set
}

type mcpToolsetsContextKey struct{}

// withKeyToolsets records the toolsets of the MCP API key that authenticated
// the request. Set by the key middleware only; never read from a header.
func withKeyToolsets(ctx context.Context, sets map[string]bool) context.Context {
	if sets == nil {
		return ctx
	}
	return context.WithValue(ctx, mcpToolsetsContextKey{}, sets)
}

// builtinToolAllowed reports whether a built-in tool is in the server's
// toolsets and, when the request came with a restricted key, in the key's.
func builtinToolAllowed(ctx context.Context, name string) bool {
	group := mcpToolGroup[name]
	if server := serverToolsets(); server != nil && !server[group] {
		return false
	}
	if key, ok := ctx.Value(mcpToolsetsContextKey{}).(map[string]bool); ok && !key[group] {
		return false
	}
	return true
}

// errToolNotInToolset is the -32601 a tools/call outside the caller's
// toolsets gets: to the caller, the tool does not exist.
func errToolNotInToolset(name string) map[string]interface{} {
	return mcpError(mcpErrMethodNotFound, "Tool not available: "+name+" is not in this server's or this key's MCP toolsets")
}
