package main

import (
	"bytes"
	"strings"
	"testing"
)

func ptr(f float64) *float64 { return &f }

// result builds a search-only result with the given p95 per scenario.
func result(cpu string, calibration float64, p95 map[string]float64, recall map[string]float64) *Result {
	r := &Result{Schema: schemaVersion, Profile: "small", CalibrationMs: calibration,
		Environment: Environment{OS: "linux", Arch: "amd64", CPU: cpu, Cores: 4}}
	for _, name := range []string{"a", "b", "c", "d"} {
		m := SearchMetric{Name: name, Latency: Latency{P95: p95[name]}}
		if v, ok := recall[name]; ok {
			m.Recall10 = ptr(v)
		}
		r.Search = append(r.Search, m)
	}
	return r
}

var baseP95 = map[string]float64{"a": 1, "b": 2, "c": 4, "d": 8}

func TestCompareSameMachineRaw(t *testing.T) {
	base := result("cpu", 10, baseP95, map[string]float64{"a": 0.95})
	same := result("cpu", 10, baseP95, map[string]float64{"a": 0.94})
	if c := compare(base, same, defaultThresholds); len(c.Failures) != 0 || c.Mode != "raw p95" {
		t.Errorf("identical runs: %+v", c)
	}
	slow := result("cpu", 10, map[string]float64{"a": 1, "b": 2, "c": 4.8, "d": 8}, map[string]float64{"a": 0.92})
	c := compare(base, slow, defaultThresholds)
	if len(c.Failures) != 2 || !strings.Contains(c.Failures[0], "recall@10") || !strings.Contains(c.Failures[1], "c: p95") {
		t.Errorf("a 20%% slower scenario and a 0.03 recall loss: %+v", c.Failures)
	}
	fast := result("cpu", 10, map[string]float64{"a": 1, "b": 2, "c": 2, "d": 8}, nil)
	if c := compare(base, fast, defaultThresholds); len(c.Failures) != 0 || len(c.Notes) != 1 {
		t.Errorf("a faster scenario is a note: %+v", c)
	}
	// Under MinDeltaMs nothing counts, however large the share.
	tiny := result("cpu", 10, map[string]float64{"a": 1.2, "b": 2, "c": 4, "d": 8}, nil)
	if c := compare(base, tiny, defaultThresholds); len(c.Failures) != 0 {
		t.Errorf("0.2 ms is noise: %+v", c.Failures)
	}
}

func TestCompareAcrossMachinesUsesShares(t *testing.T) {
	base := result("cpu-a", 10, baseP95, nil)
	// Everything twice as slow on a twice slower machine: no regression.
	twice := result("cpu-b", 20, map[string]float64{"a": 2, "b": 4, "c": 8, "d": 16}, nil)
	if c := compare(base, twice, defaultThresholds); len(c.Failures) != 0 || c.Mode != "p95 share of the run" {
		t.Errorf("a uniformly slower machine: %+v", c)
	}
	// One scenario slower than the rest: caught through its share.
	one := result("cpu-b", 20, map[string]float64{"a": 2, "b": 4, "c": 12, "d": 16}, nil)
	if c := compare(base, one, defaultThresholds); len(c.Failures) != 1 || !strings.Contains(c.Failures[0], "c: p95") {
		t.Errorf("one slower scenario: %+v", c.Failures)
	}
	// Everything 4× slower on a machine that calibrates as fast: the ceiling.
	all := result("cpu-b", 10, map[string]float64{"a": 4, "b": 8, "c": 16, "d": 32}, nil)
	if c := compare(base, all, defaultThresholds); len(c.Failures) != 4 || !strings.Contains(c.Failures[0], "over 3×") {
		t.Errorf("everything slower: %+v", c.Failures)
	}
	// Raw forces the raw comparison.
	raw := defaultThresholds
	raw.Raw = true
	if c := compare(base, twice, raw); c.Mode != "raw p95" || len(c.Failures) != 4 {
		t.Errorf("forced raw: %+v", c)
	}
}

func TestCompareStructuralFailures(t *testing.T) {
	base := result("cpu", 10, baseP95, nil)
	other := result("cpu", 10, baseP95, nil)
	other.Profile = "full"
	if c := compare(base, other, defaultThresholds); len(c.Failures) != 1 || !strings.Contains(c.Failures[0], "not comparable") {
		t.Errorf("profiles differ: %+v", c)
	}
	missing := result("cpu", 10, baseP95, nil)
	missing.Search = missing.Search[1:]
	missing.Recovery = &Recovery{AckedWrites: 10, LostWrites: 2}
	missing.Replication = &Replication{}
	c := compare(base, missing, defaultThresholds)
	want := []string{"a: in the baseline", "kill -9: 2 of 10", "replication: the follower"}
	if len(c.Failures) != len(want) {
		t.Fatalf("failures %+v", c.Failures)
	}
	for i, w := range want {
		if !strings.HasPrefix(c.Failures[i], w) {
			t.Errorf("failure %d = %q, want prefix %q", i, c.Failures[i], w)
		}
	}
	// A run without search compares only what it has.
	none := &Result{Schema: schemaVersion, Profile: "small"}
	if c := compare(base, none, defaultThresholds); len(c.Failures) != 0 {
		t.Errorf("no search: %+v", c.Failures)
	}
}

func TestGeomeanAndRatio(t *testing.T) {
	if g := geomeanP95(nil, 0); g != 1 {
		t.Errorf("geomean of nothing = %v", g)
	}
	pairs := [][2]SearchMetric{{{Latency: Latency{P95: 2}}, {}}, {{Latency: Latency{P95: 8}}, {}}}
	if g := geomeanP95(pairs, 0); g < 3.999 || g > 4.001 {
		t.Errorf("geomean(2, 8) = %v", g)
	}
	if safeRatio(0, 1) != 1 || safeRatio(1, 0) != 1 || safeRatio(6, 3) != 2 {
		t.Error("safeRatio")
	}
	if sameMachine(Environment{}, Environment{}) {
		t.Error("two unknown CPUs are the same machine")
	}
}

func TestGate(t *testing.T) {
	var out bytes.Buffer
	if err := gate(comparison{Mode: "raw p95", Notes: []string{"n"}}, "b.json", &out); err != nil ||
		!containsAll(out.String(), "b.json (raw p95)", "note: n", "no regression") {
		t.Errorf("%v\n%s", err, out.String())
	}
	out.Reset()
	if err := gate(comparison{Failures: []string{"f"}}, "b.json", &out); err == nil || !strings.Contains(out.String(), "FAIL: f") {
		t.Errorf("%v\n%s", err, out.String())
	}
}
