package main

import (
	"context"
	"fmt"
	"io/ioutil"
	"os"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	
	pb "mddb/proto"
)

const (
	grpcAddr   = "localhost:11024"
	totalDocs  = 1000
	batchSize  = 100  // Send 100 documents per batch
	collection = "perftest-grpc-batch"
)

type Stats struct {
	times []time.Duration
	total time.Duration
	min   time.Duration
	max   time.Duration
	avg   time.Duration
}

func main() {
	fmt.Println("════════════════════════════════════════════════")
	fmt.Println("  MDDB gRPC Batch Performance Test")
	fmt.Println("════════════════════════════════════════════════")
	fmt.Println()

	// Connect to gRPC server
	fmt.Print("Connecting to gRPC server... ")
	conn, err := grpc.Dial(grpcAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		fmt.Printf("✗ Failed: %v\n", err)
		os.Exit(1)
	}
	defer conn.Close()
	fmt.Println("✓ Connected")

	client := pb.NewMDDBClient(conn)

	fmt.Println()
	fmt.Printf("Test Configuration:\n")
	fmt.Printf("  Collection: %s\n", collection)
	fmt.Printf("  Total documents: %d\n", totalDocs)
	fmt.Printf("  Batch size: %d\n", batchSize)
	fmt.Printf("  Server: %s\n", grpcAddr)
	fmt.Println()

	// Load test documents
	docs := loadDocuments()
	if len(docs) == 0 {
		fmt.Println("✗ No test documents found")
		os.Exit(1)
	}

	fmt.Printf("Document sizes:\n")
	for name, content := range docs {
		fmt.Printf("  %s: %d bytes\n", name, len(content))
	}
	fmt.Println()

	// Run test
	stats := runBatchTest(client, docs)

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

func runBatchTest(client pb.MDDBClient, docs map[string]string) Stats {
	var stats Stats
	stats.times = make([]time.Duration, 0, totalDocs/batchSize)

	fmt.Printf("Inserting %d documents in batches of %d...\n", totalDocs, batchSize)
	fmt.Println()

	startTotal := time.Now()
	docNum := 0

	// Prepare all documents
	allDocs := make([]*pb.BatchDocument, 0, totalDocs)
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

		allDocs = append(allDocs, &pb.BatchDocument{
			Key:       fmt.Sprintf("doc-%d", i+1),
			Lang:      "en_US",
			ContentMd: content,
			Meta:      make(map[string]*pb.MetaValues),
		})
	}

	// Send in batches
	for i := 0; i < len(allDocs); i += batchSize {
		end := i + batchSize
		if end > len(allDocs) {
			end = len(allDocs)
		}

		batch := allDocs[i:end]

		start := time.Now()
		resp, err := client.AddBatch(context.Background(), &pb.AddBatchRequest{
			Collection: collection,
			Documents:  batch,
		})
		elapsed := time.Since(start)

		if err != nil {
			fmt.Printf("✗ Batch failed: %v\n", err)
			continue
		}

		if resp.Failed > 0 {
			fmt.Printf("⚠ Batch partial failure: %d added, %d updated, %d failed\n", 
				resp.Added, resp.Updated, resp.Failed)
		}

		stats.times = append(stats.times, elapsed)
		docNum += int(resp.Added + resp.Updated)

		// Progress indicator
		if (i+batchSize)%1000 == 0 || end == len(allDocs) {
			avgMs := elapsed.Milliseconds()
			fmt.Printf("  Progress: %d/%d documents (%.1f%%) - batch: %dms\n", 
				docNum, totalDocs, float64(docNum)/float64(totalDocs)*100, avgMs)
		}
	}

	stats.total = time.Since(startTotal)

	// Calculate statistics
	if len(stats.times) > 0 {
		var sum time.Duration
		stats.min = stats.times[0]
		stats.max = stats.times[0]

		for _, t := range stats.times {
			sum += t
			if t < stats.min {
				stats.min = t
			}
			if t > stats.max {
				stats.max = t
			}
		}

		stats.avg = sum / time.Duration(len(stats.times))
	}

	fmt.Println()
	fmt.Println("✓ Completed")
	fmt.Println()

	return stats
}

func printStats(stats Stats) {
	fmt.Println("════════════════════════════════════════════════")
	fmt.Println("  Performance Test Results (gRPC Batch)")
	fmt.Println("════════════════════════════════════════════════")
	fmt.Println()
	fmt.Printf("Batches sent:       %d\n", len(stats.times))
	fmt.Printf("Total time:         %s\n", stats.total.Round(time.Millisecond))
	fmt.Printf("Average per batch:  %s\n", stats.avg.Round(time.Microsecond))
	fmt.Printf("Min batch time:     %s\n", stats.min.Round(time.Microsecond))
	fmt.Printf("Max batch time:     %s\n", stats.max.Round(time.Microsecond))
	fmt.Println()

	// Calculate per-document stats
	avgPerDoc := stats.total / time.Duration(totalDocs)
	throughput := float64(totalDocs) / stats.total.Seconds()

	fmt.Println("Overall:")
	fmt.Printf("  Total time: %s\n", stats.total.Round(time.Millisecond))
	fmt.Printf("  Average: %s\n", avgPerDoc.Round(time.Microsecond))
	fmt.Printf("  Throughput: %.2f docs/sec\n", throughput)
	fmt.Println()
}

func saveResults(stats Stats) {
	filename := "grpc-batch-performance-results.txt"
	f, err := os.Create(filename)
	if err != nil {
		fmt.Printf("Warning: Could not save results: %v\n", err)
		return
	}
	defer f.Close()

	fmt.Fprintf(f, "MDDB gRPC Batch Performance Test Results\n")
	fmt.Fprintf(f, "=========================================\n\n")
	fmt.Fprintf(f, "Test Date: %s\n\n", time.Now().Format("2006-01-02 15:04:05"))
	fmt.Fprintf(f, "Configuration:\n")
	fmt.Fprintf(f, "  Total documents: %d\n", totalDocs)
	fmt.Fprintf(f, "  Batch size: %d\n", batchSize)
	fmt.Fprintf(f, "  Batches sent: %d\n\n", len(stats.times))
	
	fmt.Fprintf(f, "Batch Statistics:\n")
	fmt.Fprintf(f, "  Total time: %s\n", stats.total.Round(time.Millisecond))
	fmt.Fprintf(f, "  Average per batch: %s\n", stats.avg.Round(time.Microsecond))
	fmt.Fprintf(f, "  Min batch time: %s\n", stats.min.Round(time.Microsecond))
	fmt.Fprintf(f, "  Max batch time: %s\n\n", stats.max.Round(time.Microsecond))

	avgPerDoc := stats.total / time.Duration(totalDocs)
	throughput := float64(totalDocs) / stats.total.Seconds()

	fmt.Fprintf(f, "Overall:\n")
	fmt.Fprintf(f, "  Total time: %s\n", stats.total.Round(time.Millisecond))
	fmt.Fprintf(f, "  Average: %s\n", avgPerDoc.Round(time.Microsecond))
	fmt.Fprintf(f, "  Throughput: %.2f docs/sec\n", throughput)

	fmt.Printf("✓ Results saved to %s\n", filename)
}
