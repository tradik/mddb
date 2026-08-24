// Command soak answers one question: does MDDB's memory grow without bound
// under sustained mixed traffic, or does it settle (GO-041)?
//
// The question comes from a downstream Windows fork, whose notes report RSS
// going from 42 MB to 153 MB under load. That number alone decides nothing.
// RSS includes bbolt's mmap of the database file, which grows with the data
// and is reclaimable page cache rather than a leak; it also includes caches
// that fill to their limit and stop. What separates those from a real leak is
// HeapInuse, which counts only what Go has allocated and not freed.
//
// So this samples both, plus goroutine count, and takes pprof heap profiles at
// the start, the middle and the end so the growth can be attributed rather
// than guessed at.
//
//	soak -url http://localhost:7890 -pid $(pgrep mddbd) -duration 45m
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

type sample struct {
	at         time.Duration
	rssBytes   uint64
	heapInuse  uint64
	sys        uint64
	alloc      uint64
	goroutines int
	ops        uint64
}

type systemInfo struct {
	MemoryUsed    uint64 `json:"memoryUsed"`
	MemorySystem  uint64 `json:"memorySystem"`
	MemoryHeap    uint64 `json:"memoryHeap"`
	NumGoroutines int    `json:"numGoroutines"`
}

func main() {
	url := flag.String("url", "http://localhost:7890", "MDDB base URL")
	pid := flag.Int("pid", 0, "server PID, for RSS from /proc (0 = skip RSS)")
	collection := flag.String("collection", "soak", "collection to work in")
	duration := flag.Duration("duration", 45*time.Minute, "how long to sustain traffic")
	interval := flag.Duration("interval", 30*time.Second, "sampling interval")
	workers := flag.Int("workers", 8, "concurrent clients")
	keyspace := flag.Int("keyspace", 5000, "distinct document keys to cycle through")
	outDir := flag.String("out", "soak-report", "directory for the CSV and pprof profiles")
	flag.Parse()

	if err := os.MkdirAll(*outDir, 0o750); err != nil {
		fail(err)
	}
	client := newClient(*workers * 2)
	if _, err := readSystemInfo(client, *url); err != nil {
		fail(fmt.Errorf("cannot reach %s: %w — start mddbd first", *url, err))
	}

	fmt.Printf("soak: seeding %d documents\n", *keyspace)
	if err := seed(client, *url, *collection, *keyspace); err != nil {
		fail(err)
	}
	fmt.Printf("soak: %s for %s, %d workers over %d keys\n", *url, *duration, *workers, *keyspace)
	if *pid == 0 {
		fmt.Println("soak: no -pid given, so RSS is not sampled — HeapInuse alone cannot tell mmap from heap")
	}

	var mix opMix
	stop := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < *workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			worker(client, *url, *collection, *keyspace, int64(i)+1, &mix, stop)
		}(i)
	}

	samples := collect(client, *url, *pid, *outDir, *duration, *interval, &mix)
	close(stop)
	wg.Wait()

	if err := writeCSV(filepath.Join(*outDir, "samples.csv"), samples); err != nil {
		fail(err)
	}
	report(samples, &mix, *outDir, *pid != 0)
}

// collect samples on a ticker and grabs heap profiles at the start, the middle
// and the end. Three profiles rather than two: a diff between the first and
// last cannot distinguish "grew early then settled" from "grew throughout",
// and those have opposite verdicts.
func collect(client *http.Client, url string, pid int, outDir string,
	duration, interval time.Duration, mix *opMix) []sample {

	start := time.Now()
	deadline := start.Add(duration)
	half := start.Add(duration / 2)
	tookMid := false

	heapProfile(client, url, filepath.Join(outDir, "heap-start.pprof"))

	var samples []sample
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for now := time.Now(); now.Before(deadline); now = time.Now() {
		<-ticker.C
		s := takeSample(client, url, pid, time.Since(start), mix)
		samples = append(samples, s)
		fmt.Printf("  %6s  rss=%7s heap=%7s sys=%7s goroutines=%4d ops=%d\n",
			s.at.Round(time.Second), human(s.rssBytes), human(s.heapInuse),
			human(s.sys), s.goroutines, s.ops)

		if !tookMid && time.Now().After(half) {
			heapProfile(client, url, filepath.Join(outDir, "heap-mid.pprof"))
			tookMid = true
		}
	}
	heapProfile(client, url, filepath.Join(outDir, "heap-end.pprof"))
	return samples
}

func takeSample(client *http.Client, url string, pid int, at time.Duration, mix *opMix) sample {
	s := sample{at: at, ops: mix.total()}
	if info, err := readSystemInfo(client, url); err == nil {
		s.heapInuse, s.sys, s.alloc, s.goroutines =
			info.MemoryHeap, info.MemorySystem, info.MemoryUsed, info.NumGoroutines
	}
	if pid != 0 {
		s.rssBytes = readRSS(pid)
	}
	return s
}

func readSystemInfo(client *http.Client, url string) (systemInfo, error) {
	var info systemInfo
	resp, err := client.Get(url + "/v1/system/info")
	if err != nil {
		return info, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return info, err
	}
	// The endpoint wraps its payload; try both shapes rather than assume.
	var wrapped struct {
		Data systemInfo `json:"data"`
	}
	if err := json.Unmarshal(body, &wrapped); err == nil && wrapped.Data.MemorySystem != 0 {
		return wrapped.Data, nil
	}
	return info, json.Unmarshal(body, &info)
}

// readRSS reads VmRSS from /proc, which is what "the process is using 153 MB"
// actually means. Returns 0 where /proc does not exist.
func readRSS(pid int) uint64 {
	b, err := os.ReadFile(filepath.Clean(fmt.Sprintf("/proc/%d/status", pid))) // #nosec G304 -- pid from a flag
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(b), "\n") {
		if !strings.HasPrefix(line, "VmRSS:") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			return 0
		}
		kb, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil {
			return 0
		}
		return kb * 1024
	}
	return 0
}

func heapProfile(client *http.Client, url, path string) {
	resp, err := client.Get(url + "/debug/pprof/heap?gc=1")
	if err != nil {
		fmt.Printf("soak: heap profile failed (%v) — is MDDB_PPROF_ENABLED=true?\n", err)
		return
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		fmt.Printf("soak: heap profile returned %d — is MDDB_PPROF_ENABLED=true?\n", resp.StatusCode)
		return
	}
	f, err := os.Create(filepath.Clean(path)) // #nosec G304 -- path from a flag
	if err != nil {
		fmt.Printf("soak: %v\n", err)
		return
	}
	defer func() { _ = f.Close() }()
	if _, err := io.Copy(f, resp.Body); err != nil {
		fmt.Printf("soak: %v\n", err)
	}
}
