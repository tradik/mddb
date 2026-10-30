package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// evalQuery is one query of an evaluation set, in the shape POST /v1/eval
// and tools/bench/evalsets use.
type evalQuery struct {
	ID           string         `json:"id"`
	Query        string         `json:"query"`
	ExpectedKeys []string       `json:"expectedKeys"`
	Grades       map[string]int `json:"grades,omitempty"`
}

// querySet is a named list of queries measured together.
type querySet struct {
	Name    string
	Queries []evalQuery
}

// readEvalSet reads {"queries": [...]}, the layout of evalsets/docs.json.
func readEvalSet(path string) ([]evalQuery, error) {
	data, err := os.ReadFile(path) // #nosec G304 -- the operator names the file
	if err != nil {
		return nil, err
	}
	var set struct {
		Queries []evalQuery `json:"queries"`
	}
	if err := json.Unmarshal(data, &set); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if len(set.Queries) == 0 {
		return nil, fmt.Errorf("%s: no queries", path)
	}
	return set.Queries, nil
}

// pairQueries is the multi-topic set: every pair of queries joined with
// "and", answered by both pages. One vector has to sit between two topics
// here, which is the case MaxSim is meant for.
func pairQueries(qs []evalQuery) []evalQuery {
	var out []evalQuery
	for i := range qs {
		for j := i + 1; j < len(qs); j++ {
			a, b := qs[i], qs[j]
			p := evalQuery{ID: a.ID + "+" + b.ID, Query: a.Query + " and " + b.Query, Grades: map[string]int{}}
			for _, q := range []evalQuery{a, b} {
				for _, k := range q.ExpectedKeys {
					if !contains(p.ExpectedKeys, k) {
						p.ExpectedKeys = append(p.ExpectedKeys, k)
					}
				}
				for k, g := range q.Grades {
					p.Grades[k] = max(p.Grades[k], g)
				}
			}
			out = append(out, p)
		}
	}
	return out
}

func contains(xs []string, x string) bool {
	for _, y := range xs {
		if y == x {
			return true
		}
	}
	return false
}

// Ways of combining the parts' scores into one ranking.
const (
	fuseMean = "" // MaxSim: the mean over the parts of each part's best chunk
	// fuseMax scores a document by its best part: not MaxSim but multi-query
	// fusion — the control that says whether a change on multi-topic queries
	// comes from splitting or from summing, and the fusion the server's
	// queryDecomposition auto ships (SRCH-020).
	fuseMax = "max"
	// fuseRRF sums 1/(60 + rank) over the parts' rankings (SRCH-020's
	// alternative to max).
	fuseRRF = "rrf"
)

// variant is one way of splitting a query into the parts MaxSim embeds,
// and of combining the parts' scores.
type variant struct {
	Name   string
	Parts  func(query string) []string
	Fusion string // fuseMean, fuseMax or fuseRRF
}

// variants are the scorings compared. "whole" is today's single-vector max,
// computed on the same path as the others so only the aggregation differs.
func variants(window, stride int) []variant {
	windows := func(q string) []string { return splitWindows(q, window, stride) }
	return []variant{
		{Name: "max, whole query", Parts: func(q string) []string { return []string{q} }},
		{Name: "maxsim, clauses", Parts: splitClauses},
		{Name: fmt.Sprintf("maxsim, %d-word windows", window), Parts: windows},
		{Name: "maxsim, whole + clauses", Parts: withWhole(splitClauses)},
		{Name: fmt.Sprintf("maxsim, whole + %d-word windows", window), Parts: withWhole(windows)},
		{Name: "best part, clauses (control)", Parts: splitClauses, Fusion: fuseMax},
		// SRCH-020: the server's queryDecomposition auto, on the client path —
		// the whole query kept as one of at most five searches.
		{Name: "best part, whole + clauses ≤4 (= auto)", Parts: withWhole(splitParts), Fusion: fuseMax},
		{Name: "rrf, whole + clauses ≤4", Parts: withWhole(splitParts), Fusion: fuseRRF},
	}
}

// maxQueryParts is the server's bound on a decomposed query's parts
// (services/mddbd/query_decomposition.go).
const maxQueryParts = 4

// splitParts is splitClauses bounded the way the server bounds it: clauses
// past the fourth join the fourth.
func splitParts(q string) []string {
	parts := splitClauses(q)
	if len(parts) > maxQueryParts {
		parts[maxQueryParts-1] = strings.Join(parts[maxQueryParts-1:], " ")
		parts = parts[:maxQueryParts]
	}
	return parts
}

// withWhole adds the whole query to split's parts, unless split left it whole.
func withWhole(split func(string) []string) func(string) []string {
	return func(q string) []string {
		parts := split(q)
		if len(parts) == 1 && parts[0] == q {
			return parts
		}
		return append([]string{q}, parts...)
	}
}

// splitClauses splits at punctuation and at the word "and" — sentences and
// coordinated clauses. Fragments of one word carry no topic and join the
// part before them; a query with nothing to split stays whole.
func splitClauses(q string) []string {
	f := func(r rune) bool { return strings.ContainsRune(".,;:?!", r) }
	var parts []string
	for _, sentence := range strings.FieldsFunc(q, f) {
		var cur []string
		for _, w := range strings.Fields(sentence) {
			if strings.EqualFold(w, "and") && len(cur) > 1 {
				parts, cur = append(parts, strings.Join(cur, " ")), nil
				continue
			}
			cur = append(cur, w)
		}
		switch {
		case len(cur) > 1 || len(parts) == 0:
			parts = append(parts, strings.Join(cur, " "))
		case len(cur) == 1:
			parts[len(parts)-1] += " " + cur[0]
		}
	}
	if len(parts) <= 1 {
		return []string{q}
	}
	return parts
}

// splitWindows cuts the query into overlapping windows of size words, one
// every stride words, the last one ending at the last word.
func splitWindows(q string, size, stride int) []string {
	words := strings.Fields(q)
	if size <= 0 || stride <= 0 || len(words) <= size {
		return []string{q}
	}
	var parts []string
	for start := 0; ; start += stride {
		if start+size >= len(words) {
			parts = append(parts, strings.Join(words[len(words)-size:], " "))
			return parts
		}
		parts = append(parts, strings.Join(words[start:start+size], " "))
	}
}
