package main

import (
	"context"
	"encoding/json"
	"flag"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	gql "mddb/graphql"
	proto "mddb/proto"
)

// The decomposition golden (SRCH-020). A multi-topic query — two needs joined
// by "and", the case decomposition splits — must answer exactly as the server
// did before queryDecomposition existed, both when the request leaves the
// field out and when it says "off". The files in
// testdata/decomposition/golden were written by this test on the commit
// before SRCH-020 (80695cb) and are never rewritten by the change itself;
// regenerate them only on purpose, with
//
//	go test -run TestQueryDecomposition_GoldenOff -update-decomposition-golden
//
// and review the diff: any diff is a change to what every client receives.

var updateDecompositionGolden = flag.Bool("update-decomposition-golden", false, "rewrite testdata/decomposition/golden")

const decompositionGoldenDir = "testdata/decomposition/golden"

// decompositionGoldenQuery holds two topics of the golden collection.
const decompositionGoldenQuery = "how is the pump seal replaced and what do the meeting notes say about the budget"

// decompositionResponses runs every vector-backed search surface once with
// the multi-topic query and mode as queryDecomposition ("" leaves it out),
// and returns each body, normalized, by name.
func decompositionResponses(t *testing.T, s *Server, mode string) map[string]string {
	t.Helper()
	ctx := context.Background()
	out := map[string]string{}
	rest := func(name string, h http.HandlerFunc, req map[string]any) {
		if mode != "" {
			req["queryDecomposition"] = mode
		}
		rec := doRequest(t, h, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", name, rec.Code, rec.Body.String())
		}
		out[name] = goldenNormalize(rec.Body.Bytes())
	}
	asJSON := func(name string, v any, err error) {
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		out[name] = goldenNormalize(b)
	}
	q := decompositionGoldenQuery
	rest("rest-vector", s.handleVectorSearch, map[string]any{"collection": "gold", "query": q, "topK": 3, "explain": true})
	rest("rest-vector-chunk", s.handleVectorSearch, map[string]any{"collection": "gold", "query": q, "topK": 3, "retrievalMode": "chunk"})
	rest("rest-hybrid", s.handleHybridSearch, map[string]any{"collection": "gold", "query": q, "topK": 3, "explain": true})

	mcp := NewDirectClient(s)
	v, err := mcp.VectorSearch(ctx, &MCPVectorSearchRequest{Collection: "gold", Query: q, TopK: 3,
		PlanOptions: PlanOptions{Explain: true}, DecompositionOptions: DecompositionOptions{QueryDecomposition: mode}})
	asJSON("mcp-vector", v, err)
	h, err := mcp.HybridSearch(ctx, &MCPHybridSearchRequest{Collection: "gold", Query: q, TopK: 3,
		PlanOptions: PlanOptions{Explain: true}, DecompositionOptions: DecompositionOptions{QueryDecomposition: mode}})
	asJSON("mcp-hybrid", h, err)

	g := &GRPCServer{server: s}
	gv, err := g.VectorSearch(ctx, &proto.VectorSearchRequest{Collection: "gold", Query: q, TopK: 3, Explain: true, QueryDecomposition: mode})
	asJSON("grpc-vector", gv, err)
	gh, err := g.HybridSearch(ctx, &proto.HybridSearchRequest{Collection: "gold", Query: q, TopK: 3, Explain: true, QueryDecomposition: mode})
	asJSON("grpc-hybrid", gh, err)

	a := &GraphQLAdapter{server: s, mcp: mcp}
	three, explain := 3, true
	in := gql.VectorSearchInput{Collection: "gold", Query: &q, TopK: &three, Explain: &explain}
	if mode != "" {
		in.QueryDecomposition = &mode
	}
	qv, err := a.VectorSearch(ctx, in)
	asJSON("graphql-vector", qv, err)
	return out
}

func TestQueryDecomposition_GoldenOff(t *testing.T) {
	s, cleanup := goldenServer(t)
	defer cleanup()
	if *updateDecompositionGolden {
		if err := os.MkdirAll(decompositionGoldenDir, 0o750); err != nil {
			t.Fatal(err)
		}
		for name, body := range decompositionResponses(t, s, "") {
			if err := os.WriteFile(filepath.Join(decompositionGoldenDir, name+".json"), []byte(body+"\n"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		return
	}
	for _, mode := range []string{"", QueryDecompositionOff} {
		for name, body := range decompositionResponses(t, s, mode) {
			want, err := os.ReadFile(filepath.Join(decompositionGoldenDir, name+".json")) // #nosec G304 -- a test fixture path
			if err != nil {
				t.Fatalf("%s: %v (regenerate with -update-decomposition-golden)", name, err)
			}
			if !sameJSON(t, body, string(want)) {
				t.Errorf("mode %q, %s differs from the golden:\ngot  %s\nwant %s", mode, name, body, want)
			}
		}
	}
}
