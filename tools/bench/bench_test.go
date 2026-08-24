package main

import (
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// TEST-001. The benchmark generates documents, posts them and renders a report.
// Its numbers are what people quote, so a division that yields +Inf or a
// minimum left at MaxFloat64 is a wrong answer presented as a measurement.

// --- generated content ---

func TestGeneratedPostsAreWellFormed(t *testing.T) {
	for i := 0; i < 50; i++ {
		title, content, tags := randomBlogPost()

		if title == "" {
			t.Fatal("a post was generated with no title")
		}
		if !strings.HasPrefix(content, "# "+title) {
			t.Errorf("the content does not open with its own title:\n%s", content[:min(80, len(content))])
		}
		if len(tags) == 0 {
			t.Error("a post was generated with no tags")
		}
		// Tags index the documents; a duplicate would skew the metadata index
		// the benchmark is meant to exercise.
		seen := map[string]bool{}
		for _, tag := range tags {
			if seen[tag] {
				t.Errorf("tag %q repeats in one post: %v", tag, tags)
			}
			seen[tag] = true
		}
	}
}

func TestCapitalizeHandlesEdges(t *testing.T) {
	cases := map[string]string{"word": "Word", "": "", "a": "A", "Already": "Already"}
	for in, want := range cases {
		if got := capitalize(in); got != want {
			t.Errorf("capitalize(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSentencesAndParagraphsAreNotEmpty(t *testing.T) {
	if s := randomSentence(); !strings.HasSuffix(s, ".") {
		t.Errorf("a sentence does not end in a full stop: %q", s)
	}
	if p := randomParagraph(); len(p) < 10 {
		t.Errorf("a paragraph came out as %q", p)
	}
	if w := randomWord(); w == "" {
		t.Error("randomWord returned an empty string")
	}
	if title := randomTitle(); title != capitalize(title) {
		t.Errorf("a title is not capitalised: %q", title)
	}
}

// --- HTTP paths ---

func TestAddDocPostsTheDocument(t *testing.T) {
	var mu sync.Mutex
	var got addRequest

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/add" {
			t.Errorf("posted to %s, want /v1/add", r.URL.Path)
		}
		mu.Lock()
		_ = json.NewDecoder(r.Body).Decode(&got)
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	if err := addDoc(srv.Client(), srv.URL, "bench", 7); err != nil {
		t.Fatalf("addDoc: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if got.Collection != "bench" {
		t.Errorf("collection = %q", got.Collection)
	}
	if got.Key != "post-7" {
		t.Errorf("key = %q, want post-7", got.Key)
	}
	if got.ContentMD == "" {
		t.Error("the document was posted with no content")
	}
	for _, field := range []string{"title", "tags", "author"} {
		if len(got.Meta[field]) == 0 {
			t.Errorf("meta.%s is empty", field)
		}
	}
}

// A rejected write must fail the run: a benchmark that counts refused documents
// as successes reports a throughput the server never achieved.
func TestAddDocReportsANonOKStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	if err := addDoc(srv.Client(), srv.URL, "bench", 1); err == nil {
		t.Fatal("a 403 was counted as a successful write")
	}
}

func TestAddDocReportsAnUnreachableServer(t *testing.T) {
	if err := addDoc(http.DefaultClient, "http://127.0.0.1:1", "bench", 1); err == nil {
		t.Fatal("a connection failure was counted as a successful write")
	}
}

func TestConnectivityCheck(t *testing.T) {
	t.Run("reachable", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/v1/stats" {
				t.Errorf("checked %s, want /v1/stats", r.URL.Path)
			}
			w.WriteHeader(http.StatusOK)
		}))
		defer srv.Close()

		if err := checkConnectivity(srv.Client(), srv.URL); err != nil {
			t.Errorf("a healthy server was reported unreachable: %v", err)
		}
	})

	t.Run("wrong status", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusServiceUnavailable)
		}))
		defer srv.Close()

		if err := checkConnectivity(srv.Client(), srv.URL); err == nil {
			t.Error("a 503 passed the connectivity check")
		}
	})

	t.Run("nothing listening", func(t *testing.T) {
		if err := checkConnectivity(http.DefaultClient, "http://127.0.0.1:1"); err == nil {
			t.Error("an unreachable address passed the connectivity check")
		}
	})
}

