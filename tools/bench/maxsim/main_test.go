package main

import (
	"bytes"
	"flag"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fixture writes a small corpus and evaluation set and returns a config
// pointing at a fake server.
func fixture(t *testing.T, f *fakeMDDB) (config, *http.Client) {
	t.Helper()
	dir := t.TempDir()
	docs := map[string]string{
		"BACKUP.md":      "# Backup\n\ntake a backup of the database\n\nrestore a snapshot",
		"REPLICATION.md": "# Replication\n\nset up a read replica\n\nthe follower follows the leader",
		"TLS.md":         "# TLS\n\nserve the API over HTTPS\n\nbring your own certificate",
		"OTHER.md":       "# Other\n\nnothing about the database or the leader",
	}
	for name, body := range docs {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	set := `{"queries": [
	 {"id": "backup", "query": "take a backup of the database and restore it", "expectedKeys": ["BACKUP.md"], "grades": {"BACKUP.md": 3}},
	 {"id": "replica", "query": "set up a read replica that follows the leader", "expectedKeys": ["REPLICATION.md"]},
	 {"id": "tls", "query": "serve the API over HTTPS with my own certificate", "expectedKeys": ["TLS.md"], "grades": {"TLS.md": 3}}]}`
	setPath := filepath.Join(dir, "set.json")
	if err := os.WriteFile(setPath, []byte(set), 0o600); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	return config{URL: srv.URL, Collection: "docs", DocsDir: dir, Load: true, EvalSet: setPath, K: 10,
		ChunkTopK: 1000, Window: 4, Stride: 2, Boot: 200, Seed: 1, WaitTries: 2}, srv.Client()
}

func TestRunPrintsBothSets(t *testing.T) {
	f := newFake()
	cfg, client := fixture(t, f)
	var out bytes.Buffer
	if err := run(client, cfg, &out); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, want := range []string{"### plain: 3 queries", "### multi-topic: 3 queries", "vector (server)",
		"hybrid (server)", "max, whole query", "maxsim, whole + 4-word windows",
		"vector, decomposition auto (server)", "hybrid, decomposition auto (server)", "best part, whole + clauses ≤4 (= auto)",
		"rrf, whole + clauses ≤4", "agrees with the server's vector top 10 on 3 of 3",
		"(server)", "(round trips)", "11 chunks"} {
		if !strings.Contains(got, want) {
			t.Errorf("output lacks %q:\n%s", want, got)
		}
	}
	if f.adds != 4 {
		t.Errorf("%d documents added, want 4", f.adds)
	}
}

func TestRunFailures(t *testing.T) {
	cases := map[string]func(*fakeMDDB, *config){
		"load":    func(f *fakeMDDB, _ *config) { f.fail = "/v1/add" },
		"wait":    func(f *fakeMDDB, _ *config) { f.pending = true },
		"set":     func(_ *fakeMDDB, c *config) { c.EvalSet = "missing.json" },
		"eval":    func(f *fakeMDDB, _ *config) { f.fail = "/v1/eval" },
		"evalErr": func(f *fakeMDDB, _ *config) { f.evalError = "no provider" },
		"short":   func(f *fakeMDDB, _ *config) { f.shortEval = true },
		"evalBad": func(f *fakeMDDB, _ *config) { f.badJSON = "/v1/eval" },
		"topk":    func(_ *fakeMDDB, c *config) { c.ChunkTopK = 2 },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			f := newFake()
			cfg, client := fixture(t, f)
			mutate(f, &cfg)
			if err := run(client, cfg, &bytes.Buffer{}); err == nil {
				t.Fatal("run succeeded")
			}
		})
	}
}

func TestMainRunsWithFlags(t *testing.T) {
	f := newFake()
	cfg, _ := fixture(t, f)
	oldArgs, oldFlags, oldOut := os.Args, flag.CommandLine, os.Stdout
	defer func() { os.Args, flag.CommandLine, os.Stdout = oldArgs, oldFlags, oldOut }()
	flag.CommandLine = flag.NewFlagSet("maxsim", flag.ContinueOnError)
	devNull, err := os.Create(filepath.Join(t.TempDir(), "out"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = devNull.Close() }()
	os.Stdout = devNull
	os.Args = []string{"maxsim", "-url", cfg.URL, "-load", "-docs", cfg.DocsDir, "-queries", cfg.EvalSet, "-bootstrap", "50"}
	main()
	if f.adds != 4 {
		t.Errorf("%d documents added, want 4", f.adds)
	}
}

func TestMainExitsOnError(t *testing.T) {
	oldArgs, oldFlags, oldExit := os.Args, flag.CommandLine, exit
	defer func() { os.Args, flag.CommandLine, exit = oldArgs, oldFlags, oldExit }()
	flag.CommandLine = flag.NewFlagSet("maxsim", flag.ContinueOnError)
	code := 0
	exit = func(c int) { code = c }
	os.Args = []string{"maxsim", "-url", "http://127.0.0.1:1", "-wait-tries", "1"}
	main()
	if code != 1 {
		t.Errorf("exit code %d, want 1", code)
	}
}
