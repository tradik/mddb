package fts

import (
	"errors"
	"reflect"
	"sort"
	"testing"

	bolterrors "go.etcd.io/bbolt/errors"
)

// covLBoolCorpus: d1 "python ruby", d2 "python", d3 "ruby", d4 "java python".
func covLBoolCorpus(t *testing.T) *FTSIndex {
	t.Helper()
	idx := covLIndex(t)
	for id, body := range map[string]string{"d1": "python ruby", "d2": "python", "d3": "ruby", "d4": "java python"} {
		if err := idx.Index("c", id, body); err != nil {
			t.Fatal(err)
		}
	}
	return idx
}

// covLDocIDs returns the sorted document IDs of results.
func covLDocIDs(results []FTSResult) []string {
	ids := make([]string, 0, len(results))
	for _, r := range results {
		ids = append(ids, r.DocID)
	}
	sort.Strings(ids)
	return ids
}

func covLBoolean(t *testing.T, idx *FTSIndex, pq *ParsedQuery, limit int) []FTSResult {
	t.Helper()
	res, err := idx.SearchBoolean("c", pq, limit)
	if err != nil {
		t.Fatalf("SearchBoolean(%+v): %v", pq, err)
	}
	return res
}

func TestCovLBooleanNoClausesNeverOpensTheDatabase(t *testing.T) {
	res, err := covLClosedIndex(t).SearchBoolean("c", &ParsedQuery{}, 10)
	if err != nil || res != nil {
		t.Errorf("want nil, nil; got %v, %v", res, err)
	}
}

func TestCovLBooleanReportsClauseErrors(t *testing.T) {
	idx := covLClosedIndex(t)
	for _, q := range []string{`alpha`, `"alpha beta"`, `"alpha beta"~3`, `al*`} {
		pq := ParseAdvancedQuery(q)
		if _, err := idx.SearchBoolean("c", pq, 10); !errors.Is(err, bolterrors.ErrDatabaseNotOpen) {
			t.Errorf("%s (%s): want ErrDatabaseNotOpen, got %v", q, pq.Clauses[0].Type, err)
		}
	}
}

func TestCovLBooleanProximityClause(t *testing.T) {
	idx := covLIndex(t)
	for id, body := range map[string]string{"near": "alpha beta gamma", "far": "alpha one two three four five six beta"} {
		if err := idx.Index("c", id, body); err != nil {
			t.Fatal(err)
		}
		if err := idx.IndexPositions("c", id, body); err != nil {
			t.Fatal(err)
		}
	}
	pq := ParseAdvancedQuery(`"alpha beta"~2`)
	if pq.Clauses[0].Type != "proximity" {
		t.Fatalf("want a proximity clause, got %+v", pq.Clauses)
	}
	res := covLBoolean(t, idx, pq, 10)
	if got := covLDocIDs(res); !reflect.DeepEqual(got, []string{"near"}) {
		t.Errorf("want [near], got %v", got)
	}
	if res[0].Score <= 0 {
		t.Errorf("want a positive score, got %v", res[0].Score)
	}
}

// A leading NOT used to shift every operator onto the next clause, so the OR
// below intersected python and ruby instead of uniting them.
func TestCovLBooleanLeadingNotKeepsOperators(t *testing.T) {
	idx := covLBoolCorpus(t)
	res := covLBoolean(t, idx, ParseAdvancedQuery("NOT java python OR ruby"), 10)
	if got := covLDocIDs(res); !reflect.DeepEqual(got, []string{"d1", "d2", "d3"}) {
		t.Errorf("want [d1 d2 d3], got %v", got)
	}
	res = covLBoolean(t, idx, ParseAdvancedQuery("-java python ruby"), 10)
	if got := covLDocIDs(res); !reflect.DeepEqual(got, []string{"d1"}) {
		t.Errorf("implicit AND: want [d1], got %v", got)
	}
}

func TestCovLBooleanOnlyNegatedClauses(t *testing.T) {
	res := covLBoolean(t, covLBoolCorpus(t), ParseAdvancedQuery("NOT java"), 10)
	if len(res) != 0 {
		t.Errorf("a query of exclusions alone matches nothing, got %+v", res)
	}
}

func TestCovLBooleanOrBoostsAndLimits(t *testing.T) {
	idx := covLBoolCorpus(t)
	all := covLBoolean(t, idx, ParseAdvancedQuery("python OR ruby"), 0)
	if got := covLDocIDs(all); !reflect.DeepEqual(got, []string{"d1", "d2", "d3", "d4"}) {
		t.Fatalf("want all four, got %v", got)
	}
	// d1 holds both terms: its scores add up and its terms merge.
	if all[0].DocID != "d1" {
		t.Errorf("want d1 first, got %+v", all)
	}
	terms := append([]string(nil), all[0].MatchedTerms...)
	sort.Strings(terms)
	if !reflect.DeepEqual(terms, []string{"python", "ruby"}) {
		t.Errorf("d1 terms: want [python ruby], got %v", terms)
	}
	top := covLBoolean(t, idx, ParseAdvancedQuery("python OR ruby"), 2)
	if len(top) != 2 || top[0].DocID != "d1" || top[1].Score > top[0].Score {
		t.Errorf("limit 2: want d1 then the next best, got %+v", top)
	}
}

func TestCovLBooleanImplicitOperatorUsesDefault(t *testing.T) {
	idx := covLBoolCorpus(t)
	clauses := []QueryClause{{Type: "term", Value: "python"}, {Type: "term", Value: "ruby"}}
	or := covLBoolean(t, idx, &ParsedQuery{Clauses: clauses, DefaultOp: "OR"}, 10)
	if got := covLDocIDs(or); !reflect.DeepEqual(got, []string{"d1", "d2", "d3", "d4"}) {
		t.Errorf("default OR: want all four, got %v", got)
	}
	and := covLBoolean(t, idx, &ParsedQuery{Clauses: clauses, DefaultOp: "AND"}, 10)
	if got := covLDocIDs(and); !reflect.DeepEqual(got, []string{"d1"}) {
		t.Errorf("default AND: want [d1], got %v", got)
	}
}

func TestCovLSearchSingleTermStopWord(t *testing.T) {
	res, err := covLClosedIndex(t).searchSingleTerm("c", "the")
	if err != nil || res != nil {
		t.Errorf("a stop word yields no terms and no read: got %v, %v", res, err)
	}
}

func TestCovLSearchSingleTermMissingBuckets(t *testing.T) {
	for name, idx := range map[string]*FTSIndex{
		"no buckets":    covLBareIndex(t),
		"no rev bucket": covLBareIndex(t, bucketFTS),
	} {
		if res, err := idx.searchSingleTerm("c", "python"); err != nil || len(res) != 0 {
			t.Errorf("%s: want nothing, got %v, %v", name, res, err)
		}
	}
}

func TestCovLSearchSingleTermIgnoresBareTermKey(t *testing.T) {
	idx := covLBoolCorpus(t)
	before, err := idx.searchSingleTerm("c", "python")
	if err != nil {
		t.Fatal(err)
	}
	covLPut(t, idx, bucketFTS, ftsKey("c", "python", ""), covLU32(2))
	after, err := idx.searchSingleTerm("c", "python")
	if err != nil {
		t.Fatal(err)
	}
	if got := covLDocIDs(after); !reflect.DeepEqual(got, []string{"d1", "d2", "d4"}) {
		t.Errorf("want [d1 d2 d4], got %v", got)
	}
	covLSameScores(t, before, after)
}
