package main

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"mddb/internal/httpclient"
	json "mddb/internal/jsonx"
	proto "mddb/proto"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	bolt "go.etcd.io/bbolt"
)

var bucketBulkJobs = []byte("bulk_jobs")

// BulkJobStatus is the lifecycle state of an async bulk ingest job.
type BulkJobStatus string

const (
	BulkJobPending    BulkJobStatus = "pending"
	BulkJobProcessing BulkJobStatus = "processing"
	BulkJobCompleted  BulkJobStatus = "completed"
	BulkJobFailed     BulkJobStatus = "failed"
	BulkJobCancelled  BulkJobStatus = "cancelled"
)

// bulkJobErrorCap bounds how many per-document errors are persisted so a
// pathological job cannot blow up the status record.
const bulkJobErrorCap = 50

// bulkJobChunkSize is the number of documents handed to the batch processor
// per commit. Larger chunks improve throughput but hold the write lock longer.
const bulkJobChunkSize = 500

// BulkIngestJob captures the serializable state of an async bulk ingest job.
// Only status + counters are persisted — the document payload lives in the
// in-memory queue and is lost on restart (jobs in flight are marked failed
// on startup, see BulkIngestManager.recoverOrphans).
type BulkIngestJob struct {
	ID          string        `json:"id"`
	Collection  string        `json:"collection"`
	Status      BulkJobStatus `json:"status"`
	Total       int           `json:"total"`
	Processed   int           `json:"processed"`
	Added       int           `json:"added"`
	Updated     int           `json:"updated"`
	Failed      int           `json:"failed"`
	Errors      []string      `json:"errors,omitempty"`
	CallbackURL string        `json:"callbackUrl,omitempty"`
	SubmittedAt int64         `json:"submittedAt"`
	StartedAt   int64         `json:"startedAt,omitempty"`
	CompletedAt int64         `json:"completedAt,omitempty"`
}

// bulkWorkItem pairs the job metadata with its in-memory payload. Only jobs
// currently in the channel carry a payload; persisted jobs are status-only.
type bulkWorkItem struct {
	jobID      string
	collection string
	docs       []*proto.BatchDocument
}

// BulkIngestManager owns the job store and the single worker that drains
// queued jobs. A single worker keeps BoltDB writes serialised and removes
// the need for per-job locking on status transitions.
type BulkIngestManager struct {
	server    *Server
	queue     chan bulkWorkItem
	stop      chan struct{}
	wg        sync.WaitGroup
	mu        sync.Mutex // guards job status transitions
	jobEvents *jobEventThrottle
}

// NewBulkIngestManager constructs a manager with a buffered queue. BufferSize
// controls how many jobs can be accepted without blocking the caller; each
// job itself may hold many documents.
func NewBulkIngestManager(server *Server, bufferSize int) *BulkIngestManager {
	if bufferSize <= 0 {
		bufferSize = 64
	}
	return &BulkIngestManager{
		server:    server,
		queue:     make(chan bulkWorkItem, bufferSize),
		stop:      make(chan struct{}),
		jobEvents: newJobEventThrottle(),
	}
}

// Start launches the single worker goroutine and recovers any orphan jobs
// that were marked "processing" at the previous shutdown.
func (m *BulkIngestManager) Start() {
	m.recoverOrphans()
	m.wg.Add(1)
	go m.worker()
	slog.Info("Bulk ingest worker started (queue)", "cap", cap(m.queue))
}

// Stop waits for the in-flight job to complete and then returns. Queued jobs
// that have not yet started are marked cancelled so external observers see a
// terminal status instead of perpetual pending.
func (m *BulkIngestManager) Stop() {
	close(m.stop)
	m.wg.Wait()
	// Drain pending jobs left in the queue — their payloads die with the
	// process but their status records must not linger as "pending".
	for {
		select {
		case item := <-m.queue:
			m.markTerminal(item.jobID, BulkJobCancelled, "server shutdown")
		default:
			return
		}
	}
}

// Submit accepts a new bulk ingest job. The job is persisted with status
// pending, its payload is queued, and the job ID is returned immediately.
// Returns an error if the queue is full (caller should retry with backoff).
func (m *BulkIngestManager) Submit(collection string, docs []*proto.BatchDocument, callbackURL string) (*BulkIngestJob, error) {
	if collection == "" {
		return nil, fmt.Errorf("missing collection")
	}
	if len(docs) == 0 {
		return nil, fmt.Errorf("no documents to ingest")
	}
	job := &BulkIngestJob{
		ID:          newBulkJobID(),
		Collection:  collection,
		Status:      BulkJobPending,
		Total:       len(docs),
		CallbackURL: callbackURL,
		SubmittedAt: time.Now().Unix(),
	}
	if err := m.saveJob(job); err != nil {
		return nil, err
	}
	select {
	case m.queue <- bulkWorkItem{jobID: job.ID, collection: collection, docs: docs}:
		return job, nil
	default:
		// Queue full — record failure so the caller sees consistent status.
		_ = m.transitionStatus(job.ID, BulkJobFailed, "queue full", 0)
		return nil, fmt.Errorf("bulk ingest queue is full")
	}
}

