package main

import (
	"net/http"

	json "mddb/internal/jsonx"
)

// ---- Request/Response types ----

// EndpointsResponse contains all available HTTP, gRPC, and MCP endpoints.
type EndpointsResponse struct {
	HTTP []HTTPEndpoint `json:"http"`
	GRPC []GRPCMethod   `json:"grpc"`
	MCP  []MCPTool      `json:"mcp"`
}

// HTTPEndpoint describes a single HTTP endpoint with its method and path.
type HTTPEndpoint struct {
	Method       string `json:"method"`
	Path         string `json:"path"`
	Description  string `json:"description"`
	RequiresAuth bool   `json:"requiresAuth"`
}

// GRPCMethod describes a single gRPC method.
type GRPCMethod struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// MCPToolAnnotations holds MCP spec tool annotation hints (2025-11-25).
type MCPToolAnnotations struct {
	Title           string `json:"title,omitempty"`
	ReadOnlyHint    *bool  `json:"readOnlyHint,omitempty"`
	DestructiveHint *bool  `json:"destructiveHint,omitempty"`
	IdempotentHint  *bool  `json:"idempotentHint,omitempty"`
	OpenWorldHint   *bool  `json:"openWorldHint,omitempty"`
}

// MCPTool describes a single MCP tool with its input and output schemas.
type MCPTool struct {
	Name         string                 `json:"name"`
	Description  string                 `json:"description"`
	InputSchema  map[string]interface{} `json:"inputSchema,omitempty"`
	OutputSchema map[string]interface{} `json:"outputSchema,omitempty"`
	Annotations  *MCPToolAnnotations    `json:"annotations,omitempty"`
}

// ---- Handlers ----

