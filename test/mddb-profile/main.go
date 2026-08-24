// Command mddb-profile measures the numbers docs/COMPARISON.md publishes.
//
// DOC-013: the comparison page carried a table of throughput and memory
// figures with no methodology, no date and no way to reproduce them. Some had
// drifted from anything the code could produce — the same page claimed a
// ~29MB binary and a 15MB Docker image, while the site's own build advertised
// ~26MB.
//
// This measures one system: MDDB. Cross-database figures come from the clients
// beside this one (mysql/, postgres/, mongodb/, couchdb/), which need their
// containers running; test/compare-all-databases.sh drives all of them.
// Nothing here fabricates a number for a system it did not run.
//
// Usage:
//
//	go run ./mddb-profile -addr http://localhost:11023 -docs 5000
//	go run ./mddb-profile -markdown   # a table ready to paste into the docs
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"os"
	"runtime"
	"sort"
	"strings"
	"time"
)

func main() {
	addr := flag.String("addr", "http://localhost:11023", "MDDB base URL")
	collection := flag.String("collection", "bench-profile", "collection to write into")
	docs := flag.Int("docs", 5000, "documents to ingest")
	batch := flag.Int("batch", 500, "documents per batch request")
	queries := flag.Int("queries", 500, "search queries to time")
	words := flag.Int("words", 160, "words per document — throughput depends on it, so it is a flag and not a constant")
	markdown := flag.Bool("markdown", false, "print a Markdown table instead of plain text")
	keep := flag.Bool("keep", false, "keep the collection afterwards")
	flag.Parse()

	client := &http.Client{Timeout: 60 * time.Second}

	if err := waitReady(client, *addr); err != nil {
		fmt.Fprintf(os.Stderr, "cannot reach MDDB at %s: %v\n", *addr, err)
		os.Exit(1)
	}

	r := newRun(*addr, *collection, *docs, *batch, *queries, *words)

	if err := r.ingest(client); err != nil {
		fmt.Fprintf(os.Stderr, "ingest: %v\n", err)
		os.Exit(1)
	}
	if err := r.searchLatency(client); err != nil {
		fmt.Fprintf(os.Stderr, "search: %v\n", err)
		os.Exit(1)
	}
	r.footprint(client)

	if !*keep {
		_ = post(client, *addr+"/v1/delete-collection",
			map[string]string{"collection": *collection}, nil)
	}

	if *markdown {
		r.printMarkdown()
		return
	}
	r.printPlain()
}

// run holds one measurement pass.
type run struct {
	Addr       string
	Collection string
	Docs       int
	Batch      int
	Queries    int
	// Words per document. Throughput is dominated by bytes written, so a
	// figure published without it means nothing: the same server does an
	// order of magnitude more documents per second when they are one line
	// each.
	Words int
	// DocBytes is the average body size actually produced.
	DocBytes int

	// Measured.
	IngestPerSecond float64
	IngestSeconds   float64
	SearchP50       time.Duration
	SearchP95       time.Duration
	SearchP99       time.Duration
	DatabaseBytes   int64
	// ResidentBytes is the server process's RSS after the corpus is loaded and
	// the queries have run — what the process actually holds, not what it
	// allocated at some point.
	ResidentBytes int64
	ServerVersion string

	// Environment, recorded so a published number can be read in context.
	GoVersion  string
	OS         string
	Arch       string
	CPUs       int
	MeasuredAt time.Time
}

func newRun(addr, collection string, docs, batch, queries, words int) *run {
	return &run{
		Addr: addr, Collection: collection,
		Docs: docs, Batch: batch, Queries: queries, Words: words,
		GoVersion: runtime.Version(), OS: runtime.GOOS,
		Arch: runtime.GOARCH, CPUs: runtime.NumCPU(),
		MeasuredAt: time.Now().UTC(),
	}
}

// --- measurements ---

