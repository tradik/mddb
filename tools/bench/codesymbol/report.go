package main

import (
	"fmt"
	"io"

	"mddb-bench/internal/benchkit"
)

// modeSummary aggregates one mode's scores over a set of symbols.
type modeSummary struct {
	// Precision and Recall pool every reference (micro average), so a symbol
	// with 300 references weighs 300 times one with one.
	Precision float64 `json:"precision"`
	Recall    float64 `json:"recall"`
	// MacroPrecision and MacroRecall average per symbol, over the symbols
	// that returned something and that the truth has references for.
	MacroPrecision float64 `json:"macroPrecision"`
	MacroRecall    float64 `json:"macroRecall"`
	Exact          int     `json:"exact"`      // symbols answered with exactly the truth's references
	ImplsExact     int     `json:"implsExact"` // symbols whose implementation count equals the truth's
	TokensMean     float64 `json:"tokensMean"`
	TokensMedian   float64 `json:"tokensMedian"`
	LatencyP50Ms   float64 `json:"latencyP50Ms"`
	LatencyP95Ms   float64 `json:"latencyP95Ms"`
	Truncated      int     `json:"truncated"`
	Returned       int     `json:"returned"`
	Correct        int     `json:"correct"`
	Truth          int     `json:"truth"`
}

func summarize(rs []symbolResult, pick func(*symbolResult) (score, []int64)) modeSummary {
	var m modeSummary
	var precs, recs, toks, lat []float64
	for i := range rs {
		sc, ms := pick(&rs[i])
		m.Returned += sc.Returned
		m.Correct += sc.Correct
		m.Truth += sc.Truth
		if sc.Returned > 0 {
			precs = append(precs, float64(sc.Correct)/float64(sc.Returned))
		}
		if sc.Truth > 0 {
			recs = append(recs, float64(sc.Correct)/float64(sc.Truth))
		}
		if sc.Correct == sc.Truth && sc.Returned == sc.Truth {
			m.Exact++
		}
		if sc.Impls == rs[i].TruthImpls {
			m.ImplsExact++
		}
		if sc.Truncated {
			m.Truncated++
		}
		toks = append(toks, float64(sc.Tokens))
		for _, us := range ms {
			lat = append(lat, float64(us)/1000)
		}
	}
	m.Precision, m.Recall = ratio(m.Correct, m.Returned), ratio(m.Correct, m.Truth)
	m.MacroPrecision, m.MacroRecall = benchkit.Mean(precs), benchkit.Mean(recs)
	m.TokensMean, m.TokensMedian = benchkit.Mean(toks), benchkit.Percentile(toks, 50)
	m.LatencyP50Ms, m.LatencyP95Ms = benchkit.Percentile(lat, 50), benchkit.Percentile(lat, 95)
	return m
}

// ratio is a/b, and 1 for 0/0: nothing returned of nothing is not an error.
func ratio(a, b int) float64 {
	if b == 0 {
		return 1
	}
	return float64(a) / float64(b)
}

// group is one row of the table: a stratum or all symbols.
type group struct {
	Name      string      `json:"name"`
	Symbols   int         `json:"symbols"`
	Heuristic modeSummary `json:"heuristic"`
	Semantic  modeSummary `json:"semantic"`
}

func heuristicOf(r *symbolResult) (score, []int64) { return r.Heuristic, r.HeurMs }
func semanticOf(r *symbolResult) (score, []int64)  { return r.Semantic, r.SemMs }

// groups summarizes per stratum and over all symbols.
func groups(rs []symbolResult) []group {
	by := map[string][]symbolResult{}
	for _, r := range rs {
		by[r.Stratum] = append(by[r.Stratum], r)
	}
	by["all"] = rs
	var out []group
	for _, name := range []string{stratumFunction, stratumAmbiguous, stratumInterface, stratumMethod, "all"} {
		if list := by[name]; len(list) > 0 {
			out = append(out, group{Name: name, Symbols: len(list),
				Heuristic: summarize(list, heuristicOf), Semantic: summarize(list, semanticOf)})
		}
	}
	return out
}

// printTable writes the Markdown table docs/BENCHMARK.md carries.
func printTable(w io.Writer, rep *report) {
	_, _ = fmt.Fprintf(w, "truth: %s, %d symbols sampled (seed %d)\n\n", rep.Server, rep.Sampled, rep.Seed)
	_, _ = fmt.Fprintln(w, "| Symbols | n | Mode | Precision | Recall | Exact | Impls = truth | Tokens (median) | p50 / p95 ms |")
	_, _ = fmt.Fprintln(w, "|---|---:|---|---:|---:|---:|---:|---:|---:|")
	for _, g := range rep.Groups {
		for _, m := range []struct {
			name string
			s    modeSummary
		}{{"heuristic", g.Heuristic}, {"semantic", g.Semantic}} {
			_, _ = fmt.Fprintf(w, "| %s | %d | %s | %.1f%% (%d/%d) | %.1f%% (%d/%d) | %d | %d | %.0f | %.1f / %.1f |\n",
				g.Name, g.Symbols, m.name, 100*m.s.Precision, m.s.Correct, m.s.Returned, 100*m.s.Recall, m.s.Correct,
				m.s.Truth, m.s.Exact, m.s.ImplsExact, m.s.TokensMedian, m.s.LatencyP50Ms, m.s.LatencyP95Ms)
		}
	}
	if rep.Meta != nil {
		_, _ = fmt.Fprintf(w, "\nfacts: %d documents, sym* bytes median %d, p99 %d, max %d, %d over 64 KiB, %d truncated\n",
			rep.Meta.Documents, rep.Meta.MedianBytes, rep.Meta.P99Bytes, rep.Meta.MaxBytes, rep.Meta.Over64KiB, rep.Meta.Truncated)
	}
	for _, d := range rep.Search {
		_, _ = fmt.Fprintf(w, "search %s, %s: %d/%d queries byte-identical, %d/%d with the same results (labels aside)\n",
			d.Endpoint, d.Compared, d.Identical, d.Queries, d.SameResults, d.Queries)
		for _, q := range d.Differ {
			_, _ = fmt.Fprintf(w, "  differs: %q\n", q)
		}
		for _, e := range d.Errors {
			_, _ = fmt.Fprintf(w, "  error: %s\n", e)
		}
	}
}
