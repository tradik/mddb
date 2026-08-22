package fts

import (
	"errors"
	"testing"
)

// --- Tokenizer ---

// --- Parser ---

func TestParseQueryExpression_Precedence(t *testing.T) {
	// AND binds tighter than OR: `a AND b OR c` parses as (a AND b) OR c.
	cases := []struct {
		in    string
		wantS string
	}{
		{"a", "a"},
		{"a AND b", "(a AND b)"},
		{"a OR b", "(a OR b)"},
		{"a b c", "((a AND b) AND c)"}, // implicit AND
		{"a AND b OR c", "((a AND b) OR c)"},
		{"a OR b AND c", "(a OR (b AND c))"},
		{"(a OR b) AND c", "((a OR b) AND c)"},
		{"a AND (b OR c) AND d", "((a AND (b OR c)) AND d)"},
		{"NOT a", "NOT a"},
		{"a AND NOT b", "(a AND NOT b)"},
		{"a AND -b", "(a AND NOT b)"},
		{`a AND "b c"`, `(a AND "b c")`},
		{`a AND "b c"~3`, `(a AND "b c"~3)`},
		{"a~1", "a~1"},
		{"mark*", "mark*"},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			expr, err := ParseQueryExpression(tc.in)
			if err != nil {
				t.Fatalf("parse error: %v", err)
			}
			got := expr.String()
			if got != tc.wantS {
				t.Errorf("\n  got  %s\n  want %s", got, tc.wantS)
			}
		})
	}
}

func TestParseQueryExpression_Errors(t *testing.T) {
	cases := []string{
		"(",
		"a AND",
		"(a OR b",
		"a )",
	}
	for _, q := range cases {
		t.Run(q, func(t *testing.T) {
			_, err := ParseQueryExpression(q)
			if err == nil {
				t.Error("expected parse error")
			}
		})
	}
}

func TestParseQueryExpression_EmptyIsAnError(t *testing.T) {
	// This used to return (nil, nil), which is a trap: a caller writing the
	// obvious error check then dereferences a nil expression. Changed to a
	// typed error after FuzzParseQueryExpression flagged the contract
	// (TEST-003).
	for _, q := range []string{"", "   ", "\t\n "} {
		expr, err := ParseQueryExpression(q)
		if !errors.Is(err, ErrEmptyQueryExpression) {
			t.Errorf("ParseQueryExpression(%q) error = %v, want ErrEmptyQueryExpression", q, err)
		}
		if expr != nil {
			t.Errorf("ParseQueryExpression(%q) returned an expression alongside the error: %v", q, expr)
		}
	}
}

func TestParseQueryExpression_FuzzyDistanceClamped(t *testing.T) {
	expr, err := ParseQueryExpression("term~9")
	if err != nil {
		t.Fatal(err)
	}
	f, ok := expr.(*FuzzyExpr)
	if !ok {
		t.Fatalf("expected FuzzyExpr, got %T", expr)
	}
	if f.Distance != 2 {
		t.Errorf("distance should clamp to 2, got %d", f.Distance)
	}
}

// --- Evaluator (integration over a real FTS index) ---

// newQueryExprServer prepares an FTSIndex so evaluator tests can seed a
// small corpus and exercise AND / OR / NOT / phrase semantics end-to-end.
func newQueryExprServer(t *testing.T) (*FTSIndex, func()) {
	t.Helper()
	idx := NewFTSIndex(openTestDB(t))
	if err := idx.EnsureBuckets(); err != nil {
		t.Fatalf("ensure FTS buckets: %v", err)
	}
	return idx, func() {}
}

// indexExprDoc wires both the flat and positional indices so phrase and
// proximity clauses in the evaluator have something to match against.
func indexExprDoc(t *testing.T, s *FTSIndex, collection, docID, content string) {
	t.Helper()
	if err := s.Index(collection, docID, content); err != nil {
		t.Fatalf("index: %v", err)
	}
	if err := s.IndexPositions(collection, docID, content); err != nil {
		t.Fatalf("index positions: %v", err)
	}
}

func TestEvaluateExpression_And(t *testing.T) {
	s, cleanup := newQueryExprServer(t)
	defer cleanup()
	indexExprDoc(t, s, "c", "a", "rust systems programming")
	indexExprDoc(t, s, "c", "b", "rust async runtime")
	indexExprDoc(t, s, "c", "c", "golang concurrency")

	expr, err := ParseQueryExpression("rust AND systems")
	if err != nil {
		t.Fatal(err)
	}
	res, err := s.EvaluateExpression("c", expr, 10)
	if err != nil {
		t.Fatal(err)
	}
	// Only doc "a" has both terms.
	if len(res) != 1 || res[0].DocID != "a" {
		t.Errorf("expected [a], got %v", docIDsFromFTS(res))
	}
}

