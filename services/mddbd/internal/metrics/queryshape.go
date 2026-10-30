package metrics

import (
	"sync"
	"sync/atomic"
	"time"
)

// Query-shape counters (OPS-025).
//
// What a search or a graph request looked like — which surface it came in on,
// which mode and algorithm ran, which filter operators it used, how deep a
// graph walk it asked for and whether that was clamped — counted, never
// recorded. No counter holds query text, a document key or a value: the
// labels are fixed vocabularies, and a collection is held by name only in
// memory, for the diagnose bundle to hash.
//
// The counters exist to answer the CODE-006/007 gates' questions with numbers
// from real instances: do agents hit the graph depth limit, chain graph calls,
// or ask for the whole map? They are always on, metrics enabled or not,
// because the diagnose bundle reads them too; a counter costs an atomic add.

// Surface is the protocol a request arrived on.
type Surface uint8

// The surfaces, in the order they are reported.
const (
	SurfaceREST Surface = iota
	SurfaceGRPC
	SurfaceMCP
	SurfaceGraphQL
	SurfaceES
	surfaceCount
)

var surfaceNames = [surfaceCount]string{"rest", "grpc", "mcp", "graphql", "es"}

// String is the surface's label.
func (s Surface) String() string {
	if s >= surfaceCount {
		return "other"
	}
	return surfaceNames[s]
}

// SearchMode is the kind of search that ran.
type SearchMode uint8

// The search modes.
const (
	ModeFTS SearchMode = iota
	ModeVector
	ModeHybrid
	ModeMeta // a metadata-filter listing (/v1/search)
	modeCount
)

var modeNames = [modeCount]string{"fts", "vector", "hybrid", "meta"}

// FilterOp is one metadata filter operator (SRCH-017), or a range filter.
type FilterOp uint8

// The filter operators, as the API names them.
const (
	FilterIn FilterOp = iota
	FilterPrefix
	FilterContains
	FilterGt
	FilterGte
	FilterLt
	FilterLte
	FilterNot
	FilterExists
	FilterRange
	filterOpCount
)

var filterOpNames = [filterOpCount]string{"in", "prefix", "contains", "gt", "gte", "lt", "lte", "not", "exists", "range"}

// FilterOps is a set of filter operators.
type FilterOps uint16

// With returns the set with op added.
func (f FilterOps) With(op FilterOp) FilterOps { return f | 1<<op }

// algorithmNames are the algorithms counted by name; anything else is
// "other", so a typo in a request cannot grow the label set.
var algorithmNames = []string{"tfidf", "bm25", "bm25f", "pmisparse", "flat", "hnsw", "ivf", "pq", "opq", "sq", "sq4", "bq", "quantized", "other"}

const algorithmOther = 13 // index of "other" in algorithmNames

// algorithmIndex maps an algorithm name to its counter without allocating.
func algorithmIndex(name string) int {
	for i, n := range algorithmNames[:algorithmOther] {
		if n == name {
			return i
		}
	}
	return algorithmOther
}

// histogram buckets of small whole numbers: conditions per filter, requested
// graph depth, decomposition parts and graph chain length. The last bucket is
// open: "5+".
const smallBuckets = 6

var smallBucketNames = [smallBuckets]string{"0", "1", "2", "3", "4", "5+"}

func smallBucket(n int) int {
	return max(0, min(n, smallBuckets-1))
}

// maxShapeCollections bounds the per-collection table; collections past it
// count under "_other", so a client inventing names cannot grow memory.
const maxShapeCollections = 512

// otherCollection is the per-collection row for collections past the bound.
const otherCollection = "_other"

// SearchShape is one search, as it is counted. It carries no query text.
type SearchShape struct {
	Surface    Surface
	Mode       SearchMode
	Collection string // held in memory only; the bundle hashes it
	// Algorithm is the algorithm that ran; names outside the known set
	// count as "other", and none (a metadata listing) is not counted.
	Algorithm string
	// Auto reports that the caller asked for "auto" and Algorithm is the
	// planner's choice (SRCH-013).
	Auto bool
	// FilterOps are the metadata filter operators the search used and
	// Conditions the number of keys they constrained.
	FilterOps  FilterOps
	Conditions int
	Results    int
	// LimitCapped reports that the server's cap lowered the result count
	// (#368).
	LimitCapped bool
	// DecomposedParts is the number of sub-queries a decomposed query ran
	// (SRCH-020); 0 or 1 when it was not split.
	DecomposedParts int
}

// collShape is one collection's counters.
type collShape struct {
	searches    [modeCount]atomic.Int64
	empty       [modeCount]atomic.Int64
	limitCapped atomic.Int64
	graph       atomic.Int64
	clamped     atomic.Int64
}

// QueryShape holds the counters. The zero value is not usable; use
// NewQueryShape. A nil *QueryShape counts nothing.
type QueryShape struct {
	since time.Time

	searches    [surfaceCount][modeCount]atomic.Int64
	empty       [surfaceCount][modeCount]atomic.Int64
	limitCapped [surfaceCount][modeCount]atomic.Int64
	algorithms  [modeCount][algorithmOther + 1]atomic.Int64
	autoChoices [algorithmOther + 1]atomic.Int64
	filterOps   [filterOpCount]atomic.Int64
	conditions  [smallBuckets]atomic.Int64
	decomposed  [surfaceCount]atomic.Int64
	parts       atomic.Int64

	graph graphCounters

	collMu sync.RWMutex
	colls  map[string]*collShape
}

// NewQueryShape returns empty counters.
func NewQueryShape() *QueryShape {
	q := &QueryShape{since: time.Now(), colls: map[string]*collShape{}}
	q.graph.chains = newChainTracker(time.Now)
	return q
}

// collection returns the row for name, creating it on first sight. The
// common case — a collection already seen — takes a read lock and allocates
// nothing.
func (q *QueryShape) collection(name string) *collShape {
	q.collMu.RLock()
	c := q.colls[name]
	q.collMu.RUnlock()
	if c != nil {
		return c
	}
	q.collMu.Lock()
	defer q.collMu.Unlock()
	// Looked up again: another search may have added it since the read.
	if c = q.colls[name]; c == nil {
		if len(q.colls) >= maxShapeCollections {
			name = otherCollection
			c = q.colls[name]
		}
		if c == nil {
			c = &collShape{}
			q.colls[name] = c
		}
	}
	return c
}

// Search counts one search.
func (q *QueryShape) Search(s SearchShape) {
	if q == nil || s.Surface >= surfaceCount || s.Mode >= modeCount {
		return
	}
	q.searches[s.Surface][s.Mode].Add(1)
	c := q.collection(s.Collection)
	c.searches[s.Mode].Add(1)
	if s.Results == 0 {
		q.empty[s.Surface][s.Mode].Add(1)
		c.empty[s.Mode].Add(1)
	}
	if s.LimitCapped {
		q.limitCapped[s.Surface][s.Mode].Add(1)
		c.limitCapped.Add(1)
	}
	if s.Algorithm != "" { // a metadata listing runs none
		algo := algorithmIndex(s.Algorithm)
		q.algorithms[s.Mode][algo].Add(1)
		if s.Auto {
			q.autoChoices[algo].Add(1)
		}
	}
	for op := FilterOp(0); op < filterOpCount; op++ {
		if s.FilterOps&(1<<op) != 0 {
			q.filterOps[op].Add(1)
		}
	}
	q.conditions[smallBucket(s.Conditions)].Add(1)
	if s.DecomposedParts > 1 {
		q.decomposed[s.Surface].Add(1)
		q.parts.Add(int64(s.DecomposedParts))
	}
}
