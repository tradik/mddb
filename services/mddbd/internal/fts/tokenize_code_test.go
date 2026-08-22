package fts

import (
	"sort"
	"strings"
	"testing"
)

// CODE-001. The prose tokeniser stems, drops stop words and splits on every
// punctuation mark, which loses exactly what makes source searchable. These
// pin what the code tokeniser keeps.

func keysOf(m map[string]int) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func has(t *testing.T, terms map[string]int, want ...string) {
	t.Helper()
	for _, w := range want {
		if _, ok := terms[w]; !ok {
			t.Errorf("missing term %q; got %v", w, keysOf(terms))
		}
	}
}

func TestCodeTokenizerKeepsTheWholeIdentifierAndItsParts(t *testing.T) {
	// A selector must be findable by its own name and by either word in it —
	// an agent asked "where is the hero banner styled?" has neither spelling.
	has(t, TokenizeCode(".hero-banner { color: red; }"), "hero-banner", "hero", "banner", "color", "red")
	has(t, TokenizeCode("function checkoutHandler() {}"), "checkouthandler", "checkout", "handler", "function")
	has(t, TokenizeCode("MAX_RETRY_COUNT = 3"), "max_retry_count", "max", "retry", "count")
}

func TestCodeTokenizerKeepsAcronymsTogether(t *testing.T) {
	terms := TokenizeCode("class XMLHttpRequest {}")
	has(t, terms, "xmlhttprequest", "xml", "http", "request")
	for _, noise := range []string{"x", "m", "l"} {
		if _, ok := terms[noise]; ok {
			t.Errorf("an acronym was split letter by letter: %q in %v", noise, keysOf(terms))
		}
	}
}

// Stop words are language keywords here; dropping them makes a codebase
// unsearchable for the thing being looked for.
func TestCodeTokenizerKeepsKeywords(t *testing.T) {
	has(t, TokenizeCode("for (const item of items) { if (item) return item; }"),
		"for", "const", "of", "if", "return")
}

func TestCodeTokenizerDoesNotStem(t *testing.T) {
	terms := TokenizeCode("const classes = getClasses()")
	has(t, terms, "classes", "getclasses")
	// The prose tokeniser would reduce both to "class"/"getclass".
	if _, ok := terms["class"]; ok {
		t.Errorf("the tokeniser stemmed a plural identifier: %v", keysOf(terms))
	}
}

// Single characters are real in source: `a` is an anchor selector.
func TestCodeTokenizerKeepsSingleCharacterTokens(t *testing.T) {
	has(t, TokenizeCode("a { text-decoration: none; } .x { top: 0 }"), "a", "x")
}

func TestCodeTokenizerKeepsDigitsAttached(t *testing.T) {
	has(t, TokenizeCode("h2 { font-size: 1rem } const utf8 = true"), "h2", "utf8")
	for _, noise := range []string{"2", "8"} {
		if _, ok := TokenizeCode("h2 utf8")[noise]; ok {
			t.Errorf("a digit was split off its identifier: %q", noise)
		}
	}
}

func TestCodeTokenizerCounts(t *testing.T) {
	terms := TokenizeCode("margin margin padding")
	if terms["margin"] != 2 || terms["padding"] != 1 {
		t.Errorf("term counts wrong: %v", terms)
	}
}

func TestCodeTokenizerHandlesEmptyAndPunctuationOnly(t *testing.T) {
	for _, in := range []string{"", "   ", "{};:,()", "\n\t"} {
		if got := TokenizeCode(in); len(got) != 0 {
			t.Errorf("TokenizeCode(%q) = %v, want nothing", in, got)
		}
	}
}

func TestSplitIdentifierReturnsNothingWhenThereIsNothingToSplit(t *testing.T) {
	for _, in := range []string{"banner", "BANNER", "x", ""} {
		if got := splitIdentifier(in); got != nil {
			t.Errorf("splitIdentifier(%q) = %v, want nil", in, got)
		}
	}
}

func TestSplitIdentifierAcrossConventions(t *testing.T) {
	for in, want := range map[string]string{
		"heroBanner":     "hero,Banner",
		"hero-banner":    "hero,banner",
		"hero_banner":    "hero,banner",
		"HERO_BANNER":    "HERO,BANNER",
		"XMLHttpRequest": "XML,Http,Request",
		"data-test-id":   "data,test,id",
	} {
		got := strings.Join(splitIdentifier(in), ",")
		if got != want {
			t.Errorf("splitIdentifier(%q) = %q, want %q", in, got, want)
		}
	}
}

// Positions drive phrase search; only whole identifiers get one, because a
// part has no position of its own.
func TestCodeTokenizerPositions(t *testing.T) {
	idx := newBulkTestIndex(t)
	// Tokens in order: margin-top(0) 0(1) margin-bottom(2) 4px(3) — values are
	// tokens too, since a number is as searchable as a name in source.
	pos := idx.TokenizePositionsCode("margin-top: 0; margin-bottom: 4px")

	if got := pos["margin-top"]; len(got) != 1 || got[0] != 0 {
		t.Errorf("margin-top positions = %v, want [0]", got)
	}
	if got := pos["margin-bottom"]; len(got) != 1 || got[0] != 2 {
		t.Errorf("margin-bottom positions = %v, want [2]", got)
	}
	if got := pos["4px"]; len(got) != 1 || got[0] != 3 {
		t.Errorf("4px positions = %v, want [3]", got)
	}
	if _, ok := pos["margin"]; ok {
		t.Error("a split part should not carry a position of its own")
	}
}

// The property the whole design rests on: a code document stays findable by a
// query tokenised the ordinary way, so the search path needs no change.
func TestCodeIndexIsReachableByProseQueries(t *testing.T) {
	idx := newBulkTestIndex(t)
	css := ".hero-banner { background: url(hero.png); }\n.checkoutButton { color: red; }"
	if err := idx.IndexDocs("theme", []BulkDoc{
		{DocID: "css/style.css", Content: css, Lang: "en", Kind: "code"},
	}); err != nil {
		t.Fatal(err)
	}

	for _, query := range []string{"hero", "banner", "hero-banner", "checkout", "checkoutButton", "background"} {
		res, err := idx.Search("theme", query, 10)
		if err != nil {
			t.Fatal(err)
		}
		if len(res) == 0 {
			t.Errorf("query %q found nothing in a code document", query)
		}
	}
}
