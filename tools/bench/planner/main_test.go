package main

import (
	"bytes"
	"flag"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

// Every size is loaded, its IVF trained and every search measured; the report
// carries what auto ran, and an exact fake scores recall 1 everywhere.
func TestServeAndRun_MeasuresEverySize(t *testing.T) {
	cfg := smallConfig("")
	f := newFake(cfg)
	f.pendingPolls = 2
	srv := httptest.NewServer(f)
	defer srv.Close()
	cfg.URL = srv.URL
	var out bytes.Buffer
	if err := serveAndRun(cfg, "127.0.0.1:0", &out); err != nil {
		t.Fatal(err)
	}
	report := out.String()
	for _, want := range []string{"| 40 | 4 (10.0%) |", "| 80 | 8 (10.0%) |"} {
		if !strings.Contains(report, want) {
			t.Fatalf("collection line %q missing:\n%s", want, report)
		}
	}
	if row := rowFor(report, "80", "slice=rare", "`auto`"); !strings.Contains(row, "| flat+prefilter | 1.000 |") {
		t.Fatal(row)
	}
	if row := rowFor(report, "40", "none", "`hnsw`"); !strings.Contains(row, "| hnsw | 1.000 |") {
		t.Fatal(row)
	}
	perSize := 1 + len(searches(cfg.Indexes))*(cfg.Queries+cfg.Warmup)
	if len(f.searched) != 2*perSize || f.searched[0] != (search{"ivf", false}) {
		t.Fatalf("searches: %d, first %+v", len(f.searched), f.searched[0])
	}
	if strings.Join(f.deleted, ",") != "c-40,c-40,c-80,c-80" {
		t.Fatalf("each collection is cleared before loading and dropped after: %v", f.deleted)
	}
}

// -keep leaves the collections behind.
func TestMeasureSize_Keep(t *testing.T) {
	cfg := smallConfig("")
	cfg.Keep = true
	f := newFake(cfg)
	srv := httptest.NewServer(f)
	defer srv.Close()
	cfg.URL = srv.URL
	if _, err := measureSize(srv.Client(), cfg, newCorpus(cfg, 40), io.Discard); err != nil {
		t.Fatal(err)
	}
	if len(f.deleted) != 1 {
		t.Fatalf("deleted: %v", f.deleted)
	}
}

// Each way a server can refuse stops the run with the reason.
func TestMeasureSize_Failures(t *testing.T) {
	for fail, want := range map[string]string{
		"add":          "403",
		"addFailed":    `"failed": 1`,
		"addJSON":      "unexpected end",
		"status":       "indexing status",
		"statusFailed": "2 embeddings failed",
		"pending":      "still pending",
		"search:ivf":   "building ivf",
		"search:hnsw":  "hnsw: POST",
		"searchJSON":   "building ivf: unexpected end",
	} {
		t.Run(fail, func(t *testing.T) {
			cfg := smallConfig("")
			cfg.Timeout = 20 * time.Millisecond
			f := newFake(cfg)
			f.fail = fail
			srv := httptest.NewServer(f)
			defer srv.Close()
			cfg.URL = srv.URL
			_, err := measureSize(srv.Client(), cfg, newCorpus(cfg, 40), io.Discard)
			if err == nil || !strings.Contains(err.Error(), want) {
				t.Fatalf("want %q, got %v", want, err)
			}
		})
	}
}

func TestServeAndRun_Failures(t *testing.T) {
	cfg := smallConfig("http://127.0.0.1:1")
	if err := serveAndRun(cfg, "256.0.0.1:0", io.Discard); err == nil {
		t.Fatal("an address nobody can listen on")
	}
	if err := serveAndRun(cfg, "127.0.0.1:0", io.Discard); err == nil || !strings.Contains(err.Error(), "40 vectors") {
		t.Fatalf("no server to measure: %v", err)
	}
	if err := waitEmbedded(http.DefaultClient, cfg, "c"); err == nil {
		t.Fatal("no server to poll")
	}
}

func TestParseFlags(t *testing.T) {
	cfg, addr, err := parseFlags(flag.NewFlagSet("p", flag.ContinueOnError),
		[]string{"-sizes", "2000, 200000", "-dims", "64", "-embed-addr", "127.0.0.1:9", "-keep", "-indexes", "sq, ,sq4"})
	if err != nil || addr != "127.0.0.1:9" || cfg.Dims != 64 || !cfg.Keep || len(cfg.Sizes) != 2 || cfg.Sizes[1] != 200000 ||
		strings.Join(cfg.Indexes, ",") != "sq,sq4" {
		t.Fatalf("%v %q %+v", err, addr, cfg)
	}
	for _, bad := range [][]string{{"-sizes", "2k"}, {"-sizes", "50"}, {"-nope"}} {
		if _, _, err := parseFlags(flag.NewFlagSet("p", flag.ContinueOnError), bad); err == nil {
			t.Fatalf("%v accepted", bad)
		}
	}
}

// The embeddings endpoint serves whichever corpus is loaded, and refuses
// before any is.
func TestEmbedSwitch(t *testing.T) {
	e := &embedSwitch{}
	srv := httptest.NewServer(e)
	defer srv.Close()
	c := newCorpus(smallConfig(""), 40)
	post := func() int {
		resp, err := srv.Client().Post(srv.URL+"/v1/embeddings", "application/json", strings.NewReader(`{"input": ["`+c.docText(1)+`"]}`))
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		return resp.StatusCode
	}
	if code := post(); code != http.StatusServiceUnavailable {
		t.Fatalf("before a corpus: %d", code)
	}
	e.set(c.embedder)
	if code := post(); code != http.StatusOK {
		t.Fatalf("with a corpus: %d", code)
	}
}

func TestRecallAt(t *testing.T) {
	if got := recallAt([]string{docKey(1), docKey(5)}, []int{1, 2}); got != 0.5 {
		t.Fatal(got)
	}
	if recallAt(nil, nil) != 0 {
		t.Fatal("empty truth")
	}
}

// main runs from the command line and exits 1 on a bad one.
func TestMain_FromFlags(t *testing.T) {
	cfg := smallConfig("")
	f := newFake(cfg)
	srv := httptest.NewServer(f)
	defer srv.Close()
	oldArgs, oldExit, oldFlags := os.Args, exit, flag.CommandLine
	defer func() { os.Args, exit, flag.CommandLine = oldArgs, oldExit, oldFlags }()
	code := 0
	exit = func(c int) { code = c }

	flag.CommandLine = flag.NewFlagSet("planner", flag.ContinueOnError)
	os.Args = []string{"planner", "-url", srv.URL, "-embed-addr", "127.0.0.1:0", "-dims", "8", "-topics", "4",
		"-sizes", "40", "-queries", "3", "-warmup", "1", "-batch", "16"}
	main()
	if code != 0 || len(f.searched) != 1+len(searches(strings.Split(defaultIndexes, ",")))*4 {
		t.Fatalf("exit %d, searches %d", code, len(f.searched))
	}

	flag.CommandLine = flag.NewFlagSet("planner", flag.ContinueOnError)
	os.Args = []string{"planner", "-sizes", "none"}
	main()
	if code != 1 {
		t.Fatalf("a bad command line exits %d", code)
	}
}
