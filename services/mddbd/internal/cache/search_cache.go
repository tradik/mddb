package cache

import (
	"crypto/sha256"
	"encoding/hex"
	"sync"
	"sync/atomic"
	"time"
)

// SearchCache caches whole search responses for a bounded time (GO-031).
//
// MCP agents repeat identical queries in loops, and a repeated search redoes
// the full scoring pass for a result set that has not changed. Unlike the
// document cache this is opt-in per request: a caller states the staleness it
// is willing to accept (cacheTtl), and a request without one behaves exactly
// as before. There is no silent staleness to reason about.
//
// Invalidation is by generation counter rather than by scanning: writing to a
// collection bumps its generation, which is part of every key, so the previous
// generation's entries become unreachable at once and age out on their own.
// Scanning a cache to evict a collection's entries would cost O(entries) on
// the write path, which is the wrong place to pay (GO-002 is the reminder that
// a cache without invalidation serves deleted documents).
type SearchCache struct {
	mu      sync.RWMutex
	entries map[string]*CacheEntry
	maxSize int

	// generations tracks a per-collection counter mixed into every key.
	generations map[string]uint64

	hits   atomic.Uint64
	misses atomic.Uint64
}

// NewSearchCache creates a cache holding at most maxSize responses.
// maxSize <= 0 disables the cache: Get always misses and Set does nothing, so
// a deployment can turn it off without the call sites knowing.
func NewSearchCache(maxSize int) *SearchCache {
	return &SearchCache{
		entries:     make(map[string]*CacheEntry),
		generations: make(map[string]uint64),
		maxSize:     maxSize,
	}
}

// Enabled reports whether the cache stores anything.
func (sc *SearchCache) Enabled() bool { return sc != nil && sc.maxSize > 0 }

// Key builds a cache key from a collection and the canonical form of a
// request. The collection's current generation is mixed in, so a write
// invalidates every key made before it without touching the entries.
func (sc *SearchCache) Key(collection string, canonicalRequest []byte) string {
	if !sc.Enabled() {
		return ""
	}
	sc.mu.RLock()
	gen := sc.generations[collection]
	sc.mu.RUnlock()

	h := sha256.New()
	h.Write([]byte(collection))
	h.Write([]byte{0})
	// The generation is written as text so it cannot collide with request
	// bytes that happen to look like a counter.
	h.Write([]byte(formatUint(gen)))
	h.Write([]byte{0})
	h.Write(canonicalRequest)
	return hex.EncodeToString(h.Sum(nil))
}

// Get returns a cached response if one is present and unexpired.
func (sc *SearchCache) Get(key string) ([]byte, bool) {
	if !sc.Enabled() || key == "" {
		return nil, false
	}
	sc.mu.RLock()
	entry, ok := sc.entries[key]
	sc.mu.RUnlock()

	if !ok {
		sc.misses.Add(1)
		return nil, false
	}
	if time.Now().Unix() > entry.ExpiresAt {
		// Expired entries are dropped on read rather than by a sweeper: the
		// cache is bounded and every entry is reached by the query that
		// created it, so a background goroutine would earn little.
		sc.mu.Lock()
		if cur, still := sc.entries[key]; still && cur == entry {
			delete(sc.entries, key)
		}
		sc.mu.Unlock()
		sc.misses.Add(1)
		return nil, false
	}
	sc.hits.Add(1)
	return entry.Data, true
}

// Set stores a response under key for ttlSeconds. A non-positive TTL stores
// nothing, which is what a request that did not ask for caching produces.
func (sc *SearchCache) Set(key string, data []byte, ttlSeconds int64) {
	if !sc.Enabled() || key == "" || ttlSeconds <= 0 || len(data) == 0 {
		return
	}
	sc.mu.Lock()
	defer sc.mu.Unlock()

	if len(sc.entries) >= sc.maxSize {
		sc.evictOneLocked()
	}
	sc.entries[key] = &CacheEntry{
		Data:      data,
		ExpiresAt: time.Now().Unix() + ttlSeconds,
	}
}

// evictOneLocked frees a slot, preferring an already-expired entry and falling
// back to an arbitrary one. Map iteration order is randomised, so the fallback
// is neither LRU nor FIFO — acceptable for a best-effort cache, and stated
// plainly here rather than implied by a misleading name.
func (sc *SearchCache) evictOneLocked() {
	now := time.Now().Unix()
	for k, e := range sc.entries {
		if now > e.ExpiresAt {
			delete(sc.entries, k)
			return
		}
	}
	for k := range sc.entries {
		delete(sc.entries, k)
		return
	}
}

// Invalidate marks a collection's cached responses unreachable. It is O(1):
// the generation moves, and the entries keyed to the old one are evicted as
// space is needed or expire on their own.
func (sc *SearchCache) Invalidate(collection string) {
	if !sc.Enabled() {
		return
	}
	sc.mu.Lock()
	sc.generations[collection]++
	sc.mu.Unlock()
}

// Generation reports a collection's current generation, for tests and
// diagnostics.
func (sc *SearchCache) Generation(collection string) uint64 {
	if !sc.Enabled() {
		return 0
	}
	sc.mu.RLock()
	defer sc.mu.RUnlock()
	return sc.generations[collection]
}

// Stats returns hit and miss counts and the current entry count.
func (sc *SearchCache) Stats() (hits, misses uint64, size int) {
	if sc == nil {
		return 0, 0, 0
	}
	sc.mu.RLock()
	size = len(sc.entries)
	sc.mu.RUnlock()
	return sc.hits.Load(), sc.misses.Load(), size
}

// Clear drops every entry, leaving generations intact.
func (sc *SearchCache) Clear() {
	if !sc.Enabled() {
		return
	}
	sc.mu.Lock()
	sc.entries = make(map[string]*CacheEntry)
	sc.mu.Unlock()
}

// formatUint avoids pulling strconv in for one call.
func formatUint(v uint64) string {
	if v == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	return string(buf[i:])
}
