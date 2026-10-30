package main

import (
	"fmt"
	"io"
)

// report prints each query set as the markdown table docs/BENCHMARK.md
// carries. Differences are against "max, whole query" — today's scoring on
// the same client path — so a row's delta is the aggregation alone; a
// server row with queryDecomposition auto is compared with the same search
// without it.
func report(w io.Writer, cfg config, stats *chunkStats, results []setResult) {
	_, _ = fmt.Fprintf(w, "collection %q, %d chunks, k=%d, windows of %d words every %d, %d bootstrap resamples, seed %d\n",
		cfg.Collection, stats.Chunks, cfg.K, cfg.Window, cfg.Stride, cfg.Boot, cfg.Seed)
	ndcg := func(m metrics) float64 { return m.NDCG }
	recall := func(m metrics) float64 { return m.Recall }
	for _, res := range results {
		_, _ = fmt.Fprintf(w, "\n### %s: %d queries\n\n", res.Name, res.Queries)
		_, _ = fmt.Fprintf(w, "Client path agrees with the server's vector top %d on %d of %d queries.\n\n", cfg.K, res.Agree, res.Queries)
		_, _ = fmt.Fprintf(w, "| Scoring | recall@%d | nDCG@%d | MRR | Δ nDCG [95%% CI] | Δ recall [95%% CI] | better / worse | embed calls | p50 ms | p99 ms |\n", cfg.K, cfg.K)
		_, _ = fmt.Fprintln(w, "|---|---|---|---|---|---|---|---|---|---|")
		for _, r := range res.Rows {
			base := baseRow(res.Rows, r.Base)
			nLo, nHi := pairedCI(r.PerQuery, base.PerQuery, ndcg, cfg.Boot, cfg.Seed)
			rLo, rHi := pairedCI(r.PerQuery, base.PerQuery, recall, cfg.Boot, cfg.Seed)
			better, worse := tally(r.PerQuery, base.PerQuery)
			lat := "round trips"
			if r.Server {
				lat = "server"
			}
			_, _ = fmt.Fprintf(w, "| %s | %.3f | %.3f | %.3f | %+.3f [%+.3f, %+.3f] | %+.3f [%+.3f, %+.3f] | %d / %d | %.1f | %.1f | %.1f (%s) |\n",
				r.Name, meanOf(r.PerQuery, recall), meanOf(r.PerQuery, ndcg), meanOf(r.PerQuery, func(m metrics) float64 { return m.RR }),
				meanOf(r.PerQuery, ndcg)-meanOf(base.PerQuery, ndcg), nLo, nHi,
				meanOf(r.PerQuery, recall)-meanOf(base.PerQuery, recall), rLo, rHi,
				better, worse, r.Calls, r.P50, r.P99, lat)
		}
	}
}

// baseRow is the row a row is compared with: the one named base, or
// "max, whole query" — today's scoring on the client path.
func baseRow(rows []row, base string) row {
	if base == "" {
		base = "max, whole query"
	}
	for _, r := range rows {
		if r.Name == base {
			return r
		}
	}
	return rows[0]
}

// tally counts the queries whose nDCG went up and down.
func tally(a, b []metrics) (better, worse int) {
	for i := range a {
		switch {
		case a[i].NDCG > b[i].NDCG+1e-9:
			better++
		case a[i].NDCG < b[i].NDCG-1e-9:
			worse++
		}
	}
	return better, worse
}
