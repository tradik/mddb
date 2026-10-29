package main

import (
	"fmt"
	"net/http"
	"regexp"

	"mddb-bench/internal/benchkit"
)

// The synthetic corpus and the embedding provider that serves it.
//
// Documents belong to topics: each is its topic's direction plus noise
// (benchkit.Gen.Clustered). A query means one topic — its intended vector —
// but is typed ambiguously, half way towards a second topic, so a plain
// vector search returns a mix. The documents that answer it are the ten
// nearest its intended vector. That is the situation relevance feedback is
// for: the agent can tell the right results from the wrong ones, and cannot
// say so in words.

// corpus is a deterministic set of document and query vectors.
type corpus struct {
	dims    int
	docs    [][]float32 // index = document number
	queries []query
	embed   http.Handler
}

// query is one search: what was typed, and what answers it.
type query struct {
	vector   []float32
	relevant map[int]bool // the ten documents nearest the intended vector
}

// newCorpus builds topics×perTopic documents and nQueries queries from seed.
func newCorpus(seed int64, dims, topics, perTopic, nQueries int) *corpus {
	g := benchkit.NewGen(seed, dims)
	docs, centroids := g.Clustered(topics, perTopic)
	c := &corpus{dims: dims, docs: docs}
	c.embed = &benchkit.Embedder{Docs: docs, Dims: dims, Ref: docRef}
	for q := 0; q < nQueries; q++ {
		t, u := q%topics, (q*7+3)%topics
		if u == t {
			u = (t + 1) % topics
		}
		intended := benchkit.Unit(benchkit.Add(centroids[t], g.Gauss(g.Noise()/2), 1))
		typed := benchkit.Unit(benchkit.Add(benchkit.Scale(intended, 0.55), benchkit.Scale(centroids[u], 0.45), 1))
		c.queries = append(c.queries, query{vector: typed, relevant: c.nearest(intended, 10)})
	}
	return c
}

// nearest is the k documents most similar to v.
func (c *corpus) nearest(v []float32, k int) map[int]bool {
	out := make(map[int]bool, k)
	for _, i := range benchkit.Nearest(c.docs, v, k, nil) {
		out[i] = true
	}
	return out
}

// docText is what the bench stores for document i; the embedder reads the
// number back.
func docText(i int) string { return fmt.Sprintf("feedback benchmark document fbdoc-%d", i) }

var docRef = regexp.MustCompile(`fbdoc-(\d+)`)

// ServeHTTP is the corpus's OpenAI-compatible embeddings endpoint.
func (c *corpus) ServeHTTP(w http.ResponseWriter, r *http.Request) { c.embed.ServeHTTP(w, r) }