func TestDeleteCollectionPostsTheName(t *testing.T) {
	var mu sync.Mutex
	var body map[string]string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		_ = json.NewDecoder(r.Body).Decode(&body)
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	if err := deleteCollection(srv.Client(), srv.URL, "bench"); err != nil {
		t.Fatalf("deleteCollection: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if body["collection"] != "bench" {
		t.Errorf("collection = %q", body["collection"])
	}
}

// --- the report ---

func TestReportIsWrittenAndReadable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "report.html")
	results := []batchResult{
		{BatchNum: 1, DocsTotal: 100, Duration: time.Second, Throughput: 100, CumAvg: 100},
		{BatchNum: 2, DocsTotal: 200, Duration: 2 * time.Second, Throughput: 50, CumAvg: 66.7},
	}

	if err := generateReport(path, results, "bench", "http://x", 200, 100); err != nil {
		t.Fatalf("generateReport: %v", err)
	}

	raw, err := os.ReadFile(path) // #nosec G304 -- test-controlled path
	if err != nil {
		t.Fatal(err)
	}
	html := string(raw)

	for _, want := range []string{"bench", "http://x", "<svg", "</html>"} {
		if !strings.Contains(html, want) {
			t.Errorf("the report does not contain %q", want)
		}
	}
	// A number the template failed to compute is worse than a missing one.
	for _, bad := range []string{"+Inf", "NaN", "1.7976931348623157e+308"} {
		if strings.Contains(html, bad) {
			t.Errorf("the report contains %s:\n%s", bad, html[:min(400, len(html))])
		}
	}
}

// GO-013 guarded perSecond and the SVG coordinates against a zero interval and
// then computed the headline average with a bare division.
func TestReportWithNoBatchesHasNoInfinities(t *testing.T) {
	path := filepath.Join(t.TempDir(), "empty.html")

	if err := generateReport(path, nil, "bench", "http://x", 0, 100); err != nil {
		t.Fatalf("generateReport on an empty run: %v", err)
	}

	raw, err := os.ReadFile(path) // #nosec G304 -- test-controlled path
	if err != nil {
		t.Fatal(err)
	}
	html := string(raw)

	for _, bad := range []string{"+Inf", "-Inf", "NaN", "1.7976931348623157e+308"} {
		if strings.Contains(html, bad) {
			t.Errorf("an empty run rendered %s into the report", bad)
		}
	}
}

// A run whose batches all took no measurable time is the shape a fast local
// server produces, and must not divide by zero either.
func TestReportWithZeroDurations(t *testing.T) {
	path := filepath.Join(t.TempDir(), "instant.html")
	results := []batchResult{{BatchNum: 1, DocsTotal: 10, Duration: 0, Throughput: 0}}

	if err := generateReport(path, results, "bench", "http://x", 10, 10); err != nil {
		t.Fatalf("generateReport: %v", err)
	}

	raw, _ := os.ReadFile(path) // #nosec G304 -- test-controlled path
	if strings.Contains(string(raw), "Inf") || strings.Contains(string(raw), "NaN") {
		t.Errorf("a zero-duration run produced a non-number:\n%s", raw[:min(400, len(raw))])
	}
}

func TestReportFailsOnAnUnwritablePath(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "no-such-dir")
	err := generateReport(filepath.Join(dir, "r.html"), nil, "bench", "http://x", 0, 10)
	if err == nil {
		t.Fatal("writing into a missing directory was reported as success")
	}
}

// --- arithmetic guards (GO-013) ---

