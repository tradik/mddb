package rerank

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"math"
	"net/http"
	"sort"
	"strings"
	"time"

	json "mddb/internal/jsonx"
)

// HTTPProvider speaks the common /rerank request shape:
//
//	POST {apiUrl}/rerank
//	{"model": "...", "query": "...", "documents": ["...", ...], "top_n": 5}
//
// answered with one entry per scored document, carrying its index in the
// request and a relevance score:
//
//	{"results": [{"index": 2, "relevance_score": 0.91}, ...]}
//
// The list may also be named "data" or be the whole body, and the score may
// be named "score" — the variants local model servers answer with. Anything
// else is ErrInvalidResponse.
type HTTPProvider struct {
	url     string
	apiKey  string
	model   string
	timeout time.Duration
	client  *http.Client
}

// maxResponseBytes bounds an answer: a score per document is a few dozen
// bytes, so a megabyte is far beyond any honest reply.
const maxResponseBytes = 1 << 20

// maxErrorBytes bounds what a failing upstream can say back (as SEC-013 does
// for embedding providers): enough for a service's error string, not a page.
const maxErrorBytes = 512

// NewHTTPProvider builds the transport from a normalized configuration.
func NewHTTPProvider(c Config) *HTTPProvider {
	c = c.Normalized()
	return &HTTPProvider{
		url:     strings.TrimRight(c.APIURL, "/") + "/rerank",
		apiKey:  c.APIKey,
		model:   c.Model,
		timeout: c.Timeout,
		client:  &http.Client{Timeout: c.Timeout},
	}
}

// Model returns the configured model name.
func (p *HTTPProvider) Model() string { return p.model }

type httpRequest struct {
	Model     string   `json:"model,omitempty"`
	Query     string   `json:"query"`
	Documents []string `json:"documents"`
	TopN      int      `json:"top_n,omitempty"`
}

type httpEntry struct {
	Index          *int     `json:"index"`
	RelevanceScore *float64 `json:"relevance_score"`
	Score          *float64 `json:"score"`
}

type httpEnvelope struct {
	Results []httpEntry `json:"results"`
	Data    []httpEntry `json:"data"`
}

// Rerank sends one request and validates the answer.
func (p *HTTPProvider) Rerank(ctx context.Context, query string, documents []string, topN int) ([]Scored, error) {
	if len(documents) == 0 {
		return nil, nil
	}
	// Strings and an int always marshal.
	body, _ := json.Marshal(httpRequest{Model: p.model, Query: query, Documents: documents, TopN: topN})
	ctx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.url, bytes.NewReader(body)) // #nosec G704 -- URL from operator config, validated by httpclient.ValidateServiceURL
	if err != nil {
		return nil, fmt.Errorf("create rerank request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if p.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+p.apiKey)
	}
	UsageFrom(ctx).addCall(query, documents)
	resp, err := p.client.Do(req) // #nosec G704 -- URL from operator config, validated by httpclient.ValidateServiceURL
	if err != nil {
		return nil, fmt.Errorf("rerank request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBytes))
		return nil, fmt.Errorf("rerank API error (status %d): %s", resp.StatusCode, msg)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return nil, fmt.Errorf("read rerank response: %w", err)
	}
	return parseAnswer(raw, len(documents), topN)
}

// parseAnswer decodes and checks an answer for n documents, best first.
func parseAnswer(raw []byte, n, topN int) ([]Scored, error) {
	entries, err := decodeEntries(raw)
	if err != nil {
		return nil, err
	}
	out := make([]Scored, 0, len(entries))
	seen := make(map[int]bool, len(entries))
	for _, e := range entries {
		sc, err := e.scored(n)
		if err != nil {
			return nil, err
		}
		if seen[sc.Index] {
			return nil, fmt.Errorf("%w: document %d scored twice", ErrInvalidResponse, sc.Index)
		}
		seen[sc.Index] = true
		out = append(out, sc)
	}
	sortScored(out)
	if topN > 0 && len(out) > topN {
		out = out[:topN]
	}
	return out, nil
}

// decodeEntries accepts {"results": [...]}, {"data": [...]} or a bare list.
func decodeEntries(raw []byte) ([]httpEntry, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) > 0 && trimmed[0] == '[' {
		var list []httpEntry
		if err := json.Unmarshal(trimmed, &list); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrInvalidResponse, err)
		}
		return list, nil
	}
	var env httpEnvelope
	if err := json.Unmarshal(trimmed, &env); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidResponse, err)
	}
	if env.Results != nil {
		return env.Results, nil
	}
	if env.Data != nil {
		return env.Data, nil
	}
	return nil, fmt.Errorf("%w: no results list", ErrInvalidResponse)
}

// scored checks one entry against the n documents sent.
func (e httpEntry) scored(n int) (Scored, error) {
	if e.Index == nil || *e.Index < 0 || *e.Index >= n {
		return Scored{}, fmt.Errorf("%w: index missing or outside 0..%d", ErrInvalidResponse, n-1)
	}
	score := e.RelevanceScore
	if score == nil {
		score = e.Score
	}
	if score == nil || math.IsNaN(*score) || math.IsInf(*score, 0) {
		return Scored{}, fmt.Errorf("%w: document %d has no usable score", ErrInvalidResponse, *e.Index)
	}
	return Scored{Index: *e.Index, Score: *score}, nil
}

// sortScored orders best first, equal scores by index, so the same answer
// always gives the same order.
func sortScored(s []Scored) {
	sort.SliceStable(s, func(i, j int) bool {
		if s[i].Score != s[j].Score {
			return s[i].Score > s[j].Score
		}
		return s[i].Index < s[j].Index
	})
}
