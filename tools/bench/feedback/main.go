// Command feedback measures relevance feedback (SRCH-015): recall@10 of a
// vector search before and after one round of positive and negative examples.
//
// It serves a synthetic corpus through an OpenAI-compatible embeddings
// endpoint, so start MDDB pointing at it, then run the bench:
//
//	MDDB_EMBEDDING_PROVIDER=openai MDDB_EMBEDDING_API_KEY=bench \
//	MDDB_EMBEDDING_API_URL=http://127.0.0.1:11099/v1 MDDB_EMBEDDING_DIMENSIONS=64 mddbd &
//	go run ./feedback -url http://localhost:11023
//
// Each query is searched as typed; its top 10 are judged against the corpus's
// ground truth, the right ones sent back as positives and the wrong ones as
// negatives, and the search is run again in each feedback mode.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"sort"
	"time"
)

// config is everything a run needs.
type config struct {
	URL        string
	Collection string
	Seed       int64
	Dims       int
	Topics     int
	PerTopic   int
	Queries    int
}

// modeResult is one way of searching, over every query.
type modeResult struct {
	Name     string
	Recall   float64 // mean recall@10
	P50, P99 float64 // ms
}

func main() {
	cfg := config{}
	flag.StringVar(&cfg.URL, "url", "http://localhost:11023", "MDDB base URL")
	flag.StringVar(&cfg.Collection, "collection", "feedback-bench", "collection to (re)create")
	flag.Int64Var(&cfg.Seed, "seed", 1, "corpus seed")
	flag.IntVar(&cfg.Dims, "dims", 64, "vector dimensions (match MDDB_EMBEDDING_DIMENSIONS)")
	flag.IntVar(&cfg.Topics, "topics", 20, "topics")
	flag.IntVar(&cfg.PerTopic, "per-topic", 100, "documents per topic")
	flag.IntVar(&cfg.Queries, "queries", 60, "queries")
	embedAddr := flag.String("embed-addr", "127.0.0.1:11099", "address to serve embeddings on")
	flag.Parse()
	if err := serveAndRun(cfg, *embedAddr, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "feedback:", err)
		os.Exit(1)
	}
}

// serveAndRun serves the corpus's embeddings on addr while the bench runs.
func serveAndRun(cfg config, addr string, out io.Writer) error {
	c := newCorpus(cfg.Seed, cfg.Dims, cfg.Topics, cfg.PerTopic, cfg.Queries)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	srv := &http.Server{Handler: c, ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = srv.Serve(ln) }()
	defer func() { _ = srv.Close() }()
	results, err := run(&http.Client{Timeout: 60 * time.Second}, cfg, c)
	if err != nil {
		return err
	}
	report(out, cfg, results)
	return nil
}

// run loads the corpus, then searches every query without feedback and with
// one round of it in each mode.
func run(client *http.Client, cfg config, c *corpus) ([]modeResult, error) {
	_, _ = post(client, cfg.URL+"/v1/delete-collection", map[string]any{"collection": cfg.Collection})
	for i := range c.docs {
		if _, err := post(client, cfg.URL+"/v1/add", map[string]any{"collection": cfg.Collection,
			"key": docKey(i), "lang": "en", "contentMd": docText(i)}); err != nil {
			return nil, err
		}
	}
	modes := []string{"none", "query", "rescore"}
	recall := map[string][]float64{}
	took := map[string][]float64{}
	for qi, q := range c.queries {
		base := map[string]any{"collection": cfg.Collection, "queryVector": q.vector, "topK": 10}
		if qi == 0 {
			base["waitForIndex"] = 30000 // the embeddings are queued; wait for them once
		}
		var pos, neg []string
		for _, mode := range modes {
			req := map[string]any{}
			for k, v := range base {
				req[k] = v
			}
			if mode != "none" {
				req["positive"], req["negative"], req["feedbackMode"] = pos, neg, mode
			}
			start := time.Now()
			keys, err := search(client, cfg.URL, req)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", mode, err)
			}
			took[mode] = append(took[mode], float64(time.Since(start).Microseconds())/1000)
			recall[mode] = append(recall[mode], recallAt(keys, q.relevant))
			if mode == "none" {
				pos, neg = judge(keys, q.relevant)
				delete(base, "waitForIndex")
			}
		}
	}
	out := make([]modeResult, 0, len(modes))
	for _, m := range modes {
		out = append(out, modeResult{Name: m, Recall: mean(recall[m]), P50: percentile(took[m], 50), P99: percentile(took[m], 99)})
	}
	return out, nil
}

func docKey(i int) string { return fmt.Sprintf("d%05d", i) }

// judge splits a result list into the keys that answer the query and the
// keys that do not — what an agent would send back.
func judge(keys []string, relevant map[int]bool) (pos, neg []string) {
	pos, neg = []string{}, []string{}
	for _, k := range keys {
		if relevant[keyDoc(k)] {
			pos = append(pos, k)
		} else {
			neg = append(neg, k)
		}
	}
	return pos, neg
}

func keyDoc(key string) int {
	var i int
	_, _ = fmt.Sscanf(key, "d%05d", &i)
	return i
}

// recallAt is the share of the relevant documents in keys.
func recallAt(keys []string, relevant map[int]bool) float64 {
	hit := 0
	for _, k := range keys {
		if relevant[keyDoc(k)] {
			hit++
		}
	}
	return float64(hit) / float64(len(relevant))
}

// search runs one vector search and returns the result keys in rank order.
func search(client *http.Client, url string, req map[string]any) ([]string, error) {
	raw, err := post(client, url+"/v1/vector-search", req)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Results []struct {
			Document struct {
				Key string `json:"key"`
			} `json:"document"`
		} `json:"results"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, err
	}
	keys := make([]string, 0, len(resp.Results))
	for _, r := range resp.Results {
		keys = append(keys, r.Document.Key)
	}
	return keys, nil
}

// post sends body as JSON and returns the response, or why it failed.
func post(client *http.Client, url string, body any) ([]byte, error) {
	buf, _ := json.Marshal(body)
	resp, err := client.Post(url, "application/json", bytes.NewReader(buf)) // #nosec G107 -- the operator's own server
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("POST %s: %d %s", url, resp.StatusCode, raw)
	}
	return raw, nil
}

func mean(xs []float64) float64 {
	var s float64
	for _, x := range xs {
		s += x
	}
	return s / float64(max(len(xs), 1))
}

func percentile(xs []float64, p int) float64 {
	if len(xs) == 0 {
		return 0
	}
	s := append([]float64(nil), xs...)
	sort.Float64s(s)
	return s[min(len(s)-1, len(s)*p/100)]
}

// report prints the results as the markdown table docs/BENCHMARK.md carries.
func report(w io.Writer, cfg config, results []modeResult) {
	_, _ = fmt.Fprintf(w, "%d documents (%d topics × %d), %d dimensions, %d queries, seed %d\n\n",
		cfg.Topics*cfg.PerTopic, cfg.Topics, cfg.PerTopic, cfg.Dims, cfg.Queries, cfg.Seed)
	_, _ = fmt.Fprintln(w, "| Feedback | recall@10 | p50 ms | p99 ms |")
	_, _ = fmt.Fprintln(w, "|----------|-----------|--------|--------|")
	for _, r := range results {
		_, _ = fmt.Fprintf(w, "| %s | %.3f | %.1f | %.1f |\n", r.Name, r.Recall, r.P50, r.P99)
	}
}
