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

func TestElectionPicksOneLeaderAndFollowersLearnIt(t *testing.T) {
	g := newGroup(t, []string{"a", "b", "c"}, true)
	for _, id := range []string{"a", "b", "c"} {
		g.start(id, false)
	}
	l := g.leader(3 * time.Second)
	id, http, term := l.Leader()
	if id != l.cfg.NodeID || http != "http://"+id || term == 0 {
		t.Fatalf("leader says %q %q %d", id, http, term)
	}
	waitFor(t, "followers to know the leader", func() bool {
		for _, other := range g.w.nodes {
			if lid, _, _ := other.Leader(); lid != id {
				return false
			}
		}
		return true
	})
	for _, other := range g.w.nodes {
		if other != l && !errors.Is(other.CanWrite(), ErrNotLeader) {
			t.Fatalf("a follower: %v", other.CanWrite())
		}
	}
	st := l.Status()
	if st.State != Leader || !st.LeaseValid || !st.Writable || st.Majority != 2 || len(st.Nodes) != 3 || !st.AutoFailover {
		t.Fatalf("status %+v", st)
	}
	if l.LeaseRemaining() <= 0 {
		t.Fatal("no lease left on a leader")
	}
}

func TestLeaderCutOffStepsDownAndTheMajorityElects(t *testing.T) {
	g := newGroup(t, []string{"a", "b", "c"}, true)
	for _, id := range []string{"a", "b", "c"} {
		g.start(id, false)
	}
	old := g.leader(3 * time.Second)
	oldID := old.cfg.NodeID
	g.w.setDown(oldID, true)
	waitFor(t, "the cut-off leader to stop", func() bool { return old.CanWrite() != nil })
	if old.LeaseRemaining() != 0 {
		t.Fatal("a lease after stepping down")
	}
	next := g.leader(3 * time.Second)
	if next == old {
		t.Fatal("the cut-off node leads")
	}
	g.w.setDown(oldID, false)
	waitFor(t, "the old leader to follow", func() bool {
		id, _, _ := old.Leader()
		return id == next.cfg.NodeID && old.Term() == next.Term()
	})
}

func TestVoteRules(t *testing.T) {
	g := newGroup(t, []string{"a", "b", "c"}, true)
	n, err := New(g.cfgs["a"], link{w: g.w, from: "a"}, g.logs["a"].hooks(), NewStore(filepath.Join(g.dir, "a.json")))
	if err != nil {
		t.Fatal(err)
	}
	g.logs["a"].set(Position{LSN: 10, Term: 2})
	cases := []struct {
		name string
		req  VoteRequest
		want bool
		why  string
	}{
		{"behind log", VoteRequest{Term: 3, Candidate: "b", Last: Position{LSN: 9, Term: 2}}, false, "log is behind"},
		{"up to date", VoteRequest{Term: 3, Candidate: "b", Last: Position{LSN: 10, Term: 2}}, true, ""},
		{"same candidate again", VoteRequest{Term: 3, Candidate: "b", Last: Position{LSN: 10, Term: 2}}, true, ""},
		{"second candidate, same term", VoteRequest{Term: 3, Candidate: "c", Last: Position{LSN: 50, Term: 2}}, false, "voted for b"},
		{"old term", VoteRequest{Term: 2, Candidate: "c", Last: Position{LSN: 50, Term: 3}}, false, "term is behind"},
		{"later term, later log term", VoteRequest{Term: 4, Candidate: "c", Last: Position{LSN: 1, Term: 3}}, true, ""},
	}
	for _, c := range cases {
		r := n.HandleVote(c.req)
		if r.Granted != c.want || !strings.Contains(r.Reason, c.why) {
			t.Errorf("%s: %+v", c.name, r)
		}
	}
	// The vote is on disk: a restarted node does not vote again in term 4.
	n2, err := New(g.cfgs["a"], link{w: g.w, from: "a"}, g.logs["a"].hooks(), NewStore(filepath.Join(g.dir, "a.json")))
	if err != nil {
		t.Fatal(err)
	}
	if r := n2.HandleVote(VoteRequest{Term: 4, Candidate: "b", Last: Position{LSN: 99, Term: 9}}); r.Granted {
		t.Fatal("voted twice in a term after a restart")
	}
	// A node that heard from a leader a moment ago refuses a candidate.
	n2.HandleHeartbeat(HeartbeatRequest{Term: 4, Leader: "c", LeaderHTTP: "http://c"})
	if r := n2.HandleVote(VoteRequest{Term: 5, Candidate: "b", Last: Position{LSN: 99, Term: 9}}); r.Granted || !strings.Contains(r.Reason, "heard from leader c") {
		t.Fatalf("vote while the leader is alive: %+v", r)
	}
	// After the leader hands over it may vote at once.
	n2.HandleHeartbeat(HeartbeatRequest{Term: 4, Leader: "c", Release: true})
	if r := n2.HandleVote(VoteRequest{Term: 5, Candidate: "b", Last: Position{LSN: 99, Term: 9}}); !r.Granted {
		t.Fatalf("vote after a release: %+v", r)
	}
	// A heartbeat from an older term is refused.
	if r := n2.HandleHeartbeat(HeartbeatRequest{Term: 1, Leader: "c"}); r.OK || r.Term != 5 {
		t.Fatalf("stale heartbeat: %+v", r)
	}
}

