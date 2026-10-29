package main

import (
	"encoding/json"
	"fmt"
	"math"
	"math/rand"
	"net/http"
	"regexp"
	"sort"
)

// The synthetic corpus and the embedding provider that serves it.
//
// Documents belong to topics: each is its topic's direction plus noise. A
// query means one topic — its intended vector — but is typed ambiguously, half
// way towards a second topic, so a plain vector search returns a mix. The
// documents that answer it are the ten nearest its intended vector. That is
// the situation relevance feedback is for: the agent can tell the right
// results from the wrong ones, and cannot say so in words.

// corpus is a deterministic set of document and query vectors.
type corpus struct {
	dims    int
	docs    [][]float32 // index = document number
	queries []query
}

// query is one search: what was typed, and what answers it.
type query struct {
	vector   []float32
	relevant map[int]bool // the ten documents nearest the intended vector
}

// newCorpus builds topics×perTopic documents and nQueries queries from seed.
//
// #nosec G404 -- synthetic benchmark data, reproducible by design
func newCorpus(seed int64, dims, topics, perTopic, nQueries int) *corpus {
	rng := rand.New(rand.NewSource(seed)) // #nosec G404 -- synthetic benchmark data
	gauss := func(scale float64) []float32 {
		v := make([]float32, dims)
		for i := range v {
			v[i] = float32(rng.NormFloat64() * scale)
		}
		return v
	}
	centroids := make([][]float32, topics)
	for t := range centroids {
		centroids[t] = unit(gauss(1))
	}
	c := &corpus{dims: dims}
	noise := 0.9 / math.Sqrt(float64(dims))
	for t := 0; t < topics; t++ {
		for i := 0; i < perTopic; i++ {
			c.docs = append(c.docs, unit(add(centroids[t], gauss(noise), 1)))
		}
	}
	for q := 0; q < nQueries; q++ {
		t, u := q%topics, (q*7+3)%topics
		if u == t {
			u = (t + 1) % topics
		}
		intended := unit(add(centroids[t], gauss(noise/2), 1))
		typed := unit(add(scale(intended, 0.55), scale(centroids[u], 0.45), 1))
		c.queries = append(c.queries, query{vector: typed, relevant: c.nearest(intended, 10)})
	}
	return c
}

// nearest is the k documents most similar to v.
func (c *corpus) nearest(v []float32, k int) map[int]bool {
	order := make([]int, len(c.docs))
	for i := range order {
		order[i] = i
	}
	sort.Slice(order, func(i, j int) bool { return dot(v, c.docs[order[i]]) > dot(v, c.docs[order[j]]) })
	out := make(map[int]bool, k)
	for _, i := range order[:min(k, len(order))] {
		out[i] = true
	}
	return out
}

// docText is what the bench stores for document i; the embedder reads the
// number back.
func docText(i int) string { return fmt.Sprintf("feedback benchmark document fbdoc-%d", i) }

var docRef = regexp.MustCompile(`fbdoc-(\d+)`)

// ServeHTTP is an OpenAI-compatible embeddings endpoint
// (POST /v1/embeddings) answering with each document's corpus vector.
func (c *corpus) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Input []string `json:"input"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	type datum struct {
		Embedding []float32 `json:"embedding"`
		Index     int       `json:"index"`
	}
	resp := struct {
		Data []datum `json:"data"`
	}{}
	for i, text := range req.Input {
		resp.Data = append(resp.Data, datum{Embedding: c.embed(text), Index: i})
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// embed is the corpus vector of the document text names, or a fixed vector
// for anything else (the server never embeds queries here: the bench sends
// query vectors).
func (c *corpus) embed(text string) []float32 {
	if m := docRef.FindStringSubmatch(text); m != nil {
		var i int
		if _, err := fmt.Sscan(m[1], &i); err == nil && i < len(c.docs) {
			return c.docs[i]
		}
	}
	v := make([]float32, c.dims)
	v[0] = 1
	return v
}

func add(a, b []float32, k float32) []float32 {
	out := make([]float32, len(a))
	for i := range a {
		out[i] = a[i] + k*b[i]
	}
	return out
}

func scale(v []float32, k float32) []float32 { return add(make([]float32, len(v)), v, k) }

func dot(a, b []float32) float32 {
	var s float32
	for i := range a {
		s += a[i] * b[i]
	}
	return s
}

func unit(v []float32) []float32 {
	n := float32(math.Sqrt(float64(dot(v, v))))
	return scale(v, 1/n)
}
