package vector

import (
	"fmt"
	"math/rand"
	"testing"
)

// SRCH-009 step 1: measure the batch kernel against the per-vector loop the
// search path actually runs, at the sizes real collections use.
//
// The comparison is only interesting on a build where the two differ. On the
// scalar build `batchCosineSim` *is* the loop — same function, called in the
// same order — so any gap here is measurement noise, and that being visible is
// the point: it says where the change can and cannot pay.

func benchVectors(dims, count int) ([]float32, []float32, []float32) {
	rng := rand.New(rand.NewSource(1)) // #nosec G404 -- deterministic benchmark fixture
	query := make([]float32, dims)
	for i := range query {
		query[i] = rng.Float32()*2 - 1
	}
	// Row-major and contiguous, which is what the kernel needs and what the
	// document store does not currently guarantee — the actual cost of wiring
	// this into search, as opposed to the call itself.
	matrix := make([]float32, dims*count)
	for i := range matrix {
		matrix[i] = rng.Float32()*2 - 1
	}
	return query, matrix, make([]float32, count)
}

func BenchmarkCosinePerVectorLoop(b *testing.B) {
	for _, dims := range []int{768, 1024, 1536} {
		for _, count := range []int{1_000, 10_000, 100_000} {
			query, matrix, out := benchVectors(dims, count)
			b.Run(fmt.Sprintf("dims=%d/count=%d", dims, count), func(b *testing.B) {
				b.SetBytes(int64(dims * count * 4))
				for b.Loop() {
					// What vector_index.go and vector_ivf.go do today.
					for i := 0; i < count; i++ {
						off := i * dims
						out[i] = CosineSimilarity(query, matrix[off:off+dims])
					}
				}
			})
		}
	}
}

func BenchmarkCosineBatchKernel(b *testing.B) {
	for _, dims := range []int{768, 1024, 1536} {
		for _, count := range []int{1_000, 10_000, 100_000} {
			query, matrix, out := benchVectors(dims, count)
			b.Run(fmt.Sprintf("dims=%d/count=%d", dims, count), func(b *testing.B) {
				b.SetBytes(int64(dims * count * 4))
				for b.Loop() {
					batchCosineSim(query, matrix, dims, count, out)
				}
			})
		}
	}
}

// Whatever the speed, the two must agree — the scalar half is the oracle the
// accelerated one is checked against, which is why neither can be deleted.
func TestBatchKernelAgreesWithThePerVectorLoop(t *testing.T) {
	for _, dims := range []int{8, 768, 1536} {
		query, matrix, out := benchVectors(dims, 64)
		batchCosineSim(query, matrix, dims, 64, out)

		for i := 0; i < 64; i++ {
			off := i * dims
			want := CosineSimilarity(query, matrix[off:off+dims])
			if diff := out[i] - want; diff > 1e-5 || diff < -1e-5 {
				t.Fatalf("dims=%d vector %d: batch %v, loop %v", dims, i, out[i], want)
			}
		}
	}
}