// ingest writes Docs documents in batches and reports sustained throughput.
//
// Batch rather than one-at-a-time: a per-document HTTP round trip measures the
// loopback interface more than it measures the database, and the batch API is
// what anyone loading a corpus actually uses.
func (r *run) ingest(client *http.Client) error {
	type batchDoc struct {
		Key       string              `json:"key"`
		Lang      string              `json:"lang"`
		Meta      map[string][]string `json:"meta,omitempty"`
		ContentMD string              `json:"contentMd"`
	}

	r.DocBytes = len(document(0, r.Words))

	start := time.Now()
	written := 0

	for written < r.Docs {
		size := r.Batch
		if written+size > r.Docs {
			size = r.Docs - written
		}

		batch := make([]batchDoc, 0, size)
		for i := 0; i < size; i++ {
			n := written + i
			batch = append(batch, batchDoc{
				Key:       fmt.Sprintf("doc-%06d", n),
				Lang:      "en",
				Meta:      map[string][]string{"kind": {"prose"}, "shard": {fmt.Sprintf("%d", n%16)}},
				ContentMD: document(n, r.Words),
			})
		}

		if err := post(client, r.Addr+"/v1/add-batch", map[string]any{
			"collection": r.Collection,
			"documents":  batch,
		}, nil); err != nil {
			return fmt.Errorf("batch at %d: %w", written, err)
		}
		written += size
	}

	r.IngestSeconds = time.Since(start).Seconds()
	if r.IngestSeconds > 0 {
		r.IngestPerSecond = float64(r.Docs) / r.IngestSeconds
	}
	return nil
}

// searchLatency times full-text queries and reports percentiles.
//
// Percentiles, not an average: an average hides the tail, and the tail is what
// a user notices. p95 is the number worth publishing.
func (r *run) searchLatency(client *http.Client) error {
	terms := []string{
		"deployment", "certificate", "throughput", "migration", "rollback",
		"latency", "quorum", "checkpoint", "namespace", "retention",
	}

	// Warm up: the first queries pay for index pages that are not yet in the
	// page cache, which is a property of the first query and not of the
	// system.
	for i := 0; i < 20; i++ {
		_ = post(client, r.Addr+"/v1/fts", map[string]any{
			"collection": r.Collection, "query": terms[i%len(terms)], "limit": 10,
		}, nil)
	}

	samples := make([]time.Duration, 0, r.Queries)
	for i := 0; i < r.Queries; i++ {
		q := terms[i%len(terms)]
		start := time.Now()
		if err := post(client, r.Addr+"/v1/fts", map[string]any{
			"collection": r.Collection, "query": q, "limit": 10,
		}, nil); err != nil {
			return fmt.Errorf("query %q: %w", q, err)
		}
		samples = append(samples, time.Since(start))
	}

	sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })
	r.SearchP50 = percentile(samples, 0.50)
	r.SearchP95 = percentile(samples, 0.95)
	r.SearchP99 = percentile(samples, 0.99)
	return nil
}

// footprint records what the server reports about itself.
func (r *run) footprint(client *http.Client) {
	var stats struct {
		DatabaseSize int64 `json:"databaseSize"`
	}
	if err := get(client, r.Addr+"/v1/stats", &stats); err == nil {
		r.DatabaseBytes = stats.DatabaseSize
	}

	var health struct {
		Version string `json:"version"`
	}
	_ = get(client, r.Addr+"/health", &health)
	r.ServerVersion = health.Version

	r.ResidentBytes = residentBytes()
}

// residentBytes reads the RSS of the mddbd process this run is talking to.
//
// Read from /proc rather than reported by the server, because a process
// cannot see its own resident size the way the kernel accounts for it — Go's
// runtime metrics describe the heap, not the pages the OS is holding.
// Returns 0 where /proc is unavailable or no single mddbd is running, which
// is honest: a missing measurement should read as missing.
func residentBytes() int64 {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return 0
	}

	var found int64
	var matches int
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		// Match the executable name exactly, not the command line. A
		// substring search over cmdline also matched the shell that launched
		// this tool, because its own command line mentions the binary — so
		// the count came out above one and every run reported "not measured".
		comm, err := os.ReadFile("/proc/" + e.Name() + "/comm") // #nosec G304
		if err != nil || strings.TrimSpace(string(comm)) != "mddbd-release" &&
			strings.TrimSpace(string(comm)) != "mddbd" {
			continue
		}
		status, err := os.ReadFile("/proc/" + e.Name() + "/status") // #nosec G304
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(status), "\n") {
			if !strings.HasPrefix(line, "VmRSS:") {
				continue
			}
			var kb int64
			if _, err := fmt.Sscanf(strings.TrimPrefix(line, "VmRSS:"), "%d kB", &kb); err == nil {
				found = kb * 1024
				matches++
			}
		}
	}
	if matches != 1 {
		// Several mddbd processes, or none: attributing memory to one of them
		// would be a guess.
		return 0
	}
	return found
}

