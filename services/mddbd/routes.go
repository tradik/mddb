package main

import (
	"log/slog"
	"net/http"
)

// HTTP route registration (TEST-002).
//
// These 107 registrations lived inside a 1089-line main(), where nothing could
// reach them: the file sat at 0.7% coverage while carrying the entire public
// surface of the server. Extracted verbatim so a test can assert what a
// deployment actually exposes — that every documented endpoint is wired, that
// none is registered twice, and that the write-guarded ones really are.
//
// registerRoutes is deliberately not clever: it is a list, and a list is the
// right shape for something whose only job is to be complete and auditable.
func (s *Server) registerRoutes(mux *http.ServeMux, authEnabled bool) {
	mux.HandleFunc("/health", s.handleHealth)
	mux.HandleFunc("/v1/health", s.handleHealth)
	mux.HandleFunc("/v1/compliance-status", s.handleComplianceStatus)
	mux.HandleFunc("/v1/add", s.guardWrite(s.handleAdd))
	mux.HandleFunc("/v1/add-batch", s.guardWrite(s.handleAddBatch))
	mux.HandleFunc("/v1/bulk-ingest-job", s.guardWrite(s.handleBulkIngestSubmit))
	mux.HandleFunc("/v1/bulk-ingest-job/", s.handleBulkIngestStatus)
	mux.HandleFunc("/v1/bulk-ingest-jobs", s.handleBulkIngestList)
	mux.HandleFunc("/v1/ingest", s.guardWrite(s.handleIngest))
	mux.HandleFunc("/v1/get", s.handleGet)
	mux.HandleFunc("/v1/search", s.handleSearch)
	mux.HandleFunc("/v1/export", s.handleExport)
	mux.HandleFunc("/v1/backup", s.handleBackup)
	mux.HandleFunc("/v1/restore", s.guardWrite(s.handleRestore))
	mux.HandleFunc("/v1/truncate", s.guardWrite(s.handleTruncate))
	mux.HandleFunc("/v1/delete", s.guardWrite(s.handleDelete))
	mux.HandleFunc("/v1/delete-batch", s.guardWrite(s.handleDeleteBatch))
	mux.HandleFunc("/v1/delete-collection", s.guardWrite(s.handleDeleteCollection))
	mux.HandleFunc("/v1/stats", s.handleStats)
	// GO-033: the four query paths that hold working memory for the length of
	// the request queue behind a semaphore, so a burst becomes a 503 a client
	// can retry rather than an OOM that takes every other request with it.
	mux.HandleFunc("/v1/vector-search", s.withSearchLimit(s.handleVectorSearch))
	mux.HandleFunc("/v1/vector-reindex", s.guardWrite(s.handleVectorReindex))
	mux.HandleFunc("/v1/vector-stats", s.handleVectorStats)
	mux.HandleFunc("/v1/vector-projection", s.handleVectorProjection)
	mux.HandleFunc("/v1/geo-search", s.handleGeoSearch)
	mux.HandleFunc("/v1/geo-within", s.handleGeoWithin)
	mux.HandleFunc("/v1/geo-polygon", s.handleGeoPolygon)
	mux.HandleFunc("/v1/geo-reindex", s.guardWrite(s.handleGeoReindex))
	mux.HandleFunc("/v1/geo-stats", s.handleGeoStats)
	mux.HandleFunc("/v1/geo-encode", s.handleGeoEncode)
	mux.HandleFunc("/v1/geo-decode", s.handleGeoDecode)
	mux.HandleFunc("/v1/embedding-configs", s.handleEmbeddingConfigs)
	mux.HandleFunc("/v1/embedding-configs/", s.handleEmbeddingConfigDetail)
	mux.HandleFunc("/v1/embedding-configs/set-default", s.guardWrite(s.handleSetDefaultEmbeddingConfig))
	mux.HandleFunc("/v1/upload", s.guardWrite(s.handleUpload))
	mux.HandleFunc("/v1/import-url", s.guardWrite(s.handleImportURL))
	mux.HandleFunc("/v1/import-wiki", s.guardWrite(s.handleWikiImport))
	mux.HandleFunc("/v1/set-ttl", s.guardWrite(s.handleSetTTL))
	mux.HandleFunc("/v1/fts", s.withSearchLimit(s.handleFTS))
	mux.HandleFunc("/v1/fts-reindex", s.guardWrite(s.handleFTSReindex))
	mux.HandleFunc("/v1/fts-languages", s.handleFTSLanguages)
	mux.HandleFunc("/v1/autocomplete", s.handleAutocomplete)
	mux.HandleFunc("/v1/meta-keys", s.handleMetaKeys)
	mux.HandleFunc("/v1/code-graph", s.withSearchLimit(s.handleCodeGraph))
	mux.HandleFunc("/v1/checksum", s.handleChecksum)
	mux.HandleFunc("/v1/update", s.guardWrite(s.handleUpdate))
	mux.HandleFunc("/v1/doc-meta", s.handleDocMeta)
	mux.HandleFunc("/v1/classify", s.handleClassify)
	mux.HandleFunc("/v1/hybrid-search", s.withSearchLimit(s.handleHybridSearch))
	mux.HandleFunc("/v1/synonyms", s.handleSynonyms)
	mux.HandleFunc("/v1/stopwords", s.handleStopWords)
	mux.HandleFunc("/v1/audit", s.handleAudit)
	mux.HandleFunc("/v1/audit/exporters", s.handleAuditExporters)
	mux.HandleFunc("/v1/encryption/status", s.handleEncryptionStatus)
	mux.HandleFunc("/v1/encryption/rotate", s.handleEncryptionRotate)
	mux.HandleFunc("/v1/encryption/jobs", s.handleEncryptionJob)
	mux.HandleFunc("/v1/encryption/jobs/", s.handleEncryptionJob)
	mux.HandleFunc("/v1/webhooks", s.handleWebhooks)
	mux.HandleFunc("/v1/webhooks/delete", s.guardWrite(s.handleWebhookDelete))
	mux.HandleFunc("/v1/revisions", s.handleRevisions)
	mux.HandleFunc("/v1/revisions/restore", s.guardWrite(s.handleRevisionRestore))
	if s.AutomationManager != nil {
		mux.HandleFunc("/v1/automation", s.handleAutomation)
		mux.HandleFunc("/v1/automation/", s.handleAutomationDetail)
		if s.AutomationLogStore != nil {
			mux.HandleFunc("/v1/automation-logs", s.handleAutomationLogs)
		}
	}
	mux.HandleFunc("/v1/schema/set", s.guardWrite(s.handleSchemaSet))
	mux.HandleFunc("/v1/schema/get", s.handleSchemaGet)
	mux.HandleFunc("/v1/schema/delete", s.guardWrite(s.handleSchemaDelete))
	mux.HandleFunc("/v1/schema/list", s.handleSchemaList)
	mux.HandleFunc("/v1/validate", s.handleValidate)
	// SRCH-010: "how should I search this collection?" — measured from the
	// collection rather than guessed from the algorithm names.
	mux.HandleFunc("/v1/search-advisor", s.handleSearchAdvisor)
	mux.HandleFunc("/v1/collection-config", s.handleCollectionConfig)
	mux.HandleFunc("/v1/collection-configs", s.handleCollectionConfigList)
	mux.HandleFunc("/v1/curation", s.handleCuration)
	mux.HandleFunc("/v1/events", s.handleSSE)
	// Memory RAG endpoints
	mux.HandleFunc("/v1/memory/session", s.guardWrite(s.handleMemorySessionCreate))
	mux.HandleFunc("/v1/memory/message", s.guardWrite(s.handleMemoryMessageAdd))
	mux.HandleFunc("/v1/memory/recall", s.handleMemoryRecall)
	mux.HandleFunc("/v1/memory/summarize", s.guardWrite(s.handleMemorySummarize))
	mux.HandleFunc("/v1/memory/sessions", s.handleMemorySessionsList)
	mux.HandleFunc("/v1/memory/history", s.handleMemoryHistory)

	mux.HandleFunc("/v1/cross-search", s.handleCrossSearch)
	mux.HandleFunc("/v1/find-duplicates", s.handleFindDuplicates)
	mux.HandleFunc("/v1/aggregate", s.withSearchLimit(s.handleAggregate))
	// Temporal event tracking
	if s.TemporalManager != nil {
		mux.HandleFunc("/v1/temporal/query", s.handleTemporalQuery)
		mux.HandleFunc("/v1/temporal/hot", s.handleTemporalHot)
		mux.HandleFunc("/v1/temporal/histogram", s.handleTemporalHistogram)
	}
	// Spell correction
	if s.SpellManager != nil {
		mux.HandleFunc("/v1/spell-suggest", s.handleSpellSuggest)
		mux.HandleFunc("/v1/spell-cleanup", s.handleSpellCleanup)
		mux.HandleFunc("/v1/spell-dictionary", s.handleSpellDictionary)
	}
	mux.HandleFunc("/metrics", s.Metrics.HandleMetrics)

	// pprof profiling endpoints (disabled by default, set MDDB_PPROF_ENABLED=true)
	if env("MDDB_PPROF_ENABLED", "false") == "true" {
		registerPprof(mux)
		slog.Info("pprof profiling endpoints enabled at /debug/pprof")
	}

	// Replication status endpoint
	mux.HandleFunc("/v1/replication/status", s.handleReplicationStatus)

	// System info and config endpoints
	mux.HandleFunc("/v1/system/info", s.handleSystemInfo)
	mux.HandleFunc("/v1/config", s.handleConfig)
	mux.HandleFunc("/v1/mcp/config", s.handleMCPConfig)
	mux.HandleFunc("/v1/mcp/keys", s.guardWrite(s.handleMCPAPIKeys))
	mux.HandleFunc("/v1/mcp/keys/disable", s.guardWrite(s.handleMCPAPIKeyDisable))
	mux.HandleFunc("/v1/endpoints", s.handleEndpoints)

	// Auth endpoints (if enabled)
	if authEnabled {
		mux.HandleFunc("/v1/auth/login", s.handleAuthLogin)
		mux.HandleFunc("/v1/auth/register", s.handleAuthRegister)
		mux.HandleFunc("/v1/auth/api-key", s.handleAuthAPIKey)
		mux.HandleFunc("/v1/auth/api-keys/", s.handleAuthAPIKeyDelete) // Note: trailing slash for DELETE /v1/auth/api-keys/:keyHash
		mux.HandleFunc("/v1/auth/api-keys", s.handleAuthAPIKeysList)
		mux.HandleFunc("/v1/auth/me", s.handleAuthMe)
		mux.HandleFunc("/v1/auth/permissions", s.handleAuthPermissions)
		mux.HandleFunc("/v1/auth/users/", s.handleAuthDeleteUser) // Note: trailing slash for DELETE /v1/auth/users/:username
		mux.HandleFunc("/v1/auth/users", s.handleAuthUsersList)
		mux.HandleFunc("/v1/auth/groups", s.handleAuthGroups)
		mux.HandleFunc("/v1/auth/groups/", s.handleAuthGroupDetail)
		mux.HandleFunc("/v1/auth/group-permissions", s.handleAuthGroupPermissions)
	}
}
