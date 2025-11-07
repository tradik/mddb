package main

import (
	"fmt"
	"hash/fnv"
	"sync"
	"sync/atomic"
)

// ShardCluster manages distributed sharding
type ShardCluster struct {
	shards      []*Shard
	router      *ConsistentHash
	replication int
	mu          sync.RWMutex
}

// Shard represents a single shard
type Shard struct {
	ID       int
	Name     string
	Server   *Server
	Weight   int
	Active   bool
	DocCount atomic.Uint64
}

// ConsistentHash implements consistent hashing for shard routing
type ConsistentHash struct {
	ring     map[uint32]int // hash -> shard ID
	sortedKeys []uint32
	replicas int
	mu       sync.RWMutex
}

// NewShardCluster creates a new shard cluster
func NewShardCluster(numShards, replication int) *ShardCluster {
	shards := make([]*Shard, numShards)
	
	for i := 0; i < numShards; i++ {
		shards[i] = &Shard{
			ID:     i,
			Name:   fmt.Sprintf("shard-%d", i),
			Weight: 1,
			Active: true,
		}
	}
	
	router := NewConsistentHash(150) // 150 virtual nodes per shard
	for i := 0; i < numShards; i++ {
		router.Add(i, 1)
	}
	
	return &ShardCluster{
		shards:      shards,
		router:      router,
		replication: replication,
	}
}

// NewConsistentHash creates a new consistent hash
func NewConsistentHash(replicas int) *ConsistentHash {
	return &ConsistentHash{
		ring:     make(map[uint32]int),
		replicas: replicas,
	}
}

// Add adds a shard to the hash ring
func (ch *ConsistentHash) Add(shardID, weight int) {
	ch.mu.Lock()
	defer ch.mu.Unlock()
	
	// Add virtual nodes
	for i := 0; i < ch.replicas*weight; i++ {
		hash := ch.hash(fmt.Sprintf("%d-%d", shardID, i))
		ch.ring[hash] = shardID
		ch.sortedKeys = append(ch.sortedKeys, hash)
	}
	
	// Sort keys
	ch.sortKeys()
}

// Remove removes a shard from the hash ring
func (ch *ConsistentHash) Remove(shardID int) {
	ch.mu.Lock()
	defer ch.mu.Unlock()
	
	// Remove virtual nodes
	newKeys := make([]uint32, 0, len(ch.sortedKeys))
	for _, hash := range ch.sortedKeys {
		if ch.ring[hash] != shardID {
			newKeys = append(newKeys, hash)
		} else {
			delete(ch.ring, hash)
		}
	}
	
	ch.sortedKeys = newKeys
}

// Get returns the shard ID for a key
func (ch *ConsistentHash) Get(key string) int {
	ch.mu.RLock()
	defer ch.mu.RUnlock()
	
	if len(ch.ring) == 0 {
		return 0
	}
	
	hash := ch.hash(key)
	
	// Binary search for the first node >= hash
	idx := ch.search(hash)
	
	return ch.ring[ch.sortedKeys[idx]]
}

// GetN returns N shard IDs for replication
func (ch *ConsistentHash) GetN(key string, n int) []int {
	ch.mu.RLock()
	defer ch.mu.RUnlock()
	
	if len(ch.ring) == 0 || n <= 0 {
		return nil
	}
	
	hash := ch.hash(key)
	idx := ch.search(hash)
	
	shards := make([]int, 0, n)
	seen := make(map[int]bool)
	
	for len(shards) < n && len(shards) < len(ch.ring) {
		shardID := ch.ring[ch.sortedKeys[idx]]
		if !seen[shardID] {
			shards = append(shards, shardID)
			seen[shardID] = true
		}
		
		idx = (idx + 1) % len(ch.sortedKeys)
	}
	
	return shards
}

// hash computes hash of a key
func (ch *ConsistentHash) hash(key string) uint32 {
	h := fnv.New32a()
	h.Write([]byte(key))
	return h.Sum32()
}

// search performs binary search
func (ch *ConsistentHash) search(hash uint32) int {
	left, right := 0, len(ch.sortedKeys)-1
	
	for left <= right {
		mid := (left + right) / 2
		
		if ch.sortedKeys[mid] == hash {
			return mid
		}
		
		if ch.sortedKeys[mid] < hash {
			left = mid + 1
		} else {
			right = mid - 1
		}
	}
	
	if left >= len(ch.sortedKeys) {
		return 0
	}
	
	return left
}