func percentile(sorted []time.Duration, p float64) time.Duration {
	if len(sorted) == 0 {
		return 0
	}
	idx := int(float64(len(sorted)-1) * p)
	return sorted[idx]
}

// The corpus is generated rather than shipped, and both its vocabulary size
// and its term distribution decide every figure this tool reports.
//
// Two earlier fixtures were wrong in the same way. Fifteen words repeated gave
// almost no distinct terms and a database ten times its content. Then 250
// words drawn uniformly gave the opposite pathology — every document contains
// nearly every term, which is the densest an inverted index can possibly be,
// and the database came out twenty-seven times its content. Neither number
// says anything about the engine.
//
// Natural language is Zipfian: the commonest word appears about twice as often
// as the second, three times as often as the third, and the tail is enormous
// and mostly unique. That distribution is what an inverted index is built for,
// so a corpus that does not have it measures the wrong thing.
const vocabularySize = 8000

var (
	vocabulary  = buildVocabulary(vocabularySize)
	zipfWeights = buildZipfWeights(vocabularySize)
)

func buildVocabulary(n int) []string {
	stems := []string{
		"deploy", "certificate", "throughput", "migrate", "rollback", "latency",
		"quorum", "checkpoint", "namespace", "retention", "replicate",
		"partition", "schedule", "telemetry", "provision", "cluster", "node",
		"index", "shard", "buffer", "cache", "commit", "transaction", "backup",
		"restore", "snapshot", "encrypt", "decrypt", "signature", "token",
		"session", "request", "response", "handler", "endpoint", "gateway",
		"proxy", "balancer", "failover", "replica",
	}
	suffixes := []string{"", "s", "ing", "ed", "er", "ion", "able", "ment"}

	words := make([]string, 0, n)
	for _, stem := range stems {
		for _, suffix := range suffixes {
			words = append(words, stem+suffix)
		}
	}
	// The long tail: terms that appear in a handful of documents, which is
	// most of a real vocabulary and most of an inverted index's size.
	for i := len(words); i < n; i++ {
		words = append(words, fmt.Sprintf("%s%d", stems[i%len(stems)], i))
	}
	return words[:n]
}

// buildZipfWeights precomputes the cumulative distribution for rank-ordered
// term frequencies with the exponent set to 1, which is what English is
// usually measured at.
func buildZipfWeights(n int) []float64 {
	weights := make([]float64, n)
	var total float64
	for i := 0; i < n; i++ {
		total += 1.0 / float64(i+1)
		weights[i] = total
	}
	for i := range weights {
		weights[i] /= total
	}
	return weights
}

// zipfWord draws a term by its rank, so common words dominate and the tail is
// sparse — the shape an inverted index is designed around.
func zipfWord(u float64) string {
	idx := sort.SearchFloat64s(zipfWeights, u)
	if idx >= len(vocabulary) {
		idx = len(vocabulary) - 1
	}
	return vocabulary[idx]
}

// document builds a body with enough vocabulary that full-text search has
// something to discriminate on. Deterministic per index, so two runs on the
// same corpus size are comparable.
func document(n, words int) string {
	// Deterministic per document index, so two runs on the same corpus size
	// generate byte-identical text and their numbers are comparable. A
	// cryptographic source would make the generator the thing being measured.
	//
	// #nosec G404 -- benchmark corpus, deliberately reproducible
	src := rand.New(rand.NewPCG(uint64(n), 0x5eed))

	if words < 1 {
		words = 1
	}
	perPara := (words + 3) / 4

	var b strings.Builder
	fmt.Fprintf(&b, "# Note %d\n\n", n)
	written := 0
	for para := 0; para < 4 && written < words; para++ {
		for w := 0; w < perPara && written < words; w++ {
			b.WriteString(zipfWord(src.Float64()))
			b.WriteByte(' ')
			written++
		}
		b.WriteString("\n\n")
	}
	return b.String()
}

