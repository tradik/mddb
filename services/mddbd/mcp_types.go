package main

import (
	"context"
	"io"
	"mddb/internal/automationlog"
	"mddb/internal/fts"
	"mddb/internal/storage"
	"time"
)

// MCPDocument represents a markdown document in MCP format.
type MCPDocument struct {
	ID        string              `json:"id"`
	Key       string              `json:"key"`
	Lang      string              `json:"lang"`
	Meta      map[string][]string `json:"meta"`
	ContentMD string              `json:"content_md"`
	AddedAt   time.Time           `json:"added_at"`
	UpdatedAt time.Time           `json:"updated_at"`
}

// MCPHealth represents server health status.
type MCPHealth struct {
	Status string `json:"status"`
	Mode   string `json:"mode"`
}

// MCPStats represents server statistics.
type MCPStats struct {
	DatabasePath     string               `json:"database_path"`
	DatabaseSize     int64                `json:"database_size"`
	Mode             string               `json:"mode"`
	Collections      []MCPCollectionStats `json:"collections"`
	TotalDocuments   int                  `json:"total_documents"`
	TotalRevisions   int                  `json:"total_revisions"`
	TotalMetaIndices int                  `json:"total_meta_indices"`
}

// MCPCollectionStats represents collection statistics.
type MCPCollectionStats struct {
	Name           string `json:"name"`
	DocumentCount  int    `json:"document_count"`
	RevisionCount  int    `json:"revision_count"`
	MetaIndexCount int    `json:"meta_index_count"`
}

// MCPAddRequest represents request to add/update a document.
type MCPAddRequest struct {
	Collection   string              `json:"collection"`
	Key          string              `json:"key"`
	Lang         string              `json:"lang"`
	Meta         map[string][]string `json:"meta"`
	ContentMD    string              `json:"content_md"`
	SaveRevision bool                `json:"save_revision"`
}

// MCPGetRequest represents request to get a document.
type MCPGetRequest struct {
	Collection string            `json:"collection"`
	Key        string            `json:"key"`
	Lang       string            `json:"lang"`
	Env        map[string]string `json:"env,omitempty"`
}

// MCPSearchRequest represents search request.
type MCPSearchRequest struct {
	Collection string              `json:"collection"`
	FilterMeta map[string][]string `json:"filter_meta,omitempty"`
	Sort       string              `json:"sort,omitempty"`
	Asc        bool                `json:"asc,omitempty"`
	Limit      int                 `json:"limit,omitempty"`
	Offset     int                 `json:"offset,omitempty"`
	// IncludeContent=false drops each document's body at the store layer so
	// projection doesn't pay for content it discards (GO-022). Callers that
	// want bodies must set it explicitly (the tool layer defaults to true).
	IncludeContent bool `json:"includeContent,omitempty"`
}

// MCPSearchResponse represents search result.
type MCPSearchResponse struct {
	Documents []MCPDocument `json:"documents"`
	Total     int           `json:"total"`
}

// MCPDeleteRequest represents request to delete a document.
type MCPDeleteRequest struct {
	Collection string `json:"collection"`
	Key        string `json:"key"`
	Lang       string `json:"lang"`
}

// MCPDeleteCollectionRequest represents request to delete a collection.
type MCPDeleteCollectionRequest struct {
	Collection string `json:"collection"`
}

// MCPDeleteCollectionResponse represents result of collection deletion.
type MCPDeleteCollectionResponse struct {
	Deleted int `json:"deleted"`
}

// MCPBatchDocument represents a document in batch operation.
type MCPBatchDocument struct {
	Key          string              `json:"key"`
	Lang         string              `json:"lang"`
	Meta         map[string][]string `json:"meta"`
	ContentMD    string              `json:"content_md"`
	SaveRevision bool                `json:"save_revision"`
}

// MCPAddBatchRequest represents request to add multiple documents.
type MCPAddBatchRequest struct {
	Collection string             `json:"collection"`
	Documents  []MCPBatchDocument `json:"documents"`
}

// MCPAddBatchResponse represents result of adding multiple documents.
type MCPAddBatchResponse struct {
	Added   int      `json:"added"`
	Updated int      `json:"updated"`
	Failed  int      `json:"failed"`
	Errors  []string `json:"errors,omitempty"`
}

// MCPUpdateDocument represents a document to update.
type MCPUpdateDocument struct {
	Key          string              `json:"key"`
	Lang         string              `json:"lang"`
	Meta         map[string][]string `json:"meta"`
	ContentMD    string              `json:"content_md"`
	SaveRevision bool                `json:"save_revision"`
}

// MCPUpdateBatchRequest represents request to update multiple documents.
type MCPUpdateBatchRequest struct {
	Collection string              `json:"collection"`
	Documents  []MCPUpdateDocument `json:"documents"`
}

