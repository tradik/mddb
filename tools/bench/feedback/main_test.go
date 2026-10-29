package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"net/http"
	"net/http/httptest"
	"os"
	"sort"
	"strings"
	"sync"
	"testing"

	"mddb-bench/internal/benchkit"
)

// fakeMDDB answers the three calls the bench makes, searching exactly over
// the corpus vectors of what was added; feedback it ignores, which is enough
// to exercise the bench and makes "none" and the modes score alike.
type fakeMDDB struct {
	c        *corpus
	mu       sync.Mutex
	keys     []string
	failAdd  bool
	badJSON  bool
	searched []map[string]any
}

func (f *fakeMDDB) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var req map[string]any
	_ = json.NewDecoder(r.Body).Decode(&req)
	f.mu.Lock()
	defer f.mu.Unlock()
	switch r.URL.Path {
	case "/v1/add":
		if f.failAdd {
			http.Error(w, "read-only", http.StatusForbidden)
			return
		}
		f.keys = append(f.keys, req["key"].(string))
	case "/v1/vector-search":
		if f.badJSON {
			_, _ = w.Write([]byte("{"))
			return
		}
		f.searched = append(f.searched, req)
		q := make([]float32, f.c.dims)
		for i, x := range req["queryVector"].([]any) {
			q[i] = float32(x.(float64))
		}
		keys := append([]string(nil), f.keys...)
		sort.Slice(keys, func(i, j int) bool {
			return benchkit.Dot(q, f.c.docs[keyDoc(keys[i])]) > benchkit.Dot(q, f.c.docs[keyDoc(keys[j])])
		})
		var results []map[string]any
		for _, k := range keys[:10] {
			results = append(results, map[string]any{"document": map[string]any{"key": k}})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"results": results})
		return
	}
	_, _ = w.Write([]byte("{}"))
}

func smallConfig(url string) config {
	return config{URL: url, Collection: "c", Seed: 1, Dims: 16, Topics: 4, PerTopic: 20, Queries: 6}
}

func TestRun_MeasuresEveryMode(t *testing.T) {
	cfg := smallConfig("")
	f := &fakeMDDB{c: newCorpus(cfg.Seed, cfg.Dims, cfg.Topics, cfg.PerTopic, cfg.Queries)}
	srv := httptest.NewServer(f)
	defer srv.Close()
	cfg.URL = srv.URL
	results, err := run(srv.Client(), cfg, f.c)
	if err != nil || len(results) != 3 {
		t.Fatalf("%v %+v", err, results)
	}
	if results[0].Recall <= 0 || results[0].Recall != results[1].Recall {
		t.Fatalf("the fake ignores feedback, so every mode scores alike: %+v", results)
	}
	if f.searched[0]["waitForIndex"] == nil || f.searched[1]["waitForIndex"] != nil {
		t.Fatal("only the first search waits for the index")
	}
	if f.searched[1]["feedbackMode"] != "query" || f.searched[2]["feedbackMode"] != "rescore" || f.searched[1]["positive"] == nil {
		t.Fatalf("feedback round: %v", f.searched[1])
	}
	var out bytes.Buffer
	report(&out, cfg, results)
	if !strings.Contains(out.String(), "| rescore |") || !strings.Contains(out.String(), "80 documents") {
		t.Fatal(out.String())
	}
}

func TestRun_Failures(t *testing.T) {
	cfg := smallConfig("")
	f := &fakeMDDB{c: newCorpus(cfg.Seed, cfg.Dims, cfg.Topics, cfg.PerTopic, cfg.Queries), failAdd: true}
	srv := httptest.NewServer(f)
	defer srv.Close()
	cfg.URL = srv.URL
	if _, err := run(srv.Client(), cfg, f.c); err == nil || !strings.Contains(err.Error(), "403") {
		t.Fatalf("a refused write: %v", err)
	}
	f.failAdd, f.badJSON = false, true
	if _, err := run(srv.Client(), cfg, f.c); err == nil {
		t.Fatal("an unreadable search answer")
	}
	cfg.URL = "http://127.0.0.1:1"
	if _, err := search(srv.Client(), cfg.URL, map[string]any{}); err == nil {
		t.Fatal("no server")
	}
	if err := serveAndRun(cfg, "256.0.0.1:0", &bytes.Buffer{}); err == nil {
		t.Fatal("an address nobody can listen on")
	}
	if err := serveAndRun(cfg, "127.0.0.1:0", &bytes.Buffer{}); err == nil {
		t.Fatal("no server to measure")
	}
}

// The embedder serves the corpus vector of each document text it is sent.
func TestCorpus_EmbeddingsEndpoint(t *testing.T) {
	c := newCorpus(1, 8, 2, 5, 3)
	srv := httptest.NewServer(c)
	defer srv.Close()
	resp, err := srv.Client().Post(srv.URL+"/v1/embeddings", "application/json",
		strings.NewReader(`{"input": ["`+docText(3)+`", "unrelated", "fbdoc-999"]}`))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	var got struct {
		Data []struct {
			Embedding []float32 `json:"embedding"`
			Index     int       `json:"index"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil || len(got.Data) != 3 {
		t.Fatalf("%v %+v", err, got)
	}
	if benchkit.Dot(got.Data[0].Embedding, c.docs[3]) < 0.999 || got.Data[1].Embedding[0] != 1 || got.Data[2].Embedding[0] != 1 {
		t.Fatalf("%+v", got.Data)
	}
	bad, _ := srv.Client().Post(srv.URL+"/v1/embeddings", "application/json", strings.NewReader("{"))
	if bad.StatusCode != http.StatusBadRequest {
		t.Fatalf("malformed request: %d", bad.StatusCode)
	}
	_ = bad.Body.Close()
	for _, q := range c.queries {
		if len(q.relevant) != 3 && len(q.relevant) != 10 {
			t.Fatalf("relevant set: %d", len(q.relevant))
		}
	}
	if pos, neg := judge([]string{docKey(0), docKey(9)}, map[int]bool{0: true}); len(pos) != 1 || len(neg) != 1 {
		t.Fatal("judge")
	}
	if benchkit.Percentile(nil, 50) != 0 || benchkit.Mean(nil) != 0 {
		t.Fatal("empty statistics")
	}
}

// main runs end to end against a server given on the command line.
func TestMain_RunsFromFlags(t *testing.T) {
	cfg := smallConfig("")
	f := &fakeMDDB{c: newCorpus(cfg.Seed, cfg.Dims, cfg.Topics, cfg.PerTopic, cfg.Queries)}
	srv := httptest.NewServer(f)
	defer srv.Close()
	old := os.Args
	defer func() { os.Args = old }()
	flag.CommandLine = flag.NewFlagSet("feedback", flag.ContinueOnError)
	os.Args = []string{"feedback", "-url", srv.URL, "-embed-addr", "127.0.0.1:0", "-dims", "16", "-topics", "4",
		"-per-topic", "20", "-queries", "6"}
	main()
	if len(f.searched) != 18 {
		t.Fatalf("searches: %d", len(f.searched))
	}
}

// A query whose second topic would be its own is typed towards the next one.
func TestCorpus_SecondTopicIsNeverTheFirst(t *testing.T) {
	c := newCorpus(1, 8, 3, 4, 1) // query 0: topic 0, and (0*7+3)%3 is 0 too
	if len(c.queries) != 1 || len(c.queries[0].relevant) != 10 {
		t.Fatalf("%+v", c.queries)
	}
}
