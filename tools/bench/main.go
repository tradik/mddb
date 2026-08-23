package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"html/template"
	"io"
	"math"
	"math/rand"
	"net/http"
	"os"
	"strings"
	"time"
)

// --- lorem ipsum generator ---

var loremWords = []string{
	"lorem", "ipsum", "dolor", "sit", "amet", "consectetur", "adipiscing", "elit",
	"sed", "do", "eiusmod", "tempor", "incididunt", "ut", "labore", "et", "dolore",
	"magna", "aliqua", "enim", "ad", "minim", "veniam", "quis", "nostrud",
	"exercitation", "ullamco", "laboris", "nisi", "aliquip", "ex", "ea", "commodo",
	"consequat", "duis", "aute", "irure", "in", "reprehenderit", "voluptate",
	"velit", "esse", "cillum", "fugiat", "nulla", "pariatur", "excepteur", "sint",
	"occaecat", "cupidatat", "non", "proident", "sunt", "culpa", "qui", "officia",
	"deserunt", "mollit", "anim", "id", "est", "laborum", "praesentium", "voluptatum",
	"deleniti", "atque", "corrupti", "quos", "dolores", "quas", "molestias",
	"excepturi", "obcaecati", "cupiditate", "provident", "similique", "accusantium",
	"nemo", "ipsam", "voluptatem", "quia", "voluptas", "aspernatur", "aut", "odit",
	"fugit", "consequuntur", "magni", "ratione", "sequi", "nesciunt", "neque",
	"porro", "quisquam", "numquam", "eius", "modi", "tempora", "quaerat",
}

var tagPool = []string{
	"golang", "tutorial", "devops", "kubernetes", "docker", "react", "typescript",
	"database", "api", "microservices", "testing", "performance", "security",
	"cloud", "linux", "architecture", "monitoring", "ci-cd", "rust", "python",
}

// randIntn returns a pseudo-random index.
//
// Deliberately math/rand and not crypto/rand: this generates lorem-ipsum
// benchmark payloads, where reproducible-looking filler is the point and a
// cryptographic source would only make the generator the thing being measured.
// Routed through one function so the exemption is stated once.
//
// #nosec G404 -- benchmark filler, not a security decision
func randIntn(n int) int {
	return rand.Intn(n)
}

func randomWord() string {
	return loremWords[randIntn(len(loremWords))]
}