// MCPUpdateBatchResponse represents result of updating multiple documents.
type MCPUpdateBatchResponse struct {
	Updated  int      `json:"updated"`
	NotFound int      `json:"not_found"`
	Failed   int      `json:"failed"`
	Errors   []string `json:"errors,omitempty"`
}

// MCPDeleteDocument represents a document to delete.
type MCPDeleteDocument struct {
	Key  string `json:"key"`
	Lang string `json:"lang"`
}

// MCPDeleteBatchRequest represents request to delete multiple documents.
type MCPDeleteBatchRequest struct {
	Collection string              `json:"collection"`
	Documents  []MCPDeleteDocument `json:"documents"`
}

// MCPDeleteBatchResponse represents result of deleting multiple documents.
type MCPDeleteBatchResponse struct {
	Deleted  int      `json:"deleted"`
	NotFound int      `json:"not_found"`
	Failed   int      `json:"failed"`
	Errors   []string `json:"errors,omitempty"`
}

// MCPExportRequest represents export request.
type MCPExportRequest struct {
	Collection string              `json:"collection"`
	FilterMeta map[string][]string `json:"filter_meta,omitempty"`
	Format     string              `json:"format"`
}

// MCPBackupRequest represents backup request.
type MCPBackupRequest struct {
	To string `json:"to"`
}

// MCPBackupResponse represents backup result.
type MCPBackupResponse struct {
	Backup string `json:"backup"`
}

// MCPRestoreRequest represents restore from backup request.
type MCPRestoreRequest struct {
	From string `json:"from"`
}

// MCPRestoreResponse represents restore result.
type MCPRestoreResponse struct {
	Restored string `json:"restored"`
}

// MCPTruncateRequest represents request to truncate revision history.
type MCPTruncateRequest struct {
	Collection string `json:"collection"`
	KeepRevs   int    `json:"keep_revs"`
	DropCache  bool   `json:"drop_cache"`
}

// MCPTruncateResponse represents truncate result.
type MCPTruncateResponse struct {
	Status string `json:"status"`
}

// MCPVectorSearchRequest represents vector/semantic search request.
type MCPVectorSearchRequest struct {
	Collection     string              `json:"collection"`
	Query          string              `json:"query"`
	QueryVector    []float32           `json:"queryVector,omitempty"`
	TopK           int                 `json:"topK,omitempty"`
	Threshold      float64             `json:"threshold,omitempty"`
	FilterMeta     map[string][]string `json:"filterMeta,omitempty"`
	IncludeContent bool                `json:"includeContent,omitempty"`
	Algorithm      string              `json:"algorithm,omitempty"`
	DistanceMetric string              `json:"distanceMetric,omitempty"`
	RetrievalMode  string              `json:"retrievalMode,omitempty"` // "parent" (default), "chunk", "window"
	WindowSize     int                 `json:"windowSize,omitempty"`    // neighbor chunks per side in "window" mode
	MMR            bool                `json:"mmr,omitempty"`           // diversify results via Maximal Marginal Relevance
	MMRLambda      float64             `json:"mmrLambda,omitempty"`     // relevance/diversity balance, 0..1 (default 0.5)
	// Oversample is the recall/latency knob (SRCH-005): candidates asked of
	// the index per requested result, before deduplication, merging or
	// rescoring trims them. 1.0-10.0; 0 = use the collection profile, then
	// the default.
	Oversample float64 `json:"oversample,omitempty"`
}

// MCPVectorSearchResult represents a single semantic search result.
type MCPVectorSearchResult struct {
	Document   MCPDocument `json:"document"`
	Score      float32     `json:"score"`
	Rank       int         `json:"rank"`
	ChunkIndex *int        `json:"chunkIndex,omitempty"` // set in chunk/window retrieval modes
	ChunkText  string      `json:"chunkText,omitempty"`  // matching passage (chunk/window modes)
	// StartLine and EndLine are 1-based and inclusive, locating the passage in
	// the parent document (CODE-002) — what an agent needs to edit a place
	// rather than read a file.
	StartLine int `json:"startLine,omitempty"`
	EndLine   int `json:"endLine,omitempty"`
}

// MCPVectorSearchResponse represents vector search results.
type MCPVectorSearchResponse struct {
	Results        []MCPVectorSearchResult `json:"results"`
	Total          int                     `json:"total"`
	Model          string                  `json:"model"`
	Dimensions     int                     `json:"dimensions"`
	Algorithm      string                  `json:"algorithm"`
	DistanceMetric string                  `json:"distanceMetric"`
	// ContextTruncated reports that the collection's contextTokenBudget cut
	// results from this answer (RAG-001). A caller assembling a prompt needs
	// to know it is holding part of the answer, not all of it.
	ContextTruncated bool `json:"contextTruncated,omitempty"`
	// ResponsePrompt is the collection's formatting instruction (RAG-002),
	// present only when one is configured. Returned with the results so an
	// agent gets it in the same round trip that fetched what to say.
	ResponsePrompt string `json:"responsePrompt,omitempty"`
}

