package main

import (
	"fmt"
	"io"
)

// report prints the results as the markdown docs/BENCHMARK.md carries: one
// line per collection on how it was built, then one table row per search.
func report(w io.Writer, cfg config, sizes []sizeResult) {
	_, _ = fmt.Fprintf(w, "%d dimensions, %d topics, %d timed queries per search (%d warmup), topK 10, seed %d\n\n",
		cfg.Dims, cfg.Topics, cfg.Queries, cfg.Warmup, cfg.Seed)
	_, _ = fmt.Fprintln(w, "| Vectors | Filter admits | Load s | IVF train s |")
	_, _ = fmt.Fprintln(w, "|---------|---------------|--------|-------------|")
	for _, s := range sizes {
		_, _ = fmt.Fprintf(w, "| %d | %d (%.1f%%) | %.0f | %.1f |\n",
			s.Vectors, s.Rare, 100*float64(s.Rare)/float64(s.Vectors), s.LoadSecs, s.BuildSecs)
	}
	_, _ = fmt.Fprintln(w)
	_, _ = fmt.Fprintln(w, "| Vectors | Filter | algorithm | Ran (explain) | recall@10 | p50 ms | p99 ms |")
	_, _ = fmt.Fprintln(w, "|---------|--------|-----------|---------------|-----------|--------|--------|")
	for _, s := range sizes {
		for _, r := range s.Searches {
			filter := "none"
			if r.Filtered {
				filter = "slice=rare"
			}
			_, _ = fmt.Fprintf(w, "| %d | %s | `%s` | %s | %.3f | %.2f | %.2f |\n",
				s.Vectors, filter, r.Algorithm, r.Ran, r.Recall, r.P50, r.P99)
		}
	}
}
