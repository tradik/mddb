package main

import (
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestSplitClauses(t *testing.T) {
	cases := map[string][]string{
		"take a backup and restore it":            {"take a backup", "restore it"},
		"one sentence. another one; a third":      {"one sentence", "another one", "a third"},
		"backup and restore":                      {"backup and restore"},
		"cats and dogs and":                       {"cats and dogs and"},
		"plain query":                             {"plain query"},
		"and then some":                           {"and then some"},
		"first part, x":                           {"first part, x"},
		"rock and roll. and":                      {"rock and roll. and"},
		"read the logs and check the disk, twice": {"read the logs", "check the disk twice"},
	}
	for in, want := range cases {
		if got := splitClauses(in); !reflect.DeepEqual(got, want) {
			t.Errorf("splitClauses(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSplitWindows(t *testing.T) {
	if got := splitWindows("a b c d e f g", 4, 2); !reflect.DeepEqual(got, []string{"a b c d", "c d e f", "d e f g"}) {
		t.Errorf("windows = %q", got)
	}
	if got := splitWindows("a b c d e f", 4, 2); !reflect.DeepEqual(got, []string{"a b c d", "c d e f"}) {
		t.Errorf("windows = %q", got)
	}
	for _, c := range []struct{ size, stride int }{{4, 0}, {0, 2}, {10, 2}} {
		if got := splitWindows("a b c d e", c.size, c.stride); !reflect.DeepEqual(got, []string{"a b c d e"}) {
			t.Errorf("windows(%d,%d) = %q", c.size, c.stride, got)
		}
	}
}

func TestVariantsAddTheWholeQueryOnlyWhenSplit(t *testing.T) {
	vs := variants(4, 2)
	if got := vs[3].Parts("short"); !reflect.DeepEqual(got, []string{"short"}) {
		t.Errorf("whole + clauses of an unsplit query = %q", got)
	}
	if got := vs[3].Parts("x y and z w"); !reflect.DeepEqual(got, []string{"x y and z w", "x y", "z w"}) {
		t.Errorf("whole + clauses = %q", got)
	}
	if got := vs[2].Parts("a b c d e"); len(got) != 2 {
		t.Errorf("windows = %q", got)
	}
}

func TestPairQueriesUnionKeysAndGrades(t *testing.T) {
	qs := []evalQuery{
		{ID: "a", Query: "qa", ExpectedKeys: []string{"A.md", "S.md"}, Grades: map[string]int{"A.md": 3, "S.md": 1}},
		{ID: "b", Query: "qb", ExpectedKeys: []string{"B.md", "S.md"}, Grades: map[string]int{"S.md": 2}},
		{ID: "c", Query: "qc", ExpectedKeys: []string{"C.md"}},
	}
	ps := pairQueries(qs)
	if len(ps) != 3 {
		t.Fatalf("%d pairs, want 3", len(ps))
	}
	p := ps[0]
	if p.ID != "a+b" || p.Query != "qa and qb" || !reflect.DeepEqual(p.ExpectedKeys, []string{"A.md", "S.md", "B.md"}) ||
		p.Grades["S.md"] != 2 || p.Grades["A.md"] != 3 {
		t.Errorf("pair = %+v", p)
	}
}

func TestReadEvalSetRejectsBadFiles(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.json")
	empty := filepath.Join(dir, "empty.json")
	_ = os.WriteFile(bad, []byte("{"), 0o600)
	_ = os.WriteFile(empty, []byte(`{"queries": []}`), 0o600)
	for _, p := range []string{bad, empty, filepath.Join(dir, "missing.json")} {
		if _, err := readEvalSet(p); err == nil {
			t.Errorf("%s accepted", p)
		}
	}
}

func TestScoreQueryMatchesTheServerFormulas(t *testing.T) {
	q := evalQuery{ExpectedKeys: []string{"A", "B"}, Grades: map[string]int{"A": 3}}
	m := scoreQuery(q, []string{"X", "B", "A", "Y"}, 3)
	idcg := 7 + 1/math.Log2(3)
	dcg := 1/math.Log2(3) + 7/math.Log2(4)
	if m.Recall != 1 || m.RR != 0.5 || math.Abs(m.NDCG-dcg/idcg) > 1e-12 {
		t.Errorf("metrics = %+v", m)
	}
	if m := scoreQuery(evalQuery{ExpectedKeys: []string{"A"}, Grades: map[string]int{"A": 0}}, []string{"A"}, 10); m.NDCG != 0 {
		t.Errorf("zero ideal gain gave nDCG %v", m.NDCG)
	}
}

func TestMaxSimRankAveragesPartsAndBreaksTies(t *testing.T) {
	parts := []partScores{
		{Best: map[string]float64{"a": 0.9, "b": 0.5, "c": 0.4}},
		{Best: map[string]float64{"a": 0.1, "b": 0.6, "c": 0.4}},
	}
	if got := maxSimRank(parts, 2, false); !reflect.DeepEqual(got, []string{"b", "a"}) {
		t.Errorf("rank = %q", got)
	}
	if got := maxSimRank(parts, 2, true); !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Errorf("best-part rank = %q", got)
	}
	tie := []partScores{{Best: map[string]float64{"z": 1, "y": 1}}}
	if got := maxSimRank(tie, 5, false); !reflect.DeepEqual(got, []string{"y", "z"}) {
		t.Errorf("tie rank = %q", got)
	}
}

func TestStatsHelpers(t *testing.T) {
	if percentile(nil, 50) != 0 || percentile([]float64{3, 1, 2}, 50) != 2 || percentile([]float64{3, 1, 2}, 99) != 3 {
		t.Error("percentile")
	}
	if equal([]string{"a"}, []string{"a", "b"}) || equal([]string{"a"}, []string{"b"}) || !equal(nil, nil) {
		t.Error("equal")
	}
	ndcg := func(m metrics) float64 { return m.NDCG }
	if lo, hi := pairedCI(nil, nil, ndcg, 100, 1); lo != 0 || hi != 0 {
		t.Error("empty CI")
	}
	a := []metrics{{NDCG: 1}, {NDCG: 1}, {NDCG: 1}}
	b := []metrics{{NDCG: 0}, {NDCG: 1}, {NDCG: 1}}
	if lo, hi := pairedCI(a, b, ndcg, 500, 1); lo < 0 || hi > 1 || hi < lo {
		t.Errorf("CI = [%v, %v]", lo, hi)
	}
	if better, worse := tally(b, a); better != 0 || worse != 1 {
		t.Errorf("tally = %d/%d", better, worse)
	}
	if r := baseRow([]row{{Name: "x"}, {Name: "y"}}); r.Name != "x" {
		t.Errorf("baseRow fallback = %q", r.Name)
	}
	if meanOf(nil, ndcg) != 0 {
		t.Error("meanOf empty")
	}
}

func TestClientErrors(t *testing.T) {
	f := newFake()
	srv := httptest.NewServer(f)
	defer srv.Close()
	client := srv.Client()
	cfg := config{URL: srv.URL, Collection: "c", ChunkTopK: 10, WaitTries: 1}
	if _, err := post(client, srv.URL, map[string]any{"bad": make(chan int)}); err == nil {
		t.Error("unmarshalable body accepted")
	}
	if _, err := post(client, "http://127.0.0.1:1/none", nil); err == nil {
		t.Error("unreachable server accepted")
	}
	f.fail = "/v1/vector-search"
	if err := waitIndexed(client, cfg); err == nil {
		t.Error("wait ignored a failing search")
	}
	if _, err := fetchPart(client, cfg, "x"); err == nil {
		t.Error("fetch ignored a failing search")
	}
	f.fail, f.badJSON = "", "/v1/vector-search"
	if err := waitIndexed(client, cfg); err == nil {
		t.Error("wait accepted malformed JSON")
	}
	if _, err := fetchPart(client, cfg, "x"); err == nil {
		t.Error("fetch accepted malformed JSON")
	}
	if err := loadDocs(client, config{URL: srv.URL, DocsDir: t.TempDir()}); err == nil {
		t.Error("empty docs directory accepted")
	}
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "dir.md"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := loadDocs(client, config{URL: srv.URL, DocsDir: dir}); err == nil {
		t.Error("unreadable page accepted")
	}
	var _ http.Handler = f
}
