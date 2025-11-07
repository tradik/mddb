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
	batchSize  = 100
	collection = "perftest-grpc"
)

type Stats struct {
	times []time.Duration
	total time.Duration
	min   time.Duration
	max   time.Duration
	avg   time.Duration
}

func (s *Stats) add(d time.Duration) {
	s.times = append(s.times, d)
	s.total += d
	if s.min == 0 || d < s.min {
		s.min = d
	}
	if d > s.max {
		s.max = d
	}
}

func (s *Stats) calculate() {
	if len(s.times) > 0 {
		s.avg = s.total / time.Duration(len(s.times))
	}
}

func main() {
	fmt.Println("\033[34m════════════════════════════════════════════════\033[0m")
	fmt.Println("\033[32m  MDDB gRPC Performance Test\033[0m")
	fmt.Println("\033[34m════════════════════════════════════════════════\033[0m")
	fmt.Println()

	// Connect to gRPC server
	fmt.Println("\033[36mConnecting to gRPC server...\033[0m")
	conn, err := grpc.Dial(grpcAddr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(),
	)
	if err != nil {
		fmt.Printf("\033[31m✗ Failed to connect: %v\033[0m\n", err)
		os.Exit(1)
	}
	defer conn.Close()

	client := pb.NewMDDBClient(conn)
	fmt.Println("\033[32m✓ Connected to gRPC server\033[0m")
	fmt.Println()

	// Load test files
	shortContent, _ := ioutil.ReadFile("lorem-short.md")
	mediumContent, _ := ioutil.ReadFile("lorem-medium.md")
	longContent, _ := ioutil.ReadFile("lorem-long.md")

	fmt.Println("\033[36mTest Configuration:\033[0m")
	fmt.Printf("  Collection: %s\n", collection)
	fmt.Printf("  Total documents: %d\n", totalDocs)
	fmt.Printf("  Batch size: %d\n", batchSize)
	fmt.Printf("  Server: %s\n", grpcAddr)
	fmt.Println()
	fmt.Println("\033[36mDocument sizes:\033[0m")
	fmt.Printf("  Short:  %d bytes\n", len(shortContent))
	fmt.Printf("  Medium: %d bytes\n", len(mediumContent))
	fmt.Printf("  Long:   %d bytes\n", len(longContent))
	fmt.Println()

	ctx := context.Background()

	// Test 1: Short documents
	fmt.Println("\033[35mTest 1: Adding", totalDocs, "short documents\033[0m")
	fmt.Println("\033[36mProgress:\033[0m")
	statsShort := testDocuments(ctx, client, "short", string(shortContent), totalDocs)
	fmt.Println("\033[32m✓ Completed\033[0m")
	fmt.Println()

	// Test 2: Medium documents
	fmt.Println("\033[35mTest 2: Adding", totalDocs, "medium documents\033[0m")
	fmt.Println("\033[36mProgress:\033[0m")
	statsMedium := testDocuments(ctx, client, "medium", string(mediumContent), totalDocs)
	fmt.Println("\033[32m✓ Completed\033[0m")
	fmt.Println()

	// Test 3: Long documents
	fmt.Println("\033[35mTest 3: Adding", totalDocs, "long documents\033[0m")
	fmt.Println("\033[36mProgress:\033[0m")
	statsLong := testDocuments(ctx, client, "long", string(longContent), totalDocs)
	fmt.Println("\033[32m✓ Completed\033[0m")
	fmt.Println()

	// Get server stats
	fmt.Println("\033[36mFetching server statistics...\033[0m")
	serverStats, _ := client.Stats(ctx, &pb.StatsRequest{})
	fmt.Println()

	// Print results
	fmt.Println("\033[34m════════════════════════════════════════════════\033[0m")
	fmt.Println("\033[32m  Performance Test Results (gRPC)\033[0m")
	fmt.Println("\033[34m════════════════════════════════════════════════\033[0m")
	fmt.Println()

	printResults("Short Documents", len(shortContent), statsShort)
	printResults("Medium Documents", len(mediumContent), statsMedium)
	printResults("Long Documents", len(longContent), statsLong)

	// Overall statistics
	totalTime := statsShort.total + statsMedium.total + statsLong.total
	totalCount := len(statsShort.times) + len(statsMedium.times) + len(statsLong.times)
	avgTime := totalTime / time.Duration(totalCount)
	throughput := float64(totalCount) / totalTime.Seconds()

	fmt.Println("\033[35mOverall Statistics:\033[0m")
	fmt.Printf("  Total documents:    %d\n", totalCount)
	fmt.Printf("  Total time:         %v (%.2fs)\n", totalTime, totalTime.Seconds())
	fmt.Printf("  Average per doc:    %v\n", avgTime)
	fmt.Printf("  Overall throughput: %.2f docs/sec\n", throughput)
	fmt.Println()

	// Server stats
	if serverStats != nil {
		fmt.Println("\033[35mServer Statistics:\033[0m")
		fmt.Printf("  Database size:      %.2fMB\n", float64(serverStats.DatabaseSize)/1024/1024)
		fmt.Printf("  Total documents:    %d\n", serverStats.TotalDocuments)
		fmt.Printf("  Total revisions:    %d\n", serverStats.TotalRevisions)
		fmt.Printf("  Collections:        %d\n", len(serverStats.Collections))
		fmt.Println()
	}

	fmt.Println("\033[34m════════════════════════════════════════════════\033[0m")
	fmt.Println("\033[32m  Test completed successfully!\033[0m")
	fmt.Println("\033[34m════════════════════════════════════════════════\033[0m")
	fmt.Println()

	// Save results
	saveResults("grpc-performance-results.txt", statsShort, statsMedium, statsLong, totalTime, totalCount)
}

