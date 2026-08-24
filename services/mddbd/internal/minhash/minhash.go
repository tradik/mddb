// Package minhash estimates how much two documents overlap without comparing
// them directly (SRCH-002).
//
// Two uses, one implementation. Search uses it to notice that the third result
// is a near-copy of the first — an agent reading the top five results wants
// five different things, not the same paragraph five times. Duplicate
// detection uses it to find near-copies that an exact hash misses and a vector
// comparison is too expensive to look for across a whole collection.
//
// The estimate comes from shingles: overlapping runs of consecutive words. Two
// documents that differ by a sentence share almost every shingle; two that
// merely discuss the same topic share few, even though their embeddings are
// close. That distinction is what makes this a different signal from vector
// similarity rather than a cheaper version of it.
package minhash

import (
	"hash/fnv"
	"math"
	"sort"
	"strings"
)

// DefaultShingleSize is how many consecutive words form one shingle.
//
// Three is the usual choice for prose: single words are too common to
// distinguish anything, and five-word runs rarely repeat outside a literal
// copy. Documents shorter than this are shingled as a single unit rather than
// producing no signature at all.
const DefaultShingleSize = 3

// DefaultSignatureSize is how many hash functions the signature uses.
//
// The estimate's standard error is about 1/sqrt(n), so 128 permutations give
// roughly ±9% — precise enough to separate "near-copy" from "same topic",
// which is the only judgement made from it. More permutations cost linear time
// and buy accuracy nobody uses.
const DefaultSignatureSize = 128

// Signature is a document's MinHash signature. Two signatures of the same size
// can be compared in O(size) regardless of how long the documents were.
type Signature []uint64

// Compute builds a signature from text.
//
// Returns nil for text with no words: an empty signature compared against
// anything gives 0, which is the honest answer for "how much does nothing
// overlap with something".
func Compute(text string, shingleSize, signatureSize int) Signature {
	if shingleSize < 1 {
		shingleSize = DefaultShingleSize
	}
	if signatureSize < 1 {
		signatureSize = DefaultSignatureSize
	}

	shingles := Shingles(text, shingleSize)
	if len(shingles) == 0 {
		return nil
	}

	sig := make(Signature, signatureSize)
	for i := range sig {
		sig[i] = math.MaxUint64
	}

	for _, sh := range shingles {
		base := hashString(sh)
		for i := 0; i < signatureSize; i++ {
			// One base hash permuted i ways, rather than i independent hash
			// functions: the same distribution for a fraction of the work.
			h := permute(base, uint64(i))
			if h < sig[i] {
				sig[i] = h
			}
		}
	}
	return sig
}

// Shingles splits text into overlapping runs of consecutive words.
//
// Deduplicated, because a phrase repeated within one document says nothing
// about its overlap with another, and sorted so the result is deterministic —
// two runs over the same text must produce the same signature or nothing built
// on this is reproducible.
func Shingles(text string, size int) []string {
	words := strings.Fields(strings.ToLower(text))
	if len(words) == 0 {
		return nil
	}
	if len(words) < size {
		// Too short to shingle: the whole thing is one shingle, so a short
		// document still has a signature and can still be found identical to
		// its own copy.
		return []string{strings.Join(words, " ")}
	}

	seen := make(map[string]struct{}, len(words))
	out := make([]string, 0, len(words)-size+1)
	for i := 0; i+size <= len(words); i++ {
		sh := strings.Join(words[i:i+size], " ")
		if _, dup := seen[sh]; dup {
			continue
		}
		seen[sh] = struct{}{}
		out = append(out, sh)
	}
	sort.Strings(out)
	return out
}

// Similarity estimates the Jaccard similarity of the two documents the
// signatures came from: 1.0 is identical, 0.0 shares nothing.
//
// Signatures of different sizes are compared over their common prefix rather
// than refused. They only differ when a caller mixes configurations, and half
// an estimate beats an error in a ranking signal.
func Similarity(a, b Signature) float64 {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	if n == 0 {
		return 0
	}

	matches := 0
	for i := 0; i < n; i++ {
		if a[i] == b[i] {
			matches++
		}
	}
	return float64(matches) / float64(n)
}

func hashString(s string) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(s))
	return h.Sum64()
}

// permute derives the i-th hash from a base hash.
//
// A multiply-xorshift mix rather than (a*x + b) mod p: the linear form leaves
// visible structure between permutations, which shows up as correlated minima
// and biases the estimate.
func permute(base, i uint64) uint64 {
	x := base ^ (i * 0x9e3779b97f4a7c15)
	x ^= x >> 33
	x *= 0xff51afd7ed558ccd
	x ^= x >> 33
	x *= 0xc4ceb9fe1a85ec53
	x ^= x >> 33
	return x
}
