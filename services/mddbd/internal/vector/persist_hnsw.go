package vector

import (
	"fmt"
	"strings"
)

// The HNSW graph in a checkpoint (SRCH-023).
//
// A graph section is the graph as it stands: every node with its key, its
// layers and their neighbours, the entry point, and which nodes are
// tombstones. A live node carries the fingerprint of its vector and takes
// the vector itself from the store at load; a tombstone's vector is in no
// store any more, and the graph still routes through it, so it is written
// out in full. Compaction keeps tombstones under half of the graph, which
// bounds what that costs.
//
// At load the section is used only if every live node finds its vector in
// the store, unchanged. A node whose document was removed or re-embedded
// after the checkpoint would have to become a tombstone, and its old vector
// is gone, so the graph is built again from the stored vectors instead.
// Vectors stored after the checkpoint are simply added to the loaded graph.

// persistMaxLayers bounds a node's layer count. Levels are drawn from an
// exponential distribution with mean 1/ln(m): 64 is never reached.
const persistMaxLayers = 64

// graphSnapshot is a graph captured under the read lock. Everything in it
// is a slice header onto memory the graph never writes again — keys,
// vectors and neighbour lists are appended or replaced, never changed in
// place — so it can be written after the lock is released.
type graphSnapshot struct {
	m, efConstruction, dim int
	entry                  int32
	top                    int
	gen                    uint64
	keys                   []string
	vecs                   [][]float32
	live                   []bool
	levels                 []uint8   // layers per node
	layers                 [][]int32 // every node's layers, in node order
}

// graphSection is a graph section decoded from a checkpoint.
type graphSection struct {
	m, efConstruction, dim int
	entry                  int32
	top                    int
	gen                    uint64
	keys                   []string
	live                   []bool
	hashes                 []uint64    // of live nodes' vectors
	vecs                   [][]float32 // of tombstones; nil for a live node
	links                  [][][]int32
}

// PersistStamp implements Persistable: the number of adds and removes the
// collection has seen. Every vector write reaches every index
// (vector_indexing.go), so this one counter stands for the writes to all of
// them.
func (h *HNSWIndex) PersistStamp(collection string) uint64 {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.mutations[collection]
}

func (h *HNSWIndex) persistKind() byte { return persistKindGraph }

// persistSnapshot implements Persistable.
func (h *HNSWIndex) persistSnapshot(collection string) persistSnapshot {
	h.mu.RLock()
	defer h.mu.RUnlock()
	g, ok := h.graphs[collection]
	if !ok || g.Len() == 0 {
		return nil
	}
	n := g.Len()
	s := &graphSnapshot{
		m: g.m, efConstruction: g.efConstruction, dim: h.dims[collection],
		entry: g.entry, top: g.top, gen: h.gen,
		keys: g.keys[:n:n], vecs: g.vecs[:n:n],
		live: make([]bool, n), levels: make([]uint8, n),
		layers: make([][]int32, 0, n+n/8),
	}
	for i := range n {
		_, s.live[i] = h.liveDocID(collection, g.keys[i])
		s.levels[i] = uint8(len(g.links[i])) // #nosec G115 -- levels stay far below persistMaxLayers
		s.layers = append(s.layers, g.links[i]...)
	}
	return s
}

// encode implements persistSnapshot.
func (s *graphSnapshot) encode(e *persistEncoder) {
	e.u32(uint32(s.m))              // #nosec G115 -- a configured small number
	e.u32(uint32(s.efConstruction)) // #nosec G115 -- likewise
	e.u32(uint32(s.dim))            // #nosec G115 -- a vector length
	e.u32(uint32(s.entry))          // #nosec G115 -- a node id, never negative in a graph with nodes
	e.u32(uint32(s.top))            // #nosec G115 -- a layer number
	e.u64(s.gen)
	e.u64(uint64(len(s.keys)))
	layer := 0
	for i, key := range s.keys {
		e.str(key)
		if s.live[i] {
			e.u8(1)
			e.u64(vectorFingerprint(s.vecs[i]))
		} else {
			e.u8(0)
			e.f32s(s.vecs[i])
		}
		e.u8(s.levels[i])
		for range s.levels[i] {
			e.u16(uint16(len(s.layers[layer]))) // #nosec G115 -- at most 2m neighbours
			e.i32s(s.layers[layer])
			layer++
		}
	}
}

// decodeGraphSection reads a graph section and checks that it is a graph a
// search can walk: every neighbour exists and has the layer it is linked on.
func decodeGraphSection(d *persistDecoder) *graphSection {
	s := &graphSection{
		m: int(d.u32()), efConstruction: int(d.u32()), dim: int(d.u32()),
		entry: int32(d.u32()), // #nosec G115 -- checked against the node count below
		top:   int(d.u32()), gen: d.u64(),
	}
	nodes := d.u64()
	if d.err == nil && (s.m < 2 || s.m > 1<<12 || s.efConstruction < 1 || s.dim < 1 || s.dim > 1<<20 || nodes == 0 || nodes > 1<<31-1) {
		d.fail(corruptf("graph of %d nodes with m=%d, efConstruction=%d, dimension=%d", nodes, s.m, s.efConstruction, s.dim))
	}
	// A node is at least a key length, a flag, a vector or its fingerprint,
	// a layer count and one neighbour count.
	n := d.count(nodes, 12)
	for i := 0; i < n && d.err == nil; i++ {
		s.readNode(d)
	}
	if d.err == nil {
		d.fail(s.check())
	}
	return s
}

