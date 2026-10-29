package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// SRCH-013 and SRCH-015 on GraphQL: the planner and relevance feedback reach
// vectorSearch through DirectClient.VectorSearch, like every other surface.

// gqlGoldenVectorQuery is a vectorSearch that uses none of the SRCH-013/015
// fields; its answer is pinned in testdata so adding them changes nothing.
const gqlGoldenVectorQuery = `{ vectorSearch(input: {collection: "fb", query: "x", topK: 5, includeContent: true}) {
  total model dimensions passages { key }
  results { score rank group groupRank document { key lang contentMd meta } } } }`

// gqlPost runs one GraphQL query through the HTTP handler and returns the
// raw response body.
func gqlPost(t *testing.T, s *Server, query string) []byte {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"query": query})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/graphql", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.newGraphQLHandler().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("graphql: %d %s", w.Code, w.Body.String())
	}
	return w.Body.Bytes()
}

func TestGraphQLVectorSearch_GoldenWithoutTheNewFields(t *testing.T) {
	s, _, cleanup := feedbackServer(t)
	defer cleanup()
	got := gqlPost(t, s, gqlGoldenVectorQuery)
	if os.Getenv("MDDB_UPDATE_GOLDEN") != "" {
		if err := os.WriteFile("testdata/graphql_vector_golden.json", got, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile("testdata/graphql_vector_golden.json")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(bytes.TrimSpace(got), bytes.TrimSpace(want)) {
		t.Fatalf("vectorSearch without the new fields changed:\n got %s\nwant %s", got, want)
	}
}

// gqlVector runs vectorSearch on the feedback corpus with extra input fields
// (GraphQL syntax) and returns the result keys and the plan.
func gqlVector(t *testing.T, s *Server, extra string) ([]string, map[string]any) {
	t.Helper()
	raw := gqlPost(t, s, `{ vectorSearch(input: {collection: "fb", query: "x", topK: 5, oversample: 5.0, `+extra+`}) {
  results { document { key } }
  plan { algorithm prefilter candidates iterativeScan reasons
    feedback { mode positive negative alpha beta gamma queryShift weight rescored } } } }`)
	var out struct {
		Data struct {
			VectorSearch struct {
				Results []struct {
					Document struct{ Key string }
				}
				Plan map[string]any
			}
		}
		Errors []struct{ Message string }
	}
	if err := json.Unmarshal(raw, &out); err != nil || len(out.Errors) > 0 {
		t.Fatalf("%v: %s", err, raw)
	}
	keys := make([]string, 0, len(out.Data.VectorSearch.Results))
	for _, r := range out.Data.VectorSearch.Results {
		keys = append(keys, r.Document.Key)
	}
	return keys, out.Data.VectorSearch.Plan
}

func TestGraphQLVectorSearch_AutoExplainReturnsThePlan(t *testing.T) {
	s, _, cleanup := feedbackServer(t)
	defer cleanup()
	if _, plan := gqlVector(t, s, `algorithm: "auto"`); plan != nil {
		t.Fatalf("a plan without explain: %v", plan)
	}
	keys, plan := gqlVector(t, s, `algorithm: "auto", explain: true, latencyTargetMs: 5`)
	if len(keys) != 5 || plan == nil || plan["algorithm"] == "" || len(plan["reasons"].([]any)) == 0 || plan["feedback"] != nil {
		t.Fatalf("auto explain: %v %v", keys, plan)
	}
	// The planner GraphQL reached is the one REST reaches: same choice.
	rest := doRequest(t, s.handleVectorSearch, map[string]any{"collection": "fb", "query": "x", "topK": 5, "oversample": 5.0,
		"algorithm": "auto", "explain": true, "latencyTargetMs": 5})
	if want := planOf(t, body(t, rest.Body.Bytes())); want["algorithm"] != plan["algorithm"] || want["candidates"] != plan["candidates"] {
		t.Fatalf("graphql plan %v, rest plan %v", plan, want)
	}
	if _, plan := gqlVector(t, s, `algorithm: "flat", explain: true`); plan["algorithm"] != "flat" {
		t.Fatalf("flat explain: %v", plan)
	}
}

// The SRCH-015 acceptance, through GraphQL: a negative top-1 leaves the top 5
// and a positive from position 21 enters it, in both modes, by key, ID and
// vector.
func TestGraphQLVectorSearch_FeedbackInBothModes(t *testing.T) {
	s, ids, cleanup := feedbackServer(t)
	defer cleanup()
	if keys, _ := gqlVector(t, s, ""); keys[0] != "f00" {
		t.Fatalf("corpus ranking: %v", keys)
	}
	f00, err := json.Marshal(corpusVector(0))
	if err != nil {
		t.Fatal(err)
	}
	negatives := map[string]string{"key": `{key: "f00"}`, "id": `{key: "` + ids["f00"] + `"}`, "vector": `{vector: ` + string(f00) + `}`}
	for _, mode := range []string{"query", "rescore"} {
		for name, neg := range negatives {
			if keys, _ := gqlVector(t, s, `feedbackMode: "`+mode+`", negative: [`+neg+`]`); rankOf(keys, "f00") != 0 {
				t.Fatalf("%s/%s: the negative top-1 is still in the top 5: %v", mode, name, keys)
			}
		}
		keys, plan := gqlVector(t, s, `feedbackMode: "`+mode+`", positive: [{key: "f20"}], explain: true, feedbackWeight: 1, rocchio: {beta: 1}`)
		if r := rankOf(keys, "f20"); r == 0 || r > 5 {
			t.Fatalf("%s: the positive from position 21 is not in the top 5: %v", mode, keys)
		}
		fb, _ := plan["feedback"].(map[string]any)
		if fb == nil || fb["mode"] != mode || fb["positive"] != 1.0 {
			t.Fatalf("%s: plan feedback %v", mode, plan)
		}
	}
}

// A refused input is a GraphQL error carrying REST's message, word for word.
func TestGraphQLVectorSearch_ErrorsMatchREST(t *testing.T) {
	s, _, cleanup := feedbackServer(t)
	defer cleanup()
	for name, c := range map[string]struct {
		gql  string
		rest map[string]any
	}{
		"bad mode":      {`feedbackMode: "nope", positive: [{key: "f01"}]`, map[string]any{"feedbackMode": "nope", "positive": []any{"f01"}}},
		"both":          {`positive: [{key: "f01", vector: [1]}]`, map[string]any{"positive": []any{map[string]any{"key": "f01", "vector": []float64{1}}}}},
		"neither":       {`negative: [{}]`, map[string]any{"negative": []any{map[string]any{}}}},
		"weight":        {`feedbackWeight: -1, positive: [{key: "f01"}]`, map[string]any{"feedbackWeight": -1, "positive": []any{"f01"}}},
		"rocchio":       {`rocchio: {gamma: 99}, positive: [{key: "f01"}]`, map[string]any{"rocchio": map[string]any{"gamma": 99}, "positive": []any{"f01"}}},
		"unknown key":   {`positive: [{key: "nope"}]`, map[string]any{"positive": []any{"nope"}}},
		"latency":       {`latencyTargetMs: -1`, map[string]any{"latencyTargetMs": -1}},
		"bad algorithm": {`algorithm: "nope"`, map[string]any{"algorithm": "nope"}},
	} {
		raw := gqlPost(t, s, `{ vectorSearch(input: {collection: "fb", query: "x", `+c.gql+`}) { total } }`)
		var out struct{ Errors []struct{ Message string } }
		if err := json.Unmarshal(raw, &out); err != nil || len(out.Errors) != 1 {
			t.Fatalf("%s: want one error: %v %s", name, err, raw)
		}
		c.rest["collection"], c.rest["query"] = "fb", "x"
		rec := doRequest(t, s.handleVectorSearch, c.rest)
		want, _ := body(t, rec.Body.Bytes())["error"].(string)
		if name == "bad algorithm" {
			// REST's HTTP writer alone appends ", available: …"; GraphQL, MCP
			// and gRPC return the planner's message itself.
			want = strings.TrimSuffix(want, ", available: auto, flat, hnsw, ivf, pq, quantized")
		}
		if rec.Code < 400 || want != out.Errors[0].Message {
			t.Fatalf("%s: graphql %q, rest %d %q", name, out.Errors[0].Message, rec.Code, want)
		}
	}
}

// reasons is a non-null list in the schema: a plan without one still answers [].
func TestSearchPlanToGQL_NoReasonsIsAnEmptyList(t *testing.T) {
	if p := searchPlanToGQL(&SearchPlan{Algorithm: "flat"}); p.Reasons == nil || len(p.Reasons) != 0 || p.Feedback != nil {
		t.Fatalf("plan: %+v", p)
	}
}
