package main

import (
	"context"
	"hash/fnv"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"mddb/internal/embedding"
)

// RAG-009 test corpus. "plant" is four short chunks about one repair each and
// a summary about what they add up to; no chunk shares a word with the
// general question "main hazards overview", the summary shares all three.

const summaryDims = 1024

// summaryEmbedding embeds text as hashed words over many dimensions, so two
// texts score by the words they share. calls counts provider calls.
type summaryEmbedding struct{ calls *atomic.Int64 }

func (e summaryEmbedding) Embed(_ context.Context, text string, _ embedding.Role) ([]float32, error) {
	if e.calls != nil {
		e.calls.Add(1)
	}
	v := make([]float32, summaryDims)
	v[0] = 0.01
	for _, w := range strings.Fields(strings.ToLower(text)) {
		w = strings.Trim(w, ".,:;!?")
		h := fnv.New32a()
		_, _ = h.Write([]byte(w))
		v[1+int(h.Sum32()%(summaryDims-1))]++
	}
	return v, nil
}

func (e summaryEmbedding) EmbedBatch(ctx context.Context, texts []string, role embedding.Role) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i, t := range texts {
		out[i], _ = e.Embed(ctx, t, role)
	}
	return out, nil
}
func (summaryEmbedding) Model() string   { return "summary-words" }
func (summaryEmbedding) Dimensions() int { return summaryDims }

const plantContent = "Pump seal on line four was replaced after the night shift found a slow drip.\n\n" +
	"Gasket torque on the inlet flange was checked against the vendor sheet.\n\n" +
	"Valve inspection for the cooling loop is booked with the contractor.\n\n" +
	"Spare filters arrived and were stored in the east cage by the stores team."

const plantSummary = "Main hazards overview: leaks, pressure and the maintenance backlog."

// generalQuery is answered by the plant's summary and by none of its chunks.
const generalQuery = "main hazards overview"

// summaryServer holds the corpus in "sum", embedded by the real worker, with
// the given retrieval profile.
func summaryServer(t *testing.T, profile *RetrievalProfileDef) (*Server, *atomic.Int64, func()) {
	t.Helper()
	s, cleanup := newHandlerTestServer(t)
	s.VectorIndex.SetReady()
	for _, sr := range s.VectorSearchers {
		sr.SetReady()
	}
	calls := &atomic.Int64{}
	s.Embedding = summaryEmbedding{calls: calls}
	s.startEmbeddingWorker(s.Embedding)
	if err := s.CollectionManager.Set("sum", &CollectionConfig{Embedding: &EmbeddingChunkingDef{ChunkSize: 100}, Retrieval: profile}); err != nil {
		t.Fatal(err)
	}
	addTestDoc(t, s, "sum", "plant", "en", plantContent, map[string][]string{"summary": {plantSummary}})
	addTestDoc(t, s, "sum", "garden", "en", "Tomatoes need sun and regular watering through the summer months.",
		map[string][]string{"summary": {"Gardening basics for beginners."}})
	addTestDoc(t, s, "sum", "kitchen", "en", "Bread recipe with flour, water, salt and yeast baked at high heat.", nil)
	addTestDoc(t, s, "sum", "office", "en", "Printer toner is ordered every quarter from the usual supplier.", nil)
	waitSummaries(t, s, "sum", 2)
	return s, calls, func() { s.EmbeddingWorker.Stop(); cleanup() }
}

// waitSummaries waits for the worker to finish and n summaries to be indexed.
func waitSummaries(t *testing.T, s *Server, collection string, n int) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if s.IndexingStatus(collection).PendingEmbeddings == 0 && s.summaries().size(collection) == n {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("summaries: %d indexed, %d pending", s.summaries().size(collection), s.IndexingStatus(collection).PendingEmbeddings)
}

// setSummaryMode stores a profile with the mode on "sum".
func setSummaryMode(t *testing.T, s *Server, p *RetrievalProfileDef) {
	t.Helper()
	cfg, _ := s.CollectionManager.Get("sum")
	next := *cfg
	next.Retrieval = p
	if err := s.CollectionManager.Set("sum", &next); err != nil {
		t.Fatal(err)
	}
}

