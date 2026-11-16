package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"mddb/internal/embedding"
	"mddb/internal/storage"
)

// GO-052: the fan-out's parts and how they merge, branch by branch.

// failing answers every request with 500.
func failing(t *testing.T) string {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"error":"down"}`, http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func TestPlacementAskPartFailures(t *testing.T) {
	tp := startTestPlacement(t, map[string]int{"meta": 1}, "meta")
	node := tp.leader("meta")
	get := func() *http.Request { return httptest.NewRequest(http.MethodGet, "/v1/stats", nil) }
	cases := []struct {
		name  string
		url   string
		group string
	}{
		{"a group without a leader", failing(t), "lost"},
		{"nobody listens", closedURL(t), "fake"},
		{"the group fails", failing(t), "fake"},
	}
	for _, c := range cases {
		extra := fakeGroup(t, c.url)
		extra["lost"] = []placementNode{{ID: "lost-1", GRPC: "127.0.0.1:1", HTTP: c.url}}
		p := unitRuntime(node, extra, nil)
		if _, err := p.askPart(get(), c.group, nil, here); err == nil {
			t.Errorf("%s: no error", c.name)
		}
	}
}

func TestPlacementStatsReportAGroupThatFails(t *testing.T) {
	tp := startTestPlacement(t, map[string]int{"meta": 1}, "meta")
	p := unitRuntime(tp.leader("meta"), fakeGroup(t, failing(t)), nil)
	w := httptest.NewRecorder()
	p.fanoutStats(w, httptest.NewRequest(http.MethodGet, "/v1/stats", nil), here)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"error":"node group \"fake\" answered 500`) {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
}

func TestPlacementStatsMergeHelpers(t *testing.T) {
	tp := startTestPlacement(t, map[string]int{"meta": 1}, "meta")
	p := unitRuntime(tp.leader("meta"), map[string][]placementNode{"g2": nil}, map[string]string{"theirs": "g2", "mine": "meta"})
	answer := map[string]any{"collections": []any{
		map[string]any{"name": "mine", "documentCount": 2.0},
		map[string]any{"name": "theirs", "documentCount": 5.0}, // a copy not dropped yet
	}}
	own, docs := p.ownStats("meta", answer)
	if len(own) != 1 || docs != 2 {
		t.Fatalf("own %v docs %d", own, docs)
	}
	out := map[string]any{"statsAgeSeconds": 3.0}
	mergeStatsFlags(out, map[string]any{"statsPending": true, "statsAgeSeconds": 9.0})
	mergeStatsFlags(out, map[string]any{"statsAgeSeconds": 1.0})
	if out["statsPending"] != true || out["statsAgeSeconds"] != 9.0 {
		t.Fatalf("flags %v", out)
	}
}

func TestPlacementFanoutCrossRefusals(t *testing.T) {
	tp := startTestPlacement(t, map[string]int{"meta": 1}, "meta")
	node := tp.leader("meta")
	p := unitRuntime(node, fakeGroup(t, failing(t)), map[string]string{"far": "fake"})
	cases := []struct {
		name, body string
		want       int
	}{
		{"not JSON", `{`, http.StatusBadRequest},
		{"a group fails", `{"targetCollections":["far","role:x"],"query":"q","searchType":"fts"}`, http.StatusServiceUnavailable},
	}
	for _, c := range cases {
		w := httptest.NewRecorder()
		p.fanoutCross(w, httptest.NewRequest(http.MethodPost, "/v1/cross-search", strings.NewReader(c.body)), here)
		if w.Code != c.want {
			t.Errorf("%s: %d, want %d: %s", c.name, w.Code, c.want, w.Body)
		}
	}
}

