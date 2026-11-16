package main

import (
	"errors"
	"sort"
	"sync"

	"mddb/internal/binlog"
	"mddb/internal/boltx"
	json "mddb/internal/jsonx"

	bolt "go.etcd.io/bbolt"
)

// TenantManager keeps the tenant configurations (GO-054) in the main
// database, cached in memory: backups carry them and the binlog records each
// change. A tenant nobody configured has no entry.
type TenantManager struct {
	db     *bolt.DB
	follow func() *bolt.DB
	binlog *binlog.Binlog
	mu     sync.RWMutex
	cache  map[string]TenantConfig
	// writeMu orders writes: held across the commit and the cache update, so
	// the cache ends where the database and the log do.
	writeMu sync.Mutex
}

// bucketTenants holds one JSON TenantConfig per tenant name.
var bucketTenants = []byte("tenants")

// errTenantsNotLoaded is a write before LoadAll created the bucket.
var errTenantsNotLoaded = errors.New("tenant configurations are not loaded")

// TenantNames is every tenant some user is assigned to (GO-054), unsorted.
// It lives here, beside the tenant store, rather than with the rest of the
// AuthManager, because only the tenant reports ask for it.
func (am *AuthManager) TenantNames() []string {
	am.mu.RLock()
	defer am.mu.RUnlock()
	seen := map[string]bool{}
	var out []string
	for _, u := range am.users {
		if u.Tenant != "" && !seen[u.Tenant] {
			seen[u.Tenant] = true
			out = append(out, u.Tenant)
		}
	}
	return out
}

// NewTenantManager creates the manager; call LoadAll before use.
func NewTenantManager(db *bolt.DB) *TenantManager {
	return &TenantManager{db: db, cache: map[string]TenantConfig{}}
}

// FollowDB makes it use the server's current handle after a restore.
func (tm *TenantManager) FollowDB(src func() *bolt.DB) { tm.follow = src }

// SetBinlog records changes for replication.
func (tm *TenantManager) SetBinlog(bl *binlog.Binlog) { tm.binlog = bl }

func (tm *TenantManager) handle() *bolt.DB { return boltx.Current(tm.db, tm.follow) }

// update runs a write transaction that records what fn collects in the
// binlog as its last step, in commit order; on a node that does not lead
// the write fails and nothing commits (GO-051).
func (tm *TenantManager) update(fn func(*bolt.Tx, *binlog.BinlogOps) error) error {
	return binlog.Logged(tm.handle().Update, tm.binlog, fn)
}

// LoadAll creates the bucket when missing and reads every configuration. A
// corrupt entry is skipped: its tenant has no limits.
func (tm *TenantManager) LoadAll() error {
	if err := boltx.EnsureBuckets(tm.handle(), bucketTenants); err != nil {
		return err
	}
	m := map[string]TenantConfig{}
	_ = tm.handle().View(func(tx *bolt.Tx) error {
		return tx.Bucket(bucketTenants).ForEach(func(k, v []byte) error {
			var cfg TenantConfig
			if json.Unmarshal(v, &cfg) == nil {
				m[string(k)] = cfg
			}
			return nil
		})
	})
	tm.mu.Lock()
	tm.cache = m
	tm.mu.Unlock()
	return nil
}

// Get is a tenant's configuration. Safe on a nil manager.
func (tm *TenantManager) Get(tenant string) (TenantConfig, bool) {
	if tm == nil {
		return TenantConfig{}, false
	}
	tm.mu.RLock()
	defer tm.mu.RUnlock()
	cfg, ok := tm.cache[tenant]
	return cfg, ok
}

// Names is every configured tenant, sorted.
func (tm *TenantManager) Names() []string {
	if tm == nil {
		return nil
	}
	tm.mu.RLock()
	defer tm.mu.RUnlock()
	out := make([]string, 0, len(tm.cache))
	for name := range tm.cache {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// Set stores a tenant's configuration; one with neither part removes it.
func (tm *TenantManager) Set(tenant string, cfg TenantConfig) error {
	if cfg.Quota == nil && cfg.Limits == nil {
		return tm.Delete(tenant)
	}
	val, _ := json.Marshal(cfg) // plain fields always encode
	key := []byte(tenant)
	tm.writeMu.Lock()
	defer tm.writeMu.Unlock()
	if err := tm.update(func(tx *bolt.Tx, bo *binlog.BinlogOps) error {
		b := tx.Bucket(bucketTenants)
		if b == nil {
			return errTenantsNotLoaded
		}
		bo.Put(string(bucketTenants), key, val)
		return b.Put(key, val)
	}); err != nil {
		return err
	}
	tm.mu.Lock()
	tm.cache[tenant] = cfg
	tm.mu.Unlock()
	return nil
}

// Delete removes a tenant's configuration: no limits of its own any more.
func (tm *TenantManager) Delete(tenant string) error {
	key := []byte(tenant)
	tm.writeMu.Lock()
	defer tm.writeMu.Unlock()
	if err := tm.update(func(tx *bolt.Tx, bo *binlog.BinlogOps) error {
		b := tx.Bucket(bucketTenants)
		if b == nil {
			return nil
		}
		bo.Delete(string(bucketTenants), key)
		return b.Delete(key)
	}); err != nil {
		return err
	}
	tm.mu.Lock()
	delete(tm.cache, tenant)
	tm.mu.Unlock()
	return nil
}
