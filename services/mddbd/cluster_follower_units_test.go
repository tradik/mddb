package main

import (
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mddb/internal/binlog"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// GO-051: a cluster node's replication client, at its edges.

func TestClusterClientEdges(t *testing.T) {
	n := soloCluster(t, ackAsync)
	c := n.s.cluster
	rc := NewReplicationClient(n.s, ReplicationClientConfig{LeaderAddr: "127.0.0.1:1", FollowerID: "n1", Cluster: c})
	rc.resume()
	if rc.log.Load() != n.s.Binlog {
		t.Fatal("a cluster client keeps a log of its own")
	}
	// Acknowledgements coalesce: a second request while one is due is dropped.
	rc.ackSoon()
	rc.ackSoon()
	if len(rc.ackNow) != 1 {
		t.Fatalf("pending acknowledgements: %d", len(rc.ackNow))
	}
	// An entry that does not continue the node's log ends replication with a
	// snapshot; the log is not started again.
	bad := []*binlog.BinlogEntry{{LSN: n.s.Binlog.CurrentLSN() + 5, Type: binlog.BinlogCheckpoint}}
	rc.applier.SetPosition(replPosition{LSN: bad[0].LSN - 1})
	if err := rc.applyEntries(bad); err == nil || !rc.needSnapshot.Load() {
		t.Fatalf("a gap in the node's log: %v", err)
	}
	// A divergence whose writes cannot be saved is logged, not fatal.
	if err := n.write("c", 1); err != nil {
		t.Fatal(err)
	}
	c.settings.rejectedDir = filepath.Join(t.TempDir(), "missing")
	st, _ := status.New(codes.FailedPrecondition, "parted").WithDetails(&errdetails.ErrorInfo{
		Reason: reasonLogDiverged, Metadata: map[string]string{matchLSNKey: "0"},
	})
	rc.setAsideDiverged(st.Err())
	if c.rejected.Load() != nil {
		t.Fatal("a file recorded that was not written")
	}
	rc.setAsideDiverged(status.Error(codes.FailedPrecondition, "other"))
	// A plain follower is untouched by all of it.
	plain := &ReplicationClient{}
	plain.ackSoon()
	if plain.resyncIfDirty() != nil {
		t.Fatal("a plain follower resynchronised")
	}
}

func TestClusterNodeInAFullServer(t *testing.T) {
	covABaseEnv(t)
	useSecret(t)
	cfg := covAConfig(t)
	cfg.HTTP.Enabled, cfg.HTTP.Addr, cfg.HTTP.Mode = true, covAFreeAddr(t), ModeRW
	cfg.GRPC.Enabled, cfg.GRPC.Addr, cfg.GRPC.Mode = true, covAFreeAddr(t), ModeRW
	t.Setenv("MDDB_NODE_ID", "solo")
	t.Setenv("MDDB_CLUSTER_PEERS", "solo="+cfg.GRPC.Addr)
	t.Setenv("MDDB_CLUSTER_BOOTSTRAP", "true")
	t.Setenv("MDDB_CLUSTER_ADVERTISE_HTTP", "http://"+cfg.HTTP.Addr)
	r := covAStartServer(t, cfg)
	deadline := time.Now().Add(10 * time.Second)
	for {
		code, body := covAGet(t, "http://"+cfg.HTTP.Addr+"/v1/replication/status")
		if code == http.StatusOK && strings.Contains(body, `"writable":true`) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the node never led: %s", body)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if got := r.stop(t); got != 0 {
		t.Fatalf("exit code %d", got)
	}
	if !strings.Contains(r.logs.String(), "cluster node configured") {
		t.Fatal("no cluster in the log")
	}
}
