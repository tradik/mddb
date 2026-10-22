package fts

import (
	"testing"

	bolt "go.etcd.io/bbolt"
)

// A database without the FTS buckets — a fresh file, or one opened before
// EnsureBuckets — must resolve no field rather than fail.
func TestFieldSourceForWithoutBuckets(t *testing.T) {
	db, err := bolt.Open(t.TempDir()+"/empty.db", 0600, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	_ = db.View(func(tx *bolt.Tx) error {
		for _, field := range []string{contentField, "meta.title"} {
			if _, ok := fieldSourceFor(tx, "c", field); ok {
				t.Errorf("%s resolved without its buckets", field)
			}
		}
		if got := fieldSources(tx, "c", map[string]float64{"content": 1, "meta.title": 2, "meta.tags": 0}); len(got) != 0 {
			t.Errorf("fieldSources = %v, want none", got)
		}
		return nil
	})
}
