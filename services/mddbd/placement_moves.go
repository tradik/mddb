package main

import (
	"sync"
	"time"
)

// The moves the meta leader runs or ran (GO-052), for GET /v1/placement and
// for a caller waiting on one.

// Phases of a move.
const (
	movePhaseCopying    = "copying"     // the target copies the collection
	movePhaseCatchingUp = "catching-up" // it applies the writes made meanwhile
	movePhaseFenced     = "fenced"      // writes are held while the map switches
	movePhaseDone       = "done"
	movePhaseFailed     = "failed"
)

// moveStatus is one move as GET /v1/placement shows it.
type moveStatus struct {
	Collection string `json:"collection"`
	From       string `json:"from"`
	To         string `json:"to"`
	Phase      string `json:"phase"`
	StartedAt  string `json:"startedAt"`
	FinishedAt string `json:"finishedAt"`
	KeysCopied uint64 `json:"keysCopied"`
	SourceLSN  uint64 `json:"sourceLsn"`
	AppliedLSN uint64 `json:"appliedLsn"`
	Error      string `json:"error"`
	DurationMs int64  `json:"durationMs"`
}

// move is a running or finished move.
type move struct {
	status moveStatus
	start  time.Time
	done   chan struct{}
}

// moveBoard holds the running moves and the last finished ones.
type moveBoard struct {
	mu     sync.Mutex
	active map[string]*move
	recent []*move
}

// recentMoves is how many finished moves are kept.
const recentMoves = 20

func (b *moveBoard) init() { b.active = map[string]*move{} }

// begin records a move of c, or returns nil when one is running.
func (b *moveBoard) begin(c, from, to string) *move {
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, busy := b.active[c]; busy {
		return nil
	}
	now := time.Now()
	m := &move{start: now, done: make(chan struct{}), status: moveStatus{Collection: c, From: from, To: to,
		Phase: movePhaseCopying, StartedAt: now.UTC().Format(time.RFC3339)}}
	b.active[c] = m
	return m
}

// running reports whether a move of c runs.
func (b *moveBoard) running(c string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	_, ok := b.active[c]
	return ok
}

// update changes m's status.
func (b *moveBoard) update(m *move, change func(*moveStatus)) {
	b.mu.Lock()
	defer b.mu.Unlock()
	change(&m.status)
}

// finish ends m, done or failed with err.
func (b *moveBoard) finish(m *move, err error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := time.Now()
	m.status.Phase = movePhaseDone
	if err != nil {
		m.status.Phase, m.status.Error = movePhaseFailed, err.Error()
	}
	m.status.FinishedAt = now.UTC().Format(time.RFC3339)
	m.status.DurationMs = now.Sub(m.start).Milliseconds()
	delete(b.active, m.status.Collection)
	b.recent = append(b.recent, m)
	if len(b.recent) > recentMoves {
		b.recent = b.recent[len(b.recent)-recentMoves:]
	}
	close(m.done)
}

// get is m's status now.
func (b *moveBoard) get(m *move) moveStatus {
	b.mu.Lock()
	defer b.mu.Unlock()
	return m.status
}

// list is every running move and the finished ones, newest last.
func (b *moveBoard) list() []moveStatus {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]moveStatus, 0, len(b.recent)+len(b.active))
	for _, m := range b.recent {
		out = append(out, m.status)
	}
	for _, m := range b.active {
		out = append(out, m.status)
	}
	return out
}
