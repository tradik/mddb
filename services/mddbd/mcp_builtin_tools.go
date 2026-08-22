package main

// Shared schema descriptions for the token-saving projection controls
// (issue #102), reused across search_documents / semantic_search /
// full_text_search so the wording stays in one place.
const (
	mcpIncludeContentDesc = "(v2.10.2+) Include the document body (content_md) in each hit. Default: true, or false when fields is set (v2.11.0+, GO-019). Pass explicitly to override either default."
	mcpFieldsDesc         = "(v2.10.2+) Restrict returned meta to these keys (e.g. [\"name\",\"currentVersion\"]); each hit is reduced to id, key and the listed meta. Since v2.11.0 setting fields also drops the body unless include_content=true is passed explicitly. Empty = all meta."
)

// mcpBuiltinTools returns the full builtin MCP tool catalog. Partitioned into
// core + advanced groups (GO-015) to keep any single function manageable.
func mcpBuiltinTools() []MCPTool {
	t := mcpBuiltinToolsCore()
	return append(t, mcpBuiltinToolsAdvanced()...)
}

// mcpBuiltinToolsCore lists the core document / search / index / webhook /
// schema / synonym / automation builtin tools.
func mcpBuiltinToolsCore() []MCPTool {
	return []MCPTool{
		{
			Name:        "add_document",
			Description: "Add or update a document in MDDB",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"collection": map[string]interface{}{"type": "string"},
					"key":        map[string]interface{}{"type": "string"},
					"lang":       map[string]interface{}{"type": "string"},
					"content_md": map[string]interface{}{"type": "string"},
					"meta":       map[string]interface{}{"type": "object"},
				},
				"required": []string{"collection", "key", "lang", "content_md"},
			},
		},
		{
			Name:        "search_documents",
			Description: "Search documents with filters and sorting",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"collection":      map[string]interface{}{"type": "string"},
					"filter_meta":     map[string]interface{}{"type": "object"},
					"sort":            map[string]interface{}{"type": "string"},
					"limit":           map[string]interface{}{"type": "integer"},
					"offset":          map[string]interface{}{"type": "integer"},
					"include_content": map[string]interface{}{"type": "boolean", "description": mcpIncludeContentDesc},
					"fields":          map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}, "description": mcpFieldsDesc},
				},
				"required": []string{"collection"},
			},
		},
		{
			Name:        "delete_document",
			Description: "Delete a document from MDDB",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"collection": map[string]interface{}{"type": "string"},
					"key":        map[string]interface{}{"type": "string"},
					"lang":       map[string]interface{}{"type": "string"},
				},
				"required": []string{"collection", "key", "lang"},
			},
		},
		{
			Name:        "get_stats",
			Description: "Get MDDB server statistics",
			InputSchema: map[string]interface{}{
				"type": "object",
			},
		},
		{
			Name:        "aggregate",
			Description: "Compute metadata facets (value counts) and date histograms over a collection, with optional metadata pre-filtering. Useful for dashboards and exploratory analytics.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"collection":  map[string]interface{}{"type": "string", "description": "Collection to aggregate over"},
					"filter_meta": map[string]interface{}{"type": "object", "description": "Optional metadata filter applied before aggregation"},
					"facets": map[string]interface{}{
						"type":        "array",
						"description": "Fields to compute value-count facets on. Each item may be a string (field name) or an object {field, order_by}",
						"items":       map[string]interface{}{"type": "object"},
					},
					"histograms": map[string]interface{}{
						"type":        "array",
						"description": "Date histograms to compute. Each item may be a string (field name) or an object {field, interval} where interval is e.g. 'day', 'week', 'month'",
						"items":       map[string]interface{}{"type": "object"},
					},
					"max_facet_size": map[string]interface{}{"type": "integer", "description": "Maximum number of facet values to return per field (default: 50)"},
				},
				"required": []string{"collection"},
			},
		},
		{
			Name:        "semantic_search",
			Description: "Search documents by meaning using semantic similarity. Use this when you need to find documents related to a concept or question, rather than filtering by exact metadata tags. Requires embedding provider to be configured on the server.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"collection":      map[string]interface{}{"type": "string", "description": "Collection to search in"},
					"query":           map[string]interface{}{"type": "string", "description": "Natural language search query"},
					"top_k":           map[string]interface{}{"type": "integer", "description": "Number of results to return (default: 5)"},
					"threshold":       map[string]interface{}{"type": "number", "description": "Minimum similarity score 0-1 (default: 0.0)"},
					"filter_meta":     map[string]interface{}{"type": "object", "description": "Optional metadata filter to combine with semantic search"},
					"algorithm":       map[string]interface{}{"type": "string", "description": "Vector search algorithm: flat (exact, default), hnsw (approximate), ivf (clustered), pq (compressed)"},
					"distance_metric": map[string]interface{}{"type": "string", "description": "Distance metric: cosine (default), dot_product, euclidean"},
					"retrieval_mode":  map[string]interface{}{"type": "string", "description": "Result granularity: parent (default, one result per document), chunk (matching passage with chunkIndex/chunkText), window (passage plus neighboring chunks)"},
					"window_size":     map[string]interface{}{"type": "integer", "description": "Neighbor chunks per side in window mode (default: 1)"},
					"mmr":             map[string]interface{}{"type": "boolean", "description": "Diversify results via Maximal Marginal Relevance reranking (default: false)"},
					"mmr_lambda":      map[string]interface{}{"type": "number", "description": "MMR relevance/diversity balance 0-1; 1.0 = pure relevance, 0.0 = max diversity (default: 0.5)"},
					"include_content": map[string]interface{}{"type": "boolean", "description": mcpIncludeContentDesc},
					"fields":          map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}, "description": mcpFieldsDesc},
				},
				"required": []string{"collection", "query"},
			},
		},
		{
			Name:        "vector_reindex",
			Description: "Re-embed all documents in a collection. Use after adding many documents or changing the embedding model.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"collection": map[string]interface{}{"type": "string", "description": "Collection to reindex"},
					"force":      map[string]interface{}{"type": "boolean", "description": "Force re-embed even if content hasn't changed (default: false)"},
				},
				"required": []string{"collection"},
			},
		},
		{
			Name:        "vector_stats",
			Description: "Get vector/embedding statistics including provider info and per-collection embedding coverage.",
			InputSchema: map[string]interface{}{
				"type": "object",
			},
		},
		{
			Name:        "import_url",
			Description: "Import a markdown document from a URL. Supports YAML frontmatter for metadata extraction.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"collection": map[string]interface{}{"type": "string", "description": "Target collection"},
					"url":        map[string]interface{}{"type": "string", "description": "URL to fetch markdown from"},
					"lang":       map[string]interface{}{"type": "string", "description": "Language code (e.g. en_US)"},
					"key":        map[string]interface{}{"type": "string", "description": "Document key (auto-derived from URL if empty)"},
					"meta":       map[string]interface{}{"type": "object", "description": "Additional metadata (overrides frontmatter)"},
					"ttl":        map[string]interface{}{"type": "integer", "description": "Time-to-live in seconds (0 = no expiry)"},
				},
				"required": []string{"collection", "url", "lang"},
			},
		},
		{
			Name:        "set_ttl",
			Description: "Set or remove time-to-live on a document. The document will be automatically deleted after TTL expires.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"collection": map[string]interface{}{"type": "string", "description": "Collection name"},
					"key":        map[string]interface{}{"type": "string", "description": "Document key"},
					"lang":       map[string]interface{}{"type": "string", "description": "Language code"},
					"ttl":        map[string]interface{}{"type": "integer", "description": "TTL in seconds (0 = remove TTL)"},
				},
				"required": []string{"collection", "key", "lang", "ttl"},
			},
		},
		{
			Name:        "full_text_search",
			Description: "Search documents by text content using full-text search with term matching and relevance scoring. Supports typo tolerance via fuzzy parameter and multi-language stemming.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"collection":       map[string]interface{}{"type": "string", "description": "Collection to search in"},
					"query":            map[string]interface{}{"type": "string", "description": "Search query text"},
					"limit":            map[string]interface{}{"type": "integer", "description": "Max results (default: 50)"},
					"algorithm":        map[string]interface{}{"type": "string", "description": "Scoring algorithm: tfidf (default), bm25, bm25f, or pmisparse"},
					"fuzzy":            map[string]interface{}{"type": "integer", "description": "Typo tolerance: 0 (off, default), 1 (1 char typo), 2 (2 char typos)"},
					"lang":             map[string]interface{}{"type": "string", "description": "Language for stemming/stop words (e.g. en, pl, de, fr, es). Default: server default language"},
					"boost":            map[string]interface{}{"type": "object", "description": "Per-query score multiplier keyed by \"metaKey:metaValue\" (e.g. {\"tag:featured\":5.0,\"status:archived\":-2.0}). Positive boosts, negative demotes; combined multiplicatively."},
					"facet_by":         map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}, "description": "(v2.9.14+) Metadata keys to aggregate into response.facets (e.g. [\"category\",\"lang\"])."},
					"facet_max_values": map[string]interface{}{"type": "integer", "description": "(v2.9.14+) Cap per-key bucket count; 0 = unlimited."},
					"include_content":  map[string]interface{}{"type": "boolean", "description": mcpIncludeContentDesc},
					"fields":           map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}, "description": mcpFieldsDesc},
					"highlight":        map[string]interface{}{"type": "boolean", "description": "(v2.12.0+) Return the matching fragments with the 1-based line range each occupies (startLine/endLine). Use this to locate a passage instead of reading the document: a result becomes \"css/style.css lines 41-58\"."},
					"highlight_tag":    map[string]interface{}{"type": "string", "description": "(v2.12.0+) Tag wrapping each match in a fragment; default \"<mark>\"."},
					"max_highlights":   map[string]interface{}{"type": "integer", "description": "(v2.12.0+) Fragments per result; default 3."},
					"fragment_size":    map[string]interface{}{"type": "integer", "description": "(v2.12.0+) Approximate characters per fragment; default 150. Lower it for source, whose lines are short — 150 bytes covers roughly fifteen lines of CSS."},
				},
				"required": []string{"collection", "query"},
			},
		},
		{
			Name:        "bulk_ingest_submit",
			Description: "Queue a long-running bulk ingest job and return immediately with a job identifier. The caller should poll bulk_ingest_status or supply callback_url for a webhook notification on completion. Documents are processed in FIFO order, 500 at a time.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"collection":   map[string]interface{}{"type": "string", "description": "Target collection"},
					"documents":    map[string]interface{}{"type": "array", "description": "Documents to ingest (same shape as add-batch)", "items": map[string]interface{}{"type": "object"}},
					"callback_url": map[string]interface{}{"type": "string", "description": "Optional URL that receives a POST with the final job record on completion"},
				},
				"required": []string{"collection", "documents"},
			},
		},
		{
			Name:        "bulk_ingest_status",
			Description: "Return the current status record (counters, timestamps, up to 50 errors) for a previously-submitted bulk ingest job.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"id": map[string]interface{}{"type": "string", "description": "Job identifier returned from bulk_ingest_submit"},
				},
				"required": []string{"id"},
			},
		},
		{
			Name:        "bulk_ingest_list",
			Description: "List bulk ingest jobs newest-first, optionally filtered by collection.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"collection": map[string]interface{}{"type": "string", "description": "Optional collection filter"},
				},
			},
		},
		{
			Name:        "bulk_ingest_cancel",
			Description: "Cancel a pending bulk ingest job. Jobs that have already started processing cannot be cancelled.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"id": map[string]interface{}{"type": "string", "description": "Job identifier to cancel"},
				},
				"required": []string{"id"},
			},
		},
		{
			Name:        "fts_reindex",
			Description: "Reindex full-text search for a collection. Re-applies language-aware stemming and stop words using each document's lang field.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"collection": map[string]interface{}{"type": "string", "description": "Collection to reindex"},
				},
				"required": []string{"collection"},
			},
		},
		{
			Name:        "autocomplete",
			Description: "Prefix autocomplete — returns top-N terms starting with the query, ranked by document frequency. Reuses the FTS inverted index.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"collection": map[string]interface{}{"type": "string", "description": "Collection name"},
					"q":          map[string]interface{}{"type": "string", "description": "Prefix query (lowercased and stripped of non-alphanumerics)"},
					"field":      map[string]interface{}{"type": "string", "description": "Optional field scope (e.g. \"meta.title\", \"content\"); empty means global"},
					"top_n":      map[string]interface{}{"type": "integer", "description": "Max suggestions (default: 10)"},
				},
				"required": []string{"collection", "q"},
			},
		},
		{
			Name:        "fts_languages",
			Description: "List all supported FTS languages with their codes and names.",
			InputSchema: map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
		},
		{
			Name:        "register_webhook",
			Description: "Register a webhook to receive HTTP callbacks when documents are added, updated, or deleted.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"url":        map[string]interface{}{"type": "string", "description": "Webhook endpoint URL"},
					"events":     map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}, "description": "Events: doc.added, doc.updated, doc.deleted"},
					"collection": map[string]interface{}{"type": "string", "description": "Filter to specific collection (empty = all)"},
				},
				"required": []string{"url", "events"},
			},
		},
		{
			Name:        "list_webhooks",
			Description: "List all registered webhooks.",
			InputSchema: map[string]interface{}{"type": "object"},
		},
		{
			Name:        "delete_webhook",
			Description: "Delete a registered webhook by ID.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"id": map[string]interface{}{"type": "string", "description": "Webhook ID to delete"},
				},
				"required": []string{"id"},
			},
		},
		{
			Name:        "set_schema",
			Description: "Set JSON Schema for collection metadata validation. Documents added to this collection will be validated against the schema.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"collection": map[string]interface{}{"type": "string", "description": "Collection name"},
					"schema":     map[string]interface{}{"type": "string", "description": "JSON Schema as a string"},
				},
				"required": []string{"collection", "schema"},
			},
		},
		{
			Name:        "get_schema",
			Description: "Get JSON Schema for a collection.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"collection": map[string]interface{}{"type": "string", "description": "Collection name"},
				},
				"required": []string{"collection"},
			},
		},
		{
			Name:        "delete_schema",
			Description: "Delete/disable schema validation for a collection.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"collection": map[string]interface{}{"type": "string", "description": "Collection name"},
				},
				"required": []string{"collection"},
			},
		},
		{
			Name:        "list_schemas",
			Description: "List all collection schemas.",
			InputSchema: map[string]interface{}{"type": "object"},
		},
		{
			Name:        "validate_document",
			Description: "Validate document metadata against collection schema without adding the document.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"collection": map[string]interface{}{"type": "string", "description": "Collection name"},
					"meta":       map[string]interface{}{"type": "object", "description": "Document metadata to validate"},
				},
				"required": []string{"collection", "meta"},
			},
		},
		{
			Name:        "add_documents_batch",
			Description: "Add multiple documents to a collection in a single batch operation.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"collection": map[string]interface{}{"type": "string", "description": "Collection name"},
					"documents":  map[string]interface{}{"type": "array", "description": "Array of documents with key, lang, content_md, meta fields"},
				},
				"required": []string{"collection", "documents"},
			},
		},
		{
			Name:        "delete_documents_batch",
			Description: "Delete multiple documents from a collection in a single batch operation.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"collection": map[string]interface{}{"type": "string", "description": "Collection name"},
					"documents":  map[string]interface{}{"type": "array", "description": "Array of documents with key and lang fields"},
				},
				"required": []string{"collection", "documents"},
			},
		},
		{
			Name:        "export_documents",
			Description: "Export documents from a collection in NDJSON format.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"collection":  map[string]interface{}{"type": "string", "description": "Collection to export"},
					"filter_meta": map[string]interface{}{"type": "object", "description": "Optional metadata filter"},
					"format":      map[string]interface{}{"type": "string", "description": "Export format: ndjson (default) or zip"},
				},
				"required": []string{"collection"},
			},
		},
		{
			Name:        "create_backup",
			Description: "Create a backup of the MDDB database.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"to": map[string]interface{}{"type": "string", "description": "Backup destination path"},
				},
			},
		},
		{
			Name:        "restore_backup",
			Description: "Restore the MDDB database from a backup file.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"from": map[string]interface{}{"type": "string", "description": "Backup file path to restore from"},
				},
				"required": []string{"from"},
			},
		},
		{
			Name:        "update_document",
			Description: "Partially update a document. Update metadata and/or content independently without re-sending the entire document. Omit fields to leave them unchanged.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"collection": map[string]interface{}{"type": "string", "description": "Collection name"},
					"key":        map[string]interface{}{"type": "string", "description": "Document key"},
					"lang":       map[string]interface{}{"type": "string", "description": "Language code"},
					"meta":       map[string]interface{}{"type": "object", "description": "New metadata (replaces all). Use {} to clear."},
					"content_md": map[string]interface{}{"type": "string", "description": "New markdown content (replaces existing)"},
					"ttl":        map[string]interface{}{"type": "integer", "description": "New TTL in seconds (0 = no expiry)"},
				},
				"required": []string{"collection", "key", "lang"},
			},
		},
		{
			Name:        "get_document_meta",
			Description: "Get document metadata without content. Lightweight read that returns only key, lang, meta, and timestamps.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"collection": map[string]interface{}{"type": "string", "description": "Collection name"},
					"key":        map[string]interface{}{"type": "string", "description": "Document key"},
					"lang":       map[string]interface{}{"type": "string", "description": "Language code"},
				},
				"required": []string{"collection", "key", "lang"},
			},
		},
		{
			Name:        "classify_document",
			Description: "Zero-shot document classification. Given candidate labels and either a document reference or raw text, ranks labels by semantic similarity using embeddings. No training data required.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"collection": map[string]interface{}{"type": "string", "description": "Collection name (for doc reference)"},
					"key":        map[string]interface{}{"type": "string", "description": "Document key (for doc reference)"},
					"lang":       map[string]interface{}{"type": "string", "description": "Language code (for doc reference)"},
					"text":       map[string]interface{}{"type": "string", "description": "Raw text to classify (alternative to doc reference)"},
					"labels":     map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}, "description": "Candidate labels to rank by similarity"},
					"top_k":      map[string]interface{}{"type": "integer", "description": "Return top K labels (0 = all, default: all)"},
					"multi":      map[string]interface{}{"type": "boolean", "description": "If true, return all labels above threshold"},
					"threshold":  map[string]interface{}{"type": "number", "description": "Minimum similarity score (default: 0.0)"},
				},
				"required": []string{"labels"},
			},
		},
		{
			Name:        "geo_search",
			Description: "Find documents within a given radius (in meters) of a latitude/longitude point. Documents must have geo_lat+geo_lng, geo_hash, or a resolvable geo_postcode+geo_country in their metadata. Results are sorted by ascending distance. Use this for 'nearest venues', 'places within 5km' style queries.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"collection":    map[string]interface{}{"type": "string", "description": "Collection to search"},
					"lat":           map[string]interface{}{"type": "number", "description": "Query latitude in decimal degrees (-90..90)"},
					"lng":           map[string]interface{}{"type": "number", "description": "Query longitude in decimal degrees (-180..180)"},
					"radius_meters": map[string]interface{}{"type": "number", "description": "Search radius in meters (max 50_000_000)"},
					"top_k":         map[string]interface{}{"type": "integer", "description": "Max results to return (default: 10)"},
					"algorithm":     map[string]interface{}{"type": "string", "description": "Index algorithm: rtree (default, best for general use) or geohash (alternative, prefix-based)"},
					"filter_meta":   map[string]interface{}{"type": "object", "description": "Optional metadata filter combined with the spatial query"},
				},
				"required": []string{"collection", "lat", "lng", "radius_meters"},
			},
		},
		{
			Name:        "geo_within",
			Description: "Find documents inside an axis-aligned bounding box (minLat..maxLat × minLng..maxLng). Does not cross the anti-meridian — split the query into two halves if needed. Returns results in no particular order.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"collection":  map[string]interface{}{"type": "string", "description": "Collection to search"},
					"min_lat":     map[string]interface{}{"type": "number", "description": "South edge of the bbox"},
					"max_lat":     map[string]interface{}{"type": "number", "description": "North edge of the bbox"},
					"min_lng":     map[string]interface{}{"type": "number", "description": "West edge of the bbox"},
					"max_lng":     map[string]interface{}{"type": "number", "description": "East edge of the bbox"},
					"filter_meta": map[string]interface{}{"type": "object", "description": "Optional metadata filter"},
				},
				"required": []string{"collection", "min_lat", "max_lat", "min_lng", "max_lng"},
			},
		},
		{
			Name:        "geo_polygon",
			Description: "Find documents whose coordinates fall inside a GeoJSON Polygon (outer ring + optional holes) or MultiPolygon (union of polygons). Exactly one of `polygon` or `multi_polygon` must be set. GeoJSON coordinate order is [lng, lat]. Ring must have at least 3 points; the first and last may be equal (closed) or not — both forms are accepted.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"collection":      map[string]interface{}{"type": "string", "description": "Collection to search"},
					"polygon":         map[string]interface{}{"type": "object", "description": "GeoJSON Polygon: {type:\"Polygon\", coordinates:[[[lng,lat],…]]}. First ring = outer boundary; subsequent rings = holes."},
					"multi_polygon":   map[string]interface{}{"type": "object", "description": "GeoJSON MultiPolygon: {type:\"MultiPolygon\", coordinates:[[[[lng,lat],…]],…]}. Union semantics — a doc matches if it's inside any member polygon."},
					"filter_meta":     map[string]interface{}{"type": "object", "description": "Optional metadata filter"},
					"include_content": map[string]interface{}{"type": "boolean", "description": "Include full contentMd in each returned doc (default false)"},
				},
				"required": []string{"collection"},
			},
		},
		{
			Name:        "geo_stats",
			Description: "Report per-collection indexed-point counts plus any loaded postcode datasets. Use to confirm that a collection is geo-enabled before running geo_search.",
			InputSchema: map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
		},
		{
			Name:        "geo_encode",
			Description: "Convert a (lat, lng) pair into a geohash string of the requested precision (1..12, default 12). Useful when writing document metadata with a geo_hash field.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"lat":       map[string]interface{}{"type": "number", "description": "Latitude in decimal degrees"},
					"lng":       map[string]interface{}{"type": "number", "description": "Longitude in decimal degrees"},
					"precision": map[string]interface{}{"type": "integer", "description": "Geohash length (1..12, default 12)"},
				},
				"required": []string{"lat", "lng"},
			},
		},
		{
			Name:        "geo_decode",
			Description: "Convert a geohash string back to the (lat, lng) centroid of its cell.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"geohash": map[string]interface{}{"type": "string", "description": "Geohash to decode (case-insensitive)"},
				},
				"required": []string{"geohash"},
			},
		},
		{
			Name:        "hybrid_search",
			Description: "Combined sparse (FTS) + dense (vector) search with alpha blending or Reciprocal Rank Fusion. Requires both FTS index and embedding provider.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"collection":       map[string]interface{}{"type": "string", "description": "Collection to search"},
					"query":            map[string]interface{}{"type": "string", "description": "Search query (used for both FTS and embedding)"},
					"top_k":            map[string]interface{}{"type": "integer", "description": "Number of results (default: 10)"},
					"algorithm":        map[string]interface{}{"type": "string", "description": "FTS algorithm: bm25, bm25f (default: bm25)"},
					"vector_algorithm": map[string]interface{}{"type": "string", "description": "Vector algorithm: flat, hnsw, ivf, pq (default: flat)"},
					"strategy":         map[string]interface{}{"type": "string", "description": "Fusion strategy: alpha or rrf (default: alpha)"},
					"alpha":            map[string]interface{}{"type": "number", "description": "Alpha weight 0-1 (0=keyword, 1=semantic, default: 0.5)"},
					"rrf_k":            map[string]interface{}{"type": "integer", "description": "RRF k parameter (default: 60)"},
					"fuzzy":            map[string]interface{}{"type": "integer", "description": "Typo tolerance: 0, 1, 2 (default: 0)"},
					"threshold":        map[string]interface{}{"type": "number", "description": "Min vector similarity 0-1"},
					"distance_metric":  map[string]interface{}{"type": "string", "description": "Distance metric: cosine (default), dot_product, euclidean"},
					"filter_meta":      map[string]interface{}{"type": "object", "description": "Metadata filter"},
					"boost":            map[string]interface{}{"type": "object", "description": "Per-query score multiplier keyed by \"metaKey:metaValue\" (e.g. {\"tag:featured\":5.0,\"status:archived\":-2.0}). Positive boosts, negative demotes; combined multiplicatively."},
					"sort":             map[string]interface{}{"type": "string", "description": "Result ordering: \"combined\" (default, by fused score) or \"distance\" (by proximity ascending — requires a geo filter on the HTTP path)"},
					"facet_by":         map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}, "description": "(v2.9.14+) Metadata keys to aggregate into response.facets."},
					"facet_max_values": map[string]interface{}{"type": "integer", "description": "(v2.9.14+) Cap per-key bucket count; 0 = unlimited."},
				},
				"required": []string{"collection", "query"},
			},
		},
		{
			Name:        "delete_collection",
			Description: "Delete an entire collection and all its documents, revisions, and metadata indices.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"collection": map[string]interface{}{"type": "string", "description": "Collection name to delete"},
				},
				"required": []string{"collection"},
			},
		},
		{
			Name:        "truncate_revisions",
			Description: "Truncate revision history for a collection, keeping only the N most recent revisions per document.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"collection": map[string]interface{}{"type": "string", "description": "Collection name"},
					"keep_revs":  map[string]interface{}{"type": "integer", "description": "Number of revisions to keep (0 = delete all history)"},
					"drop_cache": map[string]interface{}{"type": "boolean", "description": "Clear cache after truncation"},
				},
				"required": []string{"collection", "keep_revs"},
			},
		},
		{
			Name:        "list_synonyms",
			Description: "List all synonym entries for a collection. Synonyms expand FTS queries with related terms.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"collection": map[string]interface{}{"type": "string", "description": "Collection name"},
				},
				"required": []string{"collection"},
			},
		},
		{
			Name:        "add_synonym",
			Description: "Add or update a synonym group for a term. Synonyms are bidirectional in FTS queries.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"collection": map[string]interface{}{"type": "string", "description": "Collection name"},
					"term":       map[string]interface{}{"type": "string", "description": "Base term"},
					"synonyms":   map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}, "description": "List of synonyms"},
				},
				"required": []string{"collection", "term", "synonyms"},
			},
		},
		{
			Name:        "delete_synonym",
			Description: "Delete a synonym group for a term in a collection.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"collection": map[string]interface{}{"type": "string", "description": "Collection name"},
					"term":       map[string]interface{}{"type": "string", "description": "Term to remove synonyms for"},
				},
				"required": []string{"collection", "term"},
			},
		},
		{
			Name:        "list_stopwords",
			Description: "List all stop words (default + custom) for a collection. Stop words are excluded from FTS indexing.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"collection": map[string]interface{}{"type": "string", "description": "Collection name"},
				},
				"required": []string{"collection"},
			},
		},
		{
			Name:        "add_stopwords",
			Description: "Add custom stop words to a collection's FTS index.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"collection": map[string]interface{}{"type": "string", "description": "Collection name"},
					"words":      map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}, "description": "Words to add as stop words"},
				},
				"required": []string{"collection", "words"},
			},
		},
		{
			Name:        "delete_stopwords",
			Description: "Remove custom stop words from a collection.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"collection": map[string]interface{}{"type": "string", "description": "Collection name"},
					"words":      map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}, "description": "Words to remove"},
				},
				"required": []string{"collection", "words"},
			},
		},
		{
			Name:        "get_meta_keys",
			Description: "List all unique metadata keys and their values for a collection. Useful for discovering available filter options.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"collection": map[string]interface{}{"type": "string", "description": "Collection name"},
				},
				"required": []string{"collection"},
			},
		},
		{
			Name:        "get_checksum",
			Description: "Get a CRC32 checksum for a collection that changes when documents are modified. Useful for cache invalidation.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"collection": map[string]interface{}{"type": "string", "description": "Collection name"},
				},
				"required": []string{"collection"},
			},
		},
		{
			Name:        "list_automation",
			Description: "List all automation rules (webhooks, triggers, crons). Optionally filter by type.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"type": map[string]interface{}{"type": "string", "description": "Filter by rule type: webhook, trigger, or cron"},
				},
			},
		},
		{
			Name:        "create_automation",
			Description: "Create a new automation rule (webhook target, search trigger, or cron schedule).",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"type":       map[string]interface{}{"type": "string", "description": "Rule type: webhook, trigger, or cron"},
					"name":       map[string]interface{}{"type": "string", "description": "Rule name"},
					"enabled":    map[string]interface{}{"type": "boolean", "description": "Whether rule is enabled (default: true)"},
					"url":        map[string]interface{}{"type": "string", "description": "Webhook URL (type=webhook)"},
					"method":     map[string]interface{}{"type": "string", "description": "HTTP method POST/GET/PUT (type=webhook)"},
					"collection": map[string]interface{}{"type": "string", "description": "Target collection (type=trigger)"},
					"searchType": map[string]interface{}{"type": "string", "description": "Search type: fts, vector, hybrid (type=trigger)"},
					"query":      map[string]interface{}{"type": "string", "description": "Search query (type=trigger)"},
					"threshold":  map[string]interface{}{"type": "number", "description": "Score threshold 0-100 (type=trigger)"},
					"webhookId":  map[string]interface{}{"type": "string", "description": "Target webhook ID (type=trigger/cron)"},
					"events":     map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}, "description": "Events: insert, update, delete (type=trigger)"},
					"schedule":   map[string]interface{}{"type": "string", "description": "Cron expression (type=cron)"},
					"triggerId":  map[string]interface{}{"type": "string", "description": "Target trigger ID (type=cron)"},
				},
				"required": []string{"type", "name"},
			},
		},
		{
			Name:        "get_automation",
			Description: "Get a specific automation rule by ID.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"id": map[string]interface{}{"type": "string", "description": "Rule ID"},
				},
				"required": []string{"id"},
			},
		},
		{
			Name:        "update_automation",
			Description: "Update an existing automation rule.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"id":         map[string]interface{}{"type": "string", "description": "Rule ID to update"},
					"name":       map[string]interface{}{"type": "string", "description": "Updated name"},
					"enabled":    map[string]interface{}{"type": "boolean", "description": "Enable/disable"},
					"url":        map[string]interface{}{"type": "string", "description": "Updated webhook URL"},
					"collection": map[string]interface{}{"type": "string", "description": "Updated collection"},
					"query":      map[string]interface{}{"type": "string", "description": "Updated query"},
					"threshold":  map[string]interface{}{"type": "number", "description": "Updated threshold"},
					"schedule":   map[string]interface{}{"type": "string", "description": "Updated cron schedule"},
				},
				"required": []string{"id"},
			},
		},
		{
			Name:        "delete_automation",
			Description: "Delete an automation rule by ID.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"id": map[string]interface{}{"type": "string", "description": "Rule ID to delete"},
				},
				"required": []string{"id"},
			},
		},
		{
			Name:        "test_automation",
			Description: "Test a trigger rule by running its search and returning matches (dry run, no webhook fired).",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"id": map[string]interface{}{"type": "string", "description": "Trigger rule ID to test"},
				},
				"required": []string{"id"},
			},
		},
		{
			Name:        "get_automation_logs",
			Description: "List automation execution logs with optional filtering by rule ID and status.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"limit":   map[string]interface{}{"type": "integer", "description": "Max entries (default: 50)"},
					"cursor":  map[string]interface{}{"type": "string", "description": "Pagination cursor"},
					"rule_id": map[string]interface{}{"type": "string", "description": "Filter by rule ID"},
					"status":  map[string]interface{}{"type": "string", "description": "Filter by status: success, error, skipped"},
				},
			},
		},
	}
}

