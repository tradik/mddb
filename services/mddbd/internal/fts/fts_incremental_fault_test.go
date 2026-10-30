package fts

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"mddb/internal/binlog"

	bolt "go.etcd.io/bbolt"
	bolterrors "go.etcd.io/bbolt/errors"
)

// Failure paths of incremental indexing (#367): every write that fails
// fails the transaction, so a batch is indexed whole or not at all.

var errInjected = errors.New("injected put failure")

// failOn is a putFunc that refuses keys with the given prefix.
func failOn(prefix string) putFunc {
	return func(b *bolt.Bucket, k, v []byte) error {
		if bytes.HasPrefix(k, []byte(prefix)) {
			return errInjected
		}
		return b.Put(k, v)
	}
}

func faultDoc(id string) *pendingDoc {
	d := propDoc{id: id, content: "alpha beta", lang: "en", meta: map[string]string{"title": "zenith"}}.bulk()
	return newPendingDoc(&d)
}

func TestReindexInTxFailures(t *testing.T) {
	for _, prefix := range []string{"fts|", "ftsp|", "ftsf|"} {
		idx := newPropIndex(t)
		err := idx.db.Update(func(tx *bolt.Tx) error {
			var bo binlog.BinlogOps
			return idx.reindexInTx(tx, &bo, failOn(prefix), "c", faultDoc("d"))
		})
		if !errors.Is(err, errInjected) {
			t.Errorf("a failed %s write returned %v", prefix, err)
		}
	}
	// A read-only transaction refuses the removal that comes first.
	idx := newPropIndex(t)
	err := idx.db.View(func(tx *bolt.Tx) error {
		var bo binlog.BinlogOps
		return idx.applyInTx(tx, &bo, putNow, "c", faultDoc("d"), indexPlan{full: true})
	})
	if !errors.Is(err, bolterrors.ErrTxNotWritable) {
		t.Errorf("a full re-index in a read-only transaction returned %v", err)
	}
}

func TestApplyFieldPlanFailures(t *testing.T) {
	idx := newPropIndex(t)
	w := faultDoc("d")
	if err := idx.IndexDocs("c", []BulkDoc{*w.doc}); err != nil {
		t.Fatal(err)
	}
	err := idx.db.Update(func(tx *bolt.Tx) error {
		var bo binlog.BinlogOps
		return idx.applyInTx(tx, &bo, failOn("ftsf|"), "c", w, indexPlan{fields: []string{"meta.title"}})
	})
	if !errors.Is(err, errInjected) {
		t.Errorf("a failed field write returned %v", err)
	}

	// Everything but the signature succeeds: its key alone is too long.
	long := faultDoc(strings.Repeat("x", bolt.MaxKeySize-len("ftssig|c|")+1))
	err = idx.db.Update(func(tx *bolt.Tx) error {
		var bo binlog.BinlogOps
		return idx.applyInTx(tx, &bo, putNow, "c", long, indexPlan{fields: []string{"meta.gone"}})
	})
	if !errors.Is(err, bolterrors.ErrKeyTooLarge) {
		t.Errorf("a failed signature write returned %v", err)
	}
}

func TestIndexDocsWriteFailures(t *testing.T) {
	idx := newPropIndex(t)
	tooLong := BulkDoc{DocID: strings.Repeat("y", bolt.MaxKeySize), Content: "alpha", Lang: "en"}
	if err := idx.IndexDocs("c", []BulkDoc{tooLong}); !errors.Is(err, bolterrors.ErrKeyTooLarge) {
		t.Errorf("an unindexable document returned %v", err)
	}

	// The direct writes fit and the postings, written sorted at the end or
	// before a repeated document, do not.
	id := strings.Repeat("z", bolt.MaxKeySize-len("fts|c|alpha|")+1)
	first := BulkDoc{DocID: id, Content: "alpha", Lang: "en"}
	second := BulkDoc{DocID: id, Content: "alpha alpha", Lang: "en"}
	if err := idx.IndexDocs("c", []BulkDoc{first, second}); !errors.Is(err, bolterrors.ErrKeyTooLarge) {
		t.Errorf("a failed flush before a repeated document returned %v", err)
	}
}

// Missing buckets read as "nothing indexed" and write nothing.
func TestIncrementalWithoutBuckets(t *testing.T) {
	db, err := bolt.Open(t.TempDir()+"/empty.db", 0600, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	err = db.Update(func(tx *bolt.Tx) error {
		if _, ok := loadSignature(tx, "c", "d"); ok {
			t.Error("a signature was read from a missing bucket")
		}
		dropFieldLength(tx, "c", "meta.title", "d")
		return replaceFieldsInTx(tx, putNow, "c", "d", map[string]map[string]int{"meta.title": {"zenith": 1}})
	})
	if err != nil {
		t.Fatal(err)
	}
}

// The per-term entry point still replaces a document's earlier terms.
func TestIndexWithLangReplacesTerms(t *testing.T) {
	idx := newPropIndex(t)
	if err := idx.IndexWithLang("c", "d", "alpha beta", "en"); err != nil {
		t.Fatal(err)
	}
	if err := idx.IndexWithLang("c", "d", "gamma", "en"); err != nil {
		t.Fatal(err)
	}
	if hits, _ := idx.Search("c", "alpha", 10); len(hits) != 0 {
		t.Error("an earlier term survived re-indexing")
	}
}

// A field length entry too short to decode is removed without touching the
// field's statistics.
func TestDropFieldLengthShortEntry(t *testing.T) {
	idx := newPropIndex(t)
	err := idx.db.Update(func(tx *bolt.Tx) error {
		if err := tx.Bucket(bucketFTSFMeta).Put(ftsfMetaKey("c", "meta.t", "d"), []byte{1}); err != nil {
			return err
		}
		dropFieldLength(tx, "c", "meta.t", "d")
		if tx.Bucket(bucketFTSFMeta).Get(ftsfMetaKey("c", "meta.t", "d")) != nil {
			t.Error("the short entry was kept")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
