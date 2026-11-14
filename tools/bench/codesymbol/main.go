// Command codesymbol measures code_symbol with and without semantic facts
// against a language server's own answers (CODE-013).
//
// The question is the one an agent asks before a refactor: where are all the
// places to change? Two collections are made from the same tree, one with
// `mddb-cli autoindex` and one with `autoindex --semantic`. The truth comes
// from the language server directly (lspindex's local test, see truth.go).
// For a fixed-seed sample of definitions — functions, methods whose bare
// name is shared, interfaces — the tool asks both collections
// /v1/symbol?name=<bare>&path=<file>&include=references,implementations and
// scores the references (document and line) against the truth: precision,
// recall, implementation counts, response size (4 bytes a token, the
// server's estimator) and latency. It also measures the facts' bytes per
// document (-meta) and checks that FTS and hybrid search answer both
// collections alike (-queries).
//
//	go run ./codesymbol -url http://127.0.0.1:11023 -heuristic go-heur \
//	  -semantic go-sem -truth go-truth.json -queries evalsets/agent-tasks.json
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// report is the tool's JSON output.
type report struct {
	Server  string         `json:"server"`
	Seed    int64          `json:"seed"`
	Sampled int            `json:"sampled"`
	Groups  []group        `json:"groups"`
	Meta    *metaStats     `json:"meta,omitempty"`
	Search  []searchDiff   `json:"search,omitempty"`
	Symbols []symbolResult `json:"symbols"`
}

// options are the command's flags.
type options struct {
	url, heuristic, semantic, truth string
	baseline                        string
	queries                         []string
	n, repeat, limit, topK          int
	seed                            int64
	meta                            bool
	out                             string
}

// exit is os.Exit, replaced in tests.
var exit = os.Exit

func main() {
	exit(cli(os.Args[1:], os.Stdout, os.Stderr))
}

// cli parses flags, runs the benchmark and returns the exit code.
func cli(args []string, stdout, stderr io.Writer) int {
	var o options
	var queries string
	fl := flag.NewFlagSet("codesymbol", flag.ContinueOnError)
	fl.SetOutput(stderr)
	fl.StringVar(&o.url, "url", "http://localhost:11023", "MDDB base URL")
	fl.StringVar(&o.heuristic, "heuristic", "", "Collection made with mddb-cli autoindex")
	fl.StringVar(&o.semantic, "semantic", "", "Collection made from the same tree with autoindex --semantic")
	fl.StringVar(&o.truth, "truth", "", "Ground truth: the JSON TestRealDir writes with LSPREAL_OUT")
	fl.IntVar(&o.n, "n", 50, "Symbols to sample")
	fl.Int64Var(&o.seed, "seed", 13, "Sampling seed")
	fl.IntVar(&o.repeat, "repeat", 5, "Times each lookup runs, for the latency")
	fl.IntVar(&o.limit, "limit", 200, "code_symbol limit per relation")
	fl.BoolVar(&o.meta, "meta", true, "Measure the semantic collection's sym* bytes per document")
	fl.StringVar(&queries, "queries", "", "Comma-separated evaluation files whose queries must answer both collections alike")
	fl.IntVar(&o.topK, "top-k", 10, "Results per regression search")
	fl.StringVar(&o.baseline, "baseline", "", "With -queries: also compare the heuristic collection with the same collection on this server (an older mddbd)")
	fl.StringVar(&o.out, "out", "", "Write the full JSON report here")
	if err := fl.Parse(args); err != nil {
		return 2
	}
	if queries != "" {
		o.queries = strings.Split(queries, ",")
	}
	rep, err := run(&http.Client{Timeout: 2 * time.Minute}, o)
	if err == nil && o.out != "" {
		b, _ := json.MarshalIndent(rep, "", "  ")
		err = os.WriteFile(o.out, b, 0o600)
	}
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	printTable(stdout, rep)
	return 0
}

// run samples the truth, measures both collections and compares searches.
func run(client *http.Client, o options) (*report, error) {
	if o.heuristic == "" || o.semantic == "" || o.truth == "" {
		return nil, errors.New("-heuristic, -semantic and -truth are required")
	}
	server, all, err := loadTruth(o.truth)
	if err != nil {
		return nil, err
	}
	syms := sample(all, o.n, o.seed)
	rs, err := measure(client, o.url, o.heuristic, o.semantic, syms, o.limit, o.repeat)
	if err != nil {
		return nil, err
	}
	rep := &report{Server: server, Seed: o.seed, Sampled: len(rs), Groups: groups(rs), Symbols: rs}
	if o.meta {
		st, err := factBytes(client, o.url, o.semantic)
		if err != nil {
			return nil, err
		}
		rep.Meta = &st
	}
	if len(o.queries) > 0 {
		qs, err := loadQueries(o.queries)
		if err != nil {
			return nil, err
		}
		heur := side{o.url, o.heuristic}
		pairs := [][2]side{{heur, {o.url, o.semantic}}}
		if o.baseline != "" {
			pairs = append(pairs, [2]side{{o.baseline, o.heuristic}, heur})
		}
		for _, pair := range pairs {
			for _, ep := range []string{"/v1/fts", "/v1/hybrid-search"} {
				rep.Search = append(rep.Search, compareSearch(client, pair[0], pair[1], ep, qs, o.topK))
			}
		}
	}
	return rep, nil
}
