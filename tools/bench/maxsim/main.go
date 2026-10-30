// Command maxsim is the SRCH-004 spike: does late-interaction scoring over
// the chunks MDDB already stores rank documents better than today's
// single-vector max?
//
// MDDB embeds a document as one vector per chunk (keys "docID#N") and scores
// it by its best chunk against one whole-query vector. MaxSim instead splits
// the query into parts, embeds each, and scores a document by the mean over
// the parts of each part's best chunk — ColBERT's MaxSim with query parts for
// tokens and chunks for document tokens. No new storage, only a different
// aggregation.
//
// Nothing in the server changes. The tool reads every chunk's similarity to a
// query part through the existing API (vector search, retrievalMode "chunk",
// flat index, topK above the chunk count) and aggregates client-side. The
// baselines — vector and hybrid search as the server ranks them — come from
// POST /v1/eval with per-query details.
//
// SRCH-020 added the server's queryDecomposition "auto" to those baselines
// (vector and hybrid, each compared with itself without it), and two client
// rows that compare max and RRF fusion over the whole query and its parts.
//
// Start a server with an embedding provider (the spike used Ollama's
// nomic-embed-text) and the embedding cache off, so latency counts every
// embedding call, then:
//
//	MDDB_EMBEDDING_PROVIDER=ollama MDDB_EMBEDDING_CACHE_SIZE=0 mddbd &
//	go run ./maxsim -url http://localhost:11023 -load -docs ../../docs
package main

import (
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

// config is everything a run needs.
type config struct {
	URL        string
	Collection string
	DocsDir    string // loaded when Load is set: one document per *.md, keyed by file name
	Load       bool
	EvalSet    string
	K          int
	ChunkTopK  int // must exceed the collection's chunk count, so every chunk is scored
	Window     int // words per window part
	Stride     int // words between window starts
	Boot       int // bootstrap resamples
	Seed       int64
	WaitTries  int // 30 s waits for the embeddings to drain
}

// exit is os.Exit, replaced in tests.
var exit = os.Exit

func main() {
	cfg := config{}
	flag.StringVar(&cfg.URL, "url", "http://localhost:11023", "MDDB base URL")
	flag.StringVar(&cfg.Collection, "collection", "docs", "collection holding the documentation")
	flag.StringVar(&cfg.DocsDir, "docs", "../../docs", "directory of *.md pages, loaded with -load")
	flag.BoolVar(&cfg.Load, "load", false, "(re)create the collection from -docs first")
	flag.StringVar(&cfg.EvalSet, "queries", "evalsets/docs.json", "evaluation set")
	flag.IntVar(&cfg.K, "k", 10, "cut-off for recall and nDCG")
	flag.IntVar(&cfg.ChunkTopK, "chunk-topk", 20000, "chunks asked per query part; above the chunk count")
	flag.IntVar(&cfg.Window, "window", 4, "words per window part")
	flag.IntVar(&cfg.Stride, "stride", 2, "words between window starts")
	flag.IntVar(&cfg.Boot, "bootstrap", 2000, "bootstrap resamples for the confidence intervals")
	flag.Int64Var(&cfg.Seed, "seed", 1, "bootstrap seed")
	flag.IntVar(&cfg.WaitTries, "wait-tries", 40, "30-second waits for indexing before giving up")
	flag.Parse()
	if err := run(&http.Client{Timeout: 120 * time.Second}, cfg, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "maxsim:", err)
		exit(1)
	}
}

// run loads the corpus if asked, measures the plain and the multi-topic set,
// and prints both.
func run(client *http.Client, cfg config, out io.Writer) error {
	if cfg.Load {
		if err := loadDocs(client, cfg); err != nil {
			return err
		}
	}
	if err := waitIndexed(client, cfg); err != nil {
		return err
	}
	plain, err := readEvalSet(cfg.EvalSet)
	if err != nil {
		return err
	}
	sets := []querySet{{Name: "plain", Queries: plain}, {Name: "multi-topic", Queries: pairQueries(plain)}}
	stats := &chunkStats{}
	var results []setResult
	for _, s := range sets {
		r, err := measureSet(client, cfg, s, stats)
		if err != nil {
			return fmt.Errorf("%s: %w", s.Name, err)
		}
		results = append(results, r)
	}
	report(out, cfg, stats, results)
	return nil
}
