package main

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"mddb/internal/storage"
)

// SRCH-002. The ticket's rule is that a signal ships only if it improves the
// ranking, so the measurement comes first and the unit tests come after.

func item(key, content string, score float64, updated time.Time) HybridSearchResultItem {
	var ts int64
	if !updated.IsZero() {
		ts = updated.Unix()
	}
	return HybridSearchResultItem{
		Document:      storage.Doc{Key: key, ContentMD: content, UpdatedAt: ts},
		CombinedScore: score,
	}
}

func keys(items []HybridSearchResultItem) []string {
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = it.Document.Key
	}
	return out
}

// distinctInTopK counts how many genuinely different documents a caller gets.
//
// This is the number that matters to an agent: five results that are three
// documents is a wasted context window, however good each one's score is.
func distinctInTopK(items []HybridSearchResultItem, k int, families map[string]string) int {
	seen := map[string]bool{}
	for i := 0; i < k && i < len(items); i++ {
		seen[families[items[i].Document.Key]] = true
	}
	return len(seen)
}

// TestDiversitySignalImprovesTopKCoverage is the gate SRCH-002 set: the signal
// only ships if it measurably improves the ranking.
//
// The corpus is the shape this is meant for — a document that exists in four
// near-identical copies, which is what happens to a runbook that was forked
// per environment, and three other documents that answer the same query.
func TestDiversitySignalImprovesTopKCoverage(t *testing.T) {
	base := "restart the service by running systemctl restart svc and then check journalctl for errors"

	var items []HybridSearchResultItem
	families := map[string]string{}

	// Four near-copies, scored highest — which is exactly what a keyword or
	// vector score does with duplicated content.
	for i := 0; i < 4; i++ {
		key := fmt.Sprintf("runbooks/restart-env-%d.md", i)
		content := base + fmt.Sprintf(" environment %d", i)
		items = append(items, item(key, content, 0.95-float64(i)*0.01, time.Now()))
		families[key] = "restart"
	}
	// Three genuinely different answers, scored slightly lower.
	for i, other := range []string{
		"rotate the certificate with certbot renew and reload the proxy afterwards",
		"scale the deployment by editing the replica count and applying the manifest",
		"drain the node before maintenance so no request is lost mid-flight",
	} {
		key := fmt.Sprintf("runbooks/other-%d.md", i)
		items = append(items, item(key, other, 0.90-float64(i)*0.01, time.Now()))
		families[key] = fmt.Sprintf("other-%d", i)
	}

	const topK = 4

	before := distinctInTopK(items, topK, families)

	reranked, breakdown := applyWeightedSignals(append([]HybridSearchResultItem(nil), items...),
		FusionSignals{Diversity: 0.8}, time.Now())
	after := distinctInTopK(reranked, topK, families)

	t.Log("")
	t.Logf("  top-%d distinct documents without the signal: %d", topK, before)
	t.Logf("  top-%d distinct documents with diversity=0.8: %d", topK, after)
	t.Logf("  order before: %v", keys(items)[:topK])
	t.Logf("  order after:  %v", keys(reranked)[:topK])
	t.Log("")

	if after <= before {
		t.Errorf("diversity did not improve top-%d coverage (%d → %d); by SRCH-002's rule it should not ship",
			topK, before, after)
	}
	if len(breakdown) != len(items) {
		t.Errorf("breakdown has %d entries for %d results", len(breakdown), len(items))
	}
}

func TestDiversityLeavesUnrelatedResultsAlone(t *testing.T) {
	items := []HybridSearchResultItem{
		item("a.md", "the deployment pipeline builds an image and pushes it", 0.9, time.Now()),
		item("b.md", "certificate rotation needs the proxy reloaded afterwards", 0.8, time.Now()),
		item("c.md", "draining a node moves its traffic elsewhere first", 0.7, time.Now()),
	}
	original := keys(items)

	reranked, breakdown := applyWeightedSignals(append([]HybridSearchResultItem(nil), items...),
		FusionSignals{Diversity: 0.9}, time.Now())

	if got := keys(reranked); !equalStrings(got, original) {
		t.Errorf("unrelated results were reordered: %v → %v", original, got)
	}
	for i, b := range breakdown {
		if b.Diversity != 0 {
			t.Errorf("result %d was penalised %v despite sharing nothing", i, b.Diversity)
		}
	}
}

