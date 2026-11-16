//go:build clusterprocess

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// GO-051 with real processes: three mddbd binaries, kill -9 of the leader,
// the time to a new leader, and the old leader back on its files. Run with
//
//	go test -tags clusterprocess -run TestClusterProcesses -v .
//
// It builds the server once into a temporary directory. MDDB_CLUSTER_ELECTION_TIMEOUT
// and MDDB_CLUSTER_HEARTBEAT are the defaults (3s, 500ms) unless the test's
// environment sets CLUSTER_TEST_ELECTION / CLUSTER_TEST_HEARTBEAT.

type procNode struct {
	id, dir, http, grpc string
	cmd                 *exec.Cmd
}

func freePort(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	return l.Addr().String()
}

func (n *procNode) start(t *testing.T, bin, peers string) {
	t.Helper()
	logf, err := os.OpenFile(filepath.Join(n.dir, "server.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bin)
	cmd.Env = append(os.Environ(),
		"MDDB_PATH="+filepath.Join(n.dir, "mddb.db"), "MDDB_HTTP_ADDR="+n.http, "MDDB_GRPC_ADDR="+n.grpc,
		"MDDB_NODE_ID="+n.id, "MDDB_CLUSTER_PEERS="+peers, "MDDB_CLUSTER_ADVERTISE_HTTP=http://"+n.http,
		"MDDB_REPLICATION_SECRET=process-test", "MDDB_MCP_ENABLED=false", "MDDB_UPDATE_CHECK=0",
		"MDDB_EMBEDDING_AUTODETECT=false",
		"MDDB_CLUSTER_ELECTION_TIMEOUT="+envOr("CLUSTER_TEST_ELECTION", "3s"),
		"MDDB_CLUSTER_HEARTBEAT="+envOr("CLUSTER_TEST_HEARTBEAT", "500ms"))
	cmd.Stdout, cmd.Stderr, cmd.Dir = logf, logf, n.dir
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	n.cmd = cmd
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func (n *procNode) kill() {
	if n.cmd != nil {
		_ = n.cmd.Process.Signal(syscall.SIGKILL)
		_ = n.cmd.Wait()
		n.cmd = nil
	}
}

func (n *procNode) stop() {
	if n.cmd != nil {
		_ = n.cmd.Process.Signal(os.Interrupt)
		done := make(chan struct{})
		go func() { _ = n.cmd.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(30 * time.Second):
			_ = n.cmd.Process.Kill()
		}
		n.cmd = nil
	}
}

// writable reports whether the node leads and takes writes.
func (n *procNode) writable() bool {
	resp, err := http.Get("http://" + n.http + "/v1/replication/status")
	if err != nil {
		return false
	}
	defer func() { _ = resp.Body.Close() }()
	var st struct {
		Cluster *struct {
			Writable bool `json:"writable"`
		} `json:"cluster"`
	}
	return json.NewDecoder(resp.Body).Decode(&st) == nil && st.Cluster != nil && st.Cluster.Writable
}

func (n *procNode) add(i int) (int, error) {
	body, _ := json.Marshal(map[string]any{"collection": "p", "key": fmt.Sprintf("d%05d", i), "lang": "en", "contentMd": fmt.Sprintf("doc %d", i)})
	resp, err := http.Post("http://"+n.http+"/v1/add", "application/json", bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	_ = resp.Body.Close()
	return resp.StatusCode, nil
}

func (n *procNode) checksum() (string, int) {
	resp, err := http.Get("http://" + n.http + "/v1/checksum?collection=p")
	if err != nil {
		return "", -1
	}
	defer func() { _ = resp.Body.Close() }()
	var out struct {
		Checksum string `json:"checksum"`
		Count    int    `json:"documentCount"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return out.Checksum, out.Count
}

func waitLeader(t *testing.T, nodes []*procNode, within time.Duration) *procNode {
	t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		var found []*procNode
		for _, n := range nodes {
			if n.cmd != nil && n.writable() {
				found = append(found, n)
			}
		}
		if len(found) > 1 {
			t.Fatalf("%d nodes take writes at once", len(found))
		}
		if len(found) == 1 {
			return found[0]
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("no leader within %s", within)
	return nil
}

func TestClusterProcesses(t *testing.T) {
	root := t.TempDir()
	bin := filepath.Join(root, "mddbd")
	build := exec.Command("go", "build", "-o", bin, ".")
	build.Stdout, build.Stderr = os.Stderr, os.Stderr
	if err := build.Run(); err != nil {
		t.Fatal(err)
	}
	var nodes []*procNode
	var peers []string
	for i := 1; i <= 3; i++ {
		n := &procNode{id: fmt.Sprintf("n%d", i), dir: filepath.Join(root, fmt.Sprintf("n%d", i)), http: freePort(t), grpc: freePort(t)}
		if err := os.MkdirAll(n.dir, 0o700); err != nil {
			t.Fatal(err)
		}
		nodes = append(nodes, n)
		peers = append(peers, n.id+"="+n.grpc)
	}
	defer func() {
		for _, n := range nodes {
			n.stop()
		}
	}()
	for _, n := range nodes {
		n.start(t, bin, strings.Join(peers, ","))
	}
	old := waitLeader(t, nodes, 60*time.Second)
	for i := range 50 {
		if code, err := old.add(i); err != nil || code != http.StatusOK {
			t.Fatalf("write %d: %d %v", i, code, err)
		}
	}
	for _, n := range nodes {
		if n == old {
			continue
		}
		// 503 until the first heartbeat names the leader, then 421.
		deadline := time.Now().Add(10 * time.Second)
		for code, _ := n.add(1000); code != http.StatusMisdirectedRequest; code, _ = n.add(1000) {
			if code != http.StatusServiceUnavailable || time.Now().After(deadline) {
				t.Fatalf("a follower answered a write with %d", code)
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
	time.Sleep(time.Second)

	killed := time.Now()
	old.kill()
	next := waitLeader(t, nodes, 30*time.Second)
	t.Logf("FAILOVER: %s leads %s after kill -9 of %s", next.id, time.Since(killed).Round(time.Millisecond), old.id)
	for i := 50; i < 100; i++ {
		if code, err := next.add(i); err != nil || code != http.StatusOK {
			t.Fatalf("write %d on the new leader: %d %v", i, code, err)
		}
	}
	old.start(t, bin, strings.Join(peers, ","))
	want, count := next.checksum()
	deadline := time.Now().Add(60 * time.Second)
	for {
		got, _ := old.checksum()
		if got == want {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the old leader holds %s, the leader %s (%d docs)", got, want, count)
		}
		time.Sleep(100 * time.Millisecond)
	}
	if count != 100 {
		t.Fatalf("the leader holds %d documents, want 100", count)
	}
	if l := waitLeader(t, nodes, 10*time.Second); l != next {
		t.Fatalf("leadership moved to %s", l.id)
	}
}
