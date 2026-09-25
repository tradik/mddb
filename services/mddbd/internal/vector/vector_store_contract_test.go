package vector

import (
	"path/filepath"
	"testing"

	"mddb/internal/binlog"

	bolt "go.etcd.io/bbolt"
)

// Three contracts of the vector store that had no test in this package:
// every write reaches the binlog when one is attached, a store without its
// bucket refuses writes rather than pretending, and GetVectors — which
// disk-only search is built on — returns what was stored and skips what was
// not.

func openStore(t *testing.T, withBucket bool) (*VectorStore, *bolt.DB) {
	t.Helper()
	db, err := bolt.Open(filepath.Join(t.TempDir(), "v.db"), 0o600, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	vs := NewVectorStore(db)
	if withBucket {
		if err := vs.EnsureBucket(); err != nil {
			t.Fatal(err)
		}
	}
	return vs, db
}

// A follower applies the leader's binlog; a write that skips it is a write the
// replicas never see, and they diverge without anyone being told. Each write
// path appends on its own, so each is exercised.
func TestEveryVectorWriteReachesTheBinlog(t *testing.T) {
	vs, _ := openStore(t, true)
	bl, err := binlog.NewBinlog(filepath.Join(t.TempDir(), "v.db"), binlog.BinlogConfig{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = bl.Close() })
	vs.SetBinlog(bl)

	chunks := []ChunkEmbedding{{ChunkIndex: 0, Vector: []float32{1, 0, 0}}, {ChunkIndex: 1, Vector: []float32{0, 1, 0}}}
	writes := []struct {
		name string
		do   func() error
	}{
		{"Put", func() error { return vs.Put("c", "a", []float32{1, 2, 3}, "m", "h") }},
		{"PutQuantized", func() error { return vs.PutQuantized("c", "b", []float32{1, 2, 3}, "m", "h", QuantInt8) }},
		{"PutChunks", func() error { return vs.PutChunks("c", "d", chunks, "m", "h") }},
		{"PutChunksQuantized", func() error { return vs.PutChunksQuantized("c", "e", chunks, "m", "h", QuantInt8) }},
		{"Delete", func() error { return vs.Delete("c", "a") }},
		{"CleanStaleChunks", func() error { vs.CleanStaleChunks("c", "d", 1, nil); return nil }},
	}

	for _, w := range writes {
		before := bl.CurrentLSN()
		if err := w.do(); err != nil {
			t.Fatalf("%s: %v", w.name, err)
		}
		if bl.CurrentLSN() == before {
			t.Errorf("%s wrote to the store without appending to the binlog", w.name)
		}
	}

	entries, err := bl.ReadFrom(0)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.BucketName != "vectors" {
			t.Errorf("an entry names bucket %q, want vectors", e.BucketName)
		}
	}
}

// Writing before the bucket exists is a programming error somewhere upstream,
// and it has to surface as one — a nil error here would be a vector that was
// never stored, reported as stored.
func TestAStoreWithoutItsBucketRefusesWrites(t *testing.T) {
	vs, _ := openStore(t, false)
	chunks := []ChunkEmbedding{{ChunkIndex: 0, Vector: []float32{1, 0, 0}}}

	for name, err := range map[string]error{
		"Put":                vs.Put("c", "a", []float32{1}, "m", "h"),
		"PutQuantized":       vs.PutQuantized("c", "a", []float32{1}, "m", "h", QuantInt8),
		"PutChunks":          vs.PutChunks("c", "a", chunks, "m", "h"),
		"PutChunksQuantized": vs.PutChunksQuantized("c", "a", chunks, "m", "h", QuantInt8),
	} {
		if err == nil {
			t.Errorf("%s reported success with no bucket to write to", name)
		}
	}

	// Reads have nothing to find and must say so without failing.
	if coll, err := vs.LoadCollection("c"); err != nil || len(coll) != 0 {
		t.Errorf("LoadCollection = %v, %v; want empty and no error", coll, err)
	}
	if counts, err := vs.CountByCollection(); err != nil || len(counts) != 0 {
		t.Errorf("CountByCollection = %v, %v; want empty and no error", counts, err)
	}
	if got := vs.GetVectors("c", []string{"a"}); len(got) != 0 {
		t.Errorf("GetVectors = %v, want nothing", got)
	}
}

// Disk-only search asks for the vectors of its candidates by id. Plain and
// quantized records both have to come back, and an id with nothing stored is
// simply absent rather than an error or a zero vector.
func TestGetVectorsReturnsWhatWasStoredAndOnlyThat(t *testing.T) {
	vs, _ := openStore(t, true)
	if err := vs.Put("c", "plain", []float32{1, 0, 0}, "m", "h"); err != nil {
		t.Fatal(err)
	}
	if err := vs.PutQuantized("c", "quant", []float32{0, 1, 0}, "m", "h", QuantInt8); err != nil {
		t.Fatal(err)
	}

	got := vs.GetVectors("c", []string{"plain", "quant", "absent"})

	if len(got) != 2 {
		t.Fatalf("GetVectors returned %d vectors, want 2: %v", len(got), got)
	}
	if v := got["plain"]; len(v) != 3 || v[0] != 1 {
		t.Errorf("plain = %v, want the stored vector", v)
	}
	if v := got["quant"]; len(v) != 3 || v[1] < 0.9 {
		t.Errorf("quant = %v, want the stored vector back through int8 (within quantization error)", v)
	}
	if _, ok := got["absent"]; ok {
		t.Error("an id with nothing stored came back with a vector")
	}
}
