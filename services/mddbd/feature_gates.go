package main

import (
	"net/http"
	"strings"
)

// Which entry points belong to which module (GO-049), one table per surface.
// The gates that read them sit where every call already passes: the gated
// mux for REST, an interceptor for gRPC (feature_gates_grpc.go), tools/list
// and the tool gate for MCP, the field middleware for GraphQL. An entry
// point in no table is core and is never switched off.
//
// Chat is matched by name as well, so routes, RPCs and tools added to the
// chat module later (skills, #404) are switched with it without an entry.

// httpFeatureRoutes maps a registered route pattern to its module.
var httpFeatureRoutes = map[string]string{
	"/v1/geo-search": FeatureGeo, "/v1/geo-within": FeatureGeo, "/v1/geo-polygon": FeatureGeo,
	"/v1/geo-reindex": FeatureGeo, "/v1/geo-stats": FeatureGeo, "/v1/geo-encode": FeatureGeo, "/v1/geo-decode": FeatureGeo,
	"/v1/temporal/query": FeatureTemporal, "/v1/temporal/hot": FeatureTemporal, "/v1/temporal/histogram": FeatureTemporal,
	"/v1/spell-suggest": FeatureSpell, "/v1/spell-cleanup": FeatureSpell, "/v1/spell-dictionary": FeatureSpell,
	"/v1/curation":   FeatureCuration,
	"/v1/automation": FeatureAutomation, "/v1/automation/": FeatureAutomation, "/v1/automation-logs": FeatureAutomation,
	"/v1/memory/session": FeatureMemory, "/v1/memory/message": FeatureMemory, "/v1/memory/recall": FeatureMemory,
	"/v1/memory/summarize": FeatureMemory, "/v1/memory/sessions": FeatureMemory, "/v1/memory/history": FeatureMemory,
	"/v1/code-graph": FeatureCodeIntel, "/v1/symbol": FeatureCodeIntel, "/v1/code-languages": FeatureCodeIntel,
	"/v1/eval": FeatureEval, "/v1/eval-queries": FeatureEval,
	"/v1/corpus": FeatureCorpus, "/v1/corpus/build": FeatureCorpus, "/v1/corpus/install": FeatureCorpus, "/v1/corpus/remove": FeatureCorpus,
	"/v1/classify":        FeatureClassify,
	"/v1/find-duplicates": FeatureDuplicates,
	"/v1/image":           FeatureImages,
	"/v1/webhooks":        FeatureWebhooks, "/v1/webhooks/delete": FeatureWebhooks,
	"/v1/schema/set": FeatureSchemas, "/v1/schema/get": FeatureSchemas, "/v1/schema/delete": FeatureSchemas,
	"/v1/schema/list": FeatureSchemas, "/v1/validate": FeatureSchemas,
	"/v1/aliases": FeatureAliases, "/v1/aliases/": FeatureAliases,
	"/v1/cross-search":   FeatureCrossSearch,
	"/v1/search-advisor": FeatureSearchTuning, "/v1/vector-projection": FeatureSearchTuning,
	"/v1/synonyms": FeatureFTSTuning, "/v1/stopwords": FeatureFTSTuning,
}

// routeFeature is the module of a route pattern, "" for core.
func routeFeature(pattern string) string {
	if strings.HasPrefix(pattern, "/v1/chat/") {
		return FeatureChat
	}
	return httpFeatureRoutes[pattern]
}

// grpcFeatureMethods maps an RPC's short name to its module.
var grpcFeatureMethods = map[string]string{
	"GeoSearch": FeatureGeo, "GeoWithin": FeatureGeo, "GeoReindex": FeatureGeo, "GeoStats": FeatureGeo,
	"GeoEncode": FeatureGeo, "GeoDecode": FeatureGeo,
	"TemporalQuery": FeatureTemporal, "TemporalHot": FeatureTemporal,
	"SpellSuggest": FeatureSpell, "SpellCleanup": FeatureSpell,
	"ListCurationRules": FeatureCuration, "CreateCurationRule": FeatureCuration,
	"UpdateCurationRule": FeatureCuration, "DeleteCurationRule": FeatureCuration,
	"ListAutomation": FeatureAutomation, "CreateAutomation": FeatureAutomation, "GetAutomation": FeatureAutomation,
	"UpdateAutomation": FeatureAutomation, "DeleteAutomation": FeatureAutomation, "TestAutomation": FeatureAutomation,
	"GetAutomationLogs": FeatureAutomation, "RunAutomation": FeatureAutomation,
	"CodeSymbol": FeatureCodeIntel,
	"RunEval":    FeatureEval, "AddEvalQuery": FeatureEval, "ListEvalQueries": FeatureEval, "DeleteEvalQuery": FeatureEval,
	"ListCorpusPacks": FeatureCorpus, "InstallCorpusPack": FeatureCorpus, "RemoveCorpusPack": FeatureCorpus,
	"BuildCorpusPack": FeatureCorpus,
	"Classify":        FeatureClassify,
	"FindDuplicates":  FeatureDuplicates,
	"RegisterWebhook": FeatureWebhooks, "ListWebhooks": FeatureWebhooks, "DeleteWebhook": FeatureWebhooks,
	"SetSchema": FeatureSchemas, "GetSchema": FeatureSchemas, "DeleteSchema": FeatureSchemas,
	"ListSchemas": FeatureSchemas, "ValidateDocument": FeatureSchemas,
	"CrossSearch":   FeatureCrossSearch,
	"SearchAdvisor": FeatureSearchTuning,
	"ListSynonyms":  FeatureFTSTuning, "AddSynonym": FeatureFTSTuning, "DeleteSynonym": FeatureFTSTuning,
	"ListStopwords": FeatureFTSTuning, "AddStopwords": FeatureFTSTuning, "DeleteStopwords": FeatureFTSTuning,
}

