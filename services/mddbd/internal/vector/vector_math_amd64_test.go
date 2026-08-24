//go:build amd64 && !noasm

package vector

import (
	"math"
	"math/rand"
	"testing"

	"golang.org/x/sys/cpu"
)

// A binary built on a machine with AVX2 has to run on one without it: MDDB
// ships one amd64 build, not one per microarchitecture. The fallback is a
// runtime branch, so it can be exercised here by taking it deliberately —
// which is the only way to test it without different hardware.
func TestFallbackWithoutAVX2MatchesTheKernel(t *testing.T) {
	if !useAVX2 {
		t.Skip("this CPU has no AVX2, so the fallback is already what runs")
	}

	rnd := rand.New(rand.NewSource(31337)) // #nosec G404 -- fixture data

	for _, dims := range oracleDims {
		a := randomVec(rnd, dims, 1)
		b := randomVec(rnd, dims, 1)

		withAVX2 := [3]float64{
			float64(CosineSimilarity(a, b)),
			float64(dotProductSimilarity(a, b)),
			euclideanDistSq(a, b),
		}

		// No test in this package calls t.Parallel, so flipping a package
		// variable here is safe. It is restored immediately either way; a
		// failure below still leaves the kernel selected for whatever runs
		// next, because the assignment happens before any t.Errorf.
		useAVX2 = false
		withoutAVX2 := [3]float64{
			float64(CosineSimilarity(a, b)),
			float64(dotProductSimilarity(a, b)),
			euclideanDistSq(a, b),
		}
		useAVX2 = true

		names := [3]string{"CosineSimilarity", "dotProductSimilarity", "euclideanDistSq"}
		for i := range withAVX2 {
			// The two summation orders differ, so they are compared against the
			// float32 accumulation bound rather than for equality — the same
			// criterion the oracle test uses, and for the same reason.
			tol := tolerance(dims, termScale(a, b))
			switch names[i] {
			case "euclideanDistSq":
				d := make([]float32, dims)
				for j := range d {
					d[j] = a[j] - b[j]
				}
				tol = tolerance(dims, termScale(d, d))
			case "CosineSimilarity":
				tol /= math.Sqrt(termScale(a, a) * termScale(b, b))
			}
			if math.Abs(withAVX2[i]-withoutAVX2[i]) > tol {
				t.Errorf("dims=%d %s: avx2 %.9g, fallback %.9g (diff %.3g > %.3g)",
					dims, names[i], withAVX2[i], withoutAVX2[i],
					math.Abs(withAVX2[i]-withoutAVX2[i]), tol)
			}
		}
	}
}

// The kernels need FMA as well as AVX2. Every AVX2 CPU shipped has FMA3, but
// "every one shipped" is not a guarantee the code should rely on silently —
// the selection asserts both, and this records why the second half is there.
func TestAVX2SelectionRequiresFMA(t *testing.T) {
	if cpu.X86.HasAVX2 && !cpu.X86.HasFMA && useAVX2 {
		t.Fatal("AVX2 kernels selected on a CPU without FMA; they use VFMADD231PS")
	}
	t.Logf("cpu: avx2=%v fma=%v avx512f=%v → tier %s",
		cpu.X86.HasAVX2, cpu.X86.HasFMA, cpu.X86.HasAVX512F, vectorMathTier())
}
