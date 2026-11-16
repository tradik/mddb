package cluster

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The paths a running cluster rarely takes, driven one at a time.

// idle is a node of a 3-node group that is not started.
func idle(t *testing.T, store *Store) (*Node, *fakeLog) {
	t.Helper()
	g := newGroup(t, []string{"a", "b", "c"}, true)
	if store == nil {
		store = NewStore(filepath.Join(g.dir, "a.json"))
	}
	n, err := New(g.cfgs["a"], link{w: g.w, from: "a"}, g.logs["a"].hooks(), store)
	if err != nil {
		t.Fatal(err)
	}
	return n, g.logs["a"]
}

// brokenStore is a store whose saves fail: its path is a directory.
func brokenStore(t *testing.T) *Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "vote")
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	// New reads it: a directory reads as an error, so load first from a
	// file and break it after.
	return &Store{path: path}
}

func TestWhenTheTermCannotBeSaved(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "v.json"))
	n, _ := idle(t, store)
	store.path = brokenStore(t).path

	if _, won := n.campaign(context.Background()); won || n.State() != Follower {
		t.Fatal("stood without recording its vote")
	}
	r := n.HandleVote(VoteRequest{Term: 9, Candidate: "b", Last: Position{LSN: 1, Term: 1}})
	if r.Granted || !strings.Contains(r.Reason, "could not record") {
		t.Fatalf("%+v", r)
	}
	n.mu.Lock()
	n.leadLocked() // logs that the led term is not recorded, and leads
	n.mu.Unlock()
	if n.State() != Leader {
		t.Fatal("not leading")
	}
}

func TestReplyWithALaterTermStepsTheLeaderDown(t *testing.T) {
	n, _ := idle(t, nil)
	n.mu.Lock()
	n.term = 1
	n.leadLocked()
	n.extendLeaseLocked() // only this node: no majority, no lease
	lease := n.leaseUntil
	n.notePeerLocked("nobody", nil)
	n.mu.Unlock()
	if !lease.IsZero() {
		t.Fatal("a lease without a majority")
	}
	n.mu.Lock()
	n.readyTerm = n.term
	n.mu.Unlock()
	if !errors.Is(n.CanWrite(), ErrNoLeader) {
		t.Fatal("a leader without a lease takes writes")
	}
	n.onBeatReply("b", 1, time.Now(), HeartbeatReply{Term: 5}, nil)
	if n.State() != Follower || n.Term() != 5 {
		t.Fatalf("%s term %d", n.State(), n.Term())
	}
	n.onBeatReply("b", 5, time.Now(), HeartbeatReply{}, errors.New("down"))
	if n.peers["b"].Reachable {
		t.Fatal("reachable after a failed heartbeat")
	}
}

func TestApplySkipsWhatIsAlreadyApplied(t *testing.T) {
	n, log := idle(t, nil)
	n.mu.Lock()
	n.state, n.term = Candidate, 3
	n.mu.Unlock()
	done := applied{state: Follower, term: 3}
	if got := n.apply(done); got != done || log.follows.Load() != 0 {
		t.Fatalf("a candidate re-applied: %+v", got)
	}
	// A take-over that finds the node stepped down meanwhile changes nothing.
	n.takeOver(3)
	if log.leads.Load() != 1 || n.readyTerm != 0 {
		t.Fatal("ready after stepping down")
	}
}

func TestWaitsEndWithTheContext(t *testing.T) {
	n, _ := idle(t, nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := n.catchUp(ctx, Position{LSN: 9, Term: 9}, time.Hour); !errors.Is(err, context.Canceled) {
		t.Fatalf("catch-up: %v", err)
	}
	if err := n.waitReady(context.Background(), 7); !errors.Is(err, ErrElectionLost) {
		t.Fatalf("a term this node does not lead: %v", err)
	}
	n.mu.Lock()
	n.state, n.term = Leader, 7
	n.mu.Unlock()
	if err := n.waitReady(ctx, 7); !errors.Is(err, context.Canceled) {
		t.Fatalf("waiting for a take-over: %v", err)
	}
	if r, err := n.StepDown(9, "b"); err != nil || r.Term != 9 {
		t.Fatalf("stepping down to a later term: %+v %v", r, err)
	}
}

func TestForceTakesTheTermAfterEveryTermSeen(t *testing.T) {
	g := newGroup(t, []string{"a", "b"}, false)
	g.start("b", false)
	a := g.start("a", false)
	if err := a.forceLead(context.Background(), map[string]ProbeReply{"b": {Term: 9}}); err != nil {
		t.Fatal(err)
	}
	if a.Term() != 10 {
		t.Fatalf("term %d", a.Term())
	}
}

// stuckLeader is a network whose leader does not step down.
type stuckLeader struct{ link }

func (stuckLeader) StepDown(context.Context, Peer, uint64, string) (StepDownReply, error) {
	return StepDownReply{}, errors.New("refused")
}

func TestPromotionWhenTheLeaderWillNotStepDown(t *testing.T) {
	g := newGroup(t, []string{"a", "b"}, false)
	g.start("a", true)
	g.leader(3 * time.Second)
	b, err := New(g.cfgs["b"], stuckLeader{link{w: g.w, from: "b"}}, g.logs["b"].hooks(), NewStore(filepath.Join(g.dir, "b.json")))
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Promote(context.Background(), PromoteOptions{}); err == nil || !strings.Contains(err.Error(), "did not step down") {
		t.Fatalf("got %v", err)
	}
}

func TestStatusForgetsPeersNotHeardFrom(t *testing.T) {
	n, _ := idle(t, nil)
	n.mu.Lock()
	n.peers["b"].Reachable = true
	n.peers["b"].LastSeen = time.Now().Add(-time.Hour)
	n.mu.Unlock()
	for _, p := range n.Status().Nodes {
		if p.ID == "b" && p.Reachable {
			t.Fatal("a peer not heard from for an hour is reachable")
		}
	}
	if n.LeaseRemaining() != 0 {
		t.Fatal("a follower's lease")
	}
}
