package main

// featureRegistry lists every switchable module, in the order the panel
// shows them. Everything but chatExternalMcp and the six endpoints added in
// 2.19.10 is on by default, so a server that never touched a switch behaves
// as before: a module that opens a new way in starts off.
//
// Each module carries three texts (FE-021). Title and Summary are what a
// person reads on the Features page: a short name and one plain sentence on
// what switching it on gives them. Description is the technical detail —
// routes, environment variables, what stays working while it is off — shown
// behind the help control. None of them names an issue or a ticket; the
// guard in feature_texts_test.go fails the build when one does.
var featureRegistry = []featureDef{
	{Name: FeatureChat, Default: true, Title: "Chat assistants",
		Summary:     "Chat assistants that answer questions from your collections.",
		Description: "Chat scenarios, conversations and skills, and the join of the mddb-chat service."},
	{Name: FeatureChatExternalMCP, Requires: []string{FeatureChat}, Title: "Tools from other MCP servers",
		Summary:     "Lets chat assistants use read-only tools of other MCP servers you add.",
		Description: "A scenario lists the external MCP servers and tools it may call; each tool is checked against its server when the scenario is saved. Needs chat."},
	{Name: FeatureGeo, Default: true, Title: "Geo search",
		Summary:     "Find documents near a place, inside a box or inside a drawn area.",
		Description: "Radius, bounding box and polygon queries over location metadata, geohash helpers and the geo index."},
	{Name: FeatureTemporal, Default: true, Title: "Activity over time",
		Summary:     "Shows which documents are created, changed or read most, and when.",
		Description: "Document events, hot documents and histograms under /v1/temporal/."},
	{Name: FeatureSpell, Default: true, Title: "Spell checker",
		Summary:     "Suggests corrections for misspelled search words.",
		Description: "Suggestions, cleanup and a dictionary per collection."},
	{Name: FeatureCuration, Default: true, Title: "Pinned and hidden results",
		Summary:     "Pin chosen documents to the top of a query's results, or hide them.",
		Description: "Curation rules per collection and query."},
	{Name: FeatureAutomation, Default: true, Title: "Automation",
		Summary:     "Runs triggers, scheduled jobs and webhooks on the server for you.",
		Description: "Triggers, crons and webhook actions run by the server, and their run logs."},
	{Name: FeatureMemory, Default: true, Title: "Agent memory",
		Summary:     "Lets AI agents keep conversations and recall them later.",
		Description: "Sessions, messages, recall and summaries."},
	{Name: FeatureCodeIntel, Default: true, Title: "Code intelligence",
		Summary:     "Find functions and classes in source code, and what uses them.",
		Description: "Symbols, the code graph and the reference tools over code collections."},
	{Name: FeatureEval, Default: true, Title: "Search quality checks",
		Summary:     "Measures how well search finds the right documents for your test questions.",
		Description: "A collection's evaluation queries with their expected documents, and their scores."},
	{Name: FeatureCorpus, Default: true, Title: "Corpus packs",
		Summary:     "Package a collection once as a read-only pack and install it on any server.",
		Description: "Build, list and install corpus packs; an installed pack is a read-only collection."},
	{Name: FeatureClassify, Default: true, Title: "Classification",
		Summary:     "Ranks your labels against a document or a piece of text.",
		Description: "Labels are scored by similarity to the document or text."},
	{Name: FeatureDuplicates, Default: true, Title: "Duplicate finder",
		Summary:     "Finds documents that are identical or nearly the same.",
		Description: "Identical, similar and MinHash groups per collection."},
	{Name: FeatureImages, Default: true, Title: "Image documents",
		Summary:     "Serves the original picture of an image document.",
		Description: "The bytes of an image document over /v1/image."},
	// The management surfaces below (2.19.0): switched off, what they stored
	// keeps working — webhooks are delivered, schemas checked on write,
	// aliases resolved, synonyms and stop words applied.
	{Name: FeatureWebhooks, Default: true, Title: "Webhooks",
		Summary:     "Add, list and remove webhooks.",
		Description: "Webhook management; webhooks already registered are delivered whether this is on or off."},
	{Name: FeatureSchemas, Default: true, Title: "Collection schemas",
		Summary:     "Define which metadata fields a collection's documents must have.",
		Description: "Schema management and /v1/validate; stored schemas are checked on every write whether this is on or off."},
	{Name: FeatureAliases, Default: true, Title: "Collection aliases",
		Summary:     "Give a collection a second name, for example to switch versions without changing clients.",
		Description: "Create, list and delete aliases; existing aliases resolve whether this is on or off."},
	{Name: FeatureCrossSearch, Default: true, Title: "Search across collections",
		Summary:     "Run one query over several collections at once.",
		Description: "Cross search over the collections the caller may read."},
	{Name: FeatureSearchTuning, Default: true, Title: "Search tuning tools",
		Summary:     "Advice on search settings, and a map of how your documents relate.",
		Description: "The Search Advisor and the vector space projection."},
	{Name: FeatureFTSTuning, Default: true, Title: "Synonyms and stop words",
		Summary:     "Edit synonyms and the words full-text search ignores.",
		Description: "Synonym and stop word management; stored ones apply whether this is on or off."},
	// Endpoints in the request shapes a chat front end sends to an external
	// engine (2.19.10): off until an administrator turns them on.
	{Name: FeatureWebSearch, Title: "Web search endpoint",
		Summary:     "Lets a chat front end use your collections as its web search source.",
		Description: "POST /v1/web-search searches the collections the caller may read, in the Open WebUI external web search shape."},
	{Name: FeatureRerankAPI, Title: "Rerank endpoint",
		Summary:     "Lets other tools reorder their search results with this server's reranking model.",
		Description: "POST /v1/rerank scores texts with the configured reranking model."},
	{Name: FeatureExtractAPI, Title: "File-to-Markdown endpoint",
		Summary:     "Lets other tools convert a file to Markdown here without storing it.",
		Description: "PUT /v1/extract/process converts an uploaded file and returns the Markdown."},
	// Other products' API shapes over MDDB's own search (2.19.10): off until
	// an administrator turns them on, so an upgrade opens no new way in.
	{Name: FeatureVectorStores, Title: "Vector store API",
		Summary:     "Lets apps built for a vector store API search your collections.",
		Description: "OpenAI-compatible GET /v1/vector_stores and POST /v1/vector_stores/{id}/search."},
	{Name: FeatureESCompat, DefaultEnv: esCompatEnv, Title: "Search API under /es",
		Summary:     "Lets existing search clients query your collections without code changes.",
		Description: "Elasticsearch-compatible subset under /es. MDDB_ES_COMPAT=true turns it on by default."},
	{Name: FeatureQdrantCompat, DefaultEnv: qdrantCompatEnv, Title: "Vector query under /qdrant",
		Summary:     "Lets existing vector search clients query your collections without code changes.",
		Description: "Qdrant-compatible point query under /qdrant. MDDB_QDRANT_COMPAT=true turns it on by default."},
}
