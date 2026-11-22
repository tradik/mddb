package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"mddb/internal/cluster"
)

// GO-051 acceptance, on in-process nodes (cluster_e2e_helpers_test.go).

func TestClusterElectsOneLeaderAndReplicates(t *testing.T) {
	tc := startTestCluster(t, clusterOpts{nodes: 3, auto: true})
	l := tc.writeAll(tc.stableLeader(5*time.Second), "c", 0, 20)
	tc.waitSame(l, "c")
	for _, n := range tc.nodes {
		if n == l {
			continue
		}
		var nl *notLeaderError
		if err := n.write("c", 99); !errors.As(err, &nl) || nl.LeaderID != l.id || nl.LeaderHTTP != "http://"+l.id+".test" {
			t.Fatalf("a follower's write: %v, want a redirect to %s", err, l.id)
		}
		// The log's gate refuses even a write that skipped the guards.
		if err := writeDoc(n.s, "c", 98); err == nil {
			t.Fatal("a follower's log took a write")
		}
	}
}

func TestClusterFailoverWhenTheLeaderIsKilled(t *testing.T) {
	tc := startTestCluster(t, clusterOpts{nodes: 3, auto: true})
	old := tc.writeAll(tc.stableLeader(5*time.Second), "c", 0, 10)
	tc.waitSame(old, "c")
	old = tc.stableLeader(5 * time.Second) // the node to kill leads now

	killed := time.Now()
	tc.halt(old, false)
	next := tc.leader(10 * time.Second)
	t.Logf("new leader %s %s after the kill (election timeout %s)", next.id, time.Since(killed).Round(time.Millisecond), testElection)
	if next == old {
		t.Fatal("the killed node still leads")
	}
	next = tc.writeAll(next, "c", 10, 20)

	// The old leader comes back on its files: it follows, and holds what
	// the new leader holds (its log was not closed, so by a snapshot).
	tc.boot(nodeIndex(tc, old), false)
	tc.waitSame(next, "c")
	if l := tc.leader(5 * time.Second); l != next {
		t.Fatalf("leadership moved to %s when the old leader returned", l.id)
	}
	if _, n := old.checksum("c"); n != 20 {
		t.Fatalf("the returned node holds %d documents, want 20", n)
	}
}

func TestClusterPartitionedLeaderStopsAndItsWritesAreSetAside(t *testing.T) {
	tc := startTestCluster(t, clusterOpts{nodes: 3, auto: true})
	old := tc.writeAll(tc.stableLeader(5*time.Second), "c", 0, 5)
	tc.waitSame(old, "c")
	old = tc.stableLeader(5 * time.Second) // the node to cut off leads now

	// Cut the leader off. Inside its lease it still takes a write — which
	// no other node receives.
	old.cut.Store(true)
	if err := old.write("c", 100); err != nil {
		t.Fatalf("a write inside the lease: %v", err)
	}
	// Then its lease runs out: the minority side takes no writes.
	waitE2E(t, "the cut-off leader to stop taking writes", func() bool { return old.s.writeBlocked() != nil })
	if err := old.write("c", 101); err == nil {
		t.Fatal("the minority side took a write")
	}
	next := tc.leader(10 * time.Second)
	if next == old {
		t.Fatal("the cut-off node leads")
	}
	next = tc.writeAll(next, "c", 200, 201)

	// The network heals: the old leader follows, its data becomes the new
	// leader's, and the write only it took is in the rejected-writes file.
	old.cut.Store(false)
	tc.waitSame(next, "c")
	waitE2E(t, "the rejected-writes file", func() bool { return old.s.cluster.rejected.Load() != nil })
	path := *old.s.cluster.rejected.Load()
	if !rejectedFileHolds(t, path, "doc-0100") {
		t.Fatalf("%s does not hold the cut write", path)
	}
	if got, _ := old.checksum("c"); got == "" {
		t.Fatal("no checksum")
	}
	st := old.s.replicationStatus()
	if st.Cluster == nil || st.Cluster.LastRejectedFile != path || st.Role != string(cluster.Follower) {
		t.Fatalf("status of the returned node: %+v", st)
	}
}

// rejectedFileHolds reports whether a line of the file names key.
func rejectedFileHolds(t *testing.T, path, key string) bool {
	t.Helper()
	f, err := os.Open(path) // #nosec G304 -- path under t.TempDir()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	sc := bufio.NewScanner(f)
	sc.Buffer(nil, 1<<20)
	for sc.Scan() {
		var e rejectedEntry
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
			t.Fatalf("a line of %s: %v", path, err)
		}
		if strings.Contains(e.Key, key) {
			return true
		}
	}
	return false
}

