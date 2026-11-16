package main

import (
	"log/slog"
	"time"

	"mddb/internal/fairq"
)

// The embedding worker's queue (GO-054).
//
// Jobs are queued per tenant — the namespace of the document's collection,
// "" for collections outside one — and the workers take one tenant's job
// after another, so a tenant importing a million documents delays another
// tenant's document by at most one of its own per worker. Each tenant may
// queue EmbeddingQueueSize jobs.
//
// A writer whose tenant's queue is full waits up to MDDB_EMBEDDING_QUEUE_WAIT
// for room, as before. Then, instead of dropping the job (#232: the document
// stayed without a vector until someone reindexed it), the job is parked
// with the jobs waiting for an embedding budget: it stays pending in
// /v1/indexing-status and goes back on the queue as soon as its tenant has
// room. Only past MDDB_EMBEDDING_PARK_MAX parked jobs, or while the worker
// stops, is a job dropped — counted and logged as before.

// parkOverflowRecheck is how often jobs parked for room are tried again. A
// variable so tests need not wait.
var parkOverflowRecheck = time.Second

// QueueStats reports what the queue is holding and what it has lost: the
// jobs one tenant may queue, the jobs queued over all tenants, the jobs
// dropped since start, and how long a writer waits for room.
func (w *EmbeddingWorker) QueueStats() (size, depth int, dropped uint64, wait time.Duration) {
	return w.queue.PerKey(), w.queue.Len(), w.dropped.Load(), w.enqueueWait
}

// TenantDepths is the queued jobs per tenant, tenants with none left out.
func (w *EmbeddingWorker) TenantDepths() map[string]int { return w.queue.Depths() }

// ParkedDepths is the parked jobs per tenant — waiting for an embedding
// budget or for room in the queue — tenants with none left out.
func (w *EmbeddingWorker) ParkedDepths() map[string]int {
	w.parkMu.Lock()
	defer w.parkMu.Unlock()
	out := map[string]int{}
	for _, job := range w.parked {
		out[fairq.TenantOf(job.Collection)]++
	}
	return out
}

// SetObserver sets what is told the tenant and queue wait of each job a
// worker takes. Set it before Start.
func (w *EmbeddingWorker) SetObserver(fn func(tenant string, waited time.Duration)) { w.observe = fn }

// Start begins processing embedding jobs in the background.
func (w *EmbeddingWorker) Start(workers int) {
	for i := 0; i < workers; i++ {
		w.wg.Add(1)
		go w.worker()
	}
	slog.Info("embedding worker started",
		"workers", workers, "bufferPerTenant", w.queue.PerKey(), "chunking", w.chunkEnabled, "chunkSize", w.chunkSize)
}

// Stop gracefully stops the worker: the queued jobs are run first.
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

// Enqueue adds an embedding job to its tenant's queue, waiting for room if
// that is full, and parking the job when none came. Returns false only when
// the job was abandoned — the worker is stopping, or too many jobs are
// parked — which the caller is expected to report rather than ignore (#232).
func (w *EmbeddingWorker) Enqueue(job EmbeddingJob) bool {
	// Counted before the send, so a worker that finishes first cannot record
	// the end of a job the tracker has not seen start.
	w.pending.Add(job.Collection, job.DocID)
	if w.queue.Push(fairq.TenantOf(job.Collection), job, w.enqueueWait, w.stopCh) {
		return true
	}
	select {
	case <-w.stopCh:
		// Shutting down: the job is not going to be processed.
		return w.dropJob(job)
	default:
	}
	return w.park(job, parkOverflowRecheck)
}

// dropJob records an abandoned job and always reports failure.
func (w *EmbeddingWorker) dropJob(job EmbeddingJob) bool {
	w.pending.Done(job.Collection, job.DocID, true)
	w.dropped.Add(1)
	if w.metrics != nil {
		w.metrics.IncOp("embedding", "dropped")
	}
	slog.Warn("embedding job dropped — this document will have no vector until it is reindexed",
		"collection", job.Collection, "docID", job.DocID,
		"queueSizePerTenant", w.queue.PerKey(), "parkMax", w.parkMax, "droppedTotal", w.dropped.Load())
	return false
}

// worker runs jobs, one tenant's after another's. After Stop it goes on until
// the queue is empty: Pop hands out what is queued before it reports the stop.
func (w *EmbeddingWorker) worker() {
	defer w.wg.Done()
	for {
		p, ok := w.queue.Pop(w.stopCh)
		if !ok {
			return
		}
		w.take(p)
	}
}

// take reports a job's wait and runs it.
func (w *EmbeddingWorker) take(p fairq.Popped[EmbeddingJob]) {
	if w.observe != nil {
		w.observe(p.Key, p.Waited)
	}
	w.run(p.Value)
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
		w.park(job, w.budget.nextCheck())
	case jobFailed:
		sp.SetError(errEmbeddingJobFailed)
		w.pending.Done(job.Collection, job.DocID, true)
	default:
		w.pending.Done(job.Collection, job.DocID, false)
	}
}

// park holds a job, still pending, until releaseParked can queue it, and
// makes sure something looks at it again within recheck. Past parkMax
// parked jobs the job is dropped instead.
func (w *EmbeddingWorker) park(job EmbeddingJob, recheck time.Duration) bool {
	w.parkMu.Lock()
	if len(w.parked) >= w.parkMax {
		w.parkMu.Unlock()
		return w.dropJob(job)
	}
	defer w.parkMu.Unlock()
	w.parked = append(w.parked, job)
	w.scheduleLocked(recheck)
	return true
}

// scheduleLocked makes releaseParked run within d, sooner than planned if a
// look was planned later.
func (w *EmbeddingWorker) scheduleLocked(d time.Duration) {
	due := time.Now().Add(d)
	switch {
	case w.parkTimer == nil:
		w.parkTimer = time.AfterFunc(d, w.releaseParked)
	case due.Before(w.parkDue):
		w.parkTimer.Reset(d)
	default:
		return
	}
	w.parkDue = due
}

// releaseParked puts the parked jobs back on the queue whose collection has
// budget again and whose tenant has room, and schedules another look for the
// rest: soon for jobs waiting for room, at midnight UTC or within
// budgetRecheckInterval for jobs waiting for a budget.
func (w *EmbeddingWorker) releaseParked() {
	w.parkMu.Lock()
	defer w.parkMu.Unlock()
	w.parkTimer = nil
	var still []EmbeddingJob
	recheck := time.Duration(0)
	for _, job := range w.parked {
		if w.budget != nil && w.budget.exhausted(job.Collection) != nil {
			still = append(still, job)
			recheck = minPositive(recheck, w.budget.nextCheck())
			continue
		}
		if !w.queue.TryPush(fairq.TenantOf(job.Collection), job) {
			still = append(still, job)
			recheck = minPositive(recheck, parkOverflowRecheck)
		}
	}
	w.parked = still
	if len(still) > 0 {
		w.scheduleLocked(recheck)
	}
}

// minPositive is the smaller of a and b, a being unset while zero.
func minPositive(a, b time.Duration) time.Duration {
	if a == 0 {
		return b
	}
	return min(a, b)
}
