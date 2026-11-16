package main

import (
	"bytes"
	"errors"
	"fmt"
	"path/filepath"
	"runtime"
	"sync"
	"testing"

	"mddb/internal/binlog"
	"mddb/internal/fts"
	"mddb/internal/geo"
	json "mddb/internal/jsonx"

	bolt "go.etcd.io/bbolt"
)

// GO-051 follow-up: the vector store and the tenant store wrote their binlog
// entries after the commit, outside any lock, as every write did before
// GO-051. The tests race writers on one key of each, in many short rounds —
// only the last writes of a round can leave the log and the database apart —
// and check after each, as TestBinlogOrderMatchesCommitOrder does for
// documents, that the last value in the log is the stored one; and at the
// end that a follower replaying the log ends with it too. Against the old
// appends the vector test failed about one run in five, the round loop
// makes that nearly every run.

const orderRounds, orderWriters, orderWrites = 40, 16, 4

// orderServer is a server whose log syncs on every append — writers that
// have committed queue on its lock, so nothing but the commit order says
// who goes first — and whose files do not sync, so commits are short.
func orderServer(t *testing.T) (*Server, *binlog.Binlog) {
	t.Helper()
	s := newHandlerTestServerAt(t, filepath.Join(t.TempDir(), "order.db"))
	bl, err := binlog.NewBinlog(s.Path, binlog.BinlogConfig{RetentionInterval: -1, Sync: binlog.SyncCommit})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = bl.Close() })
	s.Binlog = bl
	s.VectorStore.SetBinlog(bl)
	noSync(t, s)
	// The gate runs under the log's lock as each write takes its LSNs. A
	// pause there lets later commits pile up behind the lock, the window in
	// which an append made after its commit could overtake another.
	bl.SetWriteGate(func() error {
		runtime.Gosched()
		return nil
	})
	return s, bl
}

// race runs orderRounds rounds of orderWriters goroutines, each calling
// write orderWrites times, and check after each round.
func race(t *testing.T, write func(n int) error, check func(round int)) {
	t.Helper()
	for round := range orderRounds {
		var wg sync.WaitGroup
		for w := range orderWriters {
			wg.Go(func() {
				for i := range orderWrites {
					if err := write(((round*orderWriters)+w)*orderWrites + i + 1); err != nil {
						t.Error(err)
						return
					}
				}
			})
		}
		wg.Wait()
		check(round)
	}
}

