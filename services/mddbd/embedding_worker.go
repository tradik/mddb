package main

import (
	"context"
	"fmt"
	"log/slog"
	"mddb/internal/embedding"
	"mddb/internal/envconf"
	"mddb/internal/metrics"
	vec "mddb/internal/vector"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// EmbeddingJob represents a document that needs embedding.
type EmbeddingJob struct {
	Collection string
	DocID      string
	ContentMD  string
	// ChunkMode must match what retrieval uses to re-derive the passage
	// (CODE-003). Chunks are not stored, only their index — so if the two
	// disagree, chunk 3 at read time is not chunk 3 at write time and every
	// passage is wrong.
	ChunkMode ChunkMode
}

// EmbeddingWorker processes embedding jobs asynchronously.
type EmbeddingWorker struct {
	provider     embedding.Provider
	vectorStore  *vec.VectorStore
	vectorIndex  *vec.VectorIndex
	jobs         chan EmbeddingJob
	wg           sync.WaitGroup
	stopCh       chan struct{}
	chunkSize    int
	chunkEnabled bool
	metrics      *metrics.Metrics

	// enqueueWait is how long a writer waits for room in a full queue before
	// the job is abandoned. It is the difference between an import that takes
	// longer than expected and one that quietly embeds a fraction of what it
	// wrote (#232): a full queue drains continuously, so waiting turns a bulk
	// load into backpressure rather than loss. Zero restores the old
	// drop-immediately behaviour for anyone who wants ingest latency fixed.
	enqueueWait time.Duration

	// dropped counts jobs the queue could not take. It exists because the
	// information was being computed and thrown away: Enqueue has always
	// returned false on a drop, no caller has ever read it, and the only
	// trace was a log line. A number an operator can read — in
	// /v1/vector-stats — is what makes "1,002 of 4,000 embedded" a state
	// rather than a mystery.
	dropped atomic.Uint64

	// Disk-only support: when isDiskOnly reports true for a collection, the
	// full-precision vector is NOT added to the float32 index — only the
	// quantized index keeps an in-memory representation.
	quantIndex *vec.QuantizedVectorIndex
	isDiskOnly func(collection string) bool
}

// SetDiskOnly wires the quantized index and the disk-only predicate so the
// worker can route freshly embedded vectors to the right in-memory index.
func (w *EmbeddingWorker) SetDiskOnly(quantIndex *vec.QuantizedVectorIndex, isDiskOnly func(string) bool) {
	w.quantIndex = quantIndex
	w.isDiskOnly = isDiskOnly
}

// NewEmbeddingWorker creates a new background embedding worker.
func NewEmbeddingWorker(provider embedding.Provider, store *vec.VectorStore, index *vec.VectorIndex, bufferSize int) *EmbeddingWorker {
	return &EmbeddingWorker{
		provider:     provider,
		vectorStore:  store,
		vectorIndex:  index,
		jobs:         make(chan EmbeddingJob, bufferSize),
		stopCh:       make(chan struct{}),
		chunkSize:    envconf.Int("MDDB_EMBEDDING_CHUNK_SIZE", 1500),
		chunkEnabled: envconf.String("MDDB_EMBEDDING_CHUNK_ENABLED", "true") == "true",
		enqueueWait:  embeddingEnqueueWait(),
	}
}

// EmbeddingQueueSize is how many pending jobs the worker will hold.
//
// It was 1000, hardcoded at both construction sites, which is fine until an
// import is larger than it — and then there was no lever at all.
func EmbeddingQueueSize() int {
	size := envconf.Int("MDDB_EMBEDDING_QUEUE_SIZE", 1000)
	if size < 1 {
		size = 1
	}
	return size
}

// embeddingEnqueueWait reads how long a writer may wait for queue space.
//
// The default is deliberately not zero. A document written without its vector
// is invisible to every kind of search MDDB offers, and nothing about the
// write says so — the failure is silent in the one place it can least afford
// to be. Waiting makes a large import slower and complete; MDDB_EMBEDDING_
// QUEUE_WAIT=0 restores the previous behaviour for an operator who would
// rather have the latency and watch the counter.
func embeddingEnqueueWait() time.Duration {
	raw := strings.TrimSpace(envconf.String("MDDB_EMBEDDING_QUEUE_WAIT", "5s"))
	if raw == "" {
		return 5 * time.Second
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d < 0 {
		slog.Warn("MDDB_EMBEDDING_QUEUE_WAIT is not a duration; using the default",
			"value", raw, "default", "5s")
		return 5 * time.Second
	}
	return d
}

// QueueStats reports what the queue is holding and what it has lost.
func (w *EmbeddingWorker) QueueStats() (size, depth int, dropped uint64, wait time.Duration) {
	return cap(w.jobs), len(w.jobs), w.dropped.Load(), w.enqueueWait
}

// Start begins processing embedding jobs in the background.
func (w *EmbeddingWorker) Start(workers int) {
	for i := 0; i < workers; i++ {
		w.wg.Add(1)
		go w.worker(i)
	}
	slog.Info("embedding worker started",
		"workers", workers, "buffer", cap(w.jobs), "chunking", w.chunkEnabled, "chunkSize", w.chunkSize)
}

// Stop gracefully stops the worker.
func (w *EmbeddingWorker) Stop() {
	close(w.stopCh)
	w.wg.Wait()
}

// Enqueue adds an embedding job to the queue, waiting for room if the queue is
// full. Returns false only when the job was abandoned, which the caller is
// expected to report rather than ignore (#232).
func (w *EmbeddingWorker) Enqueue(job EmbeddingJob) bool {
	// Fast path: room now, which is every write on an idle server.
	select {
	case w.jobs <- job:
		return true
	default:
	}

	if w.enqueueWait > 0 {
		timer := time.NewTimer(w.enqueueWait)
		defer timer.Stop()
		select {
		case w.jobs <- job:
			return true
		case <-timer.C:
		case <-w.stopCh:
			// Shutting down: the job is not going to be processed, and
			// blocking here would hold up the stop.
		}
	}

	return w.dropJob(job)
}

// dropJob records an abandoned job and always reports failure.
func (w *EmbeddingWorker) dropJob(job EmbeddingJob) bool {
	w.dropped.Add(1)
	if w.metrics != nil {
		w.metrics.IncOp("embedding", "dropped")
	}
	slog.Warn("embedding queue full, dropping job — this document will have no vector until it is reindexed",
		"collection", job.Collection, "docID", job.DocID,
		"queueSize", cap(w.jobs), "waited", w.enqueueWait, "droppedTotal", w.dropped.Load())
	return false
}

func (w *EmbeddingWorker) worker(id int) {
	defer w.wg.Done()

	for {
		select {
		case <-w.stopCh:
			// Drain remaining jobs
			for {
				select {
				case job := <-w.jobs:
					w.processJob(job)
				default:
					return
				}
			}
		case job := <-w.jobs:
			w.processJob(job)
		}
	}
}

func (w *EmbeddingWorker) processJob(job EmbeddingJob) {
	contentHash := vec.ContentHash(job.ContentMD)

	// Check if content already has a matching embedding
	existing, err := w.vectorStore.Get(job.Collection, job.DocID)
	if err == nil && existing != nil && existing.ContentHash == contentHash {
		return // embedding is up-to-date
	}

	// Split into chunks if enabled
	var chunks []string
	if w.chunkEnabled {
		chunks = chunkTextsMode(job.ContentMD, w.chunkSize, job.ChunkMode)
	} else {
		chunks = []string{job.ContentMD}
	}

	if len(chunks) == 0 {
		return
	}

	// RAG-003: reuse the vectors of chunks whose text did not change.
	//
	// The document-level check above only helps when nothing changed at all.
	// Editing one paragraph of a fifty-chunk document changed the document
	// hash and re-embedded all fifty — at full provider cost — even though
	// forty-nine were identical. Keyed by chunk hash, so a chunk that merely
	// shifted position is still recognised.
	reusable := w.vectorStore.ChunkVectorsByHash(job.Collection, job.DocID)

	// Generate embedding for each chunk
	var chunkEmbeddings []vec.ChunkEmbedding
	var reusedCount int
	for i, chunk := range chunks {
		chunkHash := vec.ContentHash(chunk)
		if cached, ok := reusable[chunkHash]; ok {
			chunkEmbeddings = append(chunkEmbeddings, vec.ChunkEmbedding{
				ChunkIndex: i,
				Vector:     cached,
				ChunkHash:  chunkHash,
			})
			reusedCount++
			continue
		}

		var vector []float32
		var embedErr error
		for attempt := 0; attempt < 3; attempt++ {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			vector, embedErr = w.provider.Embed(ctx, chunk, embedding.RoleDocument)
			cancel()
			if embedErr == nil {
				break
			}
			slog.Warn("embedding attempt failed", "attempt", attempt+1, "collection", job.Collection, "docID", job.DocID, "i", i, "err", embedErr)
			time.Sleep(time.Duration(attempt+1) * time.Second)
		}

		if embedErr != nil {
			slog.Error("embedding failed after all attempts", "collection", job.Collection, "docID", job.DocID, "i", i, "err", embedErr)
			if w.metrics != nil {
				w.metrics.IncOp("embedding", "error")
			}
			return
		}

		chunkEmbeddings = append(chunkEmbeddings, vec.ChunkEmbedding{
			ChunkIndex: i,
			Vector:     vector,
			ChunkHash:  chunkHash,
		})
	}

	if reusedCount > 0 {
		slog.Debug("reused unchanged chunk embeddings",
			"collection", job.Collection, "docID", job.DocID,
			"reused", reusedCount, "embedded", len(chunks)-reusedCount)
	}

	// Store all chunks in BoltDB
	noteEmbeddingProvenance(w.vectorStore, job.Collection, w.provider)
	if err := w.vectorStore.PutChunks(job.Collection, job.DocID, chunkEmbeddings, w.provider.Model(), contentHash); err != nil {
		slog.Error("failed to store embedding", "collection", job.Collection, "docID", job.DocID, "err", err)
		return
	}

	// Update in-memory index. Disk-only collections keep RAM quantized-only:
	// the full vector stays on disk and only the quantized index is updated.
	diskOnly := w.isDiskOnly != nil && w.isDiskOnly(job.Collection)
	for _, ce := range chunkEmbeddings {
		chunkKey := fmt.Sprintf("%s#%d", job.DocID, ce.ChunkIndex)
		if diskOnly {
			if w.quantIndex != nil {
				w.quantIndex.Add(job.Collection, chunkKey, ce.Vector)
			}
			continue
		}
		w.vectorIndex.Add(job.Collection, chunkKey, ce.Vector)
	}

	// Clean stale chunks from index (if document shrank)
	w.vectorStore.CleanStaleChunks(job.Collection, job.DocID, len(chunkEmbeddings), w.vectorIndex)

	if w.metrics != nil {
		w.metrics.IncOp("embedding", "completed")
	}
}
