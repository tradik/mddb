package mddb

// Types for the bulk endpoints /v1/ingest, /v1/delete-batch and
// /v1/aggregate, after the server's IngestRequestHTTP, handleDeleteBatch and
// AggregateRequest.

// IngestRequest loads many documents into one collection in a single call.
type IngestRequest struct {
	Collection string           `json:"collection"`
	Documents  []IngestDocument `json:"documents"`
	Options    IngestOptions    `json:"options,omitempty"`
}

// IngestDocument is one document of an IngestRequest. Key defaults to one
// derived from URL when empty.
type IngestDocument struct {
	URL                string              `json:"url"`
	Key                string              `json:"key,omitempty"`
	Lang               string              `json:"lang"`
	ContentMD          string              `json:"contentMd"`
	Meta               map[string][]string `json:"meta,omitempty"`
	ExtractFrontmatter bool                `json:"extractFrontmatter,omitempty"`
	ScrapedAt          int64               `json:"scrapedAt,omitempty"`
	Scraper            string              `json:"scraper,omitempty"`
	TTL                int64               `json:"ttl,omitempty"` // seconds; 0 = no expiry
	// Format and Content send a file for the server to convert to Markdown
	// (v2.18.9+), with the converters /v1/upload uses. Content travels as
	// base64; Format ("pdf", "docx", "html", ...) defaults to Key's extension.
	Format  string `json:"format,omitempty"`
	Content []byte `json:"contentBase64,omitempty"`
}

// IngestOptions turns optional ingest steps off. Profile names a preset of
// the flags ("" or "default", "fast"); a flag set explicitly overrides it.
type IngestOptions struct {
	SkipDuplicates          bool   `json:"skipDuplicates,omitempty"`
	SkipEmbeddings          bool   `json:"skipEmbeddings,omitempty"`
	SkipFTS                 bool   `json:"skipFts,omitempty"`
	SkipWebhooks            bool   `json:"skipWebhooks,omitempty"`
	AutoConfigureCollection bool   `json:"autoConfigureCollection,omitempty"`
	SaveRevision            bool   `json:"saveRevision,omitempty"`
	Profile                 string `json:"profile,omitempty"`
	TextOnly                bool   `json:"textOnly,omitempty"`
}

// IngestResponse reports what an ingest did.
type IngestResponse struct {
	Added         int      `json:"added"`
	Updated       int      `json:"updated"`
	Skipped       int      `json:"skipped"`
	Failed        int      `json:"failed"`
	Errors        []string `json:"errors,omitempty"`
	Collection    string   `json:"collection"`
	DurationMs    int64    `json:"durationMs"`
	Profile       string   `json:"profile,omitempty"`       // profile that applied
	Sanitized     int      `json:"sanitized,omitempty"`     // documents with invalid UTF-8 dropped
	KeyCollisions []string `json:"keyCollisions,omitempty"` // keys differing only in case
	// Conversions lists each document converted from a file, with how well
	// it converted (v2.18.9+).
	Conversions []IngestConversion `json:"conversions,omitempty"`
}

// IngestConversion is one document converted from a file: the format it came
// from, a heuristic confidence from 0 to 1, and what lowered it.
type IngestConversion struct {
	Key        string   `json:"key"`
	Format     string   `json:"format"`
	Confidence float64  `json:"conversionConfidence"`
	Warnings   []string `json:"conversionWarnings,omitempty"`
}

// DocumentRef names one document of a collection.
type DocumentRef struct {
	Key  string `json:"key"`
	Lang string `json:"lang"`
}

// DeleteBatchRequest deletes many documents of one collection.
type DeleteBatchRequest struct {
	Collection string        `json:"collection"`
	Documents  []DocumentRef `json:"documents"`
}

// DeleteBatchResponse counts the outcome of a batch delete.
type DeleteBatchResponse struct {
	Deleted  int      `json:"deleted"`
	NotFound int      `json:"not_found"` // the server's tag is snake_case
	Failed   int      `json:"failed"`
	Errors   []string `json:"errors"`
}

// AggregateRequest asks for facet counts and date histograms over a
// collection, optionally pre-filtered by metadata.
type AggregateRequest struct {
	Collection   string              `json:"collection"`
	FilterMeta   map[string][]string `json:"filterMeta,omitempty"`
	Facets       []FacetRequest      `json:"facets,omitempty"`
	Histograms   []HistogramRequest  `json:"histograms,omitempty"`
	MaxFacetSize int                 `json:"maxFacetSize,omitempty"` // default 50
}

// FacetRequest asks for value counts of a metadata key.
type FacetRequest struct {
	Field   string `json:"field"`
	OrderBy string `json:"orderBy,omitempty"` // count|value
}

// HistogramRequest asks for a histogram of addedAt or updatedAt.
type HistogramRequest struct {
	Field    string `json:"field"`
	Interval string `json:"interval,omitempty"` // day|week|month|year
}

// FacetBucket is one value and its count.
type FacetBucket struct {
	Value string `json:"value"`
	Count int    `json:"count"`
}

// HistogramBucket is one time range and its count; To is exclusive.
type HistogramBucket struct {
	Key   string `json:"key"`
	From  int64  `json:"from"`
	To    int64  `json:"to"`
	Count int    `json:"count"`
}

// AggregateResponse is the /v1/aggregate response.
type AggregateResponse struct {
	Collection string                       `json:"collection"`
	TotalDocs  int                          `json:"totalDocs"`
	Facets     map[string][]FacetBucket     `json:"facets,omitempty"`
	Histograms map[string][]HistogramBucket `json:"histograms,omitempty"`
	DurationMs int64                        `json:"durationMs"`
}
