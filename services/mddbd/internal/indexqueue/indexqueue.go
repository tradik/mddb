package indexqueue

import (
	"context"
	"errors"
	"log/slog"
	"mddb/internal/binlog"
	"mddb/internal/fairq"
	"mddb/internal/pending"
	"mddb/internal/storage"
	"sync"
	"time"

	bolt "go.etcd.io/bbolt"
)

// ErrQueueClosed is returned by Enqueue when the queue is shutting down.
var ErrQueueClosed = errors.New("index queue closed")

// Store is the persistence surface the queue needs to write the metadata index.
// It is a dependency-inversion seam (GO-015): the queue owns the interface and
// the daemon implements it over its *Server, so the queue no longer holds a
// back-reference to the Server god-object.
type Store interface {
	// DBUpdate runs fn inside a writable BoltDB transaction on the file that
	// holds collection (#336: one file per collection is possible).
	DBUpdate(collection string, fn func(*bolt.Tx) error) error
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

// QueueCapacity is how many jobs one tenant may have queued. It is per tenant
// (GO-054), so one tenant's backlog cannot fill the buffer for the others.
const QueueCapacity = 1000

// IndexQueue manages asynchronous metadata indexing. Jobs are queued per
// tenant and the workers take one tenant's job after another (fairq), so a
// tenant with a large backlog delays another tenant's job by at most one of
// its own per worker.
type IndexQueue struct {
	store     Store
	queue     *fairq.Queue[*IndexJob]
	workers   int
	wg        sync.WaitGroup
	ctx       context.Context
	cancel    context.CancelFunc
	processed uint64
	failed    uint64
	fallbacks uint64 // jobs processed synchronously because the queue was full
	mu        sync.RWMutex
	// pending tracks queued jobs per collection and document (GO-042), so the
	// indexing status can say which collection is behind, not only that the
	// queue is non-empty.
	pending *pending.Tracker
	// observe is told, for each job a worker takes, its tenant and how long
	// it waited (GO-054: mddb_tenant_queue_wait_seconds). nil = nobody asks.
	observe func(tenant string, waited time.Duration)
}

// NewIndexQueue creates a new index queue
func NewIndexQueue(store Store, workers int) *IndexQueue {
	if workers <= 0 {
		workers = 4 // Default 4 workers
	}

	ctx, cancel := context.WithCancel(context.Background())

	iq := &IndexQueue{
		store:   store,
		queue:   fairq.New[*IndexJob](QueueCapacity),
		workers: workers,
		ctx:     ctx,
		cancel:  cancel,
		pending: pending.New(),
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

// SetObserver sets what is told about each job a worker takes: its tenant and
// the time it waited in the queue. Set it before the first Enqueue.
func (iq *IndexQueue) SetObserver(fn func(tenant string, waited time.Duration)) {
	iq.observe = fn
}

// Enqueue submits a metadata indexing job. It NEVER drops the job (GO-010):
//
//   - normally the job is handed to a worker (async, non-blocking);
//   - if the job's tenant already has QueueCapacity jobs queued, the job is
//     indexed SYNCHRONOUSLY in the caller's goroutine (fallback) so it is
//     never silently lost — the previous behaviour left a doc permanently
//     missing from the meta index. The slow path falls on the tenant that
//     filled its queue, not on the others (GO-054);
//   - if the queue is shutting down, ErrQueueClosed is returned.
//
// IMPORTANT: the synchronous fallback opens its own write transaction, so
// Enqueue must NOT be called from inside an open bolt DB.Update — callers
// collect jobs during the write tx and Enqueue them AFTER it commits.
func (iq *IndexQueue) Enqueue(job *IndexJob) error {
	// Fail fast once shut down.
	if iq.ctx.Err() != nil {
		return ErrQueueClosed
	}
	iq.pending.Add(job.Collection, job.DocID)
	if iq.queue.TryPush(fairq.TenantOf(job.Collection), job) {
		return nil
	}
	// Indexed inline below: not pending once Enqueue returns.
	iq.pending.Done(job.Collection, job.DocID, false)
	iq.mu.Lock()
	iq.fallbacks++
	iq.mu.Unlock()
	err := iq.processJob(job)
	iq.count(err)
	return err
}

// count records the outcome of one job.
func (iq *IndexQueue) count(err error) {
	iq.mu.Lock()
	defer iq.mu.Unlock()
	if err != nil {
		iq.failed++
	} else {
		iq.processed++
	}
}

// worker processes indexing jobs, one tenant's after another's.
func (iq *IndexQueue) worker(id int) {
	defer iq.wg.Done()

	for {
		p, ok := iq.queue.Pop(iq.ctx.Done())
		if !ok {
			return
		}
		if iq.observe != nil {
			iq.observe(p.Key, p.Waited)
		}
		job := p.Value
		err := iq.processJob(job)
		iq.pending.Done(job.Collection, job.DocID, err != nil)
		iq.count(err)
		if err != nil {
			slog.Warn("Worker failed to index doc", "id", id, "docID", job.DocID, "err", err)
		}
	}
}

// processJob processes a single indexing job. The index entries reach the
// binlog inside the transaction that writes them, in commit order (GO-051).
func (iq *IndexQueue) processJob(job *IndexJob) error {
	update := func(fn func(*bolt.Tx) error) error { return iq.store.DBUpdate(job.Collection, fn) }
	return binlog.Logged(update, iq.store.Binlog(), func(tx *bolt.Tx, bo *binlog.BinlogOps) error {
		bIdx := tx.Bucket(iq.store.IdxMetaBucket())
		// Delete old indices, then add the new ones.
		for _, key := range metaKeys(job, job.OldMeta) {
			_ = bIdx.Delete(key)
			bo.Delete("idxmeta", key)
		}
		for _, key := range metaKeys(job, job.NewMeta) {
			if err := bIdx.Put(key, []byte("1")); err != nil {
				return err
			}
			bo.Put("idxmeta", key, []byte("1"))
		}
		return nil
	})
}

// metaKeys is the metadata-index key of every value of meta for the job's
// document.
func metaKeys(job *IndexJob, meta map[string][]string) [][]byte {
	var keys [][]byte
	for mk, vals := range meta {
		for _, mv := range vals {
			key := storage.MetaKeyPrefix(job.Collection, mk, mv)
			keys = append(keys, append(key, []byte(job.DocID)...))
		}
	}
	return keys
}

// Shutdown gracefully shuts down the index queue. Workers exit on ctx.Done().
// After Shutdown, Enqueue returns ErrQueueClosed.
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
	return iq.processed, iq.failed, iq.fallbacks, iq.queue.Len()
}

// TenantDepths is the number of queued jobs per tenant ("" for collections
// outside any tenant namespace), tenants with none left out (GO-054).
func (iq *IndexQueue) TenantDepths() map[string]int { return iq.queue.Depths() }

// Pending is the queue's in-flight job tracker (GO-042).
func (iq *IndexQueue) Pending() *pending.Tracker { return iq.pending }
