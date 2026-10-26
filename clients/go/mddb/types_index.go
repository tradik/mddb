package mddb

// Types for index and storage introspection: /v1/vector-stats,
// /v1/vector-reindex, /v1/code-graph and /v1/storage. Some tags are
// snake_case because the server writes them that way.

// VectorStats is the /v1/vector-stats response. Provider, Model and
// Dimensions are empty when no embedding provider is configured; Queue is nil
// when the embedding worker is not running.
type VectorStats struct {
	Enabled     bool                             `json:"enabled"`
	Provider    string                           `json:"provider,omitempty"`
	Model       string                           `json:"model,omitempty"`
	Dimensions  int                              `json:"dimensions,omitempty"`
	Collections map[string]VectorCollectionStats `json:"collections"`
	IndexReady  bool                             `json:"index_ready"`
	Chunking    VectorChunking                   `json:"chunking"`
	Queue       *EmbeddingQueue                  `json:"queue,omitempty"`
}

// VectorCollectionStats is one collection's embedding coverage.
type VectorCollectionStats struct {
	TotalDocuments    int    `json:"total_documents"`
	EmbeddedDocuments int    `json:"embedded_documents"`
	TotalChunks       int    `json:"total_chunks"`
	ChunkSize         int    `json:"chunkSize"` // effective, after the collection override
	ChunkMode         string `json:"chunkMode,omitempty"`
	Quantization      string `json:"quantization,omitempty"`
	DiskOnlyVectors   bool   `json:"diskOnlyVectors,omitempty"`
}

// VectorChunking is the server-wide chunking configuration.
type VectorChunking struct {
	Enabled   bool `json:"enabled"`
	ChunkSize int  `json:"chunkSize"`
}

// EmbeddingQueue is the embedding worker's queue state. Wait is a Go
// duration string such as "5s".
type EmbeddingQueue struct {
	Size    int    `json:"size"`
	Depth   int    `json:"depth"`
	Dropped uint64 `json:"dropped"`
	Wait    string `json:"wait"`
}

// VectorReindexRequest re-embeds a collection. Force re-embeds documents
// whose content has not changed.
type VectorReindexRequest struct {
	Collection string `json:"collection"`
	Force      bool   `json:"force,omitempty"`
}

// VectorReindexResponse counts the outcome of a reindex.
type VectorReindexResponse struct {
	Embedded    int      `json:"embedded"`
	Skipped     int      `json:"skipped"`
	Failed      int      `json:"failed"`
	TotalChunks int      `json:"totalChunks"`
	Errors      []string `json:"errors"`
}

// CodeGraphRequest walks the code connection graph from one document.
type CodeGraphRequest struct {
	Collection string `json:"collection"`
	Key        string `json:"key"`
	Direction  string `json:"direction,omitempty"` // in|out|both
	Depth      int    `json:"depth,omitempty"`
	MaxDegree  int    `json:"maxDegree,omitempty"`
	Lines      bool   `json:"lines,omitempty"` // fill CodeGraphEdge.Lines
}

// CodeGraph is the /v1/code-graph response. Truncated reports that a depth
// or degree limit cut the walk short.
type CodeGraph struct {
	Collection string          `json:"collection"`
	Root       string          `json:"root"`
	Direction  string          `json:"direction"`
	Depth      int             `json:"depth"`
	Nodes      []CodeGraphNode `json:"nodes"`
	Edges      []CodeGraphEdge `json:"edges"`
	Truncated  bool            `json:"truncated"`
}

// CodeGraphNode is a document reached by the walk.
type CodeGraphNode struct {
	Key      string `json:"key"`
	DocID    string `json:"docId"`
	Lang     string `json:"lang,omitempty"`
	Language string `json:"language,omitempty"`
	Depth    int    `json:"depth"`
}

// CodeGraphEdge is one connection and the symbol behind it. Kind is
// "uses-selector", "imports" or "calls".
type CodeGraphEdge struct {
	From      string          `json:"from"`
	To        string          `json:"to"`
	Kind      string          `json:"kind"`
	Symbol    string          `json:"symbol"`
	Direction string          `json:"direction"`
	Lines     *CodeGraphLines `json:"lines,omitempty"`
}

// CodeGraphLines is the first line of the symbol at each end of an edge.
type CodeGraphLines struct {
	FromLine int `json:"fromLine,omitempty"`
	ToLine   int `json:"toLine,omitempty"`
}

// StorageBreakdown is the GET /v1/storage response. Buckets is filled only
// for the whole-database breakdown, not for a single collection.
type StorageBreakdown struct {
	FileBytes   int64                      `json:"fileBytes"`
	PageSize    int                        `json:"pageSize"`
	FreePages   int                        `json:"freePages"`
	FreeBytes   int64                      `json:"freeBytes"`
	Buckets     map[string]BucketUsage     `json:"buckets,omitempty"`
	Collections map[string]CollectionUsage `json:"collections"`
	ElapsedMs   int64                      `json:"elapsedMs"`
}

// BucketUsage is one top-level database bucket.
type BucketUsage struct {
	AllocBytes int `json:"allocBytes"` // 0 from server 2.18.5 on (#350)
	InUseBytes int `json:"inUseBytes"`
	Keys       int `json:"keys"`
}

// CollectionUsage is the key and value bytes a collection holds. ByCategory
// groups buckets into content, metadata, fullText, vectors and other.
type CollectionUsage struct {
	TotalBytes int64            `json:"totalBytes"`
	ByBucket   map[string]int64 `json:"byBucket"`
	ByCategory map[string]int64 `json:"byCategory"`
}
