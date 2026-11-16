package main

import (
	"errors"
	"flag"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestProfileNamed(t *testing.T) {
	p, err := profileNamed("small", 0, 0)
	if err != nil || p.Vectors != 20_000 || p.Dims != 128 {
		t.Errorf("small: %+v %v", p, err)
	}
	p, err = profileNamed("full", 2_000_000, 64)
	if err != nil || p.Vectors != 2_000_000 || p.Topics != 10_000 || p.Dims != 64 {
		t.Errorf("full overridden: %+v %v", p, err)
	}
	if _, err := profileNamed("huge", 0, 0); err == nil || !strings.Contains(err.Error(), "full, small") {
		t.Errorf("unknown profile: %v", err)
	}
	if _, err := profileNamed("small", 10, 0); err == nil {
		t.Error("ten vectors accepted")
	}
}

func TestParseSections(t *testing.T) {
	all, err := parseSections(" ")
	if err != nil || len(all) != len(sections) {
		t.Errorf("default: %v %v", all, err)
	}
	two, err := parseSections("search, recovery")
	if err != nil || len(two) != 2 || !two["recovery"] {
		t.Errorf("two: %v %v", two, err)
	}
	if _, err := parseSections("search,nap"); err == nil {
		t.Error("an unknown section accepted")
	}
}

func TestParseFlags(t *testing.T) {
	parse := func(args ...string) (config, error) {
		fs := flag.NewFlagSet("profile", flag.ContinueOnError)
		fs.SetOutput(io.Discard)
		return parseFlags(fs, args)
	}
	cfg, err := parse("-mddbd", "/bin/mddbd", "-profile", "full", "-only", "ingest", "-max-p95-regression", "0.2", "-raw-p95")
	if err != nil || cfg.Profile.Name != "full" || !cfg.Only["ingest"] || cfg.Limits.P95Regression != 0.2 || !cfg.Limits.Raw {
		t.Errorf("%+v %v", cfg, err)
	}
	for _, bad := range [][]string{
		{"-nope"}, {"-mddbd", "x", "-profile", "x"}, {"-mddbd", "x", "-only", "x"},
		{"-sync-docs", "d.md"}, {},
	} {
		if _, err := parse(bad...); err == nil {
			t.Errorf("%v accepted", bad)
		}
	}
	if _, err := parse("-sync-docs", "d.md", "-results", "dir"); err != nil {
		t.Errorf("sync-docs: %v", err)
	}
}

func TestExecuteFromFiles(t *testing.T) {
	dir := t.TempDir()
	in := filepath.Join(dir, "in.json")
	if err := writeResult(in, sample()); err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	if err := execute(config{In: filepath.Join(dir, "absent.json")}, &out, io.Discard); err == nil {
		t.Error("an absent -in")
	}
	if err := execute(config{In: in, Baseline: filepath.Join(dir, "absent.json")}, &out, io.Discard); err == nil {
		t.Error("an absent -baseline")
	}
	if err := execute(config{In: in, Out: filepath.Join(in, "x.json")}, &out, io.Discard); err == nil {
		t.Error("-out under a file")
	}
	worse := sample()
	worse.Search[2].Recall10 = ptr(0.5)
	bad := filepath.Join(dir, "worse.json")
	_ = writeResult(bad, worse)
	if err := execute(config{In: bad, Baseline: in, Limits: defaultThresholds}, &out, io.Discard); err == nil {
		t.Error("a recall regression passed the gate")
	}
	if err := execute(config{In: in}, &out, io.Discard); err != nil {
		t.Error(err)
	}
	if err := execute(config{SyncDocs: filepath.Join(dir, "absent.md"), Results: dir}, &out, io.Discard); err == nil {
		t.Error("sync of an absent document")
	}
}

func TestCorpus(t *testing.T) {
	p := tinyProfile()
	c := newCorpus(p, 7)
	if len(c.docs) != 200 || len(c.queries) != p.Queries+p.Warmup || c.collection != "bench-tiny" {
		t.Fatalf("corpus %d docs %d queries %q", len(c.docs), len(c.queries), c.collection)
	}
	again := newCorpus(p, 7)
	if c.docText(5) != again.docText(5) || c.queries[3][0] != again.queries[3][0] {
		t.Error("the same seed drew a different corpus")
	}
}

func TestCorpusTexts(t *testing.T) {
	c := newCorpus(tinyProfile(), 7)
	e := c.embedder()
	if v := e.Embed("x bvec201 y"); v[0] != c.queries[1][0] {
		t.Error("a query marker does not embed to its query")
	}
	if !strings.HasPrefix(c.docText(3), "bvec3 ") || len(strings.Fields(c.docText(3))) != 4+docWords {
		t.Errorf("doc text %q", c.docText(3))
	}
	if q := c.queryText(1, true); !strings.HasSuffix(q, " bvec201") || len(strings.Fields(c.queryText(1, false))) != 2 {
		t.Errorf("query text %q", q)
	}
	if keyDoc(docKey(42)) != 42 || keyDoc("m00000001") != -1 {
		t.Error("keyDoc")
	}
	meta := docMeta(0)["sel"]
	if len(meta) != 1+len(selectivities) || len(docMeta(1)["sel"]) != 1 {
		t.Errorf("meta %v", meta)
	}
	if d := c.document(2); d["key"] != "d00000002" {
		t.Errorf("document %v", d)
	}
}

func TestCorpusTruths(t *testing.T) {
	p := tinyProfile()
	c := newCorpus(p, 7)
	all, one := c.truths(p.Warmup, 0), c.truths(p.Warmup, 1)
	if len(all) != p.Queries || len(all[0]) != 10 {
		t.Fatalf("truths %v", all)
	}
	for _, i := range one[0] {
		if !admits(i, 1) {
			t.Errorf("document %d is outside the 1%% slice", i)
		}
	}
	if r := recallAt([]string{docKey(all[0][0]), "x"}, all[0]); r != 0.1 {
		t.Errorf("recall %v", r)
	}
	if recallAt(nil, nil) != 0 {
		t.Error("recall of nothing")
	}
	if word(0) == word(1) || len(syllables) != 70 {
		t.Error("words")
	}
}

func TestWorkloadHelpers(t *testing.T) {
	var calls atomic.Int32
	tm, err := fanOut(10, 3, func(int) error { calls.Add(1); return nil })
	if err != nil || len(tm.Millis) != 10 || calls.Load() != 10 {
		t.Errorf("fanOut %v %v", tm, err)
	}
	boom := errors.New("boom")
	tm, err = fanOut(10, 1, func(i int) error {
		if i == 2 {
			return boom
		}
		return nil
	})
	if !errors.Is(err, boom) || len(tm.Millis) != 2 {
		t.Errorf("fanOut stops at the first failure: %v %v", len(tm.Millis), err)
	}
	if (timed{}).perSecond(5) != 0 || (timed{Elapsed: time.Second}).perSecond(5) != 5 {
		t.Error("perSecond")
	}
	if l := summarize([]float64{1, 2, 3, 4}); l.P50 == 0 || l.P99 < l.P50 {
		t.Errorf("summarize %+v", l)
	}
	if m := medianLatency(nil); m != (Latency{}) {
		t.Errorf("median of nothing %+v", m)
	}
	m := medianLatency([]Latency{{1, 9, 9}, {2, 2, 2}, {3, 3, 3}})
	if m != (Latency{2, 3, 3}) {
		t.Errorf("median %+v", m)
	}
	if joinSorted(map[string]bool{"b": true, "a": true}) != "a, b" || recallText(nil) != "—" {
		t.Error("text helpers")
	}
}

// statusServer answers /v1/indexing-status with the bodies in turn and
// /v1/add-batch with batch.
func statusServer(t *testing.T, batch string, statuses ...string) string {
	t.Helper()
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/add-batch" {
			_, _ = io.WriteString(w, batch)
			return
		}
		i := int(n.Add(1)) - 1
		_, _ = io.WriteString(w, statuses[min(i, len(statuses)-1)])
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func TestWriterAnswers(t *testing.T) {
	old := pollPeriod
	pollPeriod = time.Millisecond
	t.Cleanup(func() { pollPeriod = old })
	w := func(url string) writer { return writer{client: http.DefaultClient, url: url, collection: "c"} }

	if err := w(statusServer(t, "", `{"pendingEmbeddings":2}`, `{"pendingEmbeddings":0}`)).waitEmbedded(time.Second); err != nil {
		t.Errorf("drains: %v", err)
	}
	for body, want := range map[string]string{
		`{"failedEmbeddings":1}`: "failed", `{"pendingEmbeddings":1}`: "still pending", `{`: "indexing status",
	} {
		if err := w(statusServer(t, "", body)).waitEmbedded(5 * time.Millisecond); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v", body, err)
		}
	}
	if err := w("http://127.0.0.1:1").waitEmbedded(time.Second); err == nil {
		t.Error("no server")
	}
	for body, ok := range map[string]bool{`{"failed":0}`: true, `{"failed":1}`: false, `{"embeddingDropped":1}`: false, `{`: false} {
		if err := w(statusServer(t, body, "")).addBatch(nil); (err == nil) != ok {
			t.Errorf("add-batch %s: %v", body, err)
		}
	}
	if err := w("http://127.0.0.1:1").addBatch(nil); err == nil {
		t.Error("add-batch with no server")
	}
}

