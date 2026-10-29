package vector

import (
	"math"
	"sort"
)

// Relevance feedback (SRCH-015).
//
// After a first search an agent knows which results were right and which were
// not. Rewriting the query text to say so costs tokens and is guesswork; the
// examples themselves say it exactly. Two ways to use them:
//
//   - query (Rocchio): move the query vector towards the positives and away
//     from the negatives, q' = α·q + β·mean(pos) − γ·mean(neg). One vector
//     operation before the search, so it costs nothing per candidate and works
//     with every index.
//   - rescore: search with the query as given, then add to each candidate's
//     score weight × (mean similarity to the positives − mean similarity to the
//     negatives). It needs every candidate's vector, so it costs more, and it
//     can only reorder what the index returned.
//
// Every vector is taken at unit length first: an example is evidence of a
// direction, and a long document must not outweigh a short one.

// RocchioWeights are the coefficients of q' = α·q + β·mean(pos) − γ·mean(neg).
type RocchioWeights struct {
	Alpha, Beta, Gamma float64
}

// DefaultRocchio is the textbook setting: the query keeps its full weight, the
// positives pull at 0.75 and the negatives push at 0.25.
var DefaultRocchio = RocchioWeights{Alpha: 1, Beta: 0.75, Gamma: 0.25}

// Normalize returns v scaled to unit length, as a copy. A zero vector has no
// direction and is returned as zeros.
func Normalize(v []float32) []float32 {
	out := make([]float32, len(v))
	var sum float64
	for _, x := range v {
		sum += float64(x) * float64(x)
	}
	if sum == 0 {
		return out
	}
	inv := 1 / math.Sqrt(sum)
	for i, x := range v {
		out[i] = float32(float64(x) * inv)
	}
	return out
}

// Centroid is the mean of vs at unit length, over the vectors of the first
// one's dimension; nil when there are none.
func Centroid(vs [][]float32) []float32 {
	if len(vs) == 0 {
		return nil
	}
	dims := len(vs[0])
	sum := make([]float64, dims)
	n := 0
	for _, v := range vs {
		if len(v) != dims {
			continue
		}
		for i, x := range Normalize(v) {
			sum[i] += float64(x)
		}
		n++
	}
	out := make([]float32, dims)
	for i, x := range sum {
		out[i] = float32(x / float64(n))
	}
	return out
}

// Rocchio moves query towards the positives and away from the negatives and
// returns the result at unit length. A side with no examples, or examples of
// another dimension, contributes nothing.
func Rocchio(query []float32, positive, negative [][]float32, w RocchioWeights) []float32 {
	q := Normalize(query)
	out := make([]float32, len(q))
	for i, x := range q {
		out[i] = float32(w.Alpha * float64(x))
	}
	addScaled(out, Centroid(positive), w.Beta)
	addScaled(out, Centroid(negative), -w.Gamma)
	return Normalize(out)
}

// addScaled adds k·v to dst when the dimensions agree.
func addScaled(dst, v []float32, k float64) {
	if len(v) != len(dst) {
		return
	}
	for i, x := range v {
		dst[i] += float32(k * float64(x))
	}
}

// RescoreFeedback adds weight × (mean similarity to the positives − mean
// similarity to the negatives) to every result whose vector is in vectors,
// and re-sorts by the new score. A result without a vector keeps its score.
func RescoreFeedback(results []VectorResult, vectors map[string][]float32, positive, negative [][]float32,
	weight float64, metric SimilarityFunc) []VectorResult {
	if metric == nil {
		metric = CosineSimilarity
	}
	for i := range results {
		v, ok := vectors[results[i].DocID]
		if !ok {
			continue
		}
		shift := meanSimilarity(v, positive, metric) - meanSimilarity(v, negative, metric)
		results[i].Score += float32(weight * shift)
	}
	sort.SliceStable(results, func(i, j int) bool { return results[i].Score > results[j].Score })
	return results
}

// meanSimilarity is v's mean similarity to the examples of its dimension; 0
// when there are none.
func meanSimilarity(v []float32, examples [][]float32, metric SimilarityFunc) float64 {
	var sum float64
	n := 0
	for _, e := range examples {
		if len(e) != len(v) {
			continue
		}
		sum += float64(metric(v, e))
		n++
	}
	if n == 0 {
		return 0
	}
	return sum / float64(n)
}
