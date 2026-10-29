package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	"mddb/internal/embedding"
	"mddb/internal/envconf"
	vec "mddb/internal/vector"
)

// Re-embedding a collection (#347).
//
// HTTP, gRPC and MCP each had a copy of this, and they had drifted: the gRPC
// one stored one whole-document vector instead of chunks and never updated
// the in-memory indexes, so its vectors were invisible to search until a
// restart; the MCP one ignored the collection's quantization. One
// implementation now serves all three, and reports its progress so a long
// reindex can run as a job.

// vectorReindexResult is what a reindex did.
type vectorReindexResult struct {
	Total       int      `json:"total"` // documents in the collection
	Embedded    int      `json:"embedded"`
	Skipped     int      `json:"skipped"`
	Failed      int      `json:"failed"`
	TotalChunks int      `json:"totalChunks"`
	Errors      []string `json:"errors"`
}

var errNoEmbeddingProvider = errors.New("no embedding provider configured")

// reindexVectors re-embeds the documents of a collection whose content
// changed since their vectors were made (all of them with force), and
// reports after each document. ctx bounds the embedding calls.
func (s *Server) reindexVectors(ctx context.Context, collection string, force bool, progress func(done int, r vectorReindexResult)) (vectorReindexResult, error) {
	var res vectorReindexResult
	if collection == "" {
		return res, errors.New("missing collection")
	}
	if s.Embedding == nil {
		return res, errNoEmbeddingProvider
	}

	chunkSize := s.chunkSizeFor(collection) // #325
	chunkEnabled := envconf.String("MDDB_EMBEDDING_CHUNK_ENABLED", "true") == "true"

	// Disk-only collections store full-precision vectors on disk (the
	// quantized form lives only in RAM), so storage quantization is off for
	// them.
	var qt vec.QuantizationType
	if s.CollectionManager != nil {
		if cfg, ok := s.CollectionManager.Get(collection); ok && cfg.Quantization != "" {
			qt = vec.ParseQuantization(cfg.Quantization)
		}
	}
	diskOnly := s.collectionDiskOnly(collection)
	if diskOnly {
		qt = vec.QuantNone
	}

	type docEntry struct {
		ID        string
		ContentMD string
		// Reindexing must segment a document exactly as the embedding
		// worker did, or stored chunk indices stop pointing at the passages
		// they named (CODE-003).
		Mode ChunkMode
	}
	var docs []docEntry
	if err := s.DBScanC(collection, s.BucketNames.Docs, []byte("doc|"+collection+"|"), func(_, v []byte) bool {
		if d, err := loadDoc(v); err == nil {
			docs = append(docs, docEntry{ID: d.ID, ContentMD: d.ContentMD, Mode: s.chunkModeFor(collection, d)})
		}
		return true
	}); err != nil {
		return res, err
	}
	res.Total = len(docs)
	res.Errors = []string{}

	budget := s.quotas().budget
	for i, d := range docs {
		// GO-043: a reindex exists to spend embedding calls, so a spent
		// budget refuses it (429) rather than parking it; one that runs out
		// part way stops and says how much is left.
		if err := budget.check(collection); err != nil {
			if i == 0 {
				return res, err
			}
			res.Errors = append(res.Errors, fmt.Sprintf("embedding budget exhausted: %d documents not reindexed until it resets", len(docs)-i))
			break
		}
		s.reindexOne(ctx, collection, d.ID, d.ContentMD, d.Mode, force, chunkEnabled, chunkSize, qt, diskOnly, &res)
		if progress != nil {
			progress(i+1, res)
		}
	}

	// Train the trainable indexes on the new vectors (disk-only collections
	// have no float32 indexes to train).
	if res.Embedded > 0 && !diskOnly {
		if records, loadErr := s.VectorStore.LoadCollection(collection); loadErr == nil {
			collVecs := make(map[string][]float32, len(records))
			for docID, rec := range records {
				collVecs[docID] = rec.Vector
			}
			for _, searcher := range s.VectorSearchers {
				if trainer, isTrainable := searcher.(vec.Trainable); isTrainable {
					go vec.TrainSafely(trainer, collection, collVecs)
				}
			}
		}
	}
	return res, nil
}

// reindexOne re-embeds one document and records the outcome in res.
func (s *Server) reindexOne(ctx context.Context, collection, docID, content string, mode ChunkMode, force, chunkEnabled bool,
	chunkSize int, qt vec.QuantizationType, diskOnly bool, res *vectorReindexResult) {
	if content == "" {
		res.Skipped++
		return
	}
	if !force {
		if existing, err := s.VectorStore.Get(collection, docID); err == nil && existing != nil &&
			existing.ContentHash == vec.ContentHash(content) {
			res.Skipped++
			return
		}
	}

	chunks := []string{content}
	if chunkEnabled {
		chunks = chunkTextsMode(content, chunkSize, mode)
	}
	if len(chunks) == 0 {
		res.Skipped++
		return
	}

	var embeddings []vec.ChunkEmbedding
	for i, chunk := range chunks {
		ectx, cancel := context.WithTimeout(ctx, 30*time.Second)
		vector, err := s.Embedding.Embed(ectx, chunk, embedding.RoleDocument)
		cancel()
		s.quotas().budget.spend(collection, chunk)
		if err != nil {
			res.Failed++
			res.Errors = append(res.Errors, fmt.Sprintf("%s chunk %d: %s", docID, i, err.Error()))
			return
		}
		embeddings = append(embeddings, vec.ChunkEmbedding{ChunkIndex: i, Vector: vector})
	}

	noteEmbeddingProvenance(s.VectorStore, collection, s.Embedding)
	if err := s.VectorStore.PutChunksQuantized(collection, docID, embeddings, s.Embedding.Model(), vec.ContentHash(content), qt); err != nil {
		res.Failed++
		res.Errors = append(res.Errors, docID+": store: "+err.Error())
		return
	}

	// A chunk an index refuses is a failed chunk (#252): stored, but no
	// search through that index finds it, so it is not counted as embedded.
	var indexErr error
	for _, ce := range embeddings {
		if err := s.indexChunk(collection, fmt.Sprintf("%s#%d", docID, ce.ChunkIndex), ce.Vector, diskOnly); err != nil && indexErr == nil {
			indexErr = fmt.Errorf("chunk %d: %w", ce.ChunkIndex, err)
		}
	}
	s.VectorStore.CleanStaleChunks(collection, docID, len(embeddings), serverIndexes{s})
	if indexErr != nil {
		res.Failed++
		res.Errors = append(res.Errors, docID+": index: "+indexErr.Error())
		return
	}
	res.Embedded++
	res.TotalChunks += len(embeddings)
}
