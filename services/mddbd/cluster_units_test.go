package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mddb/internal/binlog"
	"mddb/internal/cluster"
	proto "mddb/proto"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// GO-051: the cluster's configuration, errors, RPCs and edges, one at a time.

func TestClusterSettingsFromEnv(t *testing.T) {
	clear := func() {
		for _, k := range []string{"MDDB_CLUSTER_PEERS", "MDDB_REPLICATION_ACK", "MDDB_CLUSTER_HEARTBEAT", "MDDB_CLUSTER_ELECTION_TIMEOUT",
			"MDDB_REPLICATION_ACK_TIMEOUT", "MDDB_CLUSTER_BOOTSTRAP", "MDDB_REPLICATION_ROLE", "MDDB_CLUSTER_ADVERTISE_HTTP", "MDDB_CLUSTER_REJECTED_DIR"} {
			t.Setenv(k, "")
		}
	}
	clear()
	if _, ok, err := clusterSettingsFromEnv("n1"); ok || err != nil {
		t.Fatalf("no cluster: %v %v", ok, err)
	}
	t.Setenv("MDDB_REPLICATION_ACK", "quorum")
	if _, _, err := clusterSettingsFromEnv("n1"); err == nil || !strings.Contains(err.Error(), "needs MDDB_CLUSTER_PEERS") {
		t.Fatalf("quorum outside a cluster: %v", err)
	}
	t.Setenv("MDDB_REPLICATION_ACK", "sometimes")
	if _, _, err := clusterSettingsFromEnv("n1"); err == nil {
		t.Fatal("an unknown ack mode outside a cluster")
	}
	t.Setenv("MDDB_CLUSTER_PEERS", "n1=a:1,n2=b:2,n3=c:3")
	if _, ok, err := clusterSettingsFromEnv("n1"); !ok || err == nil {
		t.Fatal("an unknown ack mode")
	}
	t.Setenv("MDDB_REPLICATION_ACK", "QUORUM")
	t.Setenv("MDDB_CLUSTER_HEARTBEAT", "-1s")
	if _, _, err := clusterSettingsFromEnv("n1"); err == nil {
		t.Fatal("a negative heartbeat")
	}
	t.Setenv("MDDB_CLUSTER_HEARTBEAT", "200ms")
	t.Setenv("MDDB_CLUSTER_ELECTION_TIMEOUT", "2s")
	t.Setenv("MDDB_REPLICATION_ROLE", "leader")
	t.Setenv("MDDB_CLUSTER_ADVERTISE_HTTP", "http://a:11023/")
	t.Setenv("MDDB_CLUSTER_REJECTED_DIR", "/r")
	cs, ok, err := clusterSettingsFromEnv("n1")
	if !ok || err != nil || cs.ack != ackQuorum || cs.ackTimeout != 5*time.Second || !cs.bootstrap ||
		cs.node.Heartbeat != 200*time.Millisecond || cs.node.ElectionTimeout != 2*time.Second ||
		cs.node.HTTP != "http://a:11023" || cs.rejectedDir != "/r" || !cs.node.AutoFailover || len(cs.node.Peers) != 3 {
		t.Fatalf("%+v %v %v", cs, ok, err)
	}
	t.Setenv("MDDB_CLUSTER_PEERS", "n1")
	if _, _, err := clusterSettingsFromEnv("n1"); err == nil {
		t.Fatal("malformed peers")
	}
}

