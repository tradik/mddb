package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	json "mddb/internal/jsonx"
)

// TestEval_DocsSetHoldsTheLine is the CI gate of SRCH-019: the MDDB
// documentation, searched with its own evaluation set, must keep finding the
// right pages. Keyword search only — no embedding model in CI. The floors sit
// just under what 2.18.8 measured on this test server (recall@10 0.750,
// nDCG@10 0.683; a default-configured server scores 0.825 / 0.739, see
// docs/BENCHMARK.md), so a ranking change that loses a page fails here
// instead of in a user's agent.
func TestEval_DocsSetHoldsTheLine(t *testing.T) {
	raw, err := os.ReadFile("../../tools/bench/evalsets/docs.json")
	if err != nil {
		t.Skip("evaluation set not in this checkout")
	}
	var set struct {
		Queries []EvalQuery `json:"queries"`
	}
	if err := json.Unmarshal(raw, &set); err != nil || len(set.Queries) < 20 {
		t.Fatalf("docs.json: %v (%d queries)", err, len(set.Queries))
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
	res, err := s.RunEval(context.Background(), EvalRequest{Collection: "docs", Queries: set.Queries,
		Configs: []EvalConfig{{SearchType: SearchTypeFTS, Algorithm: "bm25"}}, Details: true})
	if err != nil {
		t.Fatal(err)
	}
	r := res.Results[0]
	if r.Recall < 0.7 || r.NDCG < 0.65 {
		for _, q := range r.PerQuery {
			if q.Recall < 1 {
				t.Logf("missed %s: %q -> %v", q.ID, q.Query, q.Returned)
			}
		}
		t.Fatalf("docs eval below the floor: recall@10 %.3f (min 0.7), nDCG@10 %.3f (min 0.65)", r.Recall, r.NDCG)
	}
}
