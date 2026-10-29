package migrate

import (
	"testing"

	bolt "go.etcd.io/bbolt"

	"mddb/internal/storage"
)

// FuzzByKeyUpgrade is the upgrade path of TEST-003 for DOC-017: an index
// written by 2.18.8 — two spellings of a key, stored as written — must come
// out of the migration with every entry in normal form, one entry per
// identifier, and each spelling still leading to its document.
func FuzzByKeyUpgrade(f *testing.F) {
	f.Add("README.md", "readme.md", "en")
	f.Add("Guide/Intro.MD", "guide/intro.md", "EN")
	f.Add("a|B", "A|b", "pl")
	f.Add("Zażółć.MD", "zażółć.md", "PL")
	f.Add("same", "same", "en")
	f.Add("", "X", "")

	f.Fuzz(func(t *testing.T, a, b, lang string) {
		id := func(key string) string { return "c|" + storage.NormalizeKey(key) + "|" + storage.NormalizeKey(lang) }
		raw := func(key string) string { return "bykey|c|" + key + "|" + lang }

		db, _ := openTemp(t)
		put(t, db, "docs", string(storage.DocKey("c", id(a))), "{}")
		put(t, db, "bykey", raw(a), id(a), raw(b), id(b))

		if err := Run(db); err != nil {
			t.Fatal(err)
		}

		entries := dump(t, db, "bykey")
		for k := range entries {
			if _, changed := storage.NormalizeByKeyEntry([]byte(k)); changed {
				t.Errorf("entry %q is not in normal form", k)
			}
		}
		want := 1
		if id(a) != id(b) {
			want = 2
		}
		if len(entries) != want {
			t.Errorf("%d entries for %d identifiers: %v", len(entries), want, entries)
		}
		err := db.View(func(tx *bolt.Tx) error {
			index := tx.Bucket([]byte("bykey"))
			for _, key := range []string{a, b} {
				if got := string(index.Get(storage.ByKeyKey("c", key, lang))); got != id(key) {
					t.Errorf("%q leads to %q, want %q", key, got, id(key))
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	})
}
