package mddb

// Document is a stored MDDB document as returned by the server.
type Document struct {
	ID        string              `json:"id"`
	Key       string              `json:"key"`
	Lang      string              `json:"lang"`
	Meta      map[string][]string `json:"meta"`
	ContentMD string              `json:"contentMd"`
	AddedAt   int64               `json:"addedAt"`
	UpdatedAt int64               `json:"updatedAt"`
	ExpiresAt int64               `json:"expiresAt,omitempty"`
	// Revision identifies this version. Add, Get and Search return it; send
	// it back as ExpectedRevision to make a write or delete conditional.
	Revision string `json:"revision,omitempty"`
}

// AddRequest adds or updates a document. TTL is in seconds (0 = no expiry).
//
// ExpectedRevision writes only over that revision; IfAbsent only when the
// document does not exist. A write that loses is an *APIError with status
// 409 whose RevisionConflict reports the current revision. The two cannot
// be combined.
type AddRequest struct {
	Collection       string              `json:"collection"`
	Key              string              `json:"key"`
	Lang             string              `json:"lang"`
	Meta             map[string][]string `json:"meta,omitempty"`
	ContentMD        string              `json:"contentMd"`
	TTL              int64               `json:"ttl,omitempty"`
	ExpectedRevision string              `json:"expectedRevision,omitempty"`
	IfAbsent         bool                `json:"ifAbsent,omitempty"`
}

// GetRequest fetches a single document. Env supplies templating variables.
type GetRequest struct {
	Collection string            `json:"collection"`
	Key        string            `json:"key"`
	Lang       string            `json:"lang"`
	Env        map[string]string `json:"env,omitempty"`
}

// SearchRequest lists documents in a collection, filtered by metadata.
type SearchRequest struct {
	Collection string              `json:"collection"`
	FilterMeta map[string][]string `json:"filterMeta,omitempty"`
	Sort       string              `json:"sort,omitempty"` // addedAt|updatedAt|key
	Asc        bool                `json:"asc,omitempty"`
	Limit      int                 `json:"limit,omitempty"`
	Offset     int                 `json:"offset,omitempty"`
}

// DeleteRequest removes a single document; with ExpectedRevision, only while
// it is still at that revision.
type DeleteRequest struct {
	Collection       string `json:"collection"`
	Key              string `json:"key"`
	Lang             string `json:"lang"`
	ExpectedRevision string `json:"expectedRevision,omitempty"`
}

// SetTTLRequest sets (ttl>0) or clears (ttl=0) a document's expiry, in seconds.
type SetTTLRequest struct {
	Collection string `json:"collection"`
	Key        string `json:"key"`
	Lang       string `json:"lang"`
	TTL        int64  `json:"ttl"`
}

// ImportURLRequest fetches a remote URL and stores it as a document.
type ImportURLRequest struct {
	Collection string              `json:"collection"`
	URL        string              `json:"url"`
	Key        string              `json:"key,omitempty"`
	Lang       string              `json:"lang,omitempty"`
	Meta       map[string][]string `json:"meta,omitempty"`
	TTL        int64               `json:"ttl,omitempty"`
}

// SchemaRequest targets a single collection's metadata schema.
type SchemaRequest struct {
	Collection string `json:"collection"`
}

// CollectionStats is one collection's row in a Stats response.
type CollectionStats struct {
	Name          string `json:"name"`
	DocumentCount int64  `json:"documentCount"`
	RevisionCount int64  `json:"revisionCount"`
	MetaIndices   int64  `json:"metaIndices"`
}

// Stats is the server's /v1/stats response.
type Stats struct {
	DatabasePath     string            `json:"databasePath"`
	DatabaseSize     int64             `json:"databaseSize"`
	Mode             string            `json:"mode"`
	TotalDocuments   int64             `json:"totalDocuments"`
	TotalRevisions   int64             `json:"totalRevisions"`
	TotalMetaIndices int64             `json:"totalMetaIndices"`
	Collections      []CollectionStats `json:"collections"`
}

// Webhook is a registered webhook subscription.
type Webhook struct {
	ID         string   `json:"id"`
	URL        string   `json:"url"`
	Events     []string `json:"events"`
	Collection string   `json:"collection,omitempty"`
}

// RegisterWebhookRequest registers a new webhook.
type RegisterWebhookRequest struct {
	URL        string   `json:"url"`
	Events     []string `json:"events"`
	Collection string   `json:"collection,omitempty"`
}
