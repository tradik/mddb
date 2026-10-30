package main

import (
	"mddb/internal/embedding"
	json "mddb/internal/jsonx"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
)

// INT-019: _search and _count on each of the four paths, with the requests
// elasticsearch-py sends.

// esNDJSON is the bulk content type elasticsearch-py 9 sends.
const esNDJSON = "application/vnd.elasticsearch+x-ndjson; compatible-with=9"

// esSeed loads four documents with PUT /es/_bulk, as helpers.bulk does, and
// waits for their embeddings with _refresh.
func esSeed(t *testing.T, h http.Handler) {
	t.Helper()
	body := strings.Join([]string{
		`{"index":{"_index":"books","_id":"fox"}}`,
		`{"content":"The quick brown fox jumps over the lazy dog","genre":"fable","year":1990}`,
		`{"index":{"_index":"books","_id":"bear"}}`,
		`{"content":"Brown bears eat honey in the forest","genre":"nature","year":2005}`,
		`{"index":{"_index":"books","_id":"turtle"}}`,
		`{"content":"A slow green turtle crosses the road","genre":"fable","year":2010}`,
		`{"create":{"_index":"books","_id":"owl"}}`,
		`{"content":"The owl hunts mice at night","genre":"nature","year":2021,"tags":["bird","night"]}`,
	}, "\n") + "\n"
	rec, out := esDo(t, h, http.MethodPut, "/es/_bulk?refresh=true", body, "Content-Type", esNDJSON)
	if rec.Code != http.StatusOK || out["errors"] != false {
		t.Fatalf("bulk: %d %v", rec.Code, out)
	}
}

// esHitIDs lists a search answer's _ids in order.
func esHitIDs(out map[string]any) []string {
	var ids []string
	for _, h := range out["hits"].(map[string]any)["hits"].([]any) {
		ids = append(ids, h.(map[string]any)["_id"].(string))
	}
	return ids
}

// esTotal is hits.total.value.
func esTotal(out map[string]any) int {
	return int(out["hits"].(map[string]any)["total"].(map[string]any)["value"].(float64))
}

func esSearchOK(t *testing.T, h http.Handler, path, body string) map[string]any {
	t.Helper()
	rec, out := esDo(t, h, http.MethodPost, path, body)
	if rec.Code != http.StatusOK || rec.Header().Get("X-Elastic-Product") != "Elasticsearch" {
		t.Fatalf("%s %s: %d %s", path, body, rec.Code, rec.Body.String())
	}
	return out
}

func esSortedHitIDs(out map[string]any) string {
	ids := esHitIDs(out)
	sort.Strings(ids)
	return strings.Join(ids, ",")
}

func TestESSearchFullText(t *testing.T) {
	_, h := esTestServer(t)
	esSeed(t, h)

	out := esSearchOK(t, h, "/es/books/_search", `{"query":{"match":{"content":"brown"}},
		"highlight":{"fields":[{"content":{}}],"pre_tags":["<b class=\"x\">"],"post_tags":["</b>"],"fragment_size":40,"number_of_fragments":1},
		"aggs":{"g":{"terms":{"field":"genre.keyword","size":1}}},"_source":["genre"]}`)
	if esSortedHitIDs(out) != "bear,fox" || esTotal(out) != 2 {
		t.Fatalf("match: %v", out)
	}
	hit := out["hits"].(map[string]any)["hits"].([]any)[0].(map[string]any)
	if src := hit["_source"].(map[string]any); len(src) != 1 || src["genre"] == nil {
		t.Fatalf("_source includes: %v", src)
	}
	if hl := hit["highlight"].(map[string]any)["content"].([]any); !strings.Contains(hl[0].(string), `<b class="x">`) {
		t.Fatalf("highlight: %v", hl)
	}
	agg := out["aggregations"].(map[string]any)["g"].(map[string]any)
	if len(agg["buckets"].([]any)) != 1 || agg["sum_other_doc_count"] != float64(1) {
		t.Fatalf("terms agg: %v", agg)
	}
	if out["hits"].(map[string]any)["max_score"] == nil {
		t.Fatal("a scored search has a max_score")
	}

	out = esSearchOK(t, h, "/es/books/_search", `{"query":{"bool":{"must":[{"match":{"content":"brown"}}],
		"filter":[{"term":{"genre":"nature"}},{"range":{"year":{"gte":2000}}}]}},"_source":false}`)
	if esSortedHitIDs(out) != "bear" {
		t.Fatalf("bool: %v", out)
	}
	if _, has := out["hits"].(map[string]any)["hits"].([]any)[0].(map[string]any)["_source"]; has {
		t.Fatal("_source false drops the source")
	}

	out = esSearchOK(t, h, "/es/books/_search", `{"query":{"bool":{"should":[{"match":{"content":"owl"}},{"match":{"content":"turtle"}}],
		"must_not":[{"term":{"genre":"fable"}}]}}}`)
	if esSortedHitIDs(out) != "owl" {
		t.Fatalf("should + must_not: %v", out)
	}

	out = esSearchOK(t, h, "/es/books/_search?size=1&from=1", `{"query":{"match":{"content":"brown"}}}`)
	if len(esHitIDs(out)) != 1 || esTotal(out) != 2 {
		t.Fatalf("paging from the URL: %v", out)
	}
	out = esSearchOK(t, h, "/es/books/_search", `{"query":{"match":{"content":"brown"}},"from":5}`)
	if len(esHitIDs(out)) != 0 || esTotal(out) != 2 {
		t.Fatalf("a page past the end is empty: %v", out)
	}
	out = esSearchOK(t, h, "/es/books/_search", `{"query":{"match_phrase":{"content":"green turtle"}}}`)
	if esSortedHitIDs(out) != "turtle" {
		t.Fatalf("phrase: %v", out)
	}
}

