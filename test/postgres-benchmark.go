package main

import (
	"database/sql"
	"fmt"
	"io/ioutil"
	"os"
	"sort"
	"time"

	_ "github.com/lib/pq"
)

const (
	postgresDSN = "host=localhost port=15432 user=mddb password=benchmark123 dbname=mddb_test sslmode=disable"
	totalDocs   = 3000
	batchSize   = 100
)

type Stats struct {
	Times      []time.Duration
	TotalTime  time.Duration
	AvgTime    time.Duration
	MinTime    time.Duration
	MaxTime    time.Duration
	MedianTime time.Duration
	Throughput float64
}

func main() {
	fmt.Println("════════════════════════════════════════════════")
	fmt.Println("  PostgreSQL Performance Test")
	fmt.Println("════════════════════════════════════════════════")
	fmt.Println()

	// Connect to PostgreSQL
	fmt.Print("Connecting to PostgreSQL... ")
	db, err := sql.Open("postgres", postgresDSN)
	if err != nil {
		fmt.Printf("✗ Failed: %v\n", err)
		os.Exit(1)
	}
	defer db.Close()

	// Wait for connection
	for i := 0; i < 30; i++ {
		if err := db.Ping(); err == nil {
			break
		}
		time.Sleep(1 * time.Second)
	}

	if err := db.Ping(); err != nil {
		fmt.Printf("✗ Failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("✓ Connected")

	// Create table
	fmt.Print("Creating table... ")
	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS documents (
			id VARCHAR(255) PRIMARY KEY,
			content TEXT NOT NULL,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		)
	`)
	if err != nil {
		fmt.Printf("✗ Failed: %v\n", err)
		os.Exit(1)
	}

	// Create index
	_, err = db.Exec(`CREATE INDEX IF NOT EXISTS idx_created ON documents(created_at)`)
	if err != nil {
		fmt.Printf("✗ Failed: %v\n", err)
		os.Exit(1)
	}

	// Truncate table
	_, err = db.Exec("TRUNCATE TABLE documents")
	if err != nil {
		fmt.Printf("✗ Failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("✓ Ready")
	fmt.Println()

	// Load test documents
	docs := loadDocuments()
	if len(docs) == 0 {
		fmt.Println("✗ No test documents found")
		os.Exit(1)
	}

	// Run tests
	stats := runTest(db, docs)

	// Print results
	printStats(stats)

	// Save results
	saveResults(stats)
}

func loadDocuments() map[string]string {
	docs := make(map[string]string)

	files := []string{"lorem-short.md", "lorem-medium.md", "lorem-long.md"}
	for _, file := range files {
		content, err := ioutil.ReadFile(file)
		if err != nil {
			continue
		}
		docs[file] = string(content)
	}

	return docs
}

func runTest(db *sql.DB, docs map[string]string) Stats {
	var stats Stats
	stats.Times = make([]time.Duration, 0, totalDocs)

	fmt.Printf("Inserting %d documents...\n", totalDocs)
	fmt.Println()

	startTotal := time.Now()
	docNum := 0

	// Prepare statement
	stmt, err := db.Prepare("INSERT INTO documents (id, content) VALUES ($1, $2)")
	if err != nil {
		fmt.Printf("✗ Failed to prepare statement: %v\n", err)
		os.Exit(1)
	}
	defer stmt.Close()

	for i := 0; i < totalDocs; i++ {
		// Rotate through document sizes
		var content string
		switch i % 3 {
		case 0:
			content = docs["lorem-short.md"]
		case 1:
			content = docs["lorem-medium.md"]
		case 2:
			content = docs["lorem-long.md"]
		}

		docID := fmt.Sprintf("doc-%d", i+1)

		start := time.Now()
		_, err := stmt.Exec(docID, content)
		elapsed := time.Since(start)

		if err != nil {
			fmt.Printf("✗ Failed to insert doc %d: %v\n", i+1, err)
			continue
		}

		stats.Times = append(stats.Times, elapsed)
		docNum++

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
		sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
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
	fmt.Printf("Total time:         %s\n", stats.TotalTime.Round(time.Millisecond))
	fmt.Printf("Average time:       %s\n", stats.AvgTime.Round(time.Microsecond))
	fmt.Printf("Median time:        %s\n", stats.MedianTime.Round(time.Microsecond))
	fmt.Printf("Min time:           %s\n", stats.MinTime.Round(time.Microsecond))
	fmt.Printf("Max time:           %s\n", stats.MaxTime.Round(time.Microsecond))
	fmt.Printf("Throughput:         %.2f docs/sec\n", stats.Throughput)
	fmt.Println()
}

func saveResults(stats Stats) {
	filename := "postgres-performance-results.txt"
	f, err := os.Create(filename)
	if err != nil {
		fmt.Printf("Warning: Could not save results: %v\n", err)
		return
	}
	defer f.Close()

	fmt.Fprintf(f, "PostgreSQL Performance Test Results\n")
	fmt.Fprintf(f, "===================================\n\n")
	fmt.Fprintf(f, "Test Date: %s\n\n", time.Now().Format("2006-01-02 15:04:05"))
	fmt.Fprintf(f, "Documents inserted: %d\n", len(stats.Times))
	fmt.Fprintf(f, "Total time:         %s\n", stats.TotalTime.Round(time.Millisecond))
	fmt.Fprintf(f, "Average time:       %s\n", stats.AvgTime.Round(time.Microsecond))
	fmt.Fprintf(f, "Median time:        %s\n", stats.MedianTime.Round(time.Microsecond))
	fmt.Fprintf(f, "Min time:           %s\n", stats.MinTime.Round(time.Microsecond))
	fmt.Fprintf(f, "Max time:           %s\n", stats.MaxTime.Round(time.Microsecond))
	fmt.Fprintf(f, "Throughput:         %.2f docs/sec\n", stats.Throughput)

	fmt.Printf("✓ Results saved to %s\n", filename)
}
