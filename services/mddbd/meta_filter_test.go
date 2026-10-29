package main

import (
	"context"
	"sort"
	"strings"
	"testing"
	"time"

	json "mddb/internal/jsonx"

	bolt "go.etcd.io/bbolt"
)

// seedFilterDocs adds a small code-like collection the filter tests share.
func seedFilterDocs(t *testing.T, s *Server) {
	t.Helper()
	addTestDoc(t, s, "code", "a", "en", "hnsw graph search", map[string][]string{
		"path": {"internal/vector/hnsw.go"}, "kind": {"code"}, "size": {"9000"}, "modified": {"2026-09-15"}, "title": {"HNSW index"},
	})
	addTestDoc(t, s, "code", "b", "en", "hnsw tests", map[string][]string{
		"path": {"internal/vector/hnsw_test.go"}, "kind": {"test"}, "size": {"12000"}, "modified": {"2026-08-01"},
	})
	addTestDoc(t, s, "code", "c", "en", "main entry", map[string][]string{
		"path": {"cmd/main.go"}, "kind": {"code"}, "size": {"500"}, "modified": {"2026-10-01"}, "summary": {"entry point"},
	})
	addTestDoc(t, s, "code", "d", "en", "readme", map[string][]string{
		"path": {"README.md"}, "size": {"20000"},
	})
}

func filterKeys(t *testing.T, s *Server, ids map[string]bool) []string {
	t.Helper()
	var keys []string
	_ = s.DBViewC("code", func(tx *bolt.Tx) error {
		for id := range ids {
			d, err := loadDoc(tx.Bucket([]byte("docs")).Get([]byte("doc|code|" + id)))
			if err == nil {
				keys = append(keys, d.Key)
			}
		}
		return nil
	})
	sort.Strings(keys)
	return keys
}

func parseFilter(t *testing.T, raw string) MetaFilter {
	t.Helper()
	var f MetaFilter
	if err := json.Unmarshal([]byte(raw), &f); err != nil {
		t.Fatalf("parse %s: %v", raw, err)
	}
	return f
}

