package fts

import (
	"errors"
	"fmt"
	"strings"
	"unicode"
)

// --- Tokenizer ---

// tokenType enumerates the lexical categories emitted by the tokenizer.
// Token stream is consumed by a recursive-descent parser below.
type tokenType int

const (
	tokEOF tokenType = iota
	tokTerm
	tokPhrase    // "quoted phrase" — no distance modifier
	tokProximity // "quoted phrase"~N — phrase plus explicit word distance
	tokWildcard  // term containing * or ?
	tokFuzzy     // bare term with trailing ~N edit-distance modifier
	tokLParen    // (
	tokRParen    // )
	tokAnd       // AND
	tokOr        // OR
	tokNot       // NOT or leading -
	tokRequire   // leading +
)

// token pairs a category with the payload the parser will need later. For
// proximity and fuzzy, `n` carries the integer modifier; other categories
// ignore it.
type token struct {
	typ tokenType
	s   string
	n   int
}

// tokenize splits a raw query string into tokens. It is deliberately
// permissive: malformed phrases (missing closing quote) consume to EOL
// rather than returning an error, because search UX expects best-effort
// parsing — a user mid-typing "machine learn should still get hits.
// tokenize splits a query into tokens.
//
// Returns an error where the input cannot be tokenised at all — today only an
// unterminated phrase (SRCH-008). Everything else the tokenizer meets has a
// reading, and giving it one is better than refusing a query a person typed.
func tokenize(q string) ([]token, error) {
	var out []token
	runes := []rune(q)
	i := 0
	for i < len(runes) {
		r := runes[i]
		switch {
		case unicode.IsSpace(r):
			i++
		case r == '(':
			out = append(out, token{typ: tokLParen})
			i++
		case r == ')':
			out = append(out, token{typ: tokRParen})
			i++
		case r == '+':
			out = append(out, token{typ: tokRequire})
			i++
		case r == '-':
			// Standalone '-' before a term/group means NOT; inside a term
			// it's treated as part of the term (hyphenated words).
			if i+1 < len(runes) && !unicode.IsSpace(runes[i+1]) && runes[i+1] != '-' {
				out = append(out, token{typ: tokNot})
				i++
			} else {
				i++
			}
		case r == '"':
			// SRCH-008: a phrase containing a quote used to be unexpressible.
			// `"say \"hi\" now"` was not rejected — it was silently parsed as
			// three fragments joined by an implicit AND, so the caller got
			// results for a different query with no way to notice.
			//
			// The escape syntax is deliberately two sequences and no more:
			// `\"` is a literal quote, `\\` is a literal backslash. A
			// backslash before anything else stays a backslash, because
			// Windows paths and regex-looking terms are common in the corpora
			// this searches and inventing meanings for `\d` would break them.
			i++
			var phrase strings.Builder
			terminated := false
			for i < len(runes) {
				if runes[i] == '\\' && i+1 < len(runes) &&
					(runes[i+1] == '"' || runes[i+1] == '\\') {
					phrase.WriteRune(runes[i+1])
					i += 2
					continue
				}
				if runes[i] == '"' {
					terminated = true
					break
				}
				phrase.WriteRune(runes[i])
				i++
			}
			if !terminated {
				// An unterminated phrase used to parse as something else
				// entirely. Reporting it is the only way a caller finds out.
				return nil, fmt.Errorf("unterminated phrase: expected a closing quote")
			}
			i++ // consume closing quote
			// Check for proximity suffix: "..."~N
			if i < len(runes) && runes[i] == '~' {
				i++
				n, consumed := readInt(runes[i:])
				i += consumed
				out = append(out, token{typ: tokProximity, s: phrase.String(), n: n})
			} else {
				out = append(out, token{typ: tokPhrase, s: phrase.String()})
			}
		default:
			// Bare word: read until whitespace / special char.
			start := i
			for i < len(runes) {
				rr := runes[i]
				if unicode.IsSpace(rr) || rr == '(' || rr == ')' || rr == '"' {
					break
				}
				i++
			}
			word := string(runes[start:i])
			if word == "" {
				continue
			}
			if kw := keywordToken(word); kw.typ != tokEOF {
				out = append(out, kw)
				continue
			}
			// Split off trailing ~N (fuzzy) if present.
			if idx := strings.LastIndexByte(word, '~'); idx > 0 && idx < len(word)-1 {
				n, _ := readInt([]rune(word[idx+1:]))
				if n >= 0 {
					out = append(out, token{typ: tokFuzzy, s: word[:idx], n: n})
					continue
				}
			}
			if strings.ContainsAny(word, "*?") {
				out = append(out, token{typ: tokWildcard, s: word})
				continue
			}
			out = append(out, token{typ: tokTerm, s: word})
		}
	}
	out = append(out, token{typ: tokEOF})
	return out, nil
}

