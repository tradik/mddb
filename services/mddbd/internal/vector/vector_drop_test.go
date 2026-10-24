package vector

import (
	"path/filepath"
	"testing"

	bolt "go.etcd.io/bbolt"
)

// #331: dropping a collection empties it in every index and in the store,
// and leaves the other collections alone.

func TestDropCollectionEmptiesEveryIndex(t *testing.T) {
	indexes := map[string]VectorSearcher{
		"flat":      NewVectorIndex(),
		"hnsw":      NewHNSWIndex(8, 0, 32),
		"ivf":       NewIVFIndex(2, 5),
		"pq":        NewPQIndex(2, 4, 5),
		"opq":       NewOPQIndex(2, 4, 5, 2),
		"sq":        NewSQIndex(),
		"sq4":       NewSQ4Index(),
		"bq":        NewBQIndex(4),
		"quantized": NewQuantizedVectorIndex(func(string) QuantizationType { return QuantInt8 }),
	}
	for name, idx := range indexes {
		t.Run(name, func(t *testing.T) {
			for _, coll := range []string{"gone", "kept"} {
				for i, v := range [][]float32{{1, 0, 0, 1}, {0, 1, 1, 0}, {1, 1, 0, 0}} {
					if err := idx.Add(coll, string(rune('a'+i)), v); err != nil {
						t.Fatal(err)
					}
				}
			}
			idx.DropCollection("gone")
			if n := idx.CollectionSize("gone"); n != 0 {
				t.Errorf("dropped collection still holds %d vectors", n)
			}
			if n := idx.CollectionSize("kept"); n != 3 {
				t.Errorf("the other collection holds %d vectors, want 3", n)
			}
			if r := idx.Search("gone", []float32{1, 0, 0, 1}, 5, 0, nil); len(r) != 0 {
				t.Errorf("search in a dropped collection returned %d results", len(r))
			}
		})
	}
}

func TestVectorStoreDeleteCollection(t *testing.T) {
	db, err := bolt.Open(filepath.Join(t.TempDir(), "v.db"), 0600, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	store := NewVectorStore(db)
	if n, err := store.DeleteCollection("gone"); err != nil || n != 0 {
		t.Errorf("before the bucket exists: %d %v", n, err)
	}
	if err := store.EnsureBucket(); err != nil {
		t.Fatal(err)
	}
	chunks := []ChunkEmbedding{{ChunkIndex: 0, Vector: []float32{1, 0}}, {ChunkIndex: 1, Vector: []float32{0, 1}}}
	for _, coll := range []string{"gone", "gone2", "kept"} {
		if err := store.PutChunks(coll, "doc", chunks, "m", "h"); err != nil {
			t.Fatal(err)
		}
	}
	if n, err := store.DeleteCollection("gone"); err != nil || n != 2 {
		t.Errorf("removed %d (%v), want 2", n, err)
	}
	// "gone2" shares the prefix "gone" but is another collection.
	for _, coll := range []string{"gone2", "kept"} {
		if n, _ := store.DeleteCollection(coll); n != 2 {
			t.Errorf("%s lost vectors to the delete of gone: %d left", coll, n)
		}
	}
}