// rpcFeature is the module of a full gRPC method name, "" for core.
func rpcFeature(fullMethod string) string {
	if !strings.HasPrefix(fullMethod, "/mddb.MDDB/") {
		return "" // replication and health are core
	}
	name := strings.TrimPrefix(fullMethod, "/mddb.MDDB/")
	if strings.Contains(name, "Chat") {
		return FeatureChat
	}
	return grpcFeatureMethods[name]
}

// mcpToolsetFeatures maps whole MCP toolsets to a module.
var mcpToolsetFeatures = map[string]string{
	ToolsetGeo: FeatureGeo, ToolsetAutomation: FeatureAutomation, ToolsetMemory: FeatureMemory, ToolsetCode: FeatureCodeIntel,
	ToolsetWebhooks: FeatureWebhooks, ToolsetSchema: FeatureSchemas,
}

// mcpToolFeatures maps the module tools of mixed toolsets.
var mcpToolFeatures = map[string]string{
	"list_curation_rules": FeatureCuration, "create_curation_rule": FeatureCuration,
	"update_curation_rule": FeatureCuration, "delete_curation_rule": FeatureCuration,
	"corpus_list": FeatureCorpus, "corpus_install": FeatureCorpus, "corpus_remove": FeatureCorpus,
	"run_eval": FeatureEval, "add_eval_query": FeatureEval,
	"classify_document": FeatureClassify,
	"find_duplicates":   FeatureDuplicates,
	"cross_search":      FeatureCrossSearch,
	"search_advisor":    FeatureSearchTuning,
	"list_synonyms":     FeatureFTSTuning, "add_synonym": FeatureFTSTuning, "delete_synonym": FeatureFTSTuning,
	"list_stopwords": FeatureFTSTuning, "add_stopwords": FeatureFTSTuning, "delete_stopwords": FeatureFTSTuning,
}

// toolFeature is the module of a built-in MCP tool, "" for core.
func toolFeature(name string) string {
	if strings.HasPrefix(name, "chat_") {
		return FeatureChat
	}
	if f, ok := mcpToolFeatures[name]; ok {
		return f
	}
	return mcpToolsetFeatures[mcpToolGroup[name]]
}

// graphqlFeatureFields maps a GraphQL root field to its module.
var graphqlFeatureFields = map[string]string{
	"Query.codeGraph": FeatureCodeIntel, "Query.codeSymbol": FeatureCodeIntel,
	"Query.webhooks": FeatureWebhooks, "Mutation.registerWebhook": FeatureWebhooks, "Mutation.deleteWebhook": FeatureWebhooks,
	"Query.schema": FeatureSchemas, "Query.schemas": FeatureSchemas, "Mutation.setSchema": FeatureSchemas,
	"Mutation.deleteSchema": FeatureSchemas, "Mutation.validateDocument": FeatureSchemas,
}

// disabledFeatureOf returns the module of an entry point when that module is
// off, "" when the call may go on.
func (s *Server) disabledFeatureOf(feature string) string {
	if feature == "" || s.featureEnabled(feature) {
		return ""
	}
	return feature
}

// featureGate answers 404 on a route of a disabled module. Routes of no
// module get the handler as it is.
func (s *Server) featureGate(pattern string, next http.Handler) http.Handler {
	feature := routeFeature(pattern)
	if feature == "" {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.disabledFeatureOf(feature) != "" {
			writeFeatureDisabled(w, feature)
			return
		}
		next.ServeHTTP(w, r)
	})
}
