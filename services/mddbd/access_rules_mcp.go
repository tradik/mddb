package main

import (
	"context"
	"net/url"
	"strings"
)

// mcpToolAccessRules classifies every built-in MCP tool (SEC-019). The
// collection is read from the arguments — collection, source_collection,
// collections, target_collections — after alias resolution. A custom tool is
// gated as the built-in action it wraps.
//
// TestEveryMCPToolIsClassified walks mcpBuiltinTools and fails on a tool
// missing here.
var mcpToolAccessRules = map[string]accessRule{
	// Documents.
	"add_document":           ruleWrite,
	"add_documents_batch":    ruleWrite,
	"update_document":        ruleWrite,
	"delete_document":        ruleWrite,
	"delete_documents_batch": ruleWrite,
	"delete_collection":      ruleWrite,
	"truncate_revisions":     ruleWrite,
	"restore_revision":       ruleWrite,
	"import_url":             ruleWrite,
	"set_ttl":                ruleWrite,
	"ingest_documents":       ruleWrite,
	"upload_file":            ruleWrite,
	"bulk_ingest_submit":     ruleWrite,
	"delete_where":           ruleWrite, // a dry run too: the tool has no read-only path
	"update_where":           ruleWrite,
	"set_ttl_where":          ruleWrite,
	"get_document_meta":      ruleRead,
	"get_meta_keys":          ruleRead,
	"get_checksum":           ruleRead,
	"list_revisions":         ruleRead,
	"export_documents":       ruleRead,
	"validate_document":      ruleRead,
	"wordpress_publish":      ruleRead, // publishes a document of the collection

	// Search.
	"search_documents":    ruleRead,
	"semantic_search":     ruleRead,
	"full_text_search":    ruleRead,
	"hybrid_search":       ruleRead,
	"cross_search":        ruleRead,
	"autocomplete":        ruleRead,
	"aggregate":           ruleRead,
	"classify_document":   ruleRead,
	"find_duplicates":     ruleRead,
	"code_graph":          ruleRead,
	"code_symbol":         ruleRead,
	"search_advisor":      ruleRead,
	"get_indexing_status": ruleRead,
	"geo_search":          ruleRead,
	"geo_within":          ruleRead,
	"geo_polygon":         ruleRead,
	"run_eval":            ruleRead,
	"add_eval_query":      ruleWrite,
	"bulk_ingest_list":    ruleRead, // none named: every job

	// Reference collections, every one of them unless some are named.
	"reference_search":       referenceRule,
	"reference_symbol":       referenceRule,
	"reference_examples":     referenceRule,
	"reference_dependencies": referenceRule,

	// Collection settings.
	"vector_reindex":        ruleWrite,
	"fts_reindex":           ruleWrite,
	"set_schema":            ruleWrite,
	"delete_schema":         ruleWrite,
	"get_schema":            ruleRead,
	"list_synonyms":         ruleRead,
	"add_synonym":           ruleWrite,
	"delete_synonym":        ruleWrite,
	"list_stopwords":        ruleRead,
	"add_stopwords":         ruleWrite,
	"delete_stopwords":      ruleWrite,
	"get_collection_config": ruleRead,
	"set_collection_config": ruleWrite,
	"register_webhook":      ruleWrite, // its collection, "*" when none
	"list_curation_rules":   ruleRead,  // none named: every rule
	"create_curation_rule":  ruleWrite,

	// Every collection at once (none of these filters by tenant), or a job,
	// hook or rule looked up by id.
	"get_stats":               ruleAllRead,
	"vector_stats":            ruleAllRead,
	"geo_stats":               ruleAllRead,
	"list_schemas":            ruleAllRead,
	"list_collection_configs": ruleAllRead,
	"get_gain":                ruleAllRead,
	"corpus_list":             ruleAllListing, // as GET /v1/corpus: filtered to the tenant
	"bulk_ingest_status":      ruleAllRead,
	"bulk_ingest_cancel":      ruleAllWrite,
	"delete_webhook":          ruleAllWrite,
	"update_curation_rule":    ruleAllWrite,
	"delete_curation_rule":    ruleAllWrite,

	// The memory store.
	"memory_start_session":   memoryRule(PermWrite),
	"memory_add_message":     memoryRule(PermWrite),
	"memory_summarize":       memoryRule(PermWrite),
	"memory_recall":          memoryRule(PermRead),
	"memory_list_sessions":   memoryRule(PermRead),
	"memory_session_history": memoryRule(PermRead),

	// No collection data.
	"fts_languages":        ruleAuthenticated,
	"geo_encode":           ruleAuthenticated,
	"geo_decode":           ruleAuthenticated,
	"wordpress_set_status": ruleAuthenticated, // a post on the WordPress site, not in MDDB

	// Global administration.
	"create_backup":       ruleAdmin,
	"restore_backup":      ruleAdmin,
	"corpus_install":      ruleAdmin, // as POST /v1/corpus/install
	"corpus_remove":       ruleAdmin, // as POST /v1/corpus/remove
	"list_webhooks":       ruleAdmin,
	"list_automation":     ruleAdmin,
	"create_automation":   ruleAdmin,
	"get_automation":      ruleAdmin,
	"update_automation":   ruleAdmin,
	"delete_automation":   ruleAdmin,
	"test_automation":     ruleAdmin,
	"get_automation_logs": ruleAdmin,
	"run_automation":      ruleAdmin,
}

// referenceRule reads the named collections, or every reference collection.
var referenceRule = accessRule{class: accessCollection, op: PermRead, fallback: []string{rolePrefix + CodeRoleReference}}

// authorizeTool applies a tool's rule to its arguments. A custom tool passes
// here and is gated as the action it calls. A call without the main auth's
// claims came in on an MCP API key or over stdio, whose scope is the key's
// toolsets (MCP-003), not collections: it is left as it was.
func (s *Server) authorizeTool(ctx context.Context, name string, args map[string]any) error {
	rule, builtin := mcpToolAccessRules[name]
	if !builtin {
		if _, known := mcpToolGroup[name]; !known {
			return nil // a custom tool, or an unknown one the dispatcher refuses
		}
	}
	return s.authorizeMCP(ctx, rule, collectionsFromArgs(args))
}

// authorizeResource applies the rule of a resource URI: a document or a
// search reads its collection, the statistics read every collection.
func (s *Server) authorizeResource(ctx context.Context, u *url.URL) error {
	path := mcpResourcePath(u)
	switch {
	case u.Scheme == "mddb" && path == "health":
		return s.authorizeMCP(ctx, ruleAuthenticated, nil)
	case u.Scheme == "mddb" && path == "stats":
		return s.authorizeMCP(ctx, ruleAllRead, nil)
	case u.Scheme == "mddb" || u.Scheme == "mddb-search":
		return s.authorizeMCP(ctx, ruleRead, []string{strings.SplitN(path, "/", 2)[0]})
	}
	return nil // an unsupported scheme, refused by the reader
}

// authorizeMCP applies a rule to an MCP call that carries the main auth's
// claims, and lets one without them through (see authorizeTool).
func (s *Server) authorizeMCP(ctx context.Context, rule accessRule, names []string) error {
	if _, ok := GetClaimsFromContext(ctx); !ok {
		return nil
	}
	return s.authorize(ctx, rule, names)
}