// The existing strategies must be untouched — that is the compatibility
// promise SRCH-002 made.
func TestNoSignalsMeansNoChange(t *testing.T) {
	items := []HybridSearchResultItem{
		item("a.md", "one", 0.9, time.Now()),
		item("b.md", "two", 0.8, time.Now()),
	}
	original := append([]HybridSearchResultItem(nil), items...)

	got, breakdown := applyWeightedSignals(items, FusionSignals{}, time.Now())

	if breakdown != nil {
		t.Error("a request with no weights produced a breakdown")
	}
	for i := range got {
		if got[i].CombinedScore != original[i].CombinedScore {
			t.Errorf("result %d moved from %v to %v with no signals configured",
				i, original[i].CombinedScore, got[i].CombinedScore)
		}
	}
}

func TestProximityRewardsTheSameDirectory(t *testing.T) {
	items := []HybridSearchResultItem{
		item("docs/api/auth.md", "authentication", 0.90, time.Now()),
		item("blog/unrelated.md", "something else", 0.80, time.Now()),
		item("docs/api/keys.md", "api keys", 0.79, time.Now()),
	}

	// 0.1, not 0.5: a weight is a fraction of the result's own score, so at
	// 0.5 the bonus would carry keys.md past auth.md as well — which is a
	// neighbourhood hint overruling a much better match.
	reranked, breakdown := applyWeightedSignals(items, FusionSignals{Proximity: 0.1}, time.Now())

	// docs/api/keys.md shares a directory with the top result and started
	// 0.01 behind blog/unrelated.md; the bonus should be enough to pass it,
	// and not enough to pass the top result.
	if reranked[0].Document.Key != "docs/api/auth.md" {
		t.Errorf("order = %v, want the strongest match still first", keys(reranked))
	}
	if reranked[1].Document.Key != "docs/api/keys.md" {
		t.Errorf("order = %v, want the same-directory document promoted", keys(reranked))
	}
	if breakdown[1].Proximity <= 0 && breakdown[2].Proximity <= 0 {
		t.Error("no result received a proximity bonus")
	}
}

func TestPathAffinity(t *testing.T) {
	cases := map[string]struct {
		a, b string
		want bool // whether affinity should be positive
	}{
		"same directory":         {"docs/api/auth.md", "docs/api/keys.md", true},
		"parent and child":       {"docs/api/auth.md", "docs/guide.md", true},
		"nothing shared":         {"docs/api.md", "blog/post.md", false},
		"identical key":          {"a/b.md", "a/b.md", false},
		"empty":                  {"", "docs/a.md", false},
		"no directory on either": {"a.md", "b.md", false},
		// The reason affinity is segment-wise: these share five characters and
		// no directory.
		"similar names, different dirs": {"docs/api.md", "docs2/apiary.md", false},
	}

	for name, c := range cases {
		got := pathAffinity(c.a, c.b)
		if (got > 0) != c.want {
			t.Errorf("%s: pathAffinity(%q, %q) = %v", name, c.a, c.b, got)
		}
		if got < 0 || got > 1 {
			t.Errorf("%s: affinity %v is outside [0,1]", name, got)
		}
	}
}

func TestFreshnessDecaysAndIgnoresUndatedDocuments(t *testing.T) {
	now := time.Now()

	if got := freshnessFactor(now, now, 180); got != 1 {
		t.Errorf("a document updated now scored %v, want 1", got)
	}
	half := freshnessFactor(now.AddDate(0, 0, -180), now, 180)
	if half < 0.49 || half > 0.51 {
		t.Errorf("a document one half-life old scored %v, want ~0.5", half)
	}
	if old := freshnessFactor(now.AddDate(-5, 0, 0), now, 180); old > 0.01 {
		t.Errorf("a five-year-old document scored %v", old)
	}

	// An undated document must be neither rewarded nor punished — treating
	// the zero time as "infinitely old" would bury every legacy record.
	if got := freshnessFactor(time.Time{}, now, 180); got != 0 {
		t.Errorf("an undated document scored %v, want 0", got)
	}
	if got := freshnessFactor(now, now, 0); got != 0 {
		t.Errorf("a zero half-life scored %v, want 0", got)
	}
	// A clock skew that puts a document in the future is still just "fresh".
	if got := freshnessFactor(now.AddDate(0, 0, 5), now, 180); got != 1 {
		t.Errorf("a future timestamp scored %v, want 1", got)
	}
}

