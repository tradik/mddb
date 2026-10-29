// Package pending tracks in-flight background work per collection and per
// document, and lets a caller wait for it to drain (GO-042).
//
// MDDB answers a write before every index has caught up with it: embeddings
// are computed by a background worker, and a batch update indexes metadata on
// a queue. An agent that writes a document and searches for it at once could
// not find it, could not tell why, and could not wait. A Tracker is the
// shared answer for every such queue — counts are kept as work starts and
// ends, never recomputed by scanning, and a waiter is woken on every change
// instead of polling.
package pending

import (
	"context"
	"sync"
	"time"
)

// Stats is a point-in-time view of one collection's work (or of all of it).
type Stats struct {
	// Pending is how many jobs are queued or running.
	Pending int
	// Failed counts jobs that ended without doing their work since start —
	// a failed provider call, or a job the queue could not take.
	Failed uint64
	// Completed counts jobs that finished their work since start.
	Completed uint64
	// OldestPendingAt is when the longest-waiting job was queued; zero when
	// nothing is pending.
	OldestPendingAt time.Time
}

// Tracker counts work in flight. Use New. A nil *Tracker is valid and tracks
// nothing, so a queue built without one (a test literal) keeps working.
type Tracker struct {
	mu        sync.Mutex
	docs      map[string]map[string][]time.Time // collection → docID → queue times
	failed    map[string]uint64
	completed map[string]uint64
	changed   chan struct{}           // closed and replaced on every change
	drained   func(collection string) // called when a collection drains to zero
	now       func() time.Time
}

// New returns an empty tracker.
func New() *Tracker {
	return &Tracker{
		docs:      map[string]map[string][]time.Time{},
		failed:    map[string]uint64{},
		completed: map[string]uint64{},
		changed:   make(chan struct{}),
		now:       time.Now,
	}
}

// OnDrained registers fn to run, outside the lock, whenever a collection's
// pending count falls to zero. Set it before work starts.
func (t *Tracker) OnDrained(fn func(collection string)) {
	if t == nil {
		return
	}
	t.mu.Lock()
	t.drained = fn
	t.mu.Unlock()
}

// Add records a job for docID as queued.
func (t *Tracker) Add(collection, docID string) {
	if t == nil {
		return
	}
	t.mu.Lock()
	coll := t.docs[collection]
	if coll == nil {
		coll = map[string][]time.Time{}
		t.docs[collection] = coll
	}
	coll[docID] = append(coll[docID], t.now())
	t.notifyLocked()
	t.mu.Unlock()
}

// Done records that one job for docID ended; failed says whether it did its
// work. A Done without a matching Add only updates the counters.
func (t *Tracker) Done(collection, docID string, failed bool) {
	if t == nil {
		return
	}
	t.mu.Lock()
	if failed {
		t.failed[collection]++
	} else {
		t.completed[collection]++
	}
	drained := false
	if coll := t.docs[collection]; coll != nil {
		if times := coll[docID]; len(times) > 0 {
			if len(times) == 1 {
				delete(coll, docID)
			} else {
				coll[docID] = times[1:]
			}
			if len(coll) == 0 {
				delete(t.docs, collection)
				drained = true
			}
		}
	}
	fn := t.drained
	t.notifyLocked()
	t.mu.Unlock()
	if drained && fn != nil {
		fn(collection)
	}
}

// notifyLocked wakes every waiter. Caller holds t.mu.
func (t *Tracker) notifyLocked() {
	close(t.changed)
	t.changed = make(chan struct{})
}

// Stats reports one collection; "" reports every collection together.
func (t *Tracker) Stats(collection string) Stats {
	if t == nil {
		return Stats{}
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	var st Stats
	for coll, docs := range t.docs {
		if collection != "" && coll != collection {
			continue
		}
		for _, times := range docs {
			st.Pending += len(times)
			if first := times[0]; st.OldestPendingAt.IsZero() || first.Before(st.OldestPendingAt) {
				st.OldestPendingAt = first
			}
		}
	}
	for coll, n := range t.failed {
		if collection == "" || coll == collection {
			st.Failed += n
		}
	}
	for coll, n := range t.completed {
		if collection == "" || coll == collection {
			st.Completed += n
		}
	}
	return st
}

// IsPending reports whether docID has work queued or running.
func (t *Tracker) IsPending(collection, docID string) bool {
	if t == nil {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.docs[collection][docID]) > 0
}

// PendingDocs lists the documents of a collection with work in flight.
func (t *Tracker) PendingDocs(collection string) []string {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]string, 0, len(t.docs[collection]))
	for id := range t.docs[collection] {
		out = append(out, id)
	}
	return out
}

// Wait blocks until no pending document of collection satisfies match (nil
// matches every document), or ctx ends. It reports whether the work drained.
func (t *Tracker) Wait(ctx context.Context, collection string, match func(docID string) bool) bool {
	if t == nil {
		return true
	}
	for {
		t.mu.Lock()
		busy := false
		for id := range t.docs[collection] {
			if match == nil || match(id) {
				busy = true
				break
			}
		}
		changed := t.changed
		t.mu.Unlock()
		if !busy {
			return true
		}
		select {
		case <-changed:
		case <-ctx.Done():
			return false
		}
	}
}