// MCPVectorReindexRequest represents a reindex request.
type MCPVectorReindexRequest struct {
	Collection string `json:"collection"`
	Force      bool   `json:"force"`
}

// MCPVectorReindexResponse represents reindex results.
type MCPVectorReindexResponse struct {
	Embedded int      `json:"embedded"`
	Skipped  int      `json:"skipped"`
	Failed   int      `json:"failed"`
	Errors   []string `json:"errors,omitempty"`
}

// MCPVectorStatsResponse represents vector stats.
type MCPVectorStatsResponse struct {
	Provider    string                              `json:"provider"`
	Model       string                              `json:"model"`
	Dimensions  int                                 `json:"dimensions"`
	Enabled     bool                                `json:"enabled"`
	Collections map[string]MCPVectorCollectionStats `json:"collections"`
}

// MCPVectorCollectionStats represents per-collection embedding stats.
type MCPVectorCollectionStats struct {
	TotalDocuments    int `json:"total_documents"`
	EmbeddedDocuments int `json:"embedded_documents"`
}

// MCPImportURLRequest represents request to import a document from URL.
type MCPImportURLRequest struct {
	Collection string              `json:"collection"`
	URL        string              `json:"url"`
	Key        string              `json:"key,omitempty"`
	Lang       string              `json:"lang"`
	Meta       map[string][]string `json:"meta,omitempty"`
	TTL        int64               `json:"ttl,omitempty"`
}

// MCPSetTTLRequest represents request to set TTL on a document.
type MCPSetTTLRequest struct {
	Collection string `json:"collection"`
	Key        string `json:"key"`
	Lang       string `json:"lang"`
	TTL        int64  `json:"ttl"`
}

// MCPFTSSearchRequest represents full-text search request.
type MCPFTSSearchRequest struct {
	Collection string             `json:"collection"`
	Query      string             `json:"query"`
	Limit      int                `json:"limit,omitempty"`
	Algorithm  string             `json:"algorithm,omitempty"`
	Fuzzy      int                `json:"fuzzy,omitempty"`
	Lang       string             `json:"lang,omitempty"`
	Boost      map[string]float64 `json:"boost,omitempty"`
	// IncludeContent — see MCPSearchRequest (GO-022).
	IncludeContent bool `json:"includeContent,omitempty"`
	// Highlight asks for the matching fragments and, with them, the lines they
	// occupy (CODE-002). This is the answer shape issue #192 asked for: a
	// place to edit rather than a document to read.
	Highlight     bool   `json:"highlight,omitempty"`
	HighlightTag  string `json:"highlightTag,omitempty"`
	MaxHighlights int    `json:"maxHighlights,omitempty"`
	FragmentSize  int    `json:"fragmentSize,omitempty"`
}

// MCPFTSResult represents a single FTS result.
type MCPFTSResult struct {
	Document     MCPDocument `json:"document"`
	Score        float64     `json:"score"`
	MatchedTerms []string    `json:"matchedTerms"`
	// Highlights carry each matching fragment with the 1-based, inclusive line
	// range it occupies (CODE-002).
	Highlights []fts.Highlight `json:"highlights,omitempty"`
}

// MCPFTSSearchResponse represents full-text search results.
type MCPFTSSearchResponse struct {
	Results   []MCPFTSResult `json:"results"`
	Total     int            `json:"total"`
	Algorithm string         `json:"algorithm"`
	Fuzzy     int            `json:"fuzzy"`
	Lang      string         `json:"lang,omitempty"`
	// ContextTruncated reports that the collection's contextTokenBudget cut
	// results from this answer (RAG-001). A caller assembling a prompt needs
	// to know it is holding part of the answer, not all of it.
	ContextTruncated bool `json:"contextTruncated,omitempty"`
	// ResponsePrompt is the collection's formatting instruction (RAG-002),
	// present only when one is configured. Returned with the results so an
	// agent gets it in the same round trip that fetched what to say.
	ResponsePrompt string `json:"responsePrompt,omitempty"`
}

// MCPFTSReindexRequest represents a request to reindex FTS for a collection.
type MCPFTSReindexRequest struct {
	Collection string `json:"collection"`
}

// MCPFTSReindexResponse represents the result of FTS reindex.
type MCPFTSReindexResponse struct {
	Status    string `json:"status"`
	Reindexed int    `json:"reindexed"`
	Skipped   int    `json:"skipped"`
}

// MCPFTSLanguageInfo represents a supported language.
type MCPFTSLanguageInfo struct {
	Code string `json:"code"`
	Name string `json:"name"`
}

// MCPFTSLanguagesResponse represents the list of supported FTS languages.
type MCPFTSLanguagesResponse struct {
	Languages   []MCPFTSLanguageInfo `json:"languages"`
	DefaultLang string               `json:"defaultLang"`
}

