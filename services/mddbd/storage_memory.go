package main

import (
	"sync"

	"mddb/internal/storage"
)

// MemoryBackend stores documents entirely in memory. Data is lost on restart.
// Useful for ephemeral/scratch collections, testing, and caching scenarios.
type MemoryBackend struct {
	mu    sync.RWMutex
	docs  map[string]map[string][]byte // collection → docID → data
	byKey map[string]string            // storage.ByKeyKey(collection, key, lang) → docID
}

// NewMemoryBackend creates a new in-memory storage backend.
func NewMemoryBackend() *MemoryBackend {
	return &MemoryBackend{
		docs:  make(map[string]map[string][]byte),
		byKey: make(map[string]string),
	}
}

// memKeyIndex is the map key of a key-index entry: bbolt's key
// (storage.ByKeyKey), so the key and language are normalised the same way
// and the collection keeps its case (#360).
func memKeyIndex(collection, key, lang string) string {
	return string(storage.ByKeyKey(collection, key, lang))
}

// Name implements the StorageBackend interface.
func (m *MemoryBackend) Name() string { return "memory" }

// PutDoc implements the StorageBackend interface.
func (m *MemoryBackend) PutDoc(collection, docID string, data []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	col, ok := m.docs[collection]
	if !ok {
		col = make(map[string][]byte)
		m.docs[collection] = col
	}
	buf := make([]byte, len(data))
	copy(buf, data)
	col[docID] = buf
	return nil
}

// GetDoc implements the StorageBackend interface.
func (m *MemoryBackend) GetDoc(collection, docID string) ([]byte, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	col, ok := m.docs[collection]
	if !ok {
		return nil, nil
	}
	data, ok := col[docID]
	if !ok {
		return nil, nil
	}
	buf := make([]byte, len(data))
	copy(buf, data)
	return buf, nil
}

// DeleteDoc implements the StorageBackend interface.
func (m *MemoryBackend) DeleteDoc(collection, docID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	col, ok := m.docs[collection]
	if !ok {
		return nil
	}
	delete(col, docID)
	if len(col) == 0 {
		delete(m.docs, collection)
	}
	return nil
}

// ListDocs implements the StorageBackend interface.
func (m *MemoryBackend) ListDocs(collection string, fn func(docID string, data []byte) error) error {
	m.mu.RLock()
	col, ok := m.docs[collection]
	if !ok {
		m.mu.RUnlock()
		return nil
	}
	// snapshot keys to avoid holding lock during callback
	type entry struct {
		id   string
		data []byte
	}
	entries := make([]entry, 0, len(col))
	for id, data := range col {
		entries = append(entries, entry{id, data})
	}
	m.mu.RUnlock()

	for _, e := range entries {
		if err := fn(e.id, e.data); err != nil {
			return err
		}
	}
	return nil
}

// PutByKey implements the StorageBackend interface.
func (m *MemoryBackend) PutByKey(collection, key, lang, docID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.byKey[memKeyIndex(collection, key, lang)] = docID
	return nil
}

// GetByKey implements the StorageBackend interface.
func (m *MemoryBackend) GetByKey(collection, key, lang string) (string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	id := m.byKey[memKeyIndex(collection, key, lang)]
	return id, nil
}

// DeleteByKey implements the StorageBackend interface.
func (m *MemoryBackend) DeleteByKey(collection, key, lang string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.byKey, memKeyIndex(collection, key, lang))
	return nil
}

// Close implements the StorageBackend interface.
func (m *MemoryBackend) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.docs = make(map[string]map[string][]byte)
	m.byKey = make(map[string]string)
	return nil
}

// Stats returns the document count for a collection.
func (m *MemoryBackend) Stats(collection string) (docCount int) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if col, ok := m.docs[collection]; ok {
		return len(col)
	}
	return 0
}

// Ensure MemoryBackend implements StorageBackend at compile time.
var _ StorageBackend = (*MemoryBackend)(nil)
