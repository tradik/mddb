package main

import (
	"sync"
	"time"
)

// CacheEntry represents a cached document
type CacheEntry struct {
	Data      []byte
	ExpiresAt int64
}

// DocumentCache is a simple LRU cache for hot documents
type DocumentCache struct {
	cache    map[string]*CacheEntry
	mu       sync.RWMutex
	maxSize  int
	ttl      int64 // seconds
	hits     uint64
	misses   uint64
}

// NewDocumentCache creates a new document cache
func NewDocumentCache(maxSize int, ttlSeconds int64) *DocumentCache {
	if maxSize <= 0 {
		maxSize = 1000 // Default 1000 documents
	}
	if ttlSeconds <= 0 {
		ttlSeconds = 300 // Default 5 minutes
	}
	
	cache := &DocumentCache{
		cache:   make(map[string]*CacheEntry, maxSize),
		maxSize: maxSize,
		ttl:     ttlSeconds,
	}
	
	// Start cleanup goroutine
	go cache.cleanup()
	
	return cache
}

// Get retrieves a document from cache
func (dc *DocumentCache) Get(key string) ([]byte, bool) {
	dc.mu.RLock()
	defer dc.mu.RUnlock()
	
	entry, exists := dc.cache[key]
	if !exists {
		dc.misses++
		return nil, false
	}
	
	// Check if expired
	if time.Now().Unix() > entry.ExpiresAt {
		dc.misses++
		return nil, false
	}
	
	dc.hits++
	return entry.Data, true
}

// Set stores a document in cache
func (dc *DocumentCache) Set(key string, data []byte) {
	dc.mu.Lock()
	defer dc.mu.Unlock()
	
	// Evict if cache is full (simple FIFO, not true LRU)
	if len(dc.cache) >= dc.maxSize {
		// Remove first entry (simple eviction)
		for k := range dc.cache {
			delete(dc.cache, k)
			break
		}
	}
	
	dc.cache[key] = &CacheEntry{
		Data:      data,
		ExpiresAt: time.Now().Unix() + dc.ttl,
	}
}

// Delete removes a document from cache
func (dc *DocumentCache) Delete(key string) {
	dc.mu.Lock()
	defer dc.mu.Unlock()
	delete(dc.cache, key)
}

// Clear removes all entries from cache
func (dc *DocumentCache) Clear() {
	dc.mu.Lock()
	defer dc.mu.Unlock()
	dc.cache = make(map[string]*CacheEntry, dc.maxSize)
}

// Stats returns cache statistics
func (dc *DocumentCache) Stats() (hits, misses uint64, size int) {
	dc.mu.RLock()
	defer dc.mu.RUnlock()
	return dc.hits, dc.misses, len(dc.cache)
}

// cleanup periodically removes expired entries
func (dc *DocumentCache) cleanup() {
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()
	
	for range ticker.C {
		dc.mu.Lock()
		now := time.Now().Unix()
		for key, entry := range dc.cache {
			if now > entry.ExpiresAt {
				delete(dc.cache, key)
			}
		}
		dc.mu.Unlock()
	}
}

// BuildCacheKey builds a cache key for a document
func BuildCacheKey(collection, key, lang string) string {
	return collection + "|" + key + "|" + lang
}
