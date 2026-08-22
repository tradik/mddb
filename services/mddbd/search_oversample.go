package main

import "fmt"

// Oversampling as a request parameter (SRCH-005).
//
// Every search that has to post-process its results — deduplicating chunks of
// one document, merging two rankings, rescoring quantized candidates — asks the
// index for more than it will return, then trims. That multiplier existed in
// five places as the literal `topK * 3` with a floor, so the recall/latency
// trade-off every comparable engine exposes was fixed at whatever those
// constants said.
//
// One knob, one meaning, everywhere: ask for `oversample × topK` candidates.
// Higher finds more of what a chunk-level or quantized index would otherwise
// miss, and costs proportionally more work.

// Oversample bounds.
//
// The default is the constant the code has always used, so an unset parameter
// reproduces today's results exactly. The ceiling is a DoS guard (SEC-005:
// bound everything a caller can grow): oversample multiplies the candidate set,
// so an unbounded value turns one request into a full scan.
const (
	MinOversample     = 1.0
	MaxOversample     = 10.0
	DefaultOversample = 3.0
)

// ValidateOversample rejects a factor outside the supported range.
func ValidateOversample(factor float64) error {
	if factor == 0 {
		return nil // unset
	}
	if factor < MinOversample || factor > MaxOversample {
		return fmt.Errorf("oversample %.2f is outside the supported range %.0f–%.0f",
			factor, MinOversample, MaxOversample)
	}
	return nil
}

// ResolveOversample applies the RAG-001 precedence rule: explicit request
// parameter, then the collection's retrieval profile, then the constant the
// code has always used.
func (s *Server) ResolveOversample(collection string, requested float64) float64 {
	if requested > 0 {
		return requested
	}
	if p := s.RetrievalProfile(collection); p != nil && p.Oversample > 0 {
		return p.Oversample
	}
	return DefaultOversample
}

// OversampledTopK is how many candidates to ask the index for.
//
// floor is the minimum candidate set each call site has always applied — a
// small topK still needs enough candidates for deduplication or merging to have
// anything to work with, and the floors differ by call site because what they
// post-process differs.
func OversampledTopK(topK int, factor float64, floor int) int {
	if factor <= 0 {
		factor = DefaultOversample
	}
	n := int(float64(topK) * factor)
	if n < floor {
		n = floor
	}
	// Bounded even when topK itself is large: the ceiling on the factor is
	// meaningless if the product can still grow without limit.
	//
	// The floor wins over the ceiling. For a small topK the two cross — topK
	// 1 with a floor of 50 would otherwise be capped to 10 — and the floor is
	// a hard minimum the call site needs for deduplication or merging to have
	// anything to work with, while the ceiling only bounds growth.
	ceiling := topK * int(MaxOversample)
	if ceiling < floor {
		ceiling = floor
	}
	if ceiling > 0 && n > ceiling {
		n = ceiling
	}
	return n
}
