package main

import (
	"cmp"
	"context"
	"time"

	"mddb/internal/rerank"
	"mddb/internal/storage"
)

// The model rerank stage (RAG-011).
//
// It runs after a search has found and ordered its candidates — after fusion
// and boosts, before curation, grouping and the context budget — and asks the
// configured reranking model to order the best `candidates` of them. The
// search fetches that many instead of topK, so the model has a pool to choose
// from, and returns topK (or topN, if smaller) of the reordered list.
//
// The model reads passages, never whole documents: a chunk the search already
// cut, or the window of a document where the query's words meet. That keeps a
// call's size bounded by candidates × maxRerankChars whatever the documents
// weigh.
//
// The stage can only reorder. A timeout, a provider error, an answer it
// cannot use or a spent budget leave the search's own order in place and say
// why in rerankSkipped; a search never fails because of its reranker. With
// nothing configured and nothing asked, no code path changes.

// maxRerankChars bounds the characters sent per candidate: about 500
// tokens, as much as a cross-encoder reads of one passage anyway.
const maxRerankChars = 2048

// Reasons a rerank was skipped, beyond rerank.Reason's.
const (
	rerankSkipNotConfigured = "notConfigured"
	rerankSkipNoQuery       = "noQuery"
	rerankSkipBudget        = "budget"
)

// rerankRun is one search's rerank stage: what was resolved for it, and what
// happened. A nil run is "not asked for" and every method is a no-op on it.
type rerankRun struct {
	setup      *rerankSetup // nil: asked for, but no provider is configured
	collection string       // the searched collection; cross-search reads each hit's own
	query      string
	candidates int
	topN       int
	explain    bool
	result     RerankResult
}

// newRerankRun resolves a search's rerank options against the collection's
// profile: nil when the stage is off for this search.
func (s *Server) newRerankRun(collection, query string, o *RerankOptions, explain bool) *rerankRun {
	opts := s.resolveRerank(collection, o)
	if opts.Enabled == nil || !*opts.Enabled {
		return nil
	}
	r := &rerankRun{setup: s.rerankConfigured(), collection: collection, query: query, topN: opts.TopN,
		explain: explain || opts.Explain}
	if r.setup != nil {
		r.candidates = min(cmp.Or(opts.Candidates, r.setup.maxCandidates), r.setup.maxCandidates)
		r.candidates, _ = s.CapLimit(collection, r.candidates, 0) // never fetch past the server's cap
	}
	return r
}

// resolveRerank is request > collection profile, field by field.
func (s *Server) resolveRerank(collection string, o *RerankOptions) RerankOptions {
	var out RerankOptions
	if p := s.RetrievalProfile(collection); p != nil && p.Rerank != nil {
		out = *p.Rerank
	}
	if o == nil {
		return out
	}
	if o.Enabled != nil {
		out.Enabled = o.Enabled
	}
	out.Candidates = cmp.Or(o.Candidates, out.Candidates)
	out.TopN = cmp.Or(o.TopN, out.TopN)
	out.Explain = o.Explain || out.Explain
	return out
}

// active reports a run that will call a provider.
func (r *rerankRun) active() bool { return r != nil && r.setup != nil }

// pool is how many results a search should fetch for a final n.
func (r *rerankRun) pool(n int) int {
	if !r.active() {
		return n
	}
	return max(n, r.candidates)
}

// cut is the final result count for a requested n: topN when smaller.
func (r *rerankRun) cut(n int) int {
	if !r.active() || r.topN <= 0 {
		return n
	}
	return min(n, r.topN)
}

// Result is what the response reports; empty for a nil run.
func (r *rerankRun) Result() RerankResult {
	if r == nil {
		return RerankResult{}
	}
	return r.result
}

// textFor is the passage the model reads for a hit: the chunk the search cut,
// else the window of the document where the query's words (or the matched
// terms) meet. "" when the run is inactive, so callers can ask unconditionally.
func (r *rerankRun) textFor(collection string, doc *storage.Doc, chunk string, terms []string) string {
	if !r.active() {
		return ""
	}
	if chunk == "" {
		if len(terms) == 0 {
			terms = queryTerms(r.query)
		}
		chunk = passageAtHit(collection, doc, terms, nil, PassageOptions{}, 0, "").Content
	}
	text, _ := truncateRunes(chunk, maxRerankChars)
	return text
}

// rerankAccess tells the stage how to read and renumber one result type.
type rerankAccess[T any] struct {
	text       func(*T) string
	id         func(*T) string
	collection func(*T) string
	setRank    func(*T, int)
}

