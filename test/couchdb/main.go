package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"time"

	"mddb-test/internal/benchenv"
)

// drainClose discards and closes a response body so the keep-alive connection
// can be reused across the thousands of inserts this benchmark issues (GO-011).
func drainClose(body io.ReadCloser) {
	_, _ = io.Copy(io.Discard, body)
	_ = body.Close()
}

const (
	totalDocs = 3000
	batchSize = 100
	dbName    = "mddb_test"
)

// couchURL is set in main, with the password from the environment.
var couchURL string

type Stats struct {
	Times      []time.Duration
	TotalTime  time.Duration
	AvgTime    time.Duration
	MinTime    time.Duration
	MaxTime    time.Duration
	MedianTime time.Duration
	Throughput float64
}

type CouchDoc struct {
	Key     string `json:"key"`
	Lang    string `json:"lang"`
	Content string `json:"content"`
}

// checkConnection verifies CouchDB is reachable, exiting on failure. The
// response body is closed via defer so it can't leak (SonarCloud S2095).
func checkConnection() {
	fmt.Print("Connecting to CouchDB... ")
	resp, err := http.Get(couchURL)
	if err != nil {
		fmt.Printf("✗ Failed: %v\n", err)
		os.Exit(1)
	}
	defer drainClose(resp.Body)
	fmt.Println("✓ Connected")
}

func main() {
	couchURL = (&url.URL{Scheme: "http", User: url.UserPassword("mddb", benchenv.MustPassword()), Host: "localhost:5984"}).String()
	fmt.Println("════════════════════════════════════════════════")
	fmt.Println("  CouchDB Performance Test")
	fmt.Println("════════════════════════════════════════════════")
	fmt.Println()

	// Check connection
	checkConnection()

	// Create/recreate database
	fmt.Print("Preparing database... ")
	// Delete if exists (best-effort: a missing DB is fine)
	req, err := http.NewRequest(http.MethodDelete, couchURL+"/"+dbName, nil)
	if err != nil {
		fmt.Printf("✗ Failed to build DELETE request: %v\n", err)
		os.Exit(1)
	}
	if delResp, delErr := http.DefaultClient.Do(req); delErr == nil {
		drainClose(delResp.Body)
	}

	// Create new
	req, err = http.NewRequest(http.MethodPut, couchURL+"/"+dbName, nil)
	if err != nil {
		fmt.Printf("✗ Failed to build PUT request: %v\n", err)
		os.Exit(1)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fmt.Printf("✗ Failed: %v\n", err)
		os.Exit(1)
	}
	drainClose(resp.Body)
	fmt.Println("✓ Ready")
	fmt.Println()

	// Load test documents
	docs := loadDocuments()
	if len(docs) == 0 {
		fmt.Println("✗ No test documents found")
		os.Exit(1)
	}

	// Run test
	stats := runTest(docs)

	// Print results
	printStats(stats)

	// Save results
	saveResults(stats)
}

func loadDocuments() map[string]string {
	docs := make(map[string]string)

	files := []string{"lorem-short.md", "lorem-medium.md", "lorem-long.md"}
	for _, file := range files {
		content, err := os.ReadFile(file)
		if err != nil {
			continue
		}
		docs[file] = string(content)
	}

	return docs
}

