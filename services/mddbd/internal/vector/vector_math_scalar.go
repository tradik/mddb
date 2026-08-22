//go:build !arm64 || nosme || !cgo

package vector

import "math"

// vectorMathTier returns the active SIMD acceleration tier.
//
// Read only by tests and benchmarks: they run the same vectors through this
// file and through vector_math_arm64.go and require identical results, so the
// tier has to be reportable to say which half ran.
func vectorMathTier() string { return "scalar" }

// CosineSimilarity computes cosine similarity between two vectors.
// Returns value between -1 and 1, where 1 = identical direction.
func CosineSimilarity(a, b []float32) float32 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}

	var dotProduct, normA, normB float32
	for i := range a {
		dotProduct += a[i] * b[i]
		normA += a[i] * a[i]
		normB += b[i] * b[i]
	}

	if normA == 0 || normB == 0 {
		return 0
	}

	return dotProduct / float32(math.Sqrt(float64(normA)*float64(normB)))
}

// dotProductSimilarity computes the dot product between two vectors.
// For normalized vectors (e.g. OpenAI embeddings) this equals cosine similarity.
func dotProductSimilarity(a, b []float32) float32 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	var sum float32
	for i := range a {
		sum += a[i] * b[i]
	}
	return sum
}

// euclideanSimilarity converts Euclidean distance to a similarity score.
// Returns 1/(1+dist), so closer vectors -> higher score (range 0 to 1).
func euclideanSimilarity(a, b []float32) float32 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	var sum float64
	for i := range a {
		d := float64(a[i]) - float64(b[i])
		sum += d * d
	}
	return float32(1.0 / (1.0 + math.Sqrt(sum)))
}

// euclideanDistSq computes squared Euclidean distance between two vectors.
func euclideanDistSq(a, b []float32) float64 {
	var sum float64
	for i := range a {
		if i >= len(b) {
			break
		}
		d := float64(a[i]) - float64(b[i])
		sum += d * d
	}
	return sum
}

// batchCosineSim computes cosine similarity of query against count vectors
// packed contiguously in matrix (row-major, dims per row). Results written to out.
//
// The batch kernel is what SIMD accelerates, and the search path does not use
// it: vector_index.go calls CosineSimilarity one vector at a time. Wiring it in
// is a measured change rather than a rename — see SRCH-009. It is exercised
// here so the scalar and accelerated kernels cannot drift apart in the
// meantime.
func batchCosineSim(query []float32, matrix []float32, dims, count int, out []float32) {
	for i := 0; i < count; i++ {
		off := i * dims
		out[i] = CosineSimilarity(query, matrix[off:off+dims])
	}
}