// MCPHybridSearchRequest represents hybrid sparse+dense search request.
type MCPHybridSearchRequest struct {
	Collection      string              `json:"collection"`
	Query           string              `json:"query"`
	TopK            int                 `json:"topK,omitempty"`
	Algorithm       string              `json:"algorithm,omitempty"`       // FTS: "bm25", "bm25f"
	VectorAlgorithm string              `json:"vectorAlgorithm,omitempty"` // Vector: "flat", "hnsw", "ivf", "pq", "opq", "sq", "sq4", "bq"
	Alpha           float64             `json:"alpha,omitempty"`           // 0-1, default 0.5
	Strategy        string              `json:"strategy,omitempty"`        // "alpha" or "rrf"
	RRFK            int                 `json:"rrfK,omitempty"`            // RRF k parameter
	Fuzzy           int                 `json:"fuzzy,omitempty"`
	Threshold       float64             `json:"threshold,omitempty"`
	DistanceMetric  string              `json:"distanceMetric,omitempty"`
	FilterMeta      map[string][]string `json:"filterMeta,omitempty"`
	Boost           map[string]float64  `json:"boost,omitempty"`
	Sort            string              `json:"sort,omitempty"` // "combined" (default) or "distance"
	// Oversample is the recall/latency knob (SRCH-005): candidates asked of
	// the index per requested result, before deduplication, merging or
	// rescoring trims them. 1.0-10.0; 0 = use the collection profile, then
	// the default.
	Oversample float64 `json:"oversample,omitempty"`
}

// MCPHybridSearchResult represents a single hybrid search result.
type MCPHybridSearchResult struct {
	Document      MCPDocument `json:"document"`
	CombinedScore float64     `json:"combinedScore"`
	FTSScore      float64     `json:"ftsScore"`
	VectorScore   float64     `json:"vectorScore"`
	MatchedTerms  []string    `json:"matchedTerms,omitempty"`
	Rank          int         `json:"rank"`
}

// MCPHybridSearchResponse represents hybrid search results.
type MCPHybridSearchResponse struct {
	Results         []MCPHybridSearchResult `json:"results"`
	Total           int                     `json:"total"`
	Strategy        string                  `json:"strategy"`
	FTSAlgorithm    string                  `json:"ftsAlgorithm"`
	VectorAlgorithm string                  `json:"vectorAlgorithm"`
	DistanceMetric  string                  `json:"distanceMetric"`
	// ContextTruncated reports that the collection's contextTokenBudget cut
	// results from this answer (RAG-001). A caller assembling a prompt needs
	// to know it is holding part of the answer, not all of it.
	ContextTruncated bool `json:"contextTruncated,omitempty"`
	// ResponsePrompt is the collection's formatting instruction (RAG-002),
	// present only when one is configured. Returned with the results so an
	// agent gets it in the same round trip that fetched what to say.
	ResponsePrompt string `json:"responsePrompt,omitempty"`
}

// MCPGeoSearchRequest represents a geo radius search.
type MCPGeoSearchRequest struct {
	Collection     string              `json:"collection"`
	Lat            float64             `json:"lat"`
	Lng            float64             `json:"lng"`
	RadiusMeters   float64             `json:"radiusMeters"`
	TopK           int                 `json:"topK,omitempty"`
	Algorithm      string              `json:"algorithm,omitempty"` // rtree (default) or geohash
	FilterMeta     map[string][]string `json:"filterMeta,omitempty"`
	IncludeContent bool                `json:"includeContent,omitempty"`
}

// MCPGeoWithinRequest represents a geo bbox search.
type MCPGeoWithinRequest struct {
	Collection     string              `json:"collection"`
	MinLat         float64             `json:"minLat"`
	MaxLat         float64             `json:"maxLat"`
	MinLng         float64             `json:"minLng"`
	MaxLng         float64             `json:"maxLng"`
	FilterMeta     map[string][]string `json:"filterMeta,omitempty"`
	IncludeContent bool                `json:"includeContent,omitempty"`
}

// MCPGeoSearchResult is a single geo search result item.
type MCPGeoSearchResult struct {
	Document       MCPDocument `json:"document"`
	DistanceMeters float64     `json:"distanceMeters,omitempty"`
	Rank           int         `json:"rank"`
}

// MCPGeoSearchResponse is returned from GeoSearch/GeoWithin.
type MCPGeoSearchResponse struct {
	Results      []MCPGeoSearchResult `json:"results"`
	Total        int                  `json:"total"`
	RadiusMeters float64              `json:"radiusMeters,omitempty"`
	Algorithm    string               `json:"algorithm"`
}

// MCPGeoStatsResponse is returned from GeoStats.
type MCPGeoStatsResponse struct {
	Collections      map[string]int `json:"collections"`
	PostcodeDatasets map[string]int `json:"postcodeDatasets,omitempty"`
	Ready            bool           `json:"ready"`
}

