package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// symbolAnswer is the part of a /v1/symbol response the benchmark reads.
type symbolAnswer struct {
	References []struct {
		Path  string `json:"path"`
		Lines struct {
			Start int `json:"start"`
		} `json:"lines"`
		Target string `json:"target"`
	} `json:"references"`
	Implementations []json.RawMessage `json:"implementations"`
	Precision       string            `json:"precision"`
	Truncated       bool              `json:"truncated"`
}

// lookup asks one collection about a symbol the way an agent changing it
// would: its bare name and the file holding the definition.
func lookup(client *http.Client, base, collection string, s *symbol, limit int) (*symbolAnswer, int, time.Duration, error) {
	q := url.Values{"collection": {collection}, "name": {s.bare()}, "path": {s.Path},
		"include": {"references,implementations"}, "limit": {strconv.Itoa(limit)}}
	start := time.Now()
	resp, err := client.Get(base + "/v1/symbol?" + q.Encode())
	if err != nil {
		return nil, 0, 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	elapsed := time.Since(start)
	if err != nil {
		return nil, 0, 0, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, 0, 0, fmt.Errorf("/v1/symbol %s: %d %s", s.target(), resp.StatusCode, raw)
	}
	var a symbolAnswer
	if err := json.Unmarshal(raw, &a); err != nil {
		return nil, 0, 0, err
	}
	return &a, len(raw), elapsed, nil
}

// score is one symbol's answer in one mode against the truth.
type score struct {
	Returned  int    `json:"returned"`  // references returned for this definition
	Correct   int    `json:"correct"`   // of those, in the truth
	Truth     int    `json:"truth"`     // references in the truth
	Other     int    `json:"other"`     // labelled as another definition's (left out above)
	Impls     int    `json:"impls"`     // implementations returned
	Tokens    int    `json:"tokens"`    // response size, 4 bytes a token
	Precision string `json:"precision"` // the answer's own precision field
	Truncated bool   `json:"truncated,omitempty"`
}

// scoreAnswer compares an answer with the truth. A reference labelled with
// another definition (Target, semantic answers only) is not this symbol's
// answer, so it is counted apart; unlabelled ones are. The definition's own
// line is dropped, as the truth drops it.
func scoreAnswer(s *symbol, a *symbolAnswer, size int) score {
	sc := score{Truth: len(s.refs), Impls: len(a.Implementations), Tokens: (size + 3) / 4,
		Precision: a.Precision, Truncated: a.Truncated}
	seen := map[string]bool{}
	for _, r := range a.References {
		l := loc(r.Path, r.Lines.Start)
		switch {
		case r.Target != "" && r.Target != s.target():
			sc.Other++
		case (r.Path == s.Path && r.Lines.Start == s.Start) || seen[l]:
		default:
			seen[l] = true
			sc.Returned++
			if s.refs[l] {
				sc.Correct++
			}
		}
	}
	return sc
}

// symbolResult is one sampled symbol's scores in both modes.
type symbolResult struct {
	*symbol
	TruthImpls int     `json:"truthImpls"`
	Heuristic  score   `json:"heuristic"`
	Semantic   score   `json:"semantic"`
	HeurMs     []int64 `json:"-"`
	SemMs      []int64 `json:"-"`
}

// measure asks both collections about every symbol, repeat times each for
// the latency (the answer is taken from the first).
func measure(client *http.Client, base, heur, sem string, syms []*symbol, limit, repeat int) ([]symbolResult, error) {
	out := make([]symbolResult, 0, len(syms))
	for _, s := range syms {
		r := symbolResult{symbol: s, TruthImpls: s.impls}
		for i := range max(repeat, 1) {
			for _, mode := range []struct {
				coll string
				sc   *score
				ms   *[]int64
			}{{heur, &r.Heuristic, &r.HeurMs}, {sem, &r.Semantic, &r.SemMs}} {
				a, size, took, err := lookup(client, base, mode.coll, s, limit)
				if err != nil {
					return nil, err
				}
				if i == 0 {
					*mode.sc = scoreAnswer(s, a, size)
				}
				*mode.ms = append(*mode.ms, took.Microseconds())
			}
		}
		out = append(out, r)
	}
	return out, nil
}
