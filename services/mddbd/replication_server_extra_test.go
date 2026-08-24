package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	bolt "go.etcd.io/bbolt"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"mddb/internal/binlog"
	"mddb/internal/cache"
	proto "mddb/proto"
)

// --- replication stream mocks (SEC-001): supply an authenticated Context() so
// the authorizeReplication gate passes and the handlers reach their precondition
// checks. Send is never called on the early-error paths these tests exercise.

type replSnapshotStream struct {
	proto.MDDBReplication_RequestSnapshotServer
	ctx context.Context
}

func (s *replSnapshotStream) Context() context.Context { return s.ctx }

type replBinlogStream struct {
	proto.MDDBReplication_StreamBinlogServer
	ctx context.Context
}

func (s *replBinlogStream) Context() context.Context { return s.ctx }

// authedReplCtx returns an incoming context carrying the replication secret.
func authedReplCtx(t *testing.T, secret string) context.Context {
	t.Helper()
	t.Setenv("MDDB_REPLICATION_SECRET", secret)
	md := metadata.New(map[string]string{"x-mddb-replication-secret": secret})
	return metadata.NewIncomingContext(context.Background(), md)
}

// replTestServer creates a Server with a Binlog for replication server tests.
func replTestServer(t *testing.T) (*Server, *binlog.Binlog, func()) {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "repl_srv.db")
	db, err := bolt.Open(dbPath, 0600, nil)
	if err != nil {
		t.Fatal(err)
	}

	s := &Server{
		DB:     db,
		Path:   dbPath,
		Mode:   ModeRW,
		NodeID: "leader-1",
		BucketNames: BucketNames{
			Docs:    []byte("docs"),
			IdxMeta: []byte("idxmeta"),
			Rev:     []byte("rev"),
			ByKey:   []byte("bykey"),
		},
		Cache:           cache.NewDocumentCache(100, 60),
		ReplicationRole: "leader",
	}

	if err := s.ensureBuckets(); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}

	bl, err := binlog.NewBinlog(dbPath, binlog.BinlogConfig{
		Path:    filepath.Join(dir, "test.binlog"),
		MaxSize: 10 * 1024 * 1024,
		MaxAge:  time.Hour,
	})
	if err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	s.Binlog = bl

	// s.DB, not db: a restore replaces the handle, so closing the captured one
	// leaves the live database open and Windows refuses to remove the temp
	// directory around it.
	cleanup := func() {
		_ = bl.Close()
		if s.DB != nil {
			_ = s.DB.Close()
		}
	}
	return s, bl, cleanup
}

// ---------------------------------------------------------------------------
// Test: NewReplicationServer
// ---------------------------------------------------------------------------

func TestNewReplicationServer(t *testing.T) {
	s, _, cleanup := replTestServer(t)
	defer cleanup()

	rs := NewReplicationServer(s)
	if rs == nil {
		t.Fatal("expected non-nil ReplicationServer")
		return
	}
	if rs.server != s {
		t.Error("expected server to match")
	}
	if rs.followers == nil {
		t.Error("expected non-nil followers map")
	}
}

// ---------------------------------------------------------------------------
// Test: ReplicationStatus
// ---------------------------------------------------------------------------

func TestReplicationStatus_Basic(t *testing.T) {
	s, bl, cleanup := replTestServer(t)
	defer cleanup()

	rs := NewReplicationServer(s)

	// Append some entries to the binlog
	_ = bl.Append(&binlog.BinlogEntry{Type: binlog.BinlogPut, BucketName: "docs", Key: []byte("k1"), Value: []byte("v1")})
	_ = bl.Append(&binlog.BinlogEntry{Type: binlog.BinlogPut, BucketName: "docs", Key: []byte("k2"), Value: []byte("v2")})

	resp, err := rs.ReplicationStatus(context.Background(), &proto.ReplicationStatusRequest{})
	if err != nil {
		t.Fatalf("ReplicationStatus: %v", err)
	}
	if resp.NodeId != "leader-1" {
		t.Errorf("expected NodeId=leader-1, got %s", resp.NodeId)
	}
	if resp.Role != "leader" {
		t.Errorf("expected role=leader, got %s", resp.Role)
	}
	if resp.CurrentLsn < 2 {
		t.Errorf("expected CurrentLsn >= 2, got %d", resp.CurrentLsn)
	}
}

