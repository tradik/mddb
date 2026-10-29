package main

import (
	"math"
	"math/rand/v2"
	"net/http"
	"sort"
)

// metrics is one query's score under one configuration.
type metrics struct {
	Recall, NDCG, RR float64
}

// scoreQuery computes recall@k, nDCG@k and the reciprocal rank with the
// formulas of the server's POST /v1/eval (services/mddbd/eval.go). That code
// is in the server's package main and cannot be imported; the baselines are
// re-scored here too, so every row of a table uses this one function.
func scoreQuery(q evalQuery, returned []string, k int) metrics {
	if len(returned) > k {
		returned = returned[:k]
	}
	grade := func(key string) int {
		if g, ok := q.Grades[key]; ok {
			return g
		}
		if contains(q.ExpectedKeys, key) {
			return 1
		}
		return 0
	}
	var m metrics
	found, dcg := 0, 0.0
	for i, key := range returned {
		g := grade(key)
		if g > 0 {
			found++
			if m.RR == 0 {
				m.RR = 1 / float64(i+1)
			}
		}
		dcg += (math.Pow(2, float64(g)) - 1) / math.Log2(float64(i+2))
	}
	ideal := make([]int, 0, len(q.ExpectedKeys))
	for _, key := range q.ExpectedKeys {
		ideal = append(ideal, grade(key))
	}
	sort.Sort(sort.Reverse(sort.IntSlice(ideal)))
	idcg := 0.0
	for i, g := range ideal[:min(len(ideal), k)] {
		idcg += (math.Pow(2, float64(g)) - 1) / math.Log2(float64(i+2))
	}
	m.Recall = float64(found) / float64(len(q.ExpectedKeys))
	if idcg > 0 {
		m.NDCG = dcg / idcg
	}
	return m
}

// maxSimRank scores each document by the mean over the query parts of the
// part's best chunk, and returns the top k keys. Mean and sum rank alike: a
// query has the same number of parts for every document. bestPart takes the
// best part's score instead (fusion, not MaxSim).
func maxSimRank(parts []partScores, k int, bestPart bool) []string {
	score := map[string]float64{}
	for _, p := range parts {
		for key, s := range p.Best {
			if bestPart {
				if cur, ok := score[key]; !ok || s > cur {
					score[key] = s
				}
				continue
			}
			score[key] += s / float64(len(parts))
		}
	}
	keys := make([]string, 0, len(score))
	for key := range score {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if score[keys[i]] != score[keys[j]] {
			return score[keys[i]] > score[keys[j]]
		}
		return keys[i] < keys[j]
	})
	return keys[:min(k, len(keys))]
}

// row is one configuration over one query set.
type row struct {
	Name     string
	PerQuery []metrics
	P50, P99 float64
	Calls    float64 // embedding calls per query, mean
	Server   bool    // latency measured by the server (eval), not round trips
}

// setResult is one query set's rows; Agree counts the queries whose top k
// from the client path equals the server's vector search exactly.
type setResult struct {
	Name    string
	Queries int
	Rows    []row
	Agree   int
}

// chunkStats remembers the collection's chunk count, seen on any fetch.
type chunkStats struct{ Chunks int }

// measureSet scores the server baselines and every MaxSim variant on qs.
// Each distinct part is fetched once; its round trip counts towards every
// query that uses it, as if issued one after another.
func measureSet(client *http.Client, cfg config, s querySet, stats *chunkStats) (setResult, error) {
	res := setResult{Name: s.Name, Queries: len(s.Queries)}
	bases, err := evalBaselines(client, cfg, s.Queries)
	if err != nil {
		return res, err
	}
	for _, b := range bases {
		r := row{Name: b.Name + " (server)", P50: b.P50, P99: b.P99, Calls: 1, Server: true}
		for i, q := range s.Queries {
			r.PerQuery = append(r.PerQuery, scoreQuery(q, b.Returned[i], cfg.K))
		}
		res.Rows = append(res.Rows, r)
	}
	cache := map[string]partScores{}
	for vi, v := range variants(cfg.Window, cfg.Stride) {
		r := row{Name: v.Name}
		var took []float64
		for qi, q := range s.Queries {
			texts := v.Parts(q.Query)
			parts := make([]partScores, 0, len(texts))
			ms := 0.0
			for _, t := range texts {
				p, ok := cache[t]
				if !ok {
					if p, err = fetchPart(client, cfg, t); err != nil {
						return res, err
					}
					cache[t], stats.Chunks = p, p.Chunks
				}
				parts, ms = append(parts, p), ms+p.Ms
			}
			ranked := maxSimRank(parts, cfg.K, v.BestPart)
			if vi == 0 && equal(ranked, bases[0].Returned[qi][:min(cfg.K, len(bases[0].Returned[qi]))]) {
				res.Agree++
			}
			r.PerQuery = append(r.PerQuery, scoreQuery(q, ranked, cfg.K))
			took, r.Calls = append(took, ms), r.Calls+float64(len(texts))/float64(len(s.Queries))
		}
		r.P50, r.P99 = percentile(took, 50), percentile(took, 99)
		res.Rows = append(res.Rows, r)
	}
	return res, nil
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func percentile(xs []float64, p int) float64 {
	if len(xs) == 0 {
		return 0
	}
	s := append([]float64(nil), xs...)
	sort.Float64s(s)
	return s[min(len(s)-1, len(s)*p/100)]
}

// meanOf averages one metric over the queries.
func meanOf(ms []metrics, f func(metrics) float64) float64 {
	sum := 0.0
	for _, m := range ms {
		sum += f(m)
	}
	return sum / float64(max(len(ms), 1))
}

// pairedCI is the 95% bootstrap interval of mean(f(a) - f(b)), resampling
// queries with replacement — the same queries on both sides, so the noise
// between queries cancels and only the difference is tested.
func pairedCI(a, b []metrics, f func(metrics) float64, n int, seed int64) (lo, hi float64) {
	if len(a) == 0 || n <= 0 {
		return 0, 0
	}
	rng := rand.New(rand.NewPCG(uint64(seed), 0x5eed)) // #nosec G404 G115 -- reproducible resampling, not security
	means := make([]float64, n)
	for i := range means {
		sum := 0.0
		for range a {
			j := rng.IntN(len(a))
			sum += f(a[j]) - f(b[j])
		}
		means[i] = sum / float64(len(a))
	}
	sort.Float64s(means)
	return means[n*25/1000], means[min(n-1, n*975/1000)]
}