func TestMetaFilter_IndexOperators(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	seedFilterDocs(t, s)

	cases := []struct {
		name, filter string
		want         []string
	}{
		{"prefix", `{"path":{"prefix":"internal/"}}`, []string{"a", "b"}},
		{"contains is case-insensitive", `{"title":{"contains":"hnsw"}}`, []string{"a"}},
		{"date range", `{"modified":{"gte":"2026-09-01"}}`, []string{"a", "c"}},
		{"numeric range, not lexical", `{"size":{"lt":10000}}`, []string{"a", "c"}},
		{"gt and lte together", `{"size":{"gt":500,"lte":12000}}`, []string{"a", "b"}},
		{"not keeps documents without the key", `{"kind":{"not":["test"]}}`, []string{"a", "c", "d"}},
		{"exists true", `{"summary":{"exists":true}}`, []string{"c"}},
		{"exists false", `{"kind":{"exists":false}}`, []string{"d"}},
		{"in", `{"kind":{"in":["code"]}}`, []string{"a", "c"}},
		{"in scalar", `{"kind":{"in":"test"}}`, []string{"b"}},
		{"keys are ANDed", `{"path":{"prefix":"internal/"},"kind":{"not":"test"}}`, []string{"a"}},
		{"prefix plus range", `{"path":{"prefix":"internal/","contains":"test"}}`, []string{"b"}},
		{"nothing", `{"path":{"prefix":"nope/"}}`, nil},
		{"empty condition matches all", `{"path":{}}`, []string{"a", "b", "c", "d"}},
		{"value operator and exists false contradict", `{"kind":{"in":["code"],"exists":false}}`, nil},
		{"value operator and not", `{"path":{"prefix":"internal/","not":["internal/vector/hnsw.go"]}}`, []string{"b"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ids, active := s.allowedDocIDs("code", nil, parseFilter(t, tc.filter))
			if !active {
				t.Fatal("filter not active")
			}
			if got := filterKeys(t, s, ids); strings.Join(got, ",") != strings.Join(tc.want, ",") {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
			// The in-memory evaluator must agree with the index one.
			var mem []string
			for _, k := range []string{"a", "b", "c", "d"} {
				d, _ := s.loadDocByRef("code", k, "en")
				if parseFilter(t, tc.filter).Matches(d.Meta) {
					mem = append(mem, k)
				}
			}
			if strings.Join(mem, ",") != strings.Join(tc.want, ",") {
				t.Fatalf("in-memory got %v, want %v", mem, tc.want)
			}
		})
	}
}

// TestMetaFilter_PrefixReadsNoBodies proves prefix and range filters are
// answered from the meta index alone: with the document records gone, the
// index still yields their IDs.
func TestMetaFilter_PrefixReadsNoBodies(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	seedFilterDocs(t, s)

	before, _ := s.allowedDocIDs("code", nil, parseFilter(t, `{"path":{"prefix":"internal/"},"size":{"gte":1}}`))
	if err := s.DB.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte("docs"))
		var keys [][]byte
		_ = b.ForEach(func(k, _ []byte) error { keys = append(keys, append([]byte{}, k...)); return nil })
		for _, k := range keys {
			if err := b.Delete(k); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	after, _ := s.allowedDocIDs("code", nil, parseFilter(t, `{"path":{"prefix":"internal/"},"size":{"gte":1}}`))
	if len(before) != 2 || len(after) != 2 {
		t.Fatalf("prefix must not depend on document records: before=%d after=%d", len(before), len(after))
	}
}

func TestMetaFilter_FilterMetaUnchangedAndANDed(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	seedFilterDocs(t, s)

	if _, active := s.allowedDocIDs("code", nil, nil); active {
		t.Fatal("no filter must mean no restriction")
	}
	ids, _ := s.allowedDocIDs("code", map[string][]string{"kind": {"code", "test"}}, nil)
	if got := filterKeys(t, s, ids); strings.Join(got, ",") != "a,b,c" {
		t.Fatalf("filterMeta alone: %v", got)
	}
	ids, _ = s.allowedDocIDs("code", map[string][]string{"kind": {"code", "test"}}, parseFilter(t, `{"path":{"prefix":"internal/"}}`))
	if got := filterKeys(t, s, ids); strings.Join(got, ",") != "a,b" {
		t.Fatalf("filterMeta AND filter: %v", got)
	}
	// Same key in both: equality list narrowed by the operator's in.
	ids, _ = s.allowedDocIDs("code", map[string][]string{"kind": {"code", "test"}}, parseFilter(t, `{"kind":{"in":["test"]}}`))
	if got := filterKeys(t, s, ids); strings.Join(got, ",") != "b" {
		t.Fatalf("same key in both: %v", got)
	}
	// Same key, operator without in: equality list kept.
	ids, _ = s.allowedDocIDs("code", map[string][]string{"kind": {"code"}}, parseFilter(t, `{"kind":{"exists":true}}`))
	if got := filterKeys(t, s, ids); strings.Join(got, ",") != "a,c" {
		t.Fatalf("same key, no in: %v", got)
	}
	// Empty equality list has always matched nothing.
	ids, _ = s.allowedDocIDs("code", map[string][]string{"kind": {}}, nil)
	if len(ids) != 0 {
		t.Fatalf("empty filterMeta values must match nothing, got %d", len(ids))
	}
	if got := s.getDocIDsByMeta("code", nil); len(got) != 0 {
		t.Fatalf("getDocIDsByMeta(nil) = %v", got)
	}
}

func TestMetaCondition_UnmarshalErrors(t *testing.T) {
	bad := map[string]string{
		`{"path":{"startsWith":"x"}}`: `unsupported filter operator "startsWith"`,
		`{"path":"x"}`:                "must be an object",
		`{"path":{"gt":{"a":1}}}`:     `filter operator "gt"`,
		`{"path":{"in":[{"a":1}]}}`:   `filter operator "in"`,
		`{"path":{"exists":"yes"}}`:   `filter operator "exists"`,
		`{"path":{"not":[[1]]}}`:      `filter operator "not"`,
	}
	for raw, want := range bad {
		var f MetaFilter
		err := json.Unmarshal([]byte(raw), &f)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: err=%v, want containing %q", raw, err, want)
		}
	}
	f := parseFilter(t, `{"n":{"gte":true,"lt":"5"}}`)
	if *f["n"].Gte != "true" || *f["n"].Lt != "5" {
		t.Fatalf("scalars: %+v", f["n"])
	}
}

