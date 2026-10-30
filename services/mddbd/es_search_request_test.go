package main

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	json "mddb/internal/jsonx"
)

// INT-019: every part of a _search body is translated or refused by name.

func parseESBody(t *testing.T, body string) (*esSearchSpec, error) {
	t.Helper()
	return parseESSearch(httptest.NewRequest(http.MethodPost, "/es/b/_search", nil), []byte(body), false)
}

func TestParseESSearchShapes(t *testing.T) {
	spec, err := parseESBody(t, `{"size":3,"from":1,"sort":["_score"],"_source":{"includes":["a"]},"track_total_hits":true,"timeout":"1s",
		"highlight":{"fields":{"*":{}},"type":"unified"},"aggs":{"x":{"terms":{"field":"g","size":2}}}}`)
	if err != nil {
		t.Fatal(err)
	}
	want := &esSearchSpec{Query: &esTranslated{Filter: MetaFilter{}}, Size: 3, From: 1,
		Source: esSourceSpec{Includes: []string{"a"}}, Highlight: &esHighlightSpec{Tag: "<em>"},
		Aggs: []esTermsAgg{{Name: "x", Field: "g", Size: 2}}}
	if !reflect.DeepEqual(spec, want) {
		t.Fatalf("got %+v\nwant %+v", spec, want)
	}
	for body, sort := range map[string][2]any{
		`{"sort":"_doc"}`:                           {"key", true},
		`{"sort":{"updatedAt":"desc"}}`:             {"updatedAt", false},
		`{"sort":[{"addedAt":{"order":"ASC"}}]}`:    {"addedAt", true},
		`{"sort":[{"_score":{"order":"desc"}}]}`:    {"", false},
		`{"_source":"genre","sort":{"_id":"desc"}}`: {"key", false},
	} {
		spec, err := parseESBody(t, body)
		if err != nil || spec.SortField != sort[0] || spec.SortAsc != sort[1] {
			t.Errorf("%s: %+v %v", body, spec, err)
		}
	}
	if spec, err = parseESBody(t, `{"_source":true}`); err != nil || spec.Source.Off {
		t.Fatalf("_source true: %+v %v", spec, err)
	}
}

func TestParseESSearchRefusals(t *testing.T) {
	for body, clause := range map[string]string{
		`{"from":-1}`:                                          "from",
		`{"sort":["_score","_id"]}`:                            "sort",
		`{"sort":[1]}`:                                         "sort",
		`{"sort":{"_id":1}}`:                                   "sort._id",
		`{"sort":{"_id":"up"}}`:                                "sort._id",
		`{"sort":{"_score":"asc"}}`:                            "sort._score",
		`{"_source":[{}]}`:                                     "_source",
		`{"_source":{"includes":{}}}`:                          "_source.includes",
		`{"highlight":[]}`:                                     "highlight",
		`{"highlight":{"order":"score"}}`:                      "highlight.order",
		`{"highlight":{"fields":1}}`:                           "highlight.fields",
		`{"highlight":{"fields":{"content":1}}}`:               "highlight.fields.content",
		`{"highlight":{"fields":{"content":{"x":1}}}}`:         "highlight.fields.content",
		`{"highlight":{"pre_tags":["<a>","<b>"]}}`:             "highlight.pre_tags",
		`{"highlight":{"pre_tags":["<a>"]}}`:                   "highlight.post_tags",
		`{"highlight":{"pre_tags":["**"],"post_tags":["**"]}}`: "highlight.post_tags",
		`{"highlight":{"fragment_size":0}}`:                    "highlight.fragment_size",
		`{"aggs":[]}`:                                          "aggs",
		`{"aggs":{"a":{}}}`:                                    "aggs.a",
		`{"aggs":{"a":{"terms":[]}}}`:                          "aggs.a.terms",
		`{"aggs":{"a":{"terms":{"field":"g","order":{}}}}}`:    "aggs.a.terms.order",
		`{"aggs":{"a":{"terms":{}}}}`:                          "aggs.a.terms.field",
		`{"aggs":{"a":{"terms":{"field":"_id"}}}}`:             "aggs.a.terms.field",
		`{"aggs":{"a":{"terms":{"field":"g","size":0}}}}`:      "aggs.a.terms.size",
		`{"knn":{"query_vector":[]}}`:                          "knn.query_vector",
		`{"knn":{"query_vector_builder":{"x":{}}}}`:            "knn.query_vector_builder",
		`{"knn":{}}`:                                           "knn",
		`{"knn":{"query_vector":[1],"query_vector_builder":{"text_embedding":{"model_text":"x"}}}}`: "knn",
		`{"knn":{"query_vector":[1],"k":0}}`: "knn.k",
		`{"knn":1}`:                          "knn",
		`{"knn":{"query_vector":[1],"filter":{"match":{"content":"x"}}}}`: "knn.filter",
	} {
		_, err := parseESBody(t, body)
		var dsl *esDSLError
		if !errors.As(err, &dsl) || dsl.Clause != clause {
			t.Errorf("%s: %v, want clause %q", body, err, clause)
		}
	}
}

