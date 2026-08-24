package main

import (
	"mddb/internal/cache"
	"mddb/internal/schema"
	"mddb/internal/vector"
	"mddb/internal/webhooks"
	"os"
	"testing"
	"time"

	bolt "go.etcd.io/bbolt"
)

// newTestServerForReplClient creates a minimal Server for replication client tests.
func newTestServerForReplClient(t *testing.T) (*Server, func()) {
	t.Helper()
	f, err := os.CreateTemp("", "replclient_test_*.db")
	if err != nil {
		t.Fatal(err)
	}
	_ = f.Close()

	db, err := bolt.Open(f.Name(), 0600, nil)
	if err != nil {
		_ = os.Remove(f.Name())
		t.Fatal(err)
	}

	s := &Server{
		DB:   db,
		Path: f.Name(),
		Mode: ModeRW,
		BucketNames: BucketNames{
			Docs:    []byte("docs"),
			IdxMeta: []byte("idxmeta"),
			Rev:     []byte("rev"),
			ByKey:   []byte("bykey"),
		},
		Cache: cache.NewDocumentCache(100, 60),
	}

	if err := s.ensureBuckets(); err != nil {
		_ = db.Close()
		_ = os.Remove(f.Name())
		t.Fatal(err)
	}

	cleanup := func() {
		_ = db.Close()
		_ = os.Remove(f.Name())
	}
	return s, cleanup
}

func TestReplicationClientConfigDefaults(t *testing.T) {
	s, cleanup := newTestServerForReplClient(t)
	defer cleanup()

	rc := NewReplicationClient(s, ReplicationClientConfig{
		LeaderAddr: "localhost:9090",
		FollowerID: "follower-1",
	})

	if rc.retryInterval != 5*time.Second {
		t.Errorf("expected retryInterval 5s, got %v", rc.retryInterval)
	}
	if rc.ackInterval != 10*time.Second {
		t.Errorf("expected ackInterval 10s, got %v", rc.ackInterval)
	}
	if rc.maxLag != 30*time.Second {
		t.Errorf("expected maxLag 30s, got %v", rc.maxLag)
	}
	if rc.leaderAddr != "localhost:9090" {
		t.Errorf("expected leaderAddr localhost:9090, got %s", rc.leaderAddr)
	}
	if rc.followerID != "follower-1" {
		t.Errorf("expected followerID follower-1, got %s", rc.followerID)
	}
	if rc.applier == nil {
		t.Error("expected applier to be initialized")
	}
	if rc.stopCh == nil {
		t.Error("expected stopCh to be initialized")
	}
}

func TestReplicationClientConfigCustomValues(t *testing.T) {
	s, cleanup := newTestServerForReplClient(t)
	defer cleanup()

	rc := NewReplicationClient(s, ReplicationClientConfig{
		LeaderAddr:    "leader:5050",
		FollowerID:    "node-42",
		RetryInterval: 2 * time.Second,
		AckInterval:   5 * time.Second,
		MaxLag:        15 * time.Second,
	})

	if rc.retryInterval != 2*time.Second {
		t.Errorf("expected retryInterval 2s, got %v", rc.retryInterval)
	}
	if rc.ackInterval != 5*time.Second {
		t.Errorf("expected ackInterval 5s, got %v", rc.ackInterval)
	}
	if rc.maxLag != 15*time.Second {
		t.Errorf("expected maxLag 15s, got %v", rc.maxLag)
	}
}

func TestReplicationClientLagMs(t *testing.T) {
	s, cleanup := newTestServerForReplClient(t)
	defer cleanup()

	rc := NewReplicationClient(s, ReplicationClientConfig{
		LeaderAddr: "localhost:9090",
		FollowerID: "follower-1",
	})

	// Initially zero
	if rc.LagMs() != 0 {
		t.Errorf("expected initial LagMs 0, got %d", rc.LagMs())
	}

	// Set a value
	rc.lagMs.Store(123)
	if rc.LagMs() != 123 {
		t.Errorf("expected LagMs 123, got %d", rc.LagMs())
	}
}

func TestReplicationClientLastAppliedAt(t *testing.T) {
	s, cleanup := newTestServerForReplClient(t)
	defer cleanup()

	rc := NewReplicationClient(s, ReplicationClientConfig{
		LeaderAddr: "localhost:9090",
		FollowerID: "follower-1",
	})

	// Initially zero
	if rc.LastAppliedAt() != 0 {
		t.Errorf("expected initial LastAppliedAt 0, got %d", rc.LastAppliedAt())
	}

	// Set a value
	now := time.Now().Unix()
	rc.lastAppliedAt.Store(now)
	if rc.LastAppliedAt() != now {
		t.Errorf("expected LastAppliedAt %d, got %d", now, rc.LastAppliedAt())
	}
}

