// Command planner measures the vector query planner (SRCH-013) before and
// after: for each collection size it searches with a fixed index — flat, the
// default, and the indexes the planner may choose — and with algorithm "auto",
// and reports recall@10 against exact ground truth, p50 and p99 latency, and
// what auto picked (from explain).
//
// It serves a synthetic clustered corpus through an OpenAI-compatible
// embeddings endpoint (benchkit), so start MDDB pointing at it, then run it:
//
//	MDDB_EMBEDDING_PROVIDER=openai MDDB_EMBEDDING_API_KEY=bench \
//	MDDB_EMBEDDING_API_URL=http://127.0.0.1:11098/v1 MDDB_EMBEDDING_DIMENSIONS=32 mddbd &
//	go run ./planner -url http://localhost:11023 -sizes 2000,200000,2000000
//
// Indexes are built before anything is timed: HNSW as the vectors arrive, IVF
// by one search that names it. The planner never builds on the query path, so
// a size measured with an index still unbuilt would measure flat.
package main

import (
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

// config is everything a run needs.
type config struct {
	URL        string
	Collection string // per-size collections are named <Collection>-<size>
	Seed       int64
	Dims       int
	Topics     int
	Queries    int
	Warmup     int // queries run and discarded before each search is timed
	Sizes      []int
	Batch      int // documents per add-batch request
	Parallel   int // add-batch requests in flight
	RareEvery  int // one document in RareEvery carries slice=rare: the selective filter
	Timeout    time.Duration
	Keep       bool // leave the collections behind
	PollPeriod time.Duration
}

func main() {
	cfg, embedAddr, err := parseFlags(flag.CommandLine, os.Args[1:])
	if err == nil {
		err = serveAndRun(cfg, embedAddr, os.Stdout)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "planner:", err)
		exit(1)
	}
}

// exit is os.Exit, swapped in tests.
var exit = os.Exit

// parseFlags reads the command line into a config and the embeddings address.
func parseFlags(fs *flag.FlagSet, args []string) (config, string, error) {
	cfg := config{PollPeriod: time.Second}
	fs.StringVar(&cfg.URL, "url", "http://localhost:11023", "MDDB base URL")
	fs.StringVar(&cfg.Collection, "collection", "planner-bench", "collection name prefix")
	fs.Int64Var(&cfg.Seed, "seed", 1, "corpus seed")
	fs.IntVar(&cfg.Dims, "dims", 32, "vector dimensions (match MDDB_EMBEDDING_DIMENSIONS)")
	fs.IntVar(&cfg.Topics, "topics", 100, "topics (clusters) in the corpus")
	fs.IntVar(&cfg.Queries, "queries", 100, "timed queries per search")
	fs.IntVar(&cfg.Warmup, "warmup", 5, "untimed queries before each search")
	fs.IntVar(&cfg.Batch, "batch", 1000, "documents per add-batch request")
	fs.IntVar(&cfg.Parallel, "parallel", 4, "add-batch requests in flight")
	fs.IntVar(&cfg.RareEvery, "rare-every", 50, "one document in N matches the selective filter (50 = 2%)")
	fs.DurationVar(&cfg.Timeout, "timeout", 30*time.Minute, "per request, and for the embeddings to drain")
	fs.BoolVar(&cfg.Keep, "keep", false, "keep the collections after measuring")
	sizes := fs.String("sizes", "2000,200000,2000000", "collection sizes, comma separated")
	embedAddr := fs.String("embed-addr", "127.0.0.1:11098", "address to serve embeddings on")
	if err := fs.Parse(args); err != nil {
		return cfg, "", err
	}
	for _, s := range strings.Split(*sizes, ",") {
		n, err := strconv.Atoi(strings.TrimSpace(s))
		if err != nil || n < cfg.Topics {
			return cfg, "", fmt.Errorf("size %q: want a whole number of at least -topics (%d)", s, cfg.Topics)
		}
		cfg.Sizes = append(cfg.Sizes, n)
	}
	return cfg, *embedAddr, nil
}

// serveAndRun serves the corpus's embeddings on addr while every size is
// loaded and measured, then prints the report.
func serveAndRun(cfg config, addr string, out io.Writer) error {
	emb := &embedSwitch{}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	srv := &http.Server{Handler: emb, ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = srv.Serve(ln) }()
	defer func() { _ = srv.Close() }()

	client := &http.Client{Timeout: cfg.Timeout, Transport: &http.Transport{MaxIdleConnsPerHost: cfg.Parallel + 1}}
	var sizes []sizeResult
	for _, n := range cfg.Sizes {
		c := newCorpus(cfg, n)
		emb.set(c.embedder)
		r, err := measureSize(client, cfg, c, os.Stderr)
		if err != nil {
			return fmt.Errorf("%d vectors: %w", n, err)
		}
		sizes = append(sizes, r)
	}
	report(out, cfg, sizes)
	return nil
}
