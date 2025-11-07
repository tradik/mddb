package main

import (
	"sync"

	"github.com/bits-and-blooms/bloom/v3"
	bolt "go.etcd.io/bbolt"
)

// BloomFilterManager manages bloom filters per collection
type BloomFilterManager struct {
	filters sync.Map // collection -> *bloom.BloomFilter
	mu      sync.RWMutex
}

// NewBloomFilterManager creates a new bloom filter manager
func NewBloomFilterManager() *BloomFilterManager {
	return &BloomFilterManager{}
}

// GetOrCreate gets or creates a bloom filter for a collection
func (bfm *BloomFilterManager) GetOrCreate(collection string, expectedItems uint) *bloom.BloomFilter {
	if filter, ok := bfm.filters.Load(collection); ok {
		return filter.(*bloom.BloomFilter)
	}
	
	bfm.mu.Lock()
	defer bfm.mu.Unlock()
	
	// Double-check after acquiring lock
	if filter, ok := bfm.filters.Load(collection); ok {
		return filter.(*bloom.BloomFilter)
	}
	
	// Create new bloom filter
	// False positive rate: 0.01 (1%)
	filter := bloom.NewWithEstimates(expectedItems, 0.01)
	bfm.filters.Store(collection, filter)
	
	return filter
}

// Add adds a key to the bloom filter
func (bfm *BloomFilterManager) Add(collection, key, lang string) {
	filter := bfm.GetOrCreate(collection, 10000) // Default 10k items
	compositeKey := collection + "|" + key + "|" + lang
	filter.Add([]byte(compositeKey))
}

// Test checks if a key might exist (false positives possible)
func (bfm *BloomFilterManager) Test(collection, key, lang string) bool {
	filter, ok := bfm.filters.Load(collection)
	if !ok {
		return false // Collection doesn't exist
	}
	
	compositeKey := collection + "|" + key + "|" + lang
	return filter.(*bloom.BloomFilter).Test([]byte(compositeKey))
}

// Remove removes a key from the bloom filter
// Note: Bloom filters don't support deletion, so we need to rebuild
func (bfm *BloomFilterManager) Remove(collection, key, lang string) {
	// Bloom filters don't support deletion
	// For now, we accept false positives until rebuild
	// TODO: Implement periodic rebuild
}

// Clear clears a collection's bloom filter
func (bfm *BloomFilterManager) Clear(collection string) {
	bfm.filters.Delete(collection)
}

// Stats returns bloom filter statistics
func (bfm *BloomFilterManager) Stats() map[string]BloomStats {
	stats := make(map[string]BloomStats)
	
	bfm.filters.Range(func(key, value interface{}) bool {
		collection := key.(string)
		filter := value.(*bloom.BloomFilter)
		
		stats[collection] = BloomStats{
			Capacity: filter.Cap(),
			Count:    filter.ApproximatedSize(),
			FPRate:   0.01, // Our configured rate
		}
		return true
	})
	
	return stats
}

// BloomStats represents bloom filter statistics
type BloomStats struct {
	Capacity uint
	Count    uint32
	FPRate   float64
}

// Rebuild rebuilds a bloom filter from database
func (bfm *BloomFilterManager) Rebuild(s *Server, collection string) error {
	// Clear existing filter
	bfm.Clear(collection)
	
	// Count documents first
	var count uint
	err := s.DB.View(func(tx *bolt.Tx) error {
		bDocs := tx.Bucket(s.BucketNames.Docs)
		c := bDocs.Cursor()
		prefix := []byte("doc|" + collection + "|")
		
		for k, _ := c.Seek(prefix); k != nil && len(k) >= len(prefix); k, _ = c.Next() {
			if string(k[:len(prefix)]) != string(prefix) {
				break
			}
			count++
		}
		return nil
	})
	
	if err != nil {
		return err
	}
	
	// Create new filter with correct size
	filter := bfm.GetOrCreate(collection, count+1000) // +1000 for growth
	
	// Populate filter
	return s.DB.View(func(tx *bolt.Tx) error {
		bDocs := tx.Bucket(s.BucketNames.Docs)
		c := bDocs.Cursor()
		prefix := []byte("doc|" + collection + "|")
		
		for k, v := c.Seek(prefix); k != nil && len(k) >= len(prefix); k, v = c.Next() {
			if string(k[:len(prefix)]) != string(prefix) {
				break
			}
			
			// Parse document to get key and lang
			doc, err := unmarshalDoc(v)
			if err != nil {
				continue
			}
			
			compositeKey := collection + "|" + doc.Key + "|" + doc.Lang
			filter.Add([]byte(compositeKey))
		}
		return nil
	})
}