func TestSignalsDefaults(t *testing.T) {
	got := FusionSignals{Diversity: 0.5}.Defaults()
	if got.DiversityThreshold != 0.5 {
		t.Errorf("DiversityThreshold = %v", got.DiversityThreshold)
	}
	if got.FreshnessHalfLifeDays != 180 {
		t.Errorf("FreshnessHalfLifeDays = %v", got.FreshnessHalfLifeDays)
	}
	// An explicit value must survive.
	explicit := FusionSignals{Diversity: 0.5, DiversityThreshold: 0.9, FreshnessHalfLifeDays: 30}.Defaults()
	if explicit.DiversityThreshold != 0.9 || explicit.FreshnessHalfLifeDays != 30 {
		t.Errorf("explicit thresholds were overwritten: %+v", explicit)
	}

	if (FusionSignals{}).Any() {
		t.Error("empty signals report themselves as configured")
	}
	for name, s := range map[string]FusionSignals{
		"diversity": {Diversity: 0.1},
		"proximity": {Proximity: 0.1},
		"freshness": {Freshness: 0.1},
	} {
		if !s.Any() {
			t.Errorf("%s alone is not recognised as configured", name)
		}
	}
}

func TestWeightedSignalsRenumberRanks(t *testing.T) {
	dup := "the same words repeated so the two documents overlap almost entirely for minhash"
	items := []HybridSearchResultItem{
		item("a.md", dup, 0.9, time.Now()),
		item("b.md", dup+" plus a tail", 0.89, time.Now()),
		item("c.md", "a completely different subject about certificates and proxies", 0.5, time.Now()),
	}

	reranked, _ := applyWeightedSignals(items, FusionSignals{Diversity: 0.9}, time.Now())

	for i, it := range reranked {
		if it.Rank != i+1 {
			t.Errorf("result at position %d carries rank %d", i, it.Rank)
		}
	}
}

func TestEmptyResultsAreSafe(t *testing.T) {
	got, breakdown := applyWeightedSignals(nil, FusionSignals{Diversity: 1}, time.Now())
	if got != nil || breakdown != nil {
		t.Errorf("empty input produced %v / %v", got, breakdown)
	}
	// A document with no content has no signature; it must not be dropped.
	items := []HybridSearchResultItem{item("a.md", "", 0.9, time.Now())}
	out, _ := applyWeightedSignals(items, FusionSignals{Diversity: 1}, time.Now())
	if len(out) != 1 {
		t.Errorf("a result with no content was dropped")
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

var _ = strings.TrimSpace

// A weight is a fraction of the score, so a large one is genuinely powerful.
// That is documented behaviour rather than a bug, and the clamp is what stops
// it becoming absurd.
func TestWeightsAreClampedToAUsefulRange(t *testing.T) {
	got := FusionSignals{Diversity: 5, Proximity: -3, Freshness: 99}.Defaults()

	if got.Diversity != MaxSignalWeight {
		t.Errorf("Diversity = %v, want it clamped to %v", got.Diversity, MaxSignalWeight)
	}
	if got.Proximity != 0 {
		t.Errorf("a negative weight became %v; it should turn the signal off, not invert it", got.Proximity)
	}
	if got.Freshness != MaxSignalWeight {
		t.Errorf("Freshness = %v", got.Freshness)
	}
}

func TestALargeProximityWeightCanOutrankAStrongerMatch(t *testing.T) {
	items := []HybridSearchResultItem{
		item("docs/api/auth.md", "authentication", 0.90, time.Now()),
		item("docs/api/keys.md", "api keys", 0.79, time.Now()),
	}

	reranked, _ := applyWeightedSignals(items, FusionSignals{Proximity: 1.0}, time.Now())

	// 0.79 × 2 = 1.58 > 0.90. Pinned so the trade-off is visible rather than
	// discovered: at the top of the range, proximity is not a tie-breaker.
	if reranked[0].Document.Key != "docs/api/keys.md" {
		t.Errorf("order = %v; a maximal proximity weight is expected to dominate", keys(reranked))
	}
}
