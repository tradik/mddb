package main

import (
	"fmt"

	"mddb/internal/rerank"
	proto "mddb/proto"
)

// The request and response shapes of model reranking (RAG-011), and their
// gRPC forms.

// maxRerankTopN bounds a requested topN the way a profile's topK is bounded.
const maxRerankTopN = maxProfileTopK

// RerankOptions is the `rerank` block of a search request, and the default
// of a collection's retrieval profile.
type RerankOptions struct {
	// Enabled turns the stage on or off for this search; unset follows the
	// collection's profile, which is off unless configured.
	Enabled *bool `json:"enabled,omitempty"`
	// Candidates is how many of the search's best results the model orders;
	// 0 = the profile's, then the provider's maxCandidates (which also caps it).
	Candidates int `json:"candidates,omitempty"`
	// TopN keeps at most this many results after reranking; 0 = topK.
	TopN int `json:"topN,omitempty"`
	// Explain reports each candidate's rank before and after. Searches with
	// their own explain flag report it under that flag too.
	Explain bool `json:"explain,omitempty"`
}

// Validate rejects a block that cannot mean anything.
func (o *RerankOptions) Validate() error {
	if o == nil {
		return nil
	}
	if o.Candidates < 0 || o.Candidates > rerank.MaxCandidatesCeiling {
		return fmt.Errorf("rerank.candidates must be between 0 and %d", rerank.MaxCandidatesCeiling)
	}
	if o.TopN < 0 || o.TopN > maxRerankTopN {
		return fmt.Errorf("rerank.topN must be between 0 and %d", maxRerankTopN)
	}
	return nil
}

// RerankResult is what a response says about the stage. Both fields are
// absent when the stage did not run, so a search without reranking answers
// byte for byte as before.
type RerankResult struct {
	Rerank        *RerankReport `json:"rerank,omitempty"`
	RerankSkipped string        `json:"rerankSkipped,omitempty"`
}

// RerankReport describes a rerank that ran.
type RerankReport struct {
	Model      string  `json:"model"`
	Candidates int     `json:"candidates"`
	Cached     int     `json:"cached,omitempty"`
	DurationMs float64 `json:"durationMs"`
	// Moves are each candidate's rank before and after, with explain.
	Moves []RerankMove `json:"moves,omitempty"`
}

// RerankMove is one candidate's place before and after the model ordered it.
type RerankMove struct {
	ID         string  `json:"id"`
	Collection string  `json:"collection,omitempty"`
	Before     int     `json:"before"`
	After      int     `json:"after"`
	Score      float64 `json:"score"`
}

// RerankRequest is embedded in every search request that can rerank.
type RerankRequest struct {
	Rerank *RerankOptions `json:"rerank,omitempty"`
}

// ValidateRerank checks the request's rerank block.
func (r RerankRequest) ValidateRerank() error { return r.Rerank.Validate() }

// rerankOptionsFromProto reads a gRPC rerank block; nil for nil.
func rerankOptionsFromProto(p *proto.RerankOptions) *RerankOptions {
	if p == nil {
		return nil
	}
	return &RerankOptions{Enabled: p.Enabled, Candidates: int(p.Candidates), TopN: int(p.TopN), Explain: p.Explain}
}

// rerankOptionsToProto writes a rerank block for the wire; nil for nil.
func rerankOptionsToProto(o *RerankOptions) *proto.RerankOptions {
	if o == nil {
		return nil
	}
	return &proto.RerankOptions{Enabled: o.Enabled, Candidates: safeInt32(o.Candidates), TopN: safeInt32(o.TopN), Explain: o.Explain}
}

// rerankReportToProto writes a rerank report for the wire; nil for nil.
func rerankReportToProto(r *RerankReport) *proto.RerankReport {
	if r == nil {
		return nil
	}
	out := &proto.RerankReport{Model: r.Model, Candidates: safeInt32(r.Candidates), Cached: safeInt32(r.Cached),
		DurationMs: r.DurationMs}
	for _, m := range r.Moves {
		out.Moves = append(out.Moves, &proto.RerankMove{Id: m.ID, Collection: m.Collection, Before: safeInt32(m.Before),
			After: safeInt32(m.After), Score: m.Score})
	}
	return out
}

// validateAll returns the first failing check's error.
func validateAll(checks ...func() error) error {
	for _, check := range checks {
		if err := check(); err != nil {
			return err
		}
	}
	return nil
}
