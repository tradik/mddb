package main

import (
	"fmt"
	"maps"
	"slices"

	json "mddb/internal/jsonx"
)

// Query DSL translation for the Elasticsearch-compatible subset (INT-019).
//
// A query is compiled, once, into what MDDB's own search paths already take:
//   - text clauses (match, match_phrase, multi_match) become one full-text
//     query: a plain word list for a lone `match`, otherwise a boolean
//     expression for the FTS "expression" mode (AND, OR, NOT, phrases);
//   - term, terms and range become the operator filter and rangeMeta of
//     /v1/fts, /v1/vector-search and /v1/hybrid-search (SRCH-017).
//
// Nothing here searches. A clause MDDB cannot answer the way Elasticsearch
// would is refused with a 400 that names it; it is never dropped quietly, as
// a dropped filter returns documents the caller excluded.

// esDSLError is a request the subset does not translate. Clause names the
// Query DSL element, so the 400 says which part of the request to change.
type esDSLError struct {
	Clause string
	Reason string
}

// Error names the clause and why it is refused.
func (e *esDSLError) Error() string {
	return fmt.Sprintf("[%s] %s", e.Clause, e.Reason)
}

// dslErr builds an esDSLError.
func dslErr(clause, format string, args ...any) error {
	return &esDSLError{Clause: clause, Reason: fmt.Sprintf(format, args...)}
}

// esScope is where a clause sits. Only the must/filter chain from the top
// ANDs into MDDB's filter; under should, must_not or a bool nested in those,
// a filter would change meaning, so only text is accepted there.
type esScope int

const (
	esMust    esScope = iota // the query itself, bool.must, bool.filter
	esShould                 // bool.should of a bool in esMust
	esMustNot                // bool.must_not of a bool in esMust
	esNested                 // anything below esShould or esMustNot
)

// esTranslated is a compiled query.
type esTranslated struct {
	// Expr is the full-text expression; "" when the query has no text.
	Expr string
	// Simple is set when the only text clause is one `match` with operator
	// or: the words, for the BM25 "simple" mode, whose ranking is better than
	// the expression evaluator's.
	Simple string
	Filter MetaFilter
	Ranges []RangeFilter
}

// hasText reports whether the query needs the full-text index.
func (t *esTranslated) hasText() bool { return t.Expr != "" }

// FTSQuery is the query and mode for FTSSearchRequest.
func (t *esTranslated) FTSQuery() (query, mode, algorithm string) {
	if t.Simple != "" {
		return t.Simple, "simple", "bm25"
	}
	return t.Expr, "expression", ""
}

// esCompiler accumulates the filter while it walks a query.
type esCompiler struct {
	out        esTranslated
	textLeaves int
	simple     string
}

// compileESQuery translates a query object; an absent query is match_all.
func compileESQuery(raw json.RawMessage) (*esTranslated, error) {
	c := &esCompiler{out: esTranslated{Filter: MetaFilter{}}}
	if len(raw) > 0 && string(raw) != "null" {
		expr, err := c.clause(raw, esMust)
		if err != nil {
			return nil, err
		}
		c.out.Expr = expr
	}
	if c.textLeaves == 1 && c.simple != "" {
		c.out.Simple = c.simple
	}
	return &c.out, nil
}

// esObject decodes a JSON object, naming where it was expected on failure.
func esObject(raw json.RawMessage, where string) (map[string]json.RawMessage, error) {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil || obj == nil {
		return nil, dslErr(where, "must be a JSON object")
	}
	return obj, nil
}

// esSingle is the one key of an object such as {"match": {...}} or
// {"field": {...}}.
func esSingle(raw json.RawMessage, where string) (string, json.RawMessage, error) {
	obj, err := esObject(raw, where)
	if err != nil {
		return "", nil, err
	}
	if len(obj) != 1 {
		return "", nil, dslErr(where, "must have exactly one key, got %d", len(obj))
	}
	key := slices.Collect(maps.Keys(obj))[0]
	return key, obj[key], nil
}