func TestThroughputMathNeverProducesANonNumber(t *testing.T) {
	inputs := []struct{ val, maxY float64 }{
		{0, 0}, {100, 0}, {0, 100}, {100, 100}, {-5, 100}, {100, -5},
		{math.MaxFloat64, 1}, {1, math.SmallestNonzeroFloat64},
	}
	for _, in := range inputs {
		for name, fn := range map[string]func(float64, float64) float64{
			"barHeight": barHeight, "barY": barY, "lineY": lineY, "perSecond": perSecond,
		} {
			got := fn(in.val, in.maxY)
			if math.IsInf(got, 0) || math.IsNaN(got) {
				t.Errorf("%s(%v, %v) = %v", name, in.val, in.maxY, got)
			}
		}
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// --- the benchmark loop ---

// TEST-001: the loop lived inside main() and exited the process on the first
// failed write, so nothing could observe what it had measured.

func TestRunBenchmarkInsertsEveryDocument(t *testing.T) {
	var mu sync.Mutex
	var keys []string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req addRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		mu.Lock()
		keys = append(keys, req.Key)
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	results, err := runBenchmark(srv.Client(), benchConfig{
		URL: srv.URL, Collection: "bench", Total: 25, Batch: 10,
	}, nil)
	if err != nil {
		t.Fatalf("runBenchmark: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(keys) != 25 {
		t.Errorf("%d documents were written, want 25", len(keys))
	}
	// 25 in batches of 10 is 10, 10, 5 — the last batch must be short, not a
	// full one that overshoots the total.
	if len(results) != 3 {
		t.Fatalf("%d batches, want 3", len(results))
	}
	if got := results[len(results)-1].DocsTotal; got != 25 {
		t.Errorf("the run finished at %d documents, want 25", got)
	}

	// Keys must be unique, or the benchmark overwrites instead of inserting.
	seen := map[string]bool{}
	for _, k := range keys {
		if seen[k] {
			t.Errorf("key %q was written twice", k)
		}
		seen[k] = true
	}
}

func TestRunBenchmarkStopsAtTheFirstFailure(t *testing.T) {
	var mu sync.Mutex
	written := 0

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		written++
		n := written
		mu.Unlock()
		if n > 12 {
			w.WriteHeader(http.StatusInsufficientStorage)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	results, err := runBenchmark(srv.Client(), benchConfig{
		URL: srv.URL, Collection: "bench", Total: 100, Batch: 10,
	}, nil)

	if err == nil {
		t.Fatal("a server that started refusing writes produced no error")
	}
	// The batches that did complete are still worth reporting.
	if len(results) != 1 {
		t.Errorf("%d completed batches were kept, want 1", len(results))
	}
	if !strings.Contains(err.Error(), "document") {
		t.Errorf("the error does not say which document failed: %v", err)
	}
}

func TestRunBenchmarkWritesProgress(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	var progress strings.Builder
	if _, err := runBenchmark(srv.Client(), benchConfig{
		URL: srv.URL, Collection: "bench", Total: 4, Batch: 2,
	}, &progress); err != nil {
		t.Fatal(err)
	}

	out := progress.String()
	if !strings.Contains(out, "batch") {
		t.Errorf("no progress was written:\n%s", out)
	}
	if strings.Contains(out, "Inf") || strings.Contains(out, "NaN") {
		t.Errorf("progress reported a non-number:\n%s", out)
	}
}

// A total smaller than the batch size is one short batch, not zero.
func TestRunBenchmarkWithATotalBelowTheBatchSize(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	results, err := runBenchmark(srv.Client(), benchConfig{
		URL: srv.URL, Collection: "bench", Total: 3, Batch: 100,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].DocsTotal != 3 {
		t.Errorf("results = %+v, want one batch of 3", results)
	}
}

func TestRunBenchmarkWithNothingToDo(t *testing.T) {
	results, err := runBenchmark(http.DefaultClient, benchConfig{
		URL: "http://127.0.0.1:1", Collection: "bench", Total: 0, Batch: 10,
	}, nil)
	if err != nil {
		t.Fatalf("a zero-document run failed: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("a zero-document run produced %d batches", len(results))
	}
}

func TestSummarise(t *testing.T) {
	results := []batchResult{
		{DocsTotal: 10, Duration: time.Second, Throughput: 10},
		{DocsTotal: 20, Duration: 2 * time.Second, Throughput: 5},
		{DocsTotal: 30, Duration: time.Second, Throughput: 10},
	}

	docs, elapsed, minT, maxT := summarise(results)

	if docs != 30 {
		t.Errorf("documents = %d, want 30", docs)
	}
	if elapsed != 4*time.Second {
		t.Errorf("elapsed = %v, want 4s", elapsed)
	}
	if minT != 5 || maxT != 10 {
		t.Errorf("min/max = %v/%v, want 5/10", minT, maxT)
	}
}

// An empty run has no minimum; the scan's MaxFloat64 sentinel must not escape.
func TestSummariseOfAnEmptyRun(t *testing.T) {
	docs, elapsed, minT, maxT := summarise(nil)

	if docs != 0 || elapsed != 0 || maxT != 0 {
		t.Errorf("summarise(nil) = %d, %v, _, %v", docs, elapsed, maxT)
	}
	if minT != 0 {
		t.Errorf("the minimum of an empty run is %v, want 0", minT)
	}
	if minT == math.MaxFloat64 {
		t.Error("the sentinel escaped into the summary")
	}
}

func TestPrintSummaryOfAnEmptyRunIsReadable(t *testing.T) {
	var sb strings.Builder
	printSummary(&sb, nil)

	out := sb.String()
	for _, bad := range []string{"Inf", "NaN", "1.7976931348623157e+308"} {
		if strings.Contains(out, bad) {
			t.Errorf("an empty run summarised as:\n%s", out)
		}
	}
	if !strings.Contains(out, "Total documents: 0") {
		t.Errorf("the summary does not report zero documents:\n%s", out)
	}
}

func TestPrintSummaryReportsTheNumbers(t *testing.T) {
	var sb strings.Builder
	printSummary(&sb, []batchResult{
		{DocsTotal: 100, Duration: time.Second, Throughput: 100},
		{DocsTotal: 200, Duration: time.Second, Throughput: 100},
	})

	out := sb.String()
	for _, want := range []string{"200", "100 docs/sec"} {
		if !strings.Contains(out, want) {
			t.Errorf("the summary does not mention %q:\n%s", want, out)
		}
	}
}

func TestPrintHeaderNamesTheTarget(t *testing.T) {
	var sb strings.Builder
	printHeader(&sb, benchConfig{URL: "http://x:7890", Collection: "bench", Total: 10, Batch: 5})

	for _, want := range []string{"http://x:7890", "bench", "10", "5"} {
		if !strings.Contains(sb.String(), want) {
			t.Errorf("the header does not mention %q:\n%s", want, sb.String())
		}
	}
}