// sortKeys sorts the hash ring keys
func (ch *ConsistentHash) sortKeys() {
	// Simple insertion sort (good enough for small arrays)
	for i := 1; i < len(ch.sortedKeys); i++ {
		key := ch.sortedKeys[i]
		j := i - 1
		
		for j >= 0 && ch.sortedKeys[j] > key {
			ch.sortedKeys[j+1] = ch.sortedKeys[j]
			j--
		}
		
		ch.sortedKeys[j+1] = key
	}
}

// GetShard returns a shard by key
func (sc *ShardCluster) GetShard(key string) *Shard {
	shardID := sc.router.Get(key)
	
	sc.mu.RLock()
	defer sc.mu.RUnlock()
	
	if shardID >= 0 && shardID < len(sc.shards) {
		return sc.shards[shardID]
	}
	
	return sc.shards[0] // Fallback
}

// GetShards returns multiple shards for replication
func (sc *ShardCluster) GetShards(key string) []*Shard {
	shardIDs := sc.router.GetN(key, sc.replication)
	
	sc.mu.RLock()
	defer sc.mu.RUnlock()
	
	shards := make([]*Shard, 0, len(shardIDs))
	for _, id := range shardIDs {
		if id >= 0 && id < len(sc.shards) {
			shards = append(shards, sc.shards[id])
		}
	}
	
	return shards
}

// AddShard adds a new shard to the cluster
func (sc *ShardCluster) AddShard(shard *Shard) {
	sc.mu.Lock()
	defer sc.mu.Unlock()
	
	shard.ID = len(sc.shards)
	sc.shards = append(sc.shards, shard)
	sc.router.Add(shard.ID, shard.Weight)
}

// RemoveShard removes a shard from the cluster
func (sc *ShardCluster) RemoveShard(shardID int) error {
	sc.mu.Lock()
	defer sc.mu.Unlock()
	
	if shardID < 0 || shardID >= len(sc.shards) {
		return fmt.Errorf("invalid shard ID: %d", shardID)
	}
	
	sc.shards[shardID].Active = false
	sc.router.Remove(shardID)
	
	return nil
}

// Rebalance rebalances data across shards
func (sc *ShardCluster) Rebalance() error {
	// This is a simplified rebalancing
	// In production, would need to:
	// 1. Calculate optimal distribution
	// 2. Move data between shards
	// 3. Update routing table
	// 4. Verify consistency
	
	sc.mu.RLock()
	defer sc.mu.RUnlock()
	
	totalDocs := uint64(0)
	for _, shard := range sc.shards {
		if shard.Active {
			totalDocs += shard.DocCount.Load()
		}
	}
	
	// Calculate average
	activeShards := 0
	for _, shard := range sc.shards {
		if shard.Active {
			activeShards++
		}
	}
	
	if activeShards == 0 {
		return fmt.Errorf("no active shards")
	}
	
	// In production, would move documents here
	// For now, just log the stats
	
	return nil
}

// Stats returns cluster statistics
func (sc *ShardCluster) Stats() ShardClusterStats {
	sc.mu.RLock()
	defer sc.mu.RUnlock()
	
	stats := ShardClusterStats{
		TotalShards:  len(sc.shards),
		ActiveShards: 0,
		Shards:       make([]ShardStats, len(sc.shards)),
	}
	
	for i, shard := range sc.shards {
		if shard.Active {
			stats.ActiveShards++
		}
		
		stats.Shards[i] = ShardStats{
			ID:       shard.ID,
			Name:     shard.Name,
			Active:   shard.Active,
			DocCount: shard.DocCount.Load(),
			Weight:   shard.Weight,
		}
		
		stats.TotalDocs += shard.DocCount.Load()
	}
	
	return stats
}

// ShardClusterStats represents cluster statistics
type ShardClusterStats struct {
	TotalShards  int
	ActiveShards int
	TotalDocs    uint64
	Shards       []ShardStats
}

// ShardStats represents shard statistics
type ShardStats struct {
	ID       int
	Name     string
	Active   bool
	DocCount uint64
	Weight   int
}
