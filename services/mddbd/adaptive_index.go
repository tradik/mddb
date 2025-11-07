package main

import (
	"sync"
	"sync/atomic"
	"time"
)

// AdaptiveIndexManager manages adaptive indexing strategies
type AdaptiveIndexManager struct {
	queryStats      sync.Map // query pattern -> *QueryStats
	indexStrategies sync.Map // collection -> *IndexStrategy
}

// QueryStats tracks query performance statistics
type QueryStats struct {
	Count          atomic.Uint64
	TotalDuration  atomic.Int64 // nanoseconds
	LastAccessed   atomic.Int64 // unix timestamp
	Pattern        string
	PreferredIndex IndexType
}

// IndexType represents different index types
type IndexType int

const (
	IndexTypeHash IndexType = iota
	IndexTypeBTree
	IndexTypeBitmap
	IndexTypeBloom
	IndexTypeFull // Full scan
)

// IndexStrategy determines which index to use for a collection
type IndexStrategy struct {
	Collection     string
	PrimaryIndex   IndexType
	SecondaryIndex IndexType
	QueryPatterns  map[string]*QueryStats
	LastOptimized  time.Time
	mu             sync.RWMutex
}

// NewAdaptiveIndexManager creates a new adaptive index manager
func NewAdaptiveIndexManager() *AdaptiveIndexManager {
	aim := &AdaptiveIndexManager{}
	
	// Start optimization worker
	go aim.optimizationWorker()
	
	return aim
}

// RecordQuery records a query execution
func (aim *AdaptiveIndexManager) RecordQuery(collection, pattern string, duration time.Duration, resultCount int) {
	key := collection + "|" + pattern
	
	var stats *QueryStats
	if value, ok := aim.queryStats.Load(key); ok {
		stats = value.(*QueryStats)
	} else {
		stats = &QueryStats{
			Pattern: pattern,
		}
		aim.queryStats.Store(key, stats)
	}
	
	stats.Count.Add(1)
	stats.TotalDuration.Add(int64(duration))
	stats.LastAccessed.Store(time.Now().Unix())
	
	// Analyze and potentially update preferred index
	aim.analyzeQuery(collection, pattern, duration, resultCount, stats)
}

// analyzeQuery analyzes query performance and suggests best index
func (aim *AdaptiveIndexManager) analyzeQuery(collection, pattern string, duration time.Duration, resultCount int, stats *QueryStats) {
	count := stats.Count.Load()
	
	// Need at least 10 samples to make decision
	if count < 10 {
		return
	}
	
	avgDuration := time.Duration(stats.TotalDuration.Load() / int64(count))
	
	// Determine best index based on query characteristics
	var preferredIndex IndexType
	
	if resultCount == 0 {
		// No results - Bloom filter is best
		preferredIndex = IndexTypeBloom
	} else if resultCount == 1 {
		// Single result - Hash index is best
		preferredIndex = IndexTypeHash
	} else if resultCount < 100 {
		// Small result set - BTree is good
		preferredIndex = IndexTypeBTree
	} else if resultCount < 10000 {
		// Medium result set - Bitmap index
		preferredIndex = IndexTypeBitmap
	} else {
		// Large result set - might need full scan
		preferredIndex = IndexTypeFull
	}
	
	// Update preferred index if performance improved
	if avgDuration < 10*time.Millisecond {
		stats.PreferredIndex = preferredIndex
	}
}

// GetOptimalIndex returns the optimal index type for a query
func (aim *AdaptiveIndexManager) GetOptimalIndex(collection, pattern string) IndexType {
	key := collection + "|" + pattern
	
	if value, ok := aim.queryStats.Load(key); ok {
		stats := value.(*QueryStats)
		if stats.Count.Load() >= 10 {
			return stats.PreferredIndex
		}
	}
	
	// Default to BTree for unknown patterns
	return IndexTypeBTree
}

