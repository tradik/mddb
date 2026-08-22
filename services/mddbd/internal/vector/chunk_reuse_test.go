package vector

import (
	"encoding/binary"
	"math"
	"os"
	"testing"
	"time"

	bolt "go.etcd.io/bbolt"
)

// RAG-003 phase B. Editing one paragraph of a fifty-chunk document changed the
// document hash and re-embedded all fifty at full provider cost, even though
// forty-nine were identical.

func reuseStore(t *testing.T) (*VectorStore, func()) {
	t.Helper()
	f, err := os.CreateTemp("", "chunk_reuse_*.db")
	if err != nil {
		t.Fatal(err)
	}
	_ = f.Close()

	db, err := bolt.Open(f.Name(), 0600, &bolt.Options{Timeout: time.Second})
	if err != nil {
		_ = os.Remove(f.Name())
		t.Fatal(err)
	}
	vs := NewVectorStore(db)
	if err := vs.EnsureBucket(); err != nil {
		_ = db.Close()
		_ = os.Remove(f.Name())
		t.Fatal(err)
	}
	return vs, func() {
		_ = db.Close()
		_ = os.Remove(f.Name())
	}
}

func TestChunkVectorsByHashRoundTrip(t *testing.T) {
	vs, cleanup := reuseStore(t)
	defer cleanup()

	chunks := []ChunkEmbedding{
		{ChunkIndex: 0, Vector: []float32{1, 2, 3}, ChunkHash: ContentHash("first")},
		{ChunkIndex: 1, Vector: []float32{4, 5, 6}, ChunkHash: ContentHash("second")},
	}
	if err := vs.PutChunks("docs", "doc1", chunks, "model", ContentHash("whole doc")); err != nil {
		t.Fatal(err)
	}

	byHash := vs.ChunkVectorsByHash("docs", "doc1")
	if len(byHash) != 2 {
		t.Fatalf("got %d reusable chunks, want 2", len(byHash))
	}
	if v := byHash[ContentHash("second")]; len(v) != 3 || v[0] != 4 {
		t.Errorf("wrong vector for the second chunk: %v", v)
	}
}

// Keyed by hash, not by index: inserting a paragraph at the top shifts every
// index below it, and index-keyed reuse would miss exactly the common edit.
func TestReuseSurvivesAChunkShiftingPosition(t *testing.T) {
	vs, cleanup := reuseStore(t)
	defer cleanup()

	tail := ContentHash("the unchanged tail")
	if err := vs.PutChunks("docs", "doc1", []ChunkEmbedding{
		{ChunkIndex: 0, Vector: []float32{9, 9, 9}, ChunkHash: tail},
	}, "model", ContentHash("v1")); err != nil {
		t.Fatal(err)
	}

	byHash := vs.ChunkVectorsByHash("docs", "doc1")
	// The same text now sits at index 1 after an insertion above it.
	if v, ok := byHash[tail]; !ok || v[0] != 9 {
		t.Errorf("a chunk that moved position is no longer reusable: %v", byHash)
	}
}

// Records written before v2.12.0 carry no chunk hash. They must be skipped, not
// reused under an empty key — reusing the wrong vector is worse than embedding
// again.
func TestChunksWithoutAHashAreNotReusable(t *testing.T) {
	vs, cleanup := reuseStore(t)
	defer cleanup()

	if err := vs.PutChunks("docs", "doc1", []ChunkEmbedding{
		{ChunkIndex: 0, Vector: []float32{1, 2, 3}}, // no ChunkHash
	}, "model", ContentHash("whole")); err != nil {
		t.Fatal(err)
	}

	if got := vs.ChunkVectorsByHash("docs", "doc1"); len(got) != 0 {
		t.Errorf("a hashless chunk was offered for reuse: %v", got)
	}
}

// A quantized vector is lossy on read. Reusing one would silently replace a
// full-precision vector with a degraded copy.
func TestQuantizedChunksAreNotReusable(t *testing.T) {
	vs, cleanup := reuseStore(t)
	defer cleanup()

	if err := vs.PutChunksQuantized("docs", "doc1", []ChunkEmbedding{
		{ChunkIndex: 0, Vector: []float32{0.1, 0.2, 0.3}, ChunkHash: ContentHash("text")},
	}, "model", ContentHash("whole"), QuantInt8); err != nil {
		t.Fatal(err)
	}

	if got := vs.ChunkVectorsByHash("docs", "doc1"); len(got) != 0 {
		t.Errorf("a quantized chunk was offered for full-precision reuse: %v", got)
	}
}

func TestChunkVectorsByHashOnAnUnknownDocument(t *testing.T) {
	vs, cleanup := reuseStore(t)
	defer cleanup()

	if got := vs.ChunkVectorsByHash("docs", "never-stored"); len(got) != 0 {
		t.Errorf("an unknown document produced %v", got)
	}
}

// --- binary format compatibility ---
//
// The record format is hand-rolled with fixed offsets and no version byte in
// the float32 path. The chunk hash is appended past every field the original
// had; these pin that both directions still work, because getting this wrong
// corrupts a database in place.

