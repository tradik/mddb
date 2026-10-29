package mddb

// Request and response types for /v1/fts, /v1/vector-search and
// /v1/hybrid-search. Field names and JSON tags follow the server's structs
// (FTSSearchRequest, VectorSearchRequest, HybridSearchRequest and their
// responses in services/mddbd); optional fields are omitted when zero so the
// server applies its own defaults.

// FTSRequest is a full-text search query.
type FTSRequest struct {
	Collection      string              `json:"collection"`
	Query           string              `json:"query"`
	Lang            string              `json:"lang,omitempty"`
	Limit           int                 `json:"limit,omitempty"`
	Algorithm       string              `json:"algorithm,omitempty"` // tfidf|bm25|bm25f|pmisparse
	Fuzzy           int                 `json:"fuzzy,omitempty"`     // typo tolerance: 0, 1 or 2 edits
	DisableStem     bool                `json:"disableStem,omitempty"`
	DisableSynonyms bool                `json:"disableSynonyms,omitempty"`
	FieldWeights    map[string]float64  `json:"fieldWeights,omitempty"`
	FilterMeta      map[string][]string `json:"filterMeta,omitempty"`
	Mode            string              `json:"mode,omitempty"`     // simple|boolean|phrase|wildcard|proximity|auto
	Distance        int                 `json:"distance,omitempty"` // words, for mode=proximity
	RangeMeta       []RangeFilter       `json:"rangeMeta,omitempty"`
	Boost           map[string]float64  `json:"boost,omitempty"` // "metaKey:metaValue" -> multiplier
	Highlight       bool                `json:"highlight,omitempty"`
	HighlightTag    string              `json:"highlightTag,omitempty"`
	MaxHighlights   int                 `json:"maxHighlights,omitempty"`
	FragmentSize    int                 `json:"fragmentSize,omitempty"`
	FacetBy         []string            `json:"facetBy,omitempty"`
	FacetMaxValues  int                 `json:"facetMaxValues,omitempty"`
	FacetScope      string              `json:"facetScope,omitempty"` // returned|all
	CacheTTL        int                 `json:"cacheTtl,omitempty"`   // seconds; 0 = no result cache
	IncludeImages   bool                `json:"includeImages,omitempty"`
}

// ImageRef is an image document attached to a hit, or found by its picture
// (v2.18.9+). URL is the GET path of its bytes.
type ImageRef struct {
	Key      string  `json:"key"`
	Lang     string  `json:"lang"`
	AltText  string  `json:"altText,omitempty"`
	Caption  string  `json:"caption,omitempty"`
	Page     int     `json:"page,omitempty"`
	MimeType string  `json:"mimeType,omitempty"`
	Width    int     `json:"width,omitempty"`
	Height   int     `json:"height,omitempty"`
	Parent   string  `json:"parent,omitempty"`
	Score    float32 `json:"score,omitempty"`
	URL      string  `json:"url"`
}

// RangeFilter bounds a metadata key, or addedAt/updatedAt, in an FTS query.
type RangeFilter struct {
	Field string `json:"field"`
	Gte   string `json:"gte,omitempty"`
	Lte   string `json:"lte,omitempty"`
	Gt    string `json:"gt,omitempty"`
	Lt    string `json:"lt,omitempty"`
}

// Highlight is one matching fragment of an FTS result.
type Highlight struct {
	Fragment     string   `json:"fragment"`
	MatchedTerms []string `json:"matchedTerms,omitempty"`
	StartOffset  int      `json:"startOffset"`
	EndOffset    int      `json:"endOffset"`
	StartLine    int      `json:"startLine"` // 1-based, inclusive
	EndLine      int      `json:"endLine"`
}

// SearchStats is the optional timing block of a search response.
type SearchStats struct {
	DurationMs  float64  `json:"durationMs"`
	QueryTerms  []string `json:"queryTerms,omitempty"`
	IndexSize   int      `json:"indexSize"`
	TotalTokens int      `json:"totalTokens"`
}

// SpellCorrection reports a query the server corrected before searching.
type SpellCorrection struct {
	Original  string `json:"original"`
	Corrected string `json:"corrected"`
}

// FTSResult is one hit of a full-text search.
type FTSResult struct {
	Document     Document    `json:"document"`
	Score        float64     `json:"score"`
	MatchedTerms []string    `json:"matchedTerms"`
	Highlights   []Highlight `json:"highlights,omitempty"`
	Pinned       bool        `json:"pinned,omitempty"`
	Images       []ImageRef  `json:"images,omitempty"` // with IncludeImages
}