func TestMetaFilter_HelpersEdgeCases(t *testing.T) {
	s0, cleanup := newHandlerTestServer(t)
	defer cleanup()
	if _, _, ok := splitMetaEntry([]byte("novalue"), "c"); ok {
		t.Fatal("entry without separator must be rejected")
	}
	if v, id, ok := splitMetaEntry([]byte("a|b|c|k|en"), "c"); !ok || v != "a|b" || id != "c|k|en" {
		t.Fatalf("value with separator: %q %q", v, id)
	}
	if compareMeta("2", "10") >= 0 || compareMeta("b", "a") <= 0 || compareMeta("1.0", "1") != 0 {
		t.Fatal("compareMeta")
	}
	if got := mergeFilters(nil, nil); got != nil {
		t.Fatalf("mergeFilters(nil,nil) = %v", got)
	}
	if got := mergeFilters(nil, MetaFilter{"k": {Prefix: "x"}}); got["k"].Prefix != "x" {
		t.Fatalf("mergeFilters(nil,f) = %v", got)
	}
	if !matchesMeta(map[string][]string{"a": {"1"}}, nil) {
		t.Fatal("empty filterMeta matches everything")
	}
	if got := valueMatches(nil, "c", "k", MetaCondition{}); len(got) != 0 {
		t.Fatal("nil bucket")
	}
	_ = s0.DB.View(func(tx *bolt.Tx) error {
		if got := metaFilterIDs(tx, "code", MetaFilter{}); len(got) != 0 {
			t.Fatalf("empty filter on the index = %v", got)
		}
		return nil
	})
	if got := sortedIDs(map[string]bool{"b": true, "a": true}); strings.Join(got, ",") != "a,b" {
		t.Fatalf("sortedIDs = %v", got)
	}
}

func TestMetaFilter_SurfacesHTTP(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	seedFilterDocs(t, s)

	// /v1/search
	rec := doRequest(t, s.handleSearch, map[string]any{
		"collection": "code",
		"filter":     map[string]any{"path": map[string]any{"prefix": "internal/"}},
		"sort":       "key", "asc": true,
	})
	var docs []struct{ Key string }
	if err := json.Unmarshal(rec.Body.Bytes(), &docs); err != nil || len(docs) != 2 || docs[0].Key != "a" {
		t.Fatalf("/v1/search: %d %s", rec.Code, rec.Body.String())
	}

	// Unknown operator is a 400 naming it.
	rec = doRequest(t, s.handleSearch, map[string]any{
		"collection": "code", "filter": map[string]any{"path": map[string]any{"regex": "x"}},
	})
	if rec.Code != 400 || !strings.Contains(rec.Body.String(), "regex") {
		t.Fatalf("unknown operator: %d %s", rec.Code, rec.Body.String())
	}

	// FTS
	resp, err := s.searchFTS(FTSSearchRequest{
		Collection: "code", Query: "hnsw", Filter: parseFilter(t, `{"kind":{"not":["test"]}}`),
	}, time.Now())
	if err != nil || len(resp.Results) != 1 || resp.Results[0].Document.Key != "a" {
		t.Fatalf("fts filter: %v %+v", err, resp)
	}
	resp, err = s.searchFTS(FTSSearchRequest{
		Collection: "code", Query: "hnsw", Filter: parseFilter(t, `{"kind":{"in":["none"]}}`),
	}, time.Now())
	if err != nil || len(resp.Results) != 0 {
		t.Fatalf("fts empty filter: %v %+v", err, resp)
	}

	// Aggregate
	rec = doRequest(t, s.handleAggregate, map[string]any{
		"collection": "code",
		"filter":     map[string]any{"size": map[string]any{"lt": 10000}},
		"facets":     []any{map[string]any{"field": "kind"}},
	})
	var agg AggregateResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &agg); err != nil || agg.TotalDocs != 2 {
		t.Fatalf("aggregate: %d %s", rec.Code, rec.Body.String())
	}
}

