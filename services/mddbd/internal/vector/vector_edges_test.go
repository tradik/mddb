package vector

import (
	"testing"

	bolt "go.etcd.io/bbolt"
)

// Edges that callers rely on but no test pinned down.

// Search results are deduplicated to parent documents, so a caller holding a
// base id asks for its vector and must get the first chunk's; an id that was
// stored whole must come back as stored, and an unknown collection is nil.
func TestGetVectorResolvesAParentIDToItsFirstChunk(t *testing.T) {
	vi := NewVectorIndex()
	if err := vi.Add("c", "whole", []float32{1, 0}); err != nil {
		t.Fatal(err)
	}
	if err := vi.Add("c", "parent#0", []float32{0, 1}); err != nil {
		t.Fatal(err)
	}

	if v := vi.GetVector("c", "whole"); len(v) != 2 || v[0] != 1 {
		t.Errorf("whole = %v, want the stored vector", v)
	}
	if v := vi.GetVector("c", "parent"); len(v) != 2 || v[1] != 1 {
		t.Errorf("parent = %v, want its first chunk", v)
	}
	if v := vi.GetVector("c", "nobody"); v != nil {
		t.Errorf("an unknown id returned %v", v)
	}
	if v := vi.GetVector("other", "whole"); v != nil {
		t.Errorf("an unknown collection returned %v", v)
	}
}

// The document count on /v1/stats walks raw keys. A key that is not a vector
// key must be ignored rather than counted, and a document id that itself
// contains '#' is one document — only a numeric suffix is a chunk index.
func TestCountsIgnoreForeignKeysAndKeepHashesInDocIDs(t *testing.T) {
	vs, db := openStore(t, true)
	chunks := []ChunkEmbedding{{ChunkIndex: 0, Vector: []float32{1}}, {ChunkIndex: 1, Vector: []float32{1}}}
	if err := vs.PutChunks("c", "notes#intro", chunks, "m", "h"); err != nil {
		t.Fatal(err)
	}
	if err := db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte("vectors"))
		for _, k := range []string{"meta", "vec|nopipe", "vec|c|notes#extra"} {
			if err := b.Put([]byte(k), []byte{0}); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	docs, err := vs.CountByCollection()
	if err != nil {
		t.Fatal(err)
	}
	// "notes#intro" (two chunks) and "notes#extra" (no numeric suffix) are two
	// documents; the keys without a collection are not documents at all.
	if docs["c"] != 2 || len(docs) != 1 {
		t.Errorf("CountByCollection = %v, want map[c:2]", docs)
	}

	chunkCounts, err := vs.CountChunksByCollection()
	if err != nil {
		t.Fatal(err)
	}
	if chunkCounts["c"] != 3 || len(chunkCounts) != 1 {
		t.Errorf("CountChunksByCollection = %v, want map[c:3]", chunkCounts)
	}
}

// A query is quantized against the calibration of the stored vectors, and a
// query component outside that range has to clamp to the ends of the scale
// rather than wrap around to the other end of it.
func TestQueryQuantizationClampsOutsideTheCalibratedRange(t *testing.T) {
	q8 := QuantizeQueryForInt8([]float32{-5, 5}, 0, 1)
	if q8.Data[0] != 0 || q8.Data[1] != 255 {
		t.Errorf("int8 = %v, want [0 255]", q8.Data)
	}
	q4 := QuantizeQueryForInt4([]float32{-5, 5}, 0, 1)
	if q4.Data[0] != 0x0F {
		t.Errorf("int4 = %#x, want 0x0f (low end, high end)", q4.Data[0])
	}

	// A degenerate calibration (min == max) must not divide by zero.
	if q := QuantizeQueryForInt8([]float32{0.5}, 1, 1); q.Dims != 1 {
		t.Errorf("constant calibration gave %+v", q)
	}
	if q := QuantizeQueryForInt4([]float32{0.5}, 1, 1); q.Dims != 1 {
		t.Errorf("constant calibration gave %+v", q)
	}
}

// Similarity between vectors that cannot be compared is zero, not a panic
// and not NaN: different dimensions, no dimensions, or a zero vector.
func TestQuantizedSimilarityOfIncomparableVectorsIsZero(t *testing.T) {
	a8 := QuantizeQueryForInt8([]float32{1, 1}, 0, 1)
	zero8 := QuantizeQueryForInt8([]float32{0, 0}, 0, 1)
	short8 := QuantizeQueryForInt8([]float32{1}, 0, 1)
	a4 := QuantizeQueryForInt4([]float32{1, 1}, 0, 1)
	zero4 := QuantizeQueryForInt4([]float32{0, 0}, 0, 1)
	short4 := QuantizeQueryForInt4([]float32{1}, 0, 1)

	for name, got := range map[string]float32{
		"int8 dims":  CosineSimInt8(a8, short8),
		"int8 zero":  CosineSimInt8(a8, zero8),
		"int8 empty": CosineSimInt8(&QuantizedVector{}, &QuantizedVector{}),
		"int4 dims":  CosineSimInt4(a4, short4),
		"int4 zero":  CosineSimInt4(a4, zero4),
		"int4 empty": CosineSimInt4(&QuantizedVector{}, &QuantizedVector{}),
	} {
		if got != 0 {
			t.Errorf("%s = %v, want 0", name, got)
		}
	}
}

