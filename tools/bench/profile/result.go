package main

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// The result file: everything one run measured, with the machine and the
// build it was measured on. docs/benchmarks/<version>-<profile>.json is one of
// these, and the table in docs/BENCHMARK.md is rendered from it.

// schemaVersion changes when a field changes meaning; compare refuses to set
// two different schemas side by side.
const schemaVersion = 1

// Result is one run of one profile.
type Result struct {
	Schema      int         `json:"schema"`
	Version     string      `json:"version"` // the server's, from `mddbd --version`
	Commit      string      `json:"commit"`
	Profile     string      `json:"profile"`
	Seed        int64       `json:"seed"`
	RecordedAt  string      `json:"recordedAt"` // RFC 3339, UTC
	Environment Environment `json:"environment"`
	Corpus      CorpusInfo  `json:"corpus"`
	// CalibrationMs is a fixed in-process workload timed on this machine: a
	// rough measure of how fast the machine is, independent of the server.
	CalibrationMs float64        `json:"calibrationMs"`
	Search        []SearchMetric `json:"search,omitempty"`
	Ingest        []IngestMetric `json:"ingest,omitempty"`
	Mixed         *MixedMetric   `json:"mixed,omitempty"`
	Startup       *StartupMetric `json:"startup,omitempty"`
	Recovery      *Recovery      `json:"recovery,omitempty"`
	Replication   *Replication   `json:"replication,omitempty"`
}

// Environment is the machine and toolchain a run was measured on.
type Environment struct {
	OS        string  `json:"os"`
	Arch      string  `json:"arch"`
	Kernel    string  `json:"kernel,omitempty"`
	CPU       string  `json:"cpu,omitempty"`
	Cores     int     `json:"cores"`
	MemoryGB  float64 `json:"memoryGb,omitempty"`
	GoVersion string  `json:"goVersion"`
}

// CorpusInfo is the seeded corpus the search scenarios ran on.
type CorpusInfo struct {
	Vectors int `json:"vectors"`
	Dims    int `json:"dims"`
	Topics  int `json:"topics"`
	Queries int `json:"queries"` // timed queries per scenario and round
	Rounds  int `json:"rounds"`
	Clients int `json:"clients"` // concurrent clients of the QPS pass
}

// Latency is client-side request latency in milliseconds.
type Latency struct {
	P50 float64 `json:"p50Ms"`
	P95 float64 `json:"p95Ms"`
	P99 float64 `json:"p99Ms"`
}

// SearchMetric is one search scenario. Latency is from sequential requests
// (the median of the rounds); QPS is from the concurrent pass.
type SearchMetric struct {
	Name           string `json:"name"`                     // unique: the key compare matches on
	Kind           string `json:"kind"`                     // fts, vector, hybrid, filtered
	Algorithm      string `json:"algorithm,omitempty"`      // as requested
	Ran            string `json:"ran,omitempty"`            // what answered, from the plan
	SelectivityPct int    `json:"selectivityPct,omitempty"` // share of documents the filter admits
	Latency
	QPS float64 `json:"qps"`
	// Recall10 is mean recall@10 against the exact answer; nil where there is
	// no exact answer to score against (full text, hybrid).
	Recall10 *float64 `json:"recall10,omitempty"`
}

// IngestMetric is one way of writing documents.
type IngestMetric struct {
	Name       string  `json:"name"`
	Docs       int     `json:"docs"`
	BatchSize  int     `json:"batchSize"` // 1 = one document per request
	Embedding  string  `json:"embedding"` // "none" or the provider
	DocsPerSec float64 `json:"docsPerSec"`
	Latency            // per request
}

// MixedMetric is searches and writes on one collection at the same time.
type MixedMetric struct {
	Seconds      float64 `json:"seconds"`
	Readers      int     `json:"readers"`
	Writers      int     `json:"writers"`
	Read         Latency `json:"read"`
	ReadQPS      float64 `json:"readQps"`
	Write        Latency `json:"write"`
	WritesPerSec float64 `json:"writesPerSec"`
}

// StartupMetric is a clean restart of the loaded search node.
type StartupMetric struct {
	// HealthySeconds is process start to /health answering healthy.
	HealthySeconds float64 `json:"healthySeconds"`
	// IndexesReadySeconds is process start to every vector algorithm
	// answering a search on the corpus.
	IndexesReadySeconds float64 `json:"indexesReadySeconds"`
	// IndexReadySeconds is, per algorithm, process start to its first
	// answer. The algorithms are asked in report order, one after another.
	IndexReadySeconds map[string]float64 `json:"indexReadySeconds,omitempty"`
	// PeakRSSMB is the search node's peak resident memory, over the load and
	// the searches or over the restart and the mixed workload, whichever was
	// higher; 0 where the OS does not report it.
	PeakRSSMB float64 `json:"peakRssMb"`
}

// Recovery is the kill -9 scenario: writes in flight, SIGKILL, restart.
type Recovery struct {
	AckedWrites  int     `json:"ackedWrites"`
	LostWrites   int     `json:"lostWrites"` // acknowledged and absent after restart
	ReadySeconds float64 `json:"readySeconds"`
	// CleanRestartSeconds is the same node stopped with SIGTERM and started
	// again, for the difference a crash makes.
	CleanRestartSeconds float64 `json:"cleanRestartSeconds"`
}

// Replication is the leader and follower scenario.
type Replication struct {
	LagProbes int `json:"lagProbes"`
	// LagLoad is the documents per second written to the leader, one at a
	// time, while the probes run.
	LagLoad int     `json:"lagLoad"`
	Lag     Latency `json:"lag"` // leader acknowledgement to visible on the follower, over the probes that arrived
	// LateProbes is how many probes did not reach the follower within 30 s:
	// the follower could not keep up with the load. Probing stops at the
	// first one, so the rest of the probes count as late too.
	LateProbes  int `json:"lateProbes"`
	CatchupDocs int `json:"catchupDocs"`
	// CatchupSeconds is follower process start to the follower holding what
	// the leader wrote while it was down.
	CatchupSeconds float64 `json:"catchupSeconds"`
	InSync         bool    `json:"inSync"` // checksums and document counts equal at the end
	// OutOfSync is, when the follower did not reach the leader within the
	// profile's catch-up limit, at which stage and with how many documents
	// on each side.
	OutOfSync string `json:"outOfSync,omitempty"`
}

// writeResult stores r as indented JSON, creating the directory.
func writeResult(path string, r *Result) error {
	raw, _ := json.MarshalIndent(r, "", "  ")
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	return os.WriteFile(path, append(raw, '\n'), 0o600) // #nosec G703 -- the operator's own -out path
}

// readResult loads a result file.
func readResult(path string) (*Result, error) {
	raw, err := os.ReadFile(path) // #nosec G304 -- the operator's own result file
	if err != nil {
		return nil, err
	}
	var r Result
	if err := json.Unmarshal(raw, &r); err != nil {
		return nil, err
	}
	return &r, nil
}