// Get returns the current status record for a job.
func (m *BulkIngestManager) Get(jobID string) (*BulkIngestJob, error) {
	var job *BulkIngestJob
	err := m.server.DBView(func(tx *bolt.Tx) error {
		raw := tx.Bucket(bucketBulkJobs).Get([]byte(jobID))
		if raw == nil {
			return fmt.Errorf("job %s not found", jobID)
		}
		job = &BulkIngestJob{}
		return json.Unmarshal(raw, job)
	})
	if err != nil {
		return nil, err
	}
	return job, nil
}

// List returns all job records, optionally filtered by collection. Results
// are sorted newest-first by submittedAt.
func (m *BulkIngestManager) List(collection string) ([]*BulkIngestJob, error) {
	var jobs []*BulkIngestJob
	err := m.server.DBView(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketBulkJobs)
		return b.ForEach(func(_, v []byte) error {
			j := &BulkIngestJob{}
			if err := json.Unmarshal(v, j); err != nil {
				return nil
			}
			if collection != "" && j.Collection != collection {
				return nil
			}
			jobs = append(jobs, j)
			return nil
		})
	})
	if err != nil {
		return nil, err
	}
	// Primary sort: newest first. IDs carry nanosecond precision so they
	// break ties when multiple jobs land in the same wall-clock second.
	sort.Slice(jobs, func(i, j int) bool {
		if jobs[i].SubmittedAt != jobs[j].SubmittedAt {
			return jobs[i].SubmittedAt > jobs[j].SubmittedAt
		}
		return jobs[i].ID > jobs[j].ID
	})
	return jobs, nil
}

// Cancel marks a pending job as cancelled. Jobs that have already started
// cannot be cancelled safely because their batch transaction is in flight.
func (m *BulkIngestManager) Cancel(jobID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	job, err := m.Get(jobID)
	if err != nil {
		return err
	}
	if job.Status != BulkJobPending {
		return fmt.Errorf("job %s is %s, only pending jobs can be cancelled", jobID, job.Status)
	}
	job.Status = BulkJobCancelled
	job.CompletedAt = time.Now().Unix()
	return m.saveJob(job)
}

// worker drains the queue, processing one job at a time.
func (m *BulkIngestManager) worker() {
	defer m.wg.Done()
	for {
		select {
		case <-m.stop:
			return
		case item := <-m.queue:
			m.process(item)
		}
	}
}

// process runs a single job through the existing batch processor in chunks,
// fires post-batch hooks per chunk, and writes status updates incrementally.
func (m *BulkIngestManager) process(item bulkWorkItem) {
	// Skip jobs that were cancelled between submit and execution.
	job, err := m.Get(item.jobID)
	if err != nil || job.Status == BulkJobCancelled {
		return
	}

	job.Status = BulkJobProcessing
	job.StartedAt = time.Now().Unix()
	_ = m.saveJob(job)
	m.publishJobEvent(SSEJobStarted, job)

	ctx := context.Background()
	for i := 0; i < len(item.docs); i += bulkJobChunkSize {
		end := i + bulkJobChunkSize
		if end > len(item.docs) {
			end = len(item.docs)
		}
		chunk := item.docs[i:end]

		resp, processed, perr := m.server.processBatchWithDocs(ctx, item.collection, chunk)
		if perr != nil {
			job.Failed += len(chunk)
			job.Errors = appendCapped(job.Errors, perr.Error(), bulkJobErrorCap)
		} else {
			job.Added += int(resp.Added)
			job.Updated += int(resp.Updated)
			job.Failed += int(resp.Failed)
			for _, e := range resp.Errors {
				job.Errors = appendCapped(job.Errors, e, bulkJobErrorCap)
			}
			m.server.firePostBatchHooks(item.collection, processed, postBatchOptions{})
		}
		job.Processed = i + len(chunk)
		_ = m.saveJob(job)
		m.publishJobEvent(SSEJobProgress, job)
	}

	if job.Failed == job.Total {
		job.Status = BulkJobFailed
	} else {
		job.Status = BulkJobCompleted
	}
	job.CompletedAt = time.Now().Unix()
	_ = m.saveJob(job)
	m.publishJobEvent(eventForStatus(job.Status), job)

	if job.CallbackURL != "" {
		go m.fireCallback(job)
	}
	if m.server.Metrics != nil {
		m.server.Metrics.IncOp("bulk_ingest_job", string(job.Status))
	}
}

