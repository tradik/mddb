package fts

import (
	"errors"
	"path/filepath"
	"slices"
	"testing"

	"mddb/internal/binlog"

	bolt "go.etcd.io/bbolt"
)

// Synonyms and stop words are logged writes (GO-051): recorded inside their
// transaction, refused with it when the node may not write.

func listsDB(t *testing.T) (*bolt.DB, *binlog.Binlog) {
	t.Helper()
	dir := t.TempDir()
	db, err := bolt.Open(filepath.Join(dir, "l.db"), 0o600, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	bl, err := binlog.NewBinlog(filepath.Join(dir, "l.db"), binlog.BinlogConfig{RetentionInterval: -1})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = bl.Close() })
	return db, bl
}

var errGate = errors.New("not the leader")

func TestSynonymWritesAreLoggedAndGated(t *testing.T) {
	db, bl := listsDB(t)
	sm := NewSynonymManager(db)
	sm.SetBinlog(bl)
	if err := sm.LoadAll(); err != nil { // no bucket yet
		t.Fatal(err)
	}
	if err := sm.EnsureBucket(); err != nil {
		t.Fatal(err)
	}
	if err := sm.LoadDefaults("c"); err != nil {
		t.Fatal(err)
	}
	if bl.CurrentLSN() == 0 {
		t.Fatal("nothing logged")
	}
	// Foreign and malformed entries are skipped on load.
	if err := db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketSynonyms)
		if err := b.Put([]byte("other|c|x"), []byte("[]")); err != nil {
			return err
		}
		return b.Put([]byte("syn|c|bad"), []byte("{"))
	}); err != nil {
		t.Fatal(err)
	}
	if err := sm.Reload(db); err != nil {
		t.Fatal(err)
	}
	if err := sm.Set("c", "car", []string{"auto", "auto", "car"}); err != nil {
		t.Fatal(err)
	}
	got := sm.Expand("c", []string{"auto", "auto", "car"})
	if !slices.Contains(got, "car") || !slices.Contains(got, "auto") {
		t.Fatalf("expand %v", got)
	}
	if sm.Get("nowhere", "car") != nil {
		t.Fatal("synonyms of a collection without any")
	}

	bl.SetWriteGate(func() error { return errGate })
	if err := sm.Set("c", "a", []string{"b"}); !errors.Is(err, errGate) {
		t.Fatalf("gated set: %v", err)
	}
	if err := sm.Delete("c", "car"); !errors.Is(err, errGate) {
		t.Fatalf("gated delete: %v", err)
	}
	if err := sm.LoadDefaults("d"); !errors.Is(err, errGate) {
		t.Fatalf("gated defaults: %v", err)
	}
	bl.SetWriteGate(nil)
	if err := sm.Delete("c", "car"); err != nil {
		t.Fatal(err)
	}
}

func TestStopWordWritesAreLoggedAndGated(t *testing.T) {
	db, bl := listsDB(t)
	swm := NewStopWordManager(db)
	swm.SetBinlog(bl)
	if err := swm.LoadAll(); err != nil {
		t.Fatal(err)
	}
	if err := swm.EnsureBucket(); err != nil {
		t.Fatal(err)
	}
	if err := swm.Add("", []string{"x"}); err == nil {
		t.Fatal("no collection")
	}
	if err := swm.Add("c", []string{" ", ""}); err == nil {
		t.Fatal("no words")
	}
	if err := swm.Add("c", []string{"Foo", "bar"}); err != nil {
		t.Fatal(err)
	}
	if !swm.IsStopWord("c", "the") || !swm.IsStopWord("c", "foo") || swm.IsStopWord("c", "baz") || swm.IsStopWord("other", "foo") {
		t.Fatal("IsStopWord")
	}
	if err := db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket(bucketStopWords).Put([]byte("zz|c|w"), []byte("1"))
	}); err != nil {
		t.Fatal(err)
	}
	if err := swm.Reload(db); err != nil {
		t.Fatal(err)
	}
	if err := swm.Delete("c", ""); err == nil {
		t.Fatal("no word")
	}
	if err := swm.Delete("c", "the"); err == nil {
		t.Fatal("deleted a default stop word")
	}

	bl.SetWriteGate(func() error { return errGate })
	if err := swm.Add("c", []string{"x"}); !errors.Is(err, errGate) {
		t.Fatalf("gated add: %v", err)
	}
	if err := swm.Delete("c", "foo"); !errors.Is(err, errGate) {
		t.Fatalf("gated delete: %v", err)
	}
	bl.SetWriteGate(nil)
	if err := swm.Delete("c", "foo"); err != nil || swm.IsStopWord("c", "foo") {
		t.Fatalf("delete: %v", err)
	}
}
