package main

import (
	"fmt"

	vec "mddb/internal/vector"
)

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
	// cost grow more slowly. At 200k vectors HNSW answered in 0.46 ms at
	// recall 1.000 and a trained IVF in 1.59 ms, also at 1.000; at 20k, 0.35
	// against 0.55 ms (SRCH-013). Above a million it has not been measured
	// since the graph was replaced.
	GraphMaxVectors int
	// The filter share under which an exact scan of what the filter admits
	// beats an index is vec.HNSWFilterStrategy's (SRCH-024): the planner and
	// HNSW decide by the same rule. See prefilterBeats.
	//
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
	// Training lists the trainable indexes still training the collection
	// (GO-047); none of them is in Built.
	Training []string
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
	// Strategy is how a filtered search ran (SRCH-024): "prefilter" (the
	// admitted documents scored exactly), "graph+filter" (the HNSW graph
	// walked with the filter), "ivf" (the clusters probed, refused vectors
	// skipped) or "scan+filter" (a quantized index scanned with the filter).
	// Empty without a filter.
	Strategy string `json:"strategy,omitempty"`
	// Candidates is how many hits the index was asked for: topK × oversample.
	Candidates int `json:"candidates"`
	// IterativeScan: a filtered index search came back short of topK and the
	// scan continued further into the index (SRCH-013).
	IterativeScan bool     `json:"iterativeScan,omitempty"`
	Reasons       []string `json:"reasons"`
	// Collections holds one plan per target of a cross-collection search.
	Collections map[string]*SearchPlan `json:"collections,omitempty"`
	// Feedback is the correction relevance feedback made (SRCH-015).
	Feedback *FeedbackApplied `json:"feedback,omitempty"`
	// Decomposition is how the query was split (SRCH-020); set with
	// queryDecomposition auto.
	Decomposition *QueryDecomposition `json:"decomposition,omitempty"`

	// mayIterate: the planner allows the iterative scan for this search.
	mayIterate bool
	// summaryChunks: summaryMode expand's chunks per document a summary
	// matched, which its passages are cut from (RAG-009).
	summaryChunks map[string][]int
}

// reason appends one line of explanation.
func (p *SearchPlan) reason(format string, args ...any) {
	p.Reasons = append(p.Reasons, fmt.Sprintf(format, args...))
}

// planVector chooses the index for one query.
func planVector(in PlanInput) *SearchPlan {
	p := &SearchPlan{Candidates: in.SearchTopK}
	if in.Quantized {
		p.Algorithm = "quantized"
		if in.Filtered {
			p.Prefilter = prefilterBeats(in)
			p.Strategy = filterStrategy(p.Algorithm, p.Prefilter)
		}
		p.reason("quantized: the collection is configured for quantized vectors, and that index is the one holding them")
		return p
	}
	index := planIndex(in)
	if !in.Filtered {
		return index
	}
	switch filteredPath(in, index.Algorithm) {
	case vec.FilterStrategyPrefilter:
		p.Algorithm, p.Prefilter, p.Strategy = "flat", true, vec.FilterStrategyPrefilter
		p.reason("flat with a prefilter: the filter admits %d documents of %d vectors (%.1f%%), and scoring exactly what it admits costs less than searching an index past what it refuses",
			in.Allowed, in.Vectors, in.selectivity()*100)
		return p
	case "ivf":
		if index.Algorithm != "ivf" {
			index = &SearchPlan{Algorithm: "ivf", Candidates: in.SearchTopK}
			index.reason("ivf: for a filter admitting %.1f%% of %d vectors, probing a trained ivf's clusters costs less than walking the graph past the nodes the filter refuses",
				in.selectivity()*100, in.Vectors)
		}
	}
	index.Strategy = filterStrategy(index.Algorithm, false)
	switch index.Algorithm {
	case "flat":
		index.Prefilter = true
		index.reason("prefilter: flat scores exactly the documents the filter admits")
	case "hnsw":
		index.reason("graph+filter: the filter admits %.1f%% of the collection; the graph is walked with the filter, through refused nodes but returning only admitted ones",
			in.selectivity()*100)
	default:
		index.mayIterate = true
		index.reason("the filter admits %.1f%% of the collection; if the index returns fewer than %d after filtering, the scan continues",
			in.selectivity()*100, in.SearchTopK)
	}
	return index
}

// prefilterBeats reports whether scanning exactly the admitted documents is
// cheaper than searching an index with the filter. It is HNSW's own rule
// (vec.HNSWFilterStrategy), so auto and an explicit hnsw agree.
func prefilterBeats(in PlanInput) bool {
	return vec.HNSWFilterStrategy(in.Allowed, in.Vectors) == vec.FilterStrategyPrefilter
}

// filteredPath is the cheapest way to answer a filter when planIndex chose
// index: "prefilter", "hnsw" or "ivf" by the cost estimates of the vector
// package (vec.FilterCosts, measured in SRCH-024), or index itself for one
// without an estimate. flat is already exact.
func filteredPath(in PlanInput, index string) string {
	switch index {
	case "flat":
		return index
	case "hnsw", "ivf":
	default:
		if prefilterBeats(in) {
			return vec.FilterStrategyPrefilter
		}
		return index
	}
	costs := vec.FilterCosts{Vectors: in.Vectors, Admitted: in.Allowed, TopK: in.SearchTopK}
	best, cost := vec.FilterStrategyPrefilter, costs.Prefilter()
	if index == "hnsw" && costs.Graph() < cost {
		best, cost = index, costs.Graph()
	}
	if in.Built["ivf"] && costs.IVF() < cost {
		best = "ivf"
	}
	return best
}

// filterStrategy names how an index answers a filter, for explain.
func filterStrategy(algorithm string, prefilter bool) string {
	switch {
	case prefilter || algorithm == "flat":
		return vec.FilterStrategyPrefilter
	case algorithm == "hnsw":
		return vec.FilterStrategyGraph
	case algorithm == "ivf":
		return "ivf"
	}
	return "scan+filter"
}

// planIndex chooses the index for an unfiltered search, or the one a filtered
// search would use if the filter does not favour the prefilter.
func planIndex(in PlanInput) *SearchPlan {
	p := &SearchPlan{Candidates: in.SearchTopK}
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
	// Then the unquantized indexes, fastest measured first: hnsw, then a
	// trained ivf, then the exact scan (docs/BENCHMARK.md, SRCH-013).
	chain = append(chain, "hnsw", "ivf", "flat")
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
