package vector

import (
	"testing"
)

// TEST-003. The embedding record is a hand-rolled binary format with fixed
// offsets and length prefixes read straight from the bytes. Every one of those
// prefixes is an opportunity to index past the end of a slice.
//
// It also travels through the replication binlog, so a follower decodes bytes a
// leader wrote — and a corrupt or truncated record must produce an error, never
// a panic that takes the follower down.
//
// RAG-003 appended a chunk-hash field to both encodings. These pin that the
// decoders stay total.

func FuzzUnmarshalEmbeddingRecord(f *testing.F) {
	// Seeds: valid records of both shapes, so the fuzzer starts from
	// structurally plausible bytes rather than discovering the header from
	// scratch.
	f.Add(MarshalEmbeddingRecord(&EmbeddingRecord{
		DocID: "doc1", Model: "m", Dimensions: 2,
		Vector: []float32{1, 2}, CreatedAt: 1, ContentHash: "abc",
	}))
	f.Add(MarshalEmbeddingRecord(&EmbeddingRecord{
		DocID: "doc1", Model: "model-with-a-longer-name", Dimensions: 4,
		Vector: []float32{1, 2, 3, 4}, CreatedAt: 1 << 40,
		ContentHash: "abc", ChunkHash: "chunk",
	}))
	f.Add(MarshalEmbeddingRecord(&EmbeddingRecord{}))
	f.Add([]byte{})
	f.Add([]byte{0, 0, 0, 0})
	// A length prefix claiming far more than the record holds — the classic
	// way a hand-rolled decoder walks off the end.
	f.Add([]byte{0xff, 0xff, 0xff, 0xff, 1, 2, 3})

	f.Fuzz(func(t *testing.T, data []byte) {
		rec, err := UnmarshalEmbeddingRecord(data)
		if err != nil {
			return
		}
		if rec == nil {
			t.Fatal("decoder returned neither a record nor an error")
		}
		// A record that decodes must be internally consistent: Dimensions is
		// what readers allocate against.
		if rec.Dimensions != len(rec.Vector) {
			t.Fatalf("record claims %d dimensions but carries %d values", rec.Dimensions, len(rec.Vector))
		}
	})
}

func FuzzUnmarshalEmbeddingRecordQuantized(f *testing.F) {
	f.Add(marshalEmbeddingRecordQuantized(&EmbeddingRecord{
		DocID: "doc1", Model: "m", Dimensions: 2,
		Vector: []float32{0.1, 0.2}, CreatedAt: 1, ContentHash: "abc",
	}, QuantInt8))
	f.Add(marshalEmbeddingRecordQuantized(&EmbeddingRecord{
		DocID: "doc1", Model: "m", Dimensions: 4,
		Vector: []float32{0.1, 0.2, 0.3, 0.4}, CreatedAt: 1,
		ContentHash: "abc", ChunkHash: "chunk",
	}, QuantInt4))
	f.Add([]byte{2})
	f.Add([]byte{2, 1, 0xff, 0xff, 0xff, 0xff})

	f.Fuzz(func(t *testing.T, data []byte) {
		rec, qv, err := unmarshalEmbeddingRecordQuantized(data)
		if err != nil {
			return
		}
		if rec == nil {
			t.Fatal("decoder returned neither a record nor an error")
		}
		if qv != nil && qv.Dims < 0 {
			t.Fatalf("quantized vector reports a negative dimension count: %d", qv.Dims)
		}
	})
}

// The format's own compatibility promise: bytes written by one encoder are read
// back identically. Fuzzing the round trip catches a field whose length is
// computed one way and written another — the failure mode a hand-written size
// calculation invites.
func FuzzEmbeddingRecordRoundTrip(f *testing.F) {
	f.Add("doc1", "model", "hash", "chunk", 2, int64(12345))
	f.Add("", "", "", "", 0, int64(0))
	f.Add("a", "b", "c", "", 1, int64(-1))

	f.Fuzz(func(t *testing.T, docID, model, contentHash, chunkHash string, dims int, created int64) {
		// Bound the vector: the property is about encoding, not about
		// allocating gigabytes.
		if dims < 0 || dims > 512 {
			return
		}
		vector := make([]float32, dims)
		for i := range vector {
			vector[i] = float32(i) * 0.5
		}

		original := &EmbeddingRecord{
			DocID: docID, Model: model, Dimensions: dims,
			Vector: vector, CreatedAt: created,
			ContentHash: contentHash, ChunkHash: chunkHash,
		}

		got, err := UnmarshalEmbeddingRecord(MarshalEmbeddingRecord(original))
		if err != nil {
			t.Fatalf("a record this encoder produced does not decode: %v", err)
		}
		if got.DocID != docID || got.Model != model || got.ContentHash != contentHash {
			t.Fatalf("string fields changed:\n wrote %q/%q/%q\n read  %q/%q/%q",
				docID, model, contentHash, got.DocID, got.Model, got.ContentHash)
		}
		if got.ChunkHash != chunkHash {
			t.Fatalf("chunk hash changed: wrote %q, read %q", chunkHash, got.ChunkHash)
		}
		if got.CreatedAt != created {
			t.Fatalf("timestamp changed: wrote %d, read %d", created, got.CreatedAt)
		}
		if len(got.Vector) != len(vector) {
			t.Fatalf("vector length changed: wrote %d, read %d", len(vector), len(got.Vector))
		}
		for i := range vector {
			if got.Vector[i] != vector[i] {
				t.Fatalf("vector value %d changed: wrote %v, read %v", i, vector[i], got.Vector[i])
			}
		}
	})
}
