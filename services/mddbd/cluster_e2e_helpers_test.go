package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"mddb/internal/binlog"
	"mddb/internal/cluster"
	proto "mddb/proto"

	"google.golang.org/grpc"
)

// Clusters of in-process nodes for the GO-051 tests: real servers on real
// files, electing over gRPC on loopback ports. A node can be cut off from
// the network (every connection it has or makes fails), killed (stopped
// without closing its log, as kill -9 leaves it) and started again on the
// same files and port.

// Short timings: the tests run elections in well under a second.
const (
	testHeartbeat = 20 * time.Millisecond
	testElection  = 150 * time.Millisecond
)

type clusterOpts struct {
	nodes int
	auto  bool
	ack   ackMode
}

type testCluster struct {
	t     *testing.T
	opts  clusterOpts
	peers []cluster.Peer
	nodes []*testNode
}

// testNode is one node; cut makes its network fail.
type testNode struct {
	id, dir, addr string
	s             *Server
	srv           *grpc.Server
	cut           atomic.Bool
	up            bool
}

func startTestCluster(t *testing.T, o clusterOpts) *testCluster {
	t.Helper()
	useSecret(t)
	if o.ack == "" {
		o.ack = ackAsync
	}
	tc := &testCluster{t: t, opts: o}
	root := t.TempDir()
	for i := range o.nodes {
		n := &testNode{id: fmt.Sprintf("n%d", i+1), dir: filepath.Join(root, fmt.Sprintf("n%d", i+1))}
		if err := os.MkdirAll(n.dir, 0o700); err != nil {
			t.Fatal(err)
		}
		lis, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		n.addr = lis.Addr().String()
		_ = lis.Close()
		tc.nodes = append(tc.nodes, n)
		tc.peers = append(tc.peers, cluster.Peer{ID: n.id, Addr: n.addr})
	}
	for i := range tc.nodes {
		tc.boot(i, i == 0)
	}
	t.Cleanup(tc.stopAll)
	return tc
}

// boot starts node i on its files and port.
func (tc *testCluster) boot(i int, bootstrap bool) {
	t := tc.t
	t.Helper()
	n := tc.nodes[i]
	n.cut.Store(false)
	n.s = newHandlerTestServerAt(t, filepath.Join(n.dir, "node.db"))
	n.s.NodeID = n.id
	cs := clusterSettings{
		node: cluster.Config{
			NodeID: n.id, Peers: tc.peers, AutoFailover: tc.opts.auto,
			Heartbeat: testHeartbeat, ElectionTimeout: testElection, HTTP: "http://" + n.id + ".test",
		},
		ack: tc.opts.ack, ackTimeout: 2 * time.Second, bootstrap: bootstrap,
	}
	if _, err := cs.node.Validate(); err != nil {
		t.Fatal(err)
	}
	if err := n.s.initCluster(cs, binlog.BinlogConfig{RetentionInterval: -1}); err != nil {
		t.Fatal(err)
	}
	n.s.cluster.tr.extra = []grpc.DialOption{grpc.WithContextDialer(n.dial)}
	n.s.replServer.Store(NewReplicationServer(n.s))
	n.srv = grpc.NewServer()
	proto.RegisterMDDBReplicationServer(n.srv, n.s.replServer.Load())
	var lis net.Listener
	var err error
	for range 50 { // the port was this node's a moment ago
		if lis, err = net.Listen("tcp", n.addr); err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = n.srv.Serve(&cutListener{Listener: lis, node: n}) }()
	n.up = true
	n.s.cluster.start()
}

// dial is the node's outgoing network, which fails while it is cut off.
func (n *testNode) dial(ctx context.Context, addr string) (net.Conn, error) {
	if n.cut.Load() {
		return nil, errors.New("cut off")
	}
	c, err := (&net.Dialer{}).DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, err
	}
	return &cutConn{Conn: c, node: n}, nil
}

// cutListener accepts connections that fail while the node is cut off.
type cutListener struct {
	net.Listener
	node *testNode
}

func (l *cutListener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	return &cutConn{Conn: c, node: l.node}, nil
}

type cutConn struct {
	net.Conn
	node *testNode
}

var errCut = errors.New("network cut")

func (c *cutConn) Read(p []byte) (int, error) {
	if c.node.cut.Load() {
		_ = c.Close()
		return 0, errCut
	}
	return c.Conn.Read(p)
}

func (c *cutConn) Write(p []byte) (int, error) {
	if c.node.cut.Load() {
		_ = c.Close()
		return 0, errCut
	}
	return c.Conn.Write(p)
}

// halt stops node n; clean closes its log as a stop does, otherwise the log
// is left as kill -9 leaves it: not marked closed.
func (tc *testCluster) halt(n *testNode, clean bool) {
	if !n.up {
		return
	}
	n.up = false
	n.cut.Store(true) // its connections die with it
	n.srv.Stop()
	n.s.cluster.stop()
	bl := n.s.Binlog
	path := bl.Stats().Path
	_ = bl.Close()
	if !clean {
		markLogOpen(tc.t, path)
	}
	closeNode(n.s)
}

// markLogOpen rewrites the sidecar as a process that died leaves it.
func markLogOpen(t *testing.T, path string) {
	t.Helper()
	raw, err := os.ReadFile(path + ".meta") // #nosec G304 -- path under the test node's t.TempDir()
	if err != nil {
		t.Fatal(err)
	}
	var meta map[string]any
	if err := json.Unmarshal(raw, &meta); err != nil {
		t.Fatal(err)
	}
	meta["state"] = "open"
	raw, _ = json.Marshal(meta)
	if err := os.WriteFile(path+".meta", raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

func (tc *testCluster) stopAll() {
	for _, n := range tc.nodes {
		tc.halt(n, true)
	}
}

// write adds a document to collection c on node n.
func (n *testNode) write(c string, i int) error {
	if err := n.s.writeBlocked(); err != nil {
		return err
	}
	return writeDoc(n.s, c, i)
}

// checksum is the node's /v1/checksum of collection c.
func (n *testNode) checksum(c string) (string, int) { return n.s.collectionChecksum(c) }

// waitSame waits until every running node holds what leader holds in c.
func (tc *testCluster) waitSame(leader *testNode, c string) {
	tc.t.Helper()
	want, count := leader.checksum(c)
	waitE2E(tc.t, "every node to match the leader's "+c, func() bool {
		for _, n := range tc.nodes {
			if !n.up {
				continue
			}
			if got, _ := n.checksum(c); got != want {
				return false
			}
		}
		return true
	})
	if count == 0 {
		tc.t.Fatalf("the leader holds no documents in %s", c)
	}
}