func TestNotLeaderAnswers(t *testing.T) {
	known := &notLeaderError{LeaderID: "n2", LeaderHTTP: "http://n2", LeaderGRPC: "n2:1", Term: 4}
	none := &notLeaderError{Term: 4}
	if !strings.Contains(known.Error(), "send writes to http://n2") || !strings.Contains(none.Error(), "election is in progress") {
		t.Fatal(known.Error(), none.Error())
	}
	rec := httptest.NewRecorder()
	writeNotLeader(rec, none)
	if rec.Code != http.StatusServiceUnavailable || rec.Header().Get("Retry-After") != "1" {
		t.Fatalf("%d %v", rec.Code, rec.Header())
	}
	for _, c := range []struct {
		e      *notLeaderError
		code   codes.Code
		reason string
	}{{known, codes.FailedPrecondition, "NOT_LEADER"}, {none, codes.Unavailable, "NO_LEADER"}} {
		st := status.Convert(c.e.grpcStatus())
		info, _ := st.Details()[0].(*errdetails.ErrorInfo)
		if st.Code() != c.code || info == nil || info.Reason != c.reason || info.Metadata["term"] != "4" {
			t.Fatalf("%v %v", st, info)
		}
	}
}

// incomingSecret is a server-side context carrying the replication secret.
func incomingSecret() context.Context {
	return metadata.NewIncomingContext(context.Background(), metadata.Pairs("x-mddb-replication-secret", e2eSecret))
}

// soloCluster is one node of a one-node cluster without failover, led at
// once (bootstrap).
func soloCluster(t *testing.T, ack ackMode) *testNode {
	t.Helper()
	tc := startTestCluster(t, clusterOpts{nodes: 1, ack: ack})
	return tc.leader(5 * time.Second)
}

func TestClusterRPCsRefuseWhatTheyMust(t *testing.T) {
	n := soloCluster(t, ackAsync)
	rs := n.s.replServer.Load()
	ctx := withReplicationSecret(context.Background())
	inCtx := incomingSecret()

	// Without credentials, and on a node outside a cluster.
	if _, err := rs.RequestVote(ctx, &proto.VoteRequest{}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("vote without the secret: %v", err)
	}
	plain := &ReplicationServer{server: &Server{}}
	if _, err := plain.Heartbeat(inCtx, &proto.HeartbeatRequest{}); !errors.Is(err, errNotInCluster) {
		t.Fatalf("heartbeat outside a cluster: %v", err)
	}
	if _, err := plain.StepDown(inCtx, &proto.StepDownRequest{}); !errors.Is(err, errNotInCluster) {
		t.Fatal(err)
	}
	// A heartbeat from an older term, and a vote the leader refuses.
	hb, err := rs.Heartbeat(inCtx, &proto.HeartbeatRequest{Term: 0, LeaderId: "x"})
	if err != nil || hb.Ok {
		t.Fatalf("%+v %v", hb, err)
	}
	if v, err := rs.RequestVote(inCtx, &proto.VoteRequest{Term: 0, CandidateId: "x"}); err != nil || v.Granted {
		t.Fatalf("%+v %v", v, err)
	}

	// Streams: a follower outside a cluster passes; a later term demotes
	// this node; a node that does not lead refuses.
	c := n.s.cluster
	if err := c.checkStream(&proto.StreamBinlogRequest{}); err != nil {
		t.Fatal(err)
	}
	if err := c.checkStream(&proto.StreamBinlogRequest{Term: 99}); status.Code(err) != codes.Aborted {
		t.Fatalf("a later term: %v", err)
	}
	if err := c.checkStream(&proto.StreamBinlogRequest{Term: 1}); status.Code(err) != codes.Unavailable {
		t.Fatalf("not the leader: %v", err)
	}
	if _, err := rs.StepDown(inCtx, &proto.StepDownRequest{}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("stepping down a follower: %v", err)
	}
	if _, ok := divergedAt(errors.New("plain")); ok {
		t.Fatal("a plain error is a divergence")
	}
}

