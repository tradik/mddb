package vector

// Dropping a collection from every index (#331).
//
// Deleting a collection removed its documents and left its vectors: in every
// in-memory index, in the vector store and in /v1/vector-stats. A collection
// written again under the same name reported 15,540 embedded documents
// against 690 that existed, so a job waiting for "embedded >= written"
// finished at once and reported success, and searches could rank vectors of
// documents that were gone. Each index keeps one map entry per collection,
// so dropping one is a single delete; a collection recreated later starts
// untrained and empty.

// DropCollection removes every vector the flat index holds for a collection.
func (vi *VectorIndex) DropCollection(collection string) {
	vi.mu.Lock()
	defer vi.mu.Unlock()
	delete(vi.collections, collection)
}

// DropCollection removes a collection's graph, vectors and bookkeeping.
func (h *HNSWIndex) DropCollection(collection string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.graphs, collection)
	delete(h.vectors, collection)
	delete(h.deleted, collection)
	delete(h.dims, collection)
	delete(h.nodeKeys, collection)
}

// DropCollection removes a collection and its trained clusters.
func (idx *IVFIndex) DropCollection(collection string) {
	idx.mu.Lock()
	defer idx.mu.Unlock()
	delete(idx.data, collection)
}

// DropCollection removes a collection and its codebooks.
func (p *PQIndex) DropCollection(collection string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.data, collection)
}

// DropCollection removes a collection, its rotation and codebooks.
func (o *OPQIndex) DropCollection(collection string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	delete(o.data, collection)
}

// DropCollection removes a collection and its scales.
func (s *SQIndex) DropCollection(collection string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.data, collection)
}

// DropCollection removes a collection and its scales.
func (s *SQ4Index) DropCollection(collection string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.data, collection)
}

// DropCollection removes a collection's binary codes.
func (b *BQIndex) DropCollection(collection string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.data, collection)
}

// DropCollection removes a collection's quantized vectors.
func (qi *QuantizedVectorIndex) DropCollection(collection string) {
	qi.mu.Lock()
	defer qi.mu.Unlock()
	delete(qi.collections, collection)
}