func TestLeaderWithALeaseRefusesVotes(t *testing.T) {
	g := newGroup(t, []string{"a", "b", "c"}, true)
	for _, id := range []string{"a", "b", "c"} {
		g.start(id, false)
	}
	l := g.leader(3 * time.Second)
	r := l.HandleVote(VoteRequest{Term: l.Term() + 1, Candidate: "x", Last: Position{LSN: 1 << 40, Term: 1 << 40}})
	if r.Granted || !strings.Contains(r.Reason, "valid lease") {
		t.Fatalf("%+v", r)
	}
}

func TestPromotionHandsOverFromTheLeader(t *testing.T) {
	g := newGroup(t, []string{"a", "b", "c"}, true)
	nodes := map[string]*Node{}
	for _, id := range []string{"a", "b", "c"} {
		nodes[id] = g.start(id, id == "a")
	}
	l := g.leader(3 * time.Second)
	var target *Node
	for _, n := range nodes {
		if n != l {
			target = n
			break
		}
	}
	// The target is behind until it "replicates" the leader's last entry.
	tid := target.cfg.NodeID
	lpos := Position{LSN: 7, Term: l.Term()}
	g.logs[l.cfg.NodeID].set(lpos)
	for id := range nodes {
		if id != tid && id != l.cfg.NodeID {
			g.logs[id].set(lpos)
		}
	}
	go func() {
		time.Sleep(30 * time.Millisecond)
		g.logs[tid].set(lpos)
	}()
	if err := target.Promote(context.Background(), PromoteOptions{}); err != nil {
		t.Fatal(err)
	}
	if g.leader(3*time.Second) != target {
		t.Fatal("the promoted node does not lead")
	}
	if err := target.Promote(context.Background(), PromoteOptions{}); err != nil {
		t.Fatalf("promoting the leader again: %v", err)
	}
	if err := target.Promote(context.Background(), PromoteOptions{Force: true}); err == nil {
		t.Fatal("force with automatic failover")
	}
}

func TestPromotionRefusedWhenAnotherNodeIsAhead(t *testing.T) {
	g := newGroup(t, []string{"a", "b", "c"}, false)
	a, b := g.start("a", true), g.start("b", false)
	g.start("c", false)
	g.leader(3 * time.Second)
	g.stop("a") // the leader is gone; c has a later log than b
	g.logs["c"].set(Position{LSN: 5, Term: 1})
	err := b.Promote(context.Background(), PromoteOptions{})
	if !errors.Is(err, ErrBehind) {
		t.Fatalf("promotion behind c: %v", err)
	}
	g.logs["b"].set(Position{LSN: 5, Term: 1})
	if err := b.Promote(context.Background(), PromoteOptions{}); err != nil {
		t.Fatal(err)
	}
	_ = a
}

func TestPromotionTimesOutWhenTheNodeDoesNotCatchUp(t *testing.T) {
	g := newGroup(t, []string{"a", "b", "c"}, false)
	g.start("a", true)
	b := g.start("b", false)
	g.start("c", false)
	g.leader(3 * time.Second)
	g.logs["a"].set(Position{LSN: 9, Term: 1})
	err := b.Promote(context.Background(), PromoteOptions{CatchUp: 30 * time.Millisecond})
	if !errors.Is(err, ErrNotCaughtUp) {
		t.Fatalf("got %v", err)
	}
}

func TestForcedPromotionWithoutAMajority(t *testing.T) {
	g := newGroup(t, []string{"a", "b"}, false)
	a := g.start("a", true)
	b := g.start("b", false)
	g.leader(3 * time.Second)
	g.stop("a")
	if err := b.Promote(context.Background(), PromoteOptions{}); !errors.Is(err, ErrElectionLost) {
		t.Fatalf("an election with half the nodes: %v", err)
	}
	if err := b.Promote(context.Background(), PromoteOptions{Force: true}); err != nil {
		t.Fatal(err)
	}
	if b.CanWrite() != nil || b.Term() < 2 {
		t.Fatalf("forced leader: %v term %d", b.CanWrite(), b.Term())
	}
	_ = a
}

func TestManualLeaderResumesAfterARestart(t *testing.T) {
	g := newGroup(t, []string{"a", "b"}, false)
	g.start("a", true)
	g.start("b", false)
	l := g.leader(3 * time.Second)
	term := l.Term()
	g.stop("a")
	a := g.start("a", false)
	waitFor(t, "the leader to resume", func() bool { return a.CanWrite() == nil })
	if a.Term() != term {
		t.Fatalf("resumed in term %d, led %d", a.Term(), term)
	}
}

