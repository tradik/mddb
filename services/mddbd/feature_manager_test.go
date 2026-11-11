package main

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"mddb/internal/binlog"

	bolt "go.etcd.io/bbolt"
)

// GO-049: the module registry, the persisted switches and what "enabled"
// means with defaults, requirements and the environment lock.

// featureManager opens a fresh store with MDDB_FEATURES_FORCE_OFF as given.
func featureManager(t *testing.T, forceOff string) (*FeatureManager, *bolt.DB) {
	t.Helper()
	t.Setenv(featuresForceOffEnv, forceOff)
	db, err := bolt.Open(filepath.Join(t.TempDir(), "f.db"), 0o600, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	f := NewFeatureManager(db)
	if err := f.LoadAll(); err != nil {
		t.Fatal(err)
	}
	return f, db
}

func TestFeatureRegistryDefaults(t *testing.T) {
	f, _ := featureManager(t, "")
	list := f.List()
	if len(list) != len(featureRegistry) || len(list) != 14 {
		t.Fatalf("registry: %d modules", len(list))
	}
	for _, st := range list {
		want := st.Name != FeatureChatExternalMCP
		if st.Enabled != want || st.Configured != want || st.Default != want || st.LockedBy != "" || st.UpdatedAt != nil {
			t.Errorf("%s by default: %+v", st.Name, st)
		}
	}
	if !f.Enabled("documents") {
		t.Error("a name outside the registry is core and always on")
	}
	if _, ok := f.State("documents"); ok {
		t.Error("core has no state")
	}
	if _, ok := featureByName("nope"); ok {
		t.Error("unknown found")
	}
	var nilMgr *FeatureManager
	if !nilMgr.Enabled(FeatureGeo) || nilMgr.Enabled(FeatureChatExternalMCP) || nilMgr.Version() != 0 || len(nilMgr.List()) != 14 {
		t.Error("a nil manager knows the defaults")
	}
	if st, ok := nilMgr.State(FeatureGeo); !ok || !st.Enabled {
		t.Errorf("nil state: %+v", st)
	}
}

func TestFeatureSetPersistsAndBumpsVersion(t *testing.T) {
	f, db := featureManager(t, "")
	at := time.Date(2026, 11, 11, 10, 0, 0, 0, time.UTC)
	f.now = func() time.Time { return at }
	if err := f.Set(FeatureGeo, false, "root"); err != nil {
		t.Fatal(err)
	}
	if f.Enabled(FeatureGeo) || f.Version() != 1 {
		t.Fatalf("after switching off: enabled %v, version %d", f.Enabled(FeatureGeo), f.Version())
	}
	st, _ := f.State(FeatureGeo)
	if st.Configured || st.UpdatedBy != "root" || st.UpdatedAt == nil || !st.UpdatedAt.Equal(at) {
		t.Errorf("state: %+v", st)
	}
	if err := f.Set("nope", true, ""); !errors.Is(err, errFeatureUnknown) {
		t.Errorf("unknown: %v", err)
	}
	// A corrupt entry is skipped on load; the module follows its default.
	if err := db.Update(func(tx *bolt.Tx) error { return tx.Bucket(bucketFeatures).Put([]byte(FeatureSpell), []byte("{")) }); err != nil {
		t.Fatal(err)
	}
	again := NewFeatureManager(db)
	if err := again.LoadAll(); err != nil {
		t.Fatal(err)
	}
	if again.Enabled(FeatureGeo) || !again.Enabled(FeatureSpell) || again.Version() != 1 {
		t.Errorf("reloaded: geo %v spell %v version %d", again.Enabled(FeatureGeo), again.Enabled(FeatureSpell), again.Version())
	}
}

func TestFeatureRequirementsAndLock(t *testing.T) {
	f, _ := featureManager(t, " GEO ,unknown")
	if err := f.Set(FeatureChatExternalMCP, true, "root"); err != nil {
		t.Fatal(err)
	}
	if !f.Enabled(FeatureChatExternalMCP) {
		t.Fatal("chatExternalMcp switched on with chat on")
	}
	if err := f.Set(FeatureChat, false, "root"); err != nil {
		t.Fatal(err)
	}
	ext, _ := f.State(FeatureChatExternalMCP)
	if ext.Enabled || !ext.Configured || len(ext.BlockedBy) != 1 || ext.BlockedBy[0] != FeatureChat {
		t.Errorf("chat off stops its dependant and keeps its switch: %+v", ext)
	}
	_ = f.Set(FeatureChat, true, "root")
	if !f.Enabled(FeatureChatExternalMCP) {
		t.Error("chat back on restores the dependant")
	}
	geo, _ := f.State(FeatureGeo)
	if geo.Enabled || geo.LockedBy != featuresForceOffEnv || !geo.Configured {
		t.Errorf("forced off: %+v", geo)
	}
	for _, on := range []bool{true, false} {
		if err := f.Set(FeatureGeo, on, "root"); !errors.Is(err, errFeatureLocked) {
			t.Errorf("a locked module cannot be switched (%v): %v", on, err)
		}
	}
}

func TestFeatureStoreFailuresAndBinlog(t *testing.T) {
	f, db := featureManager(t, "")
	bl, err := binlog.NewBinlog(filepath.Join(t.TempDir(), "x.db"), binlog.BinlogConfig{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = bl.Close() })
	f.SetBinlog(bl)
	f.FollowDB(func() *bolt.DB { return db })
	if err := f.Set(FeatureEval, false, ""); err != nil {
		t.Fatal(err)
	}
	if bl.CurrentLSN() < 2 {
		t.Errorf("the switch and the version are replicated: LSN %d", bl.CurrentLSN())
	}
	// The bucket gone (a restore of an older backup): the write fails.
	if err := db.Update(func(tx *bolt.Tx) error { return tx.DeleteBucket(bucketFeatures) }); err != nil {
		t.Fatal(err)
	}
	if err := f.Set(FeatureEval, true, ""); !errors.Is(err, errFeatureUnavailable) {
		t.Errorf("no bucket: %v", err)
	}
	_ = db.Update(func(tx *bolt.Tx) error { _, err := tx.CreateBucket(bucketFeatures); return err })
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.LoadAll(); err == nil {
		t.Error("load on a closed database")
	}
	if err := f.Set(FeatureEval, true, ""); err == nil {
		t.Error("set on a closed database")
	}
}
