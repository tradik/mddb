package embedding

import (
	"container/list"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"sync"
	"sync/atomic"
	"time"
)

// Embedding cache (RAG-003).
//
// Every vector, hybrid and memory-recall query calls the embedding provider,
// with no cache at all: a network round trip and a token charge even for a
// query that was asked a second ago. MCP agents repeat the same phrase in a
// loop, so the same text was embedded over and over at full price.
//
// This is a decorator around the existing Provider interface. Providers and
// call sites are untouched — NewProvider wraps whatever it built, and setting
// MDDB_EMBEDDING_CACHE_SIZE=0 returns the bare provider, so "disabled" is
// byte-for-byte today's behaviour rather than a cache that always misses.

// CachingProvider memoises embeddings by (model, text).
//
// Exact match only. Reusing a *similar* query's vector would require embedding
// the new query to measure the similarity — the very call the cache exists to
// avoid. That belongs a layer up, where the query vector is computed anyway.
//
// Concurrent misses on the same key are not deduplicated: two goroutines asking
// for the same uncached text both reach the provider. Single-flight would fix
// that, but the case it addresses is many callers asking one identical question
// in the same instant, and the pattern this cache was built for — an agent
// repeating a phrase in a loop — is sequential. Machinery for a case that has
// not been demonstrated is machinery to maintain.
type CachingProvider struct {
	inner Provider

	mu      sync.Mutex
	entries map[string]*list.Element
	order   *list.List // front = most recently used
	maxSize int
	ttl     time.Duration

	hits   atomic.Uint64
	misses atomic.Uint64
}

type cacheEntry struct {
	key       string
	vector    []float32
	expiresAt time.Time
}

// Cache defaults. A thousand queries is a few megabytes at 1536 dimensions and
// covers the repetition an agent loop produces; an hour is long enough for a
// working session and short enough that a model swap behind the same name does
// not serve stale vectors all day.
const (
	DefaultCacheSize = 1024
	DefaultCacheTTL  = time.Hour
)

// NewCachingProvider wraps a provider with an embedding cache.
//
// Returns the inner provider unchanged when caching is off or there is nothing
// to wrap, so no call site has to know whether a cache exists.
func NewCachingProvider(inner Provider, maxSize int, ttl time.Duration) Provider {
	if inner == nil || maxSize <= 0 {
		return inner
	}
	if ttl <= 0 {
		ttl = DefaultCacheTTL
	}
	return &CachingProvider{
		inner:   inner,
		entries: make(map[string]*list.Element, maxSize),
		order:   list.New(),
		maxSize: maxSize,
		ttl:     ttl,
	}
}

// Model returns the wrapped provider's model.
func (c *CachingProvider) Model() string { return c.inner.Model() }

// Dimensions returns the wrapped provider's dimensionality.
func (c *CachingProvider) Dimensions() int { return c.inner.Dimensions() }

// Stats reports cache effectiveness.
func (c *CachingProvider) Stats() (hits, misses uint64, size int) {
	c.mu.Lock()
	size = len(c.entries)
	c.mu.Unlock()
	return c.hits.Load(), c.misses.Load(), size
}

// cacheKey identifies a text under one model.
//
// The model is part of the key because the same sentence embeds differently
// under a different model, and MDDB_EMBEDDING_MODEL can change between restarts
// while the cache is warm. Hashed so an enormous document does not become an
// enormous map key.
// The role is part of the key, not decoration. Providers embed a query and a
// document differently (RAG-006), so the same text in the two roles yields two
// different vectors — keying on text alone would hand a query the document's
// vector, or the reverse, and the result would look like a ranking problem
// rather than a cache problem.
func cacheKey(model, text string, role Role) string {
	sum := sha256.Sum256([]byte(model + "\x00" + role.String() + "\x00" + text))
	return hex.EncodeToString(sum[:])
}

// Embed returns a cached vector when one is live, otherwise asks the provider.
func (c *CachingProvider) Embed(ctx context.Context, text string, role Role) ([]float32, error) {
	key := cacheKey(c.Model(), text, role)
	if vec, ok := c.get(key); ok {
		c.hits.Add(1)
		return vec, nil
	}
	c.misses.Add(1)

	vec, err := c.inner.Embed(ctx, text, role)
	if err != nil {
		return nil, err
	}
	c.put(key, vec)
	return copyVector(vec), nil
}

// EmbedBatch asks the provider only for the texts it does not already hold.
//
// A batch is usually a document's chunks on reindex, where most chunks are
// unchanged — asking for the whole batch because one chunk moved is what makes
// reindexing expensive.
func (c *CachingProvider) EmbedBatch(ctx context.Context, texts []string, role Role) ([][]float32, error) {
	if len(texts) == 0 {
		return c.inner.EmbedBatch(ctx, texts, role)
	}

	model := c.Model()
	out := make([][]float32, len(texts))
	keys := make([]string, len(texts))

	var missing []string
	var missingAt []int

	for i, text := range texts {
		keys[i] = cacheKey(model, text, role)
		if vec, ok := c.get(keys[i]); ok {
			c.hits.Add(1)
			out[i] = vec
			continue
		}
		c.misses.Add(1)
		missing = append(missing, text)
		missingAt = append(missingAt, i)
	}

	if len(missing) == 0 {
		return out, nil
	}

	fetched, err := c.inner.EmbedBatch(ctx, missing, role)
	if err != nil {
		return nil, err
	}
	// A provider returning a different count than it was asked for would
	// silently misalign vectors with their texts — the kind of corruption
	// that only shows up as bad search results much later.
	if len(fetched) != len(missing) {
		return c.inner.EmbedBatch(ctx, texts, role)
	}

	for n, vec := range fetched {
		i := missingAt[n]
		c.put(keys[i], vec)
		out[i] = copyVector(vec)
	}
	return out, nil
}

// get returns a live cached vector, promoting it to most-recently-used.
func (c *CachingProvider) get(key string) ([]float32, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	el, ok := c.entries[key]
	if !ok {
		return nil, false
	}
	entry := el.Value.(*cacheEntry)
	if time.Now().After(entry.expiresAt) {
		c.order.Remove(el)
		delete(c.entries, key)
		return nil, false
	}
	c.order.MoveToFront(el)

	// Copied out: callers own their slice, and vector code normalises in
	// place. Handing out the cached slice would let one caller silently
	// rewrite what every later caller receives.
	return copyVector(entry.vector), true
}

// put stores a vector, evicting the least recently used entry when full.
func (c *CachingProvider) put(key string, vec []float32) {
	if vec == nil {
		return
	}
	stored := copyVector(vec)

	c.mu.Lock()
	defer c.mu.Unlock()

	if el, ok := c.entries[key]; ok {
		entry := el.Value.(*cacheEntry)
		entry.vector = stored
		entry.expiresAt = time.Now().Add(c.ttl)
		c.order.MoveToFront(el)
		return
	}

	for len(c.entries) >= c.maxSize {
		oldest := c.order.Back()
		if oldest == nil {
			break
		}
		c.order.Remove(oldest)
		delete(c.entries, oldest.Value.(*cacheEntry).key)
	}

	c.entries[key] = c.order.PushFront(&cacheEntry{
		key:       key,
		vector:    stored,
		expiresAt: time.Now().Add(c.ttl),
	})
}

func copyVector(v []float32) []float32 {
	if v == nil {
		return nil
	}
	out := make([]float32, len(v))
	copy(out, v)
	return out
}