func TestEnvironment(t *testing.T) {
	dir := t.TempDir()
	old := procRoot
	procRoot = dir
	t.Cleanup(func() { procRoot = old })
	write := func(name, body string) {
		path := filepath.Join(dir, name)
		_ = os.MkdirAll(filepath.Dir(path), 0o750)
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("sys/kernel/osrelease", "6.1.0\n")
	write("cpuinfo", "processor\t: 0\nmodel name\t: Test CPU\n")
	write("meminfo", "MemTotal:       16777216 kB\n")
	write("42/status", "Name: x\nVmHWM:\t  204800 kB\n")
	e := environment()
	if e.Kernel != "6.1.0" || e.CPU != "Test CPU" || e.MemoryGB != 16 || e.Cores == 0 {
		t.Errorf("%+v", e)
	}
	if peakRSSMB(42) != 200 || peakRSSMB(43) != 0 {
		t.Error("peakRSSMB")
	}
	if calibrate() <= 0 {
		t.Error("calibration took no time")
	}
	if (&node{}).peakRSS() != 0 {
		t.Error("a node without a process has a peak")
	}
}

func TestNodeStopAndKillWithoutProcess(t *testing.T) {
	n := &node{}
	n.stop()
	n.kill()
	if _, err := newNode("x", filepath.Join(writeScript(t, ""), "sub"), "n"); err == nil {
		t.Error("a node under a file")
	}
	if err := (&node{bin: "x", dir: filepath.Join(t.TempDir(), "absent")}).start(); err == nil {
		t.Error("started without a directory for its log")
	}
	n2, err := newNode(filepath.Join(t.TempDir(), "absent"), t.TempDir(), "n")
	if err != nil {
		t.Fatal(err)
	}
	if err := n2.start(); err == nil {
		t.Error("started an absent binary")
	}
}

// A process that ignores SIGINT is killed after stopGrace.
func TestStopKillsAStubbornProcess(t *testing.T) {
	old := stopGrace
	stopGrace = 100 * time.Millisecond
	t.Cleanup(func() { stopGrace = old })
	n, err := newNode(writeScript(t, "#!/bin/sh\ntrap '' INT\nwhile :; do sleep 0.05; done\n"), t.TempDir(), "n")
	if err != nil {
		t.Fatal(err)
	}
	if err := n.start(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	n.stop()
	if n.cmd != nil {
		t.Error("the process outlived stop")
	}
	if _, err := n.waitHealthy(10*time.Millisecond, false); err == nil {
		t.Error("an exited process is healthy")
	}
}

func TestNodeEnvironmentDropsTheShells(t *testing.T) {
	t.Setenv("MDDB_PATH", "/elsewhere")
	n := &node{dir: "/d", httpAddr: "h", grpcAddr: "g", env: []string{"MDDB_X=1"}}
	env := strings.Join(n.environ(), "\n")
	if strings.Contains(env, "/elsewhere") || !containsAll(env, "MDDB_PATH=/d/mddb.db", "MDDB_HTTP_ADDR=h", "MDDB_X=1") {
		t.Errorf("environment:\n%s", env)
	}
}