func TestEvaluateExpression_Or(t *testing.T) {
	s, cleanup := newQueryExprServer(t)
	defer cleanup()
	indexExprDoc(t, s, "c", "a", "rust systems programming")
	indexExprDoc(t, s, "c", "b", "golang concurrency")
	indexExprDoc(t, s, "c", "c", "python async")

	expr, _ := ParseQueryExpression("rust OR golang")
	res, _ := s.EvaluateExpression("c", expr, 10)
	ids := docIDsFromFTS(res)
	if len(ids) != 2 {
		t.Errorf("expected 2 docs, got %v", ids)
	}
}

func TestEvaluateExpression_Not(t *testing.T) {
	s, cleanup := newQueryExprServer(t)
	defer cleanup()
	indexExprDoc(t, s, "c", "a", "rust systems good")
	indexExprDoc(t, s, "c", "b", "rust spam bad")
	indexExprDoc(t, s, "c", "c", "rust perfect")

	expr, _ := ParseQueryExpression("rust AND NOT spam")
	res, _ := s.EvaluateExpression("c", expr, 10)
	ids := docIDsFromFTS(res)
	// "a" and "c" match rust but not spam; "b" is excluded.
	if len(ids) != 2 {
		t.Errorf("expected 2 docs, got %v", ids)
	}
	for _, id := range ids {
		if id == "b" {
			t.Errorf("doc b should be excluded by NOT spam")
		}
	}
}

func TestEvaluateExpression_NestedGrouping(t *testing.T) {
	s, cleanup := newQueryExprServer(t)
	defer cleanup()
	indexExprDoc(t, s, "c", "a", "rust systems")
	indexExprDoc(t, s, "c", "b", "golang concurrency")
	indexExprDoc(t, s, "c", "c", "python async")

	// (rust OR golang) AND (systems OR concurrency)
	// Matches: a (rust + systems), b (golang + concurrency). Not c.
	expr, err := ParseQueryExpression("(rust OR golang) AND (systems OR concurrency)")
	if err != nil {
		t.Fatal(err)
	}
	res, _ := s.EvaluateExpression("c", expr, 10)
	ids := docIDsFromFTS(res)
	if len(ids) != 2 {
		t.Errorf("expected 2 docs, got %v", ids)
	}
	for _, id := range ids {
		if id == "c" {
			t.Errorf("doc c should not match — has neither (rust|golang) nor (systems|concurrency)")
		}
	}
}

func TestEvaluateExpression_PhraseAtom(t *testing.T) {
	s, cleanup := newQueryExprServer(t)
	defer cleanup()
	indexExprDoc(t, s, "c", "a", "machine learning algorithms")
	indexExprDoc(t, s, "c", "b", "learning to use machine")

	expr, _ := ParseQueryExpression(`"machine learning"`)
	res, _ := s.EvaluateExpression("c", expr, 10)
	ids := docIDsFromFTS(res)
	if len(ids) != 1 || ids[0] != "a" {
		t.Errorf("expected [a] only, got %v", ids)
	}
}

func TestEvaluateExpression_EmptyReturnsNothing(t *testing.T) {
	s, cleanup := newQueryExprServer(t)
	defer cleanup()
	res, err := s.EvaluateExpression("c", nil, 10)
	if err != nil || res != nil {
		t.Errorf("nil AST should yield (nil, nil), got (%v, %v)", res, err)
	}
}

// docIDsFromFTS collects ids so assertions can eyeball the matched set.
func docIDsFromFTS(res []FTSResult) []string {
	out := make([]string, len(res))
	for i, r := range res {
		out[i] = r.DocID
	}
	return out
}

func TestEvaluateExpression_FuzzyProximityWildcard(t *testing.T) {
	s, cleanup := newQueryExprServer(t)
	defer cleanup()
	indexExprDoc(t, s, "c", "d1", "machine learning algorithms")
	indexExprDoc(t, s, "c", "d2", "deep learning networks")

	cases := []QueryExpr{
		&FuzzyExpr{Term: "learnin", Distance: 2},
		&ProximityExpr{Phrase: "machine algorithms", Distance: 3},
		&WildcardExpr{Pattern: "learn*"},
		&AndExpr{Left: &TermExpr{Term: "learning"}, Right: &NotExpr{Inner: &WildcardExpr{Pattern: "deep*"}}},
		&OrExpr{Left: &FuzzyExpr{Term: "machin", Distance: 1}, Right: &PhraseExpr{Phrase: "deep learning"}},
	}
	for i, expr := range cases {
		if _, err := s.EvaluateExpression("c", expr, 10); err != nil {
			t.Fatalf("case %d: %v", i, err)
		}
	}
}