func capitalize(s string) string {
	if len(s) == 0 {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func randomSentence() string {
	n := 8 + randIntn(8)
	words := make([]string, n)
	for i := range words {
		words[i] = randomWord()
	}
	words[0] = capitalize(words[0])
	return strings.Join(words, " ") + "."
}

func randomParagraph() string {
	n := 3 + randIntn(4)
	sentences := make([]string, n)
	for i := range sentences {
		sentences[i] = randomSentence()
	}
	return strings.Join(sentences, " ")
}

func randomTitle() string {
	n := 3 + randIntn(4)
	words := make([]string, n)
	for i := range words {
		words[i] = capitalize(randomWord())
	}
	return strings.Join(words, " ")
}

// randomTags picks 1-3 distinct tags.
//
// TEST-001: this used to draw with replacement, so a document could carry the
// same tag twice. The benchmark exists to measure metadata indexing, and a
// repeated value indexes once — the run then reported a tag count it had not
// actually written.
func randomTags() []string {
	n := 1 + randIntn(3)
	chosen := make(map[string]bool, n)
	tags := make([]string, 0, n)
	for len(tags) < n {
		tag := tagPool[randIntn(len(tagPool))]
		if chosen[tag] {
			continue
		}
		chosen[tag] = true
		tags = append(tags, tag)
	}
	return tags
}

func randomBlogPost() (title string, content string, tags []string) {
	title = randomTitle()
	nParagraphs := 2 + randIntn(4)
	paragraphs := make([]string, nParagraphs)
	for i := range paragraphs {
		paragraphs[i] = randomParagraph()
	}
	content = "# " + title + "\n\n" + strings.Join(paragraphs, "\n\n")
	tags = randomTags()
	return
}

// --- MDDB client ---

type addRequest struct {
	Collection string              `json:"collection"`
	Key        string              `json:"key"`
	Lang       string              `json:"lang"`
	Meta       map[string][]string `json:"meta"`
	ContentMD  string              `json:"contentMd"`
}

func addDoc(client *http.Client, baseURL, collection string, docNum int) error {
	title, content, tags := randomBlogPost()
	key := fmt.Sprintf("post-%d", docNum)

	body, _ := json.Marshal(addRequest{
		Collection: collection,
		Key:        key,
		Lang:       "en",
		Meta: map[string][]string{
			"title":  {title},
			"tags":   tags,
			"author": {fmt.Sprintf("author-%d", randIntn(20))},
		},
		ContentMD: content,
	})

	resp, err := client.Post(baseURL+"/v1/add", "application/json", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("POST /v1/add: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return fmt.Errorf("POST /v1/add: status %d", resp.StatusCode)
	}
	return nil
}

func checkConnectivity(client *http.Client, baseURL string) error {
	resp, err := client.Get(baseURL + "/v1/stats")
	if err != nil {
		return fmt.Errorf("cannot connect to %s: %w", baseURL, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET /v1/stats returned %d", resp.StatusCode)
	}
	return nil
}

func deleteCollection(client *http.Client, baseURL, collection string) error {
	body, _ := json.Marshal(map[string]string{"collection": collection})
	resp, err := client.Post(baseURL+"/v1/delete-collection", "application/json", bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	return nil
}

// --- batch result ---

type batchResult struct {
	BatchNum   int
	DocsTotal  int
	Duration   time.Duration
	Throughput float64 // docs/sec for this batch
	CumAvg     float64 // cumulative average docs/sec
}

// --- benchmark run ---

// benchConfig is everything a run needs, so the run itself can be exercised
// without a process to launch (TEST-001: this all lived inside main(), which
// called os.Exit on the first failure and could not be called by a test).
type benchConfig struct {
	URL        string
	Collection string
	Total      int
	Batch      int
	Output     string
	Cleanup    bool
}

// runBenchmark inserts Total documents in batches of Batch, timing each batch.
//
// It stops at the first failed write and returns what it had measured so far:
// a partial run is worth reporting, and continuing past a server that started
// refusing writes would report a throughput nothing achieved.
func runBenchmark(client *http.Client, cfg benchConfig, progress io.Writer) ([]batchResult, error) {
	nBatches := (cfg.Total + cfg.Batch - 1) / cfg.Batch
	results := make([]batchResult, 0, nBatches)
	totalDocs := 0
	var totalElapsed time.Duration

	for b := 0; b < nBatches; b++ {
		batchSize := cfg.Batch
		if totalDocs+batchSize > cfg.Total {
			batchSize = cfg.Total - totalDocs
		}

		start := time.Now()
		for i := 0; i < batchSize; i++ {
			docNum := totalDocs + i + 1
			if err := addDoc(client, cfg.URL, cfg.Collection, docNum); err != nil {
				return results, fmt.Errorf("document %d: %w", docNum, err)
			}
		}
		elapsed := time.Since(start)

		totalDocs += batchSize
		totalElapsed += elapsed
		// GO-013: a sub-microsecond batch makes elapsed.Seconds() ~0 → +Inf.
		throughput := perSecond(float64(batchSize), elapsed.Seconds())
		cumAvg := perSecond(float64(totalDocs), totalElapsed.Seconds())

		results = append(results, batchResult{
			BatchNum:   b + 1,
			DocsTotal:  totalDocs,
			Duration:   elapsed,
			Throughput: throughput,
			CumAvg:     cumAvg,
		})

		if progress != nil {
			_, _ = fmt.Fprintf(progress,
				"  [batch %3d/%d] %4d docs in %8s (%6.0f docs/sec) | total: %5d  avg: %6.0f docs/sec\n",
				b+1, nBatches, batchSize, elapsed.Round(time.Millisecond), throughput, totalDocs, cumAvg)
		}
	}

	return results, nil
}

// summarise reports the totals a run produced.
//
// minThroughput is 0 for an empty run rather than the MaxFloat64 sentinel the
// scan starts from — printing 1.7976931348623157e+308 as the slowest batch is
// not a summary.
func summarise(results []batchResult) (totalDocs int, totalElapsed time.Duration, minT, maxT float64) {
	if len(results) == 0 {
		return 0, 0, 0, 0
	}
	minT = math.MaxFloat64
	for _, r := range results {
		totalElapsed += r.Duration
		if r.Throughput < minT {
			minT = r.Throughput
		}
		if r.Throughput > maxT {
			maxT = r.Throughput
		}
	}
	return results[len(results)-1].DocsTotal, totalElapsed, minT, maxT
}

// printHeader describes the run about to start.
func printHeader(w io.Writer, cfg benchConfig) {
	_, _ = fmt.Fprintf(w, "MDDB Benchmark\n")
	_, _ = fmt.Fprintf(w, "  URL:        %s\n", cfg.URL)
	_, _ = fmt.Fprintf(w, "  Collection: %s\n", cfg.Collection)
	_, _ = fmt.Fprintf(w, "  Total:      %d docs\n", cfg.Total)
	_, _ = fmt.Fprintf(w, "  Batch:      %d docs\n", cfg.Batch)
	_, _ = fmt.Fprintln(w)
}

// printSummary writes the totals people quote from a run.
//
// Separate from main so the numbers can be asserted: a summary line reading
// "+Inf docs/sec" is a wrong measurement presented with authority.
func printSummary(w io.Writer, results []batchResult) {
	totalDocs, totalElapsed, minT, maxT := summarise(results)

	_, _ = fmt.Fprintln(w)
	_, _ = fmt.Fprintln(w, "--- Summary ---")
	_, _ = fmt.Fprintf(w, "  Total documents: %d\n", totalDocs)
	_, _ = fmt.Fprintf(w, "  Total time:      %s\n", totalElapsed.Round(time.Millisecond))
	_, _ = fmt.Fprintf(w, "  Avg throughput:  %.0f docs/sec\n", perSecond(float64(totalDocs), totalElapsed.Seconds()))
	_, _ = fmt.Fprintf(w, "  Min batch:       %.0f docs/sec\n", minT)
	_, _ = fmt.Fprintf(w, "  Max batch:       %.0f docs/sec\n", maxT)
}

// --- main ---

func main() {
	url := flag.String("url", "http://localhost:7890", "MDDB base URL")
	collection := flag.String("collection", "bench", "Collection name")
	total := flag.Int("total", 10000, "Total documents to insert")
	batch := flag.Int("batch", 100, "Batch size for timing")
	output := flag.String("output", "bench_report.html", "HTML report output path")
	cleanup := flag.Bool("cleanup", false, "Delete collection after benchmark")
	flag.Parse()

	cfg := benchConfig{
		URL: *url, Collection: *collection, Total: *total,
		Batch: *batch, Output: *output, Cleanup: *cleanup,
	}
	client := &http.Client{Timeout: 30 * time.Second}

	printHeader(os.Stdout, cfg)

	if err := checkConnectivity(client, cfg.URL); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("Connected to MDDB.")
	fmt.Println()

	results, runErr := runBenchmark(client, cfg, os.Stdout)
	if runErr != nil {
		fmt.Fprintf(os.Stderr, "\nError: %v\n", runErr)
	}

	printSummary(os.Stdout, results)

	if err := generateReport(cfg.Output, results, cfg.Collection, cfg.URL, cfg.Total, cfg.Batch); err != nil {
		fmt.Fprintf(os.Stderr, "Error generating report: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("\n  Report saved to: %s\n", cfg.Output)

	if cfg.Cleanup {
		fmt.Printf("  Cleaning up collection '%s'...\n", cfg.Collection)
		if err := deleteCollection(client, cfg.URL, cfg.Collection); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: cleanup failed: %v\n", err)
		}
	}

	// A failed run must not exit zero: a CI job reading the exit code would
	// treat a benchmark that stopped a tenth of the way in as a pass.
	if runErr != nil {
		os.Exit(1)
	}
}

// --- HTML report ---

type reportData struct {
	Collection string
	URL        string
	Total      int
	Batch      int
	TotalTime  string
	AvgThrpt   float64
	MinThrpt   float64
	MaxThrpt   float64
	Results    []batchResult
	MaxY       float64
	Timestamp  string
}

func generateReport(path string, results []batchResult, collection, url string, total, batch int) error {
	var totalElapsed time.Duration
	var minT, maxT float64
	// A run with no batches has no minimum. Leaving the sentinel would print
	// 1.7976931348623157e+308 as the slowest batch.
	if len(results) > 0 {
		minT = math.MaxFloat64
	}
	for _, r := range results {
		totalElapsed += r.Duration
		if r.Throughput < minT {
			minT = r.Throughput
		}
		if r.Throughput > maxT {
			maxT = r.Throughput
		}
	}

	maxY := math.Ceil(maxT/100) * 100
	if maxY < 100 {
		maxY = 100
	}

	data := reportData{
		Collection: collection,
		URL:        url,
		Total:      total,
		Batch:      batch,
		TotalTime:  totalElapsed.Round(time.Millisecond).String(),
		// perSecond, not a bare division: a run with no batches — or one whose
		// batches were too fast to measure — has a zero elapsed time, and
		// 0/0 is NaN. GO-013 guarded every other division and missed this one.
		AvgThrpt:  perSecond(float64(total), totalElapsed.Seconds()),
		MinThrpt:  minT,
		MaxThrpt:  maxT,
		Results:   results,
		MaxY:      maxY,
		Timestamp: time.Now().Format("2006-01-02 15:04:05"),
	}

	// #nosec G304 -- the report path is the operator's own --output flag
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	// GO-013: don't swallow the report's flush/close error — a truncated HTML
	// report must surface, not silently "succeed".
	if err := reportTmpl.Execute(f, data); err != nil {
		_ = f.Close()
		return fmt.Errorf("render report: %w", err)
	}
	return f.Close()
}

// perSecond returns count/secs, guarding against a zero (or negative) interval
// that would otherwise yield +Inf (GO-013).
func perSecond(count, secs float64) float64 {
	if secs <= 0 {
		return 0
	}
	rate := count / secs
	// An interval too short to divide by is unmeasurable, not infinitely
	// fast. Reporting +Inf docs/sec would put a non-number in the report.
	if math.IsInf(rate, 0) || math.IsNaN(rate) {
		return 0
	}
	return rate
}

// SVG-coordinate helpers.
//
// GO-013 guarded maxY<=0 so empty benchmark data could not put Inf or NaN into
// the report. TEST-001 added the other half: a ratio large enough to overflow,
// or a maxY small enough to, produced coordinates outside the viewBox, which an
// SVG renderer draws as nothing at all. Every result is clamped to the chart
// area, so an out-of-range bar is visibly full height rather than invisible.

// chartHeight is the plot area; chartBaseline is where the x-axis sits.
const (
	chartHeight   = 300.0
	chartBaseline = 320.0
)

// scaledHeight converts a value into a bar height clamped to the chart.
func scaledHeight(val, maxY float64) float64 {
	if maxY <= 0 || val <= 0 {
		return 0
	}
	h := (val / maxY) * chartHeight
	if math.IsInf(h, 0) || math.IsNaN(h) || h > chartHeight {
		return chartHeight
	}
	return h
}

func barHeight(val, maxY float64) float64 {
	return scaledHeight(val, maxY)
}

func barY(val, maxY float64) float64 {
	return chartHeight - scaledHeight(val, maxY)
}

func lineY(val, maxY float64) float64 {
	return chartBaseline - scaledHeight(val, maxY)
}

var reportTmpl = template.Must(template.New("report").Funcs(template.FuncMap{
	"barHeight": barHeight,
	"barY":      barY,
	"lineY":     lineY,
	"barX": func(idx, total int) float64 {
		if total <= 0 {
			return 0
		}
		w := 800.0 / float64(total)
		return 60 + float64(idx)*w
	},
	"barW": func(total int) float64 {
		if total <= 0 {
			return 5
		}
		w := 800.0 / float64(total)
		if w > 1 {
			w -= 1
		}
		return w
	},
	"fmtDur": func(d time.Duration) string {
		return d.Round(time.Millisecond).String()
	},
	"fmtFloat": func(f float64) string {
		return fmt.Sprintf("%.0f", f)
	},
	"gridY": func(val, maxY float64) float64 {
		return 320 - (val/maxY)*300
	},
	"gridLines": func(maxY float64) []float64 {
		step := maxY / 5
		lines := make([]float64, 5)
		for i := range lines {
			lines[i] = step * float64(i+1)
		}
		return lines
	},
	"mod": func(a, b int) int { return a % b },
}).Parse(`<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<title>MDDB Benchmark Report</title>
<style>
  body { font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif; max-width: 960px; margin: 40px auto; padding: 0 20px; color: #1a1a2e; background: #f8f9fa; }
  h1 { color: #16213e; border-bottom: 2px solid #0f3460; padding-bottom: 8px; }
  .stats { display: grid; grid-template-columns: repeat(auto-fit, minmax(180px, 1fr)); gap: 16px; margin: 24px 0; }
  .stat { background: #fff; border-radius: 8px; padding: 16px; box-shadow: 0 1px 3px rgba(0,0,0,0.1); }
  .stat .label { font-size: 12px; text-transform: uppercase; color: #666; letter-spacing: 0.5px; }
  .stat .value { font-size: 28px; font-weight: 700; color: #0f3460; margin-top: 4px; }
  .chart-container { background: #fff; border-radius: 8px; padding: 24px; box-shadow: 0 1px 3px rgba(0,0,0,0.1); margin: 24px 0; }
  svg text { font-family: inherit; }
  .bar { fill: #0f3460; opacity: 0.8; }
  .bar:hover { opacity: 1; }
  .avg-line { stroke: #e94560; stroke-width: 2; stroke-dasharray: 6 3; fill: none; }
  .grid-line { stroke: #e0e0e0; stroke-width: 1; }
  .axis-label { font-size: 11px; fill: #666; }
  table { width: 100%; border-collapse: collapse; margin: 24px 0; background: #fff; border-radius: 8px; overflow: hidden; box-shadow: 0 1px 3px rgba(0,0,0,0.1); }
  th { background: #0f3460; color: #fff; padding: 10px 12px; text-align: right; font-size: 13px; }
  th:first-child { text-align: left; }
  td { padding: 8px 12px; text-align: right; border-bottom: 1px solid #eee; font-size: 13px; font-variant-numeric: tabular-nums; }
  td:first-child { text-align: left; }
  tr:hover td { background: #f0f4ff; }
  .footer { margin-top: 32px; color: #999; font-size: 12px; }
</style>
</head>
<body>
<h1>MDDB Benchmark Report</h1>

<div class="stats">
  <div class="stat"><div class="label">Total Documents</div><div class="value">{{.Total}}</div></div>
  <div class="stat"><div class="label">Total Time</div><div class="value">{{.TotalTime}}</div></div>
  <div class="stat"><div class="label">Avg Throughput</div><div class="value">{{fmtFloat .AvgThrpt}} docs/s</div></div>
  <div class="stat"><div class="label">Min Batch</div><div class="value">{{fmtFloat .MinThrpt}} docs/s</div></div>
  <div class="stat"><div class="label">Max Batch</div><div class="value">{{fmtFloat .MaxThrpt}} docs/s</div></div>
  <div class="stat"><div class="label">Batch Size</div><div class="value">{{.Batch}}</div></div>
</div>

<div class="chart-container">
<h2 style="margin-top:0">Throughput per Batch (docs/sec)</h2>
<svg viewBox="0 0 900 380" width="100%" height="380">
  <!-- grid lines -->
  {{- $maxY := .MaxY}}
  {{- $nResults := len .Results}}
  {{range gridLines $maxY}}
  <line x1="60" y1="{{gridY . $maxY}}" x2="860" y2="{{gridY . $maxY}}" class="grid-line"/>
  <text x="55" y="{{gridY . $maxY}}" text-anchor="end" class="axis-label" dy="4">{{fmtFloat .}}</text>
  {{end}}

  <!-- baseline -->
  <line x1="60" y1="320" x2="860" y2="320" stroke="#333" stroke-width="1"/>

  <!-- bars (throughput per batch) -->
  {{range $i, $r := .Results}}
  <rect class="bar" x="{{barX $i $nResults}}" y="{{barY $r.Throughput $maxY}}" width="{{barW $nResults}}" height="{{barHeight $r.Throughput $maxY}}">
    <title>Batch {{$r.BatchNum}}: {{$r.DocsTotal}} docs — {{fmtFloat $r.Throughput}} docs/sec ({{fmtDur $r.Duration}})</title>
  </rect>
  {{end}}

  <!-- cumulative average line -->
  <polyline class="avg-line" points="{{range $i, $r := .Results}}{{barX $i $nResults}},{{lineY $r.CumAvg $maxY}} {{end}}"/>

  <!-- x-axis labels (every 10 batches) -->
  {{range $i, $r := .Results}}
  {{if eq (mod $r.BatchNum 10) 0}}
  <text x="{{barX $i $nResults}}" y="340" text-anchor="middle" class="axis-label">{{$r.DocsTotal}}</text>
  {{end}}
  {{end}}

  <!-- legend -->
  <rect x="660" y="5" width="12" height="12" fill="#0f3460" opacity="0.8"/>
  <text x="678" y="15" class="axis-label">Batch throughput</text>
  <line x1="660" y1="28" x2="672" y2="28" stroke="#e94560" stroke-width="2" stroke-dasharray="6 3"/>
  <text x="678" y="32" class="axis-label">Cumulative average</text>

  <!-- y-axis label -->
  <text x="15" y="170" transform="rotate(-90, 15, 170)" class="axis-label" text-anchor="middle">docs/sec</text>
</svg>
</div>

<h2>Batch Details</h2>
<table>
  <tr><th>Batch</th><th>Docs Total</th><th>Duration</th><th>Throughput</th><th>Cum. Average</th></tr>
  {{range .Results}}
  <tr>
    <td>{{.BatchNum}}</td>
    <td>{{.DocsTotal}}</td>
    <td>{{fmtDur .Duration}}</td>
    <td>{{fmtFloat .Throughput}} docs/sec</td>
    <td>{{fmtFloat .CumAvg}} docs/sec</td>
  </tr>
  {{end}}
</table>

<div class="footer">
  <p>Collection: {{.Collection}} | Server: {{.URL}} | Generated: {{.Timestamp}}</p>
</div>
</body>
</html>
`))
