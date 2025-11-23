package mddb

import (
	"context"
	"io"
)

// Client jest interfejsem klienta MDDB obsługującym wszystkie operacje API.
type Client interface {
	// Health sprawdza status zdrowia serwera.
	Health(ctx context.Context) (*Health, error)

	// Stats zwraca statystyki serwera i bazy danych.
	Stats(ctx context.Context) (*Stats, error)

	// Add dodaje lub aktualizuje dokument.
	Add(ctx context.Context, req *AddRequest) (*Document, error)

	// AddBatch dodaje lub aktualizuje wiele dokumentów w jednej transakcji.
	AddBatch(ctx context.Context, req *AddBatchRequest) (*AddBatchResponse, error)

	// UpdateBatch aktualizuje wiele dokumentów w jednej transakcji.
	UpdateBatch(ctx context.Context, req *UpdateBatchRequest) (*UpdateBatchResponse, error)

	// DeleteBatch usuwa wiele dokumentów w jednej transakcji.
	DeleteBatch(ctx context.Context, req *DeleteBatchRequest) (*DeleteBatchResponse, error)

	// Get pobiera dokument po kluczu i języku.
	Get(ctx context.Context, req *GetRequest) (*Document, error)

	// Search wyszukuje dokumenty z filtrowaniem i sortowaniem.
	Search(ctx context.Context, req *SearchRequest) (*SearchResponse, error)

	// Delete usuwa pojedynczy dokument.
	Delete(ctx context.Context, req *DeleteRequest) error

	// DeleteCollection usuwa całą kolekcję.
	DeleteCollection(ctx context.Context, req *DeleteCollectionRequest) (*DeleteCollectionResponse, error)

	// Export eksportuje dokumenty (zwraca stream danych).
	Export(ctx context.Context, req *ExportRequest) (io.ReadCloser, error)

	// Backup tworzy backup bazy danych.
	Backup(ctx context.Context, req *BackupRequest) (*BackupResponse, error)

	// Restore przywraca bazę danych z backupu.
	Restore(ctx context.Context, req *RestoreRequest) (*RestoreResponse, error)

	// Truncate obcina historię rewizji.
	Truncate(ctx context.Context, req *TruncateRequest) (*TruncateResponse, error)

	// Close zamyka połączenie z serwerem.
	Close() error
}