func TestReplicationStatus_WithFollowers(t *testing.T) {
	s, _, cleanup := replTestServer(t)
	defer cleanup()

	rs := NewReplicationServer(s)

	// Manually register a follower
	rs.mu.Lock()
	rs.followers["follower-1"] = &FollowerState{
		ID:           "follower-1",
		ConfirmedLSN: 5,
		LastSeenAt:   time.Now().Unix(),
		Address:      "192.168.1.100:50051",
	}
	rs.mu.Unlock()

	resp, err := rs.ReplicationStatus(context.Background(), &proto.ReplicationStatusRequest{})
	if err != nil {
		t.Fatalf("ReplicationStatus: %v", err)
	}
	if len(resp.Followers) != 1 {
		t.Errorf("expected 1 follower, got %d", len(resp.Followers))
	}
	if resp.Followers[0].FollowerId != "follower-1" {
		t.Errorf("expected follower-1, got %s", resp.Followers[0].FollowerId)
	}
	if resp.Followers[0].ConfirmedLsn != 5 {
		t.Errorf("expected ConfirmedLSN=5, got %d", resp.Followers[0].ConfirmedLsn)
	}
	if resp.Followers[0].Address != "192.168.1.100:50051" {
		t.Errorf("expected address 192.168.1.100:50051, got %s", resp.Followers[0].Address)
	}
}

func TestReplicationStatus_NoBinlog(t *testing.T) {
	s, bl, cleanup := replTestServer(t)
	defer cleanup()

	rs := NewReplicationServer(s)
	// Remove binlog
	_ = bl.Close()
	s.Binlog = nil

	resp, err := rs.ReplicationStatus(context.Background(), &proto.ReplicationStatusRequest{})
	if err != nil {
		t.Fatalf("ReplicationStatus: %v", err)
	}
	if resp.CurrentLsn != 0 {
		t.Errorf("expected CurrentLSN=0 when no binlog, got %d", resp.CurrentLsn)
	}
}

// ---------------------------------------------------------------------------
// Test: AcknowledgeLSN
// ---------------------------------------------------------------------------

func TestAcknowledgeLSN_KnownFollower(t *testing.T) {
	s, _, cleanup := replTestServer(t)
	defer cleanup()

	rs := NewReplicationServer(s)

	// Register a follower
	rs.mu.Lock()
	rs.followers["follower-1"] = &FollowerState{
		ID:           "follower-1",
		ConfirmedLSN: 0,
		LastSeenAt:   time.Now().Unix() - 10,
	}
	rs.mu.Unlock()

	resp, err := rs.AcknowledgeLSN(context.Background(), &proto.AcknowledgeLSNRequest{
		FollowerId:   "follower-1",
		ConfirmedLsn: 42,
	})
	if err != nil {
		t.Fatalf("AcknowledgeLSN: %v", err)
	}
	if !resp.Ok {
		t.Error("expected Ok=true")
	}

	// Verify follower state was updated
	rs.mu.RLock()
	fs := rs.followers["follower-1"]
	rs.mu.RUnlock()
	if fs.ConfirmedLSN != 42 {
		t.Errorf("expected ConfirmedLSN=42, got %d", fs.ConfirmedLSN)
	}
}

func TestAcknowledgeLSN_UnknownFollower(t *testing.T) {
	s, _, cleanup := replTestServer(t)
	defer cleanup()

	rs := NewReplicationServer(s)

	// Acknowledge from unknown follower - should still succeed (no error)
	resp, err := rs.AcknowledgeLSN(context.Background(), &proto.AcknowledgeLSNRequest{
		FollowerId:   "unknown",
		ConfirmedLsn: 10,
	})
	if err != nil {
		t.Fatalf("AcknowledgeLSN: %v", err)
	}
	if !resp.Ok {
		t.Error("expected Ok=true even for unknown follower")
	}
}

func TestAcknowledgeLSN_NoBinlog(t *testing.T) {
	s, bl, cleanup := replTestServer(t)
	defer cleanup()

	rs := NewReplicationServer(s)
	_ = bl.Close()
	s.Binlog = nil

	resp, err := rs.AcknowledgeLSN(context.Background(), &proto.AcknowledgeLSNRequest{
		FollowerId:   "follower-1",
		ConfirmedLsn: 10,
	})
	if err != nil {
		t.Fatalf("AcknowledgeLSN: %v", err)
	}
	if resp.LeaderLsn != 0 {
		t.Errorf("expected LeaderLSN=0 when no binlog, got %d", resp.LeaderLsn)
	}
}

// ---------------------------------------------------------------------------
// Test: RequestSnapshot
// ---------------------------------------------------------------------------

