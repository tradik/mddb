package main

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	json "mddb/internal/jsonx"
)

// INT-019: the Query DSL translator. Every accepted clause must land on the
// filter or expression MDDB's search paths take, and every refused one must
// say which clause it was.

func TestCompileESQueryTranslates(t *testing.T) {
	cases := []struct {
		name, query string
		want        esTranslated
	}{
		{"absent", ``, esTranslated{Filter: MetaFilter{}}},
		{"null", `null`, esTranslated{Filter: MetaFilter{}}},
		{"match_all", `{"match_all":{"boost":1}}`, esTranslated{Filter: MetaFilter{}}},
		{"match", `{"match":{"content":"Quick (Brown) AND fox"}}`, esTranslated{
			Expr: "(quick OR brown OR and OR fox)", Simple: "quick brown and fox", Filter: MetaFilter{}}},
		{"match number", `{"match":{"content":42}}`, esTranslated{Expr: "42", Simple: "42", Filter: MetaFilter{}}},
		{"match and", `{"match":{"content":{"query":"a b","operator":"AND","boost":2}}}`, esTranslated{
			Expr: "(a AND b)", Filter: MetaFilter{}}},
		{"match_phrase", `{"match_phrase":{"content":{"query":"quick fox"}}}`, esTranslated{
			Expr: `"quick fox"`, Filter: MetaFilter{}}},
		{"match_phrase one word", `{"match_phrase":{"content":"fox"}}`, esTranslated{Expr: "fox", Filter: MetaFilter{}}},
		{"multi_match", `{"multi_match":{"query":"red fox","fields":["title^2","content"]}}`, esTranslated{
			Expr: "(red OR fox)", Simple: "red fox", Filter: MetaFilter{}}},
		{"multi_match phrase", `{"multi_match":{"query":"red fox","type":"phrase"}}`, esTranslated{
			Expr: `"red fox"`, Filter: MetaFilter{}}},
		{"multi_match and", `{"multi_match":{"query":"red fox","type":"cross_fields","operator":"and"}}`, esTranslated{
			Expr: "(red AND fox)", Filter: MetaFilter{}}},
		{"term", `{"term":{"genre.keyword":"fable"}}`, esTranslated{Filter: MetaFilter{"genre": {In: []string{"fable"}}}}},
		{"term value", `{"term":{"year":{"value":2021}}}`, esTranslated{Filter: MetaFilter{"year": {In: []string{"2021"}}}}},
		{"terms", `{"terms":{"tag":["a","b"]}}`, esTranslated{Filter: MetaFilter{"tag": {In: []string{"a", "b"}}}}},
		{"range", `{"range":{"year":{"gte":2000,"lt":"2020","boost":1}}}`, esTranslated{
			Filter: MetaFilter{}, Ranges: []RangeFilter{{Field: "year", Gte: "2000", Lt: "2020"}}}},
		{"range timestamp", `{"range":{"addedAt":{"gt":1,"lte":2}}}`, esTranslated{
			Filter: MetaFilter{}, Ranges: []RangeFilter{{Field: "addedAt", Gt: "1", Lte: "2"}}}},
		{"bool", `{"bool":{"must":{"match":{"content":"fox"}},"filter":[{"term":{"g":"x"}},{"match":{"content":"dog"}}],
			"must_not":[{"term":{"g":"y"}},{"terms":{"g":["z"]}},{"match_phrase":{"content":"lazy cat"}}],"minimum_should_match":1,"boost":1}}`,
			esTranslated{Expr: `(fox AND dog AND NOT "lazy cat")`, Filter: MetaFilter{"g": {In: []string{"x"}, Not: []string{"y", "z"}}}}},
		{"bool should", `{"bool":{"should":[{"match":{"content":"owl"}},{"bool":{"must":[{"match":{"content":"a b"}}],"must_not":{"match":{"content":"c"}}}}],
			"filter":{"term":{"g":"x"}},"minimum_should_match":"1"}}`,
			esTranslated{Expr: "(owl OR ((a OR b) AND NOT c))", Filter: MetaFilter{"g": {In: []string{"x"}}}}},
		{"bool filters only", `{"bool":{"filter":[{"range":{"y":{"gt":1}}}]}}`, esTranslated{
			Filter: MetaFilter{}, Ranges: []RangeFilter{{Field: "y", Gt: "1"}}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := compileESQuery(json.RawMessage(tc.query))
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(*got, tc.want) {
				t.Fatalf("got %+v\nwant %+v", *got, tc.want)
			}
		})
	}
}

