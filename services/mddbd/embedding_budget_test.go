package main

import (
	"net/http"
	"sync"
	"testing"
	"time"
)

// GO-043: the embedding budget — counting, the daily reset, parking in the
// worker, and the 429 a reindex gets.

// budgetLimits is a quota table a test can change while a worker runs.
type budgetLimits struct {
	mu sync.Mutex
	q  map[string]*CollectionQuota
}

func (l *budgetLimits) get(c string) *CollectionQuota {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.q[c]
}

func (l *budgetLimits) set(c string, q *CollectionQuota) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.q[c] = q
}

func newTestBudget(limits *budgetLimits) *embeddingBudget {
	return newEmbeddingBudget(limits.get, func(qe *QuotaError) error { return qe })
}

func TestEmbeddingBudgetCountsAndResetsDaily(t *testing.T) {
	limits := &budgetLimits{q: map[string]*CollectionQuota{
		"calls":  {EmbeddingCallsPerDay: 2},
		"tokens": {EmbeddingTokensPerDay: 3},
	}}
	b := newTestBudget(limits)
	day := time.Date(2026, 10, 29, 23, 0, 0, 0, time.UTC)
	b.now = func() time.Time { return day }

	if b.check("calls") != nil {
		t.Fatal("a fresh budget refused")
	}
	b.spend("calls", "a")
	b.spend("calls", "b")
	err := b.check("calls")
	qe := asQuotaError(err)
	if qe == nil || qe.Resource != quotaEmbeddingCalls || qe.Used != 2 || qe.RetryAfter != time.Hour || qe.Status() != http.StatusTooManyRequests {
		t.Fatalf("spent budget: %v", err)
	}
	if f := b.usedFraction("calls", limits.get("calls")); f != 1 {
		t.Fatalf("usedFraction = %v", f)
	}

	b.spend("tokens", "twelve chars") // 3 tokens
	if qe := b.exhausted("tokens"); qe == nil || qe.Resource != quotaEmbeddingTokens || qe.Used != 3 {
		t.Fatalf("token budget: %v", qe)
	}

	// Collections without a budget are neither limited nor counted.
	b.spend("free", "text")
	if b.check("free") != nil || b.used["free"] != (budgetUse{}) {
		t.Fatal("a collection without a budget was counted or limited")
	}

	// Midnight UTC starts a new day.
	day = day.Add(2 * time.Hour)
	if b.check("calls") != nil || b.check("tokens") != nil {
		t.Fatal("the budget did not reset at midnight")
	}
	if b.nextCheck() != budgetRecheckInterval {
		t.Fatalf("nextCheck = %v, want the recheck interval", b.nextCheck())
	}
	day = time.Date(2026, 10, 30, 23, 59, 30, 0, time.UTC)
	if b.nextCheck() != 30*time.Second {
		t.Fatalf("nextCheck before midnight = %v", b.nextCheck())
	}
}

func TestEmbeddingBudgetUsedFractionTakesTheLargerShare(t *testing.T) {
	q := &CollectionQuota{EmbeddingCallsPerDay: 10, EmbeddingTokensPerDay: 4}
	b := newTestBudget(&budgetLimits{q: map[string]*CollectionQuota{"c": q}})
	b.spend("c", "eight ch") // 1 call of 10, 2 tokens of 4
	if f := b.usedFraction("c", q); f != 0.5 {
		t.Fatalf("usedFraction = %v, want 0.5", f)
	}
	if estimateTokens("") != 0 || estimateTokens("abcde") != 2 {
		t.Fatal("estimateTokens")
	}
}
