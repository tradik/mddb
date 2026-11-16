package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	json "mddb/internal/jsonx"
	"mddb/internal/rerank"
)

// A reranking model for tests (RAG-011): an HTTP server speaking the /rerank
// shape that scores a passage by how often it says "golden". The documents
// below rank "gamma" last of the matches on every search, so a rerank that
// ran is visible as gamma moving to the top.

const rerankQuery = "pump seal"

// fakeReranker is the model server; every knob is atomic because searches
// read it while a test changes it.
type fakeReranker struct {
	*httptest.Server
	calls atomic.Int64
	delay atomic.Int64 // nanoseconds before answering
	code  atomic.Int64 // non-zero: answer with this status
	body  atomic.Value // string: answer with this body instead of scoring
	docs  atomic.Int64 // documents in the last request
	// overlap scores by the query words a passage holds instead of
	// "golden": a stand-in with no model, for measuring the plumbing.
	overlap atomic.Bool
}

func newFakeReranker(t *testing.T) *fakeReranker {
	t.Helper()
	f := &fakeReranker{}
	f.body.Store("")
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.calls.Add(1)
		if d := f.delay.Load(); d > 0 {
			time.Sleep(time.Duration(d))
		}
		if c := f.code.Load(); c != 0 {
			w.WriteHeader(int(c))
			return
		}
		if b := f.body.Load().(string); b != "" {
			_, _ = io.WriteString(w, b)
			return
		}
		var req struct {
			Query     string   `json:"query"`
			Documents []string `json:"documents"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		f.docs.Store(int64(len(req.Documents)))
		type entry struct {
			Index int     `json:"index"`
			Score float64 `json:"relevance_score"`
		}
		out := []entry{}
		for i, d := range req.Documents {
			out = append(out, entry{Index: i, Score: f.score(req.Query, d)})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"results": out})
	}))
	t.Cleanup(f.Close)
	return f
}

// score is a passage's relevance to query under the server's rule.
func (f *fakeReranker) score(query, passage string) float64 {
	if !f.overlap.Load() {
		return float64(strings.Count(passage, "golden"))
	}
	lower, n := strings.ToLower(passage), 0.0
	for _, w := range queryTerms(query) {
		if strings.Contains(lower, w) {
			n++
		}
	}
	return n
}

// useReranker points s at f.
func useReranker(s *Server, f *fakeReranker, timeout time.Duration, cacheSize int) {
	s.setRerank(rerank.New(rerank.Config{Provider: rerank.ProviderHTTP, APIURL: f.URL, Model: "fake-rerank",
		Timeout: timeout}, cacheSize), rerank.DefaultMaxCandidates, "test")
}

// rerankServer is a server with full-text and vector search over "rr" and the
// fake model configured (no cache, so every search calls it).
func rerankServer(t *testing.T) (*Server, *fakeReranker) {
	t.Helper()
	s := corpusServer(t, 64)
	for key, text := range map[string]string{
		"alpha": "# Pump seal\n\nPump seal maintenance: check the pump seal weekly. The pump seal wears; a pump seal leaks.",
		"beta":  "# Pump overview\n\nThe pump moves water and the seal keeps it in. Pump seal.",
		"gamma": "# Replacing\n\ngolden golden: to replace it stop the pump, drain the housing and fit the new seal.",
		"delta": "# Kitchen\n\nBread recipe with flour, water and salt.",
	} {
		doc := addTestDoc(t, s, "rr", key, "en", text, nil)
		waitForEmbedding(t, s, "rr", doc.ID)
	}
	f := newFakeReranker(t)
	useReranker(s, f, time.Second, 0)
	return s, f
}

// rrEnabled is the rerank block that turns the stage on.
func rrEnabled(extra ...any) map[string]any {
	m := map[string]any{"enabled": true}
	for i := 0; i+1 < len(extra); i += 2 {
		m[extra[i].(string)] = extra[i+1]
	}
	return m
}

// restSearch runs a REST search and decodes its answer.
func restSearch(t *testing.T, h http.HandlerFunc, req map[string]any) map[string]any {
	t.Helper()
	rec := doRequest(t, h, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	return body(t, rec.Body.Bytes())
}

// rrKeys are the document keys of a results answer, in order.
func rrKeys(m map[string]any) []string {
	var keys []string
	for _, r := range m["results"].([]any) {
		keys = append(keys, r.(map[string]any)["document"].(map[string]any)["key"].(string))
	}
	return keys
}

// skipped is a response's rerankSkipped.
func skipped(m map[string]any) string {
	s, _ := m["rerankSkipped"].(string)
	return s
}
