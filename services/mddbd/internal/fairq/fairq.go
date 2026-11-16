// Package fairq is a queue that shares its consumers fairly between keys
// (GO-054).
//
// The server's background queues — metadata indexing and embedding — were one
// FIFO channel each. A tenant that queued ten thousand jobs put every other
// tenant's job behind all of them, and filled the buffer so the others' writes
// fell back to the slow path. Here every key (a tenant) has its own bounded
// FIFO, and consumers take one job from each key in turn: a key with one job
// waits for at most one job of every other key, however many those queued. A
// full key refuses its own pushes and nobody else's.
package fairq

import (
	"strings"
	"sync"
	"time"
)

// TenantOf is the key a job of collection is queued under: its tenant, the
// namespace before the first "/" of the name (see tenant.go in the server),
// or "" for a collection outside any namespace.
func TenantOf(collection string) string {
	if i := strings.Index(collection, "/"); i > 0 && i < len(collection)-1 {
		return collection[:i]
	}
	return ""
}

// Popped is a job taken off the queue, with its key and the time it waited.
type Popped[T any] struct {
	Value  T
	Key    string
	Waited time.Duration
}

// item is a queued value and when it was queued.
type item[T any] struct {
	v  T
	at time.Time
}

// Queue holds up to perKey values per key and pops them round-robin across
// keys, FIFO within one. It is safe for concurrent use.
type Queue[T any] struct {
	perKey int
	now    func() time.Time

	mu      sync.Mutex
	queues  map[string][]item[T]
	ring    []string // keys with queued values, in serving order
	next    int      // the ring position served next
	size    int
	changed chan struct{} // closed and replaced on every push and pop
}

// New builds a queue holding up to perKey values for each key; perKey below 1
// is 1.
func New[T any](perKey int) *Queue[T] {
	return &Queue[T]{
		perKey:  max(perKey, 1),
		now:     time.Now,
		queues:  map[string][]item[T]{},
		changed: make(chan struct{}),
	}
}

// PerKey is how many values one key may hold.
func (q *Queue[T]) PerKey() int { return q.perKey }

// Len is the number of queued values over all keys.
func (q *Queue[T]) Len() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.size
}

// Depths is the number of queued values per key, keys with none left out.
func (q *Queue[T]) Depths() map[string]int {
	q.mu.Lock()
	defer q.mu.Unlock()
	out := make(map[string]int, len(q.queues))
	for k, items := range q.queues {
		out[k] = len(items)
	}
	return out
}

// TryPush queues v under key unless key already holds perKey values.
func (q *Queue[T]) TryPush(key string, v T) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.pushLocked(key, v)
}

// Push queues v under key, waiting up to wait for the key to have room. It
// gives up early when stop is closed. It reports whether v was queued.
func (q *Queue[T]) Push(key string, v T, wait time.Duration, stop <-chan struct{}) bool {
	if q.TryPush(key, v) {
		return true
	}
	if wait <= 0 {
		return false
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	for {
		q.mu.Lock()
		if q.pushLocked(key, v) {
			q.mu.Unlock()
			return true
		}
		ch := q.changed
		q.mu.Unlock()
		select {
		case <-ch:
		case <-timer.C:
			return false
		case <-stop:
			return false
		}
	}
}

// TryPop takes the next value without waiting.
func (q *Queue[T]) TryPop() (Popped[T], bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.popLocked()
}

// Pop takes the next value, waiting until there is one or stop is closed.
func (q *Queue[T]) Pop(stop <-chan struct{}) (Popped[T], bool) {
	for {
		q.mu.Lock()
		if p, ok := q.popLocked(); ok {
			q.mu.Unlock()
			return p, true
		}
		ch := q.changed
		q.mu.Unlock()
		select {
		case <-ch:
		case <-stop:
			return Popped[T]{}, false
		}
	}
}

// pushLocked appends v to key's FIFO when it has room.
func (q *Queue[T]) pushLocked(key string, v T) bool {
	items, active := q.queues[key]
	if len(items) >= q.perKey {
		return false
	}
	if !active {
		q.ring = append(q.ring, key)
	}
	q.queues[key] = append(items, item[T]{v: v, at: q.now()})
	q.size++
	q.signalLocked()
	return true
}

// popLocked takes the head of the key whose turn it is and moves the turn on.
func (q *Queue[T]) popLocked() (Popped[T], bool) {
	if len(q.ring) == 0 {
		return Popped[T]{}, false
	}
	if q.next >= len(q.ring) {
		q.next = 0
	}
	key := q.ring[q.next]
	items := q.queues[key]
	head := items[0]
	items[0] = item[T]{} // let the value be collected
	if items = items[1:]; len(items) == 0 {
		delete(q.queues, key)
		q.ring = append(q.ring[:q.next], q.ring[q.next+1:]...)
	} else {
		q.queues[key] = items
		q.next++
	}
	q.size--
	q.signalLocked()
	return Popped[T]{Value: head.v, Key: key, Waited: q.now().Sub(head.at)}, true
}

// signalLocked wakes every waiter: something was pushed or popped.
func (q *Queue[T]) signalLocked() {
	close(q.changed)
	q.changed = make(chan struct{})
}
