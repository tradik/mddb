package vector

// Filter-aware graph search (SRCH-024).
//
// Until 2.19.8 a filtered HNSW search asked the graph for max(3×topK, 50)
// neighbours, dropped the ones the filter refused, and when fewer than topK
// were left scored the whole collection. A filter admitting 10% of 200,000
// vectors cost 36 ms that way, and the planner routed around the graph for
// anything under a third.
//
// Now the filter is part of the walk. A node the filter refuses — or a
// tombstone — is still a stepping stone: its distance is computed and its
// edges followed, because the nodes the filter admits may only be reachable
// through it. It never enters the result beam, and the beam's width counts
// admitted nodes only, so the search ends once it holds ef admitted nodes
// nearer than anything left to explore. Each node is asked about once, when
// the walk first finds it near enough to matter, so a search pays for the
// filter only on the nodes it touches.
//
// Two things the ticket proposed were measured and left out (docs/SEARCH.md,
// "Filtered search"):
//
//   - also visiting a refused neighbour's own neighbours (two hops) made the
//     walk 2–9× slower at 20k vectors from 2 to 50% and gained nothing at 1%;
//     skipping refused nodes altogether in favour of two hops lost recall
//     (0.89 at 20k and 0.94 at 200k vectors for a 1% filter) while staying
//     7–30× slower than the exact scan the planner uses there. The walk
//     does not get stuck without two hops: it never stops at a refused node;
//   - an allow-list built before the walk, a bitmap over node ids, costs one
//     lookup per admitted document: what the exact scan costs, before any walk.
//     Asking per node touched costs less wherever the walk is chosen.

// filteredBeam is the state of one filtered search of layer 0.
type filteredBeam struct {
	g     *hnswGraph
	q     []float32
	qInv  float32
	ef    int
	admit func(id int32) bool
	seen  *visitSet
	todo  candHeap // every node still to expand, nearest first
	found candHeap // admitted nodes only, farthest first, at most ef
}

// SearchFiltered returns the k nodes nearest q that admit allows, nearest
// first. ef is the number of admitted nodes the beam keeps; at least k.
func (g *hnswGraph) SearchFiltered(q []float32, k, ef int, admit func(id int32) bool) []hnswHit {
	if g.entry < 0 || k <= 0 {
		return nil
	}
	inv := invNormOf(q)
	b := &filteredBeam{g: g, q: q, qInv: inv, ef: max(ef, k), admit: admit, seen: g.visitSet(), found: candHeap{farthest: true}}
	defer g.visits.Put(b.seen)

	ep := g.descend(q, inv)
	b.seen.mark(ep.id)
	b.offer(ep)
	b.run()
	out := b.found.items
	sortCands(out)
	return g.hits(out, k)
}

// run expands nodes nearest first until the beam is full and nothing left to
// expand is nearer than its farthest admitted node.
func (b *filteredBeam) run() {
	for b.todo.len() > 0 {
		c := b.todo.pop()
		if b.found.len() >= b.ef && c.dist > b.found.peek().dist {
			return
		}
		for _, n := range b.g.links[c.id][0] {
			if b.seen.mark(n) {
				b.offer(cand{n, b.g.dist(b.q, b.qInv, n)})
			}
		}
	}
}

// offer queues a node for expansion if it could still improve the beam, and
// keeps it as a result if the filter admits it.
func (b *filteredBeam) offer(c cand) {
	if b.found.len() >= b.ef && c.dist >= b.found.peek().dist {
		return
	}
	b.todo.push(c)
	if b.admit(c.id) {
		b.found.push(c)
		if b.found.len() > b.ef {
			b.found.pop()
		}
	}
}
