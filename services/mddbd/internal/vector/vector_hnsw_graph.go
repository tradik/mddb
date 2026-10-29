package vector

import (
	"math"
	"math/rand"
	"sort"
	"sync"
)

// hnswGraph is the navigable small-world graph behind HNSWIndex (SRCH-013).
//
// Until 2.18.9 the graph was github.com/coder/hnsw. The planner benchmark
// measured its recall@10 at 0.86 (2k), 0.83 (200k) and 0.66 (2M) on a
// clustered corpus, and 0.70–0.83 between loads of the same 200k. The causes
// were in the library, not in its parameters:
//
//   - neighbours were the M closest candidates, with no diversity heuristic
//     (Malkov & Yashunin, Algorithm 4). Documents arrive topic by topic, so the
//     closest candidates of a new node are all in its own topic, the few edges
//     towards other topics are the first ones evicted, and topics become
//     islands a search entering the wrong one cannot leave;
//   - layer 0 kept M links where the paper keeps 2M;
//   - construction searched with EfSearch: efConstruction had no field, which
//     is why NewHNSWIndex ignored its second argument;
//   - the entry point was whichever node a map iteration yielded first, and
//     levels came from a clock-seeded generator, so two loads of the same data
//     built different graphs.
//
// This graph keeps nodes in slices addressed by int32, selects neighbours with
// the heuristic, keeps 2M links on layer 0, takes efConstruction separately,
// and draws levels from a fixed seed. Nodes are never deleted: HNSWIndex
// tombstones superseded keys and rebuilds the graph (#267), so the graph only
// adds and searches. It is not safe for concurrent writes; HNSWIndex's lock
// serialises Add against Search.
type hnswGraph struct {
	m, m0          int // links kept per node above layer 0, and on layer 0
	efConstruction int // candidates considered when linking a new node
	efSearch       int // candidates kept by a search; at least k
	ml             float64

	rng     *rand.Rand
	keys    []string
	vecs    [][]float32
	invNorm []float32   // 1/|v|, so the cosine distance is one dot product
	links   [][][]int32 // node → layer → neighbours
	entry   int32       // -1 while empty
	top     int         // the entry point's layer

	visits sync.Pool // *visitSet
}

// hnswSeed seeds level generation. Fixed, so one insertion order always builds
// one graph: recall must not depend on the clock.
const hnswSeed = 0x5eed

// hnswHit is a node a search found, with its distance to the query.
type hnswHit struct {
	Key   string
	Value []float32
	dist  float32
}

// newHNSWGraph returns an empty graph. Parameters are the caller's, already
// defaulted.
func newHNSWGraph(m, efConstruction, efSearch int) *hnswGraph {
	return &hnswGraph{
		m: m, m0: 2 * m, efConstruction: efConstruction, efSearch: efSearch,
		ml:    1 / math.Log(float64(m)),
		rng:   rand.New(rand.NewSource(hnswSeed)), // #nosec G404 -- graph levels, not security
		entry: -1,
	}
}

// Len is the number of nodes, tombstones included.
func (g *hnswGraph) Len() int { return len(g.keys) }

// cand is a node and its distance to whatever is being searched for.
type cand struct {
	id   int32
	dist float32
}

// norm returns 1/|v|, or 0 for a zero vector, which then sits at distance 1
// from everything.
func invNormOf(v []float32) float32 {
	var s float64
	for _, x := range v {
		s += float64(x) * float64(x)
	}
	if s == 0 {
		return 0
	}
	return float32(1 / math.Sqrt(s))
}

// dist is the cosine distance between a vector and node b: the graph orders by
// cosine whatever metric the search scores with. Embeddings are compared by
// angle, and for unit vectors dot product and euclidean order alike.
func (g *hnswGraph) dist(q []float32, qInv float32, b int32) float32 {
	return 1 - dotProductSimilarity(q, g.vecs[b])*qInv*g.invNorm[b]
}

// Add inserts one node under key.
func (g *hnswGraph) Add(key string, vec []float32) {
	id := int32(len(g.keys)) // #nosec G115 -- a collection of 2^31 vectors does not fit in memory first
	level := int(-math.Log(1-g.rng.Float64()) * g.ml)
	inv := invNormOf(vec)
	g.keys = append(g.keys, key)
	g.vecs = append(g.vecs, vec)
	g.invNorm = append(g.invNorm, inv)
	g.links = append(g.links, make([][]int32, level+1))
	if g.entry < 0 {
		g.entry, g.top = id, level
		return
	}

	ep := []cand{{g.entry, g.dist(vec, inv, g.entry)}}
	for l := g.top; l > level; l-- {
		ep = []cand{g.greedy(vec, inv, ep[0], l)}
	}
	for l := min(level, g.top); l >= 0; l-- {
		found := g.searchLayer(vec, inv, ep, g.efConstruction, l)
		neighbours := g.selectNeighbours(found, g.m)
		g.links[id][l] = neighbours
		for _, n := range neighbours {
			g.link(n, id, l)
		}
		ep = found
	}
	if level > g.top {
		g.entry, g.top = id, level
	}
}

// link adds the edge from → to on layer l, pruning from's neighbours by the
// heuristic when they exceed the layer's limit.
func (g *hnswGraph) link(from, to int32, l int) {
	limit := g.m
	if l == 0 {
		limit = g.m0
	}
	ns := append(g.links[from][l], to)
	if len(ns) <= limit {
		g.links[from][l] = ns
		return
	}
	v, inv := g.vecs[from], g.invNorm[from]
	cands := make([]cand, len(ns))
	for i, n := range ns {
		cands[i] = cand{n, g.dist(v, inv, n)}
	}
	sortCands(cands)
	g.links[from][l] = g.selectNeighbours(cands, limit)
}

