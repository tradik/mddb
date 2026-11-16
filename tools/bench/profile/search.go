package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"mddb-bench/internal/benchkit"
)

// The search scenarios: full text, every vector index by name and the
// planner's choice, hybrid, and vector search behind a filter admitting 1 to
// 50 percent of the corpus. Latency comes from sequential requests, so one
// number means one request on an otherwise idle server; throughput comes from
// a separate pass with several clients.

// scenario is one way of searching the corpus.
type scenario struct {
	Name      string
	Kind      string // fts, vector, hybrid, filtered
	Algorithm string
	Pct       int // filter selectivity; 0 = no filter
}

// scenarios is what every profile runs, in report order.
func scenarios() []scenario {
	out := []scenario{{Name: "fts-bm25", Kind: "fts", Algorithm: "bm25"}}
	for _, a := range append(vectorAlgorithms[:len(vectorAlgorithms):len(vectorAlgorithms)], "auto") {
		out = append(out, scenario{Name: "vector-" + a, Kind: "vector", Algorithm: a})
	}
	out = append(out,
		scenario{Name: "hybrid-rrf-hnsw", Kind: "hybrid", Algorithm: "hnsw"},
		scenario{Name: "hybrid-rrf-flat", Kind: "hybrid", Algorithm: "flat"})
	for _, a := range []string{"hnsw", "auto"} {
		for _, pct := range selectivities {
			out = append(out, scenario{Name: fmt.Sprintf("filtered-%s-%dpct", a, pct), Kind: "filtered", Algorithm: a, Pct: pct})
		}
	}
	return out
}

// searcher runs scenarios against one loaded server.
type searcher struct {
	client *http.Client
	url    string
	c      *corpus
	p      profile
}

// request is the endpoint and body of query q of scenario sc.
func (s searcher) request(sc scenario, q int, explain bool) (string, map[string]any) {
	switch sc.Kind {
	case "fts":
		return "/v1/fts", map[string]any{"collection": s.c.collection, "query": s.c.queryText(q, false),
			"limit": 10, "algorithm": sc.Algorithm}
	case "hybrid":
		return "/v1/hybrid-search", map[string]any{"collection": s.c.collection, "query": s.c.queryText(q, true),
			"topK": 10, "strategy": "rrf", "vectorAlgorithm": sc.Algorithm}
	}
	body := map[string]any{"collection": s.c.collection, "queryVector": s.c.queries[q], "topK": 10, "algorithm": sc.Algorithm}
	if sc.Pct > 0 {
		body["filterMeta"] = map[string][]string{"sel": {fmt.Sprintf("p%d", sc.Pct)}}
	}
	if explain {
		body["explain"] = true
	}
	return "/v1/vector-search", body
}

// answer is what one search returned.
type answer struct {
	keys []string
	ran  string // the algorithm that answered, "+prefilter" when the filter ran first
}

