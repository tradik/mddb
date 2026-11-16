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
	// tenantOf is the collection's tenant and its quota (GO-054); nil, or a
	// nil quota, means no tenant budget. A tenant's spend is counted under
	// tenantBudgetKey and stops every collection of the tenant.
	tenantOf func(collection string) (string, *TenantQuota)

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
	if q := b.limits(collection); q.limitsEmbedding() {
		if qe := b.spent(collection, q.EmbeddingCallsPerDay, q.EmbeddingTokensPerDay); qe != nil {
			qe.Collection = collection
			return qe
		}
	}
	tenant, tq := b.tenantQuota(collection) // GO-054
	if !tq.limitsEmbedding() {
		return nil
	}
	qe := b.spent(tenantBudgetKey(tenant), tq.EmbeddingCallsPerDay, tq.EmbeddingTokensPerDay)
	if qe != nil {
		qe.Collection, qe.Tenant = collection, tenant
	}
	return qe
}

// spent is the refusal for a key that used up a calls or tokens limit today,
// nil while it has budget left.
func (b *embeddingBudget) spent(key string, calls, tokens int64) *QuotaError {
	u := b.today(key)
	var qe *QuotaError
	switch {
	case calls > 0 && u.calls >= calls:
		qe = &QuotaError{Resource: quotaEmbeddingCalls, Limit: calls, Used: u.calls}
	case tokens > 0 && u.tokens >= tokens:
		qe = &QuotaError{Resource: quotaEmbeddingTokens, Limit: tokens, Used: u.tokens}
	default:
		return nil
	}
	qe.RetryAfter = b.resetIn()
	return qe
}

// tenantBudgetKey is where a tenant's spend is counted: a key no collection
// name can be.
func tenantBudgetKey(tenant string) string { return "\x00tenant:" + tenant }

// tenantQuota is the collection's tenant and its quota, nil without one.
func (b *embeddingBudget) tenantQuota(collection string) (string, *TenantQuota) {
	if b.tenantOf == nil {
		return "", nil
	}
	return b.tenantOf(collection)
}

// spend records one provider call embedding text for the collection. Only
// collections with a budget are counted.
func (b *embeddingBudget) spend(collection, text string) {
	b.spendTokens(collection, estimateTokens(text))
}

// spendTokens records one provider call of the given size: an image's, whose
// tokens are counted from its pixels (RAG-010). It counts for the collection
// when it has a budget, and for its tenant when that has one (GO-054).
func (b *embeddingBudget) spendTokens(collection string, tokens int64) {
	var keys []string
	if b.limits(collection).limitsEmbedding() {
		keys = append(keys, collection)
	}
	if tenant, tq := b.tenantQuota(collection); tq.limitsEmbedding() {
		keys = append(keys, tenantBudgetKey(tenant))
	}
	if len(keys) == 0 {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.rollLocked()
	for _, k := range keys {
		u := b.used[k]
		u.calls++
		u.tokens += tokens
		b.used[k] = u
	}
}

// today is the collection's spend this UTC day; zero for a collection
// without an embedding budget, whose spend is not counted.
func (b *embeddingBudget) today(collection string) budgetUse {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.rollLocked()
	return b.used[collection]
}

// usedFraction is the larger share of today's call and token budgets spent.
func (b *embeddingBudget) usedFraction(collection string, q *CollectionQuota) float64 {
	u := b.today(collection)
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
