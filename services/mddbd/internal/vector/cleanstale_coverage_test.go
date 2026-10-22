package vector

import (
	"os"
	"strconv"
	"testing"

	bolt "go.etcd.io/bbolt"
)

// TestVectorStoreCleanStaleChunks covers CleanStaleChunks: the stale-chunk
// delete loop (idx >= currentChunkCount), the index.Remove fan-out, and the
// currentChunkCount>1 old non-chunked-key cleanup.
func TestVectorStoreCleanStaleChunks(t *testing.T) {
	f, err := os.CreateTemp("", "vec_clean_*.db")
	if err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	defer func() { _ = os.Remove(f.Name()) }()
	db, err := bolt.Open(f.Name(), 0600, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	store := NewVectorStore(db)
	if err := store.EnsureBucket(); err != nil {
		t.Fatal(err)
	}
	idx := NewVectorIndex()

	chunks := []ChunkEmbedding{
		{ChunkIndex: 0, Vector: []float32{1, 0}},
		{ChunkIndex: 1, Vector: []float32{0, 1}},
		{ChunkIndex: 2, Vector: []float32{1, 1}},
	}
	if err := store.PutChunks("c", "doc1", chunks, "m", "h"); err != nil {
		t.Fatal(err)
	}
	for _, ch := range chunks {
		if err := idx.Add("c", "doc1#"+strconv.Itoa(ch.ChunkIndex), ch.Vector); err != nil {
			t.Fatal(err)
		}
	}

	// The doc now has only 1 chunk -> chunks #1 and #2 are stale and removed.
	store.CleanStaleChunks("c", "doc1", 1, idx)
	if idx.CollectionSize("c") != 1 {
		t.Errorf("expected 1 live chunk after cleanup, got %d", idx.CollectionSize("c"))
	}

	// Seed an old non-chunked key, then clean with currentChunkCount>1 to hit
	// the legacy-key removal branch.
	if err := db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket(store.bucketName).Put(buildVecKey("c", "doc2"), []byte("legacy"))
	}); err != nil {
		t.Fatal(err)
	}
	store.CleanStaleChunks("c", "doc2", 2, idx)
	_ = store.db.View(func(tx *bolt.Tx) error {
		if tx.Bucket(store.bucketName).Get(buildVecKey("c", "doc2")) != nil {
			t.Error("legacy non-chunked key should be removed when chunkCount>1")
		}
		return nil
	})
}

// writeCommits counts the write transactions committed since the last call:
// bbolt advances the meta txid once per commit, and nothing else does.
func writeCommits(t *testing.T, db *bolt.DB) func() int {
	t.Helper()
	txid := func() int {
		var id int
		if err := db.View(func(tx *bolt.Tx) error { id = tx.ID(); return nil }); err != nil {
			t.Fatal(err)
		}
		return id
	}
	last := txid()
	return func() int {
		now := txid()
		n := now - last
		last = now
		return n
	}
}

// #306. The embedding worker calls CleanStaleChunks after every document it
// embeds. When the document did not shrink there is nothing to delete, and it
// must not commit: each commit is an fsync, and on a spinning disk two empty
// commits per document were most of what a bulk load spent its time on.
func TestCleanStaleChunksCommitsOnlyWhenSomethingIsStale(t *testing.T) {
	vs := newTestVectorStore(t)
	idx := NewVectorIndex()
	chunks := []ChunkEmbedding{
		{ChunkIndex: 0, Vector: []float32{1, 0}},
		{ChunkIndex: 1, Vector: []float32{0, 1}},
	}
	if err := vs.PutChunks("c", "doc", chunks, "m", "h"); err != nil {
		t.Fatal(err)
	}
	commits := writeCommits(t, vs.db)

	vs.CleanStaleChunks("c", "doc", len(chunks), idx)
	if n := commits(); n != 0 {
		t.Errorf("nothing was stale, yet CleanStaleChunks committed %d transactions", n)
	}

	// A stale chunk and the legacy non-chunked key go in one commit.
	if err := vs.db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket(vs.bucketName).Put(buildVecKey("c", "doc"), []byte("legacy"))
	}); err != nil {
		t.Fatal(err)
	}
	if err := vs.PutChunks("c", "doc", append(chunks, ChunkEmbedding{ChunkIndex: 2, Vector: []float32{1, 1}}), "m", "h"); err != nil {
		t.Fatal(err)
	}
	_ = commits()
	vs.CleanStaleChunks("c", "doc", len(chunks), idx)
	if n := commits(); n != 1 {
		t.Errorf("removing a stale chunk and the legacy key took %d commits, want 1", n)
	}
	_ = vs.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(vs.bucketName)
		if b.Get(buildChunkKey("c", "doc", 2)) != nil {
			t.Error("stale chunk #2 survived the cleanup")
		}
		if b.Get(buildVecKey("c", "doc")) != nil {
			t.Error("legacy non-chunked key survived the cleanup")
		}
		if b.Get(buildChunkKey("c", "doc", 1)) == nil {
			t.Error("live chunk #1 was removed")
		}
		return nil
	})
}
