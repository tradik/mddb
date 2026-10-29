// Package benchkit is what the vector benchmarks under tools/bench share: a
// seeded clustered corpus, the vector arithmetic it is built with, exact
// nearest neighbours as ground truth, an OpenAI-compatible embeddings endpoint
// that serves the corpus, and the HTTP and statistics helpers every bench
// needs. One generator, so two benches never disagree on what "the synthetic
// corpus" means.
package benchkit

import (
	"container/heap"
	"math"
	"math/rand"
)

// Gen draws reproducible vectors of one dimensionality from a seed.
type Gen struct {
	Dims int
	rng  *rand.Rand
}

// NewGen seeds a generator.
func NewGen(seed int64, dims int) *Gen {
	return &Gen{Dims: dims, rng: rand.New(rand.NewSource(seed))} // #nosec G404 -- synthetic benchmark data, reproducible by design
}

// Gauss is a vector of independent normal components scaled by scale.
func (g *Gen) Gauss(scale float64) []float32 {
	v := make([]float32, g.Dims)
	for i := range v {
		v[i] = float32(g.rng.NormFloat64() * scale)
	}
	return v
}

// Noise is the per-component spread of a document around its topic: about
// 0.9 of a unit vector's length in total, whatever the dimensionality.
func (g *Gen) Noise() float64 { return 0.9 / math.Sqrt(float64(g.Dims)) }

// Clustered draws topics unit centroids, then perTopic unit documents around
// each, topic by topic: document t*perTopic+i belongs to topic t.
func (g *Gen) Clustered(topics, perTopic int) (docs, centroids [][]float32) {
	centroids = make([][]float32, topics)
	for t := range centroids {
		centroids[t] = Unit(g.Gauss(1))
	}
	docs = make([][]float32, 0, topics*perTopic)
	for t := 0; t < topics; t++ {
		for i := 0; i < perTopic; i++ {
			docs = append(docs, Unit(Add(centroids[t], g.Gauss(g.Noise()), 1)))
		}
	}
	return docs, centroids
}

// Add is a + k·b.
func Add(a, b []float32, k float32) []float32 {
	out := make([]float32, len(a))
	for i := range a {
		out[i] = a[i] + k*b[i]
	}
	return out
}

// Scale is k·v.
func Scale(v []float32, k float32) []float32 { return Add(make([]float32, len(v)), v, k) }

// Dot is the inner product; on unit vectors, the cosine.
func Dot(a, b []float32) float32 {
	var s float32
	for i := range a {
		s += a[i] * b[i]
	}
	return s
}

// Unit is v scaled to length 1.
func Unit(v []float32) []float32 {
	n := float32(math.Sqrt(float64(Dot(v, v))))
	return Scale(v, 1/n)
}

// Nearest is the exact k documents most similar to v, best first, among those
// allow admits (nil admits all): the ground truth an index is scored against.
func Nearest(docs [][]float32, v []float32, k int, allow func(int) bool) []int {
	h := &scored{}
	for i, d := range docs {
		if allow != nil && !allow(i) {
			continue
		}
		s := Dot(v, d)
		switch {
		case h.Len() < k:
			heap.Push(h, hit{i, s})
		case k > 0 && s > (*h)[0].score:
			(*h)[0] = hit{i, s}
			heap.Fix(h, 0)
		}
	}
	out := make([]int, h.Len())
	for i := len(out) - 1; i >= 0; i-- {
		out[i] = heap.Pop(h).(hit).doc
	}
	return out
}

type hit struct {
	doc   int
	score float32
}

// scored is a min-heap on score: its root is the weakest of the best k.
type scored []hit

func (h scored) Len() int           { return len(h) }
func (h scored) Less(i, j int) bool { return h[i].score < h[j].score }
func (h scored) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *scored) Push(x any)        { *h = append(*h, x.(hit)) }
func (h *scored) Pop() any {
	old := *h
	x := old[len(old)-1]
	*h = old[:len(old)-1]
	return x
}
