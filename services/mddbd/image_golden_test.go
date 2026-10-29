package main

import (
	"context"
	"encoding/json"
	"flag"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	gql "mddb/graphql"
	"mddb/internal/embedding"
	proto "mddb/proto"
)

// The image golden (RAG-010). A search that does not ask for images, on a
// collection that holds none, must answer exactly as the server did before
// images existed. The files in testdata/images/golden were written by this
// test on the release before RAG-010 (311776e) and are never rewritten by the
// change itself; regenerate them only on purpose, with
//
//	go test -tags noasm -run TestImages_GoldenWithoutImages -update-image-golden
//
// and review the diff: any diff is a change to what every client receives.

var updateImageGolden = flag.Bool("update-image-golden", false, "rewrite testdata/images/golden")

const imageGoldenDir = "testdata/images/golden"

// goldenVolatile matches the fields that differ on every run: write times,
// and the durations of a search or a wait.
var goldenVolatile = regexp.MustCompile(`"(addedAt|updatedAt|added_at|updated_at|durationMs|waitedMs|oldestPendingAt|AddedAt|UpdatedAt)":\s*("[^"]*"|-?[0-9.e+]+)`)

// goldenTerms matches a list of matched terms, which the full-text index
// reports in map order.
var goldenTerms = regexp.MustCompile(`"(matchedTerms|matched_terms|MatchedTerms)":\[([^\]]*)\]`)

// goldenNormalize blanks the volatile fields of a JSON body and sorts its
// lists of matched terms.
func goldenNormalize(b []byte) string {
	out := goldenVolatile.ReplaceAllString(string(b), `"$1":0`)
	return goldenTerms.ReplaceAllStringFunc(out, func(m string) string {
		sub := goldenTerms.FindStringSubmatch(m)
		terms := strings.Split(sub[2], ",")
		sort.Strings(terms)
		return `"` + sub[1] + `":[` + strings.Join(terms, ",") + `]`
	})
}

// goldenServer is a collection of three documents, one of them Markdown with
// image links, embedded with the lexical provider so the vectors are the same
// on every run and need no network.
func goldenServer(t *testing.T) (*Server, func()) {
	t.Helper()
	t.Setenv("MDDB_SEARCH_STATS", "false")
	s, cleanup := newHandlerTestServer(t)
	s.VectorIndex.SetReady()
	for _, sr := range s.VectorSearchers {
		sr.SetReady()
	}
	s.Embedding = embedding.NewLexicalProvider(256)
	s.startEmbeddingWorker(s.Embedding)
	addTestDoc(t, s, "gold", "manual", "en", "# Pump manual\n\nThe pump seal keeps water out of the motor.\n\n![Exploded view of the pump seal](img/seal.png \"Figure 1: the seal\")\n\nReplace the seal every year.", nil)
	addTestDoc(t, s, "gold", "valves", "en", "Valves control the flow of water through the pump housing.", nil)
	addTestDoc(t, s, "gold", "notes", "en", "Meeting notes about the budget for next quarter.", nil)
	deadline := time.Now().Add(10 * time.Second)
	for s.IndexingStatus("gold").PendingEmbeddings > 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	return s, func() { s.EmbeddingWorker.Stop(); cleanup() }
}

