package vector

import (
	"fmt"
	"math"
	"math/rand"
	"testing"
)

func TestVectorMathTier(t *testing.T) {
	tier := vectorMathTier()
	valid := map[string]bool{"scalar": true, "neon": true, "sme": true, "avx2": true}
	if !valid[tier] {
		t.Errorf("vectorMathTier() = %q, want one of scalar/neon/sme/avx2", tier)
	}
	t.Logf("vector math tier: %s", tier)
}

func TestCosineSimilarityIdentical(t *testing.T) {
	a := []float32{1, 2, 3, 4, 5}
	got := CosineSimilarity(a, a)
	if math.Abs(float64(got)-1.0) > 1e-5 {
		t.Errorf("CosineSimilarity(a, a) = %v, want 1.0", got)
	}
}

func TestCosineSimilarityOrthogonal(t *testing.T) {
	a := []float32{1, 0, 0}
	b := []float32{0, 1, 0}
	got := CosineSimilarity(a, b)
	if math.Abs(float64(got)) > 1e-5 {
		t.Errorf("CosineSimilarity(orthogonal) = %v, want 0.0", got)
	}
}

func TestCosineSimilarityOpposite(t *testing.T) {
	a := []float32{1, 2, 3}
	b := []float32{-1, -2, -3}
	got := CosineSimilarity(a, b)
	if math.Abs(float64(got)+1.0) > 1e-5 {
		t.Errorf("CosineSimilarity(opposite) = %v, want -1.0", got)
	}
}

func TestCosineSimilarityEdgeCases(t *testing.T) {
	// Empty vectors
	if CosineSimilarity(nil, nil) != 0 {
		t.Error("nil vectors should return 0")
	}
	if CosineSimilarity([]float32{}, []float32{}) != 0 {
		t.Error("empty vectors should return 0")
	}
	// Mismatched lengths
	if CosineSimilarity([]float32{1}, []float32{1, 2}) != 0 {
		t.Error("mismatched lengths should return 0")
	}
	// Zero vectors
	if CosineSimilarity([]float32{0, 0}, []float32{1, 1}) != 0 {
		t.Error("zero vector should return 0")
	}
}

func TestDotProductSimilarityAccel(t *testing.T) {
	a := []float32{1, 2, 3}
	b := []float32{4, 5, 6}
	got := dotProductSimilarity(a, b)
	want := float32(32.0) // 1*4 + 2*5 + 3*6
	if math.Abs(float64(got-want)) > 1e-5 {
		t.Errorf("dotProductSimilarity() = %v, want %v", got, want)
	}
}

func TestDotProductSimilarityAccelEdgeCases(t *testing.T) {
	if dotProductSimilarity(nil, nil) != 0 {
		t.Error("nil vectors should return 0")
	}
	if dotProductSimilarity([]float32{}, []float32{}) != 0 {
		t.Error("empty vectors should return 0")
	}
	if dotProductSimilarity([]float32{1}, []float32{1, 2}) != 0 {
		t.Error("mismatched lengths should return 0")
	}
}

func TestEuclideanSimilarityAccel(t *testing.T) {
	// Identical vectors = distance 0, similarity 1
	a := []float32{1, 2, 3}
	got := euclideanSimilarity(a, a)
	if math.Abs(float64(got)-1.0) > 1e-5 {
		t.Errorf("euclideanSimilarity(a, a) = %v, want 1.0", got)
	}

	// Far apart = low similarity
	b := []float32{100, 200, 300}
	far := euclideanSimilarity(a, b)
	if far >= 0.5 {
		t.Errorf("euclideanSimilarity(far) = %v, want < 0.5", far)
	}
}

func TestEuclideanSimilarityAccelEdgeCases(t *testing.T) {
	if euclideanSimilarity(nil, nil) != 0 {
		t.Error("nil vectors should return 0")
	}
	if euclideanSimilarity([]float32{}, []float32{}) != 0 {
		t.Error("empty vectors should return 0")
	}
	if euclideanSimilarity([]float32{1}, []float32{1, 2}) != 0 {
		t.Error("mismatched lengths should return 0")
	}
}

func TestEuclideanDistSq(t *testing.T) {
	a := []float32{1, 2, 3}
	b := []float32{4, 6, 3}
	got := euclideanDistSq(a, b)
	want := 25.0 // (3^2 + 4^2 + 0^2)
	if math.Abs(got-want) > 1e-5 {
		t.Errorf("euclideanDistSq() = %v, want %v", got, want)
	}

	// Identical = 0
	if euclideanDistSq(a, a) != 0 {
		t.Error("euclideanDistSq(a, a) should be 0")
	}
}