// MCPWebhook represents a webhook subscription.
type MCPWebhook struct {
	ID         string   `json:"id"`
	URL        string   `json:"url"`
	Events     []string `json:"events"`
	Collection string   `json:"collection,omitempty"`
	CreatedAt  int64    `json:"createdAt"`
}

// MCPRegisterWebhookRequest represents request to register a webhook.
type MCPRegisterWebhookRequest struct {
	URL        string   `json:"url"`
	Events     []string `json:"events"`
	Collection string   `json:"collection,omitempty"`
}

// MCPDeleteWebhookRequest represents request to delete a webhook.
type MCPDeleteWebhookRequest struct {
	ID string `json:"id"`
}

// MCPSetSchemaRequest represents request to set a collection schema.
type MCPSetSchemaRequest struct {
	Collection string `json:"collection"`
	Schema     string `json:"schema"`
}

// MCPSchemaResponse represents a schema get response.
type MCPSchemaResponse struct {
	Collection string `json:"collection"`
	Schema     string `json:"schema"`
	Enabled    bool   `json:"enabled"`
}

// MCPSchemaInfo represents a schema in a list.
type MCPSchemaInfo struct {
	Collection string `json:"collection"`
	Schema     string `json:"schema"`
}

// MCPListSchemasResponse represents list schemas result.
type MCPListSchemasResponse struct {
	Schemas []MCPSchemaInfo `json:"schemas"`
}

// MCPValidateRequest represents request to validate document metadata.
type MCPValidateRequest struct {
	Collection string              `json:"collection"`
	Meta       map[string][]string `json:"meta"`
}

// MCPValidateResponse represents validation result.
type MCPValidateResponse struct {
	Valid  bool     `json:"valid"`
	Errors []string `json:"errors"`
	// Warnings are advisory and never make a document invalid (DOC-012).
	Warnings []string `json:"warnings,omitempty"`
}

// MCPUpdateDocumentRequest represents request to partially update a document.
type MCPUpdateDocumentRequest struct {
	Collection string              `json:"collection"`
	Key        string              `json:"key"`
	Lang       string              `json:"lang"`
	Meta       map[string][]string `json:"meta,omitempty"`
	ContentMD  *string             `json:"contentMd,omitempty"`
	TTL        *int64              `json:"ttl,omitempty"`
}

// MCPGetDocMetaRequest represents request to get document metadata only.
type MCPGetDocMetaRequest struct {
	Collection string `json:"collection"`
	Key        string `json:"key"`
	Lang       string `json:"lang"`
}

// MCPDocMetaResponse represents document metadata without content.
type MCPDocMetaResponse struct {
	Key       string              `json:"key"`
	Lang      string              `json:"lang"`
	Meta      map[string][]string `json:"meta"`
	AddedAt   int64               `json:"addedAt"`
	UpdatedAt int64               `json:"updatedAt"`
	ExpiresAt int64               `json:"expiresAt,omitempty"`
}

// MCPClassifyRequest represents zero-shot classification request.
type MCPClassifyRequest struct {
	Collection string   `json:"collection,omitempty"`
	Key        string   `json:"key,omitempty"`
	Lang       string   `json:"lang,omitempty"`
	Text       string   `json:"text,omitempty"`
	Labels     []string `json:"labels"`
	TopK       int      `json:"topK,omitempty"`
	Multi      bool     `json:"multi,omitempty"`
	Threshold  float64  `json:"threshold,omitempty"`
}

// MCPClassifyLabelScore represents a single classification result.
type MCPClassifyLabelScore struct {
	Label string  `json:"label"`
	Score float64 `json:"score"`
}

// MCPClassifyResponse represents classification results.
type MCPClassifyResponse struct {
	Results    []MCPClassifyLabelScore `json:"results"`
	Model      string                  `json:"model"`
	Dimensions int                     `json:"dimensions"`
}

// MCPSynonymEntry represents a synonym group.
type MCPSynonymEntry struct {
	Term     string   `json:"term"`
	Synonyms []string `json:"synonyms"`
}

// MCPSynonymListResponse represents list of synonyms.
type MCPSynonymListResponse struct {
	Collection string            `json:"collection"`
	Entries    []MCPSynonymEntry `json:"entries"`
	Total      int               `json:"total"`
}

// MCPStopWordEntry represents a stop word entry.
type MCPStopWordEntry struct {
	Word      string `json:"word"`
	IsDefault bool   `json:"isDefault"`
}

// MCPStopWordListResponse represents list of stop words.
type MCPStopWordListResponse struct {
	Collection string             `json:"collection"`
	Entries    []MCPStopWordEntry `json:"entries"`
	Total      int                `json:"total"`
	Defaults   int                `json:"defaults"`
	Custom     int                `json:"custom"`
}

// MCPMetaKeysResponse represents metadata keys and values.
type MCPMetaKeysResponse struct {
	Meta map[string][]string `json:"meta"`
}

