package main

import (
	gql "mddb/graphql"
)

// GraphQL's side of SRCH-013 (query planner) and SRCH-015 (relevance
// feedback): the input fields become the same PlanOptions and FeedbackOptions
// REST, MCP and gRPC fill, and DirectClient.VectorSearch validates and applies
// them — there is no GraphQL-only search path.

// planOptionsFromGQL reads explain and latencyTargetMs (SRCH-013).
func planOptionsFromGQL(input gql.VectorSearchInput) PlanOptions {
	return PlanOptions{Explain: derefBool(input.Explain, false), LatencyTargetMs: derefInt(input.LatencyTargetMs, 0)}
}

// feedbackFromGQL reads the relevance feedback fields (SRCH-015).
func feedbackFromGQL(input gql.VectorSearchInput) FeedbackOptions {
	f := FeedbackOptions{
		Positive:       feedbackExamplesFromGQL(input.Positive),
		Negative:       feedbackExamplesFromGQL(input.Negative),
		FeedbackMode:   derefString(input.FeedbackMode),
		FeedbackWeight: derefFloat64(input.FeedbackWeight, 0),
	}
	if r := input.Rocchio; r != nil {
		f.Rocchio = &RocchioOptions{Alpha: r.Alpha, Beta: r.Beta, Gamma: r.Gamma}
	}
	return f
}

// feedbackExamplesFromGQL keeps an example's key and vector as given; an
// example with both or neither is refused by ValidateFeedback, with REST's
// message.
func feedbackExamplesFromGQL(in []*gql.FeedbackExampleInput) []FeedbackExample {
	if len(in) == 0 {
		return nil
	}
	out := make([]FeedbackExample, 0, len(in))
	for _, e := range in {
		out = append(out, FeedbackExample{Key: derefString(e.Key), Vector: float32sFromGQL(e.Vector)})
	}
	return out
}

// float32sFromGQL narrows a GraphQL [Float!] to a vector; nil stays nil.
func float32sFromGQL(in []float64) []float32 {
	if len(in) == 0 {
		return nil
	}
	out := make([]float32, len(in))
	for i, v := range in {
		out[i] = float32(v)
	}
	return out
}

// searchPlanToGQL converts the plan a search returned; nil (no explain)
// stays nil.
func searchPlanToGQL(p *SearchPlan) *gql.SearchPlan {
	if p == nil {
		return nil
	}
	out := &gql.SearchPlan{Algorithm: p.Algorithm, Prefilter: p.Prefilter, Candidates: p.Candidates,
		IterativeScan: p.IterativeScan, Reasons: p.Reasons}
	if out.Reasons == nil {
		out.Reasons = []string{}
	}
	if f := p.Feedback; f != nil {
		out.Feedback = &gql.FeedbackApplied{Mode: f.Mode, Positive: f.Positive, Negative: f.Negative,
			Alpha: f.Alpha, Beta: f.Beta, Gamma: f.Gamma, QueryShift: f.QueryShift, Weight: f.Weight, Rescored: f.Rescored}
	}
	return out
}
