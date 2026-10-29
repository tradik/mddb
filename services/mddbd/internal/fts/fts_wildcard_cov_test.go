package fts

import (
	"reflect"
	"sort"
	"testing"
)

func TestCovLWildcardStemsAPlainPattern(t *testing.T) {
	idx := covLIndex(t)
	idx.SetStemmer(NewPorterStemmer())
	if err := idx.Index("c", "d1", "running fast"); err != nil {
		t.Fatal(err)
	}
	res, err := idx.SearchWildcard("c", "RUNNING", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 1 || res[0].DocID != "d1" || !reflect.DeepEqual(res[0].MatchedTerms, []string{"run"}) {
		t.Errorf("plain pattern must be stemmed to match \"run\", got %+v", res)
	}
	// A pattern with wildcards is not stemmed: "runnin*" finds nothing.
	if res, err := idx.SearchWildcard("c", "runnin*", 0); err != nil || len(res) != 0 {
		t.Errorf("wildcard pattern must stay unstemmed, got %+v, %v", res, err)
	}
}

func TestCovLWildcardMissingBuckets(t *testing.T) {
	for name, idx := range map[string]*FTSIndex{
		"no buckets":    covLBareIndex(t),
		"no rev bucket": covLBareIndex(t, bucketFTS),
	} {
		if res, err := idx.SearchWildcard("c", "a*", 10); err != nil || len(res) != 0 {
			t.Errorf("%s: want nothing, got %v, %v", name, res, err)
		}
	}
}

// Malformed keys and a posting with no document must not surface: an empty
// term matches "*", and its posting would bring in d9.
func TestCovLWildcardSkipsMalformedKeys(t *testing.T) {
	idx := covLIndex(t)
	if err := idx.Index("c", "d1", "apple apricot"); err != nil {
		t.Fatal(err)
	}
	before, err := idx.SearchWildcard("c", "*", 0)
	if err != nil {
		t.Fatal(err)
	}
	covLPut(t, idx, bucketFTS, []byte("fts|c|nopipe"), covLU32(1))
	covLPut(t, idx, bucketFTS, []byte("fts|c||d9"), covLU32(1))
	covLPut(t, idx, bucketFTS, ftsKey("c", "apple", ""), covLU32(3))

	res, err := idx.SearchWildcard("c", "*", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 1 || res[0].DocID != "d1" {
		t.Fatalf("want only d1, got %+v", res)
	}
	terms := append([]string(nil), res[0].MatchedTerms...)
	sort.Strings(terms)
	if !reflect.DeepEqual(terms, []string{"apple", "apricot"}) {
		t.Errorf("want apple and apricot, got %v", terms)
	}
	covLSameScores(t, before, res)
}

func TestCovLWildcardLimit(t *testing.T) {
	idx := covLIndex(t)
	for id, body := range map[string]string{"d1": "apple", "d2": "apple pie with cream", "d3": "apple apple"} {
		if err := idx.Index("c", id, body); err != nil {
			t.Fatal(err)
		}
	}
	all, err := idx.SearchWildcard("c", "app*", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 {
		t.Fatalf("want 3, got %+v", all)
	}
	top, err := idx.SearchWildcard("c", "app*", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(top) != 2 || top[0].DocID != all[0].DocID || top[1].DocID != all[1].DocID {
		t.Errorf("limit 2: want top two of %+v, got %+v", all, top)
	}
	if top[0].Score < top[1].Score {
		t.Errorf("not in descending order: %+v", top)
	}
}