func TestRequestSnapshot_NoBinlog(t *testing.T) {
	s, bl, cleanup := replTestServer(t)
	defer cleanup()

	rs := NewReplicationServer(s)
	_ = bl.Close()
	s.Binlog = nil

	err := rs.RequestSnapshot(&proto.SnapshotRequest{FollowerId: "f1"}, &replSnapshotStream{ctx: authedReplCtx(t, "s")})
	if err == nil {
		t.Fatal("expected error when binlog not enabled")
	}
}

func TestRequestSnapshot_MissingFollowerID(t *testing.T) {
	s, _, cleanup := replTestServer(t)
	defer cleanup()

	rs := NewReplicationServer(s)

	err := rs.RequestSnapshot(&proto.SnapshotRequest{FollowerId: ""}, &replSnapshotStream{ctx: authedReplCtx(t, "s")})
	if err == nil {
		t.Fatal("expected error for empty follower_id")
	}
}

// ---------------------------------------------------------------------------
// Test: StreamBinlog
// ---------------------------------------------------------------------------

func TestStreamBinlog_NoBinlog(t *testing.T) {
	s, bl, cleanup := replTestServer(t)
	defer cleanup()

	rs := NewReplicationServer(s)
	_ = bl.Close()
	s.Binlog = nil

	err := rs.StreamBinlog(&proto.StreamBinlogRequest{FollowerId: "f1"}, &replBinlogStream{ctx: authedReplCtx(t, "s")})
	if err == nil {
		t.Fatal("expected error when binlog not enabled")
	}
}

func TestStreamBinlog_MissingFollowerID(t *testing.T) {
	s, _, cleanup := replTestServer(t)
	defer cleanup()

	rs := NewReplicationServer(s)

	err := rs.StreamBinlog(&proto.StreamBinlogRequest{FollowerId: ""}, &replBinlogStream{ctx: authedReplCtx(t, "s")})
	if err == nil {
		t.Fatal("expected error for empty follower_id")
	}
}

// ---------------------------------------------------------------------------
// Test: authorizeReplication (SEC-001)
// ---------------------------------------------------------------------------

// TestAuthorizeReplication_NoConfigDenies ensures an unconfigured leader refuses
// replication rather than streaming the whole BoltDB (auth_users, auth_apikeys)
// to anyone who can reach the gRPC port.
func TestAuthorizeReplication_NoConfigDenies(t *testing.T) {
	s, _, cleanup := replTestServer(t)
	defer cleanup()
	rs := NewReplicationServer(s)

	t.Setenv("MDDB_REPLICATION_SECRET", "")
	err := rs.authorizeReplication(context.Background())
	if err == nil {
		t.Fatal("expected PermissionDenied when nothing is configured")
	}
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("expected PermissionDenied, got %v", status.Code(err))
	}
}

// TestAuthorizeReplication_SecretMismatchDenies rejects a follower whose secret
// does not match the leader's.
func TestAuthorizeReplication_SecretMismatchDenies(t *testing.T) {
	s, _, cleanup := replTestServer(t)
	defer cleanup()
	rs := NewReplicationServer(s)

	t.Setenv("MDDB_REPLICATION_SECRET", "leader-secret")
	md := metadata.New(map[string]string{"x-mddb-replication-secret": "wrong"})
	ctx := metadata.NewIncomingContext(context.Background(), md)

	err := rs.authorizeReplication(ctx)
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("expected PermissionDenied for wrong secret, got %v", status.Code(err))
	}
}

// TestAuthorizeReplication_SecretMatchAllows accepts a follower presenting the
// correct replication secret.
func TestAuthorizeReplication_SecretMatchAllows(t *testing.T) {
	s, _, cleanup := replTestServer(t)
	defer cleanup()
	rs := NewReplicationServer(s)

	ctx := authedReplCtx(t, "matching-secret")
	if err := rs.authorizeReplication(ctx); err != nil {
		t.Fatalf("expected nil error for matching secret, got %v", err)
	}
}

// TestAuthorizeReplication_MissingMetadataDenies rejects a follower that presents
// no metadata at all while a secret is configured.
func TestAuthorizeReplication_MissingMetadataDenies(t *testing.T) {
	s, _, cleanup := replTestServer(t)
	defer cleanup()
	rs := NewReplicationServer(s)

	t.Setenv("MDDB_REPLICATION_SECRET", "leader-secret")
	err := rs.authorizeReplication(context.Background())
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("expected PermissionDenied for missing metadata, got %v", status.Code(err))
	}
}