// readNode reads one node; slices grow as nodes are actually read, so a
// count that lied costs nothing.
func (s *graphSection) readNode(d *persistDecoder) {
	key := d.str()
	live := d.u8() == 1
	var hash uint64
	var vec []float32
	if live {
		hash = d.u64()
	} else {
		vec = d.f32s(d.count(uint64(s.dim), 4)) // #nosec G115 -- dim is 1..2^20
	}
	levels := int(d.u8())
	if d.err == nil && (levels < 1 || levels > persistMaxLayers || strings.IndexByte(key, 0) < 0) {
		d.fail(corruptf("node %q with %d layers", key, levels))
		return
	}
	layers := make([][]int32, levels)
	for l := range layers {
		count := int(d.u16())
		if limit := s.layerLimit(l); count > limit {
			d.fail(corruptf("node with %d neighbours on layer %d, the limit is %d", count, l, limit))
			return
		}
		layers[l] = d.i32s(d.count(uint64(count), 4)) // #nosec G115 -- count is a uint16
	}
	s.keys = append(s.keys, key)
	s.live = append(s.live, live)
	s.hashes = append(s.hashes, hash)
	s.vecs = append(s.vecs, vec)
	s.links = append(s.links, layers)
}

// layerLimit is the most neighbours a node keeps on layer l.
func (s *graphSection) layerLimit(l int) int {
	if l == 0 {
		return 2 * s.m
	}
	return s.m
}

// check reports why the decoded graph cannot be searched, or nil. A search
// follows links[n][l] for every neighbour n it meets on layer l, so each of
// them has to be a node that has that layer.
func (s *graphSection) check() error {
	n := len(s.keys)
	if s.entry < 0 || int(s.entry) >= n || len(s.links[s.entry]) != s.top+1 {
		return corruptf("entry point %d on layer %d of a graph of %d nodes", s.entry, s.top, n)
	}
	for i, layers := range s.links {
		if len(layers) > s.top+1 {
			return corruptf("node %d has %d layers above an entry point on layer %d", i, len(layers), s.top)
		}
		for l, neighbours := range layers {
			for _, nb := range neighbours {
				if nb < 0 || int(nb) >= n || len(s.links[nb]) <= l {
					return corruptf("node %d links to %d on layer %d, which is not there", i, nb, l)
				}
			}
		}
	}
	return nil
}

// persistRestore implements Persistable.
func (h *HNSWIndex) persistRestore(collection string, section any, vectors map[string][]float32) ([]string, error) {
	s, ok := section.(*graphSection)
	if !ok {
		return nil, corruptf("the hnsw section is not a graph")
	}
	if s.m != h.m || s.efConstruction != h.efConstruction {
		return nil, fmt.Errorf("%w: the graph was built with m=%d, efConstruction=%d and the index now uses m=%d, efConstruction=%d",
			ErrCheckpointStale, s.m, s.efConstruction, h.m, h.efConstruction)
	}
	live, keys, err := s.attach(vectors)
	if err != nil {
		return nil, err
	}

	g := newHNSWGraph(h.m, h.efConstruction, h.efSearch)
	g.keys, g.vecs, g.links = s.keys, s.vecs, s.links
	g.entry, g.top = s.entry, s.top
	g.invNorm = make([]float32, len(s.vecs))
	for i, v := range s.vecs {
		g.invNorm[i] = invNormOf(v)
		// Levels come from a seeded generator, one draw per node: drawing
		// as many leaves it where it was, so the next add gets the level it
		// would have got had the process never stopped.
		g.rng.Float64()
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	h.graphs[collection] = g
	h.vectors[collection] = live
	h.nodeKeys[collection] = keys
	h.deleted[collection] = len(s.keys) - len(live)
	h.dims[collection] = s.dim
	h.gen = max(h.gen, s.gen)
	h.mutations[collection]++
	return missingFrom(vectors, live), nil
}

// attach gives every live node its vector from the store, checked against
// the fingerprint the checkpoint recorded, and returns the live vectors and
// node keys by document. The section is stale if any live node's vector is
// gone or has changed.
func (s *graphSection) attach(vectors map[string][]float32) (map[string][]float32, map[string]string, error) {
	live := make(map[string][]float32, len(s.keys))
	keys := make(map[string]string, len(s.keys))
	for i, key := range s.keys {
		if !s.live[i] {
			continue
		}
		docID := key[:strings.LastIndexByte(key, 0)]
		v, ok := vectors[docID]
		if !ok || len(v) != s.dim || vectorFingerprint(v) != s.hashes[i] {
			return nil, nil, fmt.Errorf("%w: the vector of %q was removed or replaced after the checkpoint", ErrCheckpointStale, docID)
		}
		if _, twice := keys[docID]; twice {
			return nil, nil, corruptf("two live nodes for %q", docID)
		}
		s.vecs[i] = v
		live[docID] = v
		keys[docID] = key
	}
	return live, keys, nil
}
