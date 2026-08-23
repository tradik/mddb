package fts

import (
	"strings"
	"testing"
)

// SRCH-008 coverage: the evaluator's error paths and the parser's defensive
// clamps. Every node type in evalExpr calls a scorer and forwards its error,
// and none of those branches had a test — an evaluator that swallows a storage
// error returns an empty result set, which reads as "no matches" rather than
// "the query never ran".

// closedIndex returns an index whose database is shut, so every scorer beneath
// the evaluator fails. That is the only way to reach the forwarding branches
// without stubbing the scorers themselves.
func closedIndex(t *testing.T) *FTSIndex {
	t.Helper()
	idx := NewFTSIndex(openTestDB(t))
	if err := idx.EnsureBuckets(); err != nil {
		t.Fatalf("ensure FTS buckets: %v", err)
	}
	if err := idx.Index("c", "a", "rust systems programming"); err != nil {
		t.Fatalf("index: %v", err)
	}
	if err := idx.db.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	return idx
}

func TestEvaluateExpressionForwardsStorageErrors(t *testing.T) {
	// One query per node type, so a scorer that starts swallowing errors fails
	// here rather than in whichever caller notices the empty results first.
	queries := []string{
		"rust",                 // TermExpr
		"rust~1",               // FuzzyExpr
		`"rust systems"`,       // PhraseExpr
		`"rust systems"~3`,     // ProximityExpr
		"rus*",                 // WildcardExpr
		"rust AND systems",     // AndExpr, left
		"systems AND rust",     // AndExpr, right
		"rust OR systems",      // OrExpr, left
		"systems OR rust",      // OrExpr, right
		"rust AND NOT systems", // NotExpr
	}

	idx := closedIndex(t)
	for _, query := range queries {
		expr, err := ParseQueryExpression(query)
		if err != nil {
			t.Errorf("%s: parse: %v", query, err)
			continue
		}
		if _, err := idx.EvaluateExpression("c", expr, 0); err == nil {
			t.Errorf("%s: evaluated against a closed database without an error", query)
		}
	}
}

func TestEvaluateExpressionRejectsUnknownNode(t *testing.T) {
	idx := NewFTSIndex(openTestDB(t))
	if err := idx.EnsureBuckets(); err != nil {
		t.Fatalf("ensure FTS buckets: %v", err)
	}

	_, err := idx.EvaluateExpression("c", unknownExpr{}, 0)
	if err == nil || !strings.Contains(err.Error(), "unsupported expression type") {
		t.Fatalf("want an unsupported-type error, got %v", err)
	}
}

// unknownExpr is a QueryExpr the evaluator has never heard of. It stands in for
// a node added to the AST without a matching evaluator case.
type unknownExpr struct{}

func (unknownExpr) String() string { return "?" }
func (unknownExpr) exprNode()      {}

func TestEvaluateExpressionAppliesLimit(t *testing.T) {
	s, cleanup := newQueryExprServer(t)
	defer cleanup()
	indexExprDoc(t, s, "c", "a", "rust systems")
	indexExprDoc(t, s, "c", "b", "rust async")
	indexExprDoc(t, s, "c", "d", "rust embedded")

	expr, err := ParseQueryExpression("rust")
	if err != nil {
		t.Fatal(err)
	}
	results, err := s.EvaluateExpression("c", expr, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 {
		t.Fatalf("limit 2 returned %d results", len(results))
	}
}

// The tokenizer clamps out-of-range modifiers, but parseAtom clamps again in
// case a token reaches it another way. Driving the parser directly is the only
// way to reach those guards.
func TestParseAtomClampsModifiers(t *testing.T) {
	cases := []struct {
		name  string
		token token
		want  string
	}{
		{"negative fuzzy distance", token{typ: tokFuzzy, s: "color", n: -3}, "color~0"},
		{"excessive fuzzy distance", token{typ: tokFuzzy, s: "color", n: 9}, "color~2"},
		{"zero proximity distance", token{typ: tokPhrase, s: "rust async", n: 0}, `"rust async"`},
		{"negative proximity distance", token{typ: tokProximity, s: "rust async", n: -1}, `"rust async"~5`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := &parser{tokens: []token{tc.token, {typ: tokEOF}}}
			expr, err := p.parseAtom()
			if err != nil {
				t.Fatal(err)
			}
			if got := expr.String(); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestPeekPastTheEndIsEOF(t *testing.T) {
	p := &parser{tokens: nil}
	if got := p.peek(); got.typ != tokEOF {
		t.Fatalf("peek on an empty token list returned %v, want EOF", got.typ)
	}
}

func TestRequiredPrefixParsesAsAPlainAtom(t *testing.T) {
	expr, err := ParseQueryExpression("+rust +async")
	if err != nil {
		t.Fatal(err)
	}
	// `+` is redundant where adjacency already means AND, so it is dropped
	// rather than carried into the tree.
	if got := expr.String(); got != "(rust AND async)" {
		t.Fatalf("got %q, want %q", got, "(rust AND async)")
	}
}

func TestDedupeAppendDropsDuplicatesAndTheNegationMarker(t *testing.T) {
	got := dedupeAppend(
		[]string{"rust", "rust", negatedMarker, "async"},
		[]string{"async", negatedMarker, "embedded"},
	)
	want := []string{"rust", "async", "embedded"}

	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v (order of first appearance)", got, want)
		}
	}
}

func TestOrMergesScoresForADocumentOnBothSides(t *testing.T) {
	s, cleanup := newQueryExprServer(t)
	defer cleanup()
	indexExprDoc(t, s, "c", "both", "rust async runtime")
	indexExprDoc(t, s, "c", "left", "rust systems")

	expr, err := ParseQueryExpression("rust OR async")
	if err != nil {
		t.Fatal(err)
	}
	results, err := s.EvaluateExpression("c", expr, 0)
	if err != nil {
		t.Fatal(err)
	}

	var both, left FTSResult
	for _, r := range results {
		switch r.DocID {
		case "both":
			both = r
		case "left":
			left = r
		}
	}
	if both.DocID == "" || left.DocID == "" {
		t.Fatalf("both documents should match, got %+v", results)
	}
	// A document matching both sides sums the two scores, so it outranks one
	// that matched a single side.
	if both.Score <= left.Score {
		t.Fatalf("matching both sides scored %v, one side %v", both.Score, left.Score)
	}
	if len(both.MatchedTerms) != 2 {
		t.Fatalf("matched terms should merge, got %v", both.MatchedTerms)
	}
}
