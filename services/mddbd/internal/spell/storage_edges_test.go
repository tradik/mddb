package spell

import (
	"testing"
	"time"

	bolt "go.etcd.io/bbolt"
)

// TestSpellWithoutBucket: before EnsureBucket the dictionary operations are
// no-ops, not errors.
func TestSpellWithoutBucket(t *testing.T) {
	db, cleanup := newTestDB(t)
	defer cleanup()
	sm := NewSpellManager(db)

	if err := sm.AddWord("col", "en", "word", 1); err != nil {
		t.Errorf("AddWord: %v", err)
	}
	if err := sm.RemoveWord("col", "en", "word"); err != nil {
		t.Errorf("RemoveWord: %v", err)
	}
	if words, err := sm.ListWords("col", "en"); err != nil || len(words) != 0 {
		t.Errorf("ListWords = %v, %v", words, err)
	}
	sm.LoadAll()
	waitReady(t, sm)
}

// TestSpellStorageErrors: a closed database surfaces as an error.
func TestSpellStorageErrors(t *testing.T) {
	db, cleanup := newTestDB(t)
	sm := NewSpellManager(db)
	if err := sm.EnsureBucket(); err != nil {
		t.Fatal(err)
	}
	cleanup()
	if err := sm.AddWord("", "en", "word", 1); err == nil {
		t.Error("AddWord on a closed database: want an error")
	}
	if err := sm.RemoveWord("", "en", "word"); err == nil {
		t.Error("RemoveWord on a closed database: want an error")
	}
	if err := sm.RemoveWord("", "en", "  "); err != nil {
		t.Errorf("RemoveWord of an empty word is a no-op, got %v", err)
	}
}

// TestSpellLoadAllSkipsMalformedKeys: keys LoadAll cannot parse are skipped,
// and a value that is not 4 bytes trains with frequency 0.
func TestSpellLoadAllSkipsMalformedKeys(t *testing.T) {
	db, cleanup := newTestDB(t)
	defer cleanup()
	sm := NewSpellManager(db)
	if err := sm.EnsureBucket(); err != nil {
		t.Fatal(err)
	}
	err := db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketSpellDicts)
		for k, v := range map[string][]byte{
			"nopipe":          {1, 0, 0, 0},
			"col|onlytwo|bad": {1, 0, 0, 0},
			"en|shortvalue":   {1},
			"col|c|en|termx":  {2, 0, 0, 0},
		} {
			if err := b.Put([]byte(k), v); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sm.LoadAll()
	waitReady(t, sm)
	if _, ok := sm.getOrCreate("en").words["shortvalue"]; !ok {
		t.Error("a word with a short value should still be loaded")
	}
	if _, ok := sm.getOrCreate("col:c:en").words["termx"]; !ok {
		t.Error("collection word not loaded")
	}
}

func waitReady(t *testing.T, sm *SpellManager) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !sm.Ready() {
		if time.Now().After(deadline) {
			t.Fatal("LoadAll did not finish")
		}
		time.Sleep(5 * time.Millisecond)
	}
}
