package main

import (
	"bytes"
	"crypto/tls"
	"fmt"
	"net"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"mddb/internal/binlog"
	proto "mddb/proto"

	bolt "go.etcd.io/bbolt"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

// Leader and follower nodes for the end-to-end replication tests (GO-050):
// real servers on real files, talking gRPC over a TCP port. They replace
// test_replication.sh, which started two binaries by hand.

const e2eSecret = "e2e-replication-secret"

// useSecret makes the leader and followers of a test authenticate with a
// replication secret; without it they need mTLS.
func useSecret(t *testing.T) { t.Setenv("MDDB_REPLICATION_SECRET", e2eSecret) }

// e2eLeader is a leader node with its replication service listening.
type e2eLeader struct {
	s         *Server
	rs        *ReplicationServer
	srv       *grpc.Server
	addr      string
	snapshots atomic.Int64 // snapshot requests served
	// gate, when set, holds every snapshot request until it is closed.
	gate atomic.Pointer[chan struct{}]
}

// e2eLeaderOpts is how a leader is started.
type e2eLeaderOpts struct {
	dir  string // the node's directory
	db   string // the database file in it; "" is leader.db
	addr string // "" picks a free port
	log  binlog.BinlogConfig
	tls  *tls.Config // the listener's; nil is plaintext
}

func startE2ELeader(t *testing.T, o e2eLeaderOpts) *e2eLeader {
	t.Helper()
	if o.db == "" {
		o.db = "leader.db"
	}
	s := newHandlerTestServerAt(t, filepath.Join(o.dir, o.db))
	s.ReplicationRole = "leader"
	s.NodeID = "leader"
	bl, err := binlog.NewBinlog(s.Path, o.log)
	if err != nil {
		t.Fatal(err)
	}
	s.Binlog = bl
	l := &e2eLeader{s: s, rs: NewReplicationServer(s)}
	s.replServer = l.rs
	opts := []grpc.ServerOption{grpc.ChainStreamInterceptor(l.intercept)}
	if o.tls != nil {
		opts = append(opts, grpc.Creds(credentials.NewTLS(o.tls)))
	}
	l.srv = grpc.NewServer(opts...)
	proto.RegisterMDDBReplicationServer(l.srv, l.rs)
	lis, err := net.Listen("tcp", listenAddr(o.addr))
	if err != nil {
		t.Fatal(err)
	}
	l.addr = lis.Addr().String()
	go func() { _ = l.srv.Serve(lis) }()
	return l
}

func listenAddr(addr string) string {
	if addr == "" {
		return "127.0.0.1:0"
	}
	return addr
}

// intercept counts snapshot requests and holds them at the gate.
func (l *e2eLeader) intercept(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, h grpc.StreamHandler) error {
	if info.FullMethod == proto.MDDBReplication_RequestSnapshot_FullMethodName {
		l.snapshots.Add(1)
		if g := l.gate.Load(); g != nil {
			<-*g
		}
	}
	return h(srv, ss)
}

// stop shuts the leader down as a clean stop does.
func (l *e2eLeader) stop(t *testing.T) {
	t.Helper()
	l.srv.Stop()
	if err := l.s.Binlog.Close(); err != nil {
		t.Errorf("closing the leader's binlog: %v", err)
	}
	closeNode(l.s)
}

// write adds n documents to collection c, numbered from first. It reports
// with t.Error, so a writer goroutine may call it.
func (l *e2eLeader) write(t *testing.T, c string, first, n int) {
	t.Helper()
	for i := first; i < first+n; i++ {
		if err := writeDoc(l.s, c, i); err != nil {
			t.Errorf("writing %s/%d: %v", c, i, err)
			return
		}
	}
}

func writeDoc(s *Server, c string, i int) error {
	_, _, err := s.addDocument(c, fmt.Sprintf("doc-%04d", i), "en", nil, fmt.Sprintf("content %d", i), 0, true)
	return err
}

// e2eFollower is a follower node replicating from a leader.
type e2eFollower struct {
	s  *Server
	rc *ReplicationClient
}

// e2eFollowerOpts is how a follower is started.
type e2eFollowerOpts struct {
	id     string // "" is "follower"
	dir    string
	leader string
	tls    *tls.Config
	maxLag time.Duration
}

func startE2EFollower(t *testing.T, o e2eFollowerOpts) *e2eFollower {
	t.Helper()
	s := newHandlerTestServerAt(t, filepath.Join(o.dir, "follower.db"))
	s.ReplicationRole = "follower"
	s.Mode = ModeRead // as main.go makes every follower
	if o.id == "" {
		o.id = "follower"
	}
	s.NodeID = o.id
	rc := NewReplicationClient(s, ReplicationClientConfig{
		LeaderAddr:    o.leader,
		FollowerID:    o.id,
		RetryInterval: 50 * time.Millisecond,
		AckInterval:   50 * time.Millisecond,
		MaxLag:        o.maxLag,
		TLS:           o.tls,
		Log:           &binlog.BinlogConfig{RetentionInterval: -1},
	})
	s.replClient = rc
	rc.Start()
	return &e2eFollower{s: s, rc: rc}
}

func (f *e2eFollower) stop() {
	f.rc.Stop()
	closeNode(f.s)
}

// closeNode closes every file of a node.
func closeNode(s *Server) {
	s.closeCollectionFiles()
	_ = s.DB.Close()
}

// waitE2E polls cond until it holds, failing the test after 20 seconds.
func waitE2E(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// caughtUp is whether the follower holds everything in the leader's log.
func caughtUp(l *e2eLeader, f *e2eFollower) bool {
	pos := f.rc.applier.Position()
	return pos.LogID == l.s.Binlog.LogID() && pos.LSN == l.s.Binlog.CurrentLSN()
}

// replicatedBuckets are the buckets whose content followers must match.
var replicatedBuckets = []string{"docs", "bykey", "idxmeta"}

// nodeContent is every key and value of the replicated buckets, across the
// main file and every collection file.
func nodeContent(t *testing.T, s *Server) map[string]string {
	t.Helper()
	out := map[string]string{}
	read := func(tx *bolt.Tx) error {
		for _, name := range replicatedBuckets {
			b := tx.Bucket([]byte(name))
			if b == nil {
				continue
			}
			_ = b.ForEach(func(k, v []byte) error {
				out[name+"/"+string(k)] = string(v)
				return nil
			})
		}
		return nil
	}
	if err := s.DBView(read); err != nil {
		t.Fatal(err)
	}
	if err := s.DBViewAll(read); err != nil {
		t.Fatal(err)
	}
	return out
}

// assertSameContent fails when the follower's data is not the leader's.
func assertSameContent(t *testing.T, l *e2eLeader, f *e2eFollower) {
	t.Helper()
	want, got := nodeContent(t, l.s), nodeContent(t, f.s)
	if len(want) == 0 {
		t.Fatal("the leader holds nothing to compare")
	}
	if len(got) != len(want) {
		t.Errorf("follower holds %d keys, leader %d", len(got), len(want))
	}
	for k, v := range want {
		if g, ok := got[k]; !ok || !bytes.Equal([]byte(g), []byte(v)) {
			t.Errorf("key %q differs on the follower", k)
			return
		}
	}
}
