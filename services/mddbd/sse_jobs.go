package main

import (
	"context"
	"fmt"
	"sync"
	"time"

	json "mddb/internal/jsonx"
)

// Job events over SSE (GO-030).
//
// Async jobs already had statuses and an endpoint to read them, but a client
// waiting for a large import had to poll. The hub that carries document events
// carries these too — same connection, same auth, same per-IP limits — with a
// ?job=<id> filter alongside the existing ?collection=.

// Job event names. They mirror the BulkJobStatus transitions rather than
// inventing a parallel vocabulary.
const (
	SSEJobStarted   = "job.started"
	SSEJobProgress  = "job.progress"
	SSEJobCompleted = "job.completed"
	SSEJobFailed    = "job.failed"
	SSEJobCancelled = "job.cancelled"
)

// jobProgressInterval bounds how often a single job may emit job.progress.
// A large import updates its counters per chunk, which would otherwise put
// hundreds of events on every listening connection for no added information.
// Terminal events are never throttled.
const jobProgressInterval = time.Second

// jobEventThrottle remembers when each job last emitted progress.
type jobEventThrottle struct {
	mu   sync.Mutex
	last map[string]time.Time
}

func newJobEventThrottle() *jobEventThrottle {
	return &jobEventThrottle{last: make(map[string]time.Time)}
}

// allow reports whether a progress event for jobID may be sent now, recording
// the decision. Terminal events call forget instead.
func (t *jobEventThrottle) allow(jobID string, now time.Time) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if prev, ok := t.last[jobID]; ok && now.Sub(prev) < jobProgressInterval {
		return false
	}
	t.last[jobID] = now
	return true
}

// forget drops a finished job's throttle state so the map cannot grow without
// bound over a server's lifetime.
func (t *jobEventThrottle) forget(jobID string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.last, jobID)
}

// eventForStatus maps a job status to the event announcing it. An unknown or
// non-announcing status returns "", meaning nothing is emitted.
func eventForStatus(status BulkJobStatus) string {
	switch status {
	case BulkJobProcessing:
		return SSEJobStarted
	case BulkJobCompleted:
		return SSEJobCompleted
	case BulkJobFailed:
		return SSEJobFailed
	case BulkJobCancelled:
		return SSEJobCancelled
	default:
		return ""
	}
}

// isTerminalJobEvent reports whether an event ends a job's stream.
func isTerminalJobEvent(event string) bool {
	switch event {
	case SSEJobCompleted, SSEJobFailed, SSEJobCancelled:
		return true
	default:
		return false
	}
}

// BroadcastJob delivers a job event to every client watching that job — or
// watching everything — subject to the same read-permission check document
// events use, against the job's collection.
func (h *SSEHub) BroadcastJob(event string, job *BulkIngestJob, authManager *AuthManager) {
	if !h.enabled || job == nil || event == "" {
		return
	}

	payload, err := json.Marshal(SSEJobEvent{
		Event:     event,
		Timestamp: time.Now().Unix(),
		Job:       job,
	})
	if err != nil {
		return
	}
	msg := fmt.Appendf(nil, "event: %s\ndata: %s\n\n", event, payload)

	h.mu.RLock()
	defer h.mu.RUnlock()

	for client := range h.clients {
		// A client watching one job hears about that job only.
		if client.job != "" && client.job != job.ID {
			continue
		}
		// A client watching one collection hears about jobs for it only.
		if client.collection != "" && client.collection != job.Collection {
			continue
		}
		// Job counters describe a collection's contents, so they need the same
		// read permission the document events require.
		if authManager != nil && authManager.enabled && client.claims != nil {
			ctx := context.WithValue(context.Background(), authContextKey, client.claims)
			if err := authManager.CheckPermission(ctx, job.Collection, PermRead); err != nil {
				continue
			}
		}

		select {
		case client.ch <- msg:
		default:
			// Client buffer full: drop rather than block the job.
		}
	}
}

// publishJobEvent emits one job event, applying progress throttling and
// clearing throttle state once the job ends.
func (m *BulkIngestManager) publishJobEvent(event string, job *BulkIngestJob) {
	if m.server == nil || m.server.SSEHub == nil || event == "" || job == nil {
		return
	}
	if event == SSEJobProgress && !m.jobEvents.allow(job.ID, time.Now()) {
		return
	}
	if isTerminalJobEvent(event) {
		m.jobEvents.forget(job.ID)
	}
	m.server.SSEHub.BroadcastJob(event, job, m.server.AuthManager)
}
