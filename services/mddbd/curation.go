package main

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"mddb/internal/binlog"
	"strings"
	"sync"
	"time"

	bolt "go.etcd.io/bbolt"
	json "mddb/internal/jsonx"
)

var bucketCuration = []byte("curation")

// PinnedDoc pins a specific document to a fixed 1-based position when a
// curation rule matches. Pinning is by `key` (+ optional `lang`) because
// keys are what editors know; docIDs are internal.
type PinnedDoc struct {
	Key      string `json:"key"`
	Lang     string `json:"lang,omitempty"`
	Position int    `json:"position"` // 1-based; 0 / <0 is treated as "append"
}

// CurationRule overrides the ranking of FTS / Hybrid results for specific
// queries. One rule pins a set of documents to fixed positions and/or hides
// other documents entirely.
//
// Lifecycle: rules live in a dedicated bolt bucket keyed by ID. The in-memory
// cache is refreshed on every Set/Delete so readers never need to hit disk
// during search.
type CurationRule struct {
	ID         string      `json:"id"`
	Collection string      `json:"collection"`
	Query      string      `json:"query"`     // trigger text
	MatchMode  string      `json:"matchMode"` // "exact" (default) or "contains"
	Pins       []PinnedDoc `json:"pins,omitempty"`
	Hides      []string    `json:"hides,omitempty"` // document keys (not docIDs) to drop
	Enabled    bool        `json:"enabled"`
	CreatedAt  int64       `json:"createdAt"`
	UpdatedAt  int64       `json:"updatedAt"`
}

// CurationManager owns the curation bolt bucket and an in-memory cache keyed
// by rule ID, plus a per-collection index for fast lookup during search.
type CurationManager struct {
	db        *bolt.DB
	mu        sync.RWMutex
	byID      map[string]*CurationRule
	byColl    map[string][]*CurationRule // collection -> rules
	binlog    *binlog.Binlog
	marshaler func(v any) ([]byte, error) // override for tests
}

// NewCurationManager constructs a manager bound to db; callers must invoke
// EnsureBucket then LoadAll before search traffic.
func NewCurationManager(db *bolt.DB) *CurationManager {
	return &CurationManager{
		db:        db,
		byID:      make(map[string]*CurationRule),
		byColl:    make(map[string][]*CurationRule),
		marshaler: json.Marshal,
	}
}

// SetBinlog wires replication logging.
func (cm *CurationManager) SetBinlog(bl *binlog.Binlog) { cm.binlog = bl }

// EnsureBucket is called once during server startup.
func (cm *CurationManager) EnsureBucket() error {
	return cm.db.Update(func(tx *bolt.Tx) error {
		_, err := tx.CreateBucketIfNotExists(bucketCuration)
		return err
	})
}

// LoadAll rebuilds the in-memory cache from bolt. Called on startup and
// after binlog replay.
func (cm *CurationManager) LoadAll() error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	cm.byID = make(map[string]*CurationRule)
	cm.byColl = make(map[string][]*CurationRule)

	return cm.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketCuration)
		if b == nil {
			return nil
		}
		return b.ForEach(func(k, v []byte) error {
			var r CurationRule
			if err := json.Unmarshal(v, &r); err != nil {
				return nil // skip corrupt
			}
			cm.indexLocked(&r)
			return nil
		})
	})
}

func (cm *CurationManager) indexLocked(r *CurationRule) {
	cm.byID[r.ID] = r
	cm.byColl[r.Collection] = append(cm.byColl[r.Collection], r)
}

