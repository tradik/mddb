package main

import "fmt"

// Choosing the vector index per query (SRCH-013).
//
// MDDB has eight vector algorithms and searched with flat unless told
// otherwise. The search advisor (SRCH-010) measures a collection and says which
// index suits it, but offline, and without seeing the query: its filter, its
// topK. `algorithm: "auto"` asks the server to decide per query, from what it
// knows at that moment — how many vectors, which indexes are built, how much of
// the collection the filter admits — and to say why when asked (`explain`).
//
// The planner is a pure function of PlanInput so its rules can be read in one
// place and tested without a server. It never builds an index: one that is not
// built yet is not a candidate (vector_built.go).

// AlgorithmAuto asks the planner to choose the vector index.
const AlgorithmAuto = "auto"

// searchThresholds is the one table the planner and the search advisor
// (SRCH-010) decide by. The advisor had its own numbers; two tables drift, and
// an advisor recommending one index while auto picks another explains nothing.
var searchThresholds = struct {
	// FlatMaxVectors: below it an exact scan is both fastest and exact.
	FlatMaxVectors int
	// GraphMaxVectors: up to it HNSW; above it IVF, whose memory and build
	// cost grow more slowly.
	GraphMaxVectors int
	// SelectiveFilter: a filter admitting less than this share of the
	// collection is answered by an exact scan of what it admits — an index
	// would return mostly documents the filter then throws away.
	SelectiveFilter float64
	// QuantizeBytes and Quantize4Bytes: float32 vectors above these sizes are
	// worth searching in int8 (sq) and 4-bit (sq4) form.
	QuantizeBytes  int64
	Quantize4Bytes int64
	// TightLatencyMs: a latencyTargetMs at or under this prefers a quantized
	// index on a collection too large for an exact scan.
	TightLatencyMs int
	// MaxWiden caps the iterative scan: how far past its default a filtered
	// index search may look before the plan accepts fewer than topK.
	MaxWiden int
}{
	FlatMaxVectors:  10_000,
	GraphMaxVectors: 1_000_000,
	SelectiveFilter: 0.05,
	QuantizeBytes:   256 << 20,
	Quantize4Bytes:  1 << 30,
	TightLatencyMs:  10,
	MaxWiden:        8,
}

// vectorTier is the index the threshold table names for a collection of n
// vectors occupying bytes at float32, with the reason in words. The advisor
// and the planner both start from it.
func vectorTier(n int, bytes int64) (algorithm, reason string) {
	t := searchThresholds
	switch {
	case bytes > t.Quantize4Bytes:
		return "sq4", fmt.Sprintf("%.1f GB of float32 vectors will not sit in RAM comfortably, and sq4 holds 99.5%% of int8's recall at half its size",
			float64(bytes)/(1<<30))
	case bytes > t.QuantizeBytes:
		return "sq", fmt.Sprintf("%.0f MB of vectors is enough to be worth quantizing, and int8 is effectively lossless",
			float64(bytes)/(1<<20))
	}
	return countTier(n)
}

// countTier is vectorTier by size alone.
func countTier(n int) (algorithm, reason string) {
	t := searchThresholds
	switch {
	case n > t.GraphMaxVectors:
		return "ivf", fmt.Sprintf("at %d vectors a graph costs more memory and build time than clustering, and ivf probes a fraction of the collection", n)
	case n >= t.FlatMaxVectors:
		return "hnsw", fmt.Sprintf("at %d vectors an exact scan starts costing more than the recall it buys", n)
	}
	return "flat", fmt.Sprintf("%d vectors is small enough that exact search is both fastest and most accurate", n)
}

// PlanInput is what the planner decides from.
type PlanInput struct {
	Vectors    int             // vectors the collection holds
	Dimensions int             // per vector; with Vectors, the float32 footprint
	Built      map[string]bool // indexes that can answer now without building
	Quantized  bool            // the collection is configured for quantization or disk-only vectors
	Filtered   bool            // the request has a metadata filter
	Allowed    int             // documents the filter admits
	SearchTopK int             // topK × oversample (SRCH-005)
	// LatencyTargetMs is the request's optional latency goal; 0 = none.
	LatencyTargetMs int
}

// bytes is the collection's footprint at float32.
func (in PlanInput) bytes() int64 { return int64(in.Vectors) * int64(in.Dimensions) * 4 }

