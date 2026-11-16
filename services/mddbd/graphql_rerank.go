package main

import gql "mddb/graphql"

// Model reranking over GraphQL (RAG-011): the input and the report.

// rerankFromGQL reads a GraphQL rerank input; nil is a request without one.
func rerankFromGQL(in *gql.RerankInput) RerankRequest {
	if in == nil {
		return RerankRequest{}
	}
	return RerankRequest{Rerank: &RerankOptions{Enabled: in.Enabled, Candidates: derefInt(in.Candidates, 0),
		TopN: derefInt(in.TopN, 0), Explain: derefBool(in.Explain, false)}}
}

// rerankToGQL renders what a response says about the stage: the report when
// it ran, the reason when it was skipped, both nil when it was not asked for.
func rerankToGQL(r RerankResult) (*gql.RerankReport, *string) {
	var skipped *string
	if r.RerankSkipped != "" {
		reason := r.RerankSkipped
		skipped = &reason
	}
	if r.Rerank == nil {
		return nil, skipped
	}
	out := &gql.RerankReport{Model: r.Rerank.Model, Candidates: r.Rerank.Candidates, Cached: r.Rerank.Cached,
		DurationMs: r.Rerank.DurationMs}
	for _, m := range r.Rerank.Moves {
		mv := &gql.RerankMove{ID: m.ID, Before: m.Before, After: m.After, Score: m.Score}
		if m.Collection != "" {
			c := m.Collection
			mv.Collection = &c
		}
		out.Moves = append(out.Moves, mv)
	}
	return out, skipped
}