// keywordToken recognizes reserved words case-insensitively. Returns a
// tokEOF sentinel when the word is a plain term.
func keywordToken(word string) token {
	switch strings.ToUpper(word) {
	case "AND":
		return token{typ: tokAnd}
	case "OR":
		return token{typ: tokOr}
	case "NOT":
		return token{typ: tokNot}
	}
	return token{typ: tokEOF}
}

// readInt consumes leading decimal digits and returns (value, consumed).
// Returns (-1, 0) when the input doesn't start with a digit.
func readInt(runes []rune) (int, int) {
	if len(runes) == 0 || !unicode.IsDigit(runes[0]) {
		return -1, 0
	}
	n := 0
	i := 0
	for i < len(runes) && unicode.IsDigit(runes[i]) {
		n = n*10 + int(runes[i]-'0')
		i++
	}
	return n, i
}

// --- AST ---

// QueryExpr is the common interface for every node in the query AST.
// String() returns a canonical form used in tests and debug logs — stable
// across implementation changes so tests don't have to mirror internal node
// layout.
type QueryExpr interface {
	String() string
	exprNode()
}

// AndExpr is a left-associative AND of two sub-expressions.
type AndExpr struct{ Left, Right QueryExpr }

// OrExpr is a left-associative OR of two sub-expressions.
type OrExpr struct{ Left, Right QueryExpr }

// NotExpr negates its inner expression. Top-level NOT is not allowed (the
// parser rewrites `NOT x` in isolation into `MATCH_ALL AND NOT x`).
type NotExpr struct{ Inner QueryExpr }

// TermExpr is a simple word match.
type TermExpr struct{ Term string }

// FuzzyExpr is a word with edit-distance tolerance (Levenshtein).
type FuzzyExpr struct {
	Term     string
	Distance int
}

// PhraseExpr matches an exact consecutive sequence of words.
type PhraseExpr struct{ Phrase string }

// ProximityExpr matches a phrase within N words.
type ProximityExpr struct {
	Phrase   string
	Distance int
}

// WildcardExpr matches a pattern using * (any) and ? (single).
type WildcardExpr struct{ Pattern string }

func (a *AndExpr) exprNode()       {}
func (o *OrExpr) exprNode()        {}
func (n *NotExpr) exprNode()       {}
func (t *TermExpr) exprNode()      {}
func (f *FuzzyExpr) exprNode()     {}
func (p *PhraseExpr) exprNode()    {}
func (p *ProximityExpr) exprNode() {}
func (w *WildcardExpr) exprNode()  {}

func (a *AndExpr) String() string    { return "(" + a.Left.String() + " AND " + a.Right.String() + ")" }
func (o *OrExpr) String() string     { return "(" + o.Left.String() + " OR " + o.Right.String() + ")" }
func (n *NotExpr) String() string    { return "NOT " + n.Inner.String() }
func (t *TermExpr) String() string   { return t.Term }
func (f *FuzzyExpr) String() string  { return fmt.Sprintf("%s~%d", f.Term, f.Distance) }
func (p *PhraseExpr) String() string { return quotePhrase(p.Phrase) }
func (p *ProximityExpr) String() string {
	return fmt.Sprintf("%s~%d", quotePhrase(p.Phrase), p.Distance)
}

