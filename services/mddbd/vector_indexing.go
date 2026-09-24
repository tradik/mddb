package main

// Putting one chunk's vector into the in-memory indexes (#252).
//
// The same block — every float32 searcher except the quantized one, then the
// quantized index, skipping float32 entirely for disk-only collections — was
// written out at three call sites, and each discarded what the indexes had to
// say. When an index refused a vector the chunk simply was not there, and the
// caller reported the document as embedded. This is the one copy, and it
// returns the refusal so the caller can count it.

// indexChunk adds one chunk vector to every in-memory index the server keeps.
//
// Every index is attempted even after one refuses, so a vector the graph cannot
// hold still reaches the ones that can; the first refusal is what the caller
// gets back, because a chunk missing from the index it was asked for is a
// failed chunk whatever the others did.
func (s *Server) indexChunk(collection, chunkKey string, vec []float32, diskOnly bool) error {
	var first error
	keep := func(err error) {
		if err != nil && first == nil {
			first = err
		}
	}

	if !diskOnly {
		for name, searcher := range s.VectorSearchers {
			if name == "quantized" {
				continue // populated below, for disk-only collections too
			}
			keep(searcher.Add(collection, chunkKey, vec))
		}
	}
	if s.QuantizedVecIndex != nil {
		keep(s.QuantizedVecIndex.Add(collection, chunkKey, vec))
	}
	return first
}