// A record whose payload is shorter than its declared dimensions, or of a
// type this build does not know, must not be read as a vector. The store
// turns nil into a "data length mismatch" error.
func TestDequantizingAMalformedVectorGivesNothing(t *testing.T) {
	if v := DequantizeToFloat32(&QuantizedVector{Type: QuantNone, Dims: 2}); v != nil {
		t.Errorf("unknown type dequantized to %v", v)
	}
	if v := DequantizeToFloat32(&QuantizedVector{Type: QuantInt8, Dims: 4, Data: []byte{1}}); v != nil {
		t.Errorf("short int8 payload dequantized to %v", v)
	}
	if v := DequantizeToFloat32(&QuantizedVector{Type: QuantInt4, Dims: 4, Data: []byte{1}}); v != nil {
		t.Errorf("short int4 payload dequantized to %v", v)
	}
}

// Re-embedding a document replaces its vector in the graph; the old position
// must not keep answering for it.
func TestReAddingAChunkToHNSWMovesIt(t *testing.T) {
	h := NewHNSWIndex(16, 200, 100)
	for id, v := range map[string][]float32{"a": {1, 0, 0}, "b": {0, 1, 0}, "c": {0, 0, 1}} {
		if err := h.Add("x", id, v); err != nil {
			t.Fatal(err)
		}
	}
	if err := h.Add("x", "a", []float32{0, 0.1, 1}); err != nil {
		t.Fatal(err)
	}

	hits := h.Search("x", []float32{1, 0, 0}, 3, 0, nil)
	for _, hit := range hits {
		if hit.DocID == "a" && hit.Score > 0.5 {
			t.Errorf("a still scores %v against its old position", hit.Score)
		}
	}
	if got := h.CollectionSize("x"); got != 3 {
		t.Errorf("CollectionSize = %d after a replace, want 3", got)
	}
}

// A filtered flat search called with no topK and no metric uses the defaults
// (5, cosine) instead of returning nothing.
func TestFilteredFlatSearchFallsBackToDefaults(t *testing.T) {
	vi := NewVectorIndex()
	for i, id := range []string{"a#0", "b#0", "c#0"} {
		v := []float32{0, 0, 0}
		v[i] = 1
		if err := vi.Add("x", id, v); err != nil {
			t.Fatal(err)
		}
	}
	hits := vi.SearchWithFilter("x", []float32{1, 0, 0}, 0, 0, map[string]bool{"a": true, "b": true}, nil)
	if len(hits) != 2 || hits[0].DocID != "a#0" {
		t.Errorf("hits = %+v, want a#0 first and c filtered out", hits)
	}
	if hits := vi.SearchWithFilter("none", []float32{1, 0, 0}, 0, 0, nil, nil); hits != nil {
		t.Errorf("an unknown collection returned %+v", hits)
	}
}

// Every searcher answers a filtered search the same way at the edges: an
// unknown collection is nil, and a topK of zero means the default rather than
// nothing. The server treats them as interchangeable, so they must be.
func TestEverySearcherHandlesFilteredSearchEdgesAlike(t *testing.T) {
	searchers := map[string]VectorSearcher{
		"flat": NewVectorIndex(), "hnsw": NewHNSWIndex(16, 200, 100), "ivf": NewIVFIndex(2, 2),
		"pq": NewPQIndex(2, 4, 2), "opq": NewOPQIndex(2, 4, 2, 1), "sq": NewSQIndex(),
		"sq4": NewSQ4Index(), "bq": NewBQIndex(10),
	}
	allowed := map[string]bool{"a": true, "b": true}
	for name, s := range searchers {
		t.Run(name, func(t *testing.T) {
			if hits := s.SearchWithFilter("none", []float32{1, 0, 0, 0}, 0, 0, allowed, nil); len(hits) != 0 {
				t.Errorf("an unknown collection returned %+v", hits)
			}
			for i, id := range []string{"a#0", "b#0", "c#0", "d#0"} {
				v := []float32{0.1, 0.1, 0.1, 0.1}
				v[i] = 1
				if err := s.Add("x", id, v); err != nil {
					t.Fatal(err)
				}
			}
			for _, hit := range s.SearchWithFilter("x", []float32{1, 0.1, 0.1, 0.1}, 0, 0, allowed, nil) {
				if !allowed[BaseDocID(hit.DocID)] {
					t.Errorf("%s returned %s, which the filter excludes", name, hit.DocID)
				}
			}
		})
	}
}

// Binary codes of different lengths still have a distance: the bits the
// shorter one lacks all count as differing.
func TestHammingDistanceCountsTheLongerCodesExtraBits(t *testing.T) {
	if d := hammingDistance([]uint64{0, 0b111}, []uint64{0}); d != 3 {
		t.Errorf("longer a: %d, want 3", d)
	}
	if d := hammingDistance([]uint64{0}, []uint64{0, 0b11}); d != 2 {
		t.Errorf("longer b: %d, want 2", d)
	}
}
