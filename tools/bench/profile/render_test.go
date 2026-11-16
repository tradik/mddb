package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// sample is a result with every part filled.
func sample() *Result {
	return &Result{
		Schema: schemaVersion, Version: "1.0.0", Commit: "abc", Profile: "small", Seed: 1, RecordedAt: "2026-11-16T10:00:00Z",
		Environment:   Environment{OS: "linux", Arch: "amd64", Cores: 8, MemoryGB: 16, GoVersion: "go1.27.4"},
		Corpus:        CorpusInfo{Vectors: 100, Dims: 8, Topics: 2, Queries: 10, Rounds: 3, Clients: 2},
		CalibrationMs: 9.5,
		Search: []SearchMetric{
			{Name: "fts-bm25", Kind: "fts", Ran: "bm25", Latency: Latency{1, 2, 3}, QPS: 100},
			{Name: "hybrid", Kind: "hybrid", Latency: Latency{1, 2, 3}, QPS: 100},
			{Name: "vector-hnsw", Kind: "vector", Ran: "hnsw", Latency: Latency{1, 2, 3}, QPS: 100, Recall10: ptr(0.98)},
		},
		Ingest:      []IngestMetric{{Name: "single-none", Docs: 10, BatchSize: 1, Embedding: "none", DocsPerSec: 50}},
		Mixed:       &MixedMetric{Seconds: 10, Readers: 4, Writers: 1},
		Startup:     &StartupMetric{HealthySeconds: 0.1, IndexesReadySeconds: 2, PeakRSSMB: 100, IndexReadySeconds: map[string]float64{"ivf": 2, "flat": 0.1}},
		Recovery:    &Recovery{AckedWrites: 5, ReadySeconds: 0.1, CleanRestartSeconds: 0.1},
		Replication: &Replication{LagProbes: 3, CatchupDocs: 30, CatchupSeconds: 1, InSync: true},
	}
}

func TestRender(t *testing.T) {
	md := render(sample())
	for _, want := range []string{
		"MDDB 1.0.0 (`abc`), profile `small`, seed 1, recorded 2026-11-16 on unknown — 8 cores",
		"| `vector-hnsw` | hnsw | 1.000 | 2.000 | 3.000 | 100 | 0.980 |",
		"| `hybrid` | — |", "| `fts-bm25` | bm25 |", "| — |\n",
		"| `single-none` | 10 | 1 | none | 50 |",
		"Mixed (4 readers, 1 writer, 10 s)", "| flat 0.1 s, ivf 2.0 s |", "| 1.000 s |", "Peak resident memory", "kill -9 under write", "Follower checksum and document count equal the leader's | true |",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("rendered result lacks %q:\n%s", want, md)
		}
	}
	empty := render(&Result{})
	if strings.Contains(empty, "|") {
		t.Errorf("an empty result rendered tables:\n%s", empty)
	}
}

func TestSyncDocsAndDrift(t *testing.T) {
	dir := t.TempDir()
	results := filepath.Join(dir, "benchmarks")
	if err := writeResult(filepath.Join(results, "1.0.0-small.json"), sample()); err != nil {
		t.Fatal(err)
	}
	doc := filepath.Join(dir, "BENCHMARK.md")
	begin, end := blockMarkers("1.0.0-small")
	if err := os.WriteFile(doc, []byte("# B\n\n"+begin+"\nstale\n"+end+"\n\ntail\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(doc) // #nosec G304 -- the test's own file
	if drift, err := docDrift(string(raw), results); err != nil || len(drift) != 1 {
		t.Errorf("stale block: %v %v", drift, err)
	}
	if err := syncDocs(doc, results); err != nil {
		t.Fatal(err)
	}
	raw, _ = os.ReadFile(doc) // #nosec G304 -- the test's own file
	if drift, err := docDrift(string(raw), results); err != nil || len(drift) != 0 {
		t.Errorf("after sync: %v %v", drift, err)
	}
	if !strings.HasSuffix(string(raw), end+"\n\ntail\n") || strings.Contains(string(raw), "stale") {
		t.Errorf("sync rewrote more or less than the block:\n%s", raw)
	}
	checkDocFailures(t, doc, results, string(raw))
}

// checkDocFailures: a result without a block, an absent document and a
// broken result file.
func checkDocFailures(t *testing.T, doc, results, raw string) {
	t.Helper()
	dir := filepath.Dir(doc)
	// A result without a block: drift names the markers to add; sync refuses.
	if err := writeResult(filepath.Join(results, "2.0.0-small.json"), sample()); err != nil {
		t.Fatal(err)
	}
	if drift, _ := docDrift(raw, results); len(drift) != 1 || !strings.Contains(drift[0], "bench-profile:2.0.0-small:begin") {
		t.Errorf("missing block: %v", drift)
	}
	if err := syncDocs(doc, results); err == nil {
		t.Error("sync wrote a document without a block for every result")
	}
	if err := syncDocs(filepath.Join(dir, "absent.md"), results); err == nil {
		t.Error("sync of an absent document")
	}

	// A broken result file fails both.
	if err := os.WriteFile(filepath.Join(results, "3.json"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := docDrift(raw, results); err == nil {
		t.Error("drift over a broken result file")
	}
	if err := syncDocs(doc, results); err == nil {
		t.Error("sync over a broken result file")
	}
}

func TestResultFiles(t *testing.T) {
	if _, err := readResult(filepath.Join(t.TempDir(), "absent.json")); err == nil {
		t.Error("read an absent file")
	}
	// A directory where the file's parent should be: writeResult fails.
	dir := t.TempDir()
	blocker := filepath.Join(dir, "file")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := writeResult(filepath.Join(blocker, "r.json"), sample()); err == nil {
		t.Error("wrote under a file")
	}
	names, results, err := resultFiles(filepath.Join(dir, "none"))
	if err != nil || len(names) != 0 || len(results) != 0 {
		t.Errorf("an absent directory: %v %v %v", names, results, err)
	}
}
