package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"mddb/internal/embedding"
	"mddb/internal/envconf"
	"mddb/internal/metrics"
	"mddb/internal/pending"
	"mddb/internal/tracing"
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
	// ChunkSize is the collection's chunk size (#325); 0 uses the worker's.
	ChunkSize int
	// Summary is the document's client-written summary (RAG-009), embedded
	// as a vector of its own after the chunks; "" removes one it had.
	Summary string
	// Image is true for an image document (RAG-010): its picture is embedded
	// after the summary, when the provider embeds images.
	Image bool
	// trace is the span of the request that queued the job, so its
	// ingest.embed span joins that request's trace (OPS-024). Zero = none.
	trace tracing.SpanContext
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
	tracer       *tracing.Tracer // OPS-024; nil = tracing off

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

	// pending tracks jobs from Enqueue to the end of processing, per
	// collection and per document, so a search can report or wait for them
	// (GO-042). The channel alone knows only its global length.
	pending *pending.Tracker

	// indexer receives every chunk the worker embeds, and drops the chunks a
	// shrunken document no longer has. The server sets it to all of its
	// indexes; left alone it is the flat index, which is what tests of the
	// worker in isolation want.
	indexer chunkIndexer

	// budget is the server's per-collection embedding budget (GO-043); nil
	// when nothing set it, which is every worker built outside a server.
	// Jobs of a collection that spent its budget are parked here, still
	// pending, until releaseParked finds room for them.
	budget    *embeddingBudget
	parkMu    sync.Mutex
	parked    []EmbeddingJob
	parkTimer *time.Timer
}

// jobOutcome is what processing a job came to.
type jobOutcome int

const (
	jobDone   jobOutcome = iota // embedded, or nothing to embed
	jobFailed                   // left without the vectors it should have
	jobParked                   // waiting for the collection's embedding budget
)

// chunkIndexer is where freshly embedded chunks go.
// documentChecker is implemented by an indexer that can tell whether a
// document still exists; the worker asks before storing vectors.
type documentChecker interface {
	DocumentExists(collection, docID string) bool
}

type chunkIndexer interface {
	IndexChunk(collection, chunkKey string, vector []float32) error
	Remove(collection, chunkKey string)
}

// flatIndexer is the worker's view of a lone flat index.
type flatIndexer struct{ index *vec.VectorIndex }

func (f flatIndexer) IndexChunk(collection, chunkKey string, vector []float32) error {
	return f.index.Add(collection, chunkKey, vector)
}

func (f flatIndexer) Remove(collection, chunkKey string) { f.index.Remove(collection, chunkKey) }

// SetIndexer replaces where embedded chunks go. The server passes every index
// it keeps, so a document embedded after startup reaches HNSW, IVF and the
// rest instead of the flat index alone.
func (w *EmbeddingWorker) SetIndexer(ix chunkIndexer) {
	w.indexer = ix
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
		indexer:      flatIndexer{index},
		pending:      pending.New(),
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
	// Parked jobs are abandoned with the worker; nothing should wake for them.
	w.parkMu.Lock()
	if w.parkTimer != nil {
		w.parkTimer.Stop()
		w.parkTimer = nil
	}
	w.parkMu.Unlock()
}

// Enqueue adds an embedding job to the queue, waiting for room if the queue is
// full. Returns false only when the job was abandoned, which the caller is
// expected to report rather than ignore (#232).
func (w *EmbeddingWorker) Enqueue(job EmbeddingJob) bool {
	// Counted before the send, so a worker that finishes first cannot record
	// the end of a job the tracker has not seen start.
	w.pending.Add(job.Collection, job.DocID)
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
	w.pending.Done(job.Collection, job.DocID, true)
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
					w.run(job)
				default:
					return
				}
			}
		case job := <-w.jobs:
			w.run(job)
		}
	}
}

// run processes one job and records its end with the tracker. A parked job
// has not ended: it stays pending until it is run again.
//
// A job queued by a traced request records an ingest.embed span in that
// request's trace (OPS-024), ending after the response did.
func (w *EmbeddingWorker) run(job EmbeddingJob) {
	sp := w.tracer.Child(job.trace, spanIngestEmbed)
	sp.SetString("mddb.collection", job.Collection)
	sp.SetString("mddb.document.id", job.DocID)
	outcome := w.processJob(job)
	defer sp.End()
	switch outcome {
	case jobParked:
		sp.SetString("mddb.embedding.outcome", "parked")
		w.park(job)
	case jobFailed:
		sp.SetError(errEmbeddingJobFailed)
		w.pending.Done(job.Collection, job.DocID, true)
	default:
		w.pending.Done(job.Collection, job.DocID, false)
	}
}

// park holds a job until its collection's budget allows it, and makes sure
// something will look at it again.
func (w *EmbeddingWorker) park(job EmbeddingJob) {
	w.parkMu.Lock()
	defer w.parkMu.Unlock()
	w.parked = append(w.parked, job)
	if w.parkTimer == nil {
		w.parkTimer = time.AfterFunc(w.budget.nextCheck(), w.releaseParked)
	}
}

// releaseParked puts the parked jobs whose collections have budget again back
// on the queue, and schedules another look for the rest: at midnight UTC, or
// sooner, so a raised quota does not wait for it.
func (w *EmbeddingWorker) releaseParked() {
	w.parkMu.Lock()
	var ready, still []EmbeddingJob
	for _, job := range w.parked {
		if w.budget.exhausted(job.Collection) == nil {
			ready = append(ready, job)
		} else {
			still = append(still, job)
		}
	}
	w.parked = still
	w.parkTimer = nil
	if len(still) > 0 {
		w.parkTimer = time.AfterFunc(w.budget.nextCheck(), w.releaseParked)
	}
	w.parkMu.Unlock()
	for _, job := range ready {
		select {
		case w.jobs <- job:
		case <-w.stopCh:
			return
		}
	}
}

