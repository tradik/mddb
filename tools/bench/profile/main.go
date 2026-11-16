// Command profile is the reproducible performance, recall and recovery
// harness (TEST-005). It starts its own mddbd processes on free loopback
// ports, loads a corpus drawn from a seed, and measures:
//
//   - search: full text, every vector index by name and the planner's choice,
//     hybrid, and vector search behind filters admitting 1–50% of the corpus —
//     p50/p95/p99, QPS and recall@10 against the exact answer;
//   - ingest: single and batched writes, without embedding and with the
//     built-in lexical provider — documents per second;
//   - mixed: searches and writes on one collection at once;
//   - start-up: a clean restart to healthy and to every vector index
//     answering, and peak resident memory;
//   - recovery: kill -9 under write, then restart and read back every
//     acknowledged write; a follower's lag under load and its catch-up after
//     being down.
//
// The result is one JSON file with the machine and the build in it:
//
//	go run ./profile -mddbd ../../services/mddbd/mddbd -profile small -out ../../docs/benchmarks/2.19.7-small.json
//	go run ./profile -in run.json -baseline ../../docs/benchmarks/2.19.7-small.json   # the regression gate
//	go run ./profile -sync-docs ../../docs/BENCHMARK.md -results ../../docs/benchmarks
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// config is everything one invocation needs.
type config struct {
	Profile  profile
	Seed     int64
	Bin      string // the mddbd to measure
	Out      string // where the result goes
	In       string // an existing result instead of a run
	Baseline string // compare against it
	Commit   string
	WorkDir  string // parent of the scratch directory
	Keep     bool   // leave the scratch directory behind
	Only     map[string]bool
	SyncDocs string // rewrite this document's blocks and exit
	Results  string // the directory of result files, for -sync-docs
	Markdown bool
	Limits   thresholds
}

func main() {
	// Ctrl-C, or a cancelled CI job, must not leave servers behind.
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-stop
		killRunning()
		exit(130)
	}()
	cfg, err := parseFlags(flag.CommandLine, os.Args[1:])
	if err == nil {
		err = execute(cfg, os.Stdout, os.Stderr)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "profile:", err)
		exit(exitCode(err))
	}
}

// errLatencyOnly marks a gate whose every failure is a p95 limit. The run
// exits with exitLatencyOnly for it, so CI can measure once more before it
// blames the change for what may be a noisy shared runner.
var errLatencyOnly = errors.New("latency only")

const exitLatencyOnly = 3

// exitCode is the process exit status for err.
func exitCode(err error) int {
	if errors.Is(err, errLatencyOnly) {
		return exitLatencyOnly
	}
	return 1
}

// exit is os.Exit, swapped in tests.
var exit = os.Exit

// parseFlags reads the command line.
func parseFlags(fs *flag.FlagSet, args []string) (config, error) {
	cfg := config{Limits: defaultThresholds}
	name := fs.String("profile", "small", "small (20k vectors) or full (200k)")
	vectors := fs.Int("vectors", 0, "override the profile's corpus size (full with 2000000 is the largest run)")
	dims := fs.Int("dims", 0, "override the profile's vector dimensions")
	only := fs.String("only", "", "comma separated sections to run: "+strings.Join(sections, ", ")+" (default all)")
	fs.Int64Var(&cfg.Seed, "seed", 1, "corpus seed")
	fs.StringVar(&cfg.Bin, "mddbd", "", "path of the mddbd binary to measure")
	fs.StringVar(&cfg.Out, "out", "", "write the result to this JSON file")
	fs.StringVar(&cfg.In, "in", "", "read a result file instead of running")
	fs.StringVar(&cfg.Baseline, "baseline", "", "compare with this result file; regressions fail")
	fs.StringVar(&cfg.Commit, "commit", "unknown", "commit of the binary, recorded in the result")
	fs.StringVar(&cfg.WorkDir, "work-dir", "", "where the scratch data directory is made (default the system's)")
	fs.BoolVar(&cfg.Keep, "keep", false, "keep the scratch directory and the server logs")
	fs.StringVar(&cfg.SyncDocs, "sync-docs", "", "rewrite the result blocks of this Markdown file and exit")
	fs.StringVar(&cfg.Results, "results", "", "directory of result files, for -sync-docs")
	fs.BoolVar(&cfg.Markdown, "markdown", false, "print the result as Markdown")
	fs.Float64Var(&cfg.Limits.P95Regression, "max-p95-regression", cfg.Limits.P95Regression, "allowed p95 growth against the baseline, as a fraction")
	fs.Float64Var(&cfg.Limits.RecallDrop, "max-recall-drop", cfg.Limits.RecallDrop, "allowed recall@10 loss against the baseline")
	fs.BoolVar(&cfg.Limits.Raw, "raw-p95", false, "compare raw p95 even when the baseline is from another machine")
	if err := fs.Parse(args); err != nil {
		return cfg, err
	}
	var err error
	if cfg.Profile, err = profileNamed(*name, *vectors, *dims); err != nil {
		return cfg, err
	}
	if cfg.Only, err = parseSections(*only); err != nil {
		return cfg, err
	}
	switch {
	case cfg.SyncDocs != "" && cfg.Results == "":
		return cfg, errors.New("-sync-docs needs -results")
	case cfg.SyncDocs == "" && cfg.In == "" && cfg.Bin == "":
		return cfg, errors.New("name the server to measure with -mddbd, or a result with -in")
	}
	return cfg, nil
}

