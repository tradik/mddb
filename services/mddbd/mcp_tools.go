package main

import (
	"context"
	"fmt"
	"strings"
)

// MCPToolServer provides tool and resource call dispatch.
type MCPToolServer struct {
	client      MCPClient
	customTools []MCPCustomToolConfig
	globalMode  AccessMode // server-wide mode (from MDDB_MODE / follower)
	mode        AccessMode // per-protocol override (from MDDB_MCP_MODE, "" = inherit global)
}

// isToolReadOnly returns true if a tool (builtin or custom) is safe for read-only mode.
func (s *MCPToolServer) isToolReadOnly(name string) bool {
	// Check builtin tool annotations first
	if ann, ok := mcpToolAnnotations[name]; ok {
		return ann.ReadOnlyHint != nil && *ann.ReadOnlyHint
	}
	// Check custom tools — their underlying actions are all read-only
	// (semantic_search, search_documents, full_text_search, fts_languages)
	for _, ct := range s.customTools {
		if ct.Name == name {
			return mcpCustomToolActionReadOnly[ct.Action]
		}
	}
	return false
}

// mcpCustomToolActionReadOnly maps custom tool action names to their read-only status.
var mcpCustomToolActionReadOnly = map[string]bool{
	"semantic_search":  true,
	"search_documents": true,
	"full_text_search": true,
	"fts_languages":    true,
}

