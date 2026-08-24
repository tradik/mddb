package main

import (
	"encoding/csv"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
)

func writeCSV(path string, samples []sample) error {
	f, err := os.Create(filepath.Clean(path)) // #nosec G304 -- path from a flag
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	w := csv.NewWriter(f)
	defer w.Flush()
	if err := w.Write([]string{"seconds", "rss_bytes", "heap_inuse_bytes", "sys_bytes", "alloc_bytes", "goroutines", "ops"}); err != nil {
		return err
	}
	for _, s := range samples {
		if err := w.Write([]string{
			strconv.Itoa(int(s.at.Seconds())),
			strconv.FormatUint(s.rssBytes, 10),
			strconv.FormatUint(s.heapInuse, 10),
			strconv.FormatUint(s.sys, 10),
			strconv.FormatUint(s.alloc, 10),
			strconv.Itoa(s.goroutines),
			strconv.FormatUint(s.ops, 10),
		}); err != nil {
			return err
		}
	}
	return nil
}

func human(b uint64) string {
	switch {
	case b == 0:
		return "-"
	case b < 1<<20:
		return fmt.Sprintf("%dK", b>>10)
	default:
		return fmt.Sprintf("%dM", b>>20)
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "soak:", err)
	os.Exit(1)
}

// report turns the samples into the verdict GO-041 asks for.
//
// The distinction that matters: HeapInuse is what Go has allocated and not
// returned. RSS is that plus the runtime's own overhead plus bbolt's mmap of
// the database file. A database that grows during the run pulls RSS up through
// the mmap alone, and those pages are reclaimable — the kernel drops them under
// pressure. Reading RSS as "the leak" is the mistake this tool exists to avoid.
//
// So the heap is judged on its last quarter against its first, after the caches
// have had time to fill. Growth confined to the early part is a cache reaching
// its limit; growth that continues to the end is not.
func report(samples []sample, mix *opMix, outDir string, haveRSS bool) {
	if len(samples) < 8 {
		fmt.Printf("\nsoak: %s\n", mix)
		fmt.Printf("soak: only %d samples — too few to judge a trend. Run longer.\n", len(samples))
		return
	}

	q := len(samples) / 4
	firstHeap := medianHeap(samples[:q])
	lastHeap := medianHeap(samples[len(samples)-q:])
	firstGor := samples[q/2].goroutines
	lastGor := samples[len(samples)-1].goroutines

	fmt.Printf("\n── soak result ──\n")
	fmt.Printf("duration      %s over %d samples\n", samples[len(samples)-1].at.Round(1e9), len(samples))
	fmt.Printf("operations    %s\n", mix)
	fmt.Printf("heap inuse    %s → %s (median of first and last quarter)\n",
		human(firstHeap), human(lastHeap))
	if haveRSS {
		fmt.Printf("rss           %s → %s\n", human(samples[0].rssBytes), human(samples[len(samples)-1].rssBytes))
	}
	fmt.Printf("goroutines    %d → %d\n", firstGor, lastGor)
	fmt.Printf("profiles      %s/heap-{start,mid,end}.pprof\n", outDir)
	fmt.Printf("\ncompare with:\n  go tool pprof -base %s/heap-start.pprof %s/heap-end.pprof\n", outDir, outDir)

	fmt.Printf("\n── reading ──\n")
	switch growth := ratio(firstHeap, lastHeap); {
	case growth > 1.5:
		fmt.Printf("heap grew %.0f%% between the first and last quarter, after the caches\n"+
			"had time to fill. That is not a cache settling. Take the pprof diff to a bug\n"+
			"report rather than closing this as expected.\n", (growth-1)*100)
	case growth > 1.15:
		fmt.Printf("heap grew %.0f%%, which is more than noise and less than a leak.\n"+
			"Check the pprof diff before deciding; a cache with a generous limit looks\n"+
			"like this, and so does a slow leak measured over too short a run.\n", (growth-1)*100)
	default:
		fmt.Printf("heap is flat within %.0f%% between the first and last quarter — the\n"+
			"allocation side is in steady state.\n", (growth-1)*100)
		if haveRSS && ratio(samples[0].rssBytes, samples[len(samples)-1].rssBytes) > 1.3 {
			fmt.Printf("RSS rose while the heap did not, which is what a growing bbolt mmap\n" +
				"looks like: file-backed, reclaimable, and not a leak.\n")
		}
	}
	if lastGor > firstGor+20 {
		fmt.Printf("\ngoroutines rose from %d to %d — that is its own finding, independent\n"+
			"of the heap.\n", firstGor, lastGor)
	}
}

func medianHeap(s []sample) uint64 {
	if len(s) == 0 {
		return 0
	}
	v := make([]uint64, len(s))
	for i := range s {
		v[i] = s[i].heapInuse
	}
	for i := 1; i < len(v); i++ {
		for j := i; j > 0 && v[j] < v[j-1]; j-- {
			v[j], v[j-1] = v[j-1], v[j]
		}
	}
	return v[len(v)/2]
}

func ratio(from, to uint64) float64 {
	if from == 0 {
		return 1
	}
	return float64(to) / float64(from)
}
