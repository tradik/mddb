package mddb

import (
	"context"
	"io"
)

// Client is the MDDB client interface supporting all API operations.
type Client interface {
	// Health checks server health status.
	Health(ctx context.Context) (*Health, error)

	// Stats returns server and database statistics.
	Stats(ctx context.Context) (*Stats, error)

	// Add adds or updates a document.
	Add(ctx context.Context, req *AddRequest) (*Document, error)

	// AddBatch adds or updates multiple documents in one transaction.
	AddBatch(ctx context.Context, req *AddBatchRequest) (*AddBatchResponse, error)

	// UpdateBatch updates multiple documents in one transaction.
	UpdateBatch(ctx context.Context, req *UpdateBatchRequest) (*UpdateBatchResponse, error)

	// DeleteBatch deletes multiple documents in one transaction.
	DeleteBatch(ctx context.Context, req *DeleteBatchRequest) (*DeleteBatchResponse, error)

	// Get retrieves a document by key and language.
	Get(ctx context.Context, req *GetRequest) (*Document, error)

	// Search searches documents with filtering and sorting.
	Search(ctx context.Context, req *SearchRequest) (*SearchResponse, error)

	// Delete deletes a single document.
	Delete(ctx context.Context, req *DeleteRequest) error

	// DeleteCollection deletes entire collection.
	DeleteCollection(ctx context.Context, req *DeleteCollectionRequest) (*DeleteCollectionResponse, error)

	// Export exports documents (returns data stream).
	Export(ctx context.Context, req *ExportRequest) (io.ReadCloser, error)

	// Backup creates database backup.
	Backup(ctx context.Context, req *BackupRequest) (*BackupResponse, error)

	// Restore restores database from backup.
	Restore(ctx context.Context, req *RestoreRequest) (*RestoreResponse, error)

	// Truncate truncates revision history.
	Truncate(ctx context.Context, req *TruncateRequest) (*TruncateResponse, error)

	// Close closes connection to server.
	Close() error
}