// mcpCallTool invokes an MCP tool by name.
func (s *MCPToolServer) mcpCallTool(ctx context.Context, name string, args map[string]interface{}) (string, error) {
	// Enforce read-only mode: per-protocol override takes precedence, then global mode.
	em := effectiveMode(s.globalMode, s.mode)
	if em == ModeRead {
		if !s.isToolReadOnly(name) {
			return "", fmt.Errorf("tool %q is not available in read-only mode", name)
		}
	}

	switch name {
	case "add_document":
		return s.toolAddDocument(ctx, args)
	case "search_documents":
		return s.toolSearchDocuments(ctx, args)
	case "delete_document":
		return s.toolDeleteDocument(ctx, args)
	case "get_stats":
		return s.toolGetStats(ctx, args)
	case "add_documents_batch":
		return s.toolAddBatch(ctx, args)
	case "delete_documents_batch":
		return s.toolDeleteBatch(ctx, args)
	case "export_documents":
		return s.toolExport(ctx, args)
	case "create_backup":
		return s.toolBackup(ctx, args)
	case "restore_backup":
		return s.toolRestore(ctx, args)
	case "semantic_search":
		return s.toolSemanticSearch(ctx, args)
	case "vector_reindex":
		return s.toolVectorReindex(ctx, args)
	case "vector_stats":
		return s.toolVectorStats(ctx, args)
	case "import_url":
		return s.toolImportURL(ctx, args)
	case "set_ttl":
		return s.toolSetTTL(ctx, args)
	case "bulk_ingest_submit":
		return s.toolBulkIngestSubmit(ctx, args)
	case "bulk_ingest_status":
		return s.toolBulkIngestStatus(ctx, args)
	case "bulk_ingest_list":
		return s.toolBulkIngestList(ctx, args)
	case "bulk_ingest_cancel":
		return s.toolBulkIngestCancel(ctx, args)
	case "full_text_search":
		return s.toolFTSSearch(ctx, args)
	case "fts_reindex":
		return s.toolFTSReindex(ctx, args)
	case "fts_languages":
		return s.toolFTSLanguages(ctx, args)
	case "autocomplete":
		return s.toolAutocomplete(ctx, args)
	case "hybrid_search":
		return s.toolHybridSearch(ctx, args)
	case "geo_search":
		return s.toolGeoSearch(ctx, args)
	case "geo_within":
		return s.toolGeoWithin(ctx, args)
	case "geo_polygon":
		return s.toolGeoPolygon(ctx, args)
	case "geo_stats":
		return s.toolGeoStats(ctx, args)
	case "geo_encode":
		return s.toolGeoEncode(ctx, args)
	case "geo_decode":
		return s.toolGeoDecode(ctx, args)
	case "register_webhook":
		return s.toolRegisterWebhook(ctx, args)
	case "list_webhooks":
		return s.toolListWebhooks(ctx, args)
	case "delete_webhook":
		return s.toolDeleteWebhook(ctx, args)
	case "set_schema":
		return s.toolSetSchema(ctx, args)
	case "get_schema":
		return s.toolGetSchema(ctx, args)
	case "delete_schema":
		return s.toolDeleteSchema(ctx, args)
	case "list_schemas":
		return s.toolListSchemas(ctx, args)
	case "validate_document":
		return s.toolValidateDocument(ctx, args)
	case "update_document":
		return s.toolUpdateDocument(ctx, args)
	case "get_document_meta":
		return s.toolGetDocumentMeta(ctx, args)
	case "code_graph":
		return s.toolCodeGraph(ctx, args)
	case "classify_document":
		return s.toolClassifyDocument(ctx, args)
	case "delete_collection":
		return s.toolDeleteCollection(ctx, args)
	case "truncate_revisions":
		return s.toolTruncateRevisions(ctx, args)
	case "list_revisions":
		return s.toolListRevisions(ctx, args)
	case "restore_revision":
		return s.toolRestoreRevision(ctx, args)
	case "list_synonyms":
		return s.toolListSynonyms(ctx, args)
	case "add_synonym":
		return s.toolAddSynonym(ctx, args)
	case "delete_synonym":
		return s.toolDeleteSynonym(ctx, args)
	case "list_stopwords":
		return s.toolListStopWords(ctx, args)
	case "add_stopwords":
		return s.toolAddStopWords(ctx, args)
	case "delete_stopwords":
		return s.toolDeleteStopWords(ctx, args)
	case "get_meta_keys":
		return s.toolGetMetaKeys(ctx, args)
	case "get_checksum":
		return s.toolGetChecksum(ctx, args)
	case "list_automation":
		return s.toolListAutomation(ctx, args)
	case "create_automation":
		return s.toolCreateAutomation(ctx, args)
	case "get_automation":
		return s.toolGetAutomation(ctx, args)
	case "update_automation":
		return s.toolUpdateAutomation(ctx, args)
	case "delete_automation":
		return s.toolDeleteAutomation(ctx, args)
	case "test_automation":
		return s.toolTestAutomation(ctx, args)
	case "get_automation_logs":
		return s.toolGetAutomationLogs(ctx, args)
	case "get_collection_config":
		return s.toolGetCollectionConfig(ctx, args)
	case "set_collection_config":
		return s.toolSetCollectionConfig(ctx, args)
	case "list_collection_configs":
		return s.toolListCollectionConfigs(ctx, args)
	case "list_curation_rules":
		return s.toolListCurationRules(ctx, args)
	case "create_curation_rule":
		return s.toolCreateCurationRule(ctx, args)
	case "update_curation_rule":
		return s.toolUpdateCurationRule(ctx, args)
	case "delete_curation_rule":
		return s.toolDeleteCurationRule(ctx, args)
	case "cross_search":
		return s.toolCrossSearch(ctx, args)
	case "find_duplicates":
		return s.toolFindDuplicates(ctx, args)
	case "aggregate":
		return s.toolAggregate(ctx, args)
	case "ingest_documents":
		return s.toolIngest(ctx, args)
	case "upload_file":
		return s.toolUploadFile(ctx, args)
	// WordPress publishing tools
	case "wordpress_publish":
		return s.toolWordPressPublish(ctx, args)
	case "wordpress_set_status":
		return s.toolWordPressSetStatus(ctx, args)
	// Memory RAG tools
	case "memory_start_session":
		return s.toolMemoryStartSession(ctx, args)
	case "memory_add_message":
		return s.toolMemoryAddMessage(ctx, args)
	case "memory_recall":
		return s.toolMemoryRecall(ctx, args)
	case "memory_summarize":
		return s.toolMemorySummarize(ctx, args)
	case "memory_list_sessions":
		return s.toolMemoryListSessions(ctx, args)
	case "memory_session_history":
		return s.toolMemorySessionHistory(ctx, args)
	default:
		for _, ct := range s.customTools {
			if ct.Name == name {
				return s.mcpCallCustomTool(ctx, ct, args)
			}
		}
		return "", fmt.Errorf("unknown tool: %s", name)
	}
}

