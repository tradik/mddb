package main

import (
	"fmt"
	"net/http"
	"regexp"
	"sync/atomic"

	"mddb-bench/internal/benchkit"
)

// The corpus one size is measured on: benchkit's clustered documents, a query
// per topic in turn near that topic's centroid, and one document in RareEvery
// tagged slice=rare, spread evenly over the topics, for the filtered run.

// corpus is one collection's documents, queries and exact answers.
type corpus struct {
	collection string
	docs       [][]float32
	queries    [][]float32
	embedder   *benchkit.Embedder
	rareEvery  int
}

// newCorpus builds the corpus for a collection of about n vectors: a whole
// number of documents per topic, so n rounded down to a multiple of topics.
func newCorpus(cfg config, n int) *corpus {
	g := benchkit.NewGen(cfg.Seed, cfg.Dims)
	docs, centroids := g.Clustered(cfg.Topics, n/cfg.Topics)
	c := &corpus{
		collection: fmt.Sprintf("%s-%d", cfg.Collection, len(docs)),
		docs:       docs,
		embedder:   &benchkit.Embedder{Docs: docs, Dims: cfg.Dims, Ref: docRef},
		rareEvery:  cfg.RareEvery,
	}
	for q := 0; q < cfg.Queries+cfg.Warmup; q++ {
		c.queries = append(c.queries, benchkit.Unit(benchkit.Add(centroids[q%cfg.Topics], g.Gauss(g.Noise()/2), 1)))
	}
	return c
}

// rare reports whether document i matches the selective filter.
func (c *corpus) rare(i int) bool { return i%c.rareEvery == 0 }

// rareCount is how many documents the selective filter admits.
func (c *corpus) rareCount() int { return (len(c.docs) + c.rareEvery - 1) / c.rareEvery }

// truth is the exact top 10 for query q, among the rare documents when
// filtered.
func (c *corpus) truth(q int, filtered bool) []int {
	var allow func(int) bool
	if filtered {
		allow = c.rare
	}
	return benchkit.Nearest(c.docs, c.queries[q], 10, allow)
}

func docKey(i int) string { return fmt.Sprintf("p%07d", i) }

func keyDoc(key string) int {
	var i int
	_, _ = fmt.Sscanf(key, "p%07d", &i)
	return i
}

// docText is what the bench stores for document i; the embedder reads the
// number back. The collection is part of the text: MDDB caches embeddings by
// text, and document 7 of the 2k corpus is not document 7 of the 200k one.
func (c *corpus) docText(i int) string {
	return fmt.Sprintf("planner benchmark document %s pldoc-%d", c.collection, i)
}

var docRef = regexp.MustCompile(`pldoc-(\d+)`)

// embedSwitch serves the embeddings of whichever corpus is being loaded.
type embedSwitch struct {
	cur atomic.Pointer[benchkit.Embedder]
}

func (e *embedSwitch) set(emb *benchkit.Embedder) { e.cur.Store(emb) }

func (e *embedSwitch) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	emb := e.cur.Load()
	if emb == nil {
		http.Error(w, "no corpus loaded", http.StatusServiceUnavailable)
		return
	}
	emb.ServeHTTP(w, r)
}
