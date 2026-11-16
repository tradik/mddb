package rerank

import (
	"context"
	"sync/atomic"
)

// Usage records what one rerank stage actually cost: the provider calls made,
// the characters they carried (for the token estimate a budget is charged)
// and the documents the cache answered instead. A stage puts one in the
// context; providers add to it, so a cached answer is not charged as a call.
type Usage struct {
	calls  atomic.Int64
	chars  atomic.Int64
	cached atomic.Int64
}

type usageKey struct{}

// WithUsage returns a context that records into u.
func WithUsage(ctx context.Context, u *Usage) context.Context {
	return context.WithValue(ctx, usageKey{}, u)
}

// UsageFrom returns the context's Usage, or nil; every method is nil-safe.
func UsageFrom(ctx context.Context) *Usage {
	u, _ := ctx.Value(usageKey{}).(*Usage)
	return u
}

// Calls is the provider calls made.
func (u *Usage) Calls() int64 {
	if u == nil {
		return 0
	}
	return u.calls.Load()
}

// Chars is the characters sent: the query once per call, plus each document.
func (u *Usage) Chars() int64 {
	if u == nil {
		return 0
	}
	return u.chars.Load()
}

// Cached is the documents the cache scored without a call.
func (u *Usage) Cached() int64 {
	if u == nil {
		return 0
	}
	return u.cached.Load()
}

func (u *Usage) addCall(query string, documents []string) {
	if u == nil {
		return
	}
	n := int64(len(query))
	for _, d := range documents {
		n += int64(len(d))
	}
	u.calls.Add(1)
	u.chars.Add(n)
}

func (u *Usage) addCached(n int) {
	if u == nil {
		return
	}
	u.cached.Add(int64(n))
}