// query runs query q of scenario sc.
func (s searcher) query(sc scenario, q int, explain bool) (answer, error) {
	path, body := s.request(sc, q, explain)
	raw, err := benchkit.Post(s.client, s.url+path, body)
	if err != nil {
		return answer{}, err
	}
	var resp struct {
		Results []struct {
			Document struct {
				Key string `json:"key"`
			} `json:"document"`
		} `json:"results"`
		Algorithm string `json:"algorithm"`
		Plan      struct {
			Algorithm string `json:"algorithm"`
			Prefilter bool   `json:"prefilter"`
		} `json:"plan"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return answer{}, err
	}
	a := answer{ran: resp.Plan.Algorithm}
	if a.ran == "" {
		a.ran = resp.Algorithm
	}
	if resp.Plan.Prefilter {
		a.ran += "+prefilter"
	}
	for _, r := range resp.Results {
		a.keys = append(a.keys, r.Document.Key)
	}
	return a, nil
}

// measure runs one scenario: warm-up (with explain, to learn what answers),
// Rounds sequential passes for latency and recall, one concurrent pass for
// throughput. truths is nil where there is no exact answer.
func (s searcher) measure(sc scenario, truths [][]int) (SearchMetric, error) {
	m := SearchMetric{Name: sc.Name, Kind: sc.Kind, Algorithm: sc.Algorithm, SelectivityPct: sc.Pct}
	ran := map[string]bool{}
	for q := 0; q < s.p.Warmup; q++ {
		a, err := s.query(sc, q, true)
		if err != nil {
			return m, err
		}
		ran[a.ran] = true
	}
	m.Ran = joinSorted(ran)

	var rounds []Latency
	recall := make([]float64, s.p.Queries)
	found := 0
	for r := 0; r < s.p.Rounds; r++ {
		t, err := fanOut(s.p.Queries, 1, func(i int) error {
			a, err := s.query(sc, s.p.Warmup+i, false)
			if truths != nil {
				recall[i] = recallAt(a.keys, truths[i])
			}
			found += len(a.keys)
			return err
		})
		if err != nil {
			return m, err
		}
		rounds = append(rounds, summarize(t.Millis))
	}
	if found == 0 {
		return m, errors.New("no query returned a result: the corpus and the queries do not match")
	}
	m.Latency = medianLatency(rounds)
	if truths != nil {
		r := round3(benchkit.Mean(recall))
		m.Recall10 = &r
	}

	t, err := fanOut(s.p.Queries, s.p.Clients, func(i int) error {
		_, err := s.query(sc, s.p.Warmup+i, false)
		return err
	})
	m.QPS = t.perSecond(s.p.Queries)
	return m, err
}

func joinSorted(set map[string]bool) string {
	names := make([]string, 0, len(set))
	for name := range set {
		names = append(names, name)
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

// waitAnswering blocks until every vector algorithm answers a search on the
// corpus — a trainable index refuses, or trains before it answers, until it
// has trained the collection — and returns, per algorithm, the seconds from
// since to its first answer. The algorithms are asked one after another,
// so a slow one delays the ones after it.
func (s searcher) waitAnswering(since time.Time, timeout time.Duration) (map[string]float64, error) {
	deadline := time.Now().Add(timeout)
	ready := make(map[string]float64, len(vectorAlgorithms))
	for _, algo := range vectorAlgorithms {
		for {
			_, err := s.query(scenario{Kind: "vector", Algorithm: algo}, 0, false)
			if err == nil {
				ready[algo] = round3(time.Since(since).Seconds())
				break
			}
			if time.Now().After(deadline) {
				return ready, fmt.Errorf("%s not answering after %s: %w", algo, timeout, err)
			}
			time.Sleep(pollPeriod)
		}
	}
	return ready, nil
}

// run measures every scenario.
func (s searcher) run(log io.Writer) ([]SearchMetric, error) {
	truths := map[int][][]int{}
	var out []SearchMetric
	for _, sc := range scenarios() {
		var truth [][]int
		if sc.Kind == "vector" || sc.Kind == "filtered" {
			if truths[sc.Pct] == nil {
				truths[sc.Pct] = s.c.truths(s.p.Warmup, sc.Pct)
			}
			truth = truths[sc.Pct]
		}
		m, err := s.measure(sc, truth)
		if err != nil {
			return out, fmt.Errorf("%s: %w", sc.Name, err)
		}
		_, _ = fmt.Fprintf(log, "  %-22s p50 %7.3f  p95 %7.3f  p99 %7.3f ms  %8.0f qps  %s\n",
			m.Name, m.P50, m.P95, m.P99, m.QPS, recallText(m.Recall10))
		out = append(out, m)
	}
	return out, nil
}

// recallText is a recall for a log line or a table cell; "—" for none.
func recallText(r *float64) string {
	if r == nil {
		return "—"
	}
	return fmt.Sprintf("%.3f", *r)
}
