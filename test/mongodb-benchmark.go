package main

import (
	"context"
	"fmt"
	"io/ioutil"
	"os"
	"sort"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const (
	mongoURI   = "mongodb://mddb:benchmark123@localhost:27017"
	totalDocs  = 3000
	batchSize  = 100
	dbName     = "mddb_test"
	collection = "documents"
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
	fmt.Println("  MongoDB Performance Test")
	fmt.Println("════════════════════════════════════════════════")
	fmt.Println()

	// Connect to MongoDB
	fmt.Print("Connecting to MongoDB... ")
	ctx := context.Background()
	client, err := mongo.Connect(ctx, options.Client().ApplyURI(mongoURI))
	if err != nil {
		fmt.Printf("✗ Failed: %v\n", err)
		os.Exit(1)
	}
	defer client.Disconnect(ctx)

	// Ping to verify connection
	if err := client.Ping(ctx, nil); err != nil {
		fmt.Printf("✗ Failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("✓ Connected")

	// Get collection
	coll := client.Database(dbName).Collection(collection)

	// Drop collection to start fresh
	fmt.Print("Preparing collection... ")
	coll.Drop(ctx)
	fmt.Println("✓ Ready")
	fmt.Println()

	// Load test documents
	docs := loadDocuments()
	if len(docs) == 0 {
		fmt.Println("✗ No test documents found")
		os.Exit(1)
	}

	// Run test
	stats := runTest(ctx, coll, docs)

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

func runTest(ctx context.Context, coll *mongo.Collection, docs map[string]string) Stats {
	var stats Stats
	stats.Times = make([]time.Duration, 0, totalDocs)

	fmt.Printf("Inserting %d documents...\n\n", totalDocs)

	startTotal := time.Now()

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

		doc := bson.M{
			"key":     fmt.Sprintf("doc-%d", i+1),
			"lang":    "en_US",
			"content": content,
		}

		start := time.Now()
		_, err := coll.InsertOne(ctx, doc)
		elapsed := time.Since(start)

		if err != nil {
			fmt.Printf("✗ Insert failed: %v\n", err)
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
	filename := "mongodb-performance-results.txt"
	f, err := os.Create(filename)
	if err != nil {
		fmt.Printf("Warning: Could not save results: %v\n", err)
		return
	}
	defer f.Close()

	fmt.Fprintf(f, "MongoDB Performance Test Results\n")
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
