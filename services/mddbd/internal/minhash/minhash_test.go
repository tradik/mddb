package minhash

import (
	"math"
	"strings"
	"testing"
)

// SRCH-002. The signal is only worth having if it separates "near-copy" from
// "same topic" — vector similarity already covers the second, so a MinHash
// that agrees with it adds nothing.

const lorem = "the deployment pipeline builds the image and pushes it to the registry " +
	"before the scheduler rolls the new version out across the cluster nodes"

func TestIdenticalTextScoresOne(t *testing.T) {
	a := Compute(lorem, DefaultShingleSize, DefaultSignatureSize)
	b := Compute(lorem, DefaultShingleSize, DefaultSignatureSize)

	if got := Similarity(a, b); got != 1.0 {
		t.Errorf("a document compared with itself scored %v, want 1.0", got)
	}
}

func TestUnrelatedTextScoresNearZero(t *testing.T) {
	a := Compute(lorem, DefaultShingleSize, DefaultSignatureSize)
	b := Compute("certificate rotation requires the operator to restart every proxy "+
		"after the new key material reaches the secret store", DefaultShingleSize, DefaultSignatureSize)

	if got := Similarity(a, b); got > 0.1 {
		t.Errorf("unrelated documents scored %v, want near 0", got)
	}
}

// The point of the signal: a document with one sentence changed is a
// near-copy, and must be recognisable as one.
func TestNearCopyScoresHigh(t *testing.T) {
	edited := strings.Replace(lorem, "cluster nodes", "worker fleet", 1)

	a := Compute(lorem, DefaultShingleSize, DefaultSignatureSize)
	b := Compute(edited, DefaultShingleSize, DefaultSignatureSize)

	got := Similarity(a, b)
	if got < 0.7 {
		t.Errorf("a document with two words changed scored %v, want > 0.7", got)
	}
	if got == 1.0 {
		t.Error("an edited document scored identical to the original")
	}
}

// Same subject, different words — this is where MinHash must disagree with an
// embedding, or it is not a separate signal.
func TestSameTopicDifferentWordsScoresLow(t *testing.T) {
	a := Compute("the scheduler rolls the new version out across the cluster nodes",
		DefaultShingleSize, DefaultSignatureSize)
	b := Compute("deployments are distributed to every worker by the orchestrator",
		DefaultShingleSize, DefaultSignatureSize)

	if got := Similarity(a, b); got > 0.2 {
		t.Errorf("two ways of saying the same thing scored %v — this is measuring topic, not overlap", got)
	}
}

func TestSimilarityIsSymmetric(t *testing.T) {
	a := Compute(lorem, DefaultShingleSize, DefaultSignatureSize)
	b := Compute(strings.Replace(lorem, "registry", "store", 1), DefaultShingleSize, DefaultSignatureSize)

	if math.Abs(Similarity(a, b)-Similarity(b, a)) > 1e-9 {
		t.Error("similarity depends on argument order")
	}
}

func TestEmptyAndTinyInputs(t *testing.T) {
	if got := Compute("", DefaultShingleSize, DefaultSignatureSize); got != nil {
		t.Errorf("empty text produced a signature: %v", got)
	}
	if got := Compute("   \n\t ", DefaultShingleSize, DefaultSignatureSize); got != nil {
		t.Error("whitespace produced a signature")
	}
	if got := Similarity(nil, nil); got != 0 {
		t.Errorf("Similarity(nil, nil) = %v, want 0", got)
	}

	// Shorter than one shingle: still gets a signature, and still matches its
	// own copy. Without this a two-word document could never be found as a
	// duplicate of itself.
	short := Compute("hello world", DefaultShingleSize, DefaultSignatureSize)
	if short == nil {
		t.Fatal("a two-word document produced no signature")
	}
	if got := Similarity(short, Compute("hello world", DefaultShingleSize, DefaultSignatureSize)); got != 1.0 {
		t.Errorf("a short document did not match its own copy: %v", got)
	}
}

func TestCaseAndWhitespaceDoNotMatter(t *testing.T) {
	a := Compute("The Deployment Pipeline Builds The Image", DefaultShingleSize, DefaultSignatureSize)
	b := Compute("the   deployment\npipeline builds\tthe image", DefaultShingleSize, DefaultSignatureSize)

	if got := Similarity(a, b); got != 1.0 {
		t.Errorf("the same text differing in case and spacing scored %v", got)
	}
}

func TestShinglesAreDeterministicAndDeduplicated(t *testing.T) {
	// A phrase repeated within one document says nothing about its overlap
	// with another, and two runs must agree or nothing built on this is
	// reproducible.
	text := "alpha beta gamma alpha beta gamma alpha beta gamma"

	first := Shingles(text, 3)
	second := Shingles(text, 3)

	if len(first) != len(second) {
		t.Fatalf("two runs produced %d and %d shingles", len(first), len(second))
	}
	for i := range first {
		if first[i] != second[i] {
			t.Fatalf("shingle %d differs between runs: %q vs %q", i, first[i], second[i])
		}
	}

	seen := map[string]bool{}
	for _, sh := range first {
		if seen[sh] {
			t.Errorf("shingle %q appears twice", sh)
		}
		seen[sh] = true
	}
}

func TestSignatureSizeIsHonoured(t *testing.T) {
	for _, size := range []int{16, 64, DefaultSignatureSize} {
		if got := len(Compute(lorem, DefaultShingleSize, size)); got != size {
			t.Errorf("signature size %d produced %d hashes", size, got)
		}
	}
	// Nonsense parameters fall back to the defaults rather than producing an
	// empty or enormous signature.
	if got := len(Compute(lorem, 0, 0)); got != DefaultSignatureSize {
		t.Errorf("zero parameters produced a signature of %d", got)
	}
	if got := len(Compute(lorem, -5, -5)); got != DefaultSignatureSize {
		t.Errorf("negative parameters produced a signature of %d", got)
	}
}

// Mismatched sizes happen when a caller changes configuration; half an
// estimate beats an error inside a ranking signal.
func TestMismatchedSignatureSizesCompareOverTheCommonPrefix(t *testing.T) {
	small := Compute(lorem, DefaultShingleSize, 16)
	large := Compute(lorem, DefaultShingleSize, 128)

	if got := Similarity(small, large); got != 1.0 {
		t.Errorf("the same text at two signature sizes scored %v, want 1.0", got)
	}
}

// The estimate has to be close to the truth, or a threshold tuned against it
// means nothing.
func TestEstimateTracksTheRealJaccard(t *testing.T) {
	a := "alpha beta gamma delta epsilon zeta eta theta iota kappa lambda mu"
	b := "alpha beta gamma delta epsilon zeta eta theta nu xi omicron pi"

	exact := jaccard(Shingles(a, DefaultShingleSize), Shingles(b, DefaultShingleSize))
	estimate := Similarity(
		Compute(a, DefaultShingleSize, DefaultSignatureSize),
		Compute(b, DefaultShingleSize, DefaultSignatureSize),
	)

	// 128 permutations give roughly ±9% standard error; 0.15 is a comfortable
	// bound that still fails if the estimator is wrong rather than noisy.
	if math.Abs(exact-estimate) > 0.15 {
		t.Errorf("estimate %.3f is far from the exact Jaccard %.3f", estimate, exact)
	}
}

func jaccard(a, b []string) float64 {
	set := make(map[string]bool, len(a))
	for _, s := range a {
		set[s] = true
	}
	inter := 0
	for _, s := range b {
		if set[s] {
			inter++
		}
	}
	union := len(a) + len(b) - inter
	if union == 0 {
		return 0
	}
	return float64(inter) / float64(union)
}