func TestBatchCosineSim(t *testing.T) {
	query := []float32{1, 0, 0}
	// Pack 3 vectors contiguously
	matrix := []float32{
		1, 0, 0, // identical to query
		0, 1, 0, // orthogonal
		-1, 0, 0, // opposite
	}
	out := make([]float32, 3)
	batchCosineSim(query, matrix, 3, 3, out)

	if math.Abs(float64(out[0])-1.0) > 1e-5 {
		t.Errorf("batch[0] (identical) = %v, want 1.0", out[0])
	}
	if math.Abs(float64(out[1])) > 1e-5 {
		t.Errorf("batch[1] (orthogonal) = %v, want 0.0", out[1])
	}
	if math.Abs(float64(out[2])+1.0) > 1e-5 {
		t.Errorf("batch[2] (opposite) = %v, want -1.0", out[2])
	}
}

func TestBatchCosineSimEmpty(t *testing.T) {
	// Should not panic
	batchCosineSim([]float32{1, 2}, nil, 2, 0, nil)
	batchCosineSim(nil, nil, 0, 0, nil)
}

// TestCosineSimilarityLargeDims verifies correctness at typical embedding dimensions.
func TestCosineSimilarityLargeDims(t *testing.T) {
	dims := []int{768, 1024, 1536, 3072}
	for _, d := range dims {
		t.Run(
			fmt.Sprintf("dims_%d", d),
			func(t *testing.T) {
				rng := rand.New(rand.NewSource(42)) //nolint:gosec // G404: math/rand fine for test data
				a := make([]float32, d)
				b := make([]float32, d)
				for i := range a {
					a[i] = rng.Float32()*2 - 1
					b[i] = rng.Float32()*2 - 1
				}

				got := CosineSimilarity(a, b)
				if got < -1.0 || got > 1.0 {
					t.Errorf("CosineSimilarity out of range: %v", got)
				}

				// Self-similarity should be 1.0
				self := CosineSimilarity(a, a)
				if math.Abs(float64(self)-1.0) > 1e-4 {
					t.Errorf("self-similarity = %v, want 1.0", self)
				}
			},
		)
	}
}

// TestBatchCosineSimLarge tests batch with realistic embedding dimensions.
func TestBatchCosineSimLarge(t *testing.T) {
	const dims = 768
	const count = 100
	rng := rand.New(rand.NewSource(42)) //nolint:gosec // G404: math/rand fine for test data

	query := make([]float32, dims)
	for i := range query {
		query[i] = rng.Float32()*2 - 1
	}

	matrix := make([]float32, dims*count)
	for i := range matrix {
		matrix[i] = rng.Float32()*2 - 1
	}

	out := make([]float32, count)
	batchCosineSim(query, matrix, dims, count, out)

	// Verify each batch result matches individual computation
	for i := 0; i < count; i++ {
		vec := matrix[i*dims : (i+1)*dims]
		expected := CosineSimilarity(query, vec)
		diff := math.Abs(float64(out[i] - expected))
		if diff > 1e-5 {
			t.Errorf("batch[%d] = %v, individual = %v, diff = %v", i, out[i], expected, diff)
		}
	}
}

// TestResolveSimilarity verifies metric dispatch.
func TestResolveSimilarityAccel(t *testing.T) {
	cases := []struct {
		name string
		a, b []float32
	}{
		{"cosine", []float32{1, 2, 3}, []float32{4, 5, 6}},
		{"dot_product", []float32{1, 2, 3}, []float32{4, 5, 6}},
		{"euclidean", []float32{1, 2, 3}, []float32{4, 5, 6}},
		{"unknown", []float32{1, 2, 3}, []float32{4, 5, 6}},
		{"", []float32{1, 2, 3}, []float32{4, 5, 6}},
	}

	for _, tc := range cases {
		fn := ResolveSimilarity(tc.name)
		if fn == nil {
			t.Errorf("ResolveSimilarity(%q) returned nil", tc.name)
			continue
		}
		score := fn(tc.a, tc.b)
		// Just verify it returns a finite number
		if math.IsNaN(float64(score)) || math.IsInf(float64(score), 0) {
			t.Errorf("ResolveSimilarity(%q) returned non-finite: %v", tc.name, score)
		}
	}
}
