package main

// grpcAccessRules classifies every gRPC method by its short name, for the
// services in mddb.proto (SEC-019). The collection is read from the request
// message: collection, source_collection, collections and
// target_collections, as alias resolution reads them.
//
// TestEveryGRPCMethodIsClassified walks the service descriptors and fails on a
// method missing here.
var grpcAccessRules = map[string]accessRule{
	// Documents.
	"/mddb.MDDB/Add":              ruleWrite,
	"/mddb.MDDB/AddBatch":         ruleWrite,
	"/mddb.MDDB/UpdateBatch":      ruleWrite,
	"/mddb.MDDB/DeleteBatch":      ruleWrite,
	"/mddb.MDDB/Ingest":           ruleWrite,
	"/mddb.MDDB/UpdateDocument":   ruleWrite,
	"/mddb.MDDB/DeleteDocument":   ruleWrite,
	"/mddb.MDDB/DeleteCollection": ruleWrite,
	"/mddb.MDDB/Truncate":         ruleWrite,
	"/mddb.MDDB/ImportURL":        ruleWrite,
	"/mddb.MDDB/SetTTL":           ruleWrite,
	"/mddb.MDDB/RestoreRevision":  ruleWrite,
	"/mddb.MDDB/Get":              ruleRead,
	"/mddb.MDDB/Search":           ruleRead,
	"/mddb.MDDB/Export":           ruleRead,
	"/mddb.MDDB/GetDocumentMeta":  ruleRead,
	"/mddb.MDDB/GetMetaKeys":      ruleRead,
	"/mddb.MDDB/GetChecksum":      ruleRead,
	"/mddb.MDDB/ListRevisions":    ruleRead,
	"/mddb.MDDB/ValidateDocument": ruleRead,
	// Need write unless dry_run; the method raises the gate's read.
	"/mddb.MDDB/DeleteWhere": ruleRead,
	"/mddb.MDDB/UpdateWhere": ruleRead,
	"/mddb.MDDB/SetTTLWhere": ruleRead,

	// Search.
	"/mddb.MDDB/VectorSearch":      ruleRead,
	"/mddb.MDDB/HybridSearch":      ruleRead,
	"/mddb.MDDB/FTS":               ruleRead,
	"/mddb.MDDB/CrossSearch":       ruleRead,
	"/mddb.MDDB/FindDuplicates":    ruleRead,
	"/mddb.MDDB/GeoSearch":         ruleRead,
	"/mddb.MDDB/GeoWithin":         ruleRead,
	"/mddb.MDDB/Classify":          ruleRead,
	"/mddb.MDDB/SearchAdvisor":     ruleRead, // the method also needs write to apply
	"/mddb.MDDB/GetIndexingStatus": ruleRead,
	"/mddb.MDDB/CodeSymbol":        ruleRead,
	"/mddb.MDDB/RunEval":           ruleRead,
	"/mddb.MDDB/ListEvalQueries":   ruleRead,
	"/mddb.MDDB/TemporalQuery":     ruleRead,
	"/mddb.MDDB/TemporalHot":       ruleRead,
	"/mddb.MDDB/SpellSuggest":      ruleRead,
	"/mddb.MDDB/SpellCleanup":      ruleRead,
	"/mddb.MDDB/AddEvalQuery":      ruleWrite,
	"/mddb.MDDB/DeleteEvalQuery":   ruleWrite,

	// Collection settings.
	"/mddb.MDDB/VectorReindex":       ruleWrite,
	"/mddb.MDDB/FTSReindex":          ruleWrite,
	"/mddb.MDDB/GeoReindex":          ruleWrite,
	"/mddb.MDDB/SetSchema":           ruleWrite,
	"/mddb.MDDB/DeleteSchema":        ruleWrite,
	"/mddb.MDDB/GetSchema":           ruleRead,
	"/mddb.MDDB/ListSynonyms":        ruleRead,
	"/mddb.MDDB/AddSynonym":          ruleWrite,
	"/mddb.MDDB/DeleteSynonym":       ruleWrite,
	"/mddb.MDDB/ListStopwords":       ruleRead,
	"/mddb.MDDB/AddStopwords":        ruleWrite,
	"/mddb.MDDB/DeleteStopwords":     ruleWrite,
	"/mddb.MDDB/GetCollectionConfig": ruleRead,
	"/mddb.MDDB/SetCollectionConfig": ruleWrite,
	"/mddb.MDDB/RegisterWebhook":     ruleWrite, // its collection, "*" when none
	"/mddb.MDDB/ListCurationRules":   ruleRead,  // none named: every rule

	// A curation rule is looked up by id; the method checks its collection.
	"/mddb.MDDB/CreateCurationRule": ruleHandler,
	"/mddb.MDDB/UpdateCurationRule": ruleHandler,
	"/mddb.MDDB/DeleteCurationRule": ruleHandler,

	// Every collection at once.
	"/mddb.MDDB/ListSchemas":           ruleAllListing,
	"/mddb.MDDB/ListCollectionConfigs": ruleAllListing,
	"/mddb.MDDB/GeoStats":              ruleAllRead,
	"/mddb.MDDB/GetGain":               ruleAllRead,
	"/mddb.MDDB/DeleteWebhook":         ruleAllWrite,

	// No collection data.
	"/mddb.MDDB/FTSLanguages": ruleAuthenticated,
	"/mddb.MDDB/GeoEncode":    ruleAuthenticated,
	"/mddb.MDDB/GeoDecode":    ruleAuthenticated,

	// Global administration.
	"/mddb.MDDB/Backup":            ruleAdmin,
	"/mddb.MDDB/Restore":           ruleAdmin,
	"/mddb.MDDB/Stats":             ruleAdmin,
	"/mddb.MDDB/VectorStats":       ruleAdmin,
	"/mddb.MDDB/ListWebhooks":      ruleAdmin,
	"/mddb.MDDB/ListAutomation":    ruleAdmin,
	"/mddb.MDDB/CreateAutomation":  ruleAdmin,
	"/mddb.MDDB/GetAutomation":     ruleAdmin,
	"/mddb.MDDB/UpdateAutomation":  ruleAdmin,
	"/mddb.MDDB/DeleteAutomation":  ruleAdmin,
	"/mddb.MDDB/TestAutomation":    ruleAdmin,
	"/mddb.MDDB/GetAutomationLogs": ruleAdmin,

	// Replication authorizes itself: a replication secret, a verified mTLS
	// certificate or an administrator (authorizeReplication, SEC-001).
	"/mddb.MDDBReplication/RequestSnapshot":   ruleHandler,
	"/mddb.MDDBReplication/StreamBinlog":      ruleHandler,
	"/mddb.MDDBReplication/ReplicationStatus": ruleHandler,
	"/mddb.MDDBReplication/AcknowledgeLSN":    ruleHandler,

	// Server reflection, for grpcurl: the schema, no data.
	"/grpc.reflection.v1.ServerReflection/ServerReflectionInfo":      ruleAuthenticated,
	"/grpc.reflection.v1alpha.ServerReflection/ServerReflectionInfo": ruleAuthenticated,
}