// selectivity is the share of the collection the filter admits. Documents
// over vectors: a chunked collection holds more vectors than documents, so it
// reads as more selective than it is, which errs towards the exact path —
// the side that cannot lose a result.
func (in PlanInput) selectivity() float64 {
	if in.Vectors == 0 {
		return 0
	}
	return float64(in.Allowed) / float64(in.Vectors)
}

// SearchPlan is how a vector search ran, and why. Returned when the request
// sets explain.
type SearchPlan struct {
	Algorithm string `json:"algorithm"`
	// Prefilter: the filter was applied first and the admitted documents
	// scanned exactly, rather than searching an index and filtering after.
	Prefilter bool `json:"prefilter"`
	// Candidates is how many hits the index was asked for: topK × oversample.
	Candidates int `json:"candidates"`
	// IterativeScan: a filtered index search came back short of topK and the
	// scan continued further into the index (SRCH-013).
	IterativeScan bool     `json:"iterativeScan,omitempty"`
	Reasons       []string `json:"reasons"`
	// Collections holds one plan per target of a cross-collection search.
	Collections map[string]*SearchPlan `json:"collections,omitempty"`

	// mayIterate: the planner allows the iterative scan for this search.
	mayIterate bool
}

// reason appends one line of explanation.
func (p *SearchPlan) reason(format string, args ...any) {
	p.Reasons = append(p.Reasons, fmt.Sprintf(format, args...))
}

// planVector chooses the index for one query.
func planVector(in PlanInput) *SearchPlan {
	p := &SearchPlan{Candidates: in.SearchTopK}
	selective := in.Filtered && in.selectivity() < searchThresholds.SelectiveFilter
	if in.Quantized {
		p.Algorithm, p.Prefilter = "quantized", selective
		p.reason("quantized: the collection is configured for quantized vectors, and that index is the one holding them")
		return p
	}
	if selective {
		p.Algorithm, p.Prefilter = "flat", true
		p.reason("flat with a prefilter: the filter admits %d documents of %d vectors (%.1f%%, under %.0f%%), so scanning exactly what it admits beats an index search whose hits it would mostly discard",
			in.Allowed, in.Vectors, in.selectivity()*100, searchThresholds.SelectiveFilter*100)
		return p
	}

	want, why := vectorTier(in.Vectors, in.bytes())
	chain := []string{want}
	if in.LatencyTargetMs > 0 && in.LatencyTargetMs <= searchThresholds.TightLatencyMs && in.Vectors >= searchThresholds.FlatMaxVectors {
		want, why = "bq", fmt.Sprintf("a latency target of %d ms on %d vectors favours a quantized index", in.LatencyTargetMs, in.Vectors)
		chain = []string{"bq", "sq4", "sq"}
	}
	switch want {
	case "sq4", "sq", "bq":
		size, _ := countTier(in.Vectors)
		chain = append(chain, "sq4", "sq", "bq", size)
	}
	chain = append(chain, "hnsw", "flat")
	if want == "flat" {
		chain = []string{"flat"}
	}

	for _, algo := range chain {
		if algo == "flat" || in.Built[algo] {
			p.Algorithm = algo
			break
		}
		if algo == want {
			p.reason("%s would suit (%s), but it is not built for this collection; the planner never builds an index on the query path", want, why)
		}
	}
	if p.Algorithm == want {
		p.reason("%s: %s", want, why)
	} else {
		p.reason("%s: the best of the indexes already built", p.Algorithm)
	}
	if in.Filtered && p.Algorithm != "flat" {
		p.mayIterate = true
		p.reason("the filter admits %.1f%% of the collection; if the index returns fewer than %d after filtering, the scan continues",
			in.selectivity()*100, in.SearchTopK)
	}
	return p
}

// explicitPlan describes a search whose algorithm the caller named, or left
// at the default.
func explicitPlan(ran, requested string, candidates int) *SearchPlan {
	p := &SearchPlan{Algorithm: ran, Candidates: candidates}
	switch {
	case requested == "":
		p.reason("%s: the default; send algorithm \"auto\" to let the planner choose", ran)
	case requested != ran:
		p.reason("%s: %s was requested but cannot answer for this collection yet", ran, requested)
	default:
		p.reason("%s: requested", ran)
	}
	return p
}
