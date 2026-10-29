package main

import (
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"sync"
)

// fakeMDDB answers the calls the spike makes. A "chunk" is a paragraph of a
// document and its similarity to a text is the share of the text's words it
// contains — enough to make whole-query max and MaxSim rank differently.
type fakeMDDB struct {
	mu        sync.Mutex
	docs      map[string]string
	pending   bool   // warm-up searches report timedOut
	fail      string // path that answers 500
	badJSON   string // path that answers malformed JSON
	evalError string // error the eval reports for its configs
	shortEval bool   // eval answers fewer queries than asked
	chunkCap  int    // cap on the chunks a search returns (0 = all)
	adds      int
}

func newFake() *fakeMDDB { return &fakeMDDB{docs: map[string]string{}} }

func (f *fakeMDDB) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.URL.Path == f.fail {
		http.Error(w, "boom", http.StatusInternalServerError)
		return
	}
	if r.URL.Path == f.badJSON {
		_, _ = w.Write([]byte("{"))
		return
	}
	var req map[string]any
	_ = json.NewDecoder(r.Body).Decode(&req)
	var resp any = map[string]any{}
	switch r.URL.Path {
	case "/v1/delete-collection":
		f.docs = map[string]string{}
	case "/v1/add":
		f.adds++
		f.docs[req["key"].(string)] = req["contentMd"].(string)
	case "/v1/vector-search":
		resp = f.search(req)
	case "/v1/eval":
		resp = f.eval(req)
	}
	_ = json.NewEncoder(w).Encode(resp)
}

type fakeChunk struct {
	key   string
	score float64
}

// chunks scores every paragraph of every document against text, best first.
func (f *fakeMDDB) chunks(text string) []fakeChunk {
	words := strings.Fields(strings.ToLower(text))
	var out []fakeChunk
	for key, content := range f.docs {
		for _, para := range strings.Split(content, "\n\n") {
			hit := 0
			for _, w := range words {
				if strings.Contains(strings.ToLower(para), w) {
					hit++
				}
			}
			out = append(out, fakeChunk{key: key, score: float64(hit) / float64(max(len(words), 1))})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].score != out[j].score {
			return out[i].score > out[j].score
		}
		return out[i].key < out[j].key
	})
	return out
}

func (f *fakeMDDB) search(req map[string]any) any {
	if _, ok := req["waitForIndex"]; ok {
		return map[string]any{"results": []any{}, "indexing": map[string]any{"timedOut": f.pending}}
	}
	cs := f.chunks(req["query"].(string))
	if f.chunkCap > 0 {
		cs = cs[:min(f.chunkCap, len(cs))]
	}
	results := make([]map[string]any, 0, len(cs))
	for _, c := range cs {
		results = append(results, map[string]any{"document": map[string]any{"key": c.key}, "score": c.score})
	}
	return map[string]any{"results": results}
}

// eval ranks documents by their best chunk for both configs, the way the
// server's parent mode does.
func (f *fakeMDDB) eval(req map[string]any) any {
	queries := req["queries"].([]any)
	if f.shortEval {
		queries = queries[:1]
	}
	var perQuery []map[string]any
	for _, q := range queries {
		seen := map[string]bool{}
		var keys []string
		for _, c := range f.chunks(q.(map[string]any)["query"].(string)) {
			if !seen[c.key] {
				seen[c.key] = true
				keys = append(keys, c.key)
			}
		}
		perQuery = append(perQuery, map[string]any{"returned": keys[:min(10, len(keys))]})
	}
	var results []map[string]any
	for _, name := range []string{"vector", "hybrid"} {
		results = append(results, map[string]any{"config": name, "p50Ms": 1.5, "p99Ms": 3.0,
			"error": f.evalError, "perQuery": perQuery})
	}
	return map[string]any{"results": results}
}