func TestPlacementPrepareCrossResolvesTheVectorOnce(t *testing.T) {
	tp := startTestPlacement(t, map[string]int{"meta": 1}, "meta")
	node := tp.leader("meta")
	p := unitRuntime(node, map[string][]placementNode{"g2": nil}, map[string]string{"far": "g2", "near": "meta"})
	saved, _, err := node.s.addDocument("near", "d", "en", nil, "text", 0, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := node.s.VectorStore.Put("near", saved.ID, []float32{1, 2}, "test", ""); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	refused := []CrossSearchRequest{
		{SourceCollection: "far", SourceDocID: "x"},
		{SourceCollection: "near", SourceDocID: "nothing"},
		{Query: "boom"},
	}
	node.s.Embedding = failingPartEmbedding{Provider: embedding.NewLexicalProvider(8), fail: "boom"}
	defer func() { node.s.Embedding = nil }()
	for _, req := range refused {
		if err := p.prepareCross(ctx, &req); err == nil {
			t.Errorf("%+v: no error", req)
		}
	}
	src := CrossSearchRequest{SourceCollection: "near", SourceDocID: saved.ID}
	if err := p.prepareCross(ctx, &src); err != nil || !reflect.DeepEqual(src.QueryVector, []float32{1, 2}) {
		t.Fatalf("source vector: %v %v", src.QueryVector, err)
	}
	q := CrossSearchRequest{Query: "words"}
	if err := p.prepareCross(ctx, &q); err != nil || len(q.QueryVector) == 0 {
		t.Fatalf("embedded query: %v %v", q.QueryVector, err)
	}
	node.s.Embedding = nil
	none := CrossSearchRequest{Query: "words"}
	if err := p.prepareCross(ctx, &none); err != nil || none.QueryVector != nil {
		t.Fatalf("no provider: %v %v", none.QueryVector, err)
	}
}

func TestPlacementCrossMergeHelpers(t *testing.T) {
	if n, rep := mergedLimit(0, []crossPart{{LimitReport: LimitReport{Limit: 4, LimitCapped: true}}, {}}); n != 4 || !rep.LimitCapped {
		t.Fatalf("limit %d %+v", n, rep)
	}
	hit := func(c string, score float32) crossPartItem {
		return crossPartItem{Collection: c, Score: score, Document: storage.Doc{Key: c}}
	}
	parts := []crossPart{{Hits: []crossPartItem{hit("ref-b", 1), hit("a", 1)}}, {Hits: []crossPartItem{hit("ref-a", 1), hit("b", 2)}}}
	var order []string
	for _, it := range mergeParts([]string{"a", "role:reference", "z"}, parts) {
		order = append(order, it.Collection)
	}
	if strings.Join(order, ",") != "b,a,ref-a,ref-b" {
		t.Fatalf("merged order %v", order)
	}
	if partsAlgorithm([]crossPart{{}}) != "" || partsAlgorithm([]crossPart{{}, {Algorithm: "flat"}}) != "flat" {
		t.Fatal("partsAlgorithm")
	}
	waited := &IndexWaitResult{}
	if firstIndexing([]crossPart{{}, {Indexing: waited}}) != waited || firstIndexing(nil) != nil {
		t.Fatal("firstIndexing")
	}
	plan := mergedPlan([]crossPart{{}, {Plan: &SearchPlan{Reasons: []string{"one"}}},
		{Plan: &SearchPlan{Reasons: []string{"two"}, Collections: map[string]*SearchPlan{"c": {}}}}})
	if len(plan.Reasons) != 2 || plan.Collections["c"] == nil {
		t.Fatalf("plan %+v", plan)
	}
}

func TestPlacementCrossPassagesOverGroups(t *testing.T) {
	tp := startTestPlacement(t, map[string]int{"meta": 1, "g2": 1}, "meta")
	meta := tp.leader("meta")
	p := unitRuntime(meta, nil, nil)
	targets := []string{nameIn(t, p, "meta"), nameIn(t, p, "g2")}
	for _, c := range targets {
		owner, _ := p.owner(c)
		if err := tp.leader(owner).add(c, "k", "alpha passage text in "+c); err != nil {
			t.Fatal(err)
		}
	}
	var got struct {
		Passages []any `json:"passages"`
	}
	if _, err := meta.call(http.MethodPost, "/v1/cross-search", map[string]any{"targetCollections": targets,
		"query": "alpha", "searchType": "fts", "format": "passages"}, &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Passages) != 2 {
		t.Fatalf("passages: %v", got.Passages)
	}
}
