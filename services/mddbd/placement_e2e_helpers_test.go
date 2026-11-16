package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"testing"
	"time"

	"mddb/internal/binlog"
	"mddb/internal/cluster"

	"google.golang.org/grpc"
)

// Groups of in-process nodes for the GO-052 tests: every node a real server
// on its own files, with its REST API on an httptest server and its gRPC
// API (the replication service included) on a loopback port.

const (
	testPlacementRefresh = 40 * time.Millisecond
	testDropGrace        = 400 * time.Millisecond
)

// swapHandler lets a test server start before its handler exists.
type swapHandler struct {
	mu sync.RWMutex
	h  http.Handler
}

func (sh *swapHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	sh.mu.RLock()
	h := sh.h
	sh.mu.RUnlock()
	if h == nil {
		http.Error(w, `{"error":"starting"}`, http.StatusServiceUnavailable)
		return
	}
	h.ServeHTTP(w, r)
}

// placementNodeT is one node of a test placement.
type placementNodeT struct {
	id, group, dir, grpcAddr string
	s                        *Server
	grpcSrv                  *grpc.Server
	http                     *httptest.Server
	handler                  *swapHandler
}

// testPlacement is a set of groups, each a cluster.
type testPlacement struct {
	t     *testing.T
	cfg   placementConfig
	nodes []*placementNodeT
}

// startTestPlacement starts groups of the given sizes; meta is the first.
func startTestPlacement(t *testing.T, sizes map[string]int, meta string) *testPlacement {
	t.Helper()
	useSecret(t)
	t.Setenv("MDDB_STORAGE_LAYOUT", "per-collection")
	tp := &testPlacement{t: t, cfg: placementConfig{meta: meta, groups: map[string][]placementNode{},
		refresh: testPlacementRefresh, dropGrace: testDropGrace, fenceTimeout: 5 * time.Second}}
	root := t.TempDir()
	names := make([]string, 0, len(sizes))
	for g := range sizes {
		names = append(names, g)
	}
	sort.Strings(names)
	tp.cfg.names = names
	for _, g := range names {
		for i := range sizes[g] {
			n := &placementNodeT{id: fmt.Sprintf("%s-n%d", g, i+1), group: g, handler: &swapHandler{}}
			n.dir = filepath.Join(root, n.id)
			if err := os.MkdirAll(n.dir, 0o700); err != nil {
				t.Fatal(err)
			}
			lis, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			n.grpcAddr = lis.Addr().String()
			_ = lis.Close()
			n.http = httptest.NewServer(n.handler)
			tp.nodes = append(tp.nodes, n)
			tp.cfg.groups[g] = append(tp.cfg.groups[g], placementNode{ID: n.id, GRPC: n.grpcAddr, HTTP: n.http.URL})
		}
	}
	for _, n := range tp.nodes {
		tp.boot(n)
	}
	t.Cleanup(tp.stopAll)
	tp.waitReady()
	return tp
}

// boot starts node n.
func (tp *testPlacement) boot(n *placementNodeT) {
	t := tp.t
	t.Helper()
	n.s = newHandlerTestServerAt(t, filepath.Join(n.dir, "node.db"))
	n.s.NodeID = n.id
	var peers []cluster.Peer
	for _, pn := range tp.cfg.groups[n.group] {
		peers = append(peers, cluster.Peer{ID: pn.ID, Addr: pn.GRPC})
	}
	cs := clusterSettings{
		node: cluster.Config{NodeID: n.id, Peers: peers, AutoFailover: len(peers) >= 3,
			Heartbeat: testHeartbeat, ElectionTimeout: testElection, HTTP: n.http.URL},
		ack: ackAsync, ackTimeout: 2 * time.Second, bootstrap: peers[0].ID == n.id,
	}
	if _, err := cs.node.Validate(); err != nil {
		t.Fatal(err)
	}
	if err := n.s.initCluster(cs, binlog.BinlogConfig{RetentionInterval: -1}); err != nil {
		t.Fatal(err)
	}
	// The writers the test server has, on the node's log.
	n.s.VectorStore.SetBinlog(n.s.Binlog)
	n.s.FTSIndex.SetBinlog(n.s.Binlog)
	n.s.TTLManager.SetBinlog(n.s.Binlog)
	n.s.WebhookManager.SetBinlog(n.s.Binlog)
	n.s.SchemaManager.SetBinlog(n.s.Binlog)
	n.s.CollectionManager.SetBinlog(n.s.Binlog)
	if err := n.s.initPlacement(tp.cfg); err != nil {
		t.Fatal(err)
	}
	n.grpcSrv = newGRPCTransport(n.s, grpc.ChainUnaryInterceptor(n.s.placementUnaryInterceptor()),
		grpc.ChainStreamInterceptor(n.s.placementStreamInterceptor()))
	lis, err := net.Listen("tcp", n.grpcAddr)
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = n.grpcSrv.Serve(lis) }()
	mux := http.NewServeMux()
	n.s.registerRoutes(mux, false)
	n.handler.mu.Lock()
	n.handler.h = mux
	n.handler.mu.Unlock()
	n.s.cluster.start()
	n.s.placement.start()
}

func (tp *testPlacement) stopAll() {
	for _, n := range tp.nodes {
		n.http.Close()
		n.s.placement.shutdown()
		n.grpcSrv.Stop()
		n.s.cluster.stop()
		_ = n.s.Binlog.Close()
		closeNode(n.s)
	}
}

// waitReady waits until every group has a leader, every node a map and
// knows every group's leader.
func (tp *testPlacement) waitReady() {
	waitE2E(tp.t, "the placement to settle", func() bool {
		for _, n := range tp.nodes {
			if n.s.placement.current() == nil {
				return false
			}
			for _, g := range tp.cfg.names {
				if n.s.placement.leaderOf(g) == "" {
					return false
				}
			}
		}
		return true
	})
}

// leader is group's leader.
func (tp *testPlacement) leader(group string) *placementNodeT {
	for _, n := range tp.nodes {
		if n.group == group && n.s.placement.leads() {
			return n
		}
	}
	tp.t.Fatalf("group %s has no leader", group)
	return nil
}

// call sends a REST request to node n and decodes the answer into out.
func (n *placementNodeT) call(method, path string, body, out any) (int, error) {
	var rd io.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		rd = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, n.http.URL+path, rd)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	if out != nil && len(raw) > 0 {
		if err := json.Unmarshal(raw, out); err != nil {
			return resp.StatusCode, fmt.Errorf("%s %s: %d %s", method, path, resp.StatusCode, raw)
		}
	}
	if resp.StatusCode >= 300 {
		return resp.StatusCode, fmt.Errorf("%s %s: %d %s", method, path, resp.StatusCode, raw)
	}
	return resp.StatusCode, nil
}

// add writes a document through node n.
func (n *placementNodeT) add(c, key, content string) error {
	_, err := n.call(http.MethodPost, "/v1/add", map[string]any{"collection": c, "key": key, "lang": "en",
		"contentMd": content}, nil)
	return err
}

// holds reports whether node n has a file for collection c.
func (n *placementNodeT) holds(c string) bool {
	return n.s.collectionStore().get(c) != nil
}
