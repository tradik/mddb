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
