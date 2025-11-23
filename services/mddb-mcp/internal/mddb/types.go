package mddb

import "time"

// Document represents a markdown document in MDDB.
type Document struct {
	ID        string              `json:"id"`
	Key       string              `json:"key"`
	Lang      string              `json:"lang"`
	Meta      map[string][]string `json:"meta"`
	ContentMD string              `json:"content_md"`
	AddedAt   time.Time           `json:"added_at"`
	UpdatedAt time.Time           `json:"updated_at"`
}

// Health represents server health status.
type Health struct {
	Status string `json:"status"`
	Mode   string `json:"mode"`
}

// Stats represents server statistics.
type Stats struct {
	DatabasePath     string            `json:"database_path"`
	DatabaseSize     int64             `json:"database_size"`
	Mode             string            `json:"mode"`
	Collections      []CollectionStats `json:"collections"`
	TotalDocuments   int               `json:"total_documents"`
	TotalRevisions   int               `json:"total_revisions"`
	TotalMetaIndices int               `json:"total_meta_indices"`
}

// CollectionStats represents collection statistics.
type CollectionStats struct {
	Name           string `json:"name"`
	DocumentCount  int    `json:"document_count"`
	RevisionCount  int    `json:"revision_count"`
	MetaIndexCount int    `json:"meta_index_count"`
}

// AddRequest represents request to add/update a document.
type AddRequest struct {
	Collection   string              `json:"collection"`
	Key          string              `json:"key"`
	Lang         string              `json:"lang"`
	Meta         map[string][]string `json:"meta"`
	ContentMD    string              `json:"content_md"`
	SaveRevision bool                `json:"save_revision"`
}

// GetRequest represents request to get a document.
type GetRequest struct {
	Collection string            `json:"collection"`
	Key        string            `json:"key"`
	Lang       string            `json:"lang"`
	Env        map[string]string `json:"env,omitempty"`
}

// SearchRequest represents search request.
type SearchRequest struct {
	Collection string              `json:"collection"`
	FilterMeta map[string][]string `json:"filter_meta,omitempty"`
	Sort       string              `json:"sort,omitempty"`
	Asc        bool                `json:"asc,omitempty"`
	Limit      int                 `json:"limit,omitempty"`
	Offset     int                 `json:"offset,omitempty"`
}

// SearchResponse represents search result.
type SearchResponse struct {
	Documents []Document `json:"documents"`
	Total     int        `json:"total"`
}

// DeleteRequest represents request to delete a document.
type DeleteRequest struct {
	Collection string `json:"collection"`
	Key        string `json:"key"`
	Lang       string `json:"lang"`
}

// DeleteCollectionRequest represents request to delete a collection.
type DeleteCollectionRequest struct {
	Collection string `json:"collection"`
}

// DeleteCollectionResponse represents result of collection deletion.
type DeleteCollectionResponse struct {
	Deleted int `json:"deleted"`
}

// BatchDocument represents a document in batch operation.
type BatchDocument struct {
	Key          string              `json:"key"`
	Lang         string              `json:"lang"`
	Meta         map[string][]string `json:"meta"`
	ContentMD    string              `json:"content_md"`
	SaveRevision bool                `json:"save_revision"`
}

// AddBatchRequest represents request to add multiple documents.
type AddBatchRequest struct {
	Collection string          `json:"collection"`
	Documents  []BatchDocument `json:"documents"`
}

// AddBatchResponse represents result of adding multiple documents.
type AddBatchResponse struct {
	Added   int      `json:"added"`
	Updated int      `json:"updated"`
	Failed  int      `json:"failed"`
	Errors  []string `json:"errors,omitempty"`
}

// UpdateDocument represents a document to update.
type UpdateDocument struct {
	Key          string              `json:"key"`
	Lang         string              `json:"lang"`
	Meta         map[string][]string `json:"meta"`
	ContentMD    string              `json:"content_md"`
	SaveRevision bool                `json:"save_revision"`
}

// UpdateBatchRequest represents request to update multiple documents.
type UpdateBatchRequest struct {
	Collection string           `json:"collection"`
	Documents  []UpdateDocument `json:"documents"`
}

// UpdateBatchResponse represents result of updating multiple documents.
type UpdateBatchResponse struct {
	Updated  int      `json:"updated"`
	NotFound int      `json:"not_found"`
	Failed   int      `json:"failed"`
	Errors   []string `json:"errors,omitempty"`
}

// DeleteDocument represents a document to delete.
type DeleteDocument struct {
	Key  string `json:"key"`
	Lang string `json:"lang"`
}

// DeleteBatchRequest represents request to delete multiple documents.
type DeleteBatchRequest struct {
	Collection string           `json:"collection"`
	Documents  []DeleteDocument `json:"documents"`
}

// DeleteBatchResponse represents result of deleting multiple documents.
type DeleteBatchResponse struct {
	Deleted  int      `json:"deleted"`
	NotFound int      `json:"not_found"`
	Failed   int      `json:"failed"`
	Errors   []string `json:"errors,omitempty"`
}

// ExportRequest represents export request.
type ExportRequest struct {
	Collection string              `json:"collection"`
	FilterMeta map[string][]string `json:"filter_meta,omitempty"`
	Format     string              `json:"format"` // ndjson, zip
}

// BackupRequest represents backup request.
type BackupRequest struct {
	To string `json:"to"`
}

// BackupResponse represents backup result.
type BackupResponse struct {
	Backup string `json:"backup"`
}

// RestoreRequest represents restore from backup request.
type RestoreRequest struct {
	From string `json:"from"`
}

// RestoreResponse represents restore result.
type RestoreResponse struct {
	Restored string `json:"restored"`
}

// TruncateRequest represents request to truncate revision history.
type TruncateRequest struct {
	Collection string `json:"collection"`
	KeepRevs   int    `json:"keep_revs"`
	DropCache  bool   `json:"drop_cache"`
}

// TruncateResponse represents truncate result.
type TruncateResponse struct {
	Status string `json:"status"`
}
