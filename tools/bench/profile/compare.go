package main

import (
	"fmt"
	"math"
)

// Comparing a run with a baseline: the regression gate.
//
// Recall does not depend on the machine, so it is compared as it is: a
// scenario may not lose more than RecallDrop.
//
// Latency does depend on the machine, and the committed baseline was not
// recorded on the runner that checks a pull request. So unless both results
// come from the same machine, a p95 is not compared raw but as its share of
// the run: each scenario's p95 divided by the geometric mean p95 of all the
// scenarios both results have. A slower machine moves every scenario and
// leaves the shares alone; a change that slows one index or one path moves
// its share. What shares cannot see — everything slower at once — is caught
// by a ceiling on the raw p95, set far above runner-to-runner noise.

// thresholds are the gate's limits.
type thresholds struct {
	P95Regression float64 // a p95 (or its share) may grow by this fraction
	RecallDrop    float64 // recall@10 may fall by this much
	// MinDeltaMs is how much a p95 must grow in absolute terms to count: a
	// 0.3 ms request that takes 0.4 ms is scheduling, not a regression.
	MinDeltaMs float64
	// Ceiling is the multiple of the baseline's raw p95, scaled by how much
	// slower this machine calibrated, that no scenario may pass.
	Ceiling float64
	// Raw compares raw p95 even across machines; the same machine always does.
	Raw bool
}

// defaultThresholds are the ticket's: 15% on p95, 0.02 on recall.
var defaultThresholds = thresholds{P95Regression: 0.15, RecallDrop: 0.02, MinDeltaMs: 0.25, Ceiling: 3}

// comparison is the gate's verdict.
type comparison struct {
	Mode     string   // "raw p95" or "p95 share of the run"
	Failures []string // empty = pass
	// Latency counts the failures that are p95 limits: when it is all of
	// them, a second measurement can tell a regression from a noisy runner.
	Latency int
	Notes   []string // differences that do not fail the gate
}

// sameMachine reports whether two results were measured on the same kind of
// machine: same CPU model, core count and platform.
func sameMachine(a, b Environment) bool {
	return a.CPU != "" && a.CPU == b.CPU && a.Cores == b.Cores && a.OS == b.OS && a.Arch == b.Arch
}

// compare checks cur against base.
func compare(base, cur *Result, t thresholds) comparison {
	var c comparison
	if base.Schema != cur.Schema || base.Profile != cur.Profile {
		c.Failures = append(c.Failures, fmt.Sprintf("baseline is schema %d profile %q, this run schema %d profile %q: not comparable",
			base.Schema, base.Profile, cur.Schema, cur.Profile))
		return c
	}
	raw := t.Raw || sameMachine(base.Environment, cur.Environment)
	c.Mode = "p95 share of the run"
	if raw {
		c.Mode = "raw p95"
	}
	if len(cur.Search) > 0 {
		c.compareSearch(base, cur, t, raw)
	}
	if r := cur.Recovery; r != nil && r.LostWrites > 0 {
		c.Failures = append(c.Failures, fmt.Sprintf("kill -9: %d of %d acknowledged writes missing after restart", r.LostWrites, r.AckedWrites))
	}
	c.compareReplication(base.Replication, cur.Replication)
	return c
}

// compareReplication fails a follower that no longer reaches the leader. One
// that did not in the baseline either is a known defect, noted and not
// failed, so the gate blocks a regression and not every change after it.
func (c *comparison) compareReplication(base, cur *Replication) {
	switch {
	case cur == nil || cur.InSync:
	case base == nil || base.InSync:
		c.Failures = append(c.Failures, "replication: the follower did not reach the leader: "+cur.OutOfSync)
	default:
		c.Notes = append(c.Notes, "replication: the follower did not reach the leader, as in the baseline: "+cur.OutOfSync)
	}
	if cur != nil && base != nil && cur.LateProbes > base.LateProbes {
		c.Notes = append(c.Notes, fmt.Sprintf("replication: %d lag probes late, baseline %d", cur.LateProbes, base.LateProbes))
	}
}

// compareSearch applies the recall and p95 limits scenario by scenario.
func (c *comparison) compareSearch(base, cur *Result, t thresholds, raw bool) {
	now := map[string]SearchMetric{}
	for _, m := range cur.Search {
		now[m.Name] = m
	}
	var pairs [][2]SearchMetric
	for _, b := range base.Search {
		m, ok := now[b.Name]
		if !ok {
			c.Failures = append(c.Failures, b.Name+": in the baseline, not measured by this run")
			continue
		}
		pairs = append(pairs, [2]SearchMetric{b, m})
	}
	baseMean, curMean := 1.0, 1.0
	if !raw {
		baseMean, curMean = geomeanP95(pairs, 0), geomeanP95(pairs, 1)
	}
	slower := math.Max(1, safeRatio(cur.CalibrationMs, base.CalibrationMs))
	for _, pair := range pairs {
		b, m := pair[0], pair[1]
		if b.Recall10 != nil && m.Recall10 != nil && *b.Recall10-*m.Recall10 > t.RecallDrop {
			c.Failures = append(c.Failures, fmt.Sprintf("%s: recall@10 %.3f, baseline %.3f (limit −%.2f)",
				b.Name, *m.Recall10, *b.Recall10, t.RecallDrop))
		}
		// expected is the baseline p95 carried to this run's speed.
		expected := b.P95 / baseMean * curMean
		failures := len(c.Failures)
		switch {
		case m.P95 > expected*(1+t.P95Regression) && m.P95-expected > t.MinDeltaMs:
			c.Failures = append(c.Failures, fmt.Sprintf("%s: p95 %.3f ms, expected at most %.3f ms (baseline %.3f ms, +%.0f%% allowed, %s)",
				b.Name, m.P95, expected*(1+t.P95Regression), b.P95, t.P95Regression*100, c.Mode))
		case !raw && m.P95 > b.P95*slower*t.Ceiling && m.P95-b.P95 > t.MinDeltaMs:
			c.Failures = append(c.Failures, fmt.Sprintf("%s: p95 %.3f ms is over %.0f× the baseline's %.3f ms",
				b.Name, m.P95, t.Ceiling, b.P95))
		case m.P95 < expected*(1-t.P95Regression) && expected-m.P95 > t.MinDeltaMs:
			c.Notes = append(c.Notes, fmt.Sprintf("%s: p95 %.3f ms, faster than the %.3f ms expected", b.Name, m.P95, expected))
		}
		c.Latency += len(c.Failures) - failures
	}
}

// geomeanP95 is the geometric mean p95 of one side of the pairs; 1 for none.
func geomeanP95(pairs [][2]SearchMetric, side int) float64 {
	var sum float64
	n := 0
	for _, p := range pairs {
		if v := p[side].P95; v > 0 {
			sum += math.Log(v)
			n++
		}
	}
	if n == 0 {
		return 1
	}
	return math.Exp(sum / float64(n))
}

// safeRatio is a/b, 1 when either is unknown.
func safeRatio(a, b float64) float64 {
	if a <= 0 || b <= 0 {
		return 1
	}
	return a / b
}
