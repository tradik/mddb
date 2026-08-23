package main

import (
	"math"
	"sort"
	"strings"
	"time"

	"mddb/internal/minhash"
	"mddb/internal/storage"
)

// Multi-signal fusion for hybrid search (SRCH-002).
//
// `alpha` blends a keyword score with a vector score; `rrf` blends their
// ranks. Both answer "how well does this document match the query" and nothing
// else. There are cheap signals they ignore that change which five results an
// agent actually reads:
//
//   - Two of the top five being near-copies of each other wastes one of five
//     slots. A keyword score cannot see it, and neither can a vector score —
//     near-copies have near-identical embeddings, so similarity *rewards* the
//     duplication.
//   - A document sitting next to an already-strong result in the code graph is
//     more likely relevant than one that shares no edge with anything.
//   - A runbook edited three years ago and one edited last week are not
//     equally likely to be the answer.
//
// The `weighted` strategy makes those explicit and weighted, so a caller can
// dial them or turn them off. `alpha` and `rrf` are untouched: a request that
// does not ask for `weighted` takes exactly the path it took before, which is
// what the golden tests in hybrid_search_test.go pin.
//
// Signals are applied after the base fusion, to its combined score, so the
// base ranking remains the thing being adjusted rather than replaced.

// FusionSignals configures the weighted strategy.
//
// Every weight defaults to zero — no signal applies unless it is asked for.
// A strategy that silently reranked results would make two servers on the same
// data disagree for reasons nobody could see in the request.
//
// **A weight is a fraction of the result's own score.** `proximity: 0.1` means
// "up to 10% better"; `diversity: 0.8` means "up to 80% worse". That makes the
// signals scale-free — they behave the same whether the base scores run 0..1
// or 0..100 — and it makes large weights genuinely powerful: at 0.5, a
// proximity bonus can carry a result past one scoring half again as much.
// Weights above MaxSignalWeight are clamped, because beyond that the signal is
// no longer adjusting the ranking, it is the ranking.
//
// Useful starting points: proximity 0.05–0.15, freshness 0.05–0.2, diversity
// 0.5–1.0. Diversity is deliberately the blunt one — a near-copy in the top
// five is not a small problem.
type FusionSignals struct {
	// Diversity penalises a result that is a near-copy of a higher-ranked one,
	// measured by MinHash overlap rather than embedding distance: near-copies
	// have near-identical embeddings, so a vector score cannot separate
	// "another copy" from "also relevant".
	//
	// The penalty is proportional to the overlap, so a partial rewrite is
	// demoted slightly and a verbatim copy heavily.
	Diversity float64 `json:"diversity,omitempty"`

	// DiversityThreshold is the overlap below which two documents are treated
	// as unrelated and no penalty applies. Default 0.5 — measured overlap
	// between a document and a rewritten version of it stays well above this,
	// while two documents on the same subject stay well below.
	DiversityThreshold float64 `json:"diversityThreshold,omitempty"`

	// Proximity rewards a result whose key shares a path prefix with a
	// higher-ranked one. Documents in the same directory are usually about the
	// same thing, and a query that matched one has probably found the right
	// neighbourhood.
	Proximity float64 `json:"proximity,omitempty"`

	// Freshness rewards recently updated documents on a gentle exponential
	// decay. Off by default because it is wrong for reference material: an API
	// specification does not become less true with age.
	Freshness float64 `json:"freshness,omitempty"`

	// FreshnessHalfLife is how long it takes for the freshness bonus to halve.
	// Default 180 days.
	FreshnessHalfLifeDays float64 `json:"freshnessHalfLifeDays,omitempty"`
}

// MaxSignalWeight caps how far one signal may move a result.
//
// A weight of 2 would let a proximity bonus triple a score, which stops being
// a signal and becomes the ranking. Clamped rather than rejected: a request
// that overshoots should still return sensible results.
const MaxSignalWeight = 1.0

// Any reports whether any signal is configured. A `weighted` request with no
// weights is a caller mistake worth catching rather than an expensive no-op.
func (f FusionSignals) Any() bool {
	return f.Diversity != 0 || f.Proximity != 0 || f.Freshness != 0
}

// Defaults fills the thresholds a caller did not set and clamps the weights.
func (f FusionSignals) Defaults() FusionSignals {
	if f.DiversityThreshold <= 0 {
		f.DiversityThreshold = 0.5
	}
	if f.FreshnessHalfLifeDays <= 0 {
		f.FreshnessHalfLifeDays = 180
	}
	f.Diversity = clampWeight(f.Diversity)
	f.Proximity = clampWeight(f.Proximity)
	f.Freshness = clampWeight(f.Freshness)
	return f
}

// clampWeight keeps a weight inside [0, MaxSignalWeight].
//
// Negative weights are clamped to zero rather than inverted: a caller asking
// for "less diversity" wants the signal off, and silently rewarding
// near-duplicates is not something anyone means to ask for.
func clampWeight(w float64) float64 {
	if w < 0 {
		return 0
	}
	if w > MaxSignalWeight {
		return MaxSignalWeight
	}
	return w
}

