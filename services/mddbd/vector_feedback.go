package main

import (
	"errors"
	"fmt"

	json "mddb/internal/jsonx"
	vec "mddb/internal/vector"
)

// Relevance feedback on the search paths (SRCH-015): `positive` and
// `negative` examples — document keys or vectors — on vector search, the
// vector half of hybrid search and cross search. The arithmetic is in
// internal/vector/feedback.go; this file turns a request's examples into
// vectors and applies them where each path searches.

// Feedback modes.
const (
	FeedbackModeQuery   = "query"   // Rocchio on the query vector (default)
	FeedbackModeRescore = "rescore" // re-rank the candidates by similarity to the examples
)

// Bounds on a request's feedback.
const (
	maxFeedbackExamples   = 50
	defaultFeedbackWeight = 0.5
	maxFeedbackWeight     = 10
)

// errFeedbackExample is a request's example that names nothing usable.
var errFeedbackExample = errors.New("feedback example")

// FeedbackExample is one example: a document (by key or ID) or a vector.
// In JSON it is a string or an array of numbers; {"key": ...} and
// {"vector": [...]} are accepted too.
type FeedbackExample struct {
	Key    string    `json:"key,omitempty"`
	Vector []float32 `json:"vector,omitempty"`
}

// UnmarshalJSON accepts a key, a vector, or an object holding either.
func (e *FeedbackExample) UnmarshalJSON(b []byte) error {
	if err := json.Unmarshal(b, &e.Key); err == nil {
		return nil
	}
	if err := json.Unmarshal(b, &e.Vector); err == nil {
		return nil
	}
	type plain FeedbackExample
	var p plain
	if err := json.Unmarshal(b, &p); err != nil {
		return fmt.Errorf("%w: want a document key, a vector or {key|vector}: %s", errFeedbackExample, b)
	}
	*e = FeedbackExample(p)
	return nil
}

// RocchioOptions overrides the Rocchio coefficients; nil keeps 1, 0.75, 0.25.
// Pointers, because 0 is meaningful: gamma 0 ignores the negatives.
type RocchioOptions struct {
	Alpha *float64 `json:"alpha,omitempty"`
	Beta  *float64 `json:"beta,omitempty"`
	Gamma *float64 `json:"gamma,omitempty"`
}

// FeedbackOptions are the relevance feedback fields of a vector-backed search.
type FeedbackOptions struct {
	Positive []FeedbackExample `json:"positive,omitempty"`
	Negative []FeedbackExample `json:"negative,omitempty"`
	// FeedbackMode is "query" (default) or "rescore".
	FeedbackMode string `json:"feedbackMode,omitempty"`
	// FeedbackWeight scales the rescore shift; 0 = 0.5.
	FeedbackWeight float64         `json:"feedbackWeight,omitempty"`
	Rocchio        *RocchioOptions `json:"rocchio,omitempty"`
}

// hasFeedback reports whether the request carries any example.
func (f FeedbackOptions) hasFeedback() bool { return len(f.Positive)+len(f.Negative) > 0 }

// ValidateFeedback refuses feedback that cannot mean anything.
func (f FeedbackOptions) ValidateFeedback() error {
	switch f.FeedbackMode {
	case "", FeedbackModeQuery, FeedbackModeRescore:
	default:
		return fmt.Errorf("feedbackMode %q: must be query or rescore", f.FeedbackMode)
	}
	if len(f.Positive) > maxFeedbackExamples || len(f.Negative) > maxFeedbackExamples {
		return fmt.Errorf("at most %d positive and %d negative examples", maxFeedbackExamples, maxFeedbackExamples)
	}
	for _, e := range append(append([]FeedbackExample(nil), f.Positive...), f.Negative...) {
		if (e.Key == "") == (len(e.Vector) == 0) {
			return fmt.Errorf("%w: give a document key or a vector, not both or neither", errFeedbackExample)
		}
	}
	if f.FeedbackWeight < 0 || f.FeedbackWeight > maxFeedbackWeight {
		return fmt.Errorf("feedbackWeight %.2f is outside 0–%d", f.FeedbackWeight, maxFeedbackWeight)
	}
	if r := f.Rocchio; r != nil {
		for _, w := range []*float64{r.Alpha, r.Beta, r.Gamma} {
			if w != nil && (*w < 0 || *w > maxFeedbackWeight) {
				return fmt.Errorf("rocchio weights must be within 0–%d", maxFeedbackWeight)
			}
		}
	}
	return nil
}

// FeedbackApplied is the correction a search made, reported in the plan
// when the request set explain.
type FeedbackApplied struct {
	Mode     string `json:"mode"`
	Positive int    `json:"positive"`
	Negative int    `json:"negative"`
	// Alpha, Beta and Gamma are the Rocchio weights used (query mode).
	Alpha float64 `json:"alpha,omitempty"`
	Beta  float64 `json:"beta,omitempty"`
	Gamma float64 `json:"gamma,omitempty"`
	// QueryShift is 1 − cosine(query, corrected query): how far the
	// examples moved it (query mode).
	QueryShift float64 `json:"queryShift,omitempty"`
	// Weight and Rescored: the rescore weight, and how many candidates had a
	// vector to rescore (rescore mode).
	Weight   float64 `json:"weight,omitempty"`
	Rescored int     `json:"rescored,omitempty"`
}

// feedback is a request's examples as vectors, ready to apply.
type feedback struct {
	mode     string
	pos, neg [][]float32
	rocchio  vec.RocchioWeights
	weight   float64
	applied  *FeedbackApplied
}
