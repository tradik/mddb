package mddb

// Types for /v1/hybrid-search, after the server's HybridSearchRequest and
// HybridSearchResponse.

// HybridSearchRequest combines full-text and vector search in one query.
type HybridSearchRequest struct {
	Collection      string `json:"collection"`
	Query           string `json:"query"`
	TopK            int    `json:"topK,omitempty"`
	Algorithm       string `json:"algorithm,omitempty"`       // FTS: bm25|bm25f
	VectorAlgorithm string `json:"vectorAlgorithm,omitempty"` // flat|hnsw|ivf|pq|opq|sq|sq4|bq
	// Alpha weights vector against keyword: 0 = keyword only, 1 = semantic
	// only, nil = server default (0.5). A pointer because 0 is meaningful.
	Alpha           *float64            `json:"alpha,omitempty"`
	Strategy        string              `json:"strategy,omitempty"` // alpha|rrf|weighted
	Signals         *FusionSignals      `json:"signals,omitempty"`  // strategy "weighted" only
	RRFK            int                 `json:"rrfK,omitempty"`
	Fuzzy           int                 `json:"fuzzy,omitempty"`
	Threshold       float64             `json:"threshold,omitempty"`
	DistanceMetric  string              `json:"distanceMetric,omitempty"`
	FilterMeta      map[string][]string `json:"filterMeta,omitempty"`
	Geo             *GeoFilter          `json:"geo,omitempty"`
	IncludeContent  bool                `json:"includeContent,omitempty"`
	FieldWeights    map[string]float64  `json:"fieldWeights,omitempty"`
	DisableStem     bool                `json:"disableStem,omitempty"`
	DisableSynonyms bool                `json:"disableSynonyms,omitempty"`
	Lang            string              `json:"lang,omitempty"`
	Boost           map[string]float64  `json:"boost,omitempty"`
	Sort            string              `json:"sort,omitempty"` // combined|distance
	FacetBy         []string            `json:"facetBy,omitempty"`
	FacetMaxValues  int                 `json:"facetMaxValues,omitempty"`
	Oversample      float64             `json:"oversample,omitempty"`
}

// GeoFilter restricts a hybrid search to documents within a radius.
type GeoFilter struct {
	Lat          float64 `json:"lat"`
	Lng          float64 `json:"lng"`
	RadiusMeters float64 `json:"radiusMeters"`
}

// FusionSignals configures, and echoes, the "weighted" hybrid strategy.
type FusionSignals struct {
	Diversity             float64 `json:"diversity,omitempty"`
	DiversityThreshold    float64 `json:"diversityThreshold,omitempty"`
	Proximity             float64 `json:"proximity,omitempty"`
	Freshness             float64 `json:"freshness,omitempty"`
	FreshnessHalfLifeDays float64 `json:"freshnessHalfLifeDays,omitempty"`
}

// SignalBreakdown is each signal's contribution to one weighted result.
type SignalBreakdown struct {
	Base      float64 `json:"base"`
	Diversity float64 `json:"diversity,omitempty"`
	Proximity float64 `json:"proximity,omitempty"`
	Freshness float64 `json:"freshness,omitempty"`
	Final     float64 `json:"final"`
}

// HybridSearchResult is one hit of a hybrid search.
type HybridSearchResult struct {
	Document       Document `json:"document"`
	CombinedScore  float64  `json:"combinedScore"`
	FTSScore       float64  `json:"ftsScore"`
	VectorScore    float64  `json:"vectorScore"`
	DistanceMeters float64  `json:"distanceMeters,omitempty"`
	MatchedTerms   []string `json:"matchedTerms,omitempty"`
	Rank           int      `json:"rank"`
	Pinned         bool     `json:"pinned,omitempty"`
}

// HybridSearchResponse is the /v1/hybrid-search response.
type HybridSearchResponse struct {
	Results          []HybridSearchResult     `json:"results"`
	Total            int                      `json:"total"`
	Signals          *FusionSignals           `json:"signals,omitempty"`
	SignalBreakdown  []SignalBreakdown        `json:"signalBreakdown,omitempty"`
	Strategy         string                   `json:"strategy"`
	Alpha            float64                  `json:"alpha,omitempty"`
	RRFK             int                      `json:"rrfK,omitempty"`
	FTSAlgorithm     string                   `json:"ftsAlgorithm"`
	VectorAlgorithm  string                   `json:"vectorAlgorithm"`
	DistanceMetric   string                   `json:"distanceMetric"`
	ContextTruncated bool                     `json:"contextTruncated,omitempty"`
	Stats            *SearchStats             `json:"searchStats,omitempty"`
	Facets           map[string][]FacetBucket `json:"facets,omitempty"`
}