// MCPChecksumResponse represents collection checksum.
type MCPChecksumResponse struct {
	Collection    string `json:"collection"`
	Checksum      string `json:"checksum"`
	DocumentCount int    `json:"documentCount"`
}

// MCPAutomationListResponse represents list of automation rules.
type MCPAutomationListResponse struct {
	Rules []AutomationRule `json:"rules"`
	Total int              `json:"total"`
}

// MCPAutomationLogListResponse represents list of automation logs.
type MCPAutomationLogListResponse struct {
	Logs       []automationlog.Entry `json:"logs"`
	Total      int                   `json:"total"`
	NextCursor string                `json:"nextCursor,omitempty"`
	HasMore    bool                  `json:"hasMore"`
}

// --- Collection Config MCP Types ---

// MCPCollectionConfigResponse is the response for get_collection_config.
type MCPCollectionConfigResponse struct {
	Collection string            `json:"collection"`
	Config     *CollectionConfig `json:"config"`
	Configured bool              `json:"configured"`
}

// MCPSetCollectionConfigRequest is the request for set_collection_config.
type MCPSetCollectionConfigRequest struct {
	Collection   string                 `json:"collection"`
	Type         string                 `json:"type,omitempty"`
	Description  string                 `json:"description,omitempty"`
	Icon         string                 `json:"icon,omitempty"`
	Color        string                 `json:"color,omitempty"`
	CustomMeta   map[string]string      `json:"customMeta,omitempty"`
	MaxRevisions int                    `json:"maxRevisions,omitempty"`
	WordPress    *WordPressTargetConfig `json:"wordpress,omitempty"`
	// Retrieval and ResponsePrompt are nil/empty when the caller did not
	// mention them, and are then left as stored (RAG-001, RAG-002).
	Retrieval      *RetrievalProfileDef `json:"retrieval,omitempty"`
	ResponsePrompt string               `json:"responsePrompt,omitempty"`
}

// MCPCollectionConfigListResponse is the response for list_collection_configs.
type MCPCollectionConfigListResponse struct {
	Configs map[string]*CollectionConfig `json:"configs"`
	Total   int                          `json:"total"`
}

// --- Cross-Search MCP Types ---

// MCPCrossSearchRequest is the request for cross_search.
type MCPCrossSearchRequest struct {
	SourceCollection  string              `json:"sourceCollection"`
	SourceDocID       string              `json:"sourceDocID"`
	Query             string              `json:"query"`
	TargetCollections []string            `json:"targetCollections"`
	TopK              int                 `json:"topK"`
	Threshold         float64             `json:"threshold"`
	Algorithm         string              `json:"algorithm"`
	DistanceMetric    string              `json:"distanceMetric"`
	FilterMeta        map[string][]string `json:"filterMeta"`
	IncludeContent    bool                `json:"includeContent"`
	// Oversample is the recall/latency knob (SRCH-005).
	Oversample float64 `json:"oversample,omitempty"`
}

// MCPCrossSearchResponse is the response for cross_search.
type MCPCrossSearchResponse struct {
	Results           []CrossSearchResultItem `json:"results"`
	Total             int                     `json:"total"`
	SourceCollection  string                  `json:"sourceCollection,omitempty"`
	SourceDocID       string                  `json:"sourceDocID,omitempty"`
	TargetCollections []string                `json:"targetCollections"`
	Algorithm         string                  `json:"algorithm"`
	DistanceMetric    string                  `json:"distanceMetric"`
}

// --- Find Duplicates MCP Types ---

// MCPFindDuplicatesRequest is the request for find_duplicates.
type MCPFindDuplicatesRequest struct {
	Collection     string  `json:"collection"`
	Mode           string  `json:"mode"`
	Threshold      float64 `json:"threshold"`
	MaxDocs        int     `json:"maxDocs"`
	DistanceMetric string  `json:"distanceMetric"`
	IncludeContent bool    `json:"includeContent"`
}

// MCPFindDuplicatesResponse is the response for find_duplicates.
type MCPFindDuplicatesResponse = FindDuplicatesResponse

// --- MCP Protocol Types ---

// MCPResource represents an MCP resource.
type MCPResource struct {
	URI         string `json:"uri"`
	Name        string `json:"name"`
	Description string `json:"description"`
	MimeType    string `json:"mimeType"`
}

// MCPResourceReadRequest represents resource read request.
type MCPResourceReadRequest struct {
	URI string `json:"uri"`
}

// MCPToolCallRequest represents tool call request.
type MCPToolCallRequest struct {
	Name      string                 `json:"name"`
	Arguments map[string]interface{} `json:"arguments"`
}

// --- Bulk Ingest Types ---

