package fts

import (
	"strings"
	"testing"
)

// TEST-003. The repository had no fuzz target at all. A query expression is
// the one string a user hands MDDB that gets *parsed* rather than tokenised,
// so it is the shortest path from untrusted input to a panic.
//
// The property is narrow and absolute: whatever comes in, ParseQueryExpression
// returns an expression or a typed error. It never panics, never hangs, and
// never returns both nil and nil.

func FuzzParseQueryExpression(f *testing.F) {
	// Seeds cover every construct the parser knows, because a fuzzer that has
	// to discover `~2` from random bytes will spend its whole budget there.
	seeds := []string{
		"",
		"hello",
		"hello world",
		"cat AND dog",
		"cat OR dog",
		"NOT cat",
		"(cat OR dog) AND NOT bird",
		`"exact phrase"`,
		`"near words"~5`,
		"fuzzy~2",
		"wild*",
		"pre?fix",
		"a AND (b OR (c AND NOT (d OR e)))",
		// Shapes that have broken hand-written parsers before.
		"(((((",
		")))))",
		`"unterminated`,
		"AND",
		"NOT",
		"a AND",
		"OR b",
		"~5",
		"term~",
		"term~999999999999999999999",
		"term~-1",
		`""`,
		`"~"`,
		"a~2~3",
		"NOT NOT NOT a",
		"* AND ?",
		strings.Repeat("a OR ", 50) + "b",
		strings.Repeat("(", 100) + "a" + strings.Repeat(")", 100),
	}
	for _, s := range seeds {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, q string) {
		expr, err := ParseQueryExpression(q)

		// Nil expression with nil error would make every caller's
		// `if err != nil` check useless and crash on the next dereference.
		if err == nil && expr == nil {
			t.Fatalf("query %q returned neither an expression nor an error", q)
		}
		if err != nil {
			return
		}

		// String() is how the parse is logged and cached; it must survive
		// whatever the parser produced.
		_ = expr.String()
	})
}

// FuzzQueryExpressionPrintReparses asserts that printing an expression is
// idempotent: String() → parse → String() gives the same text.
//
// This target originally asserted only the weaker "whatever String() prints,
// the parser accepts", because idempotence was false and the reason was a real
// limitation: the parser had no escape syntax, so a phrase containing a double
// quote could not be expressed at all — `"say \"hi\" now"` was silently read
// as three AND-ed fragments rather than rejected, and printing it produced a
// different expression. Filed as SRCH-008 and fixed there; the stronger
// assertion is what SRCH-008 asked to have restored.
func FuzzQueryExpressionPrintReparses(f *testing.F) {
	for _, s := range []string{"cat AND dog", `"phrase"~3`, "NOT (a OR b)", "term~2", "wild*"} {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, q string) {
		expr, err := ParseQueryExpression(q)
		if err != nil || expr == nil {
			return
		}
		printed := expr.String()

		// An empty print is the one thing that cannot re-parse, and would
		// mean String() lost the expression entirely.
		if printed == "" {
			t.Fatalf("String() printed nothing for a parsed expression (input %q)", q)
		}

		reparsed, err := ParseQueryExpression(printed)
		if err != nil {
			t.Fatalf("String() produced something the parser rejects:\n input:    %q\n printed:  %q\n error:    %v", q, printed, err)
		}
		if reparsed == nil {
			t.Fatalf("re-parsing %q returned no expression", printed)
		}

		// SRCH-008: the round trip must be stable. A second pass that differs
		// means String() and the parser disagree about the syntax, which is
		// how a logged query pasted back becomes a different query.
		if again := reparsed.String(); again != printed {
			t.Fatalf("printing is not idempotent:\n input:  %q\n first:  %q\n second: %q", q, printed, again)
		}
	})
}