func runTest(docs map[string]string) Stats {
	var stats Stats
	stats.Times = make([]time.Duration, 0, totalDocs)

	fmt.Printf("Inserting %d documents...\n\n", totalDocs)

	startTotal := time.Now()
	client := &http.Client{}

	for i := 0; i < totalDocs; i++ {
		var content string
		switch i % 3 {
		case 0:
			content = docs["lorem-short.md"]
		case 1:
			content = docs["lorem-medium.md"]
		case 2:
			content = docs["lorem-long.md"]
		}

		doc := CouchDoc{
			Key:     fmt.Sprintf("doc-%d", i+1),
			Lang:    "en_US",
			Content: content,
		}

		jsonData, err := json.Marshal(doc)
		if err != nil {
			fmt.Printf("✗ Marshal failed: %v\n", err)
			continue
		}

		start := time.Now()
		req, err := http.NewRequest(http.MethodPost, couchURL+"/"+dbName, bytes.NewBuffer(jsonData))
		if err != nil {
			fmt.Printf("✗ Build request failed: %v\n", err)
			continue
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := client.Do(req)
		elapsed := time.Since(start)

		if err != nil {
			fmt.Printf("✗ Insert failed: %v\n", err)
			continue
		}
		drainClose(resp.Body)

		if resp.StatusCode != 201 {
			fmt.Printf("✗ Insert failed with status: %d\n", resp.StatusCode)
			continue
		}

		stats.Times = append(stats.Times, elapsed)

		// Progress indicator
		if (i+1)%100 == 0 {
			fmt.Printf("  Progress: %d/%d documents (%.1f%%)\n", i+1, totalDocs, float64(i+1)/float64(totalDocs)*100)
		}
	}

	stats.TotalTime = time.Since(startTotal)

	// Calculate statistics
	if len(stats.Times) > 0 {
		var sum time.Duration
		stats.MinTime = stats.Times[0]
		stats.MaxTime = stats.Times[0]

		for _, t := range stats.Times {
			sum += t
			if t < stats.MinTime {
				stats.MinTime = t
			}
			if t > stats.MaxTime {
				stats.MaxTime = t
			}
		}

		stats.AvgTime = sum / time.Duration(len(stats.Times))

		// Calculate median
		sorted := make([]time.Duration, len(stats.Times))
		copy(sorted, stats.Times)
		sort.Slice(sorted, func(i, j int) bool {
			return sorted[i] < sorted[j]
		})
		stats.MedianTime = sorted[len(sorted)/2]

		// Calculate throughput
		stats.Throughput = float64(len(stats.Times)) / stats.TotalTime.Seconds()
	}

	fmt.Println()
	return stats
}

func printStats(stats Stats) {
	fmt.Println("════════════════════════════════════════════════")
	fmt.Println("  Results")
	fmt.Println("════════════════════════════════════════════════")
	fmt.Println()
	fmt.Printf("Documents inserted: %d\n", len(stats.Times))
	fmt.Printf("Total time:         %dms\n", stats.TotalTime.Milliseconds())
	fmt.Printf("Average time:       %dµs\n", stats.AvgTime.Microseconds())
	fmt.Printf("Median time:        %dµs\n", stats.MedianTime.Microseconds())
	fmt.Printf("Min time:           %dµs\n", stats.MinTime.Microseconds())
	fmt.Printf("Max time:           %.3fms\n", float64(stats.MaxTime.Microseconds())/1000.0)
	fmt.Printf("Throughput:         %.2f docs/sec\n", stats.Throughput)
	fmt.Println()
}

func saveResults(stats Stats) {
	filename := "couchdb-performance-results.txt"
	f, err := os.Create(filename)
	if err != nil {
		fmt.Printf("Warning: Could not save results: %v\n", err)
		return
	}
	defer f.Close()

	fmt.Fprintf(f, "CouchDB Performance Test Results\n")
	fmt.Fprintf(f, "=================================\n\n")
	fmt.Fprintf(f, "Test Date: %s\n\n", time.Now().Format("2006-01-02 15:04:05"))
	fmt.Fprintf(f, "Documents inserted: %d\n", len(stats.Times))
	fmt.Fprintf(f, "Total time:         %dms\n", stats.TotalTime.Milliseconds())
	fmt.Fprintf(f, "Average time:       %dµs\n", stats.AvgTime.Microseconds())
	fmt.Fprintf(f, "Median time:        %dµs\n", stats.MedianTime.Microseconds())
	fmt.Fprintf(f, "Min time:           %dµs\n", stats.MinTime.Microseconds())
	fmt.Fprintf(f, "Max time:           %.3fms\n", float64(stats.MaxTime.Microseconds())/1000.0)
	fmt.Fprintf(f, "Throughput:         %.2f docs/sec\n", stats.Throughput)

	fmt.Printf("✓ Results saved to %s\n", filename)
}