func TestClusterQuorumKeepsEveryAcknowledgedWrite(t *testing.T) {
	tc := startTestCluster(t, clusterOpts{nodes: 3, auto: true, ack: ackQuorum})
	const acked = 30
	l := tc.writeAll(tc.stableLeader(5*time.Second), "q", 0, acked)
	// Kill the leader at once: every acknowledged write is on a majority,
	// so the next leader has it.
	tc.halt(l, false)
	next := tc.stableLeader(10 * time.Second) // it must still lead when its followers are cut
	if _, n := next.checksum("q"); n != acked {
		t.Fatalf("the new leader holds %d of %d acknowledged writes", n, acked)
	}

	// With every follower cut off a write is not confirmed.
	for _, n := range tc.nodes {
		if n != next && n.up {
			n.cut.Store(true)
		}
	}
	err := next.write("q", 1000)
	if err == nil || !errors.Is(err, errQuorumTimeout) {
		t.Fatalf("a write no majority holds: %v", err)
	}
}

func TestClusterManualPromotionAndDemotion(t *testing.T) {
	tc := startTestCluster(t, clusterOpts{nodes: 2})
	first := tc.leader(5 * time.Second)
	if first.id != "n1" {
		t.Fatalf("the bootstrap node is %s", first.id)
	}
	if err := first.write("m", 1); err != nil {
		t.Fatal(err)
	}
	second := tc.nodes[1]
	tc.waitSame(first, "m")

	// Promotion through the API: the leader steps down, the follower
	// catches up and is elected.
	rec := postAdmin(second.s.handleReplicationPromote, `{}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("promote: %d %s", rec.Code, rec.Body)
	}
	if l := tc.leader(5 * time.Second); l != second {
		t.Fatalf("leader after the promotion: %s", l.id)
	}
	if err := second.write("m", 2); err != nil {
		t.Fatal(err)
	}
	tc.waitSame(second, "m")

	// Demotion: no leader until one is promoted (no automatic failover).
	if rec := postAdmin(second.s.handleReplicationDemote, ""); rec.Code != http.StatusOK {
		t.Fatalf("demote: %d %s", rec.Code, rec.Body)
	}
	if rec := postAdmin(second.s.handleReplicationDemote, ""); rec.Code != http.StatusConflict {
		t.Fatalf("demoting a follower: %d", rec.Code)
	}

	// The other node is down: only a forced promotion makes a leader.
	tc.halt(second, true)
	if rec := postAdmin(first.s.handleReplicationPromote, `{}`); rec.Code != http.StatusConflict {
		t.Fatalf("an election without a majority: %d %s", rec.Code, rec.Body)
	}
	if rec := postAdmin(first.s.handleReplicationPromote, `{"force":true}`); rec.Code != http.StatusOK {
		t.Fatalf("forced promotion: %d %s", rec.Code, rec.Body)
	}
	if err := first.write("m", 3); err != nil {
		t.Fatal(err)
	}
}

func postAdmin(h http.HandlerFunc, body string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body)))
	return rec
}

func TestClusterFollowerAnswersWritesWithTheLeader(t *testing.T) {
	tc := startTestCluster(t, clusterOpts{nodes: 3, auto: true})
	l := tc.stableLeader(5 * time.Second)
	var f *testNode
	for _, n := range tc.nodes {
		if n != l {
			f = n
		}
	}
	waitE2E(t, "the follower to know the leader", func() bool { id, _, _ := f.s.cluster.node.Leader(); return id == l.id })
	h := f.s.guardWrite(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodPost, "/v1/add", nil))
	if rec.Code != http.StatusMisdirectedRequest || rec.Header().Get("X-MDDB-Leader") != "http://"+l.id+".test" {
		t.Fatalf("follower answered %d, leader header %q", rec.Code, rec.Header().Get("X-MDDB-Leader"))
	}
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body["leaderAddr"] != "http://"+l.id+".test" || body["leaderId"] != l.id {
		t.Fatalf("body %v", body)
	}
}

func nodeIndex(tc *testCluster, n *testNode) int {
	for i, m := range tc.nodes {
		if m == n {
			return i
		}
	}
	return -1
}