// mcpBuiltinToolsAdvanced lists the collection-config, curation, cross-search,
// ingest, upload, revisions, duplicate-detection and memory-RAG builtin tools.
func mcpBuiltinToolsAdvanced() []MCPTool {
	return []MCPTool{
		// --- Collection Config ---
		{
			Name:        "get_collection_config",
			Description: "Get configuration attributes for a collection (type, description, icon, color, custom metadata).",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"collection": map[string]interface{}{"type": "string", "description": "Collection name"},
				},
				"required": []string{"collection"},
			},
		},
		{
			Name:        "set_collection_config",
			Description: "Set or update configuration attributes for a collection.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"collection":    map[string]interface{}{"type": "string", "description": "Collection name"},
					"type":          map[string]interface{}{"type": "string", "description": "Collection type: default, website, images, audio, documents"},
					"description":   map[string]interface{}{"type": "string", "description": "Human-readable description"},
					"icon":          map[string]interface{}{"type": "string", "description": "Emoji or icon identifier"},
					"color":         map[string]interface{}{"type": "string", "description": "Hex color code (e.g. #3B82F6)"},
					"custom_meta":   map[string]interface{}{"type": "object", "description": "Custom key-value metadata"},
					"max_revisions": map[string]interface{}{"type": "integer", "description": "(v2.9.14+) Keep last N revisions per document. 0 (default) = unlimited."},
					"wordpress": map[string]interface{}{
						"type":        "object",
						"description": "(v2.11.0+) WordPress publishing target for wordpress_publish / wordpress_set_status: {url, api_key}. url = site base URL (https), api_key = mddb-sync plugin publish key.",
						"properties": map[string]interface{}{
							"url":     map[string]interface{}{"type": "string", "description": "Site base URL, e.g. https://blog.example.com"},
							"api_key": map[string]interface{}{"type": "string", "description": "mddb-sync publish key (Settings → MDDB Sync)"},
						},
					},
				},
				"required": []string{"collection"},
			},
		},
		{
			Name:        "list_collection_configs",
			Description: "List all collections that have custom configuration set.",
			InputSchema: map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
		},
		// --- Curation rules (v2.9.14+) ---
		{
			Name:        "list_curation_rules",
			Description: "(v2.9.14+) List curation rules that pin or hide documents for specific search queries. Pass collection to filter; omit for all.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"collection": map[string]interface{}{"type": "string", "description": "Optional — limit to rules for this collection"},
				},
			},
		},
		{
			Name:        "create_curation_rule",
			Description: "(v2.9.14+) Create a rule that pins specific documents to fixed positions or hides others when a query matches.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"collection": map[string]interface{}{"type": "string", "description": "Target collection"},
					"query":      map[string]interface{}{"type": "string", "description": "Trigger query — matched against incoming search requests"},
					"match_mode": map[string]interface{}{"type": "string", "description": "exact (default) or contains"},
					"pins": map[string]interface{}{
						"type":        "array",
						"description": "Documents to pin. Each item: {key, lang?, position} — position is 1-based.",
						"items": map[string]interface{}{
							"type": "object",
							"properties": map[string]interface{}{
								"key":      map[string]interface{}{"type": "string"},
								"lang":     map[string]interface{}{"type": "string"},
								"position": map[string]interface{}{"type": "integer"},
							},
						},
					},
					"hides":   map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}, "description": "Document keys to drop from results"},
					"enabled": map[string]interface{}{"type": "boolean", "description": "Default: true"},
				},
				"required": []string{"collection", "query"},
			},
		},
		{
			Name:        "update_curation_rule",
			Description: "(v2.9.14+) Replace an existing curation rule by id. Accepts the same body as create_curation_rule plus an id.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"id":         map[string]interface{}{"type": "string", "description": "Rule id"},
					"collection": map[string]interface{}{"type": "string"},
					"query":      map[string]interface{}{"type": "string"},
					"match_mode": map[string]interface{}{"type": "string"},
					"pins":       map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "object"}},
					"hides":      map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}},
					"enabled":    map[string]interface{}{"type": "boolean"},
				},
				"required": []string{"id"},
			},
		},
		{
			Name:        "delete_curation_rule",
			Description: "(v2.9.14+) Remove a curation rule by id.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"id": map[string]interface{}{"type": "string", "description": "Rule id"},
				},
				"required": []string{"id"},
			},
		},
		// --- Cross-Collection Search ---
		{
			Name:        "cross_search",
			Description: "Search across multiple collections using a source document's embedding or a text query. Useful for finding related content across different collection types (e.g. matching images to blog posts).",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"source_collection":  map[string]interface{}{"type": "string", "description": "Collection containing the source document"},
					"source_doc_id":      map[string]interface{}{"type": "string", "description": "Source document ID whose embedding to use as query"},
					"query":              map[string]interface{}{"type": "string", "description": "Text query (alternative to source_doc_id)"},
					"target_collections": map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}, "description": "Collections to search in"},
					"top_k":              map[string]interface{}{"type": "integer", "description": "Number of results (default: 10)"},
					"threshold":          map[string]interface{}{"type": "number", "description": "Minimum similarity threshold 0-1"},
					"algorithm":          map[string]interface{}{"type": "string", "description": "Vector algorithm: flat (default), hnsw, ivf, pq, sq, bq"},
					"distance_metric":    map[string]interface{}{"type": "string", "description": "Distance metric: cosine (default), dot_product, euclidean"},
					"include_content":    map[string]interface{}{"type": "boolean", "description": "Include document content in results"},
				},
				"required": []string{"target_collections"},
			},
		},

		// --- Bulk Ingest ---
		{
			Name:        "ingest_documents",
			Description: "Bulk ingest documents with URL-based key derivation, YAML frontmatter extraction, content deduplication, and automatic metadata injection. Optimized for scraping and ETL pipelines.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"collection": map[string]interface{}{"type": "string", "description": "Target collection"},
					"documents": map[string]interface{}{
						"type":        "array",
						"description": "Array of documents to ingest",
						"items": map[string]interface{}{
							"type": "object",
							"properties": map[string]interface{}{
								"url":                 map[string]interface{}{"type": "string", "description": "Source URL (key derived from URL if key is empty)"},
								"key":                 map[string]interface{}{"type": "string", "description": "Document key (optional if url is provided)"},
								"lang":                map[string]interface{}{"type": "string", "description": "Language code (e.g. en, pl)"},
								"content_md":          map[string]interface{}{"type": "string", "description": "Markdown content"},
								"meta":                map[string]interface{}{"type": "object", "description": "Metadata key-value pairs"},
								"extract_frontmatter": map[string]interface{}{"type": "boolean", "description": "Parse YAML frontmatter from content"},
								"scraped_at":          map[string]interface{}{"type": "integer", "description": "Unix timestamp of when content was collected"},
								"scraper":             map[string]interface{}{"type": "string", "description": "Source identifier"},
								"ttl":                 map[string]interface{}{"type": "integer", "description": "Time-to-live in seconds"},
							},
							"required": []string{"lang", "content_md"},
						},
					},
					"options": map[string]interface{}{
						"type":        "object",
						"description": "Ingest options",
						"properties": map[string]interface{}{
							"skip_duplicates":           map[string]interface{}{"type": "boolean", "description": "Skip documents whose content matches existing (CRC32 hash)"},
							"skip_embeddings":           map[string]interface{}{"type": "boolean", "description": "Skip embedding generation"},
							"skip_fts":                  map[string]interface{}{"type": "boolean", "description": "Skip full-text indexing"},
							"skip_webhooks":             map[string]interface{}{"type": "boolean", "description": "Skip webhook firing"},
							"auto_configure_collection": map[string]interface{}{"type": "boolean", "description": "Auto-set collection type to 'scraping'"},
							"save_revision":             map[string]interface{}{"type": "boolean", "description": "Save revision history for each document"},
						},
					},
				},
				"required": []string{"collection", "documents"},
			},
		},

		// --- File Upload ---
		{
			Name:        "upload_file",
			Description: "Upload a file and convert it to markdown. Supports md, txt, html, pdf, and docx formats. Plain text and markdown files are stored as-is; other formats are auto-converted to markdown. File content is passed as base64-encoded string.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"collection": map[string]interface{}{"type": "string", "description": "Target collection"},
					"filename":   map[string]interface{}{"type": "string", "description": "Original filename with extension (e.g. report.pdf). Extension determines conversion format."},
					"content":    map[string]interface{}{"type": "string", "description": "Base64-encoded file content"},
					"key":        map[string]interface{}{"type": "string", "description": "Document key (optional, derived from filename if empty)"},
					"lang":       map[string]interface{}{"type": "string", "description": "Language code (e.g. en_US, pl_PL)"},
					"meta":       map[string]interface{}{"type": "object", "description": "Metadata key-value pairs"},
					"ttl":        map[string]interface{}{"type": "integer", "description": "Time-to-live in seconds (0 = no expiry)"},
				},
				"required": []string{"collection", "filename", "content", "lang"},
			},
		},

		// --- Revisions ---
		{
			Name:        "list_revisions",
			Description: "List revision history for a document. Shows all saved versions with timestamps.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"collection": map[string]interface{}{"type": "string", "description": "Collection name"},
					"key":        map[string]interface{}{"type": "string", "description": "Document key"},
					"lang":       map[string]interface{}{"type": "string", "description": "Language code"},
				},
				"required": []string{"collection", "key", "lang"},
			},
		},
		{
			Name:        "restore_revision",
			Description: "Restore a document to a previous revision by timestamp.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"collection": map[string]interface{}{"type": "string", "description": "Collection name"},
					"key":        map[string]interface{}{"type": "string", "description": "Document key"},
					"lang":       map[string]interface{}{"type": "string", "description": "Language code"},
					"timestamp":  map[string]interface{}{"type": "integer", "description": "Unix timestamp of the revision to restore"},
				},
				"required": []string{"collection", "key", "lang", "timestamp"},
			},
		},

		// --- Duplicate Detection ---
		{
			Name:        "find_duplicates",
			Description: "Find duplicate and similar documents within a collection. Detects exact duplicates (same content hash) and semantically similar documents (above similarity threshold). Requires documents to have embeddings for similar mode.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"collection":      map[string]interface{}{"type": "string", "description": "Collection to scan for duplicates"},
					"mode":            map[string]interface{}{"type": "string", "description": "Detection mode: exact, similar, or both (default: both)"},
					"threshold":       map[string]interface{}{"type": "number", "description": "Similarity threshold 0-1 for similar mode (default: 0.9)"},
					"max_docs":        map[string]interface{}{"type": "integer", "description": "Max documents to process (default: 5000)"},
					"distance_metric": map[string]interface{}{"type": "string", "description": "Distance metric: cosine (default), dot_product, euclidean"},
					"include_content": map[string]interface{}{"type": "boolean", "description": "Include document content in results"},
				},
				"required": []string{"collection"},
			},
		},

		// --- Memory RAG ---
		{
			Name:        "memory_start_session",
			Description: "Start a new memory/conversation session for RAG. Returns a session ID that can be used to add messages and recall context later.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"user_id":  map[string]interface{}{"type": "string", "description": "User identifier for the session"},
					"scenario": map[string]interface{}{"type": "string", "description": "Scenario or context name (e.g. 'customer_support', 'code_review')"},
					"title":    map[string]interface{}{"type": "string", "description": "Human-readable session title"},
					"meta":     map[string]interface{}{"type": "object", "description": "Additional metadata key-value pairs"},
					"ttl":      map[string]interface{}{"type": "integer", "description": "Session TTL in seconds (default: 30 days)"},
				},
				"required": []string{"user_id"},
			},
		},
		{
			Name:        "memory_add_message",
			Description: "Add a message to an existing memory session. Messages are automatically embedded for semantic recall.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"session_id": map[string]interface{}{"type": "string", "description": "Session ID returned from memory_start_session"},
					"role":       map[string]interface{}{"type": "string", "description": "Message role: user, assistant, system, or tool"},
					"content":    map[string]interface{}{"type": "string", "description": "Message content (markdown supported)"},
					"meta":       map[string]interface{}{"type": "object", "description": "Additional metadata (e.g. topic, source, tool_call)"},
				},
				"required": []string{"session_id", "role", "content"},
			},
		},
		{
			Name:        "memory_recall",
			Description: "Semantically recall relevant messages from past conversations. Uses hybrid search (vector + keyword) to find the most relevant context across all sessions or filtered by user/session.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"query":           map[string]interface{}{"type": "string", "description": "Natural language query to recall relevant context"},
					"user_id":         map[string]interface{}{"type": "string", "description": "Filter recall to sessions belonging to this user"},
					"session_id":      map[string]interface{}{"type": "string", "description": "Filter recall to a specific session"},
					"role":            map[string]interface{}{"type": "string", "description": "Filter by message role (user, assistant, system, tool)"},
					"top_k":           map[string]interface{}{"type": "integer", "description": "Number of results (default: 10)"},
					"threshold":       map[string]interface{}{"type": "number", "description": "Min similarity score 0-1 (default: 0.0)"},
					"strategy":        map[string]interface{}{"type": "string", "description": "Search strategy: hybrid (default), semantic, keyword"},
					"include_content": map[string]interface{}{"type": "boolean", "description": "Include full message content (default: false)"},
				},
				"required": []string{"query"},
			},
		},
		{
			Name:        "memory_summarize",
			Description: "Generate a summary of a conversation session. Stores the summary as a document with embeddings for future recall.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"session_id": map[string]interface{}{"type": "string", "description": "Session ID to summarize"},
					"user_id":    map[string]interface{}{"type": "string", "description": "User ID for validation"},
				},
				"required": []string{"session_id"},
			},
		},
		{
			Name:        "memory_list_sessions",
			Description: "List memory/conversation sessions with optional filtering by user, scenario, and sorting.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"user_id":  map[string]interface{}{"type": "string", "description": "Filter sessions by user ID"},
					"scenario": map[string]interface{}{"type": "string", "description": "Filter sessions by scenario"},
					"limit":    map[string]interface{}{"type": "integer", "description": "Max results (default: 50)"},
					"offset":   map[string]interface{}{"type": "integer", "description": "Results offset for pagination"},
					"sort":     map[string]interface{}{"type": "string", "description": "Sort by: createdAt (default), updatedAt"},
				},
			},
		},
		{
			Name:        "memory_session_history",
			Description: "Get the full message history of a specific conversation session, ordered chronologically.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"session_id": map[string]interface{}{"type": "string", "description": "Session ID to fetch history for"},
					"limit":      map[string]interface{}{"type": "integer", "description": "Max messages (default: 100)"},
					"offset":     map[string]interface{}{"type": "integer", "description": "Message offset for pagination"},
				},
				"required": []string{"session_id"},
			},
		},
		// --- WordPress publishing (v2.11.0+) ---
		{
			Name:        "wordpress_publish",
			Description: "(v2.11.0+) Create or update a WordPress post/page via the mddb-sync plugin, including tags, categories, custom taxonomies, meta fields and Polylang/WPML language assignment. Upserts by post_id, else by post_type + slug. The target site comes from the collection's wordpress config (set_collection_config) or explicit site_url/api_key.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"collection":       map[string]interface{}{"type": "string", "description": "Collection whose config holds the WordPress target (wordpress {url, api_key})"},
					"site_url":         map[string]interface{}{"type": "string", "description": "Override: WordPress site base URL (https). Skips the collection config lookup."},
					"api_key":          map[string]interface{}{"type": "string", "description": "Override: mddb-sync publish key for site_url"},
					"post_type":        map[string]interface{}{"type": "string", "description": "post (default), page, or any post type allowed in the plugin settings"},
					"post_id":          map[string]interface{}{"type": "integer", "description": "Existing post ID to update; omit to create (or upsert by slug)"},
					"slug":             map[string]interface{}{"type": "string", "description": "URL slug; with no post_id an existing post with this slug is updated"},
					"title":            map[string]interface{}{"type": "string", "description": "Post title (required when creating)"},
					"content_markdown": map[string]interface{}{"type": "string", "description": "Body as Markdown (converted to HTML by the plugin); ignored when content_html is set"},
					"content_html":     map[string]interface{}{"type": "string", "description": "Body as HTML (sanitised by the plugin)"},
					"excerpt":          map[string]interface{}{"type": "string", "description": "Optional excerpt"},
					"status":           map[string]interface{}{"type": "string", "description": "publish, draft (default), pending, private, future"},
					"date":             map[string]interface{}{"type": "string", "description": "ISO 8601 publish date; required for status=future"},
					"author":           map[string]interface{}{"type": "integer", "description": "WordPress author user ID"},
					"tags":             map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}, "description": "Tag names (created when missing)"},
					"categories":       map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}, "description": "Category names (created when missing)"},
					"taxonomies":       map[string]interface{}{"type": "object", "description": "Custom taxonomies: {taxonomy: [term names]}"},
					"meta":             map[string]interface{}{"type": "object", "description": "Post meta fields: {key: value} (scalars or arrays)"},
					"lang":             map[string]interface{}{"type": "string", "description": "Language locale or slug (pl_PL / pl) — assigned via Polylang or WPML"},
					"translation_of":   map[string]interface{}{"type": "integer", "description": "Post ID this post is a translation of (linked via Polylang/WPML)"},
				},
			},
		},
		{
			Name:        "wordpress_set_status",
			Description: "(v2.11.0+) Change the publishing status of a WordPress post/page (publish, draft, pending, private, future, trash) via the mddb-sync plugin. Identify the post by post_id or post_type + slug.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"collection": map[string]interface{}{"type": "string", "description": "Collection whose config holds the WordPress target (wordpress {url, api_key})"},
					"site_url":   map[string]interface{}{"type": "string", "description": "Override: WordPress site base URL (https). Skips the collection config lookup."},
					"api_key":    map[string]interface{}{"type": "string", "description": "Override: mddb-sync publish key for site_url"},
					"post_id":    map[string]interface{}{"type": "integer", "description": "Post ID to change"},
					"slug":       map[string]interface{}{"type": "string", "description": "Alternative to post_id: post slug"},
					"post_type":  map[string]interface{}{"type": "string", "description": "Post type for slug lookup (default: post)"},
					"status":     map[string]interface{}{"type": "string", "description": "publish, draft, pending, private, future, trash"},
					"date":       map[string]interface{}{"type": "string", "description": "ISO 8601 date; required for status=future"},
				},
				"required": []string{"status"},
			},
		},
	}
}