// MCPIngestDocument represents a document in an ingest operation.
type MCPIngestDocument struct {
	URL                string              `json:"url"`
	Key                string              `json:"key,omitempty"`
	Lang               string              `json:"lang"`
	ContentMD          string              `json:"content_md"`
	Meta               map[string][]string `json:"meta,omitempty"`
	ExtractFrontmatter bool                `json:"extract_frontmatter,omitempty"`
	ScrapedAt          int64               `json:"scraped_at,omitempty"`
	Scraper            string              `json:"scraper,omitempty"`
	TTL                int64               `json:"ttl,omitempty"`
}

// MCPIngestOptions represents options for ingest operation.
type MCPIngestOptions struct {
	SkipDuplicates          bool `json:"skip_duplicates,omitempty"`
	SkipEmbeddings          bool `json:"skip_embeddings,omitempty"`
	SkipFTS                 bool `json:"skip_fts,omitempty"`
	SkipWebhooks            bool `json:"skip_webhooks,omitempty"`
	AutoConfigureCollection bool `json:"auto_configure_collection,omitempty"`
	SaveRevision            bool `json:"save_revision,omitempty"`
}

// MCPIngestRequest represents request to ingest multiple documents.
type MCPIngestRequest struct {
	Collection string              `json:"collection"`
	Documents  []MCPIngestDocument `json:"documents"`
	Options    MCPIngestOptions    `json:"options,omitempty"`
}

// MCPIngestResponse represents result of ingest operation.
type MCPIngestResponse struct {
	Added      int      `json:"added"`
	Updated    int      `json:"updated"`
	Skipped    int      `json:"skipped"`
	Failed     int      `json:"failed"`
	Errors     []string `json:"errors,omitempty"`
	Collection string   `json:"collection"`
	DurationMs int64    `json:"duration_ms"`
}

// --- MCP Client Interface ---