// SignalBreakdown records what each signal contributed to one result.
//
// Returned with the results, because a reranking nobody can explain is a
// reranking nobody will trust — and because tuning weights blind is guesswork.
type SignalBreakdown struct {
	Base      float64 `json:"base"`
	Diversity float64 `json:"diversity,omitempty"`
	Proximity float64 `json:"proximity,omitempty"`
	Freshness float64 `json:"freshness,omitempty"`
	Final     float64 `json:"final"`
}

// applyWeightedSignals reranks merged results by the configured signals.
//
// Order matters: results arrive sorted by the base fusion, and diversity and
// proximity are both defined relative to results ranked above the one being
// scored. Scoring in the incoming order is what makes "higher-ranked" mean
// anything.
func applyWeightedSignals(items []HybridSearchResultItem, signals FusionSignals, now time.Time) ([]HybridSearchResultItem, []SignalBreakdown) {
	signals = signals.Defaults()
	if len(items) == 0 || !signals.Any() {
		return items, nil
	}

	breakdowns := make([]SignalBreakdown, len(items))

	// Signatures are computed once per result, not once per comparison: the
	// loop below is quadratic in the merge window, and recomputing a signature
	// inside it would make the whole strategy too slow to use.
	var signatures []minhash.Signature
	if signals.Diversity != 0 {
		signatures = make([]minhash.Signature, len(items))
		for i := range items {
			signatures[i] = minhash.Compute(items[i].Document.ContentMD,
				minhash.DefaultShingleSize, minhash.DefaultSignatureSize)
		}
	}

	for i := range items {
		base := items[i].CombinedScore
		b := SignalBreakdown{Base: base}
		score := base

		if signals.Diversity != 0 && signatures[i] != nil {
			// Compared against every result already accepted above this one,
			// taking the worst overlap: being a near-copy of any one of them
			// is enough.
			var worst float64
			for j := 0; j < i; j++ {
				if signatures[j] == nil {
					continue
				}
				if sim := minhash.Similarity(signatures[i], signatures[j]); sim > worst {
					worst = sim
				}
			}
			if worst >= signals.DiversityThreshold {
				b.Diversity = -signals.Diversity * worst * base
				score += b.Diversity
			}
		}

		if signals.Proximity != 0 {
			var best float64
			for j := 0; j < i; j++ {
				if p := pathAffinity(items[j].Document.Key, items[i].Document.Key); p > best {
					best = p
				}
			}
			b.Proximity = signals.Proximity * best * base
			score += b.Proximity
		}

		if signals.Freshness != 0 {
			b.Freshness = signals.Freshness * freshnessFactor(documentUpdatedAt(items[i].Document), now,
				signals.FreshnessHalfLifeDays) * base
			score += b.Freshness
		}

		b.Final = score
		breakdowns[i] = b
		items[i].CombinedScore = score
	}

	// Re-sort and renumber. A rank that no longer matches the order is worse
	// than no rank at all.
	order := make([]int, len(items))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool {
		return items[order[a]].CombinedScore > items[order[b]].CombinedScore
	})

	sorted := make([]HybridSearchResultItem, len(items))
	sortedBreakdowns := make([]SignalBreakdown, len(items))
	for newIdx, oldIdx := range order {
		sorted[newIdx] = items[oldIdx]
		sorted[newIdx].Rank = newIdx + 1
		sortedBreakdowns[newIdx] = breakdowns[oldIdx]
	}
	return sorted, sortedBreakdowns
}

// pathAffinity scores how close two document keys are in a path hierarchy.
//
// Segment-wise rather than character-wise: `docs/api/auth.md` and
// `docs/api/keys.md` share a directory, while `docs/api.md` and
// `docs/apiary.md` share five characters and nothing else.
func pathAffinity(a, b string) float64 {
	if a == "" || b == "" || a == b {
		return 0
	}

	as := strings.Split(a, "/")
	bs := strings.Split(b, "/")
	// The filename is not a directory: two files in the same folder share
	// every segment but the last.
	if len(as) > 0 {
		as = as[:len(as)-1]
	}
	if len(bs) > 0 {
		bs = bs[:len(bs)-1]
	}
	if len(as) == 0 || len(bs) == 0 {
		return 0
	}

	shared := 0
	for i := 0; i < len(as) && i < len(bs); i++ {
		if as[i] != bs[i] {
			break
		}
		shared++
	}
	if shared == 0 {
		return 0
	}

	deeper := len(as)
	if len(bs) > deeper {
		deeper = len(bs)
	}
	return float64(shared) / float64(deeper)
}

// freshnessFactor decays from 1 at "just updated" towards 0, halving every
// halfLifeDays.
//
// Returns 0 for a document with no timestamp rather than treating the zero
// time as infinitely old — an undated document should be neither rewarded nor
// punished for it.
func freshnessFactor(updated, now time.Time, halfLifeDays float64) float64 {
	if updated.IsZero() || halfLifeDays <= 0 {
		return 0
	}
	age := now.Sub(updated).Hours() / 24
	if age <= 0 {
		return 1
	}
	return math.Pow(0.5, age/halfLifeDays)
}

// documentUpdatedAt reads a stored document's update time.
//
// storage.Doc keeps timestamps as Unix seconds; zero means "never recorded"
// and must stay the zero time so freshnessFactor can tell it apart from a
// document dated 1970.
func documentUpdatedAt(d storage.Doc) time.Time {
	if d.UpdatedAt == 0 {
		return time.Time{}
	}
	return time.Unix(d.UpdatedAt, 0)
}