func TestDemoteAndStepDownErrors(t *testing.T) {
	g := newGroup(t, []string{"a", "b"}, false)
	a := g.start("a", true)
	b := g.start("b", false)
	g.leader(3 * time.Second)
	if err := b.Demote(); !errors.Is(err, ErrNotLeader) {
		t.Fatalf("demoting a follower: %v", err)
	}
	if err := a.Demote(); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(a.CanWrite(), ErrNoLeader) {
		t.Fatalf("after demotion: %v", a.CanWrite())
	}
	if _, err := a.StepDown(0, "b"); !errors.Is(err, ErrNotLeader) {
		t.Fatal("stepping down twice")
	}
}

func TestLeadHookFailureStepsDown(t *testing.T) {
	g := newGroup(t, []string{"a", "b"}, false)
	g.logs["a"].failLead.Store(true)
	a := g.start("a", true)
	waitFor(t, "the failed take-over", func() bool { return g.logs["a"].leads.Load() > 0 && a.State() == Follower })
	if a.CanWrite() == nil {
		t.Fatal("writes open after a failed take-over")
	}
}

func TestBootstrapWithFailoverStandsAtOnce(t *testing.T) {
	g := newGroup(t, []string{"a", "b", "c"}, true)
	g.start("b", false)
	g.start("c", false)
	start := time.Now()
	a := g.start("a", true)
	if g.leader(3*time.Second) != a {
		t.Fatal("the bootstrap node did not lead")
	}
	if time.Since(start) > et {
		t.Logf("bootstrap took %s", time.Since(start))
	}
}

func TestProbeAndObserve(t *testing.T) {
	g := newGroup(t, []string{"a", "b"}, false)
	n, err := New(g.cfgs["a"], link{w: g.w, from: "a"}, g.logs["a"].hooks(), NewStore(filepath.Join(g.dir, "a.json")))
	if err != nil {
		t.Fatal(err)
	}
	g.logs["a"].set(Position{LSN: 3, Term: 1})
	n.Observe(7)
	p := n.Probe()
	if p.Term != 7 || p.State != Follower || p.Last.LSN != 3 || p.HTTP != "http://a" {
		t.Fatalf("%+v", p)
	}
	if n.Config().NodeID != "a" {
		t.Fatal("config")
	}
}

func TestConfigAndPeers(t *testing.T) {
	peers, err := ParsePeers(" a=h1:1, b=h2:2 ,, c=h3:3")
	if err != nil || len(peers) != 3 || peers[1] != (Peer{ID: "b", Addr: "h2:2"}) {
		t.Fatalf("%v %v", peers, err)
	}
	for _, bad := range []string{"", "a", "a=", "=h", "a=h,a=h"} {
		if _, err := ParsePeers(bad); err == nil {
			t.Errorf("%q parsed", bad)
		}
	}
	c := Config{NodeID: "a", Peers: peers[:2], AutoFailover: true}
	warn, err := c.Validate()
	if err != nil || c.AutoFailover || !strings.Contains(warn, "at least 3") || c.Heartbeat != DefaultHeartbeat || c.ElectionTimeout != DefaultElectionTimeout {
		t.Fatalf("%q %v %+v", warn, err, c)
	}
	if _, err := (&Config{NodeID: "x", Peers: peers}).Validate(); err == nil {
		t.Fatal("a node outside its peers")
	}
	if _, err := (&Config{NodeID: "a", Peers: peers, Heartbeat: time.Second, ElectionTimeout: time.Second}).Validate(); err == nil {
		t.Fatal("timeout under twice the heartbeat")
	}
	if !(Position{LSN: 1, Term: 2}).AtLeast(Position{LSN: 9, Term: 1}) || (Position{LSN: 1, Term: 1}).AtLeast(Position{LSN: 2, Term: 1}) {
		t.Fatal("AtLeast")
	}
}

func TestStoreErrors(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "v.json")
	if err := os.WriteFile(path, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := New(Config{NodeID: "a", Peers: []Peer{{ID: "a"}}}, nil, Hooks{}, NewStore(path)); err == nil {
		t.Fatal("damaged store")
	}
	if err := os.WriteFile(path, []byte(`{"version":9}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewStore(path).load(); err == nil {
		t.Fatal("newer store")
	}
	if NewStore(path).Path() != path {
		t.Fatal("path")
	}
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chmod(dir, 0o700) }()
	if os.Geteuid() != 0 {
		if err := NewStore(path).save(vote{Term: 1}); err == nil {
			t.Fatal("saved into a read-only directory")
		}
	}
	if err := os.Chmod(path, 0); err == nil && os.Geteuid() != 0 {
		if _, err := NewStore(path).load(); err == nil {
			t.Fatal("read an unreadable store")
		}
	}
}