// restVector runs a REST vector search on "sum".
func restVector(t *testing.T, s *Server, req map[string]any) map[string]any {
	t.Helper()
	req["collection"] = "sum"
	rec := doRequest(t, s.handleVectorSearch, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	return body(t, rec.Body.Bytes())
}

func sumResultKeys(m map[string]any) []string {
	var keys []string
	for _, r := range m["results"].([]any) {
		keys = append(keys, r.(map[string]any)["document"].(map[string]any)["key"].(string))
	}
	return keys
}

func sumPassageKeys(m map[string]any) []string {
	var keys []string
	for _, p := range m["passages"].([]any) {
		keys = append(keys, p.(map[string]any)["key"].(string))
	}
	return keys
}

// The acceptance: a general question no chunk answers finds the document by
// its summary — and finds nothing with summaries off.
func TestSummary_GeneralQueryMatchesOnlyTheSummary(t *testing.T) {
	s, _, cleanup := summaryServer(t, nil)
	defer cleanup()
	if keys := sumResultKeys(restVector(t, s, map[string]any{"query": generalQuery, "threshold": 0.3})); len(keys) != 0 {
		t.Fatalf("off: no chunk answers the question, yet %v", keys)
	}
	for _, mode := range []string{SummaryModeBlend, SummaryModeExpand} {
		setSummaryMode(t, s, &RetrievalProfileDef{SummaryMode: mode})
		m := restVector(t, s, map[string]any{"query": generalQuery, "threshold": 0.3, "explain": true})
		if keys := sumResultKeys(m); len(keys) != 1 || keys[0] != "plant" {
			t.Fatalf("%s: %v", mode, keys)
		}
		if !strings.Contains(strings.Join(toStrings(planOf(t, m)["reasons"]), " "), "summaryMode "+mode) {
			t.Fatalf("%s: explain says nothing about summaries: %v", mode, planOf(t, m))
		}
	}
}

func toStrings(v any) []string {
	var out []string
	for _, x := range v.([]any) {
		out = append(out, x.(string))
	}
	return out
}

// Blend raises a document its chunks found to its summary's score, and keeps
// one result per document in parent mode.
func TestSummary_BlendTakesTheBetterScore(t *testing.T) {
	s, _, cleanup := summaryServer(t, nil)
	defer cleanup()
	q := "pump seal main hazards overview"
	score := func() float64 {
		for _, r := range restVector(t, s, map[string]any{"query": q})["results"].([]any) {
			if m := r.(map[string]any); m["document"].(map[string]any)["key"] == "plant" {
				return m["score"].(float64)
			}
		}
		return -1
	}
	off := score()
	setSummaryMode(t, s, &RetrievalProfileDef{SummaryMode: SummaryModeBlend})
	if blend := score(); off <= 0 || blend <= off {
		t.Fatalf("blend %v, off %v", blend, off)
	}
	if keys := sumResultKeys(restVector(t, s, map[string]any{"query": q})); strings.Count(strings.Join(keys, ","), "plant") != 1 {
		t.Fatalf("parent mode: %v", keys)
	}
}

// Expand brings the summary hit's best chunks: as chunk results in chunk
// mode, as one passage each with passages, fewer when the budget is tight.
func TestSummary_ExpandChunksAndPassagesREST(t *testing.T) {
	s, _, cleanup := summaryServer(t, &RetrievalProfileDef{SummaryMode: SummaryModeExpand})
	defer cleanup()
	m := restVector(t, s, map[string]any{"query": generalQuery, "threshold": 0.3, "retrievalMode": "chunk"})
	if keys := sumResultKeys(m); len(keys) != maxSummaryExpandChunks || keys[0] != "plant" {
		t.Fatalf("chunk mode: %v", keys)
	}
	p := restVector(t, s, map[string]any{"query": generalQuery, "threshold": 0.3, "format": "passages"})
	if keys := sumPassageKeys(p); len(keys) != maxSummaryExpandChunks || strings.Join(keys, ",") != "plant,plant,plant" {
		t.Fatalf("passages: %v", keys)
	}
	if text := p["passages"].([]any)[0].(map[string]any)["content"].(string); !strings.Contains(plantContent, text) {
		t.Fatalf("a passage that is not a chunk of the document: %q", text)
	}
	setSummaryMode(t, s, &RetrievalProfileDef{SummaryMode: SummaryModeExpand, ContextTokenBudget: 30})
	p = restVector(t, s, map[string]any{"query": generalQuery, "threshold": 0.3, "format": "passages", "topK": 1})
	if keys := sumPassageKeys(p); len(keys) != 1 {
		t.Fatalf("a budget of 30 tokens holds one chunk: %v", keys)
	}
	// Blend in passages: one passage, its best chunk.
	setSummaryMode(t, s, &RetrievalProfileDef{SummaryMode: SummaryModeBlend})
	if keys := sumPassageKeys(restVector(t, s, map[string]any{"query": generalQuery, "threshold": 0.3, "format": "passages"})); len(keys) != 1 {
		t.Fatalf("blend passages: %v", keys)
	}
}

func TestSummary_ExpandChunks(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	if n := s.summaryExpandChunks("none", 5); n != maxSummaryExpandChunks {
		t.Fatalf("no budget: %d", n)
	}
	_ = s.CollectionManager.Set("b", &CollectionConfig{Embedding: &EmbeddingChunkingDef{ChunkSize: 400},
		Retrieval: &RetrievalProfileDef{ContextTokenBudget: 200}})
	if n := s.summaryExpandChunks("b", 1); n != 2 {
		t.Fatalf("200 tokens of 100-token chunks: %d", n)
	}
	if n := s.summaryExpandChunks("b", 0); n != 2 {
		t.Fatalf("topK 0 counts as 1: %d", n)
	}
	if n := s.summaryExpandChunks("b", 50); n != 1 {
		t.Fatalf("never fewer than one: %d", n)
	}
}