// execute does what the flags ask: sync the document, or obtain a result (by
// running or reading), store it, print it and gate it.
func execute(cfg config, out, log io.Writer) error {
	if cfg.SyncDocs != "" {
		return syncDocs(cfg.SyncDocs, cfg.Results)
	}
	// The baseline is read first: a missing one fails before a long run, and
	// -out naming the baseline's own file cannot turn the gate into a
	// comparison of the run with itself.
	var base *Result
	var err error
	if cfg.Baseline != "" {
		if base, err = readResult(cfg.Baseline); err != nil {
			return err
		}
	}
	var res *Result
	if cfg.In != "" {
		res, err = readResult(cfg.In)
	} else {
		res, err = measureAll(cfg, log)
	}
	if err != nil {
		return err
	}
	if cfg.Out != "" {
		if err := writeResult(cfg.Out, res); err != nil {
			return err
		}
		_, _ = fmt.Fprintln(log, "result written to", cfg.Out)
	}
	if cfg.Markdown {
		_, _ = fmt.Fprint(out, render(res))
	}
	if base == nil {
		return nil
	}
	return gate(compare(base, res, cfg.Limits), cfg.Baseline, out)
}

// gate prints a comparison and fails when it has failures.
func gate(c comparison, baseline string, out io.Writer) error {
	_, _ = fmt.Fprintf(out, "compared with %s (%s)\n", baseline, c.Mode)
	for _, n := range c.Notes {
		_, _ = fmt.Fprintln(out, "  note:", n)
	}
	for _, f := range c.Failures {
		_, _ = fmt.Fprintln(out, "  FAIL:", f)
	}
	if len(c.Failures) > 0 && c.Latency == len(c.Failures) {
		return fmt.Errorf("%d p95 regression(s) against the baseline: %w", len(c.Failures), errLatencyOnly)
	}
	if len(c.Failures) > 0 {
		return fmt.Errorf("%d regression(s) against the baseline", len(c.Failures))
	}
	_, _ = fmt.Fprintln(out, "  no regression")
	return nil
}

// serverVersion asks the binary what it is.
func serverVersion(bin string) (string, error) {
	raw, err := exec.Command(bin, "--version").Output() // #nosec G204 -- the server binary the operator named with -mddbd
	if err != nil {
		return "", fmt.Errorf("%s --version: %w", bin, err)
	}
	fields := strings.Fields(string(raw))
	if len(fields) == 0 {
		return "", fmt.Errorf("%s --version printed nothing", bin)
	}
	return fields[len(fields)-1], nil
}

// measureAll runs the chosen sections in a scratch directory.
func measureAll(cfg config, log io.Writer) (*Result, error) {
	version, err := serverVersion(cfg.Bin)
	if err != nil {
		return nil, err
	}
	root, err := os.MkdirTemp(cfg.WorkDir, "bench-profile-")
	if err != nil {
		return nil, err
	}
	// Every node runs in its own directory, so a relative -work-dir would
	// name a different place for each of them.
	if root, err = filepath.Abs(root); err != nil {
		return nil, err
	}
	if !cfg.Keep {
		defer func() { _ = os.RemoveAll(root) }()
	}
	p := cfg.Profile
	res := &Result{
		Schema: schemaVersion, Version: version, Commit: cfg.Commit, Profile: p.Name, Seed: cfg.Seed,
		RecordedAt: time.Now().UTC().Format(time.RFC3339), Environment: environment(), CalibrationMs: round3(calibrate()),
		Corpus: CorpusInfo{Vectors: p.Vectors / p.Topics * p.Topics, Dims: p.Dims, Topics: p.Topics,
			Queries: p.Queries, Rounds: p.Rounds, Clients: p.Clients},
	}
	_, _ = fmt.Fprintf(log, "mddbd %s, profile %s, scratch %s\n", version, p.Name, root)
	steps := []struct {
		section string
		run     func() error
	}{
		{"search", func() error { return searchSection(cfg, root, res, log) }},
		{"ingest", func() error { return ingestSection(cfg, root, res, log) }},
		{"recovery", func() (err error) { res.Recovery, err = crash(cfg.Bin, root, p); return }},
		{"replication", func() (err error) { res.Replication, err = replication(cfg.Bin, root, p); return }},
	}
	for _, step := range steps {
		if !cfg.Only[step.section] {
			continue
		}
		_, _ = fmt.Fprintf(log, "%s…\n", step.section)
		start := time.Now()
		if err := step.run(); err != nil {
			return nil, fmt.Errorf("%s: %w", step.section, err)
		}
		_, _ = fmt.Fprintf(log, "%s done in %.1f s\n", step.section, time.Since(start).Seconds())
	}
	return res, nil
}
