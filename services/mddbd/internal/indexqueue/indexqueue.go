package indexqueue

import (
	"context"
	"errors"
	bolt "go.etcd.io/bbolt"
	"log/slog"
	"mddb/internal/binlog"
	"mddb/internal/storage"
	"sync"
)

// ErrQueueClosed is returned by Enqueue when the queue is shutting down.
var ErrQueueClosed = errors.New("index queue closed")

// Store is the persistence surface the queue needs to write the metadata index.
// It is a dependency-inversion seam (GO-015): the queue owns the interface and
// the daemon implements it over its *Server, so the queue no longer holds a
// back-reference to the Server god-object.
type Store interface {
	// DBUpdate runs fn inside a writable BoltDB transaction.
	DBUpdate(func(*bolt.Tx) error) error
	// IdxMetaBucket returns the name of the metadata-index bucket.
	IdxMetaBucket() []byte
	// Binlog returns the replication binary log (may be nil), read lazily so a
	// log wired after queue construction is still observed.
	Binlog() *binlog.Binlog
}

// IndexJob represents a metadata indexing job
type IndexJob struct {
	Collection string
	DocID      string
	OldMeta    map[string][]string
	NewMeta    map[string][]string
}

// IndexQueue manages asynchronous metadata indexing
type IndexQueue struct {
	store     Store
	queue     chan *IndexJob
	workers   int
	wg        sync.WaitGroup
	ctx       context.Context
	cancel    context.CancelFunc
	processed uint64
	failed    uint64
	fallbacks uint64 // jobs processed synchronously because the queue was full
	mu        sync.RWMutex
}

// NewIndexQueue creates a new index queue
func NewIndexQueue(store Store, workers int) *IndexQueue {
	if workers <= 0 {
		workers = 4 // Default 4 workers
	}

	ctx, cancel := context.WithCancel(context.Background())

	iq := &IndexQueue{
		store:   store,
		queue:   make(chan *IndexJob, 1000), // Buffer 1000 jobs
		workers: workers,
		ctx:     ctx,
		cancel:  cancel,
	}

	// Start workers
	for i := 0; i < workers; i++ {
		iq.wg.Add(1)
		go iq.worker(i)
	}

	return iq
}

// SetStore wires the persistence surface after construction. The daemon builds
// the queue before its *Server is fully assembled, so the store is set once the
// server is ready (before any job is ever enqueued).
func (iq *IndexQueue) SetStore(store Store) {
	iq.store = store
}

// Enqueue submits a metadata indexing job. It NEVER drops the job (GO-010):
//
//   - normally the job is handed to a worker (async, non-blocking);
//   - if the buffer is full, the job is indexed SYNCHRONOUSLY in the caller's
//     goroutine (fallback) so it is never silently lost — the previous
//     behaviour left a doc permanently missing from the meta index;
//   - if the queue is shutting down, ErrQueueClosed is returned.
//
// IMPORTANT: the synchronous fallback opens its own write transaction, so
// Enqueue must NOT be called from inside an open bolt DB.Update — callers
// collect jobs during the write tx and Enqueue them AFTER it commits.
func (iq *IndexQueue) Enqueue(job *IndexJob) error {
	// Fail fast once shut down — also avoids ever selecting a send on a
	// queue whose drain has stopped.
	if iq.ctx.Err() != nil {
		return ErrQueueClosed
	}
	select {
	case iq.queue <- job:
		return nil
	default:
		// Queue full — index inline so the job is never lost.
		iq.mu.Lock()
		iq.fallbacks++
		iq.mu.Unlock()
		if err := iq.processJob(job); err != nil {
			iq.mu.Lock()
			iq.failed++
			iq.mu.Unlock()
			return err
		}
		iq.mu.Lock()
		iq.processed++
		iq.mu.Unlock()
		return nil
	}
}

// worker processes indexing jobs
func (iq *IndexQueue) worker(id int) {
	defer iq.wg.Done()

	for {
		select {
		case job := <-iq.queue:
			if err := iq.processJob(job); err != nil {
				slog.Warn("Worker failed to index doc", "id", id, "docID", job.DocID, "err", err)
				iq.mu.Lock()
				iq.failed++
				iq.mu.Unlock()
			} else {
				iq.mu.Lock()
				iq.processed++
				iq.mu.Unlock()
			}
		case <-iq.ctx.Done():
			return
		}
	}
}

// processJob processes a single indexing job
func (iq *IndexQueue) processJob(job *IndexJob) error {
	var bo binlog.BinlogOps
	err := iq.store.DBUpdate(func(tx *bolt.Tx) error {
		bIdx := tx.Bucket(iq.store.IdxMetaBucket())

		// Delete old indices
		if job.OldMeta != nil {
			for mk, vals := range job.OldMeta {
				for _, mv := range vals {
					key := storage.MetaKeyPrefix(job.Collection, mk, mv)
					key = append(key, []byte(job.DocID)...)
					_ = bIdx.Delete(key)
					bo.Delete("idxmeta", key)
				}
			}
		}

		// Add new indices
		if job.NewMeta != nil {
			for mk, vals := range job.NewMeta {
				for _, mv := range vals {
					key := storage.MetaKeyPrefix(job.Collection, mk, mv)
					key = append(key, []byte(job.DocID)...)
					if err := bIdx.Put(key, []byte("1")); err != nil {
						return err
					}
					bo.Put("idxmeta", key, []byte("1"))
				}
			}
		}

		return nil
	})
	if err == nil {
		bo.FlushTo(iq.store.Binlog())
	}
	return err
}

// Shutdown gracefully shuts down the index queue. Workers exit on ctx.Done();
// the queue channel is intentionally NOT closed — Enqueue could otherwise race
// into a send on a closed channel and panic. After Shutdown, Enqueue returns
// ErrQueueClosed.
func (iq *IndexQueue) Shutdown() {
	iq.cancel()
	iq.wg.Wait()
}

// Stats returns queue statistics: jobs processed, jobs failed, jobs indexed
// synchronously because the queue was full (fallbacks), and the current queue
// depth.
func (iq *IndexQueue) Stats() (processed, failed, fallbacks uint64, queueLen int) {
	iq.mu.RLock()
	defer iq.mu.RUnlock()
	return iq.processed, iq.failed, iq.fallbacks, len(iq.queue)
}