// selectNeighbours is the paper's heuristic (Algorithm 4) over candidates
// sorted nearest first: a candidate is kept only if it is nearer to the base
// than to every neighbour already kept. Edges then point in different
// directions, and the ones leaving a cluster survive.
func (g *hnswGraph) selectNeighbours(cands []cand, limit int) []int32 {
	out := make([]int32, 0, limit)
	for _, c := range cands {
		if len(out) == limit {
			break
		}
		v, inv := g.vecs[c.id], g.invNorm[c.id]
		keep := true
		for _, r := range out {
			if g.dist(v, inv, r) < c.dist {
				keep = false
				break
			}
		}
		if keep {
			out = append(out, c.id)
		}
	}
	return out
}

// greedy walks layer l from ep to the nearest node it can reach.
func (g *hnswGraph) greedy(q []float32, qInv float32, ep cand, l int) cand {
	for moved := true; moved; {
		moved = false
		for _, n := range g.links[ep.id][l] {
			if d := g.dist(q, qInv, n); d < ep.dist {
				ep, moved = cand{n, d}, true
			}
		}
	}
	return ep
}

// searchLayer is the beam search of one layer (Algorithm 2): the ef nearest
// nodes reachable from ep, nearest first.
func (g *hnswGraph) searchLayer(q []float32, qInv float32, ep []cand, ef, l int) []cand {
	seen := g.visitSet()
	defer g.visits.Put(seen)

	todo := candHeap{}                // nearest first
	found := candHeap{farthest: true} // farthest first, at most ef
	// ep is one node, or the previous layer's result of the same ef: never
	// more than ef.
	for _, c := range ep {
		seen.mark(c.id)
		todo.push(c)
		found.push(c)
	}
	for todo.len() > 0 {
		c := todo.pop()
		if found.len() >= ef && c.dist > found.peek().dist {
			break
		}
		for _, n := range g.links[c.id][l] {
			if !seen.mark(n) {
				continue
			}
			d := g.dist(q, qInv, n)
			if found.len() < ef || d < found.peek().dist {
				todo.push(cand{n, d})
				found.push(cand{n, d})
				if found.len() > ef {
					found.pop()
				}
			}
		}
	}
	out := found.items
	sortCands(out)
	return out
}

// Search returns the k nodes nearest q, nearest first, searching layer 0 with
// a beam of max(efSearch, k).
func (g *hnswGraph) Search(q []float32, k int) []hnswHit {
	if g.entry < 0 || k <= 0 {
		return nil
	}
	inv := invNormOf(q)
	ep := cand{g.entry, g.dist(q, inv, g.entry)}
	for l := g.top; l > 0; l-- {
		ep = g.greedy(q, inv, ep, l)
	}
	found := g.searchLayer(q, inv, []cand{ep}, max(g.efSearch, k), 0)
	if len(found) > k {
		found = found[:k]
	}
	hits := make([]hnswHit, len(found))
	for i, c := range found {
		hits[i] = hnswHit{Key: g.keys[c.id], Value: g.vecs[c.id], dist: c.dist}
	}
	return hits
}

// visitSet marks nodes seen by one search. A generation counter clears it in
// O(1), so a pooled set costs nothing to reuse.
type visitSet struct {
	marks []uint32
	gen   uint32
}

// visitSet takes a set from the pool, sized for the graph and cleared.
func (g *hnswGraph) visitSet() *visitSet {
	s, _ := g.visits.Get().(*visitSet)
	if s == nil {
		s = &visitSet{}
	}
	if len(s.marks) < len(g.keys) {
		s.marks = make([]uint32, len(g.keys)+len(g.keys)/4)
		s.gen = 0
	}
	s.gen++
	if s.gen == 0 { // wrapped: old marks could collide with the new generation
		clear(s.marks)
		s.gen = 1
	}
	return s
}

// mark records id and reports whether it was new.
func (s *visitSet) mark(id int32) bool {
	if s.marks[id] == s.gen {
		return false
	}
	s.marks[id] = s.gen
	return true
}

// candHeap is a binary heap of candidates, nearest on top unless farthest.
type candHeap struct {
	items    []cand
	farthest bool
}

func (h *candHeap) len() int   { return len(h.items) }
func (h *candHeap) peek() cand { return h.items[0] }
func (h *candHeap) less(i, j int) bool {
	if h.farthest {
		return h.items[i].dist > h.items[j].dist
	}
	return h.items[i].dist < h.items[j].dist
}

func (h *candHeap) push(c cand) {
	h.items = append(h.items, c)
	for i := len(h.items) - 1; i > 0; {
		p := (i - 1) / 2
		if !h.less(i, p) {
			break
		}
		h.items[i], h.items[p] = h.items[p], h.items[i]
		i = p
	}
}

func (h *candHeap) pop() cand {
	top := h.items[0]
	last := len(h.items) - 1
	h.items[0] = h.items[last]
	h.items = h.items[:last]
	for i := 0; ; {
		l, r, m := 2*i+1, 2*i+2, i
		if l < last && h.less(l, m) {
			m = l
		}
		if r < last && h.less(r, m) {
			m = r
		}
		if m == i {
			break
		}
		h.items[i], h.items[m] = h.items[m], h.items[i]
		i = m
	}
	return top
}

// sortCands orders candidates nearest first, ties by id for determinism.
func sortCands(cs []cand) {
	sort.Slice(cs, func(i, j int) bool {
		if cs[i].dist != cs[j].dist {
			return cs[i].dist < cs[j].dist
		}
		return cs[i].id < cs[j].id
	})
}
