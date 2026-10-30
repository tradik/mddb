package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// The calls the spike makes to MDDB. All of them are public API; none is
// added for it.

// post sends body as JSON and returns the response, or why it failed.
func post(client *http.Client, url string, body any) ([]byte, error) {
	buf, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
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

// loadDocs recreates the collection with one document per page, keyed by
// file name — the keys the evaluation set grades.
func loadDocs(client *http.Client, cfg config) error {
	_, _ = post(client, cfg.URL+"/v1/delete-collection", map[string]any{"collection": cfg.Collection})
	files, _ := filepath.Glob(filepath.Join(cfg.DocsDir, "*.md"))
	if len(files) == 0 {
		return fmt.Errorf("no *.md pages in %s", cfg.DocsDir)
	}
	sort.Strings(files)
	for _, f := range files {
		content, err := os.ReadFile(f) // #nosec G304 -- the operator names the directory
		if err != nil {
			return err
		}
		if _, err := post(client, cfg.URL+"/v1/add", map[string]any{"collection": cfg.Collection,
			"key": filepath.Base(f), "lang": "en", "contentMd": string(content)}); err != nil {
			return err
		}
	}
	return nil
}

// waitIndexed blocks until the collection's embeddings have drained, in
// 30-second server-side waits (the most one request may wait).
func waitIndexed(client *http.Client, cfg config) error {
	for range cfg.WaitTries {
		raw, err := post(client, cfg.URL+"/v1/vector-search", map[string]any{"collection": cfg.Collection,
			"query": "index warm-up", "topK": 1, "waitForIndex": 30000})
		if err != nil {
			return err
		}
		var resp struct {
			Indexing *struct {
				TimedOut bool `json:"timedOut"`
			} `json:"indexing"`
		}
		if err := json.Unmarshal(raw, &resp); err != nil {
			return err
		}
		if resp.Indexing == nil || !resp.Indexing.TimedOut {
			return nil
		}
	}
	return fmt.Errorf("embeddings still pending after %d waits", cfg.WaitTries)
}

// partScores is one query part's similarity to every document: the best of
// the document's chunks, as the server's flat index computes it.
type partScores struct {
	Best   map[string]float64
	Chunks int
	Ms     float64 // round trip, including the server's embedding call
}

// fetchPart embeds text on the server and scores every chunk against it.
func fetchPart(client *http.Client, cfg config, text string) (partScores, error) {
	start := time.Now()
	raw, err := post(client, cfg.URL+"/v1/vector-search", map[string]any{"collection": cfg.Collection,
		"query": text, "topK": cfg.ChunkTopK, "retrievalMode": "chunk", "algorithm": "flat"})
	if err != nil {
		return partScores{}, err
	}
	ms := float64(time.Since(start).Microseconds()) / 1000
	var resp struct {
		Results []struct {
			Document struct {
				Key string `json:"key"`
			} `json:"document"`
			Score float64 `json:"score"`
		} `json:"results"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return partScores{}, err
	}
	if len(resp.Results) >= cfg.ChunkTopK {
		return partScores{}, fmt.Errorf("%d chunks returned: raise -chunk-topk above the chunk count", len(resp.Results))
	}
	p := partScores{Best: map[string]float64{}, Chunks: len(resp.Results), Ms: ms}
	for _, r := range resp.Results {
		if s, ok := p.Best[r.Document.Key]; !ok || r.Score > s {
			p.Best[r.Document.Key] = r.Score
		}
	}
	return p, nil
}

// baseline is one server-side configuration measured by POST /v1/eval.
type baseline struct {
	Name     string
	Base     string     // the configuration this one is compared with; "" = none
	Returned [][]string // per query, in rank order
	P50, P99 float64
}

// evalConfigs are the server's own searches measured: vector and hybrid as
// they rank today, and each with queryDecomposition auto (SRCH-020).
var evalConfigs = []map[string]any{
	{"name": "vector", "searchType": "vector"},
	{"name": "hybrid", "searchType": "hybrid"},
	{"name": "vector, decomposition auto", "searchType": "vector", "queryDecomposition": "auto"},
	{"name": "hybrid, decomposition auto", "searchType": "hybrid", "queryDecomposition": "auto"},
}

// baseOf is the configuration a decomposed search is compared with.
func baseOf(name string) string {
	plain, _, ok := strings.Cut(name, ", decomposition")
	if !ok {
		return ""
	}
	return plain
}

// evalBaselines runs vector and hybrid search through the server's own
// evaluation endpoint and returns what each query got back.
func evalBaselines(client *http.Client, cfg config, qs []evalQuery) ([]baseline, error) {
	raw, err := post(client, cfg.URL+"/v1/eval", map[string]any{"collection": cfg.Collection, "k": cfg.K,
		"queries": qs, "details": true, "configs": evalConfigs})
	if err != nil {
		return nil, err
	}
	var resp struct {
		Results []struct {
			Config   string  `json:"config"`
			P50Ms    float64 `json:"p50Ms"`
			P99Ms    float64 `json:"p99Ms"`
			Error    string  `json:"error"`
			PerQuery []struct {
				Returned []string `json:"returned"`
			} `json:"perQuery"`
		} `json:"results"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, err
	}
	out := make([]baseline, 0, len(resp.Results))
	for _, r := range resp.Results {
		if r.Error != "" || len(r.PerQuery) != len(qs) {
			return nil, fmt.Errorf("eval %s: %q, %d of %d queries", r.Config, r.Error, len(r.PerQuery), len(qs))
		}
		b := baseline{Name: r.Config, Base: baseOf(r.Config), P50: r.P50Ms, P99: r.P99Ms}
		for _, pq := range r.PerQuery {
			b.Returned = append(b.Returned, pq.Returned)
		}
		out = append(out, b)
	}
	return out, nil
}
