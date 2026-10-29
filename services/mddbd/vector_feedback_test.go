package main

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"mddb/internal/embedding"
	vec "mddb/internal/vector"
)

// SRCH-015 acceptance corpus: 30 documents around the query direction e0.
// Document i is cos θ·e0 + sin θ·e(i+1) with cos θ = 0.90 − 0.01·i, so the
// ranking is f00, f01, ... f29, and any two documents overlap only through
// e0.
const feedbackDims = 31

// e0Embedding embeds every text as e0, the corpus's query direction.
type e0Embedding struct{}

func (e0Embedding) Embed(context.Context, string, embedding.Role) ([]float32, error) {
	v := make([]float32, feedbackDims)
	v[0] = 1
	return v, nil
}
func (e e0Embedding) EmbedBatch(ctx context.Context, texts []string, role embedding.Role) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i := range texts {
		out[i], _ = e.Embed(ctx, texts[i], role)
	}
	return out, nil
}
func (e0Embedding) Model() string   { return "e0" }
func (e0Embedding) Dimensions() int { return feedbackDims }

func corpusVector(i int) []float32 {
	v := make([]float32, feedbackDims)
	c := 0.90 - 0.01*float64(i)
	v[0], v[i+1] = float32(c), float32(math.Sqrt(1-c*c))
	return v
}

// feedbackServer holds the corpus in collection "fb"; it returns the
// document IDs by key.
func feedbackServer(t *testing.T) (*Server, map[string]string, func()) {
	t.Helper()
	s, cleanup := newHandlerTestServer(t)
	for _, sr := range s.VectorSearchers {
		sr.SetReady()
	}
	s.Embedding = e0Embedding{}
	ids := map[string]string{}
	for i := 0; i < 30; i++ {
		key := fmt.Sprintf("f%02d", i)
		doc := addTestDoc(t, s, "fb", key, "en", fmt.Sprintf("note %s about the release", key), nil)
		ids[key] = doc.ID
		v := corpusVector(i)
		if err := s.VectorStore.PutChunks("fb", doc.ID, []vec.ChunkEmbedding{{ChunkIndex: 0, Vector: v, ChunkHash: key}}, "test-model", key); err != nil {
			t.Fatal(err)
		}
		if err := s.indexChunk("fb", doc.ID+"#0", v, false); err != nil {
			t.Fatal(err)
		}
	}
	return s, ids, cleanup
}