func TestNewReaderHandlesARecordWithoutAChunkHash(t *testing.T) {
	// Exactly the v1 layout, byte for byte, without the trailing field.
	rec := &EmbeddingRecord{
		DocID: "doc1", Vector: []float32{1, 2}, Model: "m",
		Dimensions: 2, CreatedAt: 1234, ContentHash: "abc",
	}
	old := marshalWithoutChunkHash(rec)

	got, err := UnmarshalEmbeddingRecord(old)
	if err != nil {
		t.Fatalf("a pre-v2.12.0 record no longer reads: %v", err)
	}
	if got.DocID != "doc1" || got.ContentHash != "abc" || len(got.Vector) != 2 {
		t.Errorf("the record decoded wrongly: %+v", got)
	}
	if got.ChunkHash != "" {
		t.Errorf("an absent chunk hash decoded as %q", got.ChunkHash)
	}
}

func TestChunkHashSurvivesARoundTrip(t *testing.T) {
	rec := &EmbeddingRecord{
		DocID: "doc1", Vector: []float32{1, 2}, Model: "m",
		Dimensions: 2, CreatedAt: 1234, ContentHash: "abc", ChunkHash: "chunk-hash",
	}
	got, err := UnmarshalEmbeddingRecord(MarshalEmbeddingRecord(rec))
	if err != nil {
		t.Fatal(err)
	}
	if got.ChunkHash != "chunk-hash" {
		t.Errorf("chunkHash = %q, want chunk-hash", got.ChunkHash)
	}
	if got.ContentHash != "abc" || got.DocID != "doc1" {
		t.Errorf("appending a field disturbed the earlier ones: %+v", got)
	}
}

func TestQuantizedChunkHashSurvivesARoundTrip(t *testing.T) {
	rec := &EmbeddingRecord{
		DocID: "doc1", Vector: []float32{0.1, 0.2}, Model: "m",
		Dimensions: 2, CreatedAt: 1234, ContentHash: "abc", ChunkHash: "chunk-hash",
	}
	got, _, err := unmarshalEmbeddingRecordQuantized(marshalEmbeddingRecordQuantized(rec, QuantInt8))
	if err != nil {
		t.Fatal(err)
	}
	if got.ChunkHash != "chunk-hash" {
		t.Errorf("chunkHash = %q, want chunk-hash", got.ChunkHash)
	}
	if got.DocID != "doc1" || got.ContentHash != "abc" {
		t.Errorf("appending a field disturbed the earlier ones: %+v", got)
	}
}

// A truncated or corrupt trailing field must not take the whole record down —
// the vector is still perfectly good without a chunk hash.
func TestTruncatedChunkHashIsIgnored(t *testing.T) {
	rec := &EmbeddingRecord{
		DocID: "doc1", Vector: []float32{1, 2}, Model: "m",
		Dimensions: 2, CreatedAt: 1234, ContentHash: "abc", ChunkHash: "chunk-hash",
	}
	full := MarshalEmbeddingRecord(rec)

	for _, cut := range []int{1, 4, 8} {
		got, err := UnmarshalEmbeddingRecord(full[:len(full)-cut])
		if err != nil {
			t.Errorf("cutting %d trailing bytes broke the record: %v", cut, err)
			continue
		}
		if got.DocID != "doc1" || len(got.Vector) != 2 {
			t.Errorf("cutting %d bytes corrupted the record: %+v", cut, got)
		}
	}
}

// marshalWithoutChunkHash reproduces the pre-v2.12.0 encoder exactly.
func marshalWithoutChunkHash(rec *EmbeddingRecord) []byte {
	modelBytes := []byte(rec.Model)
	hashBytes := []byte(rec.ContentHash)
	docIDBytes := []byte(rec.DocID)

	size := 4 + len(modelBytes) + 4 + 4*len(rec.Vector) + 8 + 4 + len(hashBytes) + 4 + len(docIDBytes)
	buf := make([]byte, size)
	offset := 0

	binary.LittleEndian.PutUint32(buf[offset:], uint32(len(modelBytes))) // #nosec G115 -- model name length always small
	offset += 4
	copy(buf[offset:], modelBytes)
	offset += len(modelBytes)

	binary.LittleEndian.PutUint32(buf[offset:], uint32(rec.Dimensions)) // #nosec G115 -- dimensions always positive and bounded
	offset += 4
	for _, v := range rec.Vector {
		binary.LittleEndian.PutUint32(buf[offset:], math.Float32bits(v))
		offset += 4
	}

	binary.LittleEndian.PutUint64(buf[offset:], uint64(rec.CreatedAt)) // #nosec G115 -- timestamp always non-negative
	offset += 8

	binary.LittleEndian.PutUint32(buf[offset:], uint32(len(hashBytes))) // #nosec G115 -- hash length always small
	offset += 4
	copy(buf[offset:], hashBytes)
	offset += len(hashBytes)

	binary.LittleEndian.PutUint32(buf[offset:], uint32(len(docIDBytes))) // #nosec G115 -- docID length always small
	offset += 4
	copy(buf[offset:], docIDBytes)

	return buf
}
