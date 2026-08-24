package vector

import (
	"fmt"
	"math"
	"math/rand"
	"testing"
)

// The accelerated kernels are checked against a reference written out in full
// here rather than against the scalar build (SRCH-011).
//
// The scalar file does not compile into this binary on amd64 — that is the
// whole point of the build tags — so comparing against it would mean building
// twice and comparing across processes. A reference defined in the test runs in
// the same binary as the kernel it is judging, on the same inputs, and is
// simple enough to read and agree with.

func refCosine(a, b []float32) float64 {
	var dot, na, nb float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		na += float64(a[i]) * float64(a[i])
		nb += float64(b[i]) * float64(b[i])
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / math.Sqrt(na*nb)
}

func refDot(a, b []float32) float64 {
	var sum float64
	for i := range a {
		sum += float64(a[i]) * float64(b[i])
	}
	return sum
}

func refDistSq(a, b []float32) float64 {
	var sum float64
	for i := range a {
		d := float64(a[i]) - float64(b[i])
		sum += d * d
	}
	return sum
}

// oracleDims covers every shape the kernels distinguish: below one vector
// register, exactly one, register-aligned, and every remainder from 1 to 7 —
// because the tail is handled in Go and the boundary between the two is where
// an off-by-one lives. 768 and 1536 are the dimensions real embedding models
// produce.
var oracleDims = []int{1, 3, 7, 8, 9, 15, 16, 17, 31, 32, 33, 63, 64, 100, 127, 128, 129, 384, 768, 1000, 1536}

func randomVec(rnd *rand.Rand, n int, scale float64) []float32 {
	v := make([]float32, n)
	for i := range v {
		v[i] = float32((rnd.Float64()*2 - 1) * scale)
	}
	return v
}

// float32Epsilon is 2^-23: the gap between 1 and the next representable
// float32.
const float32Epsilon = 1.1920929e-7

// termScale is the sum of |a_i * b_i|, which is what bounds the rounding error
// of a float32 dot product — not the sum itself.
//
// This distinction is the whole reason this helper exists. The first version of
// this test compared against the float64 reference relative to the *result*, and
// failed on cases where 384 products of magnitude 100 sum to 3.1: the absolute
// error was 1e-4, ordinary for float32, and enormous next to a result that
// small. Running the same test against the scalar build showed it failing more
// cases than the AVX2 one, which is the tell — the accelerated kernel is more
// accurate, because eight lanes each accumulate an eighth of the terms and
// carry an eighth of the rounding error. The tolerance was wrong, not the code.
func termScale(a, b []float32) float64 {
	var sum float64
	for i := range a {
		sum += math.Abs(float64(a[i]) * float64(b[i]))
	}
	return sum
}

// tolerance for a float32 accumulation of n terms whose magnitudes sum to
// scale. The factor of 4 is slack for the reassociation an 8-lane reduction
// performs, which is a different summation order rather than a worse one.
func tolerance(n int, scale float64) float64 {
	return 4 * float32Epsilon * float64(n) * scale
}

func TestAcceleratedMathMatchesReference(t *testing.T) {
	t.Logf("tier under test: %s", vectorMathTier())
	rnd := rand.New(rand.NewSource(20260824)) // #nosec G404 -- fixture data

	// Scales spread over four orders of magnitude: a float32 accumulator loses
	// precision differently at each, and the kernels accumulate in float32
	// while the reference does not.
	for _, scale := range []float64{1e-3, 1, 10, 1e3} {
		for _, dims := range oracleDims {
			for trial := 0; trial < 20; trial++ {
				a := randomVec(rnd, dims, scale)
				b := randomVec(rnd, dims, scale)

				name := fmt.Sprintf("dims=%d/scale=%g/trial=%d", dims, scale, trial)
				dotTol := tolerance(dims, termScale(a, b))

				if got, want := float64(dotProductSimilarity(a, b)), refDot(a, b); math.Abs(got-want) > dotTol {
					t.Errorf("%s dotProduct = %.9g, reference %.9g (err %.3g > %.3g)",
						name, got, want, math.Abs(got-want), dotTol)
				}

				// Cosine divides by |a||b|, so the dot product's absolute error
				// is scaled by the same factor.
				norm := math.Sqrt(termScale(a, a) * termScale(b, b))
				cosTol := dotTol / norm
				if got, want := float64(CosineSimilarity(a, b)), refCosine(a, b); math.Abs(got-want) > cosTol {
					t.Errorf("%s CosineSimilarity = %.9g, reference %.9g (err %.3g > %.3g)",
						name, got, want, math.Abs(got-want), cosTol)
				}

				// Squared distance sums non-negative terms, so nothing cancels
				// and the relative error stays near the accumulation bound.
				dsq := make([]float32, dims)
				for i := range dsq {
					dsq[i] = a[i] - b[i]
				}
				distTol := tolerance(dims, termScale(dsq, dsq))
				if got, want := euclideanDistSq(a, b), refDistSq(a, b); math.Abs(got-want) > distTol {
					t.Errorf("%s euclideanDistSq = %.9g, reference %.9g (err %.3g > %.3g)",
						name, got, want, math.Abs(got-want), distTol)
				}
			}
		}
	}
}

func TestAcceleratedCosineOfAVectorWithItself(t *testing.T) {
	rnd := rand.New(rand.NewSource(7)) // #nosec G404 -- fixture data
	for _, dims := range oracleDims {
		a := randomVec(rnd, dims, 1)
		if got := CosineSimilarity(a, a); math.Abs(float64(got)-1) > 1e-6 {
			t.Errorf("dims=%d: CosineSimilarity(a,a) = %.9f, want 1", dims, got)
		}
		if got := euclideanDistSq(a, a); got > 1e-9 {
			t.Errorf("dims=%d: euclideanDistSq(a,a) = %.9g, want 0", dims, got)
		}
	}
}

// Every lane must contribute. A kernel that sums only the low 128 bits of each
// accumulator, or drops the tail, passes random-data tests within tolerance and
// fails this one outright: the answer is the element count.
func TestAcceleratedKernelsUseEveryLane(t *testing.T) {
	for _, dims := range oracleDims {
		ones := make([]float32, dims)
		for i := range ones {
			ones[i] = 1
		}
		if got, want := float64(dotProductSimilarity(ones, ones)), float64(dims); got != want {
			t.Errorf("dims=%d: dot(ones,ones) = %g, want %g — a lane or the tail is missing",
				dims, got, want)
		}

		twos := make([]float32, dims)
		for i := range twos {
			twos[i] = 3
		}
		// (3-1)^2 per element.
		if got, want := euclideanDistSq(twos, ones), float64(4*dims); got != want {
			t.Errorf("dims=%d: distSq = %g, want %g", dims, got, want)
		}
	}
}