// GetStrategy returns the index strategy for a collection
func (aim *AdaptiveIndexManager) GetStrategy(collection string) *IndexStrategy {
	if value, ok := aim.indexStrategies.Load(collection); ok {
		return value.(*IndexStrategy)
	}
	
	// Create new strategy
	strategy := &IndexStrategy{
		Collection:    collection,
		PrimaryIndex:  IndexTypeBTree,
		SecondaryIndex: IndexTypeHash,
		QueryPatterns: make(map[string]*QueryStats),
		LastOptimized: time.Now(),
	}
	
	aim.indexStrategies.Store(collection, strategy)
	return strategy
}

// optimizationWorker periodically optimizes index strategies
func (aim *AdaptiveIndexManager) optimizationWorker() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	
	for range ticker.C {
		aim.optimize()
	}
}

// optimize analyzes all query patterns and optimizes strategies
func (aim *AdaptiveIndexManager) optimize() {
	now := time.Now()
	cutoff := now.Add(-1 * time.Hour).Unix()
	
	// Collect statistics per collection
	collectionStats := make(map[string]map[IndexType]int)
	
	aim.queryStats.Range(func(key, value interface{}) bool {
		stats := value.(*QueryStats)
		
		// Skip old queries
		if stats.LastAccessed.Load() < cutoff {
			return true
		}
		
		// Extract collection from key
		keyStr := key.(string)
		// Format: "collection|pattern"
		collection := keyStr[:len(keyStr)-len(stats.Pattern)-1]
		
		if collectionStats[collection] == nil {
			collectionStats[collection] = make(map[IndexType]int)
		}
		
		collectionStats[collection][stats.PreferredIndex]++
		
		return true
	})
	
	// Update strategies based on statistics
	for collection, indexCounts := range collectionStats {
		strategy := aim.GetStrategy(collection)
		strategy.mu.Lock()
		
		// Find most common index type
		maxCount := 0
		var bestIndex IndexType
		for indexType, count := range indexCounts {
			if count > maxCount {
				maxCount = count
				bestIndex = indexType
			}
		}
		
		strategy.PrimaryIndex = bestIndex
		strategy.LastOptimized = now
		
		strategy.mu.Unlock()
	}
}

// Stats returns adaptive indexing statistics
func (aim *AdaptiveIndexManager) Stats() AdaptiveIndexStats {
	stats := AdaptiveIndexStats{
		Collections: make(map[string]CollectionIndexStats),
	}
	
	aim.indexStrategies.Range(func(key, value interface{}) bool {
		collection := key.(string)
		strategy := value.(*IndexStrategy)
		
		strategy.mu.RLock()
		stats.Collections[collection] = CollectionIndexStats{
			PrimaryIndex:   strategy.PrimaryIndex.String(),
			SecondaryIndex: strategy.SecondaryIndex.String(),
			QueryCount:     len(strategy.QueryPatterns),
			LastOptimized:  strategy.LastOptimized,
		}
		strategy.mu.RUnlock()
		
		return true
	})
	
	// Count total queries
	aim.queryStats.Range(func(key, value interface{}) bool {
		stats.TotalQueries++
		return true
	})
	
	return stats
}

// AdaptiveIndexStats represents statistics
type AdaptiveIndexStats struct {
	TotalQueries uint64
	Collections  map[string]CollectionIndexStats
}

// CollectionIndexStats represents per-collection statistics
type CollectionIndexStats struct {
	PrimaryIndex   string
	SecondaryIndex string
	QueryCount     int
	LastOptimized  time.Time
}

// String returns string representation of IndexType
func (it IndexType) String() string {
	switch it {
	case IndexTypeHash:
		return "hash"
	case IndexTypeBTree:
		return "btree"
	case IndexTypeBitmap:
		return "bitmap"
	case IndexTypeBloom:
		return "bloom"
	case IndexTypeFull:
		return "full"
	default:
		return "unknown"
	}
}