// Set creates a new rule (if cfg.ID == "") or replaces an existing rule.
// Validation: collection + query required; each pin must have a non-empty key.
func (cm *CurationManager) Set(rule *CurationRule) error {
	if rule == nil {
		return errors.New("nil rule")
	}
	if rule.Collection == "" {
		return errors.New("curation rule: collection is required")
	}
	if rule.Query == "" {
		return errors.New("curation rule: query is required")
	}
	if rule.MatchMode == "" {
		rule.MatchMode = "exact"
	}
	if rule.MatchMode != "exact" && rule.MatchMode != "contains" {
		return fmt.Errorf("curation rule: invalid matchMode %q", rule.MatchMode)
	}
	for i, p := range rule.Pins {
		if p.Key == "" {
			return fmt.Errorf("curation rule: pin #%d has empty key", i+1)
		}
	}

	now := time.Now().Unix()
	if rule.ID == "" {
		rule.ID = newCurationID()
		rule.CreatedAt = now
	}
	if rule.CreatedAt == 0 {
		rule.CreatedAt = now
	}
	rule.UpdatedAt = now

	val, err := cm.marshaler(rule)
	if err != nil {
		return err
	}
	key := []byte(rule.ID)

	if err := cm.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketCuration)
		if b == nil {
			return errors.New("curation bucket missing")
		}
		return b.Put(key, val)
	}); err != nil {
		return err
	}

	if cm.binlog != nil {
		_ = cm.binlog.Append(&binlog.BinlogEntry{Type: binlog.BinlogPut, BucketName: "curation", Key: CopyBytes(key), Value: CopyBytes(val)})
	}

	cm.mu.Lock()
	// Drop any prior copy from the per-collection slice.
	if prev, ok := cm.byID[rule.ID]; ok {
		cm.removeFromCollLocked(prev)
	}
	cm.indexLocked(rule)
	cm.mu.Unlock()
	return nil
}

func (cm *CurationManager) removeFromCollLocked(r *CurationRule) {
	slice := cm.byColl[r.Collection]
	for i, cand := range slice {
		if cand.ID == r.ID {
			cm.byColl[r.Collection] = append(slice[:i], slice[i+1:]...)
			break
		}
	}
	if len(cm.byColl[r.Collection]) == 0 {
		delete(cm.byColl, r.Collection)
	}
}

// Get returns a rule by ID. ok is false if absent.
func (cm *CurationManager) Get(id string) (*CurationRule, bool) {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	r, ok := cm.byID[id]
	return r, ok
}

// ListByCollection returns all rules scoped to a collection. The returned
// slice is a defensive copy safe to mutate.
func (cm *CurationManager) ListByCollection(collection string) []*CurationRule {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	src := cm.byColl[collection]
	out := make([]*CurationRule, len(src))
	copy(out, src)
	return out
}

// ListAll returns every rule. Used by admin endpoints and tests.
func (cm *CurationManager) ListAll() []*CurationRule {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	out := make([]*CurationRule, 0, len(cm.byID))
	for _, r := range cm.byID {
		out = append(out, r)
	}
	return out
}

// Delete removes a rule by ID. No-op if already gone.
func (cm *CurationManager) Delete(id string) error {
	key := []byte(id)
	if err := cm.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketCuration)
		if b == nil {
			return nil
		}
		return b.Delete(key)
	}); err != nil {
		return err
	}
	if cm.binlog != nil {
		_ = cm.binlog.Append(&binlog.BinlogEntry{Type: binlog.BinlogDelete, BucketName: "curation", Key: CopyBytes(key)})
	}
	cm.mu.Lock()
	if prev, ok := cm.byID[id]; ok {
		cm.removeFromCollLocked(prev)
		delete(cm.byID, id)
	}
	cm.mu.Unlock()
	return nil
}

// MatchingRules returns enabled rules whose Query matches the given search
// query under their configured MatchMode. Case-insensitive on both sides.
func (cm *CurationManager) MatchingRules(collection, query string) []*CurationRule {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return nil
	}
	var out []*CurationRule
	for _, r := range cm.byColl[collection] {
		if !r.Enabled {
			continue
		}
		rq := strings.ToLower(strings.TrimSpace(r.Query))
		switch r.MatchMode {
		case "contains":
			if strings.Contains(q, rq) {
				out = append(out, r)
			}
		default: // "exact"
			if q == rq {
				out = append(out, r)
			}
		}
	}
	return out
}

// newCurationID generates a random 16-byte hex ID. Prefix "cur_" aids debugging.
func newCurationID() string {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		// crypto/rand failure is exceptional — fall back to timestamp to keep writes flowing.
		return fmt.Sprintf("cur_fallback_%d", time.Now().UnixNano())
	}
	return "cur_" + hex.EncodeToString(buf[:])
}

// Helper used by tests to assert index consistency.
func (cm *CurationManager) indexSize() int {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	return len(cm.byID)
}
