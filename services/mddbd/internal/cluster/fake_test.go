package cluster

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// An in-memory network of nodes for the election tests.

var errDown = errors.New("unreachable")

type network struct {
	mu    sync.Mutex
	nodes map[string]*Node
	down  map[string]bool // a node whose links all fail
}

func (w *network) reach(from, to string) (*Node, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.down[from] || w.down[to] || w.nodes[to] == nil {
		return nil, errDown
	}
	return w.nodes[to], nil
}

func (w *network) setDown(id string, down bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.down[id] = down
}

// link is one node's transport.
type link struct {
	w    *network
	from string
}

func (l link) RequestVote(_ context.Context, to Peer, req VoteRequest) (VoteReply, error) {
	n, err := l.w.reach(l.from, to.ID)
	if err != nil {
		return VoteReply{}, err
	}
	return n.HandleVote(req), nil
}

func (l link) Heartbeat(_ context.Context, to Peer, req HeartbeatRequest) (HeartbeatReply, error) {
	n, err := l.w.reach(l.from, to.ID)
	if err != nil {
		return HeartbeatReply{}, err
	}
	return n.HandleHeartbeat(req), nil
}

func (l link) Probe(_ context.Context, to Peer) (ProbeReply, error) {
	n, err := l.w.reach(l.from, to.ID)
	if err != nil {
		return ProbeReply{}, err
	}
	return n.Probe(), nil
}

func (l link) StepDown(_ context.Context, to Peer, term uint64, successor string) (StepDownReply, error) {
	n, err := l.w.reach(l.from, to.ID)
	if err != nil {
		return StepDownReply{}, err
	}
	return n.StepDown(term, successor)
}

// fakeLog is a node's log position, and what its hooks were told.
type fakeLog struct {
	mu       sync.Mutex
	pos      Position
	leads    atomic.Int32
	follows  atomic.Int32
	failLead atomic.Bool
}

func (f *fakeLog) Position() Position {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.pos
}

func (f *fakeLog) set(p Position) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pos = p
}

func (f *fakeLog) hooks() Hooks {
	return Hooks{
		Position: f.Position,
		Lead: func(uint64) error {
			f.leads.Add(1)
			if f.failLead.Load() {
				return errors.New("cannot lead")
			}
			return nil
		},
		Follow: func(uint64, Peer) { f.follows.Add(1) },
	}
}

const (
	hb = 10 * time.Millisecond
	et = 60 * time.Millisecond
)

type group struct {
	t    *testing.T
	w    *network
	logs map[string]*fakeLog
	cfgs map[string]Config
	dir  string
}

func newGroup(t *testing.T, ids []string, auto bool) *group {
	t.Helper()
	g := &group{t: t, w: &network{nodes: map[string]*Node{}, down: map[string]bool{}},
		logs: map[string]*fakeLog{}, cfgs: map[string]Config{}, dir: t.TempDir()}
	var peers []Peer
	for _, id := range ids {
		peers = append(peers, Peer{ID: id, Addr: id + ":1"})
	}
	for _, id := range ids {
		cfg := Config{NodeID: id, Peers: peers, Heartbeat: hb, ElectionTimeout: et, AutoFailover: auto, HTTP: "http://" + id}
		if _, err := cfg.Validate(); err != nil {
			t.Fatal(err)
		}
		g.cfgs[id] = cfg
		g.logs[id] = &fakeLog{}
	}
	return g
}

// start creates and starts node id.
func (g *group) start(id string, bootstrap bool) *Node {
	g.t.Helper()
	n, err := New(g.cfgs[id], link{w: g.w, from: id}, g.logs[id].hooks(), NewStore(filepath.Join(g.dir, id+".json")))
	if err != nil {
		g.t.Fatal(err)
	}
	g.w.mu.Lock()
	g.w.nodes[id] = n
	g.w.mu.Unlock()
	n.Start(bootstrap)
	g.t.Cleanup(n.Stop)
	return n
}

// stop removes node id from the network.
func (g *group) stop(id string) {
	g.w.mu.Lock()
	n := g.w.nodes[id]
	delete(g.w.nodes, id)
	g.w.mu.Unlock()
	if n != nil {
		n.Stop()
	}
}

// leader waits for exactly one writable node among the reachable ones.
func (g *group) leader(within time.Duration) *Node {
	g.t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		var found []*Node
		g.w.mu.Lock()
		for id, n := range g.w.nodes {
			if !g.w.down[id] && n.CanWrite() == nil {
				found = append(found, n)
			}
		}
		g.w.mu.Unlock()
		if len(found) == 1 {
			return found[0]
		}
		time.Sleep(2 * time.Millisecond)
	}
	g.t.Fatal("no single leader")
	return nil
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(2 * time.Millisecond)
	}
}
