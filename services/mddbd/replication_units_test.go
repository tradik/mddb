package main

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mddb/internal/binlog"
	proto "mddb/proto"

	bolt "go.etcd.io/bbolt"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// The edges of GO-050 the end-to-end tests do not reach: refusals,
// failures, and leaders and followers from before 2.19.8.

var errUnit = errors.New("unit")

func TestReplicationIsAuthorisedByAdminClaims(t *testing.T) {
	t.Setenv("MDDB_REPLICATION_SECRET", "")
	rs := &ReplicationServer{server: &Server{AuthManager: &AuthManager{enabled: true}}}
	if err := rs.authorizeReplication(context.Background()); status.Code(err) != codes.PermissionDenied {
		t.Errorf("no claims: %v", err)
	}
	admin := context.WithValue(context.Background(), authContextKey, &JWTClaims{Username: "a", Admin: true})
	if err := rs.authorizeReplication(admin); err != nil {
		t.Errorf("admin claims: %v", err)
	}
}

// failingSnapshotStream refuses every chunk, or those of collection files.
type failingSnapshotStream struct {
	proto.MDDBReplication_RequestSnapshotServer
	ctx        context.Context
	onlyFiles  bool
	mainChunks int
}

func (s *failingSnapshotStream) Context() context.Context { return s.ctx }
func (s *failingSnapshotStream) Send(c *proto.SnapshotChunk) error {
	if s.onlyFiles && c.File == "" {
		s.mainChunks++
		return nil
	}
	return errUnit
}

func TestSnapshotRequestsThatFail(t *testing.T) {
	s, _, cleanup := replTestServer(t)
	defer cleanup()
	rs := NewReplicationServer(s)
	ctx := authedReplCtx(t, "s")

	other := layoutPerCollection
	if perCollectionLayout() {
		other = layoutSingle
	}
	err := rs.RequestSnapshot(&proto.SnapshotRequest{FollowerId: "f", Layout: other}, &failingSnapshotStream{ctx: ctx})
	if status.Code(err) != codes.FailedPrecondition || !strings.Contains(err.Error(), "MDDB_STORAGE_LAYOUT") {
		t.Errorf("layouts that differ: %v", err)
	}
	// A follower before 2.19.8 names no layout; the stream then breaks.
	err = rs.RequestSnapshot(&proto.SnapshotRequest{FollowerId: "f", Layout: storageLayoutName()}, &failingSnapshotStream{ctx: ctx})
	if status.Code(err) != codes.Internal {
		t.Errorf("a broken stream: %v", err)
	}
	if perCollectionLayout() {
		return
	}
	if err := rs.RequestSnapshot(&proto.SnapshotRequest{FollowerId: "f"}, &failingSnapshotStream{ctx: ctx}); status.Code(err) != codes.Internal {
		t.Errorf("an old follower: %v", err)
	}
}

