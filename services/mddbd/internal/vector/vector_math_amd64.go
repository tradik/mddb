//go:build amd64 && !noasm

package vector

import (
	"math"

	"golang.org/x/sys/cpu"
)

// SIMD vector math for amd64 (SRCH-011).
//
// The arm64 path reaches NEON through cgo. That is not an option here: release
// binaries are built with CGO_ENABLED=0, so a cgo kernel would compile on a
// developer's machine and never ship. Go's own assembler has no such problem,
// and no call transition cost either — which matters because the search path
// compares one vector at a time, and a 768-dimension comparison takes under a
// microsecond. A cgo boundary crossing would be a visible fraction of that.
//
// The selection is a runtime check, not a build tag: a binary built here has to
// run on a machine without AVX2, and there is one distribution of MDDB, not one
// per microarchitecture.
var useAVX2 = cpu.X86.HasAVX2 && cpu.X86.HasFMA

// vectorMathTier returns the active SIMD acceleration tier.
//
// Read only by tests and benchmarks: they run the same vectors through this
// file and through the scalar implementation and require identical results, so
// the tier has to be reportable to say which half ran.
func vectorMathTier() string {
	if useAVX2 {
		return "avx2"
	}
	return "scalar"
}

// cosinePartsAVX2 accumulates dot(a,b), dot(a,a) and dot(b,b) into out in a
// single pass over both vectors.
//
// n must be a multiple of 8 and no greater than either length. The remainder is
// the caller's business — handling it here would mean masked loads for the sake
// of at most seven elements, and the scalar tail is both faster to write and
// impossible to get subtly wrong.
//
//go:noescape
func cosinePartsAVX2(a, b *float32, n int, out *[3]float32)

// dotAVX2 accumulates dot(a,b). Same contract as cosinePartsAVX2 for n.
//
//go:noescape
func dotAVX2(a, b *float32, n int) float32

// distSqAVX2 accumulates the squared euclidean distance. Same contract for n.
//
//go:noescape
func distSqAVX2(a, b *float32, n int) float32

// CosineSimilarity computes cosine similarity between two vectors.
// Returns value between -1 and 1, where 1 = identical direction.
func CosineSimilarity(a, b []float32) float32 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}

	var dotProduct, normA, normB float32
	i := 0
	if useAVX2 {
		if n := len(a) &^ 7; n > 0 {
			var parts [3]float32
			cosinePartsAVX2(&a[0], &b[0], n, &parts)
			dotProduct, normA, normB = parts[0], parts[1], parts[2]
			i = n
		}
	}
	for ; i < len(a); i++ {
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
	i := 0
	if useAVX2 {
		if n := len(a) &^ 7; n > 0 {
			sum = dotAVX2(&a[0], &b[0], n)
			i = n
		}
	}
	for ; i < len(a); i++ {
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
	return float32(1.0 / (1.0 + math.Sqrt(euclideanDistSq(a, b))))
}

// euclideanDistSq computes squared Euclidean distance between two vectors.
//
// The accumulator is float64 in the scalar path and float32 in the vector one.
// That is a real difference and it is bounded: the oracle test requires the two
// to agree to 1e-5 relative, which they do for embedding-shaped data. Widening
// the SIMD accumulator to float64 would halve the lanes and give back most of
// the speedup to buy precision nothing here needs.
func euclideanDistSq(a, b []float32) float64 {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	if n == 0 {
		return 0
	}

	var sum float64
	i := 0
	if useAVX2 {
		if m := n &^ 7; m > 0 {
			sum = float64(distSqAVX2(&a[0], &b[0], m))
			i = m
		}
	}
	for ; i < n; i++ {
		d := float64(a[i]) - float64(b[i])
		sum += d * d
	}
	return sum
}

// batchCosineSim computes cosine similarity of query against count vectors
// packed contiguously in matrix (row-major, dims per row). Results written to out.
//
// Still a loop over CosineSimilarity rather than a wider kernel. SRCH-009
// measured a dedicated batch kernel against this loop and found no difference:
// the limit is the rate at which the matrix streams from memory, and reading it
// once per row costs the same as reading it once in total.
func batchCosineSim(query []float32, matrix []float32, dims, count int, out []float32) {
	for i := 0; i < count; i++ {
		off := i * dims
		out[i] = CosineSimilarity(query, matrix[off:off+dims])
	}
}
