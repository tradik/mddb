package main

import (
	"sync"
	"sync/atomic"
	"time"
)

// LockFreeCache is a lock-free cache using atomic operations
type LockFreeCache struct {
	shards    []*CacheShard
	shardMask uint64
	maxSize   int
	ttl       int64
	hits      atomic.Uint64
	misses    atomic.Uint64
}

// CacheShard represents a single cache shard
type CacheShard struct {
	data atomic.Value // map[string]*LockFreeCacheEntry
	mu   sync.RWMutex // Only for writes
	size atomic.Int32
}

// LockFreeCacheEntry represents a cached entry
type LockFreeCacheEntry struct {
	Data      []byte
	ExpiresAt int64
}

// NewLockFreeCache creates a new lock-free cache
func NewLockFreeCache(maxSize int, ttlSeconds int64) *LockFreeCache {
	if maxSize <= 0 {
		maxSize = 1000
	}
	if ttlSeconds <= 0 {
		ttlSeconds = 300
	}
	
	// Use 16 shards for better concurrency
	numShards := 16
	shards := make([]*CacheShard, numShards)
	
	for i := 0; i < numShards; i++ {
		shard := &CacheShard{}
		shard.data.Store(make(map[string]*LockFreeCacheEntry))
		shards[i] = shard
	}
	
	cache := &LockFreeCache{
		shards:    shards,
		shardMask: uint64(numShards - 1),
		maxSize:   maxSize,
		ttl:       ttlSeconds,
	}
	
	// Start cleanup goroutine
	go cache.cleanup()
	
	return cache
}

// getShard returns the shard for a key
func (lfc *LockFreeCache) getShard(key string) *CacheShard {
	hash := fnv1a(key)
	return lfc.shards[hash&lfc.shardMask]
}

// Get retrieves a value from cache (lock-free read)
func (lfc *LockFreeCache) Get(key string) ([]byte, bool) {
	shard := lfc.getShard(key)
	
	// Lock-free read
	m := shard.data.Load().(map[string]*LockFreeCacheEntry)
	entry, exists := m[key]
	
	if !exists {
		lfc.misses.Add(1)
		return nil, false
	}
	
	// Check expiration
	if time.Now().Unix() > entry.ExpiresAt {
		lfc.misses.Add(1)
		return nil, false
	}
	
	lfc.hits.Add(1)
	return entry.Data, true
}

// Set stores a value in cache (uses lock for write)
func (lfc *LockFreeCache) Set(key string, data []byte) {
	shard := lfc.getShard(key)
	
	shard.mu.Lock()
	defer shard.mu.Unlock()
	
	// Copy current map
	oldMap := shard.data.Load().(map[string]*LockFreeCacheEntry)
	newMap := make(map[string]*LockFreeCacheEntry, len(oldMap)+1)
	
	// Copy existing entries
	for k, v := range oldMap {
		newMap[k] = v
	}
	
	// Evict if shard is full
	shardMaxSize := lfc.maxSize / len(lfc.shards)
	if len(newMap) >= shardMaxSize {
		// Simple FIFO eviction - remove first entry
		for k := range newMap {
			delete(newMap, k)
			shard.size.Add(-1)
			break
		}
	}
	
	// Add new entry
	newMap[key] = &LockFreeCacheEntry{
		Data:      data,
		ExpiresAt: time.Now().Unix() + lfc.ttl,
	}
	
	// Atomic swap
	shard.data.Store(newMap)
	shard.size.Add(1)
}

// Delete removes a value from cache
func (lfc *LockFreeCache) Delete(key string) {
	shard := lfc.getShard(key)
	
	shard.mu.Lock()
	defer shard.mu.Unlock()
	
	// Copy current map
	oldMap := shard.data.Load().(map[string]*LockFreeCacheEntry)
	
	if _, exists := oldMap[key]; !exists {
		return
	}
	
	newMap := make(map[string]*LockFreeCacheEntry, len(oldMap)-1)
	
	// Copy all except deleted key
	for k, v := range oldMap {
		if k != key {
			newMap[k] = v
		}
	}
	
	// Atomic swap
	shard.data.Store(newMap)
	shard.size.Add(-1)
}

// Clear removes all entries
func (lfc *LockFreeCache) Clear() {
	for _, shard := range lfc.shards {
		shard.mu.Lock()
		shard.data.Store(make(map[string]*LockFreeCacheEntry))
		shard.size.Store(0)
		shard.mu.Unlock()
	}
}

// Stats returns cache statistics
func (lfc *LockFreeCache) Stats() (hits, misses uint64, size int) {
	hits = lfc.hits.Load()
	misses = lfc.misses.Load()
	
	totalSize := 0
	for _, shard := range lfc.shards {
		totalSize += int(shard.size.Load())
	}
	
	return hits, misses, totalSize
}

// cleanup periodically removes expired entries
func (lfc *LockFreeCache) cleanup() {
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()
	
	for range ticker.C {
		now := time.Now().Unix()
		
		for _, shard := range lfc.shards {
			shard.mu.Lock()
			
			oldMap := shard.data.Load().(map[string]*LockFreeCacheEntry)
			newMap := make(map[string]*LockFreeCacheEntry)
			
			removed := 0
			for k, v := range oldMap {
				if now <= v.ExpiresAt {
					newMap[k] = v
				} else {
					removed++
				}
			}
			
			if removed > 0 {
				shard.data.Store(newMap)
				shard.size.Add(int32(-removed))
			}
			
			shard.mu.Unlock()
		}
	}
}

// fnv1a hash function
func fnv1a(s string) uint64 {
	const (
		offset64 = 14695981039346656037
		prime64  = 1099511628211
	)
	
	hash := uint64(offset64)
	for i := 0; i < len(s); i++ {
		hash ^= uint64(s[i])
		hash *= prime64
	}
	return hash
}
