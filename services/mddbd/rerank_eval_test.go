package main

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	json "mddb/internal/jsonx"
	proto "mddb/proto"
)

// RAG-011 acceptance: /v1/eval compares a configuration with and without the
// reranking model (nDCG@10, p95), and the Search Advisor recommends the model
// only when that comparison says it helps.

func rerankEvalConfigs() []EvalConfig {
	on, off := true, false
	var out []EvalConfig
	for _, st := range []string{SearchTypeFTS, SearchTypeHybrid, SearchTypeVector} {
		for _, enabled := range []*bool{&off, &on} {
			c := EvalConfig{SearchType: st, Rerank: &RerankOptions{Enabled: enabled}}
			if st == SearchTypeFTS {
				c.Algorithm = "bm25"
			}
			out = append(out, c)
		}
	}
	return out
}

func TestRerank_EvalComparesWithAndWithout(t *testing.T) {
	s, f := rerankServer(t)
	queries := []EvalQuery{{ID: "q1", Query: rerankQuery, ExpectedKeys: []string{"gamma"}}}
	res, err := s.RunEval(context.Background(), EvalRequest{Collection: "rr", Queries: queries, K: 3, Configs: rerankEvalConfigs()})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < len(res.Results); i += 2 {
		off, on := res.Results[i], res.Results[i+1]
		if off.Error != "" || on.Error != "" || on.NDCG != 1 || off.NDCG >= on.NDCG || on.RerankSkipped != 0 {
			t.Fatalf("%s vs %s: %+v %+v", off.Config, on.Config, off, on)
		}
		if !strings.HasSuffix(on.Config, " rerank=true") || !strings.HasSuffix(off.Config, " rerank=false") || on.P95Ms < on.P50Ms {
			t.Fatalf("labels and latency: %+v %+v", off, on)
		}
	}
	// A model that fails is counted, and the search's own order is scored.
	f.code.Store(http.StatusInternalServerError)
	res, err = s.RunEval(context.Background(), EvalRequest{Collection: "rr", Queries: queries, K: 3, Configs: rerankEvalConfigs()})
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i < len(res.Results); i += 2 {
		if res.Results[i].RerankSkipped != 1 || res.Results[i].NDCG != res.Results[i-1].NDCG {
			t.Fatalf("skipped: %+v", res.Results[i])
		}
	}
	// An invalid rerank block is the configuration's error.
	res, _ = s.RunEval(context.Background(), EvalRequest{Collection: "rr", Queries: queries,
		Configs: []EvalConfig{{SearchType: SearchTypeFTS, Rerank: &RerankOptions{Candidates: -1}}}})
	if res.Results[0].Error == "" {
		t.Fatal("invalid rerank accepted")
	}
}

func TestRerank_AdvisorMeasuresTheModel(t *testing.T) {
	s, f := rerankServer(t)
	// No evaluation set: the model is named, not recommended.
	rec, err := s.RecommendSearch("rr")
	if err != nil || rec.RerankModel != "fake-rerank" || rec.Rerank != nil || !strings.Contains(strings.Join(rec.Reasons, " "), "fake-rerank") {
		t.Fatalf("no set: %v %+v", err, rec)
	}
	if _, err := s.AddEvalQuery("rr", EvalQuery{Query: rerankQuery, ExpectedKeys: []string{"gamma"}}); err != nil {
		t.Fatal(err)
	}
	rec, err = s.RecommendSearch("rr")
	if err != nil || rec.Rerank == nil || !*rec.Rerank.Enabled || rec.RetrievalProfile.Rerank == nil {
		t.Fatalf("helps: %v %+v %v", err, rec, rec.Warnings)
	}
	// gRPC carries it.
	g := &GRPCServer{server: s}
	gr, err := g.SearchAdvisor(context.Background(), &proto.SearchAdvisorRequest{Collection: "rr"})
	if err != nil || gr.RerankModel != "fake-rerank" || !gr.Rerank.GetEnabled() {
		t.Fatalf("grpc: %v %+v", err, gr)
	}
	// A model that does not help stays off.
	f.overlap.Store(true)
	if _, err := s.AddEvalQuery("rr", EvalQuery{Query: "bread flour", ExpectedKeys: []string{"delta"}}); err != nil {
		t.Fatal(err)
	}
	f.body.Store(`{"results":[]}`) // scores nothing: the order cannot improve
	rec, _ = s.RecommendSearch("rr")
	if rec.Rerank != nil || !strings.Contains(strings.Join(rec.Reasons, " "), "did not improve") {
		t.Fatalf("no gain: %+v", rec.Reasons)
	}
	// A failing model is a warning, not a recommendation.
	f.code.Store(http.StatusBadGateway)
	rec, _ = s.RecommendSearch("rr")
	if rec.Rerank != nil || !strings.Contains(strings.Join(rec.Warnings, " "), "skipped") {
		t.Fatalf("failing: %+v", rec.Warnings)
	}
	// Nothing configured: nothing said.
	s.setRerank(nil, 0, "")
	rec, _ = s.RecommendSearch("rr")
	if rec.RerankModel != "" || strings.Contains(strings.Join(rec.Reasons, " "), "rerank") {
		t.Fatalf("off: %+v", rec)
	}
}