func TestClusterEdgesOfASoloNode(t *testing.T) {
	n := soloCluster(t, ackQuorum)
	c := n.s.cluster
	// One node is its own majority: a quorum write returns at once.
	if err := n.write("q", 1); err != nil {
		t.Fatal(err)
	}
	// A rejected-writes file with nothing to reject is not written.
	if path, cnt, err := c.saveRejected(c.s.Binlog.CurrentLSN()); path != "" || cnt != 0 || err != nil {
		t.Fatalf("%q %d %v", path, cnt, err)
	}
	// One that cannot be written says so.
	c.settings.rejectedDir = filepath.Join(t.TempDir(), "missing")
	if _, _, err := c.saveRejected(0); err == nil {
		t.Fatal("wrote into a missing directory")
	}
	// One whose entries the log no longer holds fails after opening it.
	c.settings.rejectedDir = ""
	if err := c.s.Binlog.Jump(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := c.saveRejected(0); err == nil {
		t.Fatal("saved entries the log no longer holds")
	}
	if fileSafe("a/b c") != "a_b_c" {
		t.Fatal("fileSafe")
	}
	// The status of a leader that does not take writes is unhealthy.
	if clusterUnhealthy(cluster.Status{State: cluster.Leader}) == "" || clusterUnhealthy(cluster.Status{State: cluster.Leader, Writable: true}) != "" {
		t.Fatal("clusterUnhealthy")
	}
	// Demoted, a quorum wait ends at once: the node does not lead.
	if err := c.node.Demote(); err != nil {
		t.Fatal(err)
	}
	c.settings.node.Peers = append(c.settings.node.Peers, cluster.Peer{ID: "x"}, cluster.Peer{ID: "y"})
	if err := c.waitQuorum(c.s.Binlog.CurrentLSN() + 1); !errors.Is(err, errQuorumTimeout) {
		t.Fatalf("waiting on a follower: %v", err)
	}
	// The gRPC write guard names the leader (none is known here).
	g := &GRPCServer{server: n.s}
	if status.Code(g.checkWritable()) != codes.Unavailable {
		t.Fatalf("%v", g.checkWritable())
	}
}

func TestClusterHTTPRefusals(t *testing.T) {
	s := newHandlerTestServerAt(t, filepath.Join(t.TempDir(), "x.db"))
	rec := httptest.NewRecorder()
	s.handleReplicationPromote(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatal(rec.Code)
	}
	if rec := postAdmin(s.handleReplicationDemote, ""); rec.Code != http.StatusBadRequest {
		t.Fatal(rec.Code)
	}
	n := soloCluster(t, ackAsync)
	if rec := postAdmin(n.s.handleReplicationPromote, "{"); rec.Code != http.StatusBadRequest {
		t.Fatal(rec.Code)
	}
	if rec := postAdmin(n.s.handleReplicationPromote, ""); rec.Code != http.StatusOK {
		t.Fatalf("promoting the leader: %d %s", rec.Code, rec.Body)
	}
	var body map[string]any
	rec = httptest.NewRecorder()
	n.s.handleReplicationStatus(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body["role"] != "leader" || body["cluster"] == nil {
		t.Fatalf("%v", body)
	}
}

func TestInitClusterFailures(t *testing.T) {
	useSecret(t)
	peers := []cluster.Peer{{ID: "n1", Addr: "127.0.0.1:1"}}
	s := newHandlerTestServerAt(t, filepath.Join(t.TempDir(), "x.db"))
	cs := clusterSettings{node: cluster.Config{Peers: peers}}
	if err := s.initCluster(cs, binlog.BinlogConfig{}); err == nil {
		t.Fatal("no node ID")
	}
	s.NodeID = "nx"
	if err := s.initCluster(cs, binlog.BinlogConfig{}); err == nil {
		t.Fatal("a node outside its peers")
	}
	s.NodeID = "n1"
	t.Setenv("MDDB_REPLICATION_TLS_CA", filepath.Join(t.TempDir(), "missing.pem"))
	if err := s.initCluster(cs, binlog.BinlogConfig{}); err == nil {
		t.Fatal("a missing CA")
	}
	t.Setenv("MDDB_REPLICATION_TLS_CA", "")
	if err := s.initCluster(cs, binlog.BinlogConfig{Path: filepath.Join(t.TempDir(), "no", "dir", "b")}); err == nil {
		t.Fatal("a binlog that cannot be opened")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "mddb.cluster.json"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := s.initCluster(cs, binlog.BinlogConfig{Path: filepath.Join(dir, "b"), RetentionInterval: -1}); err == nil {
		t.Fatal("a damaged term store")
	}
	cs.node.AutoFailover = true // one node: turned off, with a warning
	if err := s.initCluster(cs, binlog.BinlogConfig{Path: filepath.Join(t.TempDir(), "b"), RetentionInterval: -1}); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Binlog.Close() }()
	// Taking over fails when the log's sidecar cannot be written.
	if os.Geteuid() == 0 {
		t.Skip("root writes into a read-only directory")
	}
	// A write refused after its commit makes the log dirty: taking over
	// moves it ahead first, which fails here too.
	s.Binlog.SetWriteGate(func() error { return errors.New("no") })
	_ = s.Binlog.Append(&binlog.BinlogEntry{Type: binlog.BinlogPut, BucketName: "b", Key: []byte("k")})
	s.Binlog.SetWriteGate(nil)
	logDir := filepath.Dir(s.Binlog.Stats().Path)
	if err := os.Chmod(logDir, 0o500); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chmod(logDir, 0o700) }()
	if err := s.cluster.lead(1); err == nil {
		t.Fatal("led with a dirty log that cannot move ahead")
	}
	if err := s.cluster.lead(1); err == nil {
		t.Fatal("led with a log that cannot record the term")
	}
}

func TestInitClusterNeedsGRPC(t *testing.T) {
	t.Setenv("MDDB_CLUSTER_PEERS", "n1=127.0.0.1:1")
	s := newHandlerTestServerAt(t, filepath.Join(t.TempDir(), "x.db"))
	s.NodeID = "n1"
	c := covAExpectFatal(t, func() { s.initClusterFromEnv() })
	if !strings.Contains(c.args, "needs gRPC") {
		t.Fatalf("%+v", c)
	}
}

func TestADirtyLogMovesAheadWhenItsNodeLeads(t *testing.T) {
	tc := startTestCluster(t, clusterOpts{nodes: 1})
	n := tc.leader(5 * time.Second)
	if err := n.write("c", 1); err != nil {
		t.Fatal(err)
	}
	before := n.s.Binlog.CurrentLSN()
	tc.halt(n, false)
	tc.boot(0, false) // the term it led resumes; its log was not closed
	n = tc.leader(5 * time.Second)
	if n.s.Binlog.CurrentLSN() < before+1<<32 || n.s.Binlog.Dirty() {
		t.Fatalf("lsn %d after %d, dirty %v", n.s.Binlog.CurrentLSN(), before, n.s.Binlog.Dirty())
	}
}

func TestPeerTransportErrors(t *testing.T) {
	tr := newPeerTransport(&tls.Config{MinVersion: tls.VersionTLS12})
	defer tr.close()
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	p := cluster.Peer{ID: "x", Addr: "127.0.0.1:1"}
	if _, err := tr.RequestVote(ctx, p, cluster.VoteRequest{}); err == nil {
		t.Fatal("vote")
	}
	if _, err := tr.Heartbeat(ctx, p, cluster.HeartbeatRequest{}); err == nil {
		t.Fatal("heartbeat")
	}
	if _, err := tr.Probe(ctx, p); err == nil {
		t.Fatal("probe")
	}
	if _, err := tr.StepDown(ctx, p, 1, "y"); err == nil {
		t.Fatal("step down")
	}
	bad := cluster.Peer{ID: "y", Addr: "unix:///\x00bad"}
	if _, err := tr.RequestVote(ctx, bad, cluster.VoteRequest{}); err == nil {
		t.Fatal("a target that cannot be dialled")
	}
	if _, err := tr.Heartbeat(ctx, bad, cluster.HeartbeatRequest{}); err == nil {
		t.Fatal("heartbeat to a bad target")
	}
	if _, err := tr.Probe(ctx, bad); err == nil {
		t.Fatal("probe of a bad target")
	}
	if _, err := tr.StepDown(ctx, bad, 1, "y"); err == nil {
		t.Fatal("step down of a bad target")
	}
}