func testDocuments(ctx context.Context, client pb.MDDBClient, size, content string, count int) Stats {
	stats := Stats{}

	for i := 1; i <= count; i++ {
		key := fmt.Sprintf("doc-%s-%d", size, i)

		start := time.Now()
		_, err := client.Add(ctx, &pb.AddRequest{
			Collection: collection,
			Key:        key,
			Lang:       "en_US",
			Meta: map[string]*pb.MetaValues{
				"category": {Values: []string{"test"}},
				"size":     {Values: []string{size}},
				"batch":    {Values: []string{fmt.Sprintf("%d", i/batchSize)}},
			},
			ContentMd: content,
		})
		elapsed := time.Since(start)

		if err != nil {
			fmt.Printf("\033[31m✗ Error adding document: %v\033[0m\n", err)
			continue
		}

		stats.add(elapsed)

		// Progress indicator
		if i%batchSize == 0 {
			progress := i * 100 / count
			avgMs := stats.total.Milliseconds() / int64(i)
			fmt.Printf("\r  [%-50s] %d%% (%d/%d docs, avg: %dms)",
				progressBar(progress),
				progress, i, count, avgMs)
		}
	}
	fmt.Println()

	stats.calculate()
	return stats
}

func progressBar(percent int) string {
	filled := percent / 2
	bar := ""
	for i := 0; i < 50; i++ {
		if i < filled {
			bar += "#"
		} else {
			bar += " "
		}
	}
	return bar
}

func printResults(label string, size int, stats Stats) {
	throughput := float64(len(stats.times)) / stats.total.Seconds()

	fmt.Printf("\033[35m%s (%d bytes):\033[0m\n", label, size)
	fmt.Printf("  Documents:      %d\n", len(stats.times))
	fmt.Printf("  Total time:     %v (%.2fs)\n", stats.total, stats.total.Seconds())
	fmt.Printf("  Average:        %v per document\n", stats.avg)
	fmt.Printf("  Min:            %v\n", stats.min)
	fmt.Printf("  Max:            %v\n", stats.max)
	fmt.Printf("  Throughput:     %.2f docs/sec\n", throughput)
	fmt.Println()
}

func saveResults(filename string, short, medium, long Stats, totalTime time.Duration, totalCount int) {
	f, err := os.Create(filename)
	if err != nil {
		return
	}
	defer f.Close()

	fmt.Fprintf(f, "MDDB gRPC Performance Test Results\n")
	fmt.Fprintf(f, "===================================\n")
	fmt.Fprintf(f, "Date: %s\n", time.Now().Format(time.RFC3339))
	fmt.Fprintf(f, "Server: %s\n\n", grpcAddr)

	fmt.Fprintf(f, "Short Documents:\n")
	fmt.Fprintf(f, "  Average: %v\n", short.avg)
	fmt.Fprintf(f, "  Min: %v\n", short.min)
	fmt.Fprintf(f, "  Max: %v\n", short.max)
	fmt.Fprintf(f, "  Throughput: %.2f docs/sec\n\n", float64(len(short.times))/short.total.Seconds())

	fmt.Fprintf(f, "Medium Documents:\n")
	fmt.Fprintf(f, "  Average: %v\n", medium.avg)
	fmt.Fprintf(f, "  Min: %v\n", medium.min)
	fmt.Fprintf(f, "  Max: %v\n", medium.max)
	fmt.Fprintf(f, "  Throughput: %.2f docs/sec\n\n", float64(len(medium.times))/medium.total.Seconds())

	fmt.Fprintf(f, "Long Documents:\n")
	fmt.Fprintf(f, "  Average: %v\n", long.avg)
	fmt.Fprintf(f, "  Min: %v\n", long.min)
	fmt.Fprintf(f, "  Max: %v\n", long.max)
	fmt.Fprintf(f, "  Throughput: %.2f docs/sec\n\n", float64(len(long.times))/long.total.Seconds())

	fmt.Fprintf(f, "Overall:\n")
	fmt.Fprintf(f, "  Total time: %v\n", totalTime)
	fmt.Fprintf(f, "  Average: %v\n", totalTime/time.Duration(totalCount))
	fmt.Fprintf(f, "  Throughput: %.2f docs/sec\n", float64(totalCount)/totalTime.Seconds())

	fmt.Printf("\033[32mResults saved to: %s\033[0m\n", filename)
}
