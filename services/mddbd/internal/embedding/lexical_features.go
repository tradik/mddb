package embedding

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// Feature extraction for the lexical provider (SRCH-014): which features a
// text has, how often, and where they hash to. Kept apart from the provider so
// the hashing can be read — and tested — on its own.

// n-gram lengths, in runes, including the boundary markers.
const (
	lexicalMinGram = 3
	lexicalMaxGram = 5
)

// Feature tags keep a word and an identical n-gram ("<a>" for the word "a")
// from being one feature.
const (
	tagWord  byte = 'w'
	tagNGram byte = 'c'
)

// Boundary markers around a word, so "<de" (a word starting with "de") is a
// different n-gram from "de" inside a word.
const (
	lexicalMarkL = '<'
	lexicalMarkR = '>'
)

// FNV-1a 64-bit constants. The sign hash starts from a different basis, which
// makes it a second hash of the same bytes rather than a bit of the first.
const (
	fnvOffset64  uint64 = 14695981039346656037
	fnvPrime64   uint64 = 1099511628211
	signOffset64 uint64 = fnvOffset64 ^ 0x9e3779b97f4a7c15
)

// lexicalFeature is one distinct feature of a text: its hash (which places
// it), its sign and how often it occurred.
type lexicalFeature struct {
	key   uint64
	sign  float32
	count int
}

// lexicalFeatures returns every distinct feature of text with its count.
func lexicalFeatures(text string) map[uint64]*lexicalFeature {
	features := make(map[uint64]*lexicalFeature)
	for _, word := range lexicalWords(text) {
		addFeature(features, tagWord, word)
		addNGrams(features, word)
	}
	return features
}

// addNGrams adds the character n-grams of word wrapped in boundary markers.
func addNGrams(features map[uint64]*lexicalFeature, word []rune) {
	marked := make([]rune, 0, len(word)+2)
	marked = append(marked, lexicalMarkL)
	marked = append(marked, word...)
	marked = append(marked, lexicalMarkR)
	for n := lexicalMinGram; n <= lexicalMaxGram; n++ {
		for i := 0; i+n <= len(marked); i++ {
			addFeature(features, tagNGram, marked[i:i+n])
		}
	}
}

// addFeature counts one occurrence of the feature tag+runes.
func addFeature(features map[uint64]*lexicalFeature, tag byte, runes []rune) {
	key, sign := hashFeature(tag, runes)
	if f, ok := features[key]; ok {
		f.count++
		return
	}
	s := float32(1)
	if sign&1 == 1 {
		s = -1
	}
	features[key] = &lexicalFeature{key: key, sign: s, count: 1}
}

// hashFeature returns two FNV-1a hashes of tag+runes: the first identifies
// and places the feature, the second gives its sign.
func hashFeature(tag byte, runes []rune) (key, sign uint64) {
	key, sign = fnvOffset64, signOffset64
	mix := func(b byte) {
		key = (key ^ uint64(b)) * fnvPrime64
		sign = (sign ^ uint64(b)) * fnvPrime64
	}
	mix(tag)
	var buf [utf8.UTFMax]byte
	for _, r := range runes {
		n := utf8.EncodeRune(buf[:], r)
		for _, b := range buf[:n] {
			mix(b)
		}
	}
	// FNV's low bit depends only on the low bits of the input; fold the high
	// half in before the sign is read from it.
	sign ^= sign >> 32
	return key, sign
}

// lexicalWords splits text into lowercased runs of letters and digits.
func lexicalWords(text string) [][]rune {
	fields := strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	words := make([][]rune, len(fields))
	for i, f := range fields {
		words[i] = []rune(f)
	}
	return words
}