// --- output ---

func (r *run) printPlain() {
	fmt.Printf("MDDB profile — %s\n", r.MeasuredAt.Format("2006-01-02 15:04 UTC"))
	fmt.Printf("  host          %s/%s, %d CPUs, %s\n", r.OS, r.Arch, r.CPUs, r.GoVersion)
	fmt.Printf("  corpus        %d documents of ~%d B, batches of %d\n", r.Docs, r.DocBytes, r.Batch)
	fmt.Printf("  ingest        %.0f docs/s (%d in %.2fs)\n", r.IngestPerSecond, r.Docs, r.IngestSeconds)
	fmt.Printf("  search p50    %s\n", round(r.SearchP50))
	fmt.Printf("  search p95    %s\n", round(r.SearchP95))
	fmt.Printf("  search p99    %s\n", round(r.SearchP99))
	fmt.Printf("  database      %.1f MiB on disk\n", float64(r.DatabaseBytes)/(1<<20))
	if r.ResidentBytes > 0 {
		fmt.Printf("  server RSS    %.0f MiB\n", float64(r.ResidentBytes)/(1<<20))
	} else {
		fmt.Printf("  server RSS    not measured (no single mddbd process visible)\n")
	}
}

func (r *run) printMarkdown() {
	fmt.Printf("<!-- generated by `go run ./mddb-profile -markdown` on %s -->\n\n",
		r.MeasuredAt.Format("2006-01-02"))
	fmt.Printf("| Measurement | Value |\n|---|---|\n")
	fmt.Printf("| Ingest (batch API) | **%.0f docs/s** |\n", r.IngestPerSecond)
	fmt.Printf("| Full-text search, p50 | **%s** |\n", round(r.SearchP50))
	fmt.Printf("| Full-text search, p95 | **%s** |\n", round(r.SearchP95))
	fmt.Printf("| Full-text search, p99 | %s |\n", round(r.SearchP99))
	fmt.Printf("| Database on disk | %.1f MiB for %d documents |\n",
		float64(r.DatabaseBytes)/(1<<20), r.Docs)
	if r.ResidentBytes > 0 {
		fmt.Printf("| Server RSS after the run | %.0f MiB |\n", float64(r.ResidentBytes)/(1<<20))
	}
	fmt.Printf("\nMeasured on %s/%s, %d CPUs, %s, corpus of %d documents of ~%d B in batches of %d,\n",
		r.OS, r.Arch, r.CPUs, r.GoVersion, r.Docs, r.DocBytes, r.Batch)
	fmt.Printf("%d timed queries after 20 warm-up queries.\n", r.Queries)
}

func round(d time.Duration) string {
	switch {
	case d >= time.Millisecond:
		return d.Round(10 * time.Microsecond).String()
	default:
		return d.Round(time.Microsecond).String()
	}
}

// --- transport ---

func waitReady(client *http.Client, addr string) error {
	var lastErr error
	for i := 0; i < 30; i++ {
		if err := get(client, addr+"/health", nil); err == nil {
			return nil
		} else {
			lastErr = err
		}
		time.Sleep(200 * time.Millisecond)
	}
	return lastErr
}

func get(client *http.Client, url string, out any) error {
	resp, err := client.Get(url) // #nosec G107 -- URL is the operator's own -addr flag
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	return decode(resp, out)
}

func post(client *http.Client, url string, body any, out any) error {
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	resp, err := client.Post(url, "application/json", bytes.NewReader(raw)) // #nosec G107
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	return decode(resp, out)
}

func decode(resp *http.Response, out any) error {
	payload, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("status %d: %s", resp.StatusCode, truncate(string(payload), 200))
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(payload, out)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