func TestASnapshotThatBreaksInACollectionFile(t *testing.T) {
	s := perCollectionServer(t)
	addTestDoc(t, s, "blog", "a", "en", "x", nil)
	bl, err := binlog.NewBinlog(s.Path, binlog.BinlogConfig{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = bl.Close() }()
	s.Binlog = bl
	stream := &failingSnapshotStream{ctx: authedReplCtx(t, "s"), onlyFiles: true}
	err = NewReplicationServer(s).RequestSnapshot(&proto.SnapshotRequest{FollowerId: "f", Layout: layoutPerCollection}, stream)
	if status.Code(err) != codes.Internal || stream.mainChunks == 0 {
		t.Errorf("a collection file that cannot be sent: %v after %d main chunks", err, stream.mainChunks)
	}
}

// chunkStream hands out chunks, then err.
type chunkStream struct {
	chunks []*proto.SnapshotChunk
	err    error
}

func (s *chunkStream) Recv() (*proto.SnapshotChunk, error) {
	if len(s.chunks) == 0 {
		return nil, s.err
	}
	c := s.chunks[0]
	s.chunks = s.chunks[1:]
	return c, nil
}

func TestReceivingSnapshots(t *testing.T) {
	tmp := filepath.Join(t.TempDir(), "snap.tmp")
	data := []byte("db")
	cases := []struct {
		name   string
		stream *chunkStream
		want   string // "" is success
	}{
		{"complete", &chunkStream{chunks: []*proto.SnapshotChunk{{Data: data, LogId: 1, SnapshotLsn: 3}, {IsLast: true, LogId: 1, SnapshotLsn: 3}}}, ""},
		{"cut short", &chunkStream{chunks: []*proto.SnapshotChunk{{Data: data, LogId: 1}}, err: io.EOF}, "ended before"},
		{"broken", &chunkStream{err: errUnit}, "receive"},
		{"a path for a name", &chunkStream{chunks: []*proto.SnapshotChunk{{Data: data, File: "../x.db", LogId: 1}}}, "cannot take"},
	}
	for _, c := range cases {
		pos, err := receiveSnapshot(c.stream, tmp)
		if (c.want == "") != (err == nil) || (err != nil && !strings.Contains(err.Error(), c.want)) {
			t.Errorf("%s: %v", c.name, err)
		}
		if c.want == "" && pos.LSN != 3 {
			t.Errorf("%s: position %+v", c.name, pos)
		}
	}
	// A leader before 2.19.8: one file, no ID, no closing chunk.
	old := &chunkStream{chunks: []*proto.SnapshotChunk{{Data: data, SnapshotLsn: 9}}, err: io.EOF}
	_, err := receiveSnapshot(old, tmp)
	if perCollectionLayout() != errors.Is(err, errSnapshotFromOldLeader) {
		t.Errorf("a snapshot from an old leader: %v", err)
	}
	if _, err := receiveSnapshot(&chunkStream{chunks: []*proto.SnapshotChunk{{Data: data, LogId: 1}}}, filepath.Join(tmp, "missing", "x")); err == nil {
		t.Error("wrote a snapshot into a directory that does not exist")
	}
}

func TestPerCollectionSnapshotEdges(t *testing.T) {
	s := perCollectionServer(t)
	blocked := filepath.Join(t.TempDir(), "file")
	writeTestFile(t, blocked)
	if _, err := receiveSnapshot(&chunkStream{err: io.EOF}, filepath.Join(blocked, "snap")); err == nil {
		t.Error("the snapshot directory could be made under a file")
	}
	rc := &ReplicationClient{server: s, applier: NewReplicationApplier(s)}
	if err := rc.installSnapshot(filepath.Join(t.TempDir(), "absent.tmp")); err == nil {
		t.Error("installed a snapshot without its collection files")
	}
}

func TestInstallingSomethingThatIsNotADatabase(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	tmp := s.Path + ".snapshot.tmp"
	writeTestFile(t, tmp)
	_ = os.MkdirAll(collectionDir(tmp), 0o700)
	rc := &ReplicationClient{server: s, applier: NewReplicationApplier(s)}
	if err := rc.installSnapshot(tmp); err == nil {
		t.Error("installed a file that is not a database")
	}
	addTestDoc(t, s, "blog", "still", "en", "here", nil) // the old database serves on
}

func writeTestFile(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, []byte("not a database"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestPositionsInTheDatabase(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	if err := s.DB.View(func(tx *bolt.Tx) error { return stampApplied(tx, replPosition{LSN: 1}) }); err == nil {
		t.Error("stamped a position in a read transaction")
	}
	_ = s.DB.Update(func(tx *bolt.Tx) error {
		b, _ := tx.CreateBucketIfNotExists(bucketReplMeta)
		return b.Put(keyReplApplied, []byte("short"))
	})
	if pos, err := s.loadReplicationPosition(); err != nil || pos.LSN != 0 {
		t.Errorf("a damaged record: %+v, %v", pos, err)
	}
	// A former follower started as a leader forgets its position.
	if err := s.saveReplicationPosition(replPosition{LSN: 5, LogID: 2}); err != nil {
		t.Fatal(err)
	}
	if err := s.clearReplicationPosition(); err != nil {
		t.Fatal(err)
	}
	if pos, _ := s.loadReplicationPosition(); pos.LSN != 0 {
		t.Errorf("position after clearing: %+v", pos)
	}
	closeStorage(t, s)
	if _, err := s.loadReplicationPosition(); err == nil {
		t.Error("read a position from a closed database")
	}
	if err := s.clearReplicationPosition(); err == nil {
		t.Error("cleared a closed database")
	}
	rc := &ReplicationClient{server: s, applier: NewReplicationApplier(s)}
	if err := rc.adoptSnapshot(replPosition{LSN: 1}); err == nil {
		t.Error("adopted a snapshot it could not record")
	}
	rc.resume() // a position it cannot read: from the start
	if rc.applier.LastAppliedLSN() != 0 {
		t.Error("resumed from a position it could not read")
	}
	s.ReplicationRole = "leader"
	s.Binlog, _ = binlog.NewBinlog(filepath.Join(t.TempDir(), "x.db"), binlog.BinlogConfig{})
	defer func() { _ = s.Binlog.Close() }()
	_ = NewReplicationServer(s) // logs that it could not clear, and starts
}

func TestTheFollowersLog(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	if err := s.saveReplicationPosition(replPosition{LSN: 5, LogID: 9}); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	rc := NewReplicationClient(s, ReplicationClientConfig{Log: &binlog.BinlogConfig{Path: filepath.Join(dir, "f.binlog"), RetentionInterval: -1}})
	rc.resume()
	log := rc.log.Load()
	defer func() { _ = log.Close() }()
	if log.LogID() != 9 || log.CurrentLSN() != 5 {
		t.Fatalf("the copy starts as log %x at %d; the database is at 5 of log 9", log.LogID(), log.CurrentLSN())
	}
	rc.applier.SetPosition(replPosition{LSN: 7, LogID: 9})
	rc.appendLog(&binlog.BinlogEntry{LSN: 7, Type: binlog.BinlogPut}) // 6 is missing
	if log.CurrentLSN() != 7 || log.BaseLSN() != 7 {
		t.Errorf("after a gap the copy is at %d from %d; want an empty copy at 7", log.CurrentLSN(), log.BaseLSN())
	}
	// A copy that cannot be restarted is reported and kept.
	_ = os.Remove(log.Stats().Path)
	if err := os.Mkdir(log.Stats().Path, 0o700); err != nil {
		t.Fatal(err)
	}
	rc.rebaseLog(replPosition{LSN: 8})

	none := &ReplicationClient{server: s, applier: NewReplicationApplier(s)}
	none.appendLog(&binlog.BinlogEntry{LSN: 1})
	none.rebaseLog(replPosition{})

	bad := NewReplicationClient(s, ReplicationClientConfig{Log: &binlog.BinlogConfig{Path: filepath.Join(dir, "missing", "f.binlog")}})
	bad.resume()
	if bad.log.Load() != nil {
		t.Error("a copy that cannot be opened")
	}
}

func TestLagAndFollowerRecords(t *testing.T) {
	bl, err := binlog.NewBinlog(filepath.Join(t.TempDir(), "x.db"), binlog.BinlogConfig{RetentionInterval: -1})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = bl.Close() }()
	appendLeaderEntries(t, bl, 3)
	if err := bl.Reset(); err != nil { // a new log: the old entries' times are gone
		t.Fatal(err)
	}
	if lag := measureLag(bl, 1, time.Now()); lag.Entries == 0 || lag.Ms != 0 || lag.Exact {
		t.Errorf("lag with no time to measure from: %+v", lag)
	}
	if got := measureLag(nil, 1, time.Now()); !got.Exact || got.Entries != 0 {
		t.Errorf("no log: %+v", got)
	}
	for ms, want := range map[int64]string{0: "healthy", 2000: "warning", 60000: "unhealthy"} {
		if got := followerStatus(true, ms); got != want {
			t.Errorf("lag %d ms: %s, want %s", ms, got, want)
		}
	}

	rs := &ReplicationServer{server: &Server{Binlog: bl}, followers: map[string]*FollowerState{
		"gone":  {ID: "gone", DisconnectedAt: time.Now().Add(-48 * time.Hour)},
		"away":  {ID: "away", ConfirmedLSN: 4, DisconnectedAt: time.Now()},
		"ahead": {ID: "ahead", ConfirmedLSN: 9, Connected: true},
	}}
	if floor, held := rs.retentionFloor(); floor != 4 || !held || len(rs.followers) != 2 {
		t.Errorf("floor %d held %v with %d followers on record", floor, held, len(rs.followers))
	}
}

func appendLeaderEntries(t *testing.T, bl *binlog.Binlog, n int) {
	t.Helper()
	for range n {
		if err := bl.Append(&binlog.BinlogEntry{Type: binlog.BinlogPut, BucketName: "b"}); err != nil {
			t.Fatal(err)
		}
	}
}

func TestStatusOfAStoppedBinlog(t *testing.T) {
	var r ReplicationStatusResponse
	r.Healthy = true
	r.noteBinlogError(errUnit)
	if r.Healthy || !strings.Contains(r.LastError, "binlog stopped") {
		t.Errorf("status of a stopped binlog: %+v", r)
	}
}

// ackClient answers acknowledgements.
type ackClient struct {
	proto.MDDBReplicationClient
	resp *proto.AcknowledgeLSNResponse
	err  error
}

func (c *ackClient) AcknowledgeLSN(context.Context, *proto.AcknowledgeLSNRequest, ...grpc.CallOption) (*proto.AcknowledgeLSNResponse, error) {
	return c.resp, c.err
}

func TestTheFollowerClientsEdges(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	rc := NewReplicationClient(s, ReplicationClientConfig{LeaderAddr: "127.0.0.1:1"})

	rc.acknowledge(context.Background()) // no connection yet: nothing to do
	rc.client = &ackClient{err: errUnit}
	rc.acknowledge(context.Background())
	if rc.connected.Load() {
		t.Error("a failed acknowledgement counts as connected")
	}
	rc.client = &ackClient{resp: &proto.AcknowledgeLSNResponse{LeaderLsn: 10, LagMs: 1500}}
	rc.acknowledge(context.Background())
	if rc.lagEntries.Load() != 10 || rc.LagMs() != 1500 || !rc.connected.Load() {
		t.Errorf("after an acknowledgement: %d entries, %d ms", rc.lagEntries.Load(), rc.LagMs())
	}

	rc.needSnapshot.Store(true)
	if r := rc.unhealthyReason(); !strings.Contains(r, "snapshot") {
		t.Errorf("waiting for a snapshot: %q", r)
	}
	if err := rc.applyEntry(&binlog.BinlogEntry{LSN: 5, Type: binlog.BinlogPut, BucketName: "b"}); err == nil {
		t.Error("applied an entry out of sequence")
	}
	if err := rc.streamEnded(io.EOF); err != nil {
		t.Errorf("a stream the leader closed: %v", err)
	}
	rc.adoptLeaderLog(metadata.Pairs(replProtocolHeader, "2")) // a leader that names no log
	rc.adoptLeaderLog(metadata.Pairs(replProtocolHeader, "2", replLogIDHeader, "zz"))
	if rc.applier.Position().LogID != 0 {
		t.Error("adopted a log ID that does not parse")
	}

	bad := NewReplicationClient(s, ReplicationClientConfig{LeaderAddr: "dns://a b/%zz"})
	if err := bad.session(); err == nil || !strings.Contains(err.Error(), "connect") {
		t.Errorf("an address that cannot be dialled: %v", err)
	}
}

func TestASnapshotTheLeaderRefuses(t *testing.T) {
	useSecret(t)
	// A node without a binlog refuses snapshots.
	srv := grpc.NewServer()
	proto.RegisterMDDBReplicationServer(srv, NewReplicationServer(&Server{}))
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = srv.Serve(lis) }()
	defer srv.Stop()
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	rc := NewReplicationClient(s, ReplicationClientConfig{LeaderAddr: lis.Addr().String(), FollowerID: "f"})
	rc.needSnapshot.Store(true)
	err = rc.session()
	if !errors.Is(err, errSnapshotFailed) {
		t.Errorf("a refused snapshot: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := rc.connect(); err != nil {
		t.Fatal(err)
	}
	defer rc.disconnect()
	if err := rc.requestSnapshot(ctx); err == nil {
		t.Error("a snapshot requested on a cancelled context")
	}
}