// vectorKeys runs a REST vector search and returns the result keys in order.
func vectorKeys(t *testing.T, s *Server, req map[string]any) ([]string, map[string]any) {
	t.Helper()
	req["collection"] = "fb"
	rec := doRequest(t, s.handleVectorSearch, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	m := body(t, rec.Body.Bytes())
	var keys []string
	for _, r := range m["results"].([]any) {
		keys = append(keys, r.(map[string]any)["document"].(map[string]any)["key"].(string))
	}
	return keys, m
}

func rankOf(keys []string, key string) int {
	for i, k := range keys {
		if k == key {
			return i + 1
		}
	}
	return 0
}

// The ticket's acceptance: a negative top-1 leaves the top 5, a positive from
// position 20 enters it — in both modes, by key, by ID and by vector.
func TestFeedback_AcceptanceInBothModes(t *testing.T) {
	s, ids, cleanup := feedbackServer(t)
	defer cleanup()
	q, _ := e0Embedding{}.Embed(context.Background(), "", embedding.RoleQuery)

	base, _ := vectorKeys(t, s, map[string]any{"queryVector": q, "topK": 25})
	if base[0] != "f00" || rankOf(base, "f20") != 21 {
		t.Fatalf("corpus ranking: %v", base)
	}
	for _, mode := range []string{"query", "rescore"} {
		for name, neg := range map[string]any{"key": "f00", "id": ids["f00"], "vector": corpusVector(0)} {
			keys, _ := vectorKeys(t, s, map[string]any{"queryVector": q, "topK": 5, "oversample": 5.0,
				"feedbackMode": mode, "negative": []any{neg}})
			if rankOf(keys, "f00") != 0 {
				t.Fatalf("%s/%s: the negative top-1 is still in the top 5: %v", mode, name, keys)
			}
		}
		keys, _ := vectorKeys(t, s, map[string]any{"queryVector": q, "topK": 5, "oversample": 5.0,
			"feedbackMode": mode, "positive": []any{"f20"}})
		if r := rankOf(keys, "f20"); r == 0 || r > 5 {
			t.Fatalf("%s: the positive from position 21 is not in the top 5: %v", mode, keys)
		}
	}
}

// No feedback fields — or empty ones — is today's search, byte for byte.
func TestFeedback_GoldenWithoutExamples(t *testing.T) {
	s, _, cleanup := feedbackServer(t)
	defer cleanup()
	_, bare := vectorKeys(t, s, map[string]any{"query": "release", "topK": 10})
	_, empty := vectorKeys(t, s, map[string]any{"query": "release", "topK": 10, "positive": []any{}, "negative": []any{}, "feedbackMode": "rescore"})
	if !reflect.DeepEqual(bare, empty) {
		t.Fatalf("empty feedback changed the search:\n%v\n%v", bare, empty)
	}
}

func TestFeedback_ExplainShowsTheCorrection(t *testing.T) {
	s, _, cleanup := feedbackServer(t)
	defer cleanup()
	_, m := vectorKeys(t, s, map[string]any{"query": "x", "explain": true, "positive": []any{"f20"}, "negative": []any{"f00"},
		"rocchio": map[string]any{"beta": 1.0}})
	fb := planOf(t, m)["feedback"].(map[string]any)
	if fb["mode"] != "query" || fb["positive"] != 1.0 || fb["negative"] != 1.0 || fb["beta"] != 1.0 || fb["queryShift"].(float64) <= 0 {
		t.Fatalf("query mode: %v", fb)
	}
	_, m = vectorKeys(t, s, map[string]any{"query": "x", "explain": true, "negative": []any{"f00"}, "feedbackMode": "rescore", "feedbackWeight": 2.0})
	fb = planOf(t, m)["feedback"].(map[string]any)
	if fb["mode"] != "rescore" || fb["weight"] != 2.0 || fb["rescored"].(float64) == 0 {
		t.Fatalf("rescore mode: %v", fb)
	}
}

func TestFeedback_Refusals(t *testing.T) {
	s, _, cleanup := feedbackServer(t)
	defer cleanup()
	for name, extra := range map[string]map[string]any{
		"unknown key":      {"positive": []any{"nope"}},
		"wrong dimensions": {"negative": []any{[]float32{1, 0}}},
		"unknown mode":     {"positive": []any{"f01"}, "feedbackMode": "magic"},
		"key and vector":   {"positive": []any{map[string]any{"key": "f01", "vector": []float32{1}}}},
		"weight":           {"positive": []any{"f01"}, "feedbackWeight": 11},
		"rocchio":          {"positive": []any{"f01"}, "rocchio": map[string]any{"gamma": -1}},
		"too many":         {"positive": make([]any, maxFeedbackExamples+1)},
	} {
		req := map[string]any{"collection": "fb", "query": "x"}
		for k, v := range extra {
			req[k] = v
		}
		if name == "too many" {
			for i := range req["positive"].([]any) {
				req["positive"].([]any)[i] = "f01"
			}
		}
		if rec := doRequest(t, s.handleVectorSearch, req); rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("%s: %d %s", name, rec.Code, rec.Body.String())
		}
	}
	if rec := doRequest(t, s.handleVectorSearch, map[string]any{"collection": "fb", "query": "x", "positive": []any{5}}); rec.Code != http.StatusBadRequest {
		t.Fatalf("a number is no example: %d", rec.Code)
	}
}

func TestFeedbackExample_JSON(t *testing.T) {
	var got []FeedbackExample
	if err := json.Unmarshal([]byte(`["k", [1, 2], {"key": "j"}, {"vector": [3]}]`), &got); err != nil {
		t.Fatal(err)
	}
	want := []FeedbackExample{{Key: "k"}, {Vector: []float32{1, 2}}, {Key: "j"}, {Vector: []float32{3}}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%+v", got)
	}
	if err := json.Unmarshal([]byte(`[true]`), &got); err == nil || !strings.Contains(err.Error(), "feedback example") {
		t.Fatalf("a boolean is no example: %v", err)
	}
}

// Vectors the flat index does not hold — a disk-only collection's — come
// from the store, for the examples and for rescoring.
func TestFeedback_VectorsFromTheStore(t *testing.T) {
	s, ids, cleanup := feedbackServer(t)
	defer cleanup()
	got, ok := s.exampleVector([]string{"fb"}, "f03")
	if !ok || math.Abs(float64(vec.CosineSimilarity(got, corpusVector(3)))-1) > 1e-6 {
		t.Fatalf("by key from the index: %v %v", ok, got)
	}
	s.VectorIndex.Remove("fb", ids["f03"]+"#0")
	if got, ok := s.exampleVector([]string{"fb"}, "f03"); !ok || len(got) != feedbackDims {
		t.Fatalf("from the store: %v %v", ok, got)
	}
	if _, ok := s.exampleVector([]string{"none", "fb"}, "missing"); ok {
		t.Fatal("found a document that does not exist")
	}
	vectors := s.candidateVectors("fb", []vec.VectorResult{{DocID: ids["f03"] + "#0"}, {DocID: ids["f04"] + "#0"}})
	if len(vectors) != 2 {
		t.Fatalf("candidate vectors: %d", len(vectors))
	}
	var none *feedback
	if none.report() != nil || len(none.query([]float32{1})) != 1 {
		t.Fatal("no feedback is no correction")
	}
	if got := s.rescoreFeedback(none, "fb", nil, nil); got != nil {
		t.Fatal("nothing to rescore")
	}
}