// MCPClient is the interface for MCP to access MDDB operations.
type MCPClient interface {
	Health(ctx context.Context) (*MCPHealth, error)
	Stats(ctx context.Context) (*MCPStats, error)
	Add(ctx context.Context, req *MCPAddRequest) (*MCPDocument, error)
	AddBatch(ctx context.Context, req *MCPAddBatchRequest) (*MCPAddBatchResponse, error)
	UpdateBatch(ctx context.Context, req *MCPUpdateBatchRequest) (*MCPUpdateBatchResponse, error)
	DeleteBatch(ctx context.Context, req *MCPDeleteBatchRequest) (*MCPDeleteBatchResponse, error)
	Get(ctx context.Context, req *MCPGetRequest) (*MCPDocument, error)
	Search(ctx context.Context, req *MCPSearchRequest) (*MCPSearchResponse, error)
	Delete(ctx context.Context, req *MCPDeleteRequest) error
	DeleteCollection(ctx context.Context, req *MCPDeleteCollectionRequest) (*MCPDeleteCollectionResponse, error)
	Export(ctx context.Context, req *MCPExportRequest) (io.ReadCloser, error)
	Backup(ctx context.Context, req *MCPBackupRequest) (*MCPBackupResponse, error)
	Restore(ctx context.Context, req *MCPRestoreRequest) (*MCPRestoreResponse, error)
	Truncate(ctx context.Context, req *MCPTruncateRequest) (*MCPTruncateResponse, error)
	VectorSearch(ctx context.Context, req *MCPVectorSearchRequest) (*MCPVectorSearchResponse, error)
	VectorReindex(ctx context.Context, req *MCPVectorReindexRequest) (*MCPVectorReindexResponse, error)
	VectorStats(ctx context.Context) (*MCPVectorStatsResponse, error)
	ImportURL(ctx context.Context, req *MCPImportURLRequest) (*MCPDocument, error)
	SetTTL(ctx context.Context, req *MCPSetTTLRequest) (*MCPDocument, error)
	FTSSearch(ctx context.Context, req *MCPFTSSearchRequest) (*MCPFTSSearchResponse, error)
	FTSReindex(ctx context.Context, req *MCPFTSReindexRequest) (*MCPFTSReindexResponse, error)
	FTSLanguages(ctx context.Context) (*MCPFTSLanguagesResponse, error)
	HybridSearch(ctx context.Context, req *MCPHybridSearchRequest) (*MCPHybridSearchResponse, error)
	// Geo search
	GeoSearch(ctx context.Context, req *MCPGeoSearchRequest) (*MCPGeoSearchResponse, error)
	GeoWithin(ctx context.Context, req *MCPGeoWithinRequest) (*MCPGeoSearchResponse, error)
	GeoStats(ctx context.Context) (*MCPGeoStatsResponse, error)
	GeoEncode(ctx context.Context, lat, lng float64, precision int) (string, error)
	GeoDecode(ctx context.Context, hash string) (float64, float64, error)
	RegisterWebhook(ctx context.Context, req *MCPRegisterWebhookRequest) (*MCPWebhook, error)
	ListWebhooks(ctx context.Context) ([]MCPWebhook, error)
	DeleteWebhook(ctx context.Context, req *MCPDeleteWebhookRequest) error
	SetSchema(ctx context.Context, req *MCPSetSchemaRequest) error
	GetSchema(ctx context.Context, collection string) (*MCPSchemaResponse, error)
	DeleteSchema(ctx context.Context, collection string) error
	ListSchemas(ctx context.Context) (*MCPListSchemasResponse, error)
	ValidateDocument(ctx context.Context, req *MCPValidateRequest) (*MCPValidateResponse, error)
	UpdateDocument(ctx context.Context, req *MCPUpdateDocumentRequest) (*MCPDocument, error)
	GetDocumentMeta(ctx context.Context, req *MCPGetDocMetaRequest) (*MCPDocMetaResponse, error)
	CodeGraph(ctx context.Context, req *MCPCodeGraphRequest) (*GraphResult, error)
	// SearchAdvisor measures a collection and recommends how to search it
	// (SRCH-010), so an agent picks an algorithm from evidence rather than
	// from the names.
	SearchAdvisor(ctx context.Context, collection string) (*SearchRecommendation, error)
	Classify(ctx context.Context, req *MCPClassifyRequest) (*MCPClassifyResponse, error)
	// Synonyms
	ListSynonyms(ctx context.Context, collection string) (*MCPSynonymListResponse, error)
	SetSynonym(ctx context.Context, collection, term string, synonyms []string) error
	DeleteSynonym(ctx context.Context, collection, term string) error
	// Stopwords
	ListStopWords(ctx context.Context, collection string) (*MCPStopWordListResponse, error)
	AddStopWords(ctx context.Context, collection string, words []string) error
	DeleteStopWords(ctx context.Context, collection string, words []string) error
	// MetaKeys / Checksum
	GetMetaKeys(ctx context.Context, collection string) (*MCPMetaKeysResponse, error)
	GetChecksum(ctx context.Context, collection string) (*MCPChecksumResponse, error)
	// Automation
	ListAutomation(ctx context.Context, filterType string) (*MCPAutomationListResponse, error)
	CreateAutomation(ctx context.Context, rule AutomationRule) (*AutomationRule, error)
	GetAutomation(ctx context.Context, id string) (*AutomationRule, error)
	UpdateAutomation(ctx context.Context, id string, rule AutomationRule) (*AutomationRule, error)
	DeleteAutomation(ctx context.Context, id string) error
	TestAutomation(ctx context.Context, id string) (string, error)
	ListAutomationLogs(ctx context.Context, limit int, cursor, ruleID, status string) (*MCPAutomationLogListResponse, error)
	// Revisions
	ListRevisions(ctx context.Context, collection, key, lang string) (*RevisionListResponse, error)
	RestoreRevision(ctx context.Context, collection, key, lang string, timestamp int64) (*MCPDocument, error)
	// Collection config
	GetCollectionConfig(ctx context.Context, collection string) (*MCPCollectionConfigResponse, error)
	SetCollectionConfig(ctx context.Context, req *MCPSetCollectionConfigRequest) error
	ListCollectionConfigs(ctx context.Context) (*MCPCollectionConfigListResponse, error)
	// Curation (v2.9.14+)
	ListCurationRules(ctx context.Context, collection string) ([]*CurationRule, error)
	CreateCurationRule(ctx context.Context, rule *CurationRule) (*CurationRule, error)
	UpdateCurationRule(ctx context.Context, rule *CurationRule) (*CurationRule, error)
	DeleteCurationRule(ctx context.Context, id string) error
	// Cross-collection search
	CrossSearch(ctx context.Context, req *MCPCrossSearchRequest) (*MCPCrossSearchResponse, error)
	// Duplicate detection
	FindDuplicates(ctx context.Context, req *MCPFindDuplicatesRequest) (*MCPFindDuplicatesResponse, error)
	// Bulk ingest
	Ingest(ctx context.Context, req *MCPIngestRequest) (*MCPIngestResponse, error)
	// Aggregations
	Aggregate(ctx context.Context, req *AggregateRequest) (*AggregateResponse, error)
	Close() error
}

// --- Type Conversion Helpers ---

func docToMCPDocument(d storage.Doc) MCPDocument {
	return MCPDocument{
		ID:        d.ID,
		Key:       d.Key,
		Lang:      d.Lang,
		Meta:      d.Meta,
		ContentMD: d.ContentMD,
		AddedAt:   time.Unix(d.AddedAt, 0),
		UpdatedAt: time.Unix(d.UpdatedAt, 0),
	}
}

// MCPCodeGraphRequest is one code-graph traversal over MCP (CODE-005).
type MCPCodeGraphRequest struct {
	Collection string `json:"collection"`
	Key        string `json:"key"`
	Direction  string `json:"direction"`
	Depth      int    `json:"depth"`
	MaxDegree  int    `json:"max_degree"`
	Lines      bool   `json:"lines"`
}
