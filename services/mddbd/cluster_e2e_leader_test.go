package main

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"mddb/internal/cluster"
)

// Finding the leader of a test cluster (cluster_e2e_helpers_test.go), and
// writing through it while leadership may still move. On a slow runner a
// node can miss heartbeats long enough to stand, so the leader a test picks
// may lose its term before the test writes to it (#444). A test that checks
// something else waits for a stable leader and sends its setup writes with
// writeAll, which follows a re-election instead of failing on it.

// stableFor is how long a leader must hold before a test relies on it: two
// election timeouts, longer than any follower waits before it stands.
var stableFor = 2 * testElection

// maxLeaderMoves bounds how often writeAll follows leadership: a cluster
// that keeps re-electing is a failure, not noise.
const maxLeaderMoves = 5

// leader waits for exactly one node that takes writes, checking on every
// poll that no two nodes lead in the same term.
func (tc *testCluster) leader(within time.Duration) *testNode {
	tc.t.Helper()
	deadline := time.Now().Add(e2eScaled(within))
	for time.Now().Before(deadline) {
		tc.checkOneLeaderPerTerm()
		var writable []*testNode
		for _, n := range tc.nodes {
			if n.up && !n.cut.Load() && n.s.cluster.node.CanWrite() == nil {
				writable = append(writable, n)
			}
		}
		if len(writable) == 1 {
			return writable[0]
		}
		time.Sleep(5 * time.Millisecond)
	}
	tc.t.Fatalf("no single leader within %s: %s", within, tc.describe())
	return nil
}

// stableLeader waits for a leader that every running node follows and that
// keeps the same node and term for stableFor. Like leader, it fails when two
// nodes lead the same term.
func (tc *testCluster) stableLeader(within time.Duration) *testNode {
	tc.t.Helper()
	deadline := time.Now().Add(e2eScaled(within))
	var held *testNode
	var heldTerm uint64
	var since time.Time
	for time.Now().Before(deadline) {
		tc.checkOneLeaderPerTerm()
		l, term := tc.agreedLeader()
		switch {
		case l == nil:
			held = nil
		case l != held || term != heldTerm:
			held, heldTerm, since = l, term, time.Now()
		case time.Since(since) >= stableFor:
			return l
		}
		time.Sleep(5 * time.Millisecond)
	}
	tc.t.Fatalf("no leader held for %s within %s: %s", stableFor, within, tc.describe())
	return nil
}

// agreedLeader is the one node that takes writes and its term, when every
// other running node that is not cut off follows it in that term; nil
// otherwise.
func (tc *testCluster) agreedLeader() (*testNode, uint64) {
	var l *testNode
	for _, n := range tc.nodes {
		if !n.up || n.cut.Load() || n.s.cluster.node.CanWrite() != nil {
			continue
		}
		if l != nil {
			return nil, 0
		}
		l = n
	}
	if l == nil {
		return nil, 0
	}
	term := l.s.cluster.node.Term()
	for _, n := range tc.nodes {
		if n == l || !n.up || n.cut.Load() {
			continue
		}
		if id, _, t := n.s.cluster.node.Leader(); id != l.id || t != term {
			return nil, 0
		}
	}
	return l, term
}

// writeAll writes documents from to to-1 of collection c through l. A write
// refused because l no longer leads waits for the stable leader and is sent
// there; any other error fails the test. It returns the node that took the
// last write.
func (tc *testCluster) writeAll(l *testNode, c string, from, to int) *testNode {
	tc.t.Helper()
	moves := 0
	for i := from; i < to; {
		err := l.write(c, i)
		if err == nil {
			i++
			continue
		}
		var nl *notLeaderError
		if !errors.As(err, &nl) || moves == maxLeaderMoves {
			tc.t.Fatalf("writing %s/%d on %s: %v (%s)", c, i, l.id, err, tc.describe())
		}
		moves++
		tc.t.Logf("%s refused %s/%d (%v); waiting for the stable leader", l.id, c, i, err)
		l = tc.stableLeader(5 * time.Second)
	}
	return l
}

// checkOneLeaderPerTerm fails when two nodes lead the same term.
func (tc *testCluster) checkOneLeaderPerTerm() {
	tc.t.Helper()
	leaders := map[uint64]string{}
	for _, n := range tc.nodes {
		if !n.up || n.s.cluster.node.State() != cluster.Leader {
			continue
		}
		term := n.s.cluster.node.Term()
		if other, dup := leaders[term]; dup {
			tc.t.Fatalf("%s and %s both lead term %d", other, n.id, term)
		}
		leaders[term] = n.id
	}
}

func (tc *testCluster) describe() string {
	var parts []string
	for _, n := range tc.nodes {
		if !n.up {
			parts = append(parts, n.id+": down")
			continue
		}
		st := n.s.cluster.node.Status()
		parts = append(parts, fmt.Sprintf("%s: %s term %d leader %q writable %v cut %v", n.id, st.State, st.Term, st.LeaderID, st.Writable, n.cut.Load()))
	}
	return strings.Join(parts, "; ")
}