// saveJob persists a job record under its ID.
func (m *BulkIngestManager) saveJob(job *BulkIngestJob) error {
	buf, err := json.Marshal(job)
	if err != nil {
		return err
	}
	return m.server.DBUpdate(func(tx *bolt.Tx) error {
		return tx.Bucket(bucketBulkJobs).Put([]byte(job.ID), buf)
	})
}

// transitionStatus is a focused helper for setting a terminal status when we
// don't already hold the full job record.
func (m *BulkIngestManager) transitionStatus(jobID string, status BulkJobStatus, reason string, completedAt int64) error {
	job, err := m.Get(jobID)
	if err != nil {
		return err
	}
	job.Status = status
	if reason != "" {
		job.Errors = appendCapped(job.Errors, reason, bulkJobErrorCap)
	}
	if completedAt > 0 {
		job.CompletedAt = completedAt
	} else {
		job.CompletedAt = time.Now().Unix()
	}
	if err := m.saveJob(job); err != nil {
		return err
	}
	// Announced here rather than at each call site: this is the single place a
	// job changes state, so a new transition cannot forget to emit (GO-030).
	m.publishJobEvent(eventForStatus(status), job)
	return nil
}

// markTerminal is the mutex-guarded version used during shutdown drain.
func (m *BulkIngestManager) markTerminal(jobID string, status BulkJobStatus, reason string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	_ = m.transitionStatus(jobID, status, reason, 0)
}

// recoverOrphans flips any job left in a non-terminal state to failed. This
// runs once at startup so observers never see a "processing" job whose
// owning worker died with the previous process.
func (m *BulkIngestManager) recoverOrphans() {
	var orphans []string
	_ = m.server.DBView(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketBulkJobs)
		return b.ForEach(func(k, v []byte) error {
			j := &BulkIngestJob{}
			if err := json.Unmarshal(v, j); err != nil {
				return nil
			}
			if j.Status == BulkJobPending || j.Status == BulkJobProcessing {
				orphans = append(orphans, j.ID)
			}
			return nil
		})
	})
	for _, id := range orphans {
		_ = m.transitionStatus(id, BulkJobFailed, "server restarted mid-job", 0)
	}
	if len(orphans) > 0 {
		slog.Info("Bulk ingest marked orphan jobs as failed", "orphansCount", len(orphans))
	}
}

// fireCallback best-effort posts the final job record to the caller-supplied
// URL. Failures are logged but do not affect the job status — the status
// endpoint remains the source of truth.
func (m *BulkIngestManager) fireCallback(job *BulkIngestJob) {
	payload, err := json.Marshal(job)
	if err != nil {
		slog.Warn("bulk ingest callback marshal failed", "iD", job.ID, "err", err)
		return
	}
	req, err := http.NewRequest(http.MethodPost, job.CallbackURL, bytes.NewReader(payload))
	if err != nil {
		slog.Warn("bulk ingest callback bad URL", "callbackURL", job.CallbackURL, "err", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-MDDB-Event", "bulk_ingest.completed")
	// SEC-004: use the SSRF-guarded pooled client (callback URL is user-supplied).
	resp, err := httpclient.NewPooledClientWithTimeout(10 * time.Second).Do(req)
	if err != nil {
		slog.Warn("bulk ingest callback POST failed", "callbackURL", job.CallbackURL, "err", err)
		return
	}
	httpclient.DrainAndClose(resp.Body)
}

// newBulkJobID generates a collision-resistant, time-ordered job identifier.
// Format: "bulk_<unix-nanos>_<short-hex>" so jobs sort naturally in listings.
func newBulkJobID() string {
	return fmt.Sprintf("bulk_%d_%s", time.Now().UnixNano(), shortHex(6))
}

// shortHex returns n bytes of low-collision pseudo-random hex without pulling
// in crypto/rand — uniqueness is guaranteed by the nanosecond timestamp.
func shortHex(n int) string {
	const hex = "0123456789abcdef"
	now := time.Now().UnixNano()
	var b strings.Builder
	b.Grow(n)
	for i := 0; i < n; i++ {
		b.WriteByte(hex[(now>>(i*4))&0xf])
	}
	return b.String()
}

// appendCapped adds s to the slice only while it is under the given limit.
// This prevents a misbehaving source from blowing up the status record.
func appendCapped(slice []string, s string, limit int) []string {
	if len(slice) >= limit {
		return slice
	}
	return append(slice, s)
}
