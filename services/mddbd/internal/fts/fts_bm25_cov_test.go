package fts

import (
	"encoding/binary"
	"errors"
	"path/filepath"
	"testing"

	bolt "go.etcd.io/bbolt"
	bolterrors "go.etcd.io/bbolt/errors"
)

// covLIndex is a fresh index with every FTS bucket, closed at test end.
func covLIndex(t *testing.T) *FTSIndex {
	t.Helper()
	idx, cleanup := newTestFTSIndex(t)
	t.Cleanup(cleanup)
	return idx
}

// covLBareIndex is an index over a database holding only the named buckets.
func covLBareIndex(t *testing.T, buckets ...[]byte) *FTSIndex {
	t.Helper()
	db, err := bolt.Open(filepath.Join(t.TempDir(), "bare.db"), 0o600, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Update(func(tx *bolt.Tx) error {
		for _, b := range buckets {
			if _, err := tx.CreateBucket(b); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return NewFTSIndex(db)
}

// covLClosedIndex is an index whose database is already closed, so every
// transaction it opens fails.
func covLClosedIndex(t *testing.T) *FTSIndex {
	t.Helper()
	idx, cleanup := newTestFTSIndex(t)
	cleanup()
	return idx
}

// covLPut writes one raw key into bucket, bypassing the indexer.
func covLPut(t *testing.T, idx *FTSIndex, bucket, key, val []byte) {
	t.Helper()
	if err := idx.db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket(bucket).Put(key, val)
	}); err != nil {
		t.Fatal(err)
	}
}

// covLU32 encodes n the way postings and lengths are stored.
func covLU32(n uint32) []byte {
	b := make([]byte, 4)
	binary.LittleEndian.PutUint32(b, n)
	return b
}

// covLScores maps each result's DocID to its score.
func covLScores(results []FTSResult) map[string]float64 {
	out := make(map[string]float64, len(results))
	for _, r := range results {
		out[r.DocID] = r.Score
	}
	return out
}

// covLSameScores fails unless both result sets hold the same documents with
// the same scores.
func covLSameScores(t *testing.T, want, got []FTSResult) {
	t.Helper()
	w, g := covLScores(want), covLScores(got)
	if len(w) != len(g) {
		t.Fatalf("results differ: want %v, got %v", w, g)
	}
	for id, s := range w {
		if gs, ok := g[id]; !ok || gs != s {
			t.Fatalf("doc %q: want score %v, got %v (present=%v)", id, s, gs, ok)
		}
	}
}

func covLStats(t *testing.T, idx *FTSIndex, collection string) collectionStats {
	t.Helper()
	var s collectionStats
	if err := idx.db.View(func(tx *bolt.Tx) error {
		s = decodeCollectionStats(tx.Bucket(bucketFTSRev).Get(ftsStatKey(collection)))
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestCovLBM25MetaWithoutReverseBucket(t *testing.T) {
	idx := covLBareIndex(t)
	err := idx.db.Update(func(tx *bolt.Tx) error {
		if err := idx.IndexBM25Meta(tx, "c", "d1", 7); err != nil {
			return err
		}
		return idx.RemoveBM25Meta(tx, "c", "d1")
	})
	if err != nil {
		t.Fatalf("missing bucket must be a no-op, got %v", err)
	}
	_ = idx.db.View(func(tx *bolt.Tx) error {
		if tx.Bucket(bucketFTSRev) != nil {
			t.Error("no-op must not create the reverse bucket")
		}
		return nil
	})
}

func TestCovLIndexBM25MetaReadOnlyTx(t *testing.T) {
	idx := covLIndex(t)
	err := idx.db.View(func(tx *bolt.Tx) error {
		return idx.IndexBM25Meta(tx, "c", "d1", 7)
	})
	if !errors.Is(err, bolterrors.ErrTxNotWritable) {
		t.Fatalf("want ErrTxNotWritable, got %v", err)
	}
	if s := covLStats(t, idx, "c"); s.TotalDocs != 0 || s.TotalTerms != 0 {
		t.Errorf("failed write must leave stats empty, got %+v", s)
	}
}

func TestCovLRemoveBM25MetaReadOnlyTx(t *testing.T) {
	idx := covLIndex(t)
	if err := idx.db.Update(func(tx *bolt.Tx) error {
		return idx.IndexBM25Meta(tx, "c", "d1", 7)
	}); err != nil {
		t.Fatal(err)
	}
	err := idx.db.View(func(tx *bolt.Tx) error {
		return idx.RemoveBM25Meta(tx, "c", "d1")
	})
	if !errors.Is(err, bolterrors.ErrTxNotWritable) {
		t.Fatalf("want ErrTxNotWritable, got %v", err)
	}
	if s := covLStats(t, idx, "c"); s.TotalDocs != 1 || s.TotalTerms != 7 {
		t.Errorf("failed delete must keep stats, got %+v", s)
	}
}

func TestCovLRemoveBM25MetaClampsTotalTerms(t *testing.T) {
	idx := covLIndex(t)
	if err := idx.db.Update(func(tx *bolt.Tx) error {
		return idx.IndexBM25Meta(tx, "c", "d1", 10)
	}); err != nil {
		t.Fatal(err)
	}
	// Stats that disagree with the document length: removing must not wrap.
	covLPut(t, idx, bucketFTSRev, ftsStatKey("c"), encodeCollectionStats(collectionStats{TotalDocs: 1, TotalTerms: 3}))
	if err := idx.db.Update(func(tx *bolt.Tx) error {
		return idx.RemoveBM25Meta(tx, "c", "d1")
	}); err != nil {
		t.Fatal(err)
	}
	if s := covLStats(t, idx, "c"); s.TotalDocs != 0 || s.TotalTerms != 0 {
		t.Errorf("want stats clamped to zero, got %+v", s)
	}
}

func TestCovLSearchBM25MissingBuckets(t *testing.T) {
	for name, idx := range map[string]*FTSIndex{
		"no buckets":      covLBareIndex(t),
		"no rev bucket":   covLBareIndex(t, bucketFTS),
		"no fts postings": covLBareIndex(t, bucketFTSRev),
	} {
		res, err := idx.SearchBM25("c", "alpha", 10, DefaultBM25)
		if err != nil || len(res) != 0 {
			t.Errorf("%s: want no results and no error, got %v, %v", name, res, err)
		}
	}
}

func TestCovLSearchBM25IgnoresBareTermKey(t *testing.T) {
	idx := covLIndex(t)
	for id, body := range map[string]string{"d1": "alpha beta", "d2": "alpha gamma gamma", "d3": "delta"} {
		if err := idx.Index("c", id, body); err != nil {
			t.Fatal(err)
		}
	}
	before, err := idx.SearchBM25("c", "alpha", 0, DefaultBM25)
	if err != nil {
		t.Fatal(err)
	}
	// A posting key with no document must neither count towards df nor score.
	covLPut(t, idx, bucketFTS, ftsKey("c", "alpha", ""), covLU32(5))
	after, err := idx.SearchBM25("c", "alpha", 0, DefaultBM25)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != 2 {
		t.Fatalf("want d1 and d2, got %+v", after)
	}
	covLSameScores(t, before, after)
	// d1 is the shorter document, so the same tf scores higher.
	if after[0].DocID != "d1" || after[0].Score <= after[1].Score {
		t.Errorf("want d1 ranked first, got %+v", after)
	}
}
