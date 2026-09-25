package main

import (
	"mddb/internal/embedding"
	"mddb/internal/vector"
)

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

// serverIndexes is every in-memory index the server keeps, seen as one.
//
// The embedding worker used to hold the flat index alone. Every document
// embedded after startup therefore reached flat search and nothing else: HNSW,
// IVF, PQ, OPQ, SQ, SQ4 and BQ answered from a snapshot of the store taken at
// the last startup or reindex, and a query naming one of them returned nothing
// for a document added a minute earlier — served by the index it asked for,
// with no error and no fallback. A restart repaired it, by reloading every
// index from the store, which is why it looked fine to anyone who checked
// after one. Replication and the gRPC reindex had the same blind spot.
//
// Passing this instead of an index gives every writer the same view.
type serverIndexes struct{ s *Server }

// IndexChunk adds a chunk to every index, routing disk-only collections to the
// quantized index alone.
func (x serverIndexes) IndexChunk(collection, chunkKey string, vec []float32) error {
	return x.s.indexChunk(collection, chunkKey, vec, x.s.collectionDiskOnly(collection))
}

// Remove drops a chunk from every index. The quantized index is one of the
// searchers, so iterating them covers it.
func (x serverIndexes) Remove(collection, chunkKey string) {
	for _, searcher := range x.s.VectorSearchers {
		searcher.Remove(collection, chunkKey)
	}
}

// startEmbeddingWorker builds, wires and starts the worker for a provider.
//
// Written out three times before — twice at startup and once when the
// embedding configuration changes at runtime — and the third copy had drifted:
// it passed a queue size of 1000 where the others read
// MDDB_EMBEDDING_QUEUE_SIZE, so changing the provider at runtime quietly
// reset the queue an operator had sized (#232).
func (s *Server) startEmbeddingWorker(provider embedding.Provider) {
	s.EmbeddingWorker = NewEmbeddingWorker(provider, s.VectorStore, s.VectorIndex, EmbeddingQueueSize())
	s.EmbeddingWorker.SetIndexer(serverIndexes{s})
	s.EmbeddingWorker.Start(2)
}

// newVectorSearchers builds the set of search algorithms a server offers.
//
// One function for production and tests. The test server used to build its
// own map with flat and quantized in it and nothing else, so no test ever ran
// against HNSW, IVF, PQ, OPQ, SQ, SQ4 or BQ — which is how every one of them
// missing each document embedded after startup went unnoticed. A fixture that
// assembles the indexes differently from production tests a server nobody
// runs.
func newVectorSearchers(flat *vector.VectorIndex, quantized *vector.QuantizedVectorIndex, bqRerank int) map[string]vector.VectorSearcher {
	return map[string]vector.VectorSearcher{
		"flat": flat,
		"hnsw": vector.NewHNSWIndex(16, 200, 100),
		"ivf":  vector.NewIVFIndex(10, 20),
		"pq":   vector.NewPQIndex(8, 256, 20),
		"opq":  vector.NewOPQIndex(8, 256, 20, 5),
		"sq":   vector.NewSQIndex(),
		// SRCH-003: 4 bits per dimension, two dimensions per byte. Keeps
		// 99.5% of int8's recall at half its storage — measured, not assumed;
		// see TestQuantizerRecallCurve.
		"sq4":       vector.NewSQ4Index(),
		"bq":        vector.NewBQIndex(bqRerank),
		"quantized": quantized,
	}
}
