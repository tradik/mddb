package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"mddb-bench/internal/benchkit"
)

// search is one way of searching a collection.
type search struct {
	Algorithm string // as requested
	Filtered  bool   // with the selective filter (slice=rare)
}

// defaultIndexes are the indexes every size is searched with by name: flat
// (the default before SRCH-013) and the ones auto chooses between by size.
const defaultIndexes = "flat,hnsw,ivf"

// searches is what every size runs: each named index, then auto, unfiltered
// and then filtered.
func searches(indexes []string) []search {
	var out []search
	for _, filtered := range []bool{false, true} {
		for _, name := range append(indexes[:len(indexes):len(indexes)], "auto") {
			out = append(out, search{name, filtered})
		}
	}
	return out
}

// searchResult is one search over every query.
type searchResult struct {
	search
	Ran      string  // the algorithms that answered, from the plan; "+prefilter" when it applied
	Recall   float64 // mean recall@10 against the exact answer
	P50, P99 float64 // ms, client side
}

// sizeResult is everything measured on one collection.
type sizeResult struct {
	Vectors   int
	Rare      int // documents the selective filter admits
	LoadSecs  float64
	BuildSecs float64 // the IVF training the first IVF search triggers
	Searches  []searchResult
}

// measureSize loads one corpus, builds its indexes and runs every search.
func measureSize(client *http.Client, cfg config, c *corpus, log io.Writer) (sizeResult, error) {
	r := sizeResult{Vectors: len(c.docs), Rare: c.rareCount()}
	start := time.Now()
	if err := load(client, cfg, c); err != nil {
		return r, err
	}
	if err := waitEmbedded(client, cfg, c.collection); err != nil {
		return r, err
	}
	r.LoadSecs = time.Since(start).Seconds()
	_, _ = fmt.Fprintf(log, "%s: %d vectors loaded in %.0f s\n", c.collection, r.Vectors, r.LoadSecs)

	start = time.Now()
	if _, err := query(client, cfg, c, search{Algorithm: "ivf"}, 0); err != nil {
		return r, fmt.Errorf("building ivf: %w", err)
	}
	r.BuildSecs = time.Since(start).Seconds()
	_, _ = fmt.Fprintf(log, "%s: ivf trained in %.1f s\n", c.collection, r.BuildSecs)

	truths := map[bool][][]int{}
	for _, filtered := range []bool{false, true} {
		for q := cfg.Warmup; q < len(c.queries); q++ {
			truths[filtered] = append(truths[filtered], c.truth(q, filtered))
		}
	}
	for _, s := range searches(cfg.Indexes) {
		res, err := measure(client, cfg, c, s, truths[s.Filtered])
		if err != nil {
			return r, fmt.Errorf("%s: %w", s.Algorithm, err)
		}
		_, _ = fmt.Fprintf(log, "%s: %+v\n", c.collection, res)
		r.Searches = append(r.Searches, res)
	}
	if !cfg.Keep {
		_, _ = benchkit.Post(client, cfg.URL+"/v1/delete-collection", map[string]any{"collection": c.collection})
	}
	return r, nil
}

// measure runs one search over every query, warmup first.
func measure(client *http.Client, cfg config, c *corpus, s search, truths [][]int) (searchResult, error) {
	res := searchResult{search: s}
	ran := map[string]bool{}
	var recall, took []float64
	for q := range c.queries {
		start := time.Now()
		a, err := query(client, cfg, c, s, q)
		if err != nil {
			return res, err
		}
		if q < cfg.Warmup {
			continue
		}
		took = append(took, float64(time.Since(start).Microseconds())/1000)
		recall = append(recall, recallAt(a.keys, truths[q-cfg.Warmup]))
		ran[a.ran] = true
	}
	names := make([]string, 0, len(ran))
	for name := range ran {
		names = append(names, name)
	}
	sort.Strings(names)
	res.Ran = strings.Join(names, ", ")
	res.Recall, res.P50, res.P99 = benchkit.Mean(recall), benchkit.Percentile(took, 50), benchkit.Percentile(took, 99)
	return res, nil
}

// answer is what one search returned.
type answer struct {
	keys []string
	ran  string // the plan's algorithm, "+prefilter" when the filter ran first
}

// query runs query q as s, with explain.
func query(client *http.Client, cfg config, c *corpus, s search, q int) (answer, error) {
	req := map[string]any{"collection": c.collection, "queryVector": c.queries[q], "topK": 10,
		"algorithm": s.Algorithm, "explain": true}
	if s.Filtered {
		req["filterMeta"] = map[string][]string{"slice": {"rare"}}
	}
	raw, err := benchkit.Post(client, cfg.URL+"/v1/vector-search", req)
	if err != nil {
		return answer{}, err
	}
	var resp struct {
		Results []struct {
			Document struct {
				Key string `json:"key"`
			} `json:"document"`
		} `json:"results"`
		Plan struct {
			Algorithm string `json:"algorithm"`
			Prefilter bool   `json:"prefilter"`
		} `json:"plan"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return answer{}, err
	}
	a := answer{ran: resp.Plan.Algorithm}
	if resp.Plan.Prefilter {
		a.ran += "+prefilter"
	}
	for _, r := range resp.Results {
		a.keys = append(a.keys, r.Document.Key)
	}
	return a, nil
}

// recallAt is the share of the exact answer found in keys.
func recallAt(keys []string, truth []int) float64 {
	want := make(map[int]bool, len(truth))
	for _, i := range truth {
		want[i] = true
	}
	hit := 0
	for _, k := range keys {
		if want[keyDoc(k)] {
			hit++
		}
	}
	return float64(hit) / float64(max(len(truth), 1))
}