// FTSResponse is the /v1/fts response.
type FTSResponse struct {
	Results          []FTSResult              `json:"results"`
	Total            int                      `json:"total"`
	Algorithm        string                   `json:"algorithm"`
	Mode             string                   `json:"mode"`
	Fuzzy            int                      `json:"fuzzy"`
	Lang             string                   `json:"lang,omitempty"`
	StemmingActive   bool                     `json:"stemmingActive"`
	SynonymsActive   bool                     `json:"synonymsActive"`
	FieldWeights     map[string]float64       `json:"fieldWeights,omitempty"`
	Stats            *SearchStats             `json:"searchStats,omitempty"`
	SpellCorrected   *SpellCorrection         `json:"spellCorrected,omitempty"`
	Facets           map[string][]FacetBucket `json:"facets,omitempty"`
	Matched          int                      `json:"matched,omitempty"` // with facetScope "all"
	ContextTruncated bool                     `json:"contextTruncated,omitempty"`
}

// VectorSearchRequest is a semantic (embedding) search query.
type VectorSearchRequest struct {
	Collection     string              `json:"collection"`
	Query          string              `json:"query"`
	TopK           int                 `json:"topK,omitempty"`
	Threshold      float64             `json:"threshold,omitempty"`
	QueryVector    []float32           `json:"queryVector,omitempty"`
	FilterMeta     map[string][]string `json:"filterMeta,omitempty"`
	IncludeContent bool                `json:"includeContent,omitempty"`
	Algorithm      string              `json:"algorithm,omitempty"`      // flat|hnsw|ivf|pq|opq|sq|sq4|bq
	DistanceMetric string              `json:"distanceMetric,omitempty"` // cosine|dot_product|euclidean
	RetrievalMode  string              `json:"retrievalMode,omitempty"`  // parent|chunk|window|graph
	GraphExpand    *GraphExpand        `json:"graphExpand,omitempty"`    // retrievalMode "graph" only
	WindowSize     int                 `json:"windowSize,omitempty"`
	MMR            bool                `json:"mmr,omitempty"`
	MMRLambda      float64             `json:"mmrLambda,omitempty"`
	Oversample     float64             `json:"oversample,omitempty"`
	IncludeImages  bool                `json:"includeImages,omitempty"`
	// QueryImage searches by an image (base64, a data URI or an http(s)
	// URL) instead of Query; needs a server provider that embeds images.
	QueryImage string `json:"queryImage,omitempty"`
}

// GraphExpand tunes vector search in retrievalMode "graph".
type GraphExpand struct {
	Depth         int     `json:"graphDepth,omitempty"`
	Decay         float64 `json:"graphDecay,omitempty"`
	MaxNeighbours int     `json:"graphMaxNeighbours,omitempty"`
	Direction     string  `json:"graphDirection,omitempty"` // in|out|both
}

// VectorSearchResult is one hit of a vector search.
type VectorSearchResult struct {
	Document   Document   `json:"document"`
	Score      float32    `json:"score"`
	Rank       int        `json:"rank"`
	ChunkIndex *int       `json:"chunkIndex,omitempty"` // chunk/window modes
	ChunkText  string     `json:"chunkText,omitempty"`
	StartLine  int        `json:"startLine,omitempty"`
	EndLine    int        `json:"endLine,omitempty"`
	Images     []ImageRef `json:"images,omitempty"` // with IncludeImages
}

// GraphExpansion explains a document added by retrievalMode "graph".
type GraphExpansion struct {
	Key     string  `json:"key"`
	FromKey string  `json:"fromKey"`
	Symbol  string  `json:"symbol"`
	Kind    string  `json:"kind"`
	Depth   int     `json:"depth"`
	Score   float64 `json:"score"`
}

// VectorSearchResponse is the /v1/vector-search response.
type VectorSearchResponse struct {
	Results          []VectorSearchResult `json:"results"`
	Total            int                  `json:"total"`
	Model            string               `json:"model"`
	Dimensions       int                  `json:"dimensions"`
	Algorithm        string               `json:"algorithm"`
	DistanceMetric   string               `json:"distanceMetric"`
	Stats            *SearchStats         `json:"searchStats,omitempty"`
	ContextTruncated bool                 `json:"contextTruncated,omitempty"`
	GraphExpansions  []GraphExpansion     `json:"graphExpansions,omitempty"`
	ImageHits        []ImageRef           `json:"imageHits,omitempty"` // with IncludeImages
}