// goldenResponses runs every search surface once without images and returns
// each body, normalized, by name.
func goldenResponses(t *testing.T, s *Server) map[string]string {
	t.Helper()
	ctx := context.Background()
	out := map[string]string{}
	rest := func(name string, h http.HandlerFunc, req map[string]any) {
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
	rest("rest-fts", s.handleFTS, map[string]any{"collection": "gold", "query": "pump seal", "algorithm": "bm25"})
	rest("rest-fts-passages", s.handleFTS, map[string]any{"collection": "gold", "query": "pump seal", "format": "passages"})
	rest("rest-vector", s.handleVectorSearch, map[string]any{"collection": "gold", "query": "pump seal water", "topK": 3, "includeContent": true, "explain": true})
	rest("rest-vector-passages", s.handleVectorSearch, map[string]any{"collection": "gold", "query": "pump seal water", "format": "passages"})
	rest("rest-hybrid", s.handleHybridSearch, map[string]any{"collection": "gold", "query": "pump seal", "topK": 3, "explain": true})

	mcp := NewDirectClient(s)
	v, err := mcp.VectorSearch(ctx, &MCPVectorSearchRequest{Collection: "gold", Query: "pump seal water", TopK: 3, IncludeContent: true})
	asJSON("mcp-vector", v, err)
	h, err := mcp.HybridSearch(ctx, &MCPHybridSearchRequest{Collection: "gold", Query: "pump seal", TopK: 3})
	asJSON("mcp-hybrid", h, err)
	f, err := mcp.FTSSearch(ctx, &MCPFTSSearchRequest{Collection: "gold", Query: "pump seal", IncludeContent: true})
	asJSON("mcp-fts", f, err)
	st, err := mcp.IndexingStatus(ctx, "gold")
	asJSON("mcp-indexing-status", st, err)

	g := &GRPCServer{server: s}
	gv, err := g.VectorSearch(ctx, &proto.VectorSearchRequest{Collection: "gold", Query: "pump seal water", TopK: 3, IncludeContent: true})
	asJSON("grpc-vector", gv, err)
	gh, err := g.HybridSearch(ctx, &proto.HybridSearchRequest{Collection: "gold", Query: "pump seal", TopK: 3, IncludeContent: true})
	asJSON("grpc-hybrid", gh, err)
	gf, err := g.FTS(ctx, &proto.FTSRequest{Collection: "gold", Query: "pump seal"})
	asJSON("grpc-fts", gf, err)
	gs, err := g.GetIndexingStatus(ctx, &proto.IndexingStatusRequest{Collection: "gold"})
	asJSON("grpc-indexing-status", gs, err)

	a := &GraphQLAdapter{server: s, mcp: mcp}
	query, three := "pump seal water", 3
	qv, err := a.VectorSearch(ctx, gql.VectorSearchInput{Collection: "gold", Query: &query, TopK: &three})
	asJSON("graphql-vector", qv, err)
	qf, err := a.FullTextSearch(ctx, gql.FTSInput{Collection: "gold", Query: "pump seal"})
	asJSON("graphql-fts", qf, err)
	return out
}

func TestImages_GoldenWithoutImages(t *testing.T) {
	s, cleanup := goldenServer(t)
	defer cleanup()
	got := goldenResponses(t, s)
	if *updateImageGolden {
		if err := os.MkdirAll(imageGoldenDir, 0o750); err != nil {
			t.Fatal(err)
		}
		for name, body := range got {
			if err := os.WriteFile(filepath.Join(imageGoldenDir, name+".json"), []byte(body+"\n"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		return
	}
	for name, body := range got {
		want, err := os.ReadFile(filepath.Join(imageGoldenDir, name+".json")) // #nosec G304 -- a test fixture path
		if err != nil {
			t.Fatalf("%s: %v (regenerate with -update-image-golden)", name, err)
		}
		if !sameJSON(t, body, string(want)) {
			t.Errorf("%s differs from the golden:\ngot  %s\nwant %s", name, body, want)
		}
	}
}

// sameJSON compares two JSON bodies exactly — shape, order, text, integers —
// except that fractional numbers may differ by one part in 100,000. Scores
// come from float32 maths whose last bits depend on the build: the SIMD cosine
// (the default) and the scalar one (-tags noasm) differ in the seventh digit,
// and rounding both to a fixed precision still splits them at a boundary.
func sameJSON(t *testing.T, got, want string) bool {
	t.Helper()
	return sameValue(decodeNumbers(t, got), decodeNumbers(t, want))
}

func decodeNumbers(t *testing.T, body string) any {
	t.Helper()
	dec := json.NewDecoder(strings.NewReader(body))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		t.Fatalf("golden body is not JSON: %v", err)
	}
	return v
}

func sameValue(a, b any) bool {
	switch x := a.(type) {
	case map[string]any:
		y, ok := b.(map[string]any)
		if !ok || len(x) != len(y) {
			return false
		}
		for k, v := range x {
			if w, ok := y[k]; !ok || !sameValue(v, w) {
				return false
			}
		}
		return true
	case []any:
		y, ok := b.([]any)
		if !ok || len(x) != len(y) {
			return false
		}
		for i := range x {
			if !sameValue(x[i], y[i]) {
				return false
			}
		}
		return true
	case json.Number:
		y, ok := b.(json.Number)
		if !ok {
			return false
		}
		if x == y {
			return true
		}
		if !strings.ContainsAny(x.String()+y.String(), ".eE") {
			return false // integers compare exactly
		}
		f, err1 := x.Float64()
		g, err2 := y.Float64()
		return err1 == nil && err2 == nil && math.Abs(f-g) <= 1e-5*math.Max(1, math.Abs(g))
	default:
		return a == b
	}
}
