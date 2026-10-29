package main

import (
	"sync"
	"time"
)

// The per-collection embedding budget (GO-043).
//
// A collection's quota may cap the embedding provider calls, or the tokens
// sent to it, per UTC day. The budget never refuses a document write: the
// embedding worker parks a document whose collection has spent its budget,
// and the document stays pending (GO-042) until the budget resets at
// midnight UTC or its quota is raised. A reindex — which exists only to spend
// embedding calls — is refused with 429 and Retry-After instead.
//
// The budget is checked before each document; a document that starts is
// finished, so a day's use can pass the limit by one document's chunks.
// Counters live in memory: a restart starts the day's count again.

// budgetRecheckInterval is how often parked documents are looked at again,
// so a raised or removed quota releases them without waiting for midnight.
var budgetRecheckInterval = time.Minute

// embeddingBudget counts embedding use per collection for the current UTC day.
type embeddingBudget struct {
	limits func(collection string) *CollectionQuota
	notify func(qe *QuotaError) error // reports a refusal (reportQuota)
	now    func() time.Time

	mu   sync.Mutex
	day  string
	used map[string]budgetUse
}

// budgetUse is one collection's spend today.
type budgetUse struct {
	calls  int64
	tokens int64
}

// newEmbeddingBudget builds a budget reading each collection's limits from limits.
func newEmbeddingBudget(limits func(string) *CollectionQuota, notify func(*QuotaError) error) *embeddingBudget {
	return &embeddingBudget{limits: limits, notify: notify, now: time.Now, used: map[string]budgetUse{}}
}

// estimateTokens approximates the tokens of a text as a provider counts them:
// about four characters each.
func estimateTokens(text string) int64 {
	return int64((len(text) + 3) / 4)
}

// rollLocked starts the day's counters afresh when the UTC date changed.
func (b *embeddingBudget) rollLocked() {
	if day := b.now().UTC().Format(time.DateOnly); day != b.day {
		b.day = day
		b.used = map[string]budgetUse{}
	}
}

// resetIn is the time until the budget resets, at the next midnight UTC.
func (b *embeddingBudget) resetIn() time.Duration {
	now := b.now().UTC()
	midnight := time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, time.UTC)
	return midnight.Sub(now)
}

// check returns a 429 QuotaError, reported, when the collection has spent
// today's budget.
func (b *embeddingBudget) check(collection string) error {
	if qe := b.exhausted(collection); qe != nil {
		return b.notify(qe)
	}
	return nil
}

// exhausted is check without the report, for looking at parked documents
// again: a recheck is not a new refusal.
func (b *embeddingBudget) exhausted(collection string) *QuotaError {
	q := b.limits(collection)
	if !q.limitsEmbedding() {
		return nil
	}
	b.mu.Lock()
	b.rollLocked()
	u := b.used[collection]
	b.mu.Unlock()
	var qe *QuotaError
	switch {
	case q.EmbeddingCallsPerDay > 0 && u.calls >= q.EmbeddingCallsPerDay:
		qe = &QuotaError{Collection: collection, Resource: quotaEmbeddingCalls, Limit: q.EmbeddingCallsPerDay, Used: u.calls}
	case q.EmbeddingTokensPerDay > 0 && u.tokens >= q.EmbeddingTokensPerDay:
		qe = &QuotaError{Collection: collection, Resource: quotaEmbeddingTokens, Limit: q.EmbeddingTokensPerDay, Used: u.tokens}
	default:
		return nil
	}
	qe.RetryAfter = b.resetIn()
	return qe
}

// spend records one provider call embedding text for the collection. Only
// collections with a budget are counted.
func (b *embeddingBudget) spend(collection, text string) {
	b.spendTokens(collection, estimateTokens(text))
}

// spendTokens records one provider call of the given size: an image's, whose
// tokens are counted from its pixels (RAG-010).
func (b *embeddingBudget) spendTokens(collection string, tokens int64) {
	if !b.limits(collection).limitsEmbedding() {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.rollLocked()
	u := b.used[collection]
	u.calls++
	u.tokens += tokens
	b.used[collection] = u
}

// usedFraction is the larger share of today's call and token budgets spent.
func (b *embeddingBudget) usedFraction(collection string, q *CollectionQuota) float64 {
	b.mu.Lock()
	b.rollLocked()
	u := b.used[collection]
	b.mu.Unlock()
	var f float64
	if q.EmbeddingCallsPerDay > 0 {
		f = float64(u.calls) / float64(q.EmbeddingCallsPerDay)
	}
	if q.EmbeddingTokensPerDay > 0 {
		f = max(f, float64(u.tokens)/float64(q.EmbeddingTokensPerDay))
	}
	return f
}

// nextCheck is when parked documents are looked at again.
func (b *embeddingBudget) nextCheck() time.Duration {
	return min(b.resetIn(), budgetRecheckInterval)
}
