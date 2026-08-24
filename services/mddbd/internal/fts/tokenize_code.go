package fts

import (
	"strings"
	"unicode"
)

// Code-aware tokenisation (CODE-001).
//
// The prose tokeniser is wrong for source: it stems, so `classes` becomes
// `class`; it drops stop words, so `for`, `if` and `class` disappear from a
// language where they are the point; and it splits on every non-alphanumeric
// character, so `.hero-banner` survives only as two unrelated words and the
// selector itself becomes unfindable.
//
// TokenizeCode keeps the whole identifier *and* emits its parts, so a document
// indexed this way answers both `hero-banner` and `hero`. That matters beyond
// recall: it means a collection of code can be searched by queries tokenised
// the ordinary way, so nothing else in the search path has to know.
//
// The one asymmetry left is stemming on the query side: a search for `classes`
// stems to `class` and will not match the unstemmed `classes` in a code index.
// Searching code for an English plural is rare enough to accept, and the
// alternative — tokenising every query twice — would blur prose results.

// codeMinToken is the shortest token worth indexing. One character is kept
// here, unlike prose: `a` is a real CSS selector and `x`/`y` are real
// identifiers.
const codeMinToken = 1

// TokenizeCode splits source text into searchable terms.
//
// For each run of identifier characters it emits the whole run, then its parts
// where the run is camelCase, snake_case or kebab-case. Digits stay attached
// (`h2`, `utf8`), because splitting them produces noise rather than meaning.
func TokenizeCode(text string) map[string]int {
	terms := make(map[string]int)

	var cur strings.Builder
	flush := func() {
		if cur.Len() == 0 {
			return
		}
		raw := cur.String()
		cur.Reset()

		whole := strings.ToLower(raw)
		if len(whole) >= codeMinToken {
			terms[whole]++
		}
		// Parts are only interesting when they differ from the whole.
		for _, part := range splitIdentifier(raw) {
			p := strings.ToLower(part)
			if len(p) >= codeMinToken && p != whole {
				terms[p]++
			}
		}
	}

	for _, r := range text {
		// `-` and `_` join an identifier rather than ending it: kebab-case and
		// snake_case names are single things that also have parts.
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '-' {
			cur.WriteRune(r)
			continue
		}
		flush()
	}
	flush()

	return terms
}

// splitIdentifier breaks a name into its parts across the three conventions
// source code actually uses. It returns nothing when there is nothing to split.
//
//	heroBanner   -> hero, banner
//	hero-banner  -> hero, banner
//	HERO_BANNER  -> hero, banner
//	XMLHttpRequest -> xml, http, request
func splitIdentifier(s string) []string {
	// Separator-delimited first; each piece may still be camelCase.
	var pieces []string
	for _, piece := range strings.FieldsFunc(s, func(r rune) bool { return r == '_' || r == '-' }) {
		pieces = append(pieces, splitCamel(piece)...)
	}
	if len(pieces) <= 1 {
		return nil
	}
	return pieces
}

// splitCamel breaks camelCase and PascalCase, keeping acronym runs together:
// XMLHttpRequest yields XML, Http, Request rather than X, M, L, ...
func splitCamel(s string) []string {
	if s == "" {
		return nil
	}
	runes := []rune(s)
	var out []string
	start := 0
	for i := 1; i < len(runes); i++ {
		prev, curr := runes[i-1], runes[i]
		boundary := false
		switch {
		case unicode.IsLower(prev) && unicode.IsUpper(curr):
			// heroBanner -> hero | Banner
			boundary = true
		case unicode.IsUpper(prev) && unicode.IsUpper(curr) &&
			i+1 < len(runes) && unicode.IsLower(runes[i+1]):
			// XMLHttp -> XML | Http
			boundary = true
		}
		if boundary {
			out = append(out, string(runes[start:i]))
			start = i
		}
	}
	out = append(out, string(runes[start:]))
	if len(out) == 1 {
		return out
	}
	return out
}

// TokenizePositionsCode is TokenizeCode with positions, for phrase search over
// source. Only whole identifiers carry positions: a part has no position of
// its own, and inventing one would make phrase search report matches that are
// not there.
func (f *FTSIndex) TokenizePositionsCode(text string) map[string][]uint32 {
	positions := make(map[string][]uint32)

	var cur strings.Builder
	var index uint32
	flush := func() {
		if cur.Len() == 0 {
			return
		}
		whole := strings.ToLower(cur.String())
		cur.Reset()
		if len(whole) >= codeMinToken {
			positions[whole] = append(positions[whole], index)
			index++
		}
	}

	for _, r := range text {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '-' {
			cur.WriteRune(r)
			continue
		}
		flush()
	}
	flush()

	return positions
}
