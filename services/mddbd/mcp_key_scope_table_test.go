package main

import (
	"sort"
	"strings"
	"testing"
)

// The scope table pinned (AGT-007, AGT-008): what every built-in tool lets
// each kind of key do, written out, so an edit that opens a tool to scoped
// keys fails here instead of shipping silently. A new tool fails too until
// it is given a row.
//
// Each verdict is ten characters, 1 = allowed, in this order:
//
//	1 read-only key on a, call naming a
//	2 read-write key on a, naming a
//	3 read-write key on a, naming b
//	4 read-only key on every collection, naming b
//	5 key bound to a scenario over a, naming a
//	6 tools/list for key 1
//	7 tools/list for key 2
//	8 key on a and b with b read-only, naming b
//	9 the same key, naming a
//	10 tools/list for key 8
//
// Every limited key carries a memory namespace, as a stored one does.
var pinnedKeyScopeTable = map[string][]string{
	// No collection data, read-only.
	"1111111111": {"chat_scenario_list", "feature_list", "fts_languages", "geo_decode", "geo_encode"},
	// No collection named, a write: a post on the WordPress site, and the
	// memory store's writes inside the key's namespace (AGT-008).
	"0110001111": {"wordpress_set_status", "memory_add_message", "memory_start_session", "memory_summarize"},
	// Reads of the collections named.
	"1101111111": {"aggregate", "autocomplete", "bulk_ingest_list", "classify_document", "code_graph", "code_symbol",
		"cross_search", "export_documents", "find_duplicates", "full_text_search", "geo_polygon", "geo_search",
		"geo_within", "get_checksum", "get_collection_config", "get_document_meta", "get_indexing_status",
		"get_meta_keys", "get_schema", "hybrid_search", "list_curation_rules", "list_revisions", "list_stopwords",
		"list_synonyms", "reference_dependencies", "reference_examples", "reference_search", "reference_symbol",
		"run_eval", "search_advisor", "search_documents", "semantic_search", "validate_document"},
	// Writes of the collections named.
	"0100001011": {"add_document", "add_documents_batch", "add_eval_query", "add_stopwords", "add_synonym",
		"bulk_ingest_submit", "create_curation_rule", "delete_collection", "delete_document", "delete_documents_batch",
		"delete_schema", "delete_stopwords", "delete_synonym", "delete_where", "fts_reindex", "import_url",
		"ingest_documents", "register_webhook", "restore_revision", "set_collection_config", "set_schema", "set_ttl",
		"set_ttl_where", "truncate_revisions", "update_document", "update_where", "upload_file", "vector_reindex",
		"wordpress_publish"},
	// Every collection at once: only a key not limited by collection reads.
	"0001000000": {"bulk_ingest_status", "corpus_list", "geo_stats", "get_gain", "get_stats",
		"list_collection_configs", "list_schemas", "vector_stats"},
	// The memory store's reads, inside the key's namespace (AGT-008).
	"1110111111": {"memory_list_sessions", "memory_recall", "memory_session_history"},
	// Administration, handlers and writes of every collection: never.
	"0000000000": {"bulk_ingest_cancel", "chat_conversation_get", "chat_conversation_list", "chat_gaps", "chat_log_list",
		"chat_mcp_secret_delete", "chat_mcp_secret_list", "chat_mcp_secret_put", "chat_scenario_delete",
		"chat_scenario_get", "chat_scenario_put", "chat_skill_delete", "chat_skill_get", "chat_skill_list",
		"chat_skill_put", "corpus_install", "corpus_remove", "create_automation", "create_backup", "delete_automation",
		"delete_curation_rule", "delete_webhook", "feature_set", "get_automation", "get_automation_logs",
		"list_automation", "list_webhooks", "restore_backup", "run_automation", "test_automation",
		"update_automation", "update_curation_rule"},
}

func TestKeyScopeTablePinned(t *testing.T) {
	want := map[string]string{}
	for verdict, tools := range pinnedKeyScopeTable {
		for _, tool := range tools {
			want[tool] = verdict
		}
	}

	s := scopeServer(t)
	putScenario(t, s, &ChatScenario{Name: "s", Enabled: true, AllowedCollections: []string{"a"}})
	ns := keyMemoryNamespace("", "pinned")
	keys := []struct {
		k     mcpKeyScope
		names []string
		list  bool
	}{
		{mcpKeyScope{collections: []string{"a"}, readOnly: true, memoryNamespace: ns}, []string{"a"}, false},
		{mcpKeyScope{collections: []string{"a"}, memoryNamespace: ns}, []string{"a"}, false},
		{mcpKeyScope{collections: []string{"a"}, memoryNamespace: ns}, []string{"b"}, false},
		{mcpKeyScope{readOnly: true, memoryNamespace: ns}, []string{"b"}, false},
		{mcpKeyScope{scenario: "s", memoryNamespace: ns}, []string{"a"}, false},
		{mcpKeyScope{collections: []string{"a"}, readOnly: true, memoryNamespace: ns}, nil, true},
		{mcpKeyScope{collections: []string{"a"}, memoryNamespace: ns}, nil, true},
		{mcpKeyScope{collections: []string{"a", "b"}, readOnlyCollections: []string{"b"}, memoryNamespace: ns}, []string{"b"}, false},
		{mcpKeyScope{collections: []string{"a", "b"}, readOnlyCollections: []string{"b"}, memoryNamespace: ns}, []string{"a"}, false},
		{mcpKeyScope{collections: []string{"a", "b"}, readOnlyCollections: []string{"b"}, memoryNamespace: ns}, nil, true},
	}
	var unpinned []string
	for tool, rule := range mcpToolAccessRules {
		var got strings.Builder
		for _, c := range keys {
			ctx := scoped(c.k)
			ok := s.authorizeMCP(ctx, tool, rule, c.names) == nil
			if c.list {
				ok = keyScopeMayList(ctx, tool)
			}
			got.WriteByte("01"[boolIndex(ok)])
		}
		switch w, pinned := want[tool]; {
		case !pinned:
			unpinned = append(unpinned, tool+" "+got.String())
		case got.String() != w:
			t.Errorf("%s: verdict %s, pinned %s", tool, got.String(), w)
		}
		delete(want, tool)
	}
	sort.Strings(unpinned)
	for _, row := range unpinned {
		t.Errorf("not pinned — add it to pinnedKeyScopeTable: %s", row)
	}
	for tool := range want {
		t.Errorf("pinned but no longer a tool: %s", tool)
	}
}

func boolIndex(b bool) int {
	if b {
		return 1
	}
	return 0
}

// TestKeyScopeNeverReachesMemoryCollections: a limited key naming a memory
// collection, in its scope or in a call, cannot read the store around its
// namespace.
func TestKeyScopeNeverReachesMemoryCollections(t *testing.T) {
	s := scopeServer(t)
	k := mcpKeyScope{collections: append([]string{"a"}, memoryCollections...), memoryNamespace: "key:x"}
	for _, c := range memoryCollections {
		if err := s.authorizeMCP(scoped(k), "search_documents", ruleRead, []string{c}); err == nil {
			t.Errorf("a limited key read %s", c)
		}
	}
	if err := s.authorizeMCP(scoped(k), "search_documents", ruleRead, []string{"a"}); err != nil {
		t.Errorf("the rest of the scope: %v", err)
	}
}