func TestReplicationClientIsHealthy(t *testing.T) {
	s, cleanup := newTestServerForReplClient(t)
	defer cleanup()

	rc := NewReplicationClient(s, ReplicationClientConfig{
		LeaderAddr: "localhost:9090",
		FollowerID: "follower-1",
		MaxLag:     30 * time.Second,
	})

	// Never applied: unhealthy
	if rc.IsHealthy() {
		t.Error("expected unhealthy when nothing applied")
	}

	// Applied recently: healthy
	rc.lastAppliedAt.Store(time.Now().Unix())
	if !rc.IsHealthy() {
		t.Error("expected healthy after recent apply")
	}

	// Applied long ago: unhealthy
	rc.lastAppliedAt.Store(time.Now().Add(-1 * time.Minute).Unix())
	if rc.IsHealthy() {
		t.Error("expected unhealthy when last applied > maxLag ago")
	}
}

func TestReplicationClientStartStop(t *testing.T) {
	s, cleanup := newTestServerForReplClient(t)
	defer cleanup()

	rc := NewReplicationClient(s, ReplicationClientConfig{
		LeaderAddr:    "localhost:19999", // invalid, loop will fail to connect
		FollowerID:    "follower-1",
		RetryInterval: 100 * time.Millisecond,
	})

	// Start
	rc.Start()
	if !rc.running.Load() {
		t.Error("expected running after Start")
	}

	// Starting again should be a no-op
	rc.Start()

	// Stop
	rc.Stop()
	if rc.running.Load() {
		t.Error("expected not running after Stop")
	}

	// Stopping again should be a no-op (no panic)
	rc.Stop()
}

func TestReplicationClientDisconnect(t *testing.T) {
	s, cleanup := newTestServerForReplClient(t)
	defer cleanup()

	rc := NewReplicationClient(s, ReplicationClientConfig{
		LeaderAddr: "localhost:9090",
		FollowerID: "follower-1",
	})

	// disconnect with nil conn should not panic
	rc.disconnect()

	if rc.conn != nil {
		t.Error("expected conn to be nil")
	}
	if rc.client != nil {
		t.Error("expected client to be nil")
	}
}

func TestReplicationClientWaitRetry(t *testing.T) {
	s, cleanup := newTestServerForReplClient(t)
	defer cleanup()

	rc := NewReplicationClient(s, ReplicationClientConfig{
		LeaderAddr:    "localhost:9090",
		FollowerID:    "follower-1",
		RetryInterval: 50 * time.Millisecond,
	})

	// waitRetry should return after retryInterval
	start := time.Now()
	rc.waitRetry()
	elapsed := time.Since(start)

	if elapsed < 40*time.Millisecond {
		t.Errorf("waitRetry returned too quickly: %v", elapsed)
	}
}

func TestReplicationClientWaitRetryStopCh(t *testing.T) {
	s, cleanup := newTestServerForReplClient(t)
	defer cleanup()

	rc := NewReplicationClient(s, ReplicationClientConfig{
		LeaderAddr:    "localhost:9090",
		FollowerID:    "follower-1",
		RetryInterval: 10 * time.Second, // long interval
	})

	// Close stopCh to interrupt waitRetry immediately
	go func() {
		time.Sleep(50 * time.Millisecond)
		close(rc.stopCh)
	}()

	start := time.Now()
	rc.waitRetry()
	elapsed := time.Since(start)

	if elapsed > 2*time.Second {
		t.Errorf("waitRetry should have been interrupted by stopCh, took %v", elapsed)
	}
}

