package main

import (
	"context"
	"fmt"
)

// The advisor's heuristics read the collection's shape; an evaluation set
// says what actually works on it (SRCH-019). When a set exists, the advisor
// runs it and lets the measurement decide the search type.

// bm25Variants are the BM25 parameter changes the advisor tries (SRCH-018):
// less length normalisation, for documents of similar length; less and more
// term-frequency saturation, for very short and for long, repetitive
// documents. The advisor adopts one only when it scores better on the
// evaluation set; there are no thresholds on the collection's shape. On the
// MDDB docs set lower b and lower k1 both scored worse and k1=2 slightly
// better (docs/BENCHMARK.md) — which is why the shape alone cannot decide.
var bm25Variants = []struct{ k1, b float64 }{{1.2, 0.3}, {0.6, 0.75}, {2.0, 0.75}}

// measuredConfigs are what the advisor compares: keyword search with the
// default and the varied BM25 parameters always, the vector halves only when
// the server can embed a query.
func measuredConfigs(canEmbed bool) []EvalConfig {
	configs := []EvalConfig{{SearchType: SearchTypeFTS, Algorithm: "bm25"}, {SearchType: SearchTypeFTS, Algorithm: "bm25f"}}
	if canEmbed {
		configs = append(configs, EvalConfig{SearchType: SearchTypeHybrid}, EvalConfig{SearchType: SearchTypeVector})
	}
	for _, v := range bm25Variants {
		configs = append(configs, EvalConfig{SearchType: SearchTypeFTS, Algorithm: "bm25", BM25K1: floatPtr(v.k1), BM25B: floatPtr(v.b)})
	}
	return configs
}

// applyMeasurement runs the collection's evaluation set and, when it has one,
// replaces the guessed search type with the best measured configuration.
func (s *Server) applyMeasurement(rec *SearchRecommendation, collection string) {
	if len(s.evalSet(collection)) == 0 {
		return
	}
	res, err := s.RunEval(context.Background(), EvalRequest{Collection: collection, Configs: measuredConfigs(s.Embedding != nil)})
	if err != nil {
		rec.Warnings = append(rec.Warnings, "The evaluation set could not be run: "+err.Error())
		return
	}
	rec.Measured = res
	if best, cfg := bestMeasured(res, measuredConfigs(s.Embedding != nil)); best != nil {
		adoptMeasured(rec, res, best, cfg)
	}
}

// adoptMeasured makes the measured winner the recommendation.
func adoptMeasured(rec *SearchRecommendation, res *EvalResult, best *EvalConfigResult, cfg EvalConfig) {
	rec.SearchType = cfg.SearchType
	if cfg.Algorithm != "" {
		rec.FTSAlgorithm = cfg.Algorithm
	}
	if cfg.SearchType == SearchTypeHybrid && rec.HybridStrategy == "" {
		// What the measurement ran: the server's default fusion.
		rec.HybridStrategy, rec.HybridAlpha = "alpha", alphaOrDefault(nil)
	}
	rec.RetrievalProfile = rec.toProfile()
	// SRCH-018: BM25 parameters that won are stored with the profile.
	rec.RetrievalProfile.BM25K1, rec.RetrievalProfile.BM25B = cfg.BM25K1, cfg.BM25B
	rec.Reasons = append([]string{fmt.Sprintf("Measured on %d evaluation queries: %s scored nDCG@%d %.3f, recall %.3f — "+
		"the best of the configurations tried. This overrides the shape-based guess below.",
		res.Queries, best.Config, res.K, best.NDCG, best.Recall)}, rec.Reasons...)
}

// bestMeasured picks the highest nDCG; fewer returned tokens break a tie.
// Results come back in the order of configs.
func bestMeasured(res *EvalResult, configs []EvalConfig) (*EvalConfigResult, EvalConfig) {
	var best *EvalConfigResult
	var cfg EvalConfig
	for i, c := range configs {
		if i >= len(res.Results) {
			break
		}
		r := &res.Results[i]
		if r.Error != "" {
			continue
		}
		if best == nil || r.NDCG > best.NDCG || (r.NDCG == best.NDCG && r.Tokens < best.Tokens) {
			best, cfg = r, c
		}
	}
	return best, cfg
}
