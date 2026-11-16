package main

import (
	"fmt"
	"regexp"
	"strings"
	"sync"

	"mddb-bench/internal/benchkit"
)

// The search corpus: benchkit's seeded clustered vectors, one document each.
// A document's text carries words of its topic (what full-text search finds),
// filler every topic shares, and a marker the embeddings endpoint reads the
// vector back from. Its metadata puts it in nested slices of 1, 2, 10, 25 and
// 50 percent of the corpus, for the filtered scenarios. Everything follows
// from the seed and the document number: no dataset is stored anywhere.

// corpus is one collection's documents, queries and exact answers.
type corpus struct {
	collection string
	docs       [][]float32
	queries    [][]float32 // Warmup + Queries of them, topic by topic
	perTopic   int
	topics     int
}

// newCorpus draws the corpus of profile p.
func newCorpus(p profile, seed int64) *corpus {
	g := benchkit.NewGen(seed, p.Dims)
	docs, centroids := g.Clustered(p.Topics, p.Vectors/p.Topics)
	c := &corpus{collection: "bench-" + p.Name, docs: docs, perTopic: p.Vectors / p.Topics, topics: p.Topics}
	for q := 0; q < p.Queries+p.Warmup; q++ {
		c.queries = append(c.queries, benchkit.Unit(benchkit.Add(centroids[q%p.Topics], g.Gauss(g.Noise()/2), 1)))
	}
	return c
}

// embedder serves the corpus to the server: marker N names document N, and
// markers past the last document name the queries, so hybrid search — which
// takes its query as text — searches with the same vectors vector search does.
func (c *corpus) embedder() *benchkit.Embedder {
	all := append(c.docs[:len(c.docs):len(c.docs)], c.queries...)
	return &benchkit.Embedder{Docs: all, Dims: len(c.docs[0]), Ref: markerRef}
}

var markerRef = regexp.MustCompile(`bvec(\d+)`)

func docKey(i int) string { return fmt.Sprintf("d%08d", i) }

// keyDoc is the document number of a key, -1 for a key that is not a corpus
// document's.
func keyDoc(key string) int {
	var i int
	if _, err := fmt.Sscanf(key, "d%08d", &i); err != nil {
		return -1
	}
	return i
}

// syllables make pronounceable words no stemmer or stop list knows.
var syllables = func() []string {
	var out []string
	for _, c := range "bdfgklmnprstvz" {
		for _, v := range "aeiou" {
			out = append(out, string(c)+string(v))
		}
	}
	return out
}()

// word is the n-th synthetic word: three syllables, distinct for every n
// below len(syllables)³.
func word(n int) string {
	s := len(syllables)
	return syllables[n%s] + syllables[n/s%s] + syllables[n/s/s%s]
}

const (
	fillerWords = 400 // words every topic shares
	topicWords  = 6   // words only one topic uses
	docWords    = 40  // filler words in a document
)

// topicWord is word j of topic t.
func topicWord(t, j int) string { return word(fillerWords + t*topicWords + j%topicWords) }

// docText is what document i stores: its marker, three of its topic's words
// and filler chosen by a generator seeded with i.
func (c *corpus) docText(i int) string {
	t := i / c.perTopic
	var b strings.Builder
	fmt.Fprintf(&b, "bvec%d %s %s %s", i, topicWord(t, i), topicWord(t, i+1), topicWord(t, i+3))
	x := uint32(i)*2654435761 + 1 // #nosec G115 -- a hash seed; wrapping is the point
	for w := 0; w < docWords; w++ {
		x = x*1664525 + 1013904223
		b.WriteByte(' ')
		b.WriteString(word(int(x>>16) % fillerWords))
	}
	return b.String()
}

// queryText is query q as text: two words of its topic, and for hybrid
// search the marker of its vector.
func (c *corpus) queryText(q int, withVector bool) string {
	t := q % c.topics
	text := topicWord(t, q) + " " + topicWord(t, q+1)
	if withVector {
		text += fmt.Sprintf(" bvec%d", len(c.docs)+q)
	}
	return text
}

// admits reports whether document i is in the slice of pct percent.
func admits(i, pct int) bool { return i%(100/pct) == 0 }

// docMeta is document i's metadata: the slices it is in.
func docMeta(i int) map[string][]string {
	in := []string{"all"}
	for _, pct := range selectivities {
		if admits(i, pct) {
			in = append(in, fmt.Sprintf("p%d", pct))
		}
	}
	return map[string][]string{"sel": in}
}

// document is document i as add and add-batch take it.
func (c *corpus) document(i int) map[string]any {
	return map[string]any{"key": docKey(i), "lang": "en", "contentMd": c.docText(i), "meta": docMeta(i)}
}

// truths is the exact top 10 of queries [from, len) among the documents in
// the slice of pct percent (0 = all), computed on every core.
func (c *corpus) truths(from, pct int) [][]int {
	out := make([][]int, len(c.queries)-from)
	var allow func(int) bool
	if pct > 0 {
		allow = func(i int) bool { return admits(i, pct) }
	}
	var wg sync.WaitGroup
	for q := from; q < len(c.queries); q++ {
		wg.Add(1)
		go func(q int) {
			defer wg.Done()
			out[q-from] = benchkit.Nearest(c.docs, c.queries[q], 10, allow)
		}(q)
	}
	wg.Wait()
	return out
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