func TestMetaFilter_MCPTools(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	seedFilterDocs(t, s)
	tools := &MCPToolServer{client: NewDirectClient(s)}
	ctx := context.Background()

	out, err := tools.toolSearchDocuments(ctx, map[string]interface{}{
		"collection": "code",
		"filter":     map[string]interface{}{"summary": map[string]interface{}{"exists": true}},
	})
	if err != nil || !strings.Contains(out, `"c"`) || strings.Contains(out, `"key": "a"`) {
		t.Fatalf("search_documents: %v %s", err, out)
	}
	out, err = tools.toolFTSSearch(ctx, map[string]interface{}{
		"collection": "code", "query": "hnsw",
		"filter": map[string]interface{}{"path": map[string]interface{}{"contains": "_test"}},
	})
	if err != nil || !strings.Contains(out, "hnsw_test.go") || strings.Contains(out, `"internal/vector/hnsw.go"`) {
		t.Fatalf("full_text_search: %v %s", err, out)
	}
	out, err = tools.toolAggregate(ctx, map[string]interface{}{
		"collection": "code",
		"filter":     map[string]interface{}{"path": map[string]interface{}{"prefix": "cmd/"}},
	})
	if err != nil || !strings.Contains(out, `"totalDocs": 1`) {
		t.Fatalf("aggregate: %v %s", err, out)
	}
	for name, fn := range map[string]func(context.Context, map[string]interface{}) (string, error){
		"search_documents": tools.toolSearchDocuments,
		"semantic_search":  tools.toolSemanticSearch,
		"full_text_search": tools.toolFTSSearch,
		"hybrid_search":    tools.toolHybridSearch,
		"cross_search":     tools.toolCrossSearch,
		"aggregate":        tools.toolAggregate,
	} {
		_, err := fn(ctx, map[string]interface{}{
			"collection": "code", "query": "x", "target_collections": []interface{}{"code"},
			"filter": map[string]interface{}{"path": map[string]interface{}{"like": "x"}},
		})
		if err == nil || !strings.Contains(err.Error(), `"like"`) {
			t.Errorf("%s: unknown operator must be rejected, got %v", name, err)
		}
	}
	if _, err := mcpGetFilter(map[string]interface{}{"filter": func() {}}); err == nil {
		t.Fatal("unmarshalable filter must fail")
	}
}

func TestRangeMetaAsFilter(t *testing.T) {
	base := MetaFilter{"size": {Prefix: "1"}}
	f, rest := rangeMetaAsFilter(base, []RangeFilter{
		{Field: "size", Gte: "10", Lt: "20"},
		{Field: "size", Gte: "11"}, // gte already set: stays a post-filter
		{Field: "addedAt", Gte: "2026-01-01"},
		{Field: "UpdatedAt", Lt: "2027-01-01"},
	})
	if *f["size"].Gte != "10" || *f["size"].Lt != "20" || f["size"].Prefix != "1" {
		t.Fatalf("folded: %+v", f["size"])
	}
	if len(rest) != 3 {
		t.Fatalf("rest = %+v", rest)
	}
	if _, ok := base["size"]; !ok || base["size"].Gte != nil {
		t.Fatal("caller's filter must not be mutated")
	}
	if f, rest := rangeMetaAsFilter(base, nil); len(rest) != 0 || f["size"].Prefix != "1" {
		t.Fatal("no ranges is a no-op")
	}
}

func TestCompareMeta_Dates(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"2026-09-15", "2026-09-01", 1},
		{"2026-09-01T10:00:00Z", "2026-09-01", 1},
		{"2026/01/02", "2026-01-02", 0},
		{"2026-01-02", "1767225600", 1}, // date against a unix timestamp (2026-01-01)
		{"abc", "abd", -1},
		{"10", "9", 1},
	}
	for _, tc := range cases {
		if got := compareMeta(tc.a, tc.b); got != tc.want {
			t.Errorf("compareMeta(%q,%q) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestFTS_RangeMetaUsesIndex(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	seedFilterDocs(t, s)
	resp, err := s.searchFTS(FTSSearchRequest{
		Collection: "code", Query: "hnsw",
		RangeMeta: []RangeFilter{{Field: "modified", Gte: "2026-09-01"}, {Field: "addedAt", Gte: "0"}},
	}, time.Now())
	if err != nil || len(resp.Results) != 1 || resp.Results[0].Document.Key != "a" {
		t.Fatalf("rangeMeta through the index: %v %+v", err, resp)
	}
}

func TestMetaCondition_MarshalKeepsEmptyIn(t *testing.T) {
	empty, _ := json.Marshal(filterMetaAsFilter(map[string][]string{"k": {}}))
	none, _ := json.Marshal(MetaFilter{"k": {}})
	if string(empty) == string(none) {
		t.Fatalf("empty in and no in serialize alike: %s", empty)
	}
	if !strings.Contains(string(empty), `"in":[]`) {
		t.Fatalf("empty in: %s", empty)
	}
	round := parseFilter(t, string(empty))
	if !round["k"].hasIn || len(round["k"].In) != 0 {
		t.Fatalf("round trip lost the empty in: %+v", round["k"])
	}
	full, _ := json.Marshal(MetaFilter{"k": {In: []string{"a"}, Prefix: "p"}})
	if !strings.Contains(string(full), `"in":["a"]`) || !strings.Contains(string(full), `"prefix":"p"`) {
		t.Fatalf("full: %s", full)
	}
}
