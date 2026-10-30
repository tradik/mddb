package main

// httpAccessRules classifies every HTTP route pattern the server registers,
// on the API listener and the MCP listener (SEC-019). The gate applies the
// rule before the handler runs; the collection is read from the query string
// (collection, collections) and from a JSON body (collection,
// sourceCollection, collections, targetCollections), all of them checked.
//
// TestEveryHTTPRouteIsClassified fails when a registered pattern is missing
// here, and a missing pattern is refused at run time while auth is on.
var httpAccessRules = map[string]accessRule{
	// Probes and login.
	"/health":                rulePublic,
	"/v1/health":             rulePublic,
	"/v1/auth/login":         rulePublic,
	"/metrics":               ruleAuthenticated, // public only with MDDB_METRICS_PUBLIC=true (the auth middleware's list)
	"/v1/endpoints":          ruleAuthenticated,
	"/v1/system/info":        ruleAuthenticated,
	"/v1/config":             ruleAuthenticated,
	"/v1/mcp/config":         ruleAuthenticated,
	"/v1/compliance-status":  ruleAuthenticated,
	"/v1/replication/status": ruleAuthenticated,
	"/v1/fts-languages":      ruleAuthenticated,
	"/v1/code-languages":     ruleAuthenticated,
	"/v1/geo-encode":         ruleAuthenticated, // arithmetic on the request, no collection
	"/v1/geo-decode":         ruleAuthenticated,
	"/v1/auth/me":            ruleAuthenticated, // the caller's own account
	"/v1/auth/api-key":       ruleAuthenticated,
	"/v1/auth/api-keys":      ruleAuthenticated,
	"/v1/auth/api-keys/":     ruleAuthenticated, // the handler refuses another user's key
	"/playground":            ruleAuthenticated,

	// Documents.
	"/v1/add":               ruleWrite,
	"/v1/add-batch":         ruleWrite,
	"/v1/ingest":            ruleWrite,
	"/v1/bulk-ingest-job":   ruleWrite,
	"/v1/update":            ruleWrite,
	"/v1/set-ttl":           ruleWrite,
	"/v1/import-url":        ruleWrite,
	"/v1/delete":            ruleWrite,
	"/v1/delete-batch":      ruleWrite,
	"/v1/delete-collection": ruleWrite,
	"/v1/truncate":          ruleWrite,
	"/v1/revisions/restore": ruleWrite,
	"/v1/get":               ruleRead,
	"/v1/search":            ruleRead,
	"/v1/export":            ruleRead,
	"/v1/doc-meta":          ruleRead,
	"/v1/checksum":          ruleRead,
	"/v1/meta-keys":         ruleRead,
	"/v1/revisions":         ruleRead,
	"/v1/image":             ruleRead,
	"/v1/validate":          ruleRead,
	// The where operations need write unless dryRun; the handler raises the
	// gate's read to write for a real run.
	"/v1/documents:deleteWhere": ruleRead,
	"/v1/documents:updateWhere": ruleRead,
	"/v1/documents:setTTLWhere": ruleRead,

	// Search.
	"/v1/fts":                ruleRead,
	"/v1/vector-search":      ruleRead,
	"/v1/hybrid-search":      ruleRead,
	"/v1/cross-search":       ruleRead, // sourceCollection and every target
	"/v1/autocomplete":       ruleRead,
	"/v1/code-graph":         ruleRead,
	"/v1/symbol":             ruleRead,
	"/v1/classify":           ruleRead,
	"/v1/find-duplicates":    ruleRead,
	"/v1/aggregate":          ruleRead,
	"/v1/vector-projection":  ruleRead,
	"/v1/geo-search":         ruleRead,
	"/v1/geo-within":         ruleRead,
	"/v1/geo-polygon":        ruleRead,
	"/v1/search-advisor":     ruleRead, // the handler also needs write for apply=true
	"/v1/eval":               ruleRead,
	"/v1/temporal/query":     ruleRead,
	"/v1/temporal/hot":       ruleRead,
	"/v1/temporal/histogram": ruleRead,
	"/v1/spell-suggest":      ruleRead,
	"/v1/spell-cleanup":      ruleRead,

	// Collection settings: GET reads, the other methods write.
	"/v1/synonyms":          ruleReadWrite,
	"/v1/stopwords":         ruleReadWrite,
	"/v1/spell-dictionary":  ruleReadWrite,
	"/v1/collection-config": ruleReadWrite,
	"/v1/eval-queries":      ruleReadWrite,
	"/v1/schema/set":        ruleWrite,
	"/v1/schema/delete":     ruleWrite,
	"/v1/schema/get":        ruleRead,
	"/v1/vector-reindex":    ruleWrite,
	"/v1/fts-reindex":       ruleWrite,

	// Every collection at once.
	"/v1/stats":              ruleAllListing,
	"/v1/vector-stats":       ruleAllListing,
	"/v1/collection-configs": ruleAllListing,
	"/v1/corpus":             ruleAllListing,
	"/v1/storage":            ruleListing, // ?collection narrows it
	"/v1/indexing-status":    ruleRead,    // no collection: the server's queue
	"/v1/bulk-ingest-jobs":   ruleRead,    // no collection: every job
	"/v1/geo-stats":          ruleAllRead,
	"/v1/schema/list":        ruleAllRead,
	"/v1/gain":               ruleAllRead,
	"/v1/webhooks": {class: accessCollection, op: PermWrite, // POST registers on its collection ("*" when none)
		methods: map[string]accessRule{"GET": ruleAdmin}}, // the list shows every hook's URL
	"/v1/webhooks/delete": ruleAllWrite,

	// The memory store: three fixed collections shared by every session.
	"/v1/memory/session":   memoryRule(PermWrite),
	"/v1/memory/message":   memoryRule(PermWrite),
	"/v1/memory/summarize": memoryRule(PermWrite),
	"/v1/memory/recall":    memoryRule(PermRead),
	"/v1/memory/sessions":  memoryRule(PermRead),
	"/v1/memory/history":   memoryRule(PermRead),

	// Authorized in the handler, which alone knows the collection.
	"/v1/upload":               ruleHandler, // multipart form field
	"/v1/import-wiki":          ruleHandler, // multipart form field
	"/v1/bulk-ingest-job/":     ruleHandler, // the job's collection
	"/v1/vector-reindex/jobs":  ruleHandler, // the jobs' collections
	"/v1/vector-reindex/jobs/": ruleHandler,
	"/v1/curation":             ruleHandler, // a rule looked up by id
	"/v1/aliases":              ruleHandler, // the alias's old and new collection
	"/v1/aliases/":             ruleHandler,
	"/v1/events":               ruleHandler, // each event is filtered by permission
	"/events":                  ruleHandler,
	"/graphql":                 ruleHandler, // per field: graphqlAccessRules
	"/es":                      ruleHandler, // the index in the path (INT-019)
	"/es/":                     ruleHandler,
	"/tools/call":              ruleHandler, // per tool: mcpToolAccessRules
	"/resources/read":          ruleHandler, // per resource: mcpResourceRule
	"/sse":                     ruleHandler, // JSON-RPC, per tool and resource
	"/message":                 ruleHandler,
	"/mcp":                     ruleHandler,
	"/tools":                   ruleAuthenticated,
	"/resources":               ruleAuthenticated,

	// Global administration.
	"/v1/backup":                        ruleAdmin,
	"/v1/restore":                       ruleAdmin,
	"/v1/geo-reindex":                   ruleAdmin,
	"/v1/embedding-configs":             ruleAdmin,
	"/v1/embedding-configs/":            ruleAdmin,
	"/v1/embedding-configs/set-default": ruleAdmin,
	"/v1/audit":                         ruleAdmin,
	"/v1/audit/exporters":               ruleAdmin,
	"/v1/encryption/status":             ruleAdmin,
	"/v1/encryption/rotate":             ruleAdmin,
	"/v1/encryption/jobs":               ruleAdmin,
	"/v1/encryption/jobs/":              ruleAdmin,
	"/v1/automation":                    ruleAdmin,
	"/v1/automation/":                   ruleAdmin,
	"/v1/automation-logs":               ruleAdmin,
	"/v1/corpus/build":                  ruleAdmin,
	"/v1/corpus/install":                ruleAdmin,
	"/v1/corpus/remove":                 ruleAdmin,
	"/v1/mcp/keys":                      ruleAdmin,
	"/v1/mcp/keys/disable":              ruleAdmin,
	"/v1/admin/diagnose":                ruleAdmin, // OPS-025
	"/v1/auth/register":                 ruleAdmin,
	"/v1/auth/permissions":              ruleAdmin,
	"/v1/auth/users":                    ruleAdmin,
	"/v1/auth/users/":                   ruleAdmin,
	"/v1/auth/groups":                   ruleAdmin,
	"/v1/auth/groups/":                  ruleAdmin,
	"/v1/auth/group-permissions":        ruleAdmin,
	"/debug/pprof/":                     ruleAdmin,
	"/debug/pprof/cmdline":              ruleAdmin,
	"/debug/pprof/profile":              ruleAdmin,
	"/debug/pprof/symbol":               ruleAdmin,
	"/debug/pprof/trace":                ruleAdmin,
}

// memoryCollections are the collections the memory store keeps.
var memoryCollections = []string{memorySessionsCollection, memoryMessagesCollection, memorySummariesCollection}

// memoryRule needs op on every memory collection: a session's messages and
// summaries are read and written together, and recall searches all of them.
func memoryRule(op PermissionType) accessRule {
	return accessRule{class: accessCollection, op: op, fixed: memoryCollections}
}