func TestESSearchListing(t *testing.T) {
	_, h := esTestServer(t)
	esSeed(t, h)

	out := esSearchOK(t, h, "/es/books/_search", "")
	if esTotal(out) != 4 || len(esHitIDs(out)) != 4 {
		t.Fatalf("match_all: %v", out)
	}
	out = esSearchOK(t, h, "/es/books/_search", `{"query":{"term":{"genre":"fable"}},"sort":[{"_id":{"order":"desc"}}],"size":1}`)
	if strings.Join(esHitIDs(out), ",") != "turtle" || esTotal(out) != 2 {
		t.Fatalf("sorted listing: %v", out)
	}
	if hit := out["hits"].(map[string]any)["hits"].([]any)[0].(map[string]any); hit["_score"] != nil {
		t.Fatalf("a field sort has no score: %v", hit)
	}
	out = esSearchOK(t, h, "/es/books/_search", `{"query":{"range":{"addedAt":{"gte":"1"}}},"size":0,
		"aggregations":{"genres":{"terms":{"field":"genre"}},"tags":{"terms":{"field":"tags"}}}}`)
	aggs := out["aggregations"].(map[string]any)
	if esTotal(out) != 4 || len(aggs["genres"].(map[string]any)["buckets"].([]any)) != 2 || len(aggs["tags"].(map[string]any)["buckets"].([]any)) != 2 {
		t.Fatalf("timestamp range and aggs: %v", out)
	}
	out = esSearchOK(t, h, "/es/books/_search", `{"query":{"range":{"addedAt":{"lt":"1"}}},"sort":"addedAt"}`)
	if esTotal(out) != 0 {
		t.Fatalf("a range nothing meets: %v", out)
	}

	rec, out := esDo(t, h, http.MethodGet, "/es/books/_count", "")
	if rec.Code != http.StatusOK || out["count"] != float64(4) {
		t.Fatalf("count: %d %v", rec.Code, out)
	}
	_, out = esDo(t, h, http.MethodPost, "/es/books/_count", `{"query":{"match":{"content":"brown"}}}`)
	if out["count"] != float64(2) {
		t.Fatalf("count with a query: %v", out)
	}
	rec, out = esDo(t, h, http.MethodPost, "/es/books/_count", `{"size":1}`)
	if rec.Code != http.StatusBadRequest || esErrType(out) != "parsing_exception" {
		t.Fatalf("_count takes only a query: %d %v", rec.Code, out)
	}
}

func TestESSearchKNNAndHybrid(t *testing.T) {
	s, h := esTestServer(t)
	esSeed(t, h)

	out := esSearchOK(t, h, "/es/books/_search", `{"knn":{"field":"v","k":2,"num_candidates":10,
		"query_vector_builder":{"text_embedding":{"model_text":"brown fox jumps"}},"filter":[{"range":{"year":{"lt":2010}}}]},
		"aggs":{"g":{"terms":{"field":"genre"}}}}`)
	ids := esHitIDs(out)
	if len(ids) != 2 || ids[0] != "fox" || out["aggregations"] == nil {
		t.Fatalf("knn: %v", out)
	}

	vec, err := s.Embedding.Embed(t.Context(), "The owl hunts mice at night", embedding.RoleDocument)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(map[string]any{"knn": []any{map[string]any{"field": "v", "query_vector": vec, "k": 1}}})
	out = esSearchOK(t, h, "/es/books/_search", string(b))
	if ids := esHitIDs(out); len(ids) != 1 || ids[0] != "owl" {
		t.Fatalf("knn with a vector: %v", out)
	}

	out = esSearchOK(t, h, "/es/books/_search", `{"query":{"bool":{"must":{"match":{"content":"Brown fox"}},"filter":{"term":{"genre":"fable"}}}},
		"knn":{"field":"v","query_vector_builder":{"text_embedding":{"model_text":"brown FOX"}}},"size":3}`)
	if ids := esHitIDs(out); len(ids) == 0 || ids[0] != "fox" {
		t.Fatalf("hybrid: %v", out)
	}

	// A failure inside the hybrid or vector path comes back with its status.
	s.Embedding = failingPartEmbedding{Provider: s.Embedding, fail: "brown"}
	rec, out := esDo(t, h, http.MethodPost, "/es/books/_search", `{"query":{"match":{"content":"brown"}},
		"knn":{"field":"v","query_vector_builder":{"text_embedding":{"model_text":"brown"}}}}`)
	if rec.Code < 400 || out["error"] == nil {
		t.Fatalf("hybrid failure: %d %v", rec.Code, out)
	}
	s.Embedding = nil
	rec, out = esDo(t, h, http.MethodPost, "/es/books/_search", `{"knn":{"field":"v","query_vector_builder":{"text_embedding":{"model_text":"x"}}}}`)
	if rec.Code < 400 || out["error"] == nil {
		t.Fatalf("vector failure: %d %v", rec.Code, out)
	}
}

