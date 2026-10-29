package fts

import (
	"errors"
	"math"
	"testing"

	bolterrors "go.etcd.io/bbolt/errors"
)

func TestCovLBM25FEmptyTokensNeverOpenTheDatabase(t *testing.T) {
	idx := covLClosedIndex(t)
	if res, err := idx.SearchBM25F("c", nil, 10, nil, DefaultBM25); err != nil || res != nil {
		t.Errorf("SearchBM25F: want nil, nil; got %v, %v", res, err)
	}
	if res, err := idx.SearchBM25FFuzzy("c", map[string]int{}, 10, 1, nil, DefaultBM25); err != nil || res != nil {
		t.Errorf("SearchBM25FFuzzy: want nil, nil; got %v, %v", res, err)
	}
}

func TestCovLBM25FReportsReadErrors(t *testing.T) {
	idx := covLClosedIndex(t)
	tokens := map[string]int{"golang": 1}
	if _, err := idx.SearchBM25F("c", tokens, 10, nil, DefaultBM25); !errors.Is(err, bolterrors.ErrDatabaseNotOpen) {
		t.Errorf("SearchBM25F: want ErrDatabaseNotOpen, got %v", err)
	}
	if _, err := idx.SearchBM25FFuzzy("c", tokens, 10, 1, nil, DefaultBM25); !errors.Is(err, bolterrors.ErrDatabaseNotOpen) {
		t.Errorf("SearchBM25FFuzzy: want ErrDatabaseNotOpen, got %v", err)
	}
}

func TestCovLBM25FFuzzyWithoutFieldStats(t *testing.T) {
	idx := covLIndex(t)
	res, err := idx.SearchBM25FFuzzy("empty", map[string]int{"golang": 1}, 10, 1, nil, DefaultBM25)
	if err != nil || len(res) != 0 {
		t.Errorf("want no results, got %v, %v", res, err)
	}
}

// A field whose statistics say it holds no terms falls back to avgdl 1, and a
// posting key with no document is skipped; the score is then exact.
func TestCovLBM25FZeroAverageLengthField(t *testing.T) {
	idx := covLIndex(t)
	if err := idx.Index("c", "d0", "zeta omega"); err != nil { // content: N = 1
		t.Fatal(err)
	}
	covLPut(t, idx, bucketFTSF, ftsfKey("c", "meta.title", "quark", "d1"), covLU32(3))
	covLPut(t, idx, bucketFTSF, ftsfKey("c", "meta.title", "quark", ""), covLU32(9))
	covLPut(t, idx, bucketFTSFStat, ftsfStatKey("c", "meta.title"), encodeCollectionStats(collectionStats{}))

	p := BM25Params{K1: 2, B: 0.5}
	weights := map[string]float64{"content": 1, "meta.title": 2}
	res, err := idx.SearchBM25F("c", map[string]int{"quark": 1}, 10, weights, p)
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 1 || res[0].DocID != "d1" {
		t.Fatalf("want only d1, got %+v", res)
	}
	// N=1, df=1: idf = ln(0.5/1.5 + 1). tf~ = 2*3 / (1 - b + b*1/1) = 6.
	want := math.Log(0.5/1.5+1) * 6 / (p.K1 + 6)
	if math.Abs(res[0].Score-want) > 1e-12 {
		t.Errorf("score: want %v, got %v", want, res[0].Score)
	}
}

func TestCovLBM25FFuzzyExactLabelsAndLimit(t *testing.T) {
	idx := covLIndex(t)
	for id, title := range map[string]string{"d1": "golang", "d2": "golang golang", "d3": "golanx"} {
		if err := indexWithFields(idx, "c", id, map[string]string{"meta.title": title}); err != nil {
			t.Fatal(err)
		}
	}
	tokens := map[string]int{"golang": 1}
	all, err := idx.SearchBM25FFuzzy("c", tokens, 0, 1, nil, DefaultBM25)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 {
		t.Fatalf("want 3 results, got %+v", all)
	}
	labels := map[string]string{}
	for i, r := range all {
		labels[r.DocID] = r.MatchedTerms[0]
		if i > 0 && all[i-1].Score < r.Score {
			t.Errorf("results not in descending score order: %+v", all)
		}
	}
	if labels["d1"] != "golang" || labels["d2"] != "golang" || labels["d3"] != "golang~golanx" {
		t.Errorf("unexpected labels %v", labels)
	}
	// The fuzzy match scores as its own term, times the fuzzy penalty.
	direct, err := idx.SearchBM25F("c", map[string]int{"golanx": 1}, 0, nil, DefaultBM25)
	if err != nil || len(direct) != 1 {
		t.Fatalf("direct golanx search: %+v, %v", direct, err)
	}
	if got, want := covLScores(all)["d3"], direct[0].Score*fuzzyPenalty; math.Abs(got-want) > 1e-12 {
		t.Errorf("d3: want penalised score %v, got %v", want, got)
	}

	top, err := idx.SearchBM25FFuzzy("c", tokens, 2, 1, nil, DefaultBM25)
	if err != nil {
		t.Fatal(err)
	}
	if len(top) != 2 || top[0].DocID != all[0].DocID || top[1].DocID != all[1].DocID {
		t.Errorf("limit 2: want the first two of %+v, got %+v", all, top)
	}
}
