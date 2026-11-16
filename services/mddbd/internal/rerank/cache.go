package rerank

import (
	"container/list"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"sync"
	"time"
)

// CachingProvider remembers (model, query, text) scores, bounded and LRU.
//
// A reranking model scores a pair, not a text, so a pair is the only thing
// that can be reused: the same passage scored for a different question is a
// different number. Agents repeat queries in loops and page through the same
// result list, so the same pairs come back often; a repeat costs no call and
// no tokens. Only the documents the cache does not hold are sent, and a call
// is skipped entirely when it holds them all.
type CachingProvider struct {
	inner Provider

	mu      sync.Mutex
	entries map[string]*list.Element
	order   *list.List // front = most recently used
	maxSize int
	ttl     time.Duration
	now     func() time.Time
}

type cacheEntry struct {
	key       string
	score     float64
	expiresAt time.Time
}

// NewCachingProvider wraps inner; it returns inner unchanged when maxSize is
// not positive, so "no cache" is exactly the bare provider.
func NewCachingProvider(inner Provider, maxSize int, ttl time.Duration) Provider {
	if maxSize <= 0 {
		return inner
	}
	if ttl <= 0 {
		ttl = DefaultCacheTTL
	}
	return &CachingProvider{inner: inner, entries: make(map[string]*list.Element), order: list.New(),
		maxSize: maxSize, ttl: ttl, now: time.Now}
}

// Model is the inner provider's.
func (c *CachingProvider) Model() string { return c.inner.Model() }

// Rerank answers from the cache where it can and asks the provider for the rest.
func (c *CachingProvider) Rerank(ctx context.Context, query string, documents []string, topN int) ([]Scored, error) {
	keys := make([]string, len(documents))
	out := make([]Scored, 0, len(documents))
	var missIdx []int
	var missDocs []string
	for i, d := range documents {
		keys[i] = c.key(query, d)
		if score, ok := c.get(keys[i]); ok {
			out = append(out, Scored{Index: i, Score: score})
			continue
		}
		missIdx, missDocs = append(missIdx, i), append(missDocs, d)
	}
	UsageFrom(ctx).addCached(len(out))
	if len(missDocs) > 0 {
		scored, err := c.inner.Rerank(ctx, query, missDocs, 0)
		if err != nil {
			return nil, err
		}
		for _, s := range scored {
			orig := missIdx[s.Index]
			c.put(keys[orig], s.Score)
			out = append(out, Scored{Index: orig, Score: s.Score})
		}
	}
	sortScored(out)
	if topN > 0 && len(out) > topN {
		out = out[:topN]
	}
	return out, nil
}

// key identifies a pair under the current model.
func (c *CachingProvider) key(query, text string) string {
	h := sha256.New()
	for _, part := range []string{c.inner.Model(), query, text} {
		h.Write([]byte(part))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

func (c *CachingProvider) get(key string) (float64, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	el, ok := c.entries[key]
	if !ok {
		return 0, false
	}
	e := el.Value.(*cacheEntry)
	if c.now().After(e.expiresAt) {
		c.order.Remove(el)
		delete(c.entries, key)
		return 0, false
	}
	c.order.MoveToFront(el)
	return e.score, true
}

func (c *CachingProvider) put(key string, score float64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.entries[key]; ok {
		c.order.Remove(el)
	}
	c.entries[key] = c.order.PushFront(&cacheEntry{key: key, score: score, expiresAt: c.now().Add(c.ttl)})
	for c.order.Len() > c.maxSize {
		oldest := c.order.Back()
		c.order.Remove(oldest)
		delete(c.entries, oldest.Value.(*cacheEntry).key)
	}
}

// Len is the number of pairs held, for tests and metrics.
func (c *CachingProvider) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.order.Len()
}