func TestParseESKNN(t *testing.T) {
	knn, err := parseESKNN(json.RawMessage(`[{"query_vector":[1,2],"k":3,"filter":{"bool":{"must_not":{"term":{"g":"x"}}}}}]`))
	if err != nil || !reflect.DeepEqual(knn.Vector, []float32{1, 2}) || knn.K != 3 || !reflect.DeepEqual(knn.Filter.Filter["g"].Not, []string{"x"}) {
		t.Fatalf("%+v %v", knn, err)
	}
	if knn, err = parseESKNN(json.RawMessage(`null`)); knn != nil || err != nil {
		t.Fatal("null knn is no knn")
	}
}

func TestESMergeFilter(t *testing.T) {
	q := &esTranslated{Filter: MetaFilter{"a": {In: []string{"1"}}}}
	knn := &esTranslated{Filter: MetaFilter{"a": {Not: []string{"2"}}, "b": {In: []string{"3"}}}, Ranges: []RangeFilter{{Field: "y", Gt: "1"}}}
	if err := q.mergeFilter(knn); err != nil {
		t.Fatal(err)
	}
	want := MetaFilter{"a": {In: []string{"1"}, Not: []string{"2"}}, "b": {In: []string{"3"}}}
	if !reflect.DeepEqual(q.Filter, want) || len(q.Ranges) != 1 {
		t.Fatalf("%+v", q)
	}
}

func TestESSearchPathFailures(t *testing.T) {
	s, h := esTestServer(t)
	esSeed(t, h)
	knn := `"knn":{"field":"v","query_vector_builder":{"text_embedding":{"model_text":"fox"}}}`
	rec, out := esDo(t, h, http.MethodPost, "/es/books/_search",
		`{"query":{"bool":{"must":{"match":{"content":"fox"}},"filter":{"range":{"addedAt":{"gt":1}}}}},`+knn+`}`)
	if rec.Code != http.StatusBadRequest || esErrType(out) != "parsing_exception" {
		t.Fatalf("hybrid with a timestamp range: %d %v", rec.Code, out)
	}

	corruptDoc(t, s, "books", "fox")
	if rec, _ = esDo(t, h, http.MethodPost, "/es/books/_search", `{"query":{"term":{"genre":"fable"}}}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("listing over an unreadable document: %d", rec.Code)
	}

	s.FTSIndex = nil
	if rec, _ = esDo(t, h, http.MethodPost, "/es/books/_search", `{"query":{"match":{"content":"fox"}}}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("no full-text index: %d", rec.Code)
	}

	c := newESCapture()
	c.Header().Set("X", "y")
	if c.header.Get("X") != "y" {
		t.Fatal("capture header")
	}
}
