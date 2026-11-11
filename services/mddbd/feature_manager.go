package main

import (
	"encoding/binary"
	"errors"
	"sync"
	"time"

	"mddb/internal/binlog"
	"mddb/internal/boltx"
	json "mddb/internal/jsonx"

	bolt "go.etcd.io/bbolt"
)

// FeatureManager holds the module switches (GO-049), cached in memory and
// stored in the main database like the chat scenarios: backups carry them,
// the binlog replicates them, and a version counter, persisted with them,
// bumps on every change so a consumer (mddb-chat) polls one number.
//
// Only switches someone set are stored; a module never touched follows its
// default. MDDB_FEATURES_FORCE_OFF, read once at start, wins over both.
type FeatureManager struct {
	db       *bolt.DB
	follow   func() *bolt.DB
	binlog   *binlog.Binlog
	mu       sync.RWMutex
	m        map[string]featureSetting
	version  uint64
	forceOff map[string]bool
	now      func() time.Time
}

// featureSetting is what is stored for one module.
type featureSetting struct {
	Name      string    `json:"name"`
	Enabled   bool      `json:"enabled"`
	UpdatedAt time.Time `json:"updatedAt"`
	UpdatedBy string    `json:"updatedBy,omitempty"`
}

// bucketFeatures holds one JSON value per switch set, plus the version
// counter under featureVersionKey, which no module name can be.
var (
	bucketFeatures    = []byte("features")
	featureVersionKey = []byte("|version")
)

// Errors of SetFeature, mapped to 404, 409 and 503 by each surface.
var (
	errFeatureUnknown     = errors.New("unknown feature")
	errFeatureLocked      = errors.New("feature locked by " + featuresForceOffEnv)
	errFeatureUnavailable = errors.New("features are not loaded")
)

// NewFeatureManager creates the manager with MDDB_FEATURES_FORCE_OFF as it is
// now; call LoadAll before use.
func NewFeatureManager(db *bolt.DB) *FeatureManager {
	return &FeatureManager{db: db, m: map[string]featureSetting{}, forceOff: featuresForcedOff(), now: time.Now}
}

// FollowDB makes it use the server's current handle after a restore.
func (f *FeatureManager) FollowDB(src func() *bolt.DB) { f.follow = src }

// SetBinlog records switch changes for replication.
func (f *FeatureManager) SetBinlog(bl *binlog.Binlog) { f.binlog = bl }

func (f *FeatureManager) handle() *bolt.DB { return boltx.Current(f.db, f.follow) }

// LoadAll creates the bucket when missing and reads the switches and the
// version counter into memory. A corrupt entry is skipped: its module
// follows its default.
func (f *FeatureManager) LoadAll() error {
	m := map[string]featureSetting{}
	var version uint64
	err := f.handle().Update(func(tx *bolt.Tx) error {
		b, err := tx.CreateBucketIfNotExists(bucketFeatures)
		if err != nil {
			return err
		}
		if v := b.Get(featureVersionKey); len(v) == 8 {
			version = binary.BigEndian.Uint64(v)
		}
		return b.ForEach(func(k, v []byte) error {
			var s featureSetting
			if string(k) != string(featureVersionKey) && json.Unmarshal(v, &s) == nil {
				m[string(k)] = s
			}
			return nil
		})
	})
	if err != nil {
		return err
	}
	f.mu.Lock()
	f.m, f.version = m, version
	f.mu.Unlock()
	return nil
}

// Version is the counter. Safe on a nil manager.
func (f *FeatureManager) Version() uint64 {
	if f == nil {
		return 0
	}
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.version
}

// setting returns a module's stored switch, or its default. Called under the
// lock (or on a nil manager, which has only defaults).
func (f *FeatureManager) setting(d featureDef) featureSetting {
	if f != nil {
		if s, ok := f.m[d.Name]; ok {
			return s
		}
	}
	return featureSetting{Name: d.Name, Enabled: d.Default}
}

// forced reports whether the operator forced a module off.
func (f *FeatureManager) forced(name string) bool { return f != nil && f.forceOff[name] }

// Set stores one module's switch and bumps the version. A module forced off
// by the environment cannot be changed at all.
func (f *FeatureManager) Set(name string, enabled bool, by string) error {
	if _, known := featureByName(name); !known {
		return errFeatureUnknown
	}
	if f.forced(name) {
		return errFeatureLocked
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	s := featureSetting{Name: name, Enabled: enabled, UpdatedAt: f.now().UTC(), UpdatedBy: by}
	val, _ := json.Marshal(s) // a struct of plain fields always encodes
	next := make([]byte, 8)
	binary.BigEndian.PutUint64(next, f.version+1)
	if err := f.handle().Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketFeatures)
		if b == nil {
			return errFeatureUnavailable
		}
		if err := b.Put([]byte(name), val); err != nil {
			return err
		}
		return b.Put(featureVersionKey, next)
	}); err != nil {
		return err
	}
	if f.binlog != nil {
		_ = f.binlog.Append(&binlog.BinlogEntry{Type: binlog.BinlogPut, BucketName: string(bucketFeatures), Key: []byte(name), Value: val})
		_ = f.binlog.Append(&binlog.BinlogEntry{Type: binlog.BinlogPut, BucketName: string(bucketFeatures), Key: featureVersionKey, Value: next})
	}
	f.m[name] = s
	f.version++
	return nil
}