// TestRequestSnapshot_Unauthenticated verifies the handler refuses an anonymous
// caller before touching the binlog.
func TestRequestSnapshot_Unauthenticated(t *testing.T) {
	s, _, cleanup := replTestServer(t)
	defer cleanup()
	rs := NewReplicationServer(s)

	t.Setenv("MDDB_REPLICATION_SECRET", "leader-secret")
	err := rs.RequestSnapshot(&proto.SnapshotRequest{FollowerId: "f1"},
		&replSnapshotStream{ctx: context.Background()})
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("expected PermissionDenied for anonymous snapshot, got %v", status.Code(err))
	}
}

// TestStreamBinlog_Unauthenticated verifies the handler refuses an anonymous
// caller before touching the binlog.
func TestStreamBinlog_Unauthenticated(t *testing.T) {
	s, _, cleanup := replTestServer(t)
	defer cleanup()
	rs := NewReplicationServer(s)

	t.Setenv("MDDB_REPLICATION_SECRET", "leader-secret")
	err := rs.StreamBinlog(&proto.StreamBinlogRequest{FollowerId: "f1"},
		&replBinlogStream{ctx: context.Background()})
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("expected PermissionDenied for anonymous binlog stream, got %v", status.Code(err))
	}
}

// ---------------------------------------------------------------------------
// Test: entryToProto / protoToEntry roundtrip
// ---------------------------------------------------------------------------

func TestEntryToProtoRoundtrip_AllTypes(t *testing.T) {
	types := []binlog.BinlogEntryType{binlog.BinlogPut, binlog.BinlogDelete, binlog.BinlogDeleteBucket, binlog.BinlogCheckpoint}

	for _, tp := range types {
		entry := &binlog.BinlogEntry{
			LSN:        100,
			Type:       tp,
			Timestamp:  time.Now().Unix(),
			BucketName: "testbucket",
			Key:        []byte("some|key"),
			Value:      []byte("some|value"),
			Checksum:   12345,
		}

		p := entryToProto(entry)
		back := protoToEntry(p)

		if back.LSN != entry.LSN {
			t.Errorf("type %d: LSN mismatch", tp)
		}
		if back.Type != entry.Type {
			t.Errorf("type %d: Type mismatch", tp)
		}
		if back.BucketName != entry.BucketName {
			t.Errorf("type %d: BucketName mismatch", tp)
		}
		if string(back.Key) != string(entry.Key) {
			t.Errorf("type %d: Key mismatch", tp)
		}
		if string(back.Value) != string(entry.Value) {
			t.Errorf("type %d: Value mismatch", tp)
		}
	}
}

// ---------------------------------------------------------------------------
// Test: ReplicationStatus with follower lag estimation
// ---------------------------------------------------------------------------

func TestReplicationStatus_FollowerLag(t *testing.T) {
	s, bl, cleanup := replTestServer(t)
	defer cleanup()

	rs := NewReplicationServer(s)

	// Append several entries
	for i := 0; i < 10; i++ {
		_ = bl.Append(&binlog.BinlogEntry{Type: binlog.BinlogPut, BucketName: "docs", Key: []byte("k"), Value: []byte("v")})
	}

	currentLSN := bl.CurrentLSN()

	// Register a follower that's behind
	rs.mu.Lock()
	rs.followers["lag-follower"] = &FollowerState{
		ID:           "lag-follower",
		ConfirmedLSN: 3,
		LastSeenAt:   time.Now().Unix(),
		Address:      "10.0.0.1:50051",
	}
	rs.mu.Unlock()

	resp, err := rs.ReplicationStatus(context.Background(), &proto.ReplicationStatusRequest{})
	if err != nil {
		t.Fatalf("ReplicationStatus: %v", err)
	}

	if len(resp.Followers) != 1 {
		t.Fatalf("expected 1 follower, got %d", len(resp.Followers))
	}

	follower := resp.Followers[0]
	if follower.LagMs <= 0 {
		t.Errorf("expected positive lag when follower is behind (currentLSN=%d, confirmed=3), got %d", currentLSN, follower.LagMs)
	}
}

// ---------------------------------------------------------------------------
// Test: Verify binlog file exists
// ---------------------------------------------------------------------------

func TestReplServer_BinlogFileCreated(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "check.db")
	db, err := bolt.Open(dbPath, 0600, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	binlogPath := filepath.Join(dir, "check.binlog")
	bl, err := binlog.NewBinlog(dbPath, binlog.BinlogConfig{Path: binlogPath})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = bl.Close() }()

	if _, err := os.Stat(binlogPath); os.IsNotExist(err) {
		t.Error("binlog file should exist after creation")
	}
}