// clause compiles one query clause and returns its text expression.
func (c *esCompiler) clause(raw json.RawMessage, scope esScope) (string, error) {
	name, body, err := esSingle(raw, "query")
	if err != nil {
		return "", err
	}
	switch name {
	case "match_all":
		return "", c.matchAll(body, scope)
	case "match", "match_phrase":
		return c.match(name, body, scope)
	case "multi_match":
		return c.multiMatch(body, scope)
	case "term", "terms":
		return "", c.term(name, body, scope)
	case "range":
		return "", c.rangeClause(body, scope)
	case "bool":
		return c.boolClause(body, scope)
	default:
		return "", dslErr(name, "query clause is not supported by the MDDB Elasticsearch subset")
	}
}

// matchAll accepts {} (and boost) where it narrows nothing.
func (c *esCompiler) matchAll(body json.RawMessage, scope esScope) error {
	obj, err := esObject(body, "match_all")
	if err != nil {
		return err
	}
	if err := esOnlyKeys("match_all", obj, "boost"); err != nil {
		return err
	}
	if scope != esMust {
		return dslErr("match_all", "is only supported in the query itself, bool.must or bool.filter")
	}
	return nil
}

// esOnlyKeys refuses any key of obj outside allowed, naming it.
func esOnlyKeys(clause string, obj map[string]json.RawMessage, allowed ...string) error {
	for k := range obj {
		if !slices.Contains(allowed, k) {
			return dslErr(clause+"."+k, "option is not supported")
		}
	}
	return nil
}

// boolClause compiles bool. In the must chain, filters AND into the request
// and text ANDs into the expression. should becomes a required OR group
// (minimum_should_match 1); next to a text must it would only rank, which
// the expression cannot express, so that pairing is refused.
func (c *esCompiler) boolClause(body json.RawMessage, scope esScope) (string, error) {
	obj, err := esObject(body, "bool")
	if err != nil {
		return "", err
	}
	if err := esOnlyKeys("bool", obj, "must", "filter", "should", "must_not", "minimum_should_match", "boost"); err != nil {
		return "", err
	}
	if raw, ok := obj["minimum_should_match"]; ok {
		if v, err := scalarString(raw); err != nil || v != "1" {
			return "", dslErr("bool.minimum_should_match", "only 1 is supported")
		}
	}
	child := func(occ esScope) esScope {
		if scope == esMust {
			return occ
		}
		return esNested
	}
	var positive, should, negative []string
	for _, part := range []struct {
		key   string
		scope esScope
		dst   *[]string
	}{
		{"must", esMust, &positive}, {"filter", esMust, &positive},
		{"should", esShould, &should}, {"must_not", esMustNot, &negative},
	} {
		frags, err := c.clauseList(obj[part.key], child(part.scope))
		if err != nil {
			return "", err
		}
		*part.dst = append(*part.dst, frags...)
	}
	if len(should) > 0 {
		if len(positive) > 0 {
			return "", dslErr("bool.should", "next to a text bool.must or bool.filter only ranks in Elasticsearch; MDDB cannot rank without matching")
		}
		positive = []string{esJoin(should, " OR ")}
	}
	if len(negative) > 0 && len(positive) == 0 {
		return "", dslErr("bool.must_not", "a text clause under must_not needs a text clause to subtract from")
	}
	for _, n := range negative {
		positive = append(positive, "NOT "+n)
	}
	if len(positive) == 0 {
		return "", nil
	}
	return esJoin(positive, " AND "), nil
}

// clauseList compiles a bool occurrence: one clause or an array of them. It
// returns the text fragments; filters went into the compiler.
func (c *esCompiler) clauseList(raw json.RawMessage, scope esScope) ([]string, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var list []json.RawMessage
	if json.Unmarshal(raw, &list) != nil {
		list = []json.RawMessage{raw}
	}
	var frags []string
	for _, item := range list {
		frag, err := c.clause(item, scope)
		if err != nil {
			return nil, err
		}
		if frag != "" {
			frags = append(frags, frag)
		}
	}
	return frags, nil
}