// lastLogged is the value of the last Put of key in bucket, nil if none.
func lastLogged(t *testing.T, bl *binlog.Binlog, bucket string, key []byte) []byte {
	t.Helper()
	var last []byte
	if err := bl.ScanFrom(0, func(e *binlog.BinlogEntry) error {
		if e.Type == binlog.BinlogPut && e.BucketName == bucket && bytes.Equal(e.Key, key) {
			last = e.Value
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return last
}

// storedValue is key's value in bucket of the file that holds collection.
func storedValue(t *testing.T, s *Server, collection, bucket string, key []byte) []byte {
	t.Helper()
	var out []byte
	view := func(tx *bolt.Tx) error {
		if b := tx.Bucket([]byte(bucket)); b != nil {
			out = bytes.Clone(b.Get(key))
		}
		return nil
	}
	var err error
	if collection == "" {
		err = s.DBView(view)
	} else {
		err = s.DBViewC(collection, view)
	}
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// replayed applies the whole log to a fresh follower and returns it.
func replayed(t *testing.T, bl *binlog.Binlog) *Server {
	t.Helper()
	f := newHandlerTestServerAt(t, filepath.Join(t.TempDir(), "follower.db"))
	f.Tenants = NewTenantManager(f.DB)
	if err := f.Tenants.LoadAll(); err != nil {
		t.Fatal(err)
	}
	entries, err := bl.ReadFrom(0)
	if err != nil {
		t.Fatal(err)
	}
	if err := NewReplicationApplier(f).ApplyBatch(entries); err != nil {
		t.Fatal(err)
	}
	return f
}

func TestBinlogOrderMatchesCommitOrderForVectors(t *testing.T) {
	s, bl := orderServer(t)
	key := []byte("vec|race|same")
	var stored []byte
	race(t, func(n int) error {
		return s.VectorStore.Put("race", "same", []float32{float32(n), 1}, "m", fmt.Sprint(n))
	}, func(round int) {
		stored = storedValue(t, s, "race", "vectors", key)
		if logged := lastLogged(t, bl, "vectors", key); !bytes.Equal(stored, logged) {
			t.Fatalf("round %d: the log's last version of the vector is not the stored one:\nstored %x\nlogged %x", round, stored, logged)
		}
	})
	f := replayed(t, bl)
	if got := storedValue(t, f, "race", "vectors", key); !bytes.Equal(stored, got) {
		t.Fatalf("a follower replaying the log ends with another vector:\nleader   %x\nfollower %x", stored, got)
	}
}

func TestBinlogOrderMatchesCommitOrderForTenantConfigs(t *testing.T) {
	s, bl := orderServer(t)
	tm := NewTenantManager(s.DB)
	if err := tm.LoadAll(); err != nil {
		t.Fatal(err)
	}
	tm.SetBinlog(bl)
	var stored []byte
	race(t, func(n int) error {
		return tm.Set("t1", TenantConfig{Quota: &TenantQuota{MaxDocuments: int64(n)}})
	}, func(round int) {
		stored = storedValue(t, s, "", "tenants", []byte("t1"))
		if logged := lastLogged(t, bl, "tenants", []byte("t1")); !bytes.Equal(stored, logged) {
			t.Fatalf("round %d: the log's last tenant configuration is not the stored one:\nstored %s\nlogged %s", round, stored, logged)
		}
		// The cache ends where the database does: the write lock spans both.
		if cfg, _ := tm.Get("t1"); fmt.Sprint(cfg.Quota.MaxDocuments) != quotaDocs(t, stored) {
			t.Fatalf("round %d: cached MaxDocuments %d, stored %s", round, cfg.Quota.MaxDocuments, stored)
		}
	})
	f := replayed(t, bl)
	if got := storedValue(t, f, "", "tenants", []byte("t1")); !bytes.Equal(stored, got) {
		t.Fatalf("a follower replaying the log ends with another configuration:\nleader   %s\nfollower %s", stored, got)
	}
	// And it answers with it at once (replication_reload.go).
	if cfg, ok := f.Tenants.Get("t1"); !ok || fmt.Sprint(cfg.Quota.MaxDocuments) != quotaDocs(t, stored) {
		t.Fatalf("the follower's tenant cache holds %+v", cfg)
	}
}

// quotaDocs is MaxDocuments of a stored configuration, as text.
func quotaDocs(t *testing.T, stored []byte) string {
	t.Helper()
	var cfg TenantConfig
	if err := json.Unmarshal(stored, &cfg); err != nil || cfg.Quota == nil {
		t.Fatalf("stored configuration %s: %v", stored, err)
	}
	return fmt.Sprint(cfg.Quota.MaxDocuments)
}

// On a node that does not lead, a write the log refuses commits nothing:
// the managers that appended after their commit used to keep the change and
// lose the entry.
func TestManagerWritesCommitNothingTheLogRefuses(t *testing.T) {
	s, bl := orderServer(t)
	s.Tenants = NewTenantManager(s.DB)
	s.Aliases = NewAliasManager(s.DB)
	s.Features = NewFeatureManager(s.DB)
	s.ChatScenarios = NewChatScenarioManager(s.DB)
	for _, load := range []func() error{s.Tenants.LoadAll, s.Aliases.LoadAll, s.Features.LoadAll, s.ChatScenarios.LoadAll} {
		if err := load(); err != nil {
			t.Fatal(err)
		}
	}
	s.Tenants.SetBinlog(bl)
	s.Aliases.SetBinlog(bl)
	s.Features.SetBinlog(bl)
	s.ChatScenarios.SetBinlog(bl)
	if err := s.Tenants.Set("gone", TenantConfig{Limits: &TenantLimits{ReadPerSecond: 1}}); err != nil {
		t.Fatal(err)
	}
	if err := s.Aliases.set("old", "c", func(string) bool { return false }); err != nil {
		t.Fatal(err)
	}
	before := bl.CurrentLSN()

	gate := errors.New("not the leader")
	bl.SetWriteGate(func() error { return gate })
	writes := map[string]func() error{
		"tenant set":    func() error { return s.Tenants.Set("t1", TenantConfig{Limits: &TenantLimits{ReadPerSecond: 1}}) },
		"tenant delete": func() error { return s.Tenants.Delete("gone") },
		"alias set":     func() error { return s.Aliases.set("a", "c", func(string) bool { return false }) },
		"alias remove":  func() error { return s.Aliases.remove("old") },
		"feature":       func() error { return s.Features.Set(knownFeature(t), false, "test") },
		"chat scenario": func() error { _, err := s.ChatScenarios.Put(&ChatScenario{Name: "s"}); return err },
		"vector":        func() error { return s.VectorStore.Put("c", "d", []float32{1, 2}, "m", "h") },
	}
	for name, write := range writes {
		if err := write(); !errors.Is(err, gate) {
			t.Errorf("%s on a node that does not lead: %v", name, err)
		}
	}
	bl.SetWriteGate(nil)

	if bl.CurrentLSN() != before {
		t.Fatalf("the log moved from %d to %d", before, bl.CurrentLSN())
	}
	if bl.Dirty() {
		t.Fatal("a refused write left the log behind the database")
	}
	if _, ok := s.Tenants.Get("t1"); ok {
		t.Error("the refused tenant configuration is cached")
	}
	if _, ok := s.Tenants.Get("gone"); !ok {
		t.Error("the refused tenant delete took effect")
	}
	if storedValue(t, s, "", "tenants", []byte("t1")) != nil || storedValue(t, s, "", "aliases", []byte("a")) != nil {
		t.Error("a refused write was committed")
	}
	if storedValue(t, s, "", "aliases", []byte("old")) == nil {
		t.Error("the refused alias removal was committed")
	}
	if storedValue(t, s, "c", "vectors", []byte("vec|c|d")) != nil {
		t.Error("the refused vector was committed")
	}
}

// knownFeature is the name of some module the feature manager knows.
func knownFeature(t *testing.T) string {
	t.Helper()
	if len(featureRegistry) == 0 {
		t.Fatal("no features")
	}
	return featureRegistry[0].Name
}

// A follower reloads every manager whose bucket a batch touched, once, and
// keeps going when a reload fails.
func TestFollowerRefreshReloadsReplicatedManagers(t *testing.T) {
	s, bl := orderServer(t)
	s.CurationManager = NewCurationManager(s.DB)
	s.SynonymManager = fts.NewSynonymManager(s.DB)
	s.StopWordManager = fts.NewStopWordManager(s.DB)
	s.Tenants = NewTenantManager(s.DB)
	follower := newHandlerTestServerAt(t, filepath.Join(t.TempDir(), "follower.db"))
	follower.CurationManager = NewCurationManager(follower.DB)
	follower.SynonymManager = fts.NewSynonymManager(follower.DB)
	follower.StopWordManager = fts.NewStopWordManager(follower.DB)
	follower.Tenants = NewTenantManager(follower.DB)
	for _, n := range []*Server{s, follower} {
		allOK(t, n.CurationManager.EnsureBucket(), n.SynonymManager.EnsureBucket(), n.StopWordManager.EnsureBucket(), n.Tenants.LoadAll())
	}
	s.CurationManager.SetBinlog(bl)
	s.SynonymManager.SetBinlog(bl)
	s.StopWordManager.SetBinlog(bl)
	s.Tenants.SetBinlog(bl)

	rule := &CurationRule{Collection: "c", Query: "q", Hides: []string{"d1"}}
	allOK(t, s.CurationManager.Set(rule),
		s.SynonymManager.Set("c", "car", []string{"auto"}),
		s.StopWordManager.Add("c", []string{"zzz"}),
		s.Tenants.Set("t1", TenantConfig{Limits: &TenantLimits{ReadPerSecond: 3}}))
	entries, err := bl.ReadFrom(0)
	if err != nil {
		t.Fatal(err)
	}
	if err := NewReplicationApplier(follower).ApplyBatch(entries); err != nil {
		t.Fatal(err)
	}
	if _, ok := follower.CurationManager.Get(rule.ID); !ok {
		t.Error("the follower does not see the curation rule")
	}
	if got := follower.SynonymManager.Get("c", "car"); len(got) != 1 || got[0] != "auto" {
		t.Errorf("the follower's synonyms of car: %v", got)
	}
	if !follower.StopWordManager.IsStopWord("c", "zzz") {
		t.Error("the follower does not see the stop word")
	}
	if got := follower.effectiveLimits("t1").ReadPerSecond; got != 3 {
		t.Errorf("the follower's read limit for t1 is %d", got)
	}

	// A manager that cannot reload is reported and the rest go on.
	follower.Tenants.db, follower.Tenants.follow = closedDB(t), nil
	NewReplicationApplier(follower).updateInMemoryState(&binlog.BinlogEntry{Type: binlog.BinlogPut, BucketName: "tenants"})
	// A server without the managers reloads nothing.
	bare := &Server{}
	for name, reload := range bare.replicatedManagerReloads() {
		if err := reload(); err != nil {
			t.Errorf("%s on a server without it: %v", name, err)
		}
	}
}

func allOK(t *testing.T, errs ...error) {
	t.Helper()
	for _, err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
}

// closedDB is a database handle that fails every transaction.
func closedDB(t *testing.T) *bolt.DB {
	t.Helper()
	db, err := bolt.Open(filepath.Join(t.TempDir(), "closed.db"), 0o600, nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	return db
}

// A follower's geo indexes follow the replicated geo bucket without a
// restart: a point put on the leader is found, a point deleted is gone.
func TestFollowerGeoIndexesFollowTheLog(t *testing.T) {
	s, bl := orderServer(t)
	s.GeoStore = geo.NewGeoStore(s.DB)
	allOK(t, s.GeoStore.EnsureBucket())
	s.GeoStore.SetBinlog(bl)
	allOK(t, s.GeoStore.Put("c", "d1", 52.23, 21.01), s.GeoStore.Put("c", "d2", 50.06, 19.94), s.GeoStore.Delete("c", "d2"))

	follower := newHandlerTestServerAt(t, filepath.Join(t.TempDir(), "follower.db"))
	follower.GeoIndex, follower.GeoHashIndex = geo.NewGeoIndex(), geo.NewGeoHashIndex()
	entries, err := bl.ReadFrom(0)
	if err != nil {
		t.Fatal(err)
	}
	if err := NewReplicationApplier(follower).ApplyBatch(entries); err != nil {
		t.Fatal(err)
	}
	if follower.GeoIndex.Len("c") != 1 || follower.GeoHashIndex.Len("c") != 1 {
		t.Fatalf("the follower's geo indexes hold %d and %d points, want 1", follower.GeoIndex.Len("c"), follower.GeoHashIndex.Len("c"))
	}
	// A follower without geo indexes applies the bucket and nothing else.
	bare := newHandlerTestServerAt(t, filepath.Join(t.TempDir(), "bare.db"))
	if err := NewReplicationApplier(bare).ApplyBatch(entries); err != nil {
		t.Fatal(err)
	}
}
