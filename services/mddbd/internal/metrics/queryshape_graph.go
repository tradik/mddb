package metrics

import (
	"hash/maphash"
	"iter"
	"slices"
	"sync"
	"sync/atomic"
	"time"
)

// Graph-request counters and chain tracking (OPS-025, for CODE-006/007).
//
// A chain is a graph call followed, within chainWindow, by another from the
// same client that starts at a document the previous answer reached: the
// agent walking further than one request lets it. Long chains are the
// quantitative trace of "depth 3 is too shallow" (CODE-006). Keys are kept
// only as seeded 64-bit hashes, in memory, and only for the last answer of
// each client; nothing of them reaches a counter.

// GraphKind is the graph operation counted.
type GraphKind uint8

// The graph operations.
const (
	GraphCodeGraph GraphKind = iota // /v1/code-graph and its MCP and GraphQL forms
	GraphExpand                     // retrievalMode "graph" in vector search (SRCH-006)
	graphKindCount
)

var graphKindNames = [graphKindCount]string{"code_graph", "expand"}

const (
	// chainWindow is how soon the next call must follow to extend a chain.
	chainWindow = 30 * time.Second
	// maxChainClients bounds the clients tracked at once.
	maxChainClients = 4096
	// maxChainReached bounds the keys remembered from one answer.
	maxChainReached = 256
)

// GraphShape is one graph request, as it is counted.
type GraphShape struct {
	Surface    Surface
	Kind       GraphKind
	Collection string // held in memory only; the bundle hashes it
	// Depth is the depth the caller asked for; 0 means the default.
	Depth int
	// MaxDepth is the server's limit for this operation; a Depth above it
	// was clamped.
	MaxDepth int
	// NoStart reports a request refused for want of a start document.
	NoStart bool
	// WholeGraph reports a request at the maximum depth and fan-out: the
	// "show me the map" shape (CODE-007).
	WholeGraph bool
	// Truncated reports that a limit cut the walk short.
	Truncated bool
	// Client identifies the caller for chain tracking, already hashed; 0
	// leaves the request out of chains.
	Client uint64
	// Start is the start document's key and Reached the keys the answer
	// reached. Both are hashed for chain tracking and never stored.
	Start   string
	Reached iter.Seq[string]
}

// graphCounters are the graph half of QueryShape.
type graphCounters struct {
	requests   [surfaceCount][graphKindCount]atomic.Int64
	depth      [graphKindCount][smallBuckets]atomic.Int64
	clamped    [surfaceCount][graphKindCount]atomic.Int64
	truncated  [graphKindCount]atomic.Int64
	noStart    [surfaceCount]atomic.Int64
	wholeGraph [surfaceCount]atomic.Int64
	chains     *chainTracker
}

// Graph counts one graph request and advances its client's chain.
func (q *QueryShape) Graph(g GraphShape) {
	if q == nil || g.Surface >= surfaceCount || g.Kind >= graphKindCount {
		return
	}
	gc := &q.graph
	gc.requests[g.Surface][g.Kind].Add(1)
	c := q.collection(g.Collection)
	c.graph.Add(1)
	if g.Kind == GraphCodeGraph {
		gc.chains.observe(g.Client, g.Collection, g.Start, g.Reached)
	}
	if g.NoStart {
		gc.noStart[g.Surface].Add(1)
		return
	}
	gc.depth[g.Kind][smallBucket(g.Depth)].Add(1)
	if g.MaxDepth > 0 && g.Depth > g.MaxDepth {
		gc.clamped[g.Surface][g.Kind].Add(1)
		c.clamped.Add(1)
	}
	if g.Truncated {
		gc.truncated[g.Kind].Add(1)
	}
	if g.WholeGraph {
		gc.wholeGraph[g.Surface].Add(1)
	}
}

// chainState is one client's chain in progress.
type chainState struct {
	last    time.Time
	length  int
	reached []uint64
}

// chainTracker follows each client's chain.
type chainTracker struct {
	mu        sync.Mutex
	now       func() time.Time
	seed      maphash.Seed
	clients   map[uint64]*chainState
	lengths   [smallBuckets]int64 // ended chains by length; bucket 0 unused
	sum       int64               // total length of ended chains
	sessions  int64               // ended chains
	untracked int64               // calls not followed: too many clients
}

func newChainTracker(now func() time.Time) *chainTracker {
	return &chainTracker{now: now, seed: maphash.MakeSeed(), clients: map[uint64]*chainState{}}
}

// keyHash hashes a document key within its collection.
func (t *chainTracker) keyHash(collection, key string) uint64 {
	var h maphash.Hash
	h.SetSeed(t.seed)
	_, _ = h.WriteString(collection)
	_ = h.WriteByte(0)
	_, _ = h.WriteString(key)
	return h.Sum64()
}

// observe records one code-graph call of client.
func (t *chainTracker) observe(client uint64, collection, start string, reached iter.Seq[string]) {
	if client == 0 {
		return
	}
	now := t.now()
	t.mu.Lock()
	defer t.mu.Unlock()
	st := t.clients[client]
	switch {
	case st != nil && now.Sub(st.last) <= chainWindow && slices.Contains(st.reached, t.keyHash(collection, start)):
		st.length++
	case st != nil:
		t.end(st)
		st.length = 1
	default:
		if len(t.clients) >= maxChainClients {
			t.sweep(now)
		}
		if len(t.clients) >= maxChainClients {
			t.untracked++
			return
		}
		st = &chainState{length: 1}
		t.clients[client] = st
	}
	st.last = now
	st.reached = st.reached[:0]
	if reached == nil {
		return
	}
	for key := range reached {
		if len(st.reached) == maxChainReached {
			break
		}
		st.reached = append(st.reached, t.keyHash(collection, key))
	}
}

// end counts a finished chain.
func (t *chainTracker) end(st *chainState) {
	t.lengths[smallBucket(st.length)]++
	t.sum += int64(st.length)
	t.sessions++
}

// sweep ends the chains whose window has passed.
func (t *chainTracker) sweep(now time.Time) {
	for client, st := range t.clients {
		if now.Sub(st.last) > chainWindow {
			t.end(st)
			delete(t.clients, client)
		}
	}
}

// chainStats is a snapshot of the tracker.
type chainStats struct {
	lengths   [smallBuckets]int64
	sum       int64
	sessions  int64
	open      int
	untracked int64
}

// stats ends the expired chains and reports.
func (t *chainTracker) stats() chainStats {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.sweep(t.now())
	return chainStats{lengths: t.lengths, sum: t.sum, sessions: t.sessions, open: len(t.clients), untracked: t.untracked}
}