func TestReplicationClientReplaceDatabase(t *testing.T) {
	s, cleanup := newTestServerForReplClient(t)
	defer cleanup()

	rc := NewReplicationClient(s, ReplicationClientConfig{
		LeaderAddr: "localhost:9090",
		FollowerID: "follower-1",
	})

	// Create a snapshot file (a valid BoltDB)
	snapshotPath := s.Path + ".snapshot.tmp"
	snapDB, err := bolt.Open(snapshotPath, 0600, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Write something into snapshot DB to distinguish it
	err = snapDB.Update(func(tx *bolt.Tx) error {
		b, err := tx.CreateBucketIfNotExists([]byte("test_marker"))
		if err != nil {
			return err
		}
		return b.Put([]byte("key"), []byte("value"))
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = snapDB.Close()

	// Replace database
	err = rc.replaceDatabase(snapshotPath)
	if err != nil {
		t.Fatalf("replaceDatabase failed: %v", err)
	}

	// Verify the new database has the marker bucket
	var found bool
	_ = rc.server.DB.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte("test_marker"))
		if b != nil {
			v := b.Get([]byte("key"))
			found = string(v) == "value"
		}
		return nil
	})
	if !found {
		t.Error("expected marker bucket in replaced database")
	}
}

func TestReplicationClientRebuildInMemoryState(t *testing.T) {
	s, cleanup := newTestServerForReplClient(t)
	defer cleanup()

	// Set up subsystems
	s.VectorStore = vector.NewVectorStore(s.DB)
	s.VectorIndex = vector.NewVectorIndex()
	s.VectorSearchers = map[string]vector.VectorSearcher{
		"flat": s.VectorIndex,
	}
	s.WebhookManager = webhooks.NewWebhookManager(s.DB)
	_ = s.WebhookManager.EnsureBucket()
	s.SchemaManager = schema.NewSchemaManager(s.DB)
	_ = s.SchemaManager.EnsureBucket()
	s.Cache = cache.NewDocumentCache(100, 60)

	rc := NewReplicationClient(s, ReplicationClientConfig{
		LeaderAddr: "localhost:9090",
		FollowerID: "follower-1",
	})

	// Should not panic
	rc.server.rebuildInMemoryState()

	// Verify subsystems were rebuilt (non-nil)
	if s.VectorStore == nil {
		t.Error("expected VectorStore to be rebuilt")
	}
	if s.WebhookManager == nil {
		t.Error("expected WebhookManager to be rebuilt")
	}
	if s.SchemaManager == nil {
		t.Error("expected schema.SchemaManager to be rebuilt")
	}
	if s.Cache == nil {
		t.Error("expected Cache to be rebuilt")
	}
}

func TestReplicationClientRebuildInMemoryStateNilSubsystems(t *testing.T) {
	s, cleanup := newTestServerForReplClient(t)
	defer cleanup()

	// Leave all subsystems nil
	s.VectorStore = nil
	s.VectorIndex = nil
	s.WebhookManager = nil
	s.SchemaManager = nil
	s.Cache = nil

	rc := NewReplicationClient(s, ReplicationClientConfig{
		LeaderAddr: "localhost:9090",
		FollowerID: "follower-1",
	})

	// Should not panic with nil subsystems
	rc.server.rebuildInMemoryState()
}

func TestReplicationClientConnect(t *testing.T) {
	s, cleanup := newTestServerForReplClient(t)
	defer cleanup()

	rc := NewReplicationClient(s, ReplicationClientConfig{
		LeaderAddr: "localhost:19999",
		FollowerID: "follower-1",
	})

	// connect should succeed (gRPC.NewClient doesn't actually connect eagerly)
	err := rc.connect()
	if err != nil {
		t.Fatalf("connect failed: %v", err)
	}

	if rc.conn == nil {
		t.Error("expected conn to be set after connect")
	}
	if rc.client == nil {
		t.Error("expected client to be set after connect")
	}

	// Cleanup
	rc.disconnect()
}

func TestReplicationClientConfigStruct(t *testing.T) {
	cfg := ReplicationClientConfig{
		LeaderAddr:    "leader:8080",
		FollowerID:    "f1",
		RetryInterval: 1 * time.Second,
		AckInterval:   2 * time.Second,
		MaxLag:        5 * time.Second,
	}

	if cfg.LeaderAddr != "leader:8080" {
		t.Errorf("unexpected LeaderAddr: %s", cfg.LeaderAddr)
	}
	if cfg.FollowerID != "f1" {
		t.Errorf("unexpected FollowerID: %s", cfg.FollowerID)
	}
	if cfg.RetryInterval != 1*time.Second {
		t.Errorf("unexpected RetryInterval: %v", cfg.RetryInterval)
	}
	if cfg.AckInterval != 2*time.Second {
		t.Errorf("unexpected AckInterval: %v", cfg.AckInterval)
	}
	if cfg.MaxLag != 5*time.Second {
		t.Errorf("unexpected MaxLag: %v", cfg.MaxLag)
	}
}
