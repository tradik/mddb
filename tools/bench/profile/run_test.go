package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// tinyProfile does every scenario with as little work as there is.
func tinyProfile() profile {
	return profile{
		Name: "tiny", Vectors: 200, Dims: 8, Topics: 2,
		Queries: 4, Warmup: 2, Rounds: 1, Clients: 2,
		LoadBatch: 50, LoadParallel: 2,
		IngestSingle: 5, IngestBatch: 60,
		Mixed: 100 * time.Millisecond, KillAfter: 100 * time.Millisecond, KillWriters: 2,
		BaseDocs: 20, LagProbes: 3, LagLoad: 50, CatchupDocs: 30, CatchupLimit: 20 * time.Second,
		Timeout: 20 * time.Second,
	}
}

// The whole run against the stand-in server: every section, a result file,
// its Markdown and a comparison with itself.
func TestMeasureAllAgainstTheStandIn(t *testing.T) {
	bin := useFake(t)
	dir := t.TempDir()
	cfg := config{Profile: tinyProfile(), Seed: 1, Bin: bin, Commit: "abc", WorkDir: dir,
		Only: map[string]bool{"search": true, "ingest": true, "recovery": true, "replication": true},
		Out:  filepath.Join(dir, "out", "r.json"), Markdown: true, Limits: defaultThresholds}
	var out, log bytes.Buffer
	if err := execute(cfg, &out, &log); err != nil {
		t.Fatalf("%v\n%s", err, log.String())
	}
	checkFullResult(t, cfg.Out)
	if !containsAll(out.String(), "| `vector-hnsw` |", "kill -9 under write") {
		t.Errorf("markdown:\n%s", out.String())
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Errorf("the scratch directory was left behind: %v", entries)
	}

	// The same file as its own baseline passes the gate.
	out.Reset()
	cfg = config{In: cfg.Out, Baseline: cfg.Out, Limits: defaultThresholds}
	if err := execute(cfg, &out, &log); err != nil || !strings.Contains(out.String(), "no regression") {
		t.Errorf("self comparison: %v\n%s", err, out.String())
	}
}

// checkFullResult checks a result of every section of the tiny profile.
func checkFullResult(t *testing.T, path string) {
	t.Helper()
	res, err := readResult(path)
	if err != nil {
		t.Fatal(err)
	}
	if res.Version != "0.0.0-fake" || res.Commit != "abc" || res.Profile != "tiny" {
		t.Errorf("identity: %+v", res)
	}
	if len(res.Search) != len(scenarios()) || len(res.Ingest) != 4 {
		t.Errorf("search %d scenarios, ingest %d", len(res.Search), len(res.Ingest))
	}
	if res.Startup == nil || len(res.Startup.IndexReadySeconds) != len(vectorAlgorithms) || res.Mixed == nil {
		t.Errorf("startup %+v mixed %+v", res.Startup, res.Mixed)
	}
	if r := res.Recovery; r == nil || r.AckedWrites == 0 || r.LostWrites != 0 {
		t.Errorf("recovery %+v", r)
	}
	if r := res.Replication; r == nil || !r.InSync || r.LagProbes != 3 {
		t.Errorf("replication %+v", r)
	}
}

// One section alone, with the scratch directory kept.
func TestMeasureAllOneSectionKept(t *testing.T) {
	bin := useFake(t)
	dir := t.TempDir()
	cfg := config{Profile: tinyProfile(), Bin: bin, WorkDir: dir, Keep: true, Only: map[string]bool{"recovery": true}}
	var log bytes.Buffer
	res, err := measureAll(cfg, &log)
	if err != nil {
		t.Fatalf("%v\n%s", err, log.String())
	}
	if res.Recovery == nil || res.Search != nil || res.Replication != nil {
		t.Errorf("ran more than recovery: %+v", res)
	}
	kept, _ := filepath.Glob(filepath.Join(dir, "bench-profile-*", "crash", "server.log"))
	if len(kept) != 1 {
		t.Errorf("the kept scratch directory has no server log: %v", kept)
	}
}

func TestMeasureAllFailures(t *testing.T) {
	var log bytes.Buffer
	if _, err := measureAll(config{Bin: filepath.Join(t.TempDir(), "missing")}, &log); err == nil {
		t.Error("a missing binary measured")
	}
	bin := useFake(t)
	if _, err := measureAll(config{Bin: bin, WorkDir: filepath.Join(t.TempDir(), "no", "such")}, &log); err == nil {
		t.Error("an absent work directory was used")
	}
	// A server that never becomes healthy fails its section, by name.
	p := tinyProfile()
	p.Timeout = 300 * time.Millisecond
	t.Setenv(fakeServerEnv, "")
	cfg := config{Profile: p, Bin: writeScript(t, "#!/bin/sh\n[ \"$1\" = --version ] && echo mddbd version 9\nsleep 5\n"),
		WorkDir: t.TempDir(), Only: map[string]bool{"recovery": true}}
	if _, err := measureAll(cfg, &log); err == nil || !strings.HasPrefix(err.Error(), "recovery:") {
		t.Errorf("an unhealthy server: %v", err)
	}
}

// writeScript is an executable shell script with body.
func writeScript(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "mddbd")
	if err := os.WriteFile(path, []byte(body), 0o700); err != nil { // #nosec G306 -- a test script that must be executable
		t.Fatal(err)
	}
	return path
}

func TestServerVersion(t *testing.T) {
	if _, err := serverVersion(writeScript(t, "#!/bin/sh\nexit 0\n")); err == nil {
		t.Error("a binary that prints nothing has a version")
	}
	if v, err := serverVersion(writeScript(t, "#!/bin/sh\necho mddbd version 1.2.3\n")); err != nil || v != "1.2.3" {
		t.Errorf("version %q, %v", v, err)
	}
}
