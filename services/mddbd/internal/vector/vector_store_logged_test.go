package vector

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"mddb/internal/binlog"

	bolt "go.etcd.io/bbolt"
)

// GO-051 follow-up: every vector write records its entries inside its own
// transaction, so on a node whose log refuses them nothing commits.

// loggedStore is a store with its bucket and a log.
func loggedStore(t *testing.T) (*VectorStore, *binlog.Binlog, *bolt.DB) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "v.db")
	db, err := bolt.Open(path, 0o600, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	bl, err := binlog.NewBinlog(path, binlog.BinlogConfig{RetentionInterval: -1})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = bl.Close() })
	vs := NewVectorStore(db)
	if err := vs.EnsureBucket(); err != nil {
		t.Fatal(err)
	}
	vs.SetBinlog(bl)
	return vs, bl, db
}

// stored is the number of records in the vectors bucket.
func stored(t *testing.T, db *bolt.DB) int {
	t.Helper()
	n := 0
	if err := db.View(func(tx *bolt.Tx) error {
		n = tx.Bucket([]byte("vectors")).Stats().KeyN
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestVectorWritesAreLoggedInTheirTransaction(t *testing.T) {
	vs, bl, db := loggedStore(t)
	chunks := []ChunkEmbedding{{ChunkIndex: 0, Vector: []float32{1, 0}}, {ChunkIndex: 1, Vector: []float32{0, 1}}}
	ok := []func() error{
		func() error { return vs.Put("c", "a", []float32{1, 2}, "m", "h") },
		func() error { return vs.PutQuantized("c", "q", []float32{1, 2}, "m", "h", QuantInt8) },
		func() error { return vs.PutChunks("c", "b", chunks, "m", "h") },
		func() error { return vs.PutChunksQuantized("c", "d", chunks, "m", "h", QuantInt8) },
		func() error { return vs.PutSummary("c", "a", []float32{1, 2}, "m", "h") },
	}
	for i, write := range ok {
		if err := write(); err != nil {
			t.Fatalf("write %d: %v", i, err)
		}
	}
	if got, want := bl.CurrentLSN(), uint64(stored(t, db)); got != want { // #nosec G115 -- a document count is never negative
		t.Fatalf("logged %d entries for %d records", got, want)
	}

	gate := errors.New("not the leader")
	bl.SetWriteGate(func() error { return gate })
	before, records := bl.CurrentLSN(), stored(t, db)
	refused := map[string]func() error{
		"put":           func() error { return vs.Put("c", "x", []float32{1, 2}, "m", "h") },
		"put quantized": func() error { return vs.PutQuantized("c", "x", []float32{1, 2}, "m", "h", QuantInt8) },
		"put chunks":    func() error { return vs.PutChunks("c", "x", chunks, "m", "h") },
		"put chunks q":  func() error { return vs.PutChunksQuantized("c", "x", chunks, "m", "h", QuantInt8) },
		"summary":       func() error { return vs.PutSummary("c", "x", []float32{1, 2}, "m", "h") },
		"delete":        func() error { return vs.Delete("c", "b") },
		"delete sum":    func() error { return vs.DeleteSummary("c", "a") },
		"drop":          func() error { _, err := vs.DeleteCollection("c"); return err },
		"delete keys":   func() error { return vs.deleteKeys("c", [][]byte{buildChunkKey("c", "b", 1)}) },
	}
	for name, write := range refused {
		if err := write(); !errors.Is(err, gate) {
			t.Errorf("%s on a node that does not lead: %v", name, err)
		}
	}
	idx := NewVectorIndex()
	_ = idx.Add("c", "b#1", []float32{0, 1})
	vs.CleanStaleChunks("c", "b", 1, idx)
	if idx.CollectionSize("c") != 1 {
		t.Error("a refused clean-up dropped the chunk from the index")
	}
	bl.SetWriteGate(nil)
	if bl.CurrentLSN() != before || stored(t, db) != records || bl.Dirty() {
		t.Fatalf("a refused write moved the log (%d→%d) or the records (%d→%d)", before, bl.CurrentLSN(), records, stored(t, db))
	}
}

// A write bbolt refuses — a nested bucket where a record should be, a key
// too long — fails whole and logs nothing.
func TestVectorWritesBboltRefuses(t *testing.T) {
	vs, bl, db := loggedStore(t)
	if err := db.Update(func(tx *bolt.Tx) error {
		_, err := tx.Bucket([]byte("vectors")).CreateBucket(buildChunkKey("c", "n", 3))
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := vs.Delete("c", "n"); err == nil {
		t.Error("deleted a nested bucket as a record")
	}
	vs.CleanStaleChunks("c", "n", 1, nil) // the stale "record" is the bucket: nothing done
	long := strings.Repeat("k", bolt.MaxKeySize)
	if err := vs.PutChunks("c", long, []ChunkEmbedding{{Vector: []float32{1}}}, "m", "h"); err == nil {
		t.Error("stored a key longer than bbolt takes")
	}
	if bl.CurrentLSN() != 0 {
		t.Fatalf("logged %d entries for writes that failed", bl.CurrentLSN())
	}
}

// Without a vectors bucket the deletes have nothing to do.
func TestVectorDeleteKeysWithoutTheBucket(t *testing.T) {
	db, err := bolt.Open(filepath.Join(t.TempDir(), "empty.db"), 0o600, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if err := NewVectorStore(db).deleteKeys("c", [][]byte{[]byte("k")}); err != nil {
		t.Fatal(err)
	}
}
