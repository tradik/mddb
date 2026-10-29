package fts

import (
	"errors"
	"math"
	"reflect"
	"testing"

	bolt "go.etcd.io/bbolt"
	bolterrors "go.etcd.io/bbolt/errors"
)

func TestCovLExpandFuzzyTermsWithoutBucket(t *testing.T) {
	idx := covLBareIndex(t)
	_ = idx.db.View(func(tx *bolt.Tx) error {
		if got := idx.expandFuzzyTerms(tx, "c", map[string]int{"kitten": 1}, 2); got != nil {
			t.Errorf("want nil without the fts bucket, got %v", got)
		}
		return nil
	})
}

// Keys without a term (no pipe after it, or an empty one) are not terms: an
// empty term would otherwise be within distance 1 of any one-letter query.
func TestCovLExpandFuzzyTermsSkipsMalformedKeys(t *testing.T) {
	idx := covLIndex(t)
	if err := idx.Index("c", "d1", "kitten"); err != nil {
		t.Fatal(err)
	}
	covLPut(t, idx, bucketFTS, []byte("fts|c|nopipe"), covLU32(1))
	covLPut(t, idx, bucketFTS, []byte("fts|c||d9"), covLU32(1))

	_ = idx.db.View(func(tx *bolt.Tx) error {
		got := idx.expandFuzzyTerms(tx, "c", map[string]int{"kitten": 1, "x": 1}, 1)
		if want := map[string]string{"kitten": "kitten"}; !reflect.DeepEqual(got, want) {
			t.Errorf("want %v, got %v", want, got)
		}
		return nil
	})
}

func TestCovLFuzzySearchesMissingBuckets(t *testing.T) {
	for name, idx := range map[string]*FTSIndex{
		"no buckets":    covLBareIndex(t),
		"no rev bucket": covLBareIndex(t, bucketFTS),
	} {
		if res, err := idx.SearchBM25Fuzzy("c", "kitten", 10, 1, DefaultBM25); err != nil || len(res) != 0 {
			t.Errorf("%s: SearchBM25Fuzzy want nothing, got %v, %v", name, res, err)
		}
	}
	if res, err := covLBareIndex(t).SearchFuzzy("c", "kitten", 10, 1); err != nil || len(res) != 0 {
		t.Errorf("SearchFuzzy without buckets: want nothing, got %v, %v", res, err)
	}
}

func TestCovLFuzzySearchesReportReadErrors(t *testing.T) {
	idx := covLClosedIndex(t)
	if _, err := idx.SearchFuzzy("c", "kitten", 10, 1); !errors.Is(err, bolterrors.ErrDatabaseNotOpen) {
		t.Errorf("SearchFuzzy: want ErrDatabaseNotOpen, got %v", err)
	}
	if _, err := idx.SearchBM25Fuzzy("c", "kitten", 10, 1, DefaultBM25); !errors.Is(err, bolterrors.ErrDatabaseNotOpen) {
		t.Errorf("SearchBM25Fuzzy: want ErrDatabaseNotOpen, got %v", err)
	}
}

// covLFuzzyCorpus indexes two exact and one fuzzy match for "kitten".
func covLFuzzyCorpus(t *testing.T) *FTSIndex {
	t.Helper()
	idx := covLIndex(t)
	for id, body := range map[string]string{"d1": "kitten", "d2": "kitten kitten purr", "d3": "sitten", "d4": "dog"} {
		if err := idx.Index("c", id, body); err != nil {
			t.Fatal(err)
		}
	}
	return idx
}

// covLCheckFuzzyResults asserts the corpus' three matches, their labels and
// their order, and that a limit keeps the top of that order.
func covLCheckFuzzyResults(t *testing.T, all, top []FTSResult) {
	t.Helper()
	if len(all) != 3 {
		t.Fatalf("want d1, d2, d3; got %+v", all)
	}
	for i, r := range all {
		want := "kitten"
		if r.DocID == "d3" {
			want = "kitten~sitten"
		}
		if !reflect.DeepEqual(r.MatchedTerms, []string{want}) {
			t.Errorf("%s: want terms [%s], got %v", r.DocID, want, r.MatchedTerms)
		}
		if i > 0 && all[i-1].Score < r.Score {
			t.Errorf("not in descending order: %+v", all)
		}
	}
	if len(top) != 2 || top[0].DocID != all[0].DocID || top[1].DocID != all[1].DocID {
		t.Errorf("limit 2: want top two of %+v, got %+v", all, top)
	}
}

func TestCovLSearchFuzzyIgnoresBareTermKeyAndLimits(t *testing.T) {
	idx := covLFuzzyCorpus(t)
	before, err := idx.SearchFuzzy("c", "kitten", 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	covLPut(t, idx, bucketFTS, ftsKey("c", "kitten", ""), covLU32(4))
	all, err := idx.SearchFuzzy("c", "kitten", 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	covLSameScores(t, before, all)
	top, err := idx.SearchFuzzy("c", "kitten", 2, 1)
	if err != nil {
		t.Fatal(err)
	}
	covLCheckFuzzyResults(t, all, top)
	// TF-IDF here has no IDF: the penalised fuzzy match ranks last.
	if all[2].DocID != "d3" {
		t.Errorf("fuzzy match must rank last, got %+v", all)
	}
}

func TestCovLSearchBM25FuzzyIgnoresBareTermKeyAndLimits(t *testing.T) {
	idx := covLFuzzyCorpus(t)
	before, err := idx.SearchBM25Fuzzy("c", "kitten", 0, 1, DefaultBM25)
	if err != nil {
		t.Fatal(err)
	}
	covLPut(t, idx, bucketFTS, ftsKey("c", "kitten", ""), covLU32(4))
	all, err := idx.SearchBM25Fuzzy("c", "kitten", 0, 1, DefaultBM25)
	if err != nil {
		t.Fatal(err)
	}
	covLSameScores(t, before, all)
	top, err := idx.SearchBM25Fuzzy("c", "kitten", 2, 1, DefaultBM25)
	if err != nil {
		t.Fatal(err)
	}
	covLCheckFuzzyResults(t, all, top)
	// The fuzzy term keeps its own (higher, rarer) IDF, times the penalty.
	direct, err := idx.SearchBM25("c", "sitten", 0, DefaultBM25)
	if err != nil || len(direct) != 1 {
		t.Fatalf("direct sitten search: %+v, %v", direct, err)
	}
	if got, want := covLScores(all)["d3"], direct[0].Score*fuzzyPenalty; math.Abs(got-want) > 1e-12 {
		t.Errorf("d3: want penalised score %v, got %v", want, got)
	}
}