// --- arg helpers ---

func mcpGetString(m map[string]interface{}, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

func mcpGetInt(m map[string]interface{}, key string) int {
	switch v := m[key].(type) {
	case float64:
		// JSON has no integers; every number arrives this way.
		return int(v)
	case int:
		// Not from JSON, but from an internal caller or a YAML default,
		// where returning 0 would silently discard the value.
		return v
	}
	return 0
}

func mcpGetFloat(m map[string]interface{}, key string) float64 {
	if v, ok := m[key].(float64); ok {
		return v
	}
	return 0
}

func mcpGetBool(m map[string]interface{}, key string) bool {
	// Delegates to mcpCoerceBool, which already tolerates the string and
	// numeric spellings some LLM clients emit instead of a JSON bool.
	//
	// That helper was written because "silently ignoring a stringified bool
	// would be a footgun" — and this function, used for saveRevision,
	// highlight and lines, had exactly that footgun. An agent sending
	// "lines": "true" got false and no indication why (TEST-002).
	val, _ := mcpCoerceBool(m[key])
	return val
}

// mcpCoerceBool interprets a JSON value as a boolean, tolerating the string
// ("true"/"false"/"yes"/"no"/"1"/"0") and numeric (1/0) forms that some LLM
// clients emit instead of a real JSON bool. Returns the parsed value and
// whether the input was recognized as a boolean at all. Used for token-control
// flags (e.g. include_content) where silently ignoring a stringified bool would
// be a footgun.
func mcpCoerceBool(v interface{}) (val bool, ok bool) {
	switch t := v.(type) {
	case bool:
		return t, true
	case string:
		switch strings.ToLower(strings.TrimSpace(t)) {
		case "true", "1", "yes":
			return true, true
		case "false", "0", "no":
			return false, true
		}
	case float64:
		return t != 0, true
	}
	return false, false
}

// mcpGetStringSlice reads a JSON array of strings into a []string. It also
// accepts a single string (wrapped into a one-element slice) and a native
// []string (as produced by YAML custom-tool defaults). Returns nil when the
// key is absent or holds no strings.
func mcpGetStringSlice(m map[string]interface{}, key string) []string {
	switch v := m[key].(type) {
	case []string:
		return v
	case []interface{}:
		out := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		if len(out) == 0 {
			return nil
		}
		return out
	case string:
		if v == "" {
			return nil
		}
		return []string{v}
	}
	return nil
}

func mcpGetMetaMap(m map[string]interface{}, key string) map[string][]string {
	result := make(map[string][]string)
	if meta, ok := m[key].(map[string]interface{}); ok {
		for k, v := range meta {
			switch val := v.(type) {
			case string:
				result[k] = []string{val}
			case []interface{}:
				// Non-strings are skipped rather than left as empty
				// entries: an empty metadata value is indexed and
				// searchable, so blanking one invents a value the caller
				// never wrote (TEST-002).
				strs := make([]string, 0, len(val))
				for _, item := range val {
					if s, ok := item.(string); ok {
						strs = append(strs, s)
					}
				}
				result[k] = strs
			}
		}
	}
	return result
}

// mcpGetFloat64Map reads a JSON object of numeric values into a float64 map.
// Returns nil when the key is absent so downstream callers can treat empty
// and "not provided" identically.
func mcpGetFloat64Map(m map[string]interface{}, key string) map[string]float64 {
	raw, ok := m[key].(map[string]interface{})
	if !ok {
		return nil
	}
	result := make(map[string]float64, len(raw))
	for k, v := range raw {
		if f, ok := v.(float64); ok {
			result[k] = f
		}
	}
	if len(result) == 0 {
		return nil
	}
	return result
}