func TestRerank_AdviseRerankFailures(t *testing.T) {
	rec := &SearchRecommendation{SearchType: SearchTypeVector, RerankModel: "m"}
	if c := rec.evalConfig(true); c.Algorithm != "" || !*c.Rerank.Enabled {
		t.Fatalf("vector config: %+v", c)
	}
	adoptRerank(rec, &EvalResult{Results: []EvalConfigResult{{Error: "x"}, {}}})
	if len(rec.Warnings) != 1 {
		t.Fatalf("failed comparison: %v", rec.Warnings)
	}
	s, _ := rerankServer(t)
	rec = &SearchRecommendation{SearchType: "nonsense", Measured: &EvalResult{}}
	s.adviseRerank(rec, "")
	if len(rec.Warnings) != 1 {
		t.Fatalf("eval error: %v", rec.Warnings)
	}
}

// The comparison on the documentation's own evaluation set. The fake model
// scores a passage by the query words it holds, so the numbers measure the
// plumbing — candidates fetched, passages sent, order applied, latency
// counted — not a model's quality.
func TestRerank_DocsSetComparison(t *testing.T) {
	raw, err := os.ReadFile("../../tools/bench/evalsets/docs.json")
	if err != nil {
		t.Skip("evaluation set not in this checkout")
	}
	var set struct {
		Queries []EvalQuery `json:"queries"`
	}
	if err := json.Unmarshal(raw, &set); err != nil {
		t.Fatal(err)
	}
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	pages, _ := filepath.Glob("../../docs/*.md")
	for _, p := range pages {
		content, err := os.ReadFile(p) //nolint:gosec // G304: test fixture path
		if err != nil {
			t.Fatal(err)
		}
		addTestDoc(t, s, "docs", filepath.Base(p), "en", string(content), nil)
	}
	f := newFakeReranker(t)
	f.overlap.Store(true)
	useReranker(s, f, 5*time.Second, 0)
	on, off := true, false
	res, err := s.RunEval(context.Background(), EvalRequest{Collection: "docs", Queries: set.Queries, Configs: []EvalConfig{
		{SearchType: SearchTypeFTS, Algorithm: "bm25", Rerank: &RerankOptions{Enabled: &off}},
		{SearchType: SearchTypeFTS, Algorithm: "bm25", Rerank: &RerankOptions{Enabled: &on}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range res.Results {
		if r.Error != "" || r.RerankSkipped != 0 {
			t.Fatalf("%+v", r)
		}
		t.Logf("%-28s nDCG@%d %.3f recall %.3f MRR %.3f p50 %.2f ms p95 %.2f ms", r.Config, res.K, r.NDCG, r.Recall, r.MRR, r.P50Ms, r.P95Ms)
	}
	if f.calls.Load() != int64(len(set.Queries)) {
		t.Fatalf("model calls %d for %d queries", f.calls.Load(), len(set.Queries))
	}
}
