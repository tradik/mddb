package main

import (
	"errors"

	"mddb/internal/envconf"
	"mddb/internal/storage"
)

var (
	errInvalidChunkSize = errors.New("embedding.chunkSize must be 0 (server default) or 100-100000")
	errInvalidChunkMode = errors.New(`embedding.chunkMode must be "", "code" or "prose"`)
)

// Chunking per collection (#325).
//
// Chunk size and mode were server-wide, so one instance holding long C
// functions and short articles had to pick one size for both: 1500
// characters is right for prose and cuts a function in half. A collection
// can now set its own; the server-wide variables remain the default.
//
// The size and mode have to be the same wherever chunks are derived — when a
// document is embedded, on vector-reindex, and when a search re-derives the
// passage a hit came from — because chunks are not stored, only their index.
// Every one of those paths asks these two functions.

// EmbeddingChunkingDef is a collection's chunking for embeddings.
type EmbeddingChunkingDef struct {
	// ChunkSize is the target chunk length in characters; 0 uses
	// MDDB_EMBEDDING_CHUNK_SIZE.
	ChunkSize int `json:"chunkSize,omitempty"`
	// ChunkMode is "code" or "prose"; empty uses MDDB_EMBEDDING_CHUNK_MODE,
	// then each document's kind.
	ChunkMode string `json:"chunkMode,omitempty"`
}

// chunkSizeFor is the chunk size for a collection: its own, else the server's.
func (s *Server) chunkSizeFor(collection string) int {
	if cfg := s.collectionChunking(collection); cfg != nil && cfg.ChunkSize > 0 {
		return cfg.ChunkSize
	}
	return envconf.Int("MDDB_EMBEDDING_CHUNK_SIZE", 1500)
}

// chunkModeFor is how a document is segmented: the collection's mode, else
// the server's override, else the document's own kind.
func (s *Server) chunkModeFor(collection string, doc *storage.Doc) ChunkMode {
	if cfg := s.collectionChunking(collection); cfg != nil {
		switch ChunkMode(cfg.ChunkMode) {
		case ChunkModeCode, ChunkModeProse:
			return ChunkMode(cfg.ChunkMode)
		}
	}
	return ChunkModeFor(doc)
}

func (s *Server) collectionChunking(collection string) *EmbeddingChunkingDef {
	if s.CollectionManager == nil {
		return nil
	}
	cfg, ok := s.CollectionManager.Get(collection)
	if !ok {
		return nil
	}
	return cfg.Embedding
}

// validateChunking rejects a chunking setting the chunker cannot honour.
func validateChunking(c *EmbeddingChunkingDef) error {
	if c == nil {
		return nil
	}
	if c.ChunkSize < 0 || (c.ChunkSize > 0 && c.ChunkSize < 100) || c.ChunkSize > 100000 {
		return errInvalidChunkSize
	}
	switch c.ChunkMode {
	case "", string(ChunkModeCode), string(ChunkModeProse):
		return nil
	}
	return errInvalidChunkMode
}
