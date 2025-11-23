package mddb

import "time"

// Document reprezentuje dokument markdown w MDDB.
type Document struct {
	ID        string              `json:"id"`
	Key       string              `json:"key"`
	Lang      string              `json:"lang"`
	Meta      map[string][]string `json:"meta"`
	ContentMD string              `json:"content_md"`
	AddedAt   time.Time           `json:"added_at"`
	UpdatedAt time.Time           `json:"updated_at"`
}

// Health reprezentuje status zdrowia serwera.
type Health struct {
	Status string `json:"status"`
	Mode   string `json:"mode"`
}

// Stats reprezentuje statystyki serwera.
type Stats struct {
	DatabasePath     string            `json:"database_path"`
	DatabaseSize     int64             `json:"database_size"`
	Mode             string            `json:"mode"`
	Collections      []CollectionStats `json:"collections"`
	TotalDocuments   int               `json:"total_documents"`
	TotalRevisions   int               `json:"total_revisions"`
	TotalMetaIndices int               `json:"total_meta_indices"`
}

// CollectionStats reprezentuje statystyki kolekcji.
type CollectionStats struct {
	Name           string `json:"name"`
	DocumentCount  int    `json:"document_count"`
	RevisionCount  int    `json:"revision_count"`
	MetaIndexCount int    `json:"meta_index_count"`
}

// AddRequest reprezentuje żądanie dodania/aktualizacji dokumentu.
type AddRequest struct {
	Collection   string              `json:"collection"`
	Key          string              `json:"key"`
	Lang         string              `json:"lang"`
	Meta         map[string][]string `json:"meta"`
	ContentMD    string              `json:"content_md"`
	SaveRevision bool                `json:"save_revision"`
}

// GetRequest reprezentuje żądanie pobrania dokumentu.
type GetRequest struct {
	Collection string            `json:"collection"`
	Key        string            `json:"key"`
	Lang       string            `json:"lang"`
	Env        map[string]string `json:"env,omitempty"`
}

// SearchRequest reprezentuje żądanie wyszukiwania.
type SearchRequest struct {
	Collection string              `json:"collection"`
	FilterMeta map[string][]string `json:"filter_meta,omitempty"`
	Sort       string              `json:"sort,omitempty"`
	Asc        bool                `json:"asc,omitempty"`
	Limit      int                 `json:"limit,omitempty"`
	Offset     int                 `json:"offset,omitempty"`
}

// SearchResponse reprezentuje wynik wyszukiwania.
type SearchResponse struct {
	Documents []Document `json:"documents"`
	Total     int        `json:"total"`
}

// DeleteRequest reprezentuje żądanie usunięcia dokumentu.
type DeleteRequest struct {
	Collection string `json:"collection"`
	Key        string `json:"key"`
	Lang       string `json:"lang"`
}

// DeleteCollectionRequest reprezentuje żądanie usunięcia kolekcji.
type DeleteCollectionRequest struct {
	Collection string `json:"collection"`
}

// DeleteCollectionResponse reprezentuje wynik usunięcia kolekcji.
type DeleteCollectionResponse struct {
	Deleted int `json:"deleted"`
}

// BatchDocument reprezentuje dokument w operacji batch.
type BatchDocument struct {
	Key          string              `json:"key"`
	Lang         string              `json:"lang"`
	Meta         map[string][]string `json:"meta"`
	ContentMD    string              `json:"content_md"`
	SaveRevision bool                `json:"save_revision"`
}

// AddBatchRequest reprezentuje żądanie dodania wielu dokumentów.
type AddBatchRequest struct {
	Collection string          `json:"collection"`
	Documents  []BatchDocument `json:"documents"`
}

// AddBatchResponse reprezentuje wynik dodania wielu dokumentów.
type AddBatchResponse struct {
	Added   int      `json:"added"`
	Updated int      `json:"updated"`
	Failed  int      `json:"failed"`
	Errors  []string `json:"errors,omitempty"`
}

// UpdateDocument reprezentuje dokument do aktualizacji.
type UpdateDocument struct {
	Key          string              `json:"key"`
	Lang         string              `json:"lang"`
	Meta         map[string][]string `json:"meta"`
	ContentMD    string              `json:"content_md"`
	SaveRevision bool                `json:"save_revision"`
}

// UpdateBatchRequest reprezentuje żądanie aktualizacji wielu dokumentów.
type UpdateBatchRequest struct {
	Collection string           `json:"collection"`
	Documents  []UpdateDocument `json:"documents"`
}

// UpdateBatchResponse reprezentuje wynik aktualizacji wielu dokumentów.
type UpdateBatchResponse struct {
	Updated  int      `json:"updated"`
	NotFound int      `json:"not_found"`
	Failed   int      `json:"failed"`
	Errors   []string `json:"errors,omitempty"`
}

// DeleteDocument reprezentuje dokument do usunięcia.
type DeleteDocument struct {
	Key  string `json:"key"`
	Lang string `json:"lang"`
}

// DeleteBatchRequest reprezentuje żądanie usunięcia wielu dokumentów.
type DeleteBatchRequest struct {
	Collection string           `json:"collection"`
	Documents  []DeleteDocument `json:"documents"`
}

// DeleteBatchResponse reprezentuje wynik usunięcia wielu dokumentów.
type DeleteBatchResponse struct {
	Deleted  int      `json:"deleted"`
	NotFound int      `json:"not_found"`
	Failed   int      `json:"failed"`
	Errors   []string `json:"errors,omitempty"`
}

// ExportRequest reprezentuje żądanie eksportu.
type ExportRequest struct {
	Collection string              `json:"collection"`
	FilterMeta map[string][]string `json:"filter_meta,omitempty"`
	Format     string              `json:"format"` // ndjson, zip
}

// BackupRequest reprezentuje żądanie backupu.
type BackupRequest struct {
	To string `json:"to"`
}

// BackupResponse reprezentuje wynik backupu.
type BackupResponse struct {
	Backup string `json:"backup"`
}

// RestoreRequest reprezentuje żądanie przywrócenia z backupu.
type RestoreRequest struct {
	From string `json:"from"`
}

// RestoreResponse reprezentuje wynik przywrócenia.
type RestoreResponse struct {
	Restored string `json:"restored"`
}

// TruncateRequest reprezentuje żądanie obcięcia historii rewizji.
type TruncateRequest struct {
	Collection string `json:"collection"`
	KeepRevs   int    `json:"keep_revs"`
	DropCache  bool   `json:"drop_cache"`
}

// TruncateResponse reprezentuje wynik obcięcia.
type TruncateResponse struct {
	Status string `json:"status"`
}