// Pending is the worker's in-flight job tracker (GO-042).
func (w *EmbeddingWorker) Pending() *pending.Tracker { return w.pending }

// processJob embeds one document. It reports jobFailed when the document was
// left without the vectors it should have: the provider failed or storing
// failed. Nothing to do — unchanged content, no chunks, a deleted document —
// is jobDone. jobParked means the collection has spent its embedding budget
// and no provider call was made.
// embeddingRetryDelay is the backoff unit between embedding attempts: the
// worker waits one unit after the first failure, two after the second. A
// variable only so tests of the failure path need not wait six seconds.
var embeddingRetryDelay = time.Second

// errEmbeddingJobFailed is what a failed job's ingest.embed span reports; the
// cause is already in the log.
var errEmbeddingJobFailed = errors.New("document left without its embedding")

func (w *EmbeddingWorker) processJob(job EmbeddingJob) jobOutcome {
	if out := w.processChunks(job); out != jobDone {
		return out
	}
	if out := w.processSummary(job); out != jobDone { // RAG-009, summary_embed.go
		return out
	}
	return w.processImage(job) // RAG-010, image_embed.go
}

// processChunks embeds the document's chunks: processJob without the summary.
//
// A document whose content is now empty has no chunks: the vectors of the
// content it had are removed, or semantic and hybrid search would go on
// finding it by text it no longer contains (GO-048).
func (w *EmbeddingWorker) processChunks(job EmbeddingJob) jobOutcome {
	if job.ContentMD == "" {
		w.vectorStore.CleanStaleChunks(job.Collection, job.DocID, 0, w.indexer)
		return jobDone
	}
	contentHash := vec.ContentHash(job.ContentMD)

	// Check if content already has a matching embedding
	existing, err := w.vectorStore.Get(job.Collection, job.DocID)
	if err == nil && existing != nil && existing.ContentHash == contentHash {
		return jobDone // embedding is up-to-date
	}

	// Split into chunks if enabled
	var chunks []string
	if w.chunkEnabled {
		size := job.ChunkSize
		if size <= 0 {
			size = w.chunkSize
		}
		chunks = chunkTextsMode(job.ContentMD, size, job.ChunkMode)
	} else {
		chunks = []string{job.ContentMD}
	}

	if len(chunks) == 0 {
		return jobDone
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
	admitted := false
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

		// GO-043: the budget is asked once, before the document's first
		// provider call; a document that starts is finished.
		if !admitted && w.budget != nil {
			if w.budget.check(job.Collection) != nil {
				return jobParked
			}
			admitted = true
		}

		var vector []float32
		var embedErr error
		for attempt := 0; attempt < 3; attempt++ {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			vector, embedErr = w.provider.Embed(ctx, chunk, embedding.RoleDocument)
			cancel()
			if w.budget != nil {
				w.budget.spend(job.Collection, chunk)
			}
			if embedErr == nil {
				break
			}
			slog.Warn("embedding attempt failed", "attempt", attempt+1, "collection", job.Collection, "docID", job.DocID, "i", i, "err", embedErr)
			time.Sleep(time.Duration(attempt+1) * embeddingRetryDelay)
		}

		if embedErr != nil {
			slog.Error("embedding failed after all attempts", "collection", job.Collection, "docID", job.DocID, "i", i, "err", embedErr)
			if w.metrics != nil {
				w.metrics.IncOp("embedding", "error")
			}
			return jobFailed
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

	// A document deleted while its embedding was in flight — alone, or with
	// its collection — must not get vectors back afterwards: they would count
	// as embedded and rank in searches with nothing behind them (#331).
	if checker, ok := w.indexer.(documentChecker); ok && !checker.DocumentExists(job.Collection, job.DocID) {
		slog.Debug("document deleted while it was being embedded; vectors discarded",
			"collection", job.Collection, "docID", job.DocID)
		return jobDone
	}

	// Store all chunks in BoltDB
	noteEmbeddingProvenance(w.vectorStore, job.Collection, w.provider)
	if err := w.vectorStore.PutChunks(job.Collection, job.DocID, chunkEmbeddings, w.provider.Model(), contentHash); err != nil {
		slog.Error("failed to store embedding", "collection", job.Collection, "docID", job.DocID, "err", err)
		return jobFailed
	}

	// Update the in-memory indexes — all of them, through the indexer. Which
	// ones a collection gets (disk-only collections keep only the quantized
	// index in RAM) is the indexer's decision, made once for every writer.
	for _, ce := range chunkEmbeddings {
		chunkKey := fmt.Sprintf("%s#%d", job.DocID, ce.ChunkIndex)
		if err := w.indexer.IndexChunk(job.Collection, chunkKey, ce.Vector); err != nil {
			// The provider now refuses to hand back an unusable vector, so
			// this is a vector that no longer matches its collection — most
			// often an embedding model changed under a collection that was
			// not reindexed (#252). The chunk is stored and unsearchable.
			slog.Warn("embedded chunk refused by the vector index; it will not be searchable until reindexed",
				"collection", job.Collection, "chunk", chunkKey, "err", err)
			if w.metrics != nil {
				w.metrics.IncOp("embedding", "index_refused")
			}
		}
	}

	// Clean stale chunks (if the document shrank) from every index, not only
	// flat: the others went on returning passages the document no longer had.
	w.vectorStore.CleanStaleChunks(job.Collection, job.DocID, len(chunkEmbeddings), w.indexer)

	if w.metrics != nil {
		w.metrics.IncOp("embedding", "completed")
	}
	return jobDone
}