// handleEndpoints returns a list of all available endpoints
func (s *Server) handleEndpoints(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	authEnabled := env("MDDB_AUTH_ENABLED", "false") == "true"

	// HTTP Endpoints
	httpEndpoints := s.httpEndpointCatalogue(authEnabled)

	// Add GraphQL endpoint if enabled
	graphqlEnabled := env("MDDB_GRAPHQL_ENABLED", "true") != "false"
	if graphqlEnabled {
		httpEndpoints = append(httpEndpoints,
			HTTPEndpoint{Method: "POST", Path: "/graphql", Description: "GraphQL API endpoint", RequiresAuth: authEnabled},
			HTTPEndpoint{Method: "GET", Path: "/playground", Description: "GraphQL Playground", RequiresAuth: false},
		)
	}

	// Add auth endpoints if auth is enabled
	if authEnabled {
		authEndpoints := []HTTPEndpoint{
			{Method: "POST", Path: "/v1/auth/login", Description: "Login with username/password", RequiresAuth: false},
			{Method: "POST", Path: "/v1/auth/register", Description: "Register new user", RequiresAuth: true},
			{Method: "POST", Path: "/v1/auth/api-key", Description: "Create/manage API keys", RequiresAuth: true},
			{Method: "GET", Path: "/v1/auth/api-keys", Description: "List API keys", RequiresAuth: true},
			{Method: "DELETE", Path: "/v1/auth/api-keys/{keyHash}", Description: "Delete API key", RequiresAuth: true},
			{Method: "POST", Path: "/v1/auth/me", Description: "Get current user info", RequiresAuth: true},
			{Method: "POST", Path: "/v1/auth/permissions", Description: "Get user permissions", RequiresAuth: true},
			{Method: "GET", Path: "/v1/auth/users", Description: "List all users", RequiresAuth: true},
			{Method: "DELETE", Path: "/v1/auth/users/{username}", Description: "Delete user", RequiresAuth: true},
			{Method: "GET", Path: "/v1/auth/groups", Description: "List all groups", RequiresAuth: true},
			{Method: "POST", Path: "/v1/auth/groups", Description: "Create group", RequiresAuth: true},
			{Method: "GET", Path: "/v1/auth/groups/{name}", Description: "Get group details", RequiresAuth: true},
			{Method: "PUT", Path: "/v1/auth/groups/{name}", Description: "Update group", RequiresAuth: true},
			{Method: "DELETE", Path: "/v1/auth/groups/{name}", Description: "Delete group", RequiresAuth: true},
			{Method: "GET", Path: "/v1/auth/group-permissions", Description: "Get group permissions", RequiresAuth: true},
			{Method: "POST", Path: "/v1/auth/group-permissions", Description: "Set group permission", RequiresAuth: true},
		}
		httpEndpoints = append(httpEndpoints, authEndpoints...)
	}

	// gRPC Methods
	grpcMethods := []GRPCMethod{
		// Document Management
		{Name: "Add", Description: "Add/update document"},
		{Name: "AddBatch", Description: "Batch add documents"},
		{Name: "Ingest", Description: "Bulk ingest with URL key derivation, dedup, and auto-metadata"},
		{Name: "UpdateDocument", Description: "Partial document update (meta/content/ttl)"},
		{Name: "UpdateBatch", Description: "Batch update documents"},
		{Name: "DeleteDocument", Description: "Delete a single document"},
		{Name: "DeleteBatch", Description: "Batch delete documents"},
		{Name: "DeleteCollection", Description: "Delete entire collection"},
		{Name: "Get", Description: "Get document by key"},
		{Name: "GetDocumentMeta", Description: "Get document metadata without content"},
		{Name: "Search", Description: "Search documents with filters"},
		{Name: "ImportURL", Description: "Import markdown from URL"},
		{Name: "SetTTL", Description: "Set document time-to-live"},
		// Full-Text Search
		{Name: "FTS", Description: "Full-text search (tfidf, bm25, bm25f, pmisparse)"},
		// Vector / Semantic
		{Name: "VectorSearch", Description: "Semantic search using embeddings"},
		{Name: "VectorReindex", Description: "Re-embed collection documents"},
		{Name: "VectorStats", Description: "Vector/embedding statistics"},
		// Hybrid & Cross
		{Name: "HybridSearch", Description: "Hybrid sparse+dense search (FTS + vector)"},
		{Name: "CrossSearch", Description: "Cross-collection vector search"},
		// Analysis
		{Name: "Classify", Description: "Zero-shot document classification"},
		{Name: "FindDuplicates", Description: "Find duplicate and similar documents"},
		{Name: "GetChecksum", Description: "Collection CRC32 checksum"},
		{Name: "GetMetaKeys", Description: "List metadata keys and values"},
		// Revisions
		{Name: "ListRevisions", Description: "List document revision history"},
		{Name: "RestoreRevision", Description: "Restore document to a previous revision"},
		{Name: "Truncate", Description: "Truncate revision history"},
		// Export & Backup
		{Name: "Export", Description: "Export collection (streaming)"},
		{Name: "Backup", Description: "Create database backup"},
		{Name: "Restore", Description: "Restore from backup"},
		// FTS Config
		{Name: "ListSynonyms", Description: "List FTS synonyms"},
		{Name: "AddSynonym", Description: "Add/update synonym group"},
		{Name: "DeleteSynonym", Description: "Delete synonym group"},
		{Name: "ListStopwords", Description: "List FTS stop words"},
		{Name: "AddStopwords", Description: "Add custom stop words"},
		{Name: "DeleteStopwords", Description: "Remove custom stop words"},
		// Schemas
		{Name: "SetSchema", Description: "Set JSON schema"},
		{Name: "GetSchema", Description: "Get collection schema"},
		{Name: "DeleteSchema", Description: "Delete schema"},
		{Name: "ListSchemas", Description: "List all schemas"},
		{Name: "ValidateDocument", Description: "Validate document metadata"},
		// Webhooks
		{Name: "RegisterWebhook", Description: "Register webhook"},
		{Name: "ListWebhooks", Description: "List webhooks"},
		{Name: "DeleteWebhook", Description: "Delete webhook"},
		// Automation
		{Name: "ListAutomation", Description: "List automation rules"},
		{Name: "CreateAutomation", Description: "Create automation rule"},
		{Name: "GetAutomation", Description: "Get automation rule by ID"},
		{Name: "UpdateAutomation", Description: "Update automation rule"},
		{Name: "DeleteAutomation", Description: "Delete automation rule"},
		{Name: "TestAutomation", Description: "Test trigger (dry run)"},
		{Name: "GetAutomationLogs", Description: "List automation execution logs"},
		// Collection Config
		{Name: "GetCollectionConfig", Description: "Get collection configuration"},
		{Name: "SetCollectionConfig", Description: "Set collection configuration"},
		{Name: "ListCollectionConfigs", Description: "List all collection configurations"},
		// System
		{Name: "Stats", Description: "Database statistics"},
	}

	// MCP Tools
	mcpTools := []MCPTool{
		{Name: "add_document", Description: "Add/update document"},
		{Name: "search_documents", Description: "Search with filters and sorting"},
		{Name: "delete_document", Description: "Delete document"},
		{Name: "get_stats", Description: "Get server statistics"},
		{Name: "add_documents_batch", Description: "Batch add documents"},
		{Name: "delete_documents_batch", Description: "Batch delete documents"},
		{Name: "export_documents", Description: "Export collection"},
		{Name: "create_backup", Description: "Create database backup"},
		{Name: "restore_backup", Description: "Restore from backup"},
		{Name: "semantic_search", Description: "Semantic/vector search"},
		{Name: "hybrid_search", Description: "Hybrid sparse+dense search (FTS + vector)"},
		{Name: "vector_reindex", Description: "Re-embed collection"},
		{Name: "vector_stats", Description: "Vector statistics"},
		{Name: "import_url", Description: "Import from URL"},
		{Name: "set_ttl", Description: "Set document TTL"},
		{Name: "full_text_search", Description: "Full-text search (with in-graph filtering, multi-language stemming)"},
		{Name: "fts_reindex", Description: "Reindex FTS for a collection"},
		{Name: "fts_languages", Description: "List supported FTS languages"},
		{Name: "register_webhook", Description: "Register webhook"},
		{Name: "list_webhooks", Description: "List webhooks"},
		{Name: "delete_webhook", Description: "Delete webhook"},
		{Name: "set_schema", Description: "Set JSON schema"},
		{Name: "get_schema", Description: "Get schema"},
		{Name: "delete_schema", Description: "Delete schema"},
		{Name: "list_schemas", Description: "List schemas"},
		{Name: "validate_document", Description: "Validate metadata"},
		{Name: "update_document", Description: "Partial document update"},
		{Name: "get_document_meta", Description: "Get document metadata"},
		{Name: "classify_document", Description: "Zero-shot classification"},
		{Name: "delete_collection", Description: "Delete entire collection"},
		{Name: "truncate_revisions", Description: "Truncate revision history"},
		{Name: "list_synonyms", Description: "List FTS synonyms"},
		{Name: "add_synonym", Description: "Add/update synonym group"},
		{Name: "delete_synonym", Description: "Delete synonym group"},
		{Name: "list_stopwords", Description: "List FTS stop words"},
		{Name: "add_stopwords", Description: "Add custom stop words"},
		{Name: "delete_stopwords", Description: "Remove custom stop words"},
		{Name: "get_meta_keys", Description: "List metadata keys and values"},
		{Name: "get_checksum", Description: "Collection CRC32 checksum"},
		{Name: "list_automation", Description: "List automation rules"},
		{Name: "create_automation", Description: "Create automation rule"},
		{Name: "get_automation", Description: "Get automation rule by ID"},
		{Name: "update_automation", Description: "Update automation rule"},
		{Name: "delete_automation", Description: "Delete automation rule"},
		{Name: "test_automation", Description: "Test trigger (dry run)"},
		{Name: "get_automation_logs", Description: "List automation execution logs"},
		{Name: "list_revisions", Description: "List document revision history"},
		{Name: "restore_revision", Description: "Restore document to a previous revision"},
		{Name: "get_collection_config", Description: "Get collection configuration"},
		{Name: "set_collection_config", Description: "Set collection configuration"},
		{Name: "list_collection_configs", Description: "List all collection configurations"},
		{Name: "cross_search", Description: "Cross-collection vector search"},
		{Name: "find_duplicates", Description: "Find duplicate and similar documents"},
		{Name: "aggregate", Description: "Aggregate metadata facets and date histograms for a collection"},
		{Name: "ingest_documents", Description: "Bulk ingest with URL key derivation, dedup, and auto-metadata"},
		{Name: "upload_file", Description: "Upload file (PDF, DOCX, HTML, TXT, MD, ODT, RTF, YAML, TEX, LOG) with auto-conversion"},
	}

	response := EndpointsResponse{
		HTTP: httpEndpoints,
		GRPC: grpcMethods,
		MCP:  mcpTools,
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(response); err != nil {
		http.Error(w, `{"error":"failed to encode response"}`, http.StatusInternalServerError)
	}
}

// httpEndpointCatalogue is MDDB's own statement of what it serves.
//
// Extracted from handleEndpoints so a test can hold it against the routes
// actually registered (TEST-002). A server advertising an endpoint it does not
// serve sends every caller to a 404 with no explanation; the two lists must
// agree, and now something checks that they do.
func (s *Server) httpEndpointCatalogue(authEnabled bool) []HTTPEndpoint {
	endpoints := []HTTPEndpoint{
		{Method: "GET/POST", Path: "/v1/health", Description: "Server health check", RequiresAuth: false},
		{Method: "GET", Path: "/v1/stats", Description: "Database statistics", RequiresAuth: authEnabled},
		{Method: "GET", Path: "/metrics", Description: "Prometheus metrics", RequiresAuth: false},

		// Core document operations
		{Method: "POST", Path: "/v1/add", Description: "Add/update document", RequiresAuth: authEnabled},
		{Method: "POST", Path: "/v1/add-batch", Description: "Batch add/update documents", RequiresAuth: authEnabled},
		{Method: "POST", Path: "/v1/ingest", Description: "Bulk ingest with URL key derivation, frontmatter extraction, dedup, and auto-metadata", RequiresAuth: authEnabled},
		{Method: "POST", Path: "/v1/get", Description: "Get document by key", RequiresAuth: authEnabled},
		{Method: "POST", Path: "/v1/search", Description: "Search documents with filters", RequiresAuth: authEnabled},
		{Method: "POST", Path: "/v1/delete", Description: "Delete document", RequiresAuth: authEnabled},
		{Method: "POST", Path: "/v1/delete-batch", Description: "Batch delete documents", RequiresAuth: authEnabled},
		{Method: "POST", Path: "/v1/delete-collection", Description: "Delete entire collection", RequiresAuth: authEnabled},

		// Export & backup
		{Method: "POST", Path: "/v1/export", Description: "Export collection (NDJSON/ZIP)", RequiresAuth: authEnabled},
		{Method: "POST", Path: "/v1/backup", Description: "Create database backup", RequiresAuth: authEnabled},
		{Method: "POST", Path: "/v1/restore", Description: "Restore from backup", RequiresAuth: authEnabled},
		{Method: "POST", Path: "/v1/truncate", Description: "Truncate revision history", RequiresAuth: authEnabled},

		// Vector search
		{Method: "POST", Path: "/v1/vector-search", Description: "Semantic search using embeddings", RequiresAuth: authEnabled},
		{Method: "POST", Path: "/v1/vector-reindex", Description: "Re-embed collection documents", RequiresAuth: authEnabled},
		{Method: "GET", Path: "/v1/vector-stats", Description: "Vector/embedding statistics", RequiresAuth: authEnabled},

		// Geo search
		{Method: "POST", Path: "/v1/geo-search", Description: "Radius search (R-tree or geohash)", RequiresAuth: authEnabled},
		{Method: "POST", Path: "/v1/geo-within", Description: "Bounding-box search", RequiresAuth: authEnabled},
		{Method: "POST", Path: "/v1/geo-reindex", Description: "Force-rebuild geo indexes from BoltDB", RequiresAuth: authEnabled},
		{Method: "GET", Path: "/v1/geo-stats", Description: "Geo index statistics", RequiresAuth: authEnabled},
		{Method: "POST", Path: "/v1/geo-encode", Description: "Encode (lat, lng) → geohash", RequiresAuth: authEnabled},
		{Method: "POST", Path: "/v1/geo-decode", Description: "Decode geohash → (lat, lng) + bbox", RequiresAuth: authEnabled},

		// Search features
		{Method: "POST", Path: "/v1/upload", Description: "Upload files (md/txt/html/pdf/docx/odt/rtf/yaml/tex/log) with auto-conversion to markdown", RequiresAuth: authEnabled},
		{Method: "POST", Path: "/v1/import-url", Description: "Import markdown from URL", RequiresAuth: authEnabled},
		{Method: "POST", Path: "/v1/set-ttl", Description: "Set document time-to-live", RequiresAuth: authEnabled},
		{Method: "POST", Path: "/v1/fts", Description: "Full-text search (with in-graph metadata filtering)", RequiresAuth: authEnabled},
		{Method: "GET/POST", Path: "/v1/code-graph", Description: "Code connection graph: which documents define, use or import a symbol", RequiresAuth: authEnabled},
		{Method: "POST", Path: "/v1/hybrid-search", Description: "Hybrid sparse+dense search (FTS + vector)", RequiresAuth: authEnabled},
		{Method: "GET/POST/DELETE", Path: "/v1/synonyms", Description: "Manage FTS synonyms", RequiresAuth: authEnabled},
		{Method: "GET/POST/DELETE", Path: "/v1/stopwords", Description: "Manage FTS stop words", RequiresAuth: authEnabled},

		// Metadata & checksum
		{Method: "GET", Path: "/v1/meta-keys", Description: "List unique metadata keys and values", RequiresAuth: authEnabled},
		{Method: "GET", Path: "/v1/checksum", Description: "Collection CRC32 checksum", RequiresAuth: authEnabled},

		// Partial update & doc-meta
		{Method: "PATCH", Path: "/v1/update", Description: "Partial document update (meta/content/ttl)", RequiresAuth: authEnabled},
		{Method: "GET", Path: "/v1/doc-meta", Description: "Get document metadata without content", RequiresAuth: authEnabled},

		// Zero-shot classification
		{Method: "POST", Path: "/v1/classify", Description: "Zero-shot document classification using embeddings", RequiresAuth: authEnabled},

		// Revisions
		{Method: "POST", Path: "/v1/revisions", Description: "List document revision history", RequiresAuth: authEnabled},
		{Method: "POST", Path: "/v1/revisions/restore", Description: "Restore document to a previous revision", RequiresAuth: authEnabled},

		// Webhooks
		{Method: "POST", Path: "/v1/webhooks", Description: "List/register webhooks", RequiresAuth: authEnabled},
		{Method: "POST", Path: "/v1/webhooks/delete", Description: "Delete webhook", RequiresAuth: authEnabled},

		// Automation (triggers, crons, webhook targets)

		// Schema validation
		{Method: "POST", Path: "/v1/schema/set", Description: "Set JSON schema for collection", RequiresAuth: authEnabled},
		{Method: "POST", Path: "/v1/schema/get", Description: "Get collection schema", RequiresAuth: authEnabled},
		{Method: "POST", Path: "/v1/schema/delete", Description: "Delete schema", RequiresAuth: authEnabled},
		{Method: "POST", Path: "/v1/schema/list", Description: "List all schemas", RequiresAuth: authEnabled},
		{Method: "POST", Path: "/v1/validate", Description: "Validate document metadata", RequiresAuth: authEnabled},

		// Collection config
		{Method: "GET", Path: "/v1/collection-config", Description: "Get collection configuration", RequiresAuth: authEnabled},
		{Method: "PUT", Path: "/v1/collection-config", Description: "Set collection configuration", RequiresAuth: authEnabled},
		{Method: "DELETE", Path: "/v1/collection-config", Description: "Delete collection configuration", RequiresAuth: authEnabled},
		{Method: "GET", Path: "/v1/collection-configs", Description: "List all collection configurations", RequiresAuth: authEnabled},

		// Cross-collection search
		{Method: "POST", Path: "/v1/cross-search", Description: "Cross-collection vector search", RequiresAuth: authEnabled},

		// Duplicate detection
		{Method: "POST", Path: "/v1/find-duplicates", Description: "Find duplicate and similar documents in a collection", RequiresAuth: authEnabled},

		// Aggregations
		{Method: "POST", Path: "/v1/aggregate", Description: "Aggregate metadata facets and date histograms", RequiresAuth: authEnabled},

		// Embedding configuration
		{Method: "GET", Path: "/v1/embedding-configs", Description: "List embedding configurations", RequiresAuth: authEnabled},
		{Method: "POST", Path: "/v1/embedding-configs", Description: "Create embedding configuration", RequiresAuth: authEnabled},
		{Method: "GET", Path: "/v1/embedding-configs/{id}", Description: "Get embedding configuration", RequiresAuth: authEnabled},
		{Method: "PUT", Path: "/v1/embedding-configs/{id}", Description: "Update embedding configuration", RequiresAuth: authEnabled},
		{Method: "DELETE", Path: "/v1/embedding-configs/{id}", Description: "Delete embedding configuration", RequiresAuth: authEnabled},
		{Method: "POST", Path: "/v1/embedding-configs/set-default", Description: "Set default embedding config", RequiresAuth: authEnabled},

		// Replication
		{Method: "GET", Path: "/v1/replication/status", Description: "Replication/cluster status", RequiresAuth: authEnabled},

		// System info
		{Method: "GET", Path: "/v1/system/info", Description: "System information", RequiresAuth: authEnabled},
		{Method: "GET", Path: "/v1/config", Description: "Server configuration", RequiresAuth: authEnabled},
		{Method: "GET", Path: "/v1/mcp/config", Description: "MCP YAML configuration", RequiresAuth: authEnabled},
		{Method: "GET", Path: "/v1/endpoints", Description: "List all endpoints", RequiresAuth: false},
	}

	// Subsystems that can be switched off. Listing an endpoint this server
	// does not serve sends every caller reading /v1/endpoints to a 404 with
	// no explanation, and omitting one it does serve hides a capability
	// (TEST-002).
	if s != nil && s.AutomationManager != nil {
		endpoints = append(endpoints,
			HTTPEndpoint{Method: "GET/POST", Path: "/v1/automation", Description: "List/create automation rules", RequiresAuth: authEnabled},
			HTTPEndpoint{Method: "GET/PUT/DELETE", Path: "/v1/automation/{id}", Description: "Get/update/delete automation rule", RequiresAuth: authEnabled},
			HTTPEndpoint{Method: "POST", Path: "/v1/automation/{id}/test", Description: "Test trigger (dry run)", RequiresAuth: authEnabled},
		)
		if s.AutomationLogStore != nil {
			endpoints = append(endpoints,
				HTTPEndpoint{Method: "GET", Path: "/v1/automation-logs", Description: "Get automation execution logs", RequiresAuth: authEnabled},
			)
		}
	}

	if s != nil && s.TemporalManager != nil {
		endpoints = append(endpoints,
			HTTPEndpoint{Method: "POST", Path: "/v1/temporal/query", Description: "Query documents by access history", RequiresAuth: authEnabled},
			HTTPEndpoint{Method: "GET", Path: "/v1/temporal/hot", Description: "Hot-documents leaderboard", RequiresAuth: authEnabled},
			HTTPEndpoint{Method: "GET", Path: "/v1/temporal/histogram", Description: "Access histogram over time", RequiresAuth: authEnabled},
		)
	}

	if s != nil && s.SpellManager != nil {
		endpoints = append(endpoints,
			HTTPEndpoint{Method: "GET/POST", Path: "/v1/spell-suggest", Description: "Spelling suggestions for a query", RequiresAuth: authEnabled},
			HTTPEndpoint{Method: "POST", Path: "/v1/spell-cleanup", Description: "Rebuild the spelling dictionary", RequiresAuth: authEnabled},
			HTTPEndpoint{Method: "GET/POST", Path: "/v1/spell-dictionary", Description: "Inspect or extend the spelling dictionary", RequiresAuth: authEnabled},
		)
	}

	return endpoints
}