// quotePhrase renders a phrase so the parser reads it back unchanged.
//
// Not %q: Go's quoting escapes control characters as \x03 and \n, sequences
// this parser does not understand, so String() produced output it could not
// re-parse. Only the two characters the escape syntax defines are escaped, and
// everything else — including control characters — is written literally,
// because a phrase is matched against document text where those bytes appear
// as themselves.
func quotePhrase(phrase string) string {
	var b strings.Builder
	b.Grow(len(phrase) + 2)
	b.WriteByte('"')
	for _, r := range phrase {
		if r == '"' || r == '\\' {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	b.WriteByte('"')
	return b.String()
}
func (w *WildcardExpr) String() string { return w.Pattern }

// --- Parser ---

// parser is a recursive-descent driver over the token stream. It is single
// use: construct, call Parse, discard.
type parser struct {
	tokens []token
	pos    int
}

// ParseQueryExpression parses a query string into an AST with proper operator
// precedence (NOT > AND > OR) and parenthesized grouping. Adjacent terms
// without an explicit operator are joined with AND (Lucene-style default).
// Returns nil on empty input.
// ErrEmptyQueryExpression is returned for a query that is empty once trimmed.
//
// It exists because the function used to return (nil, nil) there, which is a
// trap: a caller writing the obvious `expr, err := Parse(...); if err != nil`
// then dereferences a nil expression. The one caller in this repository
// happened to guard it; the next one would not. Found by FuzzParseQueryExpression.
var ErrEmptyQueryExpression = errors.New("empty query expression")

// ParseQueryExpression parses a boolean query expression.
//
// It returns either a non-nil expression and a nil error, or a nil expression
// and a non-nil error — never both nil.
func ParseQueryExpression(q string) (QueryExpr, error) {
	q = strings.TrimSpace(q)
	if q == "" {
		return nil, ErrEmptyQueryExpression
	}
	tokens, err := tokenize(q)
	if err != nil {
		return nil, err
	}
	p := &parser{tokens: tokens}
	expr, err := p.parseOr()
	if err != nil {
		return nil, err
	}
	if p.peek().typ != tokEOF {
		return nil, fmt.Errorf("unexpected token %q at position %d", p.peek().s, p.pos)
	}
	return expr, nil
}

func (p *parser) peek() token {
	if p.pos >= len(p.tokens) {
		return token{typ: tokEOF}
	}
	return p.tokens[p.pos]
}

func (p *parser) advance() token {
	t := p.peek()
	p.pos++
	return t
}

// parseOr handles the lowest-precedence operator. Grammar:
//
//	or  := and ( "OR" and )*
func (p *parser) parseOr() (QueryExpr, error) {
	left, err := p.parseAnd()
	if err != nil {
		return nil, err
	}
	for p.peek().typ == tokOr {
		p.advance()
		right, err := p.parseAnd()
		if err != nil {
			return nil, err
		}
		left = &OrExpr{Left: left, Right: right}
	}
	return left, nil
}

// parseAnd accepts explicit AND or implicit juxtaposition as conjunction.
// Grammar:
//
//	and := not ( ( "AND" | implicit ) not )*
//
// Implicit AND stops at OR, ), or EOF.
func (p *parser) parseAnd() (QueryExpr, error) {
	left, err := p.parseNot()
	if err != nil {
		return nil, err
	}
	for {
		switch p.peek().typ {
		case tokAnd:
			p.advance()
		case tokOr, tokRParen, tokEOF:
			return left, nil
		}
		right, err := p.parseNot()
		if err != nil {
			return nil, err
		}
		if right == nil {
			return left, nil
		}
		left = &AndExpr{Left: left, Right: right}
	}
}

// parseNot consumes a leading NOT / - prefix, zero or more. Double negation
// is collapsed to plain atom — rarely useful but costs nothing to handle.
func (p *parser) parseNot() (QueryExpr, error) {
	negated := false
	for p.peek().typ == tokNot {
		p.advance()
		negated = !negated
	}
	atom, err := p.parseAtom()
	if err != nil {
		return nil, err
	}
	if negated {
		return negate(atom), nil
	}
	return atom, nil
}

// negate wraps an expression in NOT, collapsing a double negation.
//
// SRCH-008: the loop above already collapses adjacent NOTs, so `NOT NOT x`
// parsed to `x`. Parentheses hid it — `NOT(NOT x)` produced a nested NotExpr
// for the same logic, so two ways of writing one query parsed differently and
// printing the second was not idempotent. Found by
// FuzzQueryExpressionPrintReparses once the stronger assertion went back in.
func negate(expr QueryExpr) QueryExpr {
	if inner, ok := expr.(*NotExpr); ok {
		return inner.Inner
	}
	return &NotExpr{Inner: expr}
}

// parseAtom handles the leaf forms: term / phrase / wildcard / fuzzy and
// parenthesized sub-expressions.
func (p *parser) parseAtom() (QueryExpr, error) {
	t := p.advance()
	switch t.typ {
	case tokLParen:
		inner, err := p.parseOr()
		if err != nil {
			return nil, err
		}
		if p.peek().typ != tokRParen {
			return nil, fmt.Errorf("expected ')', got %q", p.peek().s)
		}
		p.advance()
		return inner, nil
	case tokRequire:
		// `+term` is already implicit in AND semantics at this layer; treat
		// identically to a plain atom so mixed +term/-term queries parse.
		return p.parseAtom()
	case tokTerm:
		return &TermExpr{Term: strings.ToLower(t.s)}, nil
	case tokFuzzy:
		d := t.n
		if d < 0 {
			d = 0
		}
		if d > 2 {
			d = 2
		}
		return &FuzzyExpr{Term: strings.ToLower(t.s), Distance: d}, nil
	case tokPhrase:
		return &PhraseExpr{Phrase: t.s}, nil
	case tokProximity:
		d := t.n
		if d <= 0 {
			d = 5
		}
		return &ProximityExpr{Phrase: t.s, Distance: d}, nil
	case tokWildcard:
		return &WildcardExpr{Pattern: strings.ToLower(t.s)}, nil
	case tokEOF:
		return nil, fmt.Errorf("unexpected end of query")
	default:
		return nil, fmt.Errorf("unexpected token %q", t.s)
	}
}

// --- Evaluator ---

// EvaluateExpression resolves a parsed query AST against the FTS index for
// the given collection and returns the matching doc-ID set. Scores are
// aggregated per leaf (BM25 via the existing scorers) and combined at
// AND/OR boundaries; NOT subtracts docs without touching scores.
//
// This is the query-string DSL counterpart of SearchBoolean/SearchPhrase
// etc. — it dispatches to those underneath rather than re-implementing
// scoring, so results stay consistent with mode="boolean" when the AST is
// flat.
func (f *FTSIndex) EvaluateExpression(collection string, expr QueryExpr, limit int) ([]FTSResult, error) {
	if expr == nil {
		return nil, nil
	}
	scores, err := f.evalExpr(collection, expr)
	if err != nil {
		return nil, err
	}
	out := make([]FTSResult, 0, len(scores))
	for docID, sc := range scores {
		out = append(out, FTSResult{
			DocID:        docID,
			Score:        sc.score,
			MatchedTerms: sc.terms,
		})
	}
	sortFTSResultsByScore(out)
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// exprScore accumulates per-doc score and matched-term breadcrumbs as the
// evaluator walks the tree. Unexported — callers only touch FTSResult.
type exprScore struct {
	score float64
	terms []string
}

// evalExpr walks the AST and returns a docID → exprScore map. AND
// intersects two subtrees; OR unions them; NOT relies on an enumeration
// of "all doc IDs in the collection" which is cheap because we already
// scan the inverted index per leaf.
func (f *FTSIndex) evalExpr(collection string, expr QueryExpr) (map[string]*exprScore, error) {
	switch e := expr.(type) {
	case *TermExpr:
		return f.evalTermNode(collection, e.Term)
	case *FuzzyExpr:
		res, err := f.SearchBM25Fuzzy(collection, e.Term, 0, e.Distance)
		if err != nil {
			return nil, err
		}
		return resultsToMap(res), nil
	case *PhraseExpr:
		res, err := f.SearchPhrase(collection, e.Phrase, 0)
		if err != nil {
			return nil, err
		}
		return resultsToMap(res), nil
	case *ProximityExpr:
		res, err := f.SearchProximity(collection, e.Phrase, e.Distance, 0)
		if err != nil {
			return nil, err
		}
		return resultsToMap(res), nil
	case *WildcardExpr:
		res, err := f.SearchWildcard(collection, e.Pattern, 0)
		if err != nil {
			return nil, err
		}
		return resultsToMap(res), nil
	case *AndExpr:
		left, err := f.evalExpr(collection, e.Left)
		if err != nil {
			return nil, err
		}
		right, err := f.evalExpr(collection, e.Right)
		if err != nil {
			return nil, err
		}
		return intersectScores(left, right), nil
	case *OrExpr:
		left, err := f.evalExpr(collection, e.Left)
		if err != nil {
			return nil, err
		}
		right, err := f.evalExpr(collection, e.Right)
		if err != nil {
			return nil, err
		}
		return unionScores(left, right), nil
	case *NotExpr:
		// NOT on its own is meaningless — the parser wraps it in AND when
		// it appears at statement level. Here we assume it's the right
		// operand of AND and return the excluded set for subtractAnd to
		// handle. Since evalExpr doesn't carry context, we return the
		// inner set and let AND handle the subtraction via a sentinel key.
		inner, err := f.evalExpr(collection, e.Inner)
		if err != nil {
			return nil, err
		}
		return negateScores(inner), nil
	default:
		return nil, fmt.Errorf("unsupported expression type %T", expr)
	}
}

// evalTermNode scores a single term via the same BM25 path used by mode=bm25.
// We pass the raw term as the "query" and limit 0 so the scorer doesn't
// truncate — the caller applies limit after AST evaluation.
func (f *FTSIndex) evalTermNode(collection, term string) (map[string]*exprScore, error) {
	res, err := f.SearchBM25(collection, term, 0)
	if err != nil {
		return nil, err
	}
	return resultsToMap(res), nil
}

// resultsToMap converts a slice of FTSResult into the score map used by the
// evaluator. The slice-map round-trip keeps leaf scorers interchangeable.
func resultsToMap(res []FTSResult) map[string]*exprScore {
	m := make(map[string]*exprScore, len(res))
	for _, r := range res {
		m[r.DocID] = &exprScore{score: r.Score, terms: r.MatchedTerms}
	}
	return m
}

// negatedMarker tags a docID set as "excluded" rather than "matched". The
// evaluator uses a reserved sentinel in the term list so AND can detect
// the exclusion semantic at merge time.
const negatedMarker = "\x00NOT\x00"

// negateScores flags every entry of the input as an exclusion. The map
// structure is preserved so downstream AND still has O(1) lookup.
func negateScores(in map[string]*exprScore) map[string]*exprScore {
	out := make(map[string]*exprScore, len(in))
	for id, s := range in {
		out[id] = &exprScore{score: s.score, terms: append([]string{negatedMarker}, s.terms...)}
	}
	return out
}

// intersectScores handles AND and AND-NOT: when either side is an
// exclusion set, the result is the other side minus those doc IDs.
func intersectScores(left, right map[string]*exprScore) map[string]*exprScore {
	leftNeg := isNegated(left)
	rightNeg := isNegated(right)
	switch {
	case leftNeg && rightNeg:
		// Intersection of two exclusion sets is the exclusion of their union.
		out := make(map[string]*exprScore, len(left))
		for id, s := range left {
			out[id] = s
		}
		for id, s := range right {
			if _, ok := out[id]; !ok {
				out[id] = s
			}
		}
		return out
	case leftNeg:
		return subtract(right, left)
	case rightNeg:
		return subtract(left, right)
	default:
		out := make(map[string]*exprScore)
		for id, ls := range left {
			if rs, ok := right[id]; ok {
				out[id] = &exprScore{
					score: ls.score + rs.score,
					terms: dedupeAppend(ls.terms, rs.terms),
				}
			}
		}
		return out
	}
}

// unionScores handles OR: keep every doc that matched either side, add the
// scores so docs matching both branches rank higher than single-branch.
func unionScores(left, right map[string]*exprScore) map[string]*exprScore {
	out := make(map[string]*exprScore, len(left)+len(right))
	for id, s := range left {
		out[id] = &exprScore{score: s.score, terms: append([]string(nil), s.terms...)}
	}
	for id, rs := range right {
		if ls, ok := out[id]; ok {
			ls.score += rs.score
			ls.terms = dedupeAppend(ls.terms, rs.terms)
		} else {
			out[id] = &exprScore{score: rs.score, terms: append([]string(nil), rs.terms...)}
		}
	}
	return out
}

// subtract returns everything in `keep` that isn't in `drop`.
func subtract(keep, drop map[string]*exprScore) map[string]*exprScore {
	out := make(map[string]*exprScore, len(keep))
	for id, s := range keep {
		if _, excluded := drop[id]; !excluded {
			out[id] = s
		}
	}
	return out
}

// isNegated returns true when the score map was produced by NotExpr. The
// evaluator tags excluded sets with the negatedMarker sentinel term.
func isNegated(m map[string]*exprScore) bool {
	for _, s := range m {
		if len(s.terms) > 0 && s.terms[0] == negatedMarker {
			return true
		}
		return false
	}
	return false
}

// dedupeAppend concatenates two term slices, dropping duplicates. Order of
// first appearance is preserved so results are reproducible run-to-run.
func dedupeAppend(a, b []string) []string {
	seen := make(map[string]struct{}, len(a)+len(b))
	out := make([]string, 0, len(a)+len(b))
	for _, s := range a {
		if s == negatedMarker {
			continue
		}
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	for _, s := range b {
		if s == negatedMarker {
			continue
		}
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}

// sortFTSResultsByScore sorts in place, descending by score. Matches the
// existing behavior of the per-algorithm scorers so clients see consistent
// ordering across modes.
func sortFTSResultsByScore(r []FTSResult) {
	// Insertion sort — the caller applies a topK truncation afterwards, so
	// we avoid allocating a comparator closure for large N. For N>32 the
	// cost is fine in practice because AST leaves already cap at index
	// scan size.
	for i := 1; i < len(r); i++ {
		for j := i; j > 0 && r[j].Score > r[j-1].Score; j-- {
			r[j], r[j-1] = r[j-1], r[j]
		}
	}
}
