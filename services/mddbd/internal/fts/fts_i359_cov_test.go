package fts

import (
	"errors"
	"path/filepath"
	"testing"

	"mddb/internal/binlog"
	"mddb/internal/boltx"

	bolt "go.etcd.io/bbolt"
)

// Coverage of fts.go (#359): the query views, the router and reload
// switches, and the error paths of indexing and removal. A key that is a
// nested bucket makes bbolt refuse to write or delete it, which is how a
// write fails inside a writable transaction; a read-only transaction makes
// every write fail.

// i359Index is an index with every FTS bucket on a fresh database.
func i359Index(t *testing.T) *FTSIndex {
	t.Helper()
	db, err := bolt.Open(filepath.Join(t.TempDir(), "i359.db"), 0o600, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	f := NewFTSIndex(db)
	if err := f.EnsureBuckets(); err != nil {
		t.Fatal(err)
	}
	return f
}

// i359Nest makes key a nested bucket inside bucket, so bbolt refuses to put
// or delete it as a value.
func i359Nest(t *testing.T, f *FTSIndex, bucket, key []byte) {
	t.Helper()
	if err := f.db.Update(func(tx *bolt.Tx) error {
		_, err := tx.Bucket(bucket).CreateBucket(key)
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

// i359Put stores one raw value.
func i359Put(t *testing.T, f *FTSIndex, bucket, key, value []byte) {
	t.Helper()
	if err := f.db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket(bucket).Put(key, value)
	}); err != nil {
		t.Fatal(err)
	}
}

var errI359Put = errors.New("put refused")

func i359FailPut(*bolt.Bucket, []byte, []byte) error { return errI359Put }

func i359NoPut(*bolt.Bucket, []byte, []byte) error { return nil }

// A query view turns stemming and synonyms off for one request only, and
// costs nothing when neither is turned off.
func TestI359QueryView(t *testing.T) {
	var nilIndex *FTSIndex
	if nilIndex.QueryView(true, true) != nil {
		t.Fatal("a nil index has a nil view")
	}
	f := NewFTSIndex(nil)
	f.SetStemmer(NewPorterStemmer())
	f.SetSynonymManager(&SynonymManager{})
	if f.QueryView(false, false) != f {
		t.Fatal("a view with nothing turned off is the index itself")
	}
	v := f.QueryView(true, true)
	if v == f || v.Stemmer() != nil || !v.noStem || v.SynonymManager() != nil {
		t.Fatalf("view kept stemming or synonyms: %+v", v)
	}
	if f.Stemmer() == nil || f.SynonymManager() == nil {
		t.Fatal("the view changed the index")
	}
	if v.router != f.router {
		t.Fatal("a view shares the router of its index")
	}
}

// With a registry, a no-stem view keeps the language's stop words and drops
// its stemmer.
func TestI359NoStemViewOfALanguage(t *testing.T) {
	f := NewFTSIndex(nil)
	reg := NewLangRegistry("en")
	RegisterDefaultLanguages(reg)
	f.SetLangRegistry(reg)
	terms := f.QueryView(true, false).TokenizeLang("the running", "en")
	if _, ok := terms["running"]; !ok || len(terms) != 1 {
		t.Fatalf("no-stem view tokenised as %v, want only running", terms)
	}
}

// Without synonyms, a language query is its terms.
func TestI359TokenizeQueryLangWithoutSynonyms(t *testing.T) {
	f := NewFTSIndex(nil)
	if terms := f.TokenizeQueryLang("c", "hello world", ""); terms["hello"] != 1 || terms["world"] != 1 {
		t.Fatalf("terms = %v", terms)
	}
}

// SetRouter sends reads to the router's file; Reload drops PMI data built
// from the old file and keeps none when there was none.
func TestI359SetRouterAndReload(t *testing.T) {
	f := i359Index(t)
	if err := f.Index("c", "d", "hello"); err != nil {
		t.Fatal(err)
	}
	empty := i359Index(t)
	f.SetRouter(boltx.Single{DB: empty.db})
	if res, err := f.Search("c", "hello", 10); err != nil || len(res) != 0 {
		t.Fatalf("search through the router = %v %v, want nothing", res, err)
	}

	f.Reload(empty.db)
	if f.pmiData != nil {
		t.Fatal("reload made PMI data")
	}
	f.pmiData = NewPMIData()
	old := f.pmiData
	f.Reload(empty.db)
	if f.pmiData == nil || f.pmiData == old {
		t.Fatal("reload kept the old PMI data")
	}
}

// Text with no terms indexes nothing and opens no transaction.
func TestI359EmptyTextIndexesNothing(t *testing.T) {
	f := i359Index(t)
	if err := f.IndexWithLang("c", "d", "a", "en"); err != nil {
		t.Fatal(err)
	}
	if err := f.IndexFieldsWithLang("c", "d", map[string]string{"title": "a"}, "en"); err != nil {
		t.Fatal(err)
	}
	if err := f.IndexFields("c", "d", map[string]string{"title": "a"}); err != nil {
		t.Fatal(err)
	}
}

// A posting or reverse entry bbolt refuses fails the whole index write.
func TestI359IndexWriteErrors(t *testing.T) {
	for name, index := range map[string]func(*FTSIndex) error{
		"Index":         func(f *FTSIndex) error { return f.Index("c", "d", "hello") },
		"IndexWithLang": func(f *FTSIndex) error { return f.IndexWithLang("c", "d", "hello", "") },
	} {
		t.Run(name+"/posting", func(t *testing.T) {
			f := i359Index(t)
			i359Nest(t, f, bucketFTS, ftsKey("c", "hello", "d"))
			if err := index(f); err == nil {
				t.Fatal("a refused posting was not reported")
			}
		})
		t.Run(name+"/reverse", func(t *testing.T) {
			f := i359Index(t)
			i359Nest(t, f, bucketFTSRev, ftsRevKey("c", "d"))
			if err := index(f); err == nil {
				t.Fatal("a refused reverse entry was not reported")
			}
		})
	}
}

// The shared bulk body reports a refused posting.
func TestI359IndexTermsInTxPutError(t *testing.T) {
	f := i359Index(t)
	var bo binlog.BinlogOps
	err := f.db.Update(func(tx *bolt.Tx) error {
		return f.indexTermsInTx(tx, &bo, i359FailPut, "c", "d", map[string]int{"hello": 1})
	})
	if !errors.Is(err, errI359Put) {
		t.Fatalf("err = %v, want the put error", err)
	}
}

// The per-field body: no field buckets is nothing to do; a refused
// posting, removal, length, statistic or reverse entry is an error.
func TestI359IndexFieldTokensInTx(t *testing.T) {
	tokens := map[string]map[string]int{"title": {"hello": 1}}

	t.Run("no buckets", func(t *testing.T) {
		f := covLBareIndex(t)
		if err := f.db.Update(func(tx *bolt.Tx) error {
			return f.indexFieldTokensInTx(tx, putNow, "c", "d", tokens)
		}); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("posting", func(t *testing.T) {
		f := i359Index(t)
		if err := f.db.Update(func(tx *bolt.Tx) error {
			return f.indexFieldTokensInTx(tx, i359FailPut, "c", "d", tokens)
		}); !errors.Is(err, errI359Put) {
			t.Fatalf("err = %v, want the put error", err)
		}
	})
	t.Run("removal", func(t *testing.T) {
		f := i359Index(t)
		i359Put(t, f, bucketFTSFRev, ftsfRevKey("c", "d"), []byte(`[]`))
		if err := f.db.View(func(tx *bolt.Tx) error {
			return f.indexFieldTokensInTx(tx, i359NoPut, "c", "d", tokens)
		}); err == nil {
			t.Fatal("a refused removal was not reported")
		}
	})
	t.Run("length", func(t *testing.T) {
		f := i359Index(t)
		if err := f.db.View(func(tx *bolt.Tx) error {
			return f.indexFieldTokensInTx(tx, i359NoPut, "c", "d", tokens)
		}); err == nil {
			t.Fatal("a refused length was not reported")
		}
	})
	t.Run("statistic", func(t *testing.T) {
		f := i359Index(t)
		i359Nest(t, f, bucketFTSFStat, ftsfStatKey("c", "title"))
		if err := f.db.Update(func(tx *bolt.Tx) error {
			return f.indexFieldTokensInTx(tx, putNow, "c", "d", tokens)
		}); err == nil {
			t.Fatal("a refused statistic was not reported")
		}
	})
}

// IndexFields sorts its reverse entries by field, and reports a refused
// posting, length or statistic.
func TestI359IndexFields(t *testing.T) {
	fields := map[string]string{"title": "hello", "meta.tag": "hello"}

	f := i359Index(t)
	if err := f.IndexFields("c", "d", fields); err != nil {
		t.Fatal(err)
	}
	var rev string
	_ = f.db.View(func(tx *bolt.Tx) error {
		rev = string(tx.Bucket(bucketFTSFRev).Get(ftsfRevKey("c", "d")))
		return nil
	})
	if rev != `[{"f":"meta.tag","t":"hello"},{"f":"title","t":"hello"}]` {
		t.Fatalf("reverse entries = %s", rev)
	}
	// The language path writes the same bytes for the same fields.
	if err := f.IndexFieldsWithLang("c", "d", fields, ""); err != nil {
		t.Fatal(err)
	}
	var revLang string
	_ = f.db.View(func(tx *bolt.Tx) error {
		revLang = string(tx.Bucket(bucketFTSFRev).Get(ftsfRevKey("c", "d")))
		return nil
	})
	if revLang != rev {
		t.Fatalf("language path reverse entries = %s, want %s", revLang, rev)
	}

	bare := covLBareIndex(t)
	if err := bare.IndexFields("c", "d", fields); err != nil {
		t.Fatalf("no field buckets: %v", err)
	}

	for name, nest := range map[string]func(*FTSIndex){
		"posting":   func(f *FTSIndex) { i359Nest(t, f, bucketFTSF, ftsfKey("c", "title", "hello", "d")) },
		"length":    func(f *FTSIndex) { i359Nest(t, f, bucketFTSFMeta, ftsfMetaKey("c", "title", "d")) },
		"statistic": func(f *FTSIndex) { i359Nest(t, f, bucketFTSFStat, ftsfStatKey("c", "title")) },
	} {
		t.Run(name, func(t *testing.T) {
			f := i359Index(t)
			nest(f)
			if err := f.IndexFields("c", "d", map[string]string{"title": "hello"}); err == nil {
				t.Fatalf("a refused %s was not reported", name)
			}
		})
	}
}

// Removal: no field buckets is nothing to do, an unreadable reverse entry
// is dropped, statistics never go below zero, and a refused delete fails
// the removal.
func TestI359Removal(t *testing.T) {
	t.Run("no field buckets", func(t *testing.T) {
		f := covLBareIndex(t, bucketFTS, bucketFTSRev)
		if err := f.Remove("c", "d"); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("unreadable reverse entry", func(t *testing.T) {
		f := i359Index(t)
		i359Put(t, f, bucketFTSFRev, ftsfRevKey("c", "d"), []byte("{"))
		if err := f.Remove("c", "d"); err != nil {
			t.Fatal(err)
		}
		_ = f.db.View(func(tx *bolt.Tx) error {
			if tx.Bucket(bucketFTSFRev).Get(ftsfRevKey("c", "d")) != nil {
				t.Fatal("the unreadable entry was kept")
			}
			return nil
		})
	})
	t.Run("statistics floor", func(t *testing.T) {
		f := i359Index(t)
		i359Put(t, f, bucketFTSFRev, ftsfRevKey("c", "d"), []byte(`[{"f":"title","t":"hello"}]`))
		i359Put(t, f, bucketFTSFMeta, ftsfMetaKey("c", "title", "d"), []byte{100, 0, 0, 0})
		i359Put(t, f, bucketFTSFStat, ftsfStatKey("c", "title"), encodeCollectionStats(collectionStats{TotalDocs: 1, TotalTerms: 5}))
		if err := f.Remove("c", "d"); err != nil {
			t.Fatal(err)
		}
		_ = f.db.View(func(tx *bolt.Tx) error {
			if s := decodeCollectionStats(tx.Bucket(bucketFTSFStat).Get(ftsfStatKey("c", "title"))); s.TotalTerms != 0 || s.TotalDocs != 0 {
				t.Fatalf("stats = %+v, want zero", s)
			}
			return nil
		})
	})
	t.Run("refused delete", func(t *testing.T) {
		f := i359Index(t)
		i359Nest(t, f, bucketFTSRev, ftsRevKey("c", "d"))
		if err := f.Remove("c", "d"); err == nil {
			t.Fatal("a refused delete was not reported")
		}
	})
	t.Run("closed database", func(t *testing.T) {
		f := i359Index(t)
		_ = f.db.Close()
		if err := f.Remove("c", "d"); err == nil {
			t.Fatal("removal from a closed database was not reported")
		}
	})
	t.Run("read-only transaction", func(t *testing.T) {
		f := i359Index(t)
		i359Put(t, f, bucketFTSRev, ftsMetaKey("c", "d"), []byte{1, 0, 0, 0})
		i359Put(t, f, bucketFTSFRev, ftsfRevKey("c", "e"), []byte(`[]`))
		var bo binlog.BinlogOps
		_ = f.db.View(func(tx *bolt.Tx) error {
			if err := f.removeInTx(tx, &bo, "c", "d"); err == nil {
				t.Fatal("a refused length delete was not reported")
			}
			if err := f.removeInTx(tx, &bo, "c", "e"); err == nil {
				t.Fatal("a refused field delete was not reported")
			}
			return nil
		})
	})
}

// Search: no postings bucket finds nothing, a posting without a document is
// skipped, and a closed database is an error.
func TestI359Search(t *testing.T) {
	bare := covLBareIndex(t)
	if res, err := bare.Search("c", "hello", 10); err != nil || len(res) != 0 {
		t.Fatalf("no bucket: %v %v", res, err)
	}

	f := i359Index(t)
	i359Put(t, f, bucketFTS, ftsKey("c", "hello", ""), []byte{1, 0, 0, 0})
	if res, err := f.Search("c", "hello", 10); err != nil || len(res) != 0 {
		t.Fatalf("posting without a document: %v %v", res, err)
	}

	_ = f.db.Close()
	if _, err := f.Search("c", "hello", 10); err == nil {
		t.Fatal("search of a closed database was not reported")
	}
}
