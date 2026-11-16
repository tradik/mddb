package main

import (
	"context"
	"fmt"
)

// The Search Advisor and model reranking (RAG-011).
//
// A reranking model costs a provider call per search, so the advisor never
// turns it on from the collection's shape. With a model configured and an
// evaluation set to measure on, it runs the recommended configuration with
// and without reranking and recommends the stage only when nDCG improves with
// every query reranked; the reason states the gain and the p95 it costs.
// Without a set it says the model is there and how to measure it.

// adviseRerank adds the reranking model to a recommendation.
func (s *Server) adviseRerank(rec *SearchRecommendation, collection string) {
	setup := s.rerankConfigured()
	if setup == nil {
		return
	}
	rec.RerankModel = setup.provider.Model()
	if rec.Measured == nil {
		rec.Reasons = append(rec.Reasons, fmt.Sprintf("A reranking model (%s) is configured. It is off unless asked for: "+
			"add an evaluation set and ask again, or compare configurations with and without rerank on POST /v1/eval, "+
			"before turning it on in the retrieval profile.", rec.RerankModel))
		return
	}
	off, on := rec.evalConfig(false), rec.evalConfig(true)
	res, err := s.RunEval(context.Background(), EvalRequest{Collection: collection, Configs: []EvalConfig{off, on}})
	if err != nil {
		rec.Warnings = append(rec.Warnings, "The reranking comparison could not be run: "+err.Error())
		return
	}
	adoptRerank(rec, res)
}

// evalConfig is the recommendation as an evaluation configuration, with the
// reranking model on or off.
func (rec *SearchRecommendation) evalConfig(rerank bool) EvalConfig {
	c := EvalConfig{SearchType: rec.SearchType, Rerank: &RerankOptions{Enabled: &rerank}}
	if rec.SearchType != SearchTypeVector {
		c.Algorithm = rec.FTSAlgorithm
	}
	if p := rec.RetrievalProfile; p != nil {
		c.BM25K1, c.BM25B = p.BM25K1, p.BM25B
	}
	return c
}

// adoptRerank recommends the stage when the measurement says it helps.
func adoptRerank(rec *SearchRecommendation, res *EvalResult) {
	off, on := res.Results[0], res.Results[1]
	switch {
	case off.Error != "" || on.Error != "":
		rec.Warnings = append(rec.Warnings, "The reranking comparison failed: "+off.Error+on.Error)
	case on.RerankSkipped > 0:
		rec.Warnings = append(rec.Warnings, fmt.Sprintf("The reranking model was skipped on %d of %d evaluation queries "+
			"(timeout, error or budget), so its effect could not be measured.", on.RerankSkipped, res.Queries))
	case on.NDCG > off.NDCG:
		enabled := true
		rec.Rerank = &RerankOptions{Enabled: &enabled}
		if rec.RetrievalProfile != nil {
			rec.RetrievalProfile.Rerank = rec.Rerank
		}
		rec.Reasons = append(rec.Reasons, fmt.Sprintf("Reranking with %s raised nDCG@%d from %.3f to %.3f on %d evaluation "+
			"queries, at a p95 of %.1f ms against %.1f ms without it.", rec.RerankModel, res.K, off.NDCG, on.NDCG,
			res.Queries, on.P95Ms, off.P95Ms))
	default:
		rec.Reasons = append(rec.Reasons, fmt.Sprintf("Reranking with %s did not improve nDCG@%d on the evaluation set "+
			"(%.3f with, %.3f without), so it stays off.", rec.RerankModel, res.K, on.NDCG, off.NDCG))
	}
}
