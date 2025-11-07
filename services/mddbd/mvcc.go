package main

import (
	"sync"
	"sync/atomic"
	"time"
)

// MVCC implements Multi-Version Concurrency Control
type MVCC struct {
	versions sync.Map // key -> *VersionChain
	txnID    atomic.Uint64
	gcTicker *time.Ticker
	gcDone   chan struct{}
}

// VersionChain holds multiple versions of a document
type VersionChain struct {
	mu       sync.RWMutex
	versions []*Version
	current  int // Index of current version
}

// Version represents a single version of a document
type Version struct {
	TxnID     uint64
	Timestamp int64
	Data      []byte
	Deleted   bool
	Visible   bool // Committed and visible
}

// NewMVCC creates a new MVCC manager
func NewMVCC() *MVCC {
	mvcc := &MVCC{
		gcDone: make(chan struct{}),
	}
	
	// Start garbage collector
	mvcc.gcTicker = time.NewTicker(10 * time.Second)
	go mvcc.garbageCollector()
	
	return mvcc
}

// BeginTxn starts a new transaction
func (m *MVCC) BeginTxn() uint64 {
	return m.txnID.Add(1)
}

// Read reads a document at a specific transaction ID (snapshot isolation)
func (m *MVCC) Read(key string, txnID uint64) ([]byte, bool) {
	value, ok := m.versions.Load(key)
	if !ok {
		return nil, false
	}
	
	chain := value.(*VersionChain)
	chain.mu.RLock()
	defer chain.mu.RUnlock()
	
	// Find the latest visible version <= txnID
	for i := len(chain.versions) - 1; i >= 0; i-- {
		v := chain.versions[i]
		if v.Visible && v.TxnID <= txnID && !v.Deleted {
			return v.Data, true
		}
	}
	
	return nil, false
}

// Write writes a new version of a document
func (m *MVCC) Write(key string, data []byte, txnID uint64) {
	now := time.Now().Unix()
	
	newVersion := &Version{
		TxnID:     txnID,
		Timestamp: now,
		Data:      data,
		Deleted:   false,
		Visible:   false, // Not visible until commit
	}
	
	// Load or create version chain
	value, _ := m.versions.LoadOrStore(key, &VersionChain{
		versions: make([]*Version, 0, 4),
	})
	
	chain := value.(*VersionChain)
	chain.mu.Lock()
	defer chain.mu.Unlock()
	
	// Append new version
	chain.versions = append(chain.versions, newVersion)
}

// Delete marks a document as deleted
func (m *MVCC) Delete(key string, txnID uint64) {
	now := time.Now().Unix()
	
	deleteVersion := &Version{
		TxnID:     txnID,
		Timestamp: now,
		Data:      nil,
		Deleted:   true,
		Visible:   false,
	}
	
	value, ok := m.versions.Load(key)
	if !ok {
		// Create tombstone
		m.versions.Store(key, &VersionChain{
			versions: []*Version{deleteVersion},
		})
		return
	}
	
	chain := value.(*VersionChain)
	chain.mu.Lock()
	defer chain.mu.Unlock()
	
	chain.versions = append(chain.versions, deleteVersion)
}

// Commit makes all versions for a transaction visible
func (m *MVCC) Commit(txnID uint64) {
	m.versions.Range(func(key, value interface{}) bool {
		chain := value.(*VersionChain)
		chain.mu.Lock()
		
		for _, v := range chain.versions {
			if v.TxnID == txnID {
				v.Visible = true
			}
		}
		
		chain.mu.Unlock()
		return true
	})
}

// Rollback removes all versions for a transaction
func (m *MVCC) Rollback(txnID uint64) {
	m.versions.Range(func(key, value interface{}) bool {
		chain := value.(*VersionChain)
		chain.mu.Lock()
		
		// Remove versions with this txnID
		filtered := make([]*Version, 0, len(chain.versions))
		for _, v := range chain.versions {
			if v.TxnID != txnID {
				filtered = append(filtered, v)
			}
		}
		chain.versions = filtered
		
		chain.mu.Unlock()
		return true
	})
}

// garbageCollector removes old versions
func (m *MVCC) garbageCollector() {
	for {
		select {
		case <-m.gcTicker.C:
			m.gc()
		case <-m.gcDone:
			return
		}
	}
}

// gc performs garbage collection
func (m *MVCC) gc() {
	cutoff := time.Now().Unix() - 300 // Keep versions for 5 minutes
	
	m.versions.Range(func(key, value interface{}) bool {
		chain := value.(*VersionChain)
		chain.mu.Lock()
		
		// Keep only recent versions
		kept := make([]*Version, 0, len(chain.versions))
		for _, v := range chain.versions {
			if v.Timestamp > cutoff || !v.Visible {
				kept = append(kept, v)
			}
		}
		
		// Always keep at least one version
		if len(kept) == 0 && len(chain.versions) > 0 {
			kept = chain.versions[len(chain.versions)-1:]
		}
		
		chain.versions = kept
		
		chain.mu.Unlock()
		return true
	})
}

// Close stops the MVCC manager
func (m *MVCC) Close() {
	m.gcTicker.Stop()
	close(m.gcDone)
}

// Stats returns MVCC statistics
func (m *MVCC) Stats() (keys int, totalVersions int) {
	m.versions.Range(func(key, value interface{}) bool {
		keys++
		chain := value.(*VersionChain)
		chain.mu.RLock()
		totalVersions += len(chain.versions)
		chain.mu.RUnlock()
		return true
	})
	return
}