func TestCompileESQueryRefusesByName(t *testing.T) {
	cases := []struct{ query, clause string }{
		{`[]`, "query"},
		{`{}`, "query"},
		{`{"fuzzy":{"content":"x"}}`, "fuzzy"},
		{`{"match_all":[]}`, "match_all"},
		{`{"match_all":{"x":1}}`, "match_all.x"},
		{`{"bool":{"should":{"match_all":{}}}}`, "match_all"},
		{`{"match":{"a":"x","b":"y"}}`, "match"},
		{`{"match":{"content":[]}}`, "match"},
		{`{"match":{"content":{"fuzziness":1}}}`, "match.fuzziness"},
		{`{"match":{"content":{"query":{}}}}`, "match.query"},
		{`{"match":{"content":{"query":"x","operator":1}}}`, "match.operator"},
		{`{"match":{"content":{"query":"x","operator":"xor"}}}`, "match.operator"},
		{`{"match_phrase":{"content":{"query":"x","operator":"and"}}}`, "match_phrase.operator"},
		{`{"match":{"content":"!!"}}`, "match"},
		{`{"multi_match":1}`, "multi_match"},
		{`{"multi_match":{"query":"x","slop":1}}`, "multi_match.slop"},
		{`{"multi_match":{"query":{}}}`, "multi_match.query"},
		{`{"multi_match":{"query":"x","fields":{}}}`, "multi_match.fields"},
		{`{"multi_match":{"query":"x","type":1}}`, "multi_match.type"},
		{`{"multi_match":{"query":"x","type":"bool_prefix"}}`, "multi_match.type"},
		{`{"term":[]}`, "term"},
		{`{"term":{"_id":"x"}}`, "term"},
		{`{"term":{"content":"x"}}`, "term"},
		{`{"term":{"g":{"v":1}}}`, "term.g"},
		{`{"term":{"g":{"value":{}}}}`, "term.g"},
		{`{"terms":{"g":[{}]}}`, "terms.g"},
		{`{"bool":{"filter":[{"term":{"g":"a"}},{"term":{"g":"b"}}]}}`, "term.g"},
		{`{"bool":{"should":[{"term":{"g":"a"}}]}}`, "term"},
		{`{"range":[]}`, "range"},
		{`{"range":{"lang":{"gt":1}}}`, "range"},
		{`{"bool":{"must_not":{"range":{"y":{"gt":1}}}}}`, "range"},
		{`{"range":{"y":1}}`, "range.y"},
		{`{"range":{"y":{"format":"x"}}}`, "range.y.format"},
		{`{"range":{"y":{"gt":{}}}}`, "range.y.gt"},
		{`{"bool":[]}`, "bool"},
		{`{"bool":{"x":1}}`, "bool.x"},
		{`{"bool":{"minimum_should_match":2}}`, "bool.minimum_should_match"},
		{`{"bool":{"must":{"fuzzy":{}}}}`, "fuzzy"},
		{`{"bool":{"must":{"match":{"content":"a"}},"should":{"match":{"content":"b"}}}}`, "bool.should"},
		{`{"bool":{"must_not":{"match":{"content":"a"}}}}`, "bool.must_not"},
	}
	for _, tc := range cases {
		t.Run(tc.query, func(t *testing.T) {
			_, err := compileESQuery(json.RawMessage(tc.query))
			var dsl *esDSLError
			if !errors.As(err, &dsl) || dsl.Clause != tc.clause {
				t.Fatalf("error %v, want clause %q", err, tc.clause)
			}
			if !strings.Contains(err.Error(), "["+tc.clause+"]") {
				t.Fatalf("message %q does not name %q", err, tc.clause)
			}
		})
	}
}

func TestESTranslatedFTSQuery(t *testing.T) {
	q, mode, algo := (&esTranslated{Expr: "(a OR b)", Simple: "a b"}).FTSQuery()
	if q != "a b" || mode != "simple" || algo != "bm25" {
		t.Fatalf("simple: %q %q %q", q, mode, algo)
	}
	q, mode, algo = (&esTranslated{Expr: "(a AND b)"}).FTSQuery()
	if q != "(a AND b)" || mode != "expression" || algo != "" {
		t.Fatalf("expression: %q %q %q", q, mode, algo)
	}
}

func TestESSingleEmptyObject(t *testing.T) {
	if _, _, err := esSingle(json.RawMessage(`null`), "x"); err == nil {
		t.Fatal("null is not an object")
	}
}
