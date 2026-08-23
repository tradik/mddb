package fts

import (
	"errors"
	"strings"
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

// SRCH-008. A phrase containing a quote was not rejected — it was silently
// parsed as several fragments joined by an implicit AND, so the caller got
// results for a different query with no way to notice. Silent
// reinterpretation is worse than an error, because the results look
// plausible and nobody checks whether they answer the question asked.

func TestPhraseCanContainAnEscapedQuote(t *testing.T) {
	expr, err := ParseQueryExpression(`"say \"hi\" now"`)
	if err != nil {
		t.Fatalf("a phrase with escaped quotes was rejected: %v", err)
	}

	phrase, ok := expr.(*PhraseExpr)
	if !ok {
		t.Fatalf("parsed as %T, want a single phrase — the old behaviour split it into three fragments", expr)
	}
	if phrase.Phrase != `say "hi" now` {
		t.Errorf("phrase = %q, want the quotes literal", phrase.Phrase)
	}
}

func TestPhraseCanContainAnEscapedBackslash(t *testing.T) {
	expr, err := ParseQueryExpression(`"a \\ b"`)
	if err != nil {
		t.Fatal(err)
	}
	phrase, ok := expr.(*PhraseExpr)
	if !ok {
		t.Fatalf("parsed as %T", expr)
	}
	if phrase.Phrase != `a \ b` {
		t.Errorf("phrase = %q, want a single backslash", phrase.Phrase)
	}
}

// A backslash before anything else stays a backslash. Windows paths and
// regex-looking terms are common in the corpora this searches, and inventing a
// meaning for `\d` would break them.
func TestABackslashBeforeAnythingElseIsLiteral(t *testing.T) {
	expr, err := ParseQueryExpression(`"C:\Users\name and \d+"`)
	if err != nil {
		t.Fatal(err)
	}
	phrase := expr.(*PhraseExpr)
	if phrase.Phrase != `C:\Users\name and \d+` {
		t.Errorf("phrase = %q, want the backslashes preserved", phrase.Phrase)
	}
}

// The real-world case from the ticket: API documentation full of quoted values.
func TestAPIDocumentationPhraseParses(t *testing.T) {
	expr, err := ParseQueryExpression(`"Content-Type: \"application/json\""`)
	if err != nil {
		t.Fatalf("an API-documentation phrase was rejected: %v", err)
	}
	phrase, ok := expr.(*PhraseExpr)
	if !ok {
		t.Fatalf("parsed as %T, not a single phrase", expr)
	}
	if phrase.Phrase != `Content-Type: "application/json"` {
		t.Errorf("phrase = %q", phrase.Phrase)
	}
}

func TestUnterminatedPhraseIsAnError(t *testing.T) {
	// This used to parse as something else entirely, which is the worst
	// outcome: a different query, answered confidently.
	for _, q := range []string{`"unterminated`, `hello "world`, `"a \"b`} {
		if _, err := ParseQueryExpression(q); err == nil {
			t.Errorf("%q was accepted", q)
		} else if !strings.Contains(err.Error(), "unterminated") {
			t.Errorf("%q: error does not name the problem: %v", q, err)
		}
	}
}

// String() must produce something the parser reads back unchanged. It used to
// use %q, which escapes control characters as \x03 — a sequence this parser
// does not understand — so printing a phrase produced a query that parsed
// differently.
func TestPhrasePrintsBackToItself(t *testing.T) {
	phrases := []string{
		`plain phrase`,
		`say "hi" now`,
		`a \ backslash`,
		`Content-Type: "application/json"`,
		`C:\Users\name`,
		`quote at the end "`,
		`"quote at the start`,
		`\`,
		`""`,
	}

	for _, p := range phrases {
		printed := (&PhraseExpr{Phrase: p}).String()

		reparsed, err := ParseQueryExpression(printed)
		if err != nil {
			t.Errorf("%q printed as %s, which does not parse: %v", p, printed, err)
			continue
		}
		got, ok := reparsed.(*PhraseExpr)
		if !ok {
			t.Errorf("%q printed as %s, which parses as %T", p, printed, reparsed)
			continue
		}
		if got.Phrase != p {
			t.Errorf("round trip changed the phrase:\n  in:      %q\n  printed: %s\n  out:     %q", p, printed, got.Phrase)
		}
	}
}

func TestProximityPrintsBackToItself(t *testing.T) {
	original := &ProximityExpr{Phrase: `say "hi"`, Distance: 5}

	reparsed, err := ParseQueryExpression(original.String())
	if err != nil {
		t.Fatalf("%s does not parse: %v", original.String(), err)
	}
	got, ok := reparsed.(*ProximityExpr)
	if !ok {
		t.Fatalf("parsed as %T", reparsed)
	}
	if got.Phrase != original.Phrase || got.Distance != original.Distance {
		t.Errorf("round trip = %q~%d, want %q~%d", got.Phrase, got.Distance, original.Phrase, original.Distance)
	}
}

// Control characters are written literally rather than escaped: a phrase is
// matched against document text, where those bytes appear as themselves.
func TestControlCharactersSurviveTheRoundTrip(t *testing.T) {
	for _, p := range []string{"\x03", "a\tb", "line\nbreak"} {
		printed := (&PhraseExpr{Phrase: p}).String()
		reparsed, err := ParseQueryExpression(printed)
		if err != nil {
			t.Errorf("%q printed as %q, which does not parse: %v", p, printed, err)
			continue
		}
		if got := reparsed.(*PhraseExpr).Phrase; got != p {
			t.Errorf("round trip changed %q into %q", p, got)
		}
	}
}

// SRCH-008, found by the fuzzer once idempotence was asserted again: the
// parser collapsed adjacent NOTs but not parenthesised ones, so `NOT NOT x`
// and `NOT(NOT x)` — the same query — parsed to different trees.
func TestDoubleNegationCollapsesWhicheverWayItIsWritten(t *testing.T) {
	cases := map[string]string{
		"NOT term":           "NOT term",
		"NOT NOT term":       "term",
		"NOT(NOT term)":      "term",
		"NOT (NOT term)":     "term",
		"NOT NOT NOT term":   "NOT term",
		"NOT(NOT(NOT term))": "NOT term",
	}

	for query, want := range cases {
		expr, err := ParseQueryExpression(query)
		if err != nil {
			t.Errorf("%s: %v", query, err)
			continue
		}
		if got := expr.String(); got != want {
			t.Errorf("%s → %q, want %q", query, got, want)
		}
	}
}
