package main

import (
	"encoding/binary"
	"errors"
	"sort"
	"sync"
	"time"

	"mddb/internal/binlog"
	"mddb/internal/boltx"
	json "mddb/internal/jsonx"

	bolt "go.etcd.io/bbolt"
)

// ChatScenarioManager holds the scenarios (#403), cached in memory and
// stored in the main database — so backups carry them and the binlog
// replicates them, as with aliases and collection configs.
//
// One global version counter bumps on every create, update and delete, and
// is persisted with the scenarios, so a consumer polls one number and the
// number keeps growing across restarts.
type ChatScenarioManager struct {
	db      *bolt.DB
	follow  func() *bolt.DB
	binlog  *binlog.Binlog
	mu      sync.RWMutex
	m       map[string]*ChatScenario
	version uint64
	now     func() time.Time
}

// bucketChatScenarios holds one JSON value per scenario, plus the version
// counter under chatScenarioVersionKey — a key no scenario can have, since a
// name may not hold '|'.
var (
	bucketChatScenarios    = []byte("chat_scenarios")
	chatScenarioVersionKey = []byte("|version")
)

// NewChatScenarioManager creates the manager; call LoadAll before use.
func NewChatScenarioManager(db *bolt.DB) *ChatScenarioManager {
	return &ChatScenarioManager{db: db, m: map[string]*ChatScenario{}, now: time.Now}
}

// FollowDB makes it use the server's current handle after a restore.
func (c *ChatScenarioManager) FollowDB(src func() *bolt.DB) { c.follow = src }

// SetBinlog records scenario writes for replication.
func (c *ChatScenarioManager) SetBinlog(bl *binlog.Binlog) { c.binlog = bl }

func (c *ChatScenarioManager) handle() *bolt.DB { return boltx.Current(c.db, c.follow) }

// LoadAll creates the bucket when missing and reads every scenario and the
// version counter into memory.
func (c *ChatScenarioManager) LoadAll() error {
	m := map[string]*ChatScenario{}
	var version uint64
	err := c.handle().Update(func(tx *bolt.Tx) error {
		b, err := tx.CreateBucketIfNotExists(bucketChatScenarios)
		if err == nil {
			if v := b.Get(chatScenarioVersionKey); len(v) == 8 {
				version = binary.BigEndian.Uint64(v)
			}
			err = b.ForEach(func(k, v []byte) error {
				var sc ChatScenario
				// The version key is skipped, and so is a corrupt entry, as
				// the collection manager does.
				if string(k) != string(chatScenarioVersionKey) && json.Unmarshal(v, &sc) == nil {
					m[string(k)] = &sc
				}
				return nil
			})
		}
		return err
	})
	if err != nil {
		return err
	}
	c.mu.Lock()
	c.m, c.version = m, version
	c.mu.Unlock()
	return nil
}

// Version is the current counter. Safe on a nil manager.
func (c *ChatScenarioManager) Version() uint64 {
	if c == nil {
		return 0
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.version
}

// Get returns a copy of one scenario.
func (c *ChatScenarioManager) Get(name string) (*ChatScenario, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	sc, ok := c.m[name]
	if !ok {
		return nil, false
	}
	cp := *sc
	return &cp, true
}

// List returns every scenario, sorted by name.
func (c *ChatScenarioManager) List() []*ChatScenario {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]*ChatScenario, 0, len(c.m))
	for _, sc := range c.m {
		cp := *sc
		out = append(out, &cp)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Put creates or replaces a scenario, already normalized by the caller. It
// reports whether the scenario is new, and bumps the version.
func (c *ChatScenarioManager) Put(sc *ChatScenario) (created bool, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now().UTC()
	prev, exists := c.m[sc.Name]
	sc.CreatedAt, sc.UpdatedAt = now, now
	if exists {
		sc.CreatedAt = prev.CreatedAt
	}
	val, err := json.Marshal(sc)
	if err != nil {
		return false, err
	}
	if err := c.write(func(b *bolt.Bucket) error { return b.Put([]byte(sc.Name), val) },
		&binlog.BinlogEntry{Type: binlog.BinlogPut, BucketName: string(bucketChatScenarios), Key: []byte(sc.Name), Value: val}); err != nil {
		return false, err
	}
	cp := *sc
	c.m[sc.Name] = &cp
	return !exists, nil
}

// Delete removes a scenario and bumps the version.
func (c *ChatScenarioManager) Delete(name string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.m[name]; !ok {
		return errChatScenarioNotFound
	}
	if err := c.write(func(b *bolt.Bucket) error { return b.Delete([]byte(name)) },
		&binlog.BinlogEntry{Type: binlog.BinlogDelete, BucketName: string(bucketChatScenarios), Key: []byte(name)}); err != nil {
		return err
	}
	delete(c.m, name)
	return nil
}

// write applies one change and the version bump in a single transaction,
// then records both in the binlog. Called under the lock.
func (c *ChatScenarioManager) write(change func(*bolt.Bucket) error, entry *binlog.BinlogEntry) error {
	next := make([]byte, 8)
	binary.BigEndian.PutUint64(next, c.version+1)
	if err := c.handle().Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketChatScenarios)
		if b == nil {
			return errors.New("chat scenarios are not loaded")
		}
		if err := change(b); err != nil {
			return err
		}
		return b.Put(chatScenarioVersionKey, next)
	}); err != nil {
		return err
	}
	if c.binlog != nil {
		_ = c.binlog.Append(entry)
		_ = c.binlog.Append(&binlog.BinlogEntry{Type: binlog.BinlogPut, BucketName: string(bucketChatScenarios),
			Key: chatScenarioVersionKey, Value: next})
	}
	c.version++
	return nil
}