func TestESSearchRefusals(t *testing.T) {
	_, h := esTestServer(t)
	esSeed(t, h)
	knn := `"knn":{"field":"v","query_vector_builder":{"text_embedding":{"model_text":"fox"}}}`
	for body, clause := range map[string]string{
		`{"query":{"fuzzy":{}}}`: "[fuzzy]",
		`{"knn":[1,2]}`:          "[knn]",
		`{"knn":{"field":"v","query_vector":[1],"k":1},"query":{"match":{"content":"fox"}}}`: "[knn.query_vector]",
		`{"query":{"match_phrase":{"content":"a b"}},` + knn + `}`:                           "[knn]",
		`{"query":{"match":{"content":"dog"}},` + knn + `}`:                                  "[knn.query_vector_builder]",
		`{"query":{"range":{"addedAt":{"gt":1}}},` + knn + `}`:                               "[range]",
		`{"query":{"match":{"content":"fox"}},"sort":"addedAt"}`:                             "[sort]",
		`{"highlight":{}}`: "[highlight]",
		`{"query":{"term":{"g":"a"}},"knn":{"field":"v","query_vector_builder":{"text_embedding":{"model_text":"fox"}},"filter":{"term":{"g":"b"}}}}`: "[knn.filter]",
		`{"knn":{"field":"v","query_vector_builder":{"text_embedding":{"model_text":"fox"}},"filter":{"bad":1}}}`:                                     "[bad]",
		`{"post_filter":{}}`:                    "[search.post_filter]",
		`{"size":"x"}`:                          "[size]",
		`[]`:                                    "[body]",
		`{"query":{"bool":{"must":1}}}`:         "[query]",
		`{"sort":"title"}`:                      "[sort.title]",
		`{"_source":{"excludes":["a"]}}`:        "[_source.excludes]",
		`{"highlight":{"fields":{"title":{}}}}`: "[highlight.fields.title]",
		`{"aggs":{"a":{"avg":{"field":"y"}}}}`:  "[aggs.a.avg]",
		`{"knn":{"field":"v","similarity":1}}`:  "[knn.similarity]",
	} {
		rec, out := esDo(t, h, http.MethodPost, "/es/books/_search", body)
		reason, _ := out["error"].(map[string]any)["reason"].(string)
		if rec.Code != http.StatusBadRequest || !strings.Contains(reason, clause) {
			t.Errorf("%s: %d %v, want %s", body, rec.Code, out, clause)
		}
	}
	rec, out := esDo(t, h, http.MethodPost, "/es/books/_search?q=fox", "")
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "[q]") {
		t.Fatalf("?q: %d %v", rec.Code, out)
	}
}

func TestESSearchReportsACappedPage(t *testing.T) {
	s, h := esTestServer(t)
	esSeed(t, h)
	s.SearchMaxLimit = 2
	for _, body := range []string{`{"size":50}`, `{"size":50,"query":{"match":{"content":"brown turtle owl"}}}`} {
		rec, out := esDo(t, h, http.MethodPost, "/es/books/_search", body)
		if rec.Code != http.StatusOK || len(esHitIDs(out)) != 2 || out["limitCapped"] != true || out["limit"] != float64(2) {
			t.Fatalf("%s: %d %v", body, rec.Code, out)
		}
		if rec.Header().Get("X-Limit-Capped") != "true" || !strings.HasPrefix(rec.Header().Get("Warning"), `299 MDDB "from+size capped to 2`) {
			t.Fatalf("%s: headers %v", body, rec.Header())
		}
	}
	// The vector path reports its own cap when k asks for more.
	rec, out := esDo(t, h, http.MethodPost, "/es/books/_search", `{"size":1,"knn":{"field":"v","k":9,"query_vector_builder":{"text_embedding":{"model_text":"fox"}}}}`)
	if rec.Code != http.StatusOK || out["limitCapped"] != true || len(esHitIDs(out)) != 1 {
		t.Fatalf("knn cap: %d %v", rec.Code, out)
	}
}

func TestESSearchNeedsReadPermission(t *testing.T) {
	s, _ := esTestServer(t)
	am, cleanup := authMwSetup(t)
	defer cleanup()
	s.AuthManager = am
	rec := httptest.NewRecorder()
	s.esSearch(rec, httptest.NewRequest(http.MethodPost, "/es/books/_search", nil), "books", false)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("no claims: %d", rec.Code)
	}
}
