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

// FuzzQueryExpressionPrintReparses asserts the property the code actually
// promises: whatever String() prints, the parser accepts.
//
// It deliberately does NOT assert that printing is idempotent. It is not, and
// the reason is a real limitation rather than a bug in String(): the parser has
// no escape syntax, so a phrase containing a double quote cannot be expressed
// at all — `"say \"hi\" now"` is silently read as three AND-ed fragments
// rather than rejected. Printing such a phrase and re-reading it therefore
// yields a different expression. Found here, filed as SRCH-008; asserting
// idempotence would just fail forever without fixing anything.
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
	})
}