// rerankItems orders items' first r.candidates by the model and returns the
// list with the rest after them, ranks renumbered. On any failure it returns
// items unchanged and records why.
func rerankItems[T any](ctx context.Context, s *Server, r *rerankRun, items []T, acc rerankAccess[T]) []T {
	if r == nil {
		return items
	}
	n := rerankPoolSize(len(items), r.candidates)
	cands := items[:n]
	if reason := rerankPrecheck(s, r, cands, acc); reason != "" {
		r.skip(s, reason)
		return items
	}
	if n == 0 {
		return items
	}
	// Sized by the candidates in hand, not by the requested pool size.
	texts := make([]string, 0, min(len(items), rerank.MaxCandidatesCeiling))
	for i := range cands {
		texts = append(texts, acc.text(&cands[i]))
	}
	usage := &rerank.Usage{}
	start := time.Now()
	sp := s.Tracer.Stage(ctx, spanSearchRerank) // OPS-024
	scored, err := r.setup.provider.Rerank(rerank.WithUsage(ctx, usage), r.query, texts, 0)
	sp.SetInt("mddb.candidates", n)
	sp.End()
	rerankCharge(s, r, usage, cands, acc, texts)
	if err != nil {
		r.skip(s, rerank.Reason(err))
		return items
	}
	s.Metrics.IncOp("rerank", "applied")
	r.result.Rerank = &RerankReport{Model: r.setup.provider.Model(), Candidates: n, Cached: int(usage.Cached()),
		DurationMs: float64(time.Since(start).Microseconds()) / 1000}
	if r.explain {
		r.result.Rerank.Moves = rerankMoves(r, cands, acc, scored)
	}
	out := reorder(items, n, scored)
	if acc.setRank != nil {
		for i := range out {
			acc.setRank(&out[i], i+1)
		}
	}
	return out
}

// skip records a skipped stage and counts it.
func (r *rerankRun) skip(s *Server, reason string) {
	r.result.RerankSkipped = reason
	s.Metrics.IncOp("rerank", "skipped", reason)
}

// rerankCollection is a candidate's collection: its own in a cross-collection
// search, else the run's.
func rerankCollection[T any](r *rerankRun, acc rerankAccess[T], it *T) string {
	if acc.collection != nil {
		return acc.collection(it)
	}
	return r.collection
}

// rerankPrecheck returns why the run cannot call its provider, or "". A
// collection among the candidates that has spent today's model budget
// (GO-043) stops the call: reranking counts against the same per-collection
// calls and tokens as embedding.
func rerankPrecheck[T any](s *Server, r *rerankRun, cands []T, acc rerankAccess[T]) string {
	switch {
	case r.setup == nil:
		return rerankSkipNotConfigured
	case r.query == "":
		return rerankSkipNoQuery
	}
	for i := range cands {
		if s.quotas().budget.exhausted(rerankCollection(r, acc, &cands[i])) != nil {
			return rerankSkipBudget
		}
	}
	return ""
}

// rerankCharge counts a call against each collection whose candidates it
// carried: one call, and the estimated tokens of the query and of that
// collection's passages. A call the cache answered in full costs nothing.
func rerankCharge[T any](s *Server, r *rerankRun, usage *rerank.Usage, cands []T, acc rerankAccess[T], texts []string) {
	if usage.Calls() == 0 {
		return
	}
	tokens := map[string]int64{}
	for i := range cands {
		c := rerankCollection(r, acc, &cands[i])
		if _, seen := tokens[c]; !seen {
			tokens[c] = estimateTokens(r.query)
		}
		tokens[c] += estimateTokens(texts[i])
	}
	for c, n := range tokens {
		s.quotas().budget.spendTokens(c, n)
	}
}

// rerankMoves reports each candidate's rank before and after, in the new order.
func rerankMoves[T any](r *rerankRun, cands []T, acc rerankAccess[T], scored []rerank.Scored) []RerankMove {
	moves := make([]RerankMove, 0, len(scored))
	for after, sc := range scored {
		it := &cands[sc.Index]
		m := RerankMove{ID: acc.id(it), Before: sc.Index + 1, After: after + 1, Score: sc.Score}
		if acc.collection != nil {
			m.Collection = rerankCollection(r, acc, it)
		}
		moves = append(moves, m)
	}
	return moves
}

// rerankPoolSize is how many of available items go to the model: the
// requested number, never more than there are and never more than the
// configured ceiling, whatever a request asks.
func rerankPoolSize(available, requested int) int {
	n := requested
	if n > available {
		n = available
	}
	if n > rerank.MaxCandidatesCeiling {
		n = rerank.MaxCandidatesCeiling
	}
	if n < 0 {
		n = 0
	}
	return n
}

// reorder puts the scored candidates first, best first, then candidates the
// model did not score in their old order, then the items beyond the pool.
// Indexes come from a validated answer: in range, each once.
func reorder[T any](items []T, n int, scored []rerank.Scored) []T {
	n = rerankPoolSize(len(items), n)
	out := make([]T, 0, len(items))
	// One flag per result in hand; only the first n are consulted.
	placed := make([]bool, len(items))
	for _, sc := range scored {
		placed[sc.Index] = true
		out = append(out, items[sc.Index])
	}
	for i := 0; i < n; i++ {
		if !placed[i] {
			out = append(out, items[i])
		}
	}
	return append(out, items[n:]...)
}
