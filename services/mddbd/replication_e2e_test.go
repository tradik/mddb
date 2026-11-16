package main

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mddb/internal/binlog"
	"mddb/internal/storage"
)

// End-to-end replication (GO-050, TEST-005). Each test runs in the storage
// layout the suite runs in; CI runs the suite in both.

// A snapshot in the per-collection layout carries every collection file;
// it used to be refused there.
func TestE2ESnapshotInThePerCollectionLayout(t *testing.T) {
	useSecret(t)
	t.Setenv("MDDB_STORAGE_LAYOUT", "per-collection")
	l := startE2ELeader(t, e2eLeaderOpts{dir: t.TempDir()})
	defer l.stop(t)
	l.write(t, "blog", 0, 20)
	l.write(t, "wiki", 0, 20)
	if err := l.s.Binlog.Reset(); err != nil { // the history is gone: only a snapshot will do
		t.Fatal(err)
	}
	fdir := t.TempDir()
	f := startE2EFollower(t, e2eFollowerOpts{dir: fdir, leader: l.addr})
	defer f.stop()
	l.write(t, "wiki", 20, 5)
	waitE2E(t, "the follower to catch up", func() bool { return caughtUp(l, f) })
	if n := l.snapshots.Load(); n != 1 {
		t.Errorf("snapshots: %d, want 1", n)
	}
	for _, c := range []string{"blog", "wiki"} {
		if _, err := os.Stat(filepath.Join(collectionDir(f.s.Path), collectionFileName(c))); err != nil {
			t.Errorf("the follower has no file for %s: %v", c, err)
		}
	}
	assertSameContent(t, l, f)
}

// A follower stopped while the leader writes resumes where its database
// says it is: every entry once, none missing, and no snapshot.
func TestE2EFollowerRestartResumesWithoutSnapshot(t *testing.T) {
	useSecret(t)
	l := startE2ELeader(t, e2eLeaderOpts{dir: t.TempDir()})
	defer l.stop(t)
	fdir := t.TempDir()
	f := startE2EFollower(t, e2eFollowerOpts{dir: fdir, leader: l.addr})

	done := make(chan struct{})
	go func() {
		defer close(done)
		l.write(t, "blog", 0, 60)
	}()
	waitE2E(t, "the follower to apply part of the stream", func() bool { return f.rc.applier.LastAppliedLSN() > 20 })
	f.stop() // mid-stream: the leader is still writing
	<-done
	l.write(t, "wiki", 0, 20)

	f = startE2EFollower(t, e2eFollowerOpts{dir: fdir, leader: l.addr})
	defer f.stop()
	waitE2E(t, "the restarted follower to catch up", func() bool { return caughtUp(l, f) })

	if n := l.snapshots.Load(); n != 0 {
		t.Errorf("the follower took %d snapshots; a restart resumes from its position", n)
	}
	// The follower's own log took every entry under the leader's LSN, in
	// order: a duplicate or a gap would have started it again.
	log := f.rc.log.Load()
	if log.OldestLSN() != 1 || log.CurrentLSN() != l.s.Binlog.CurrentLSN() || log.LogID() != l.s.Binlog.LogID() {
		t.Errorf("follower log holds %d..%d of log %x; leader is at %d of log %x",
			log.OldestLSN(), log.CurrentLSN(), log.LogID(), l.s.Binlog.CurrentLSN(), l.s.Binlog.LogID())
	}
	assertSameContent(t, l, f)
	st := f.s.replicationStatus()
	if !st.Healthy || !st.Connected || st.LeaderAddr != l.addr || st.CurrentLSN != l.s.Binlog.CurrentLSN() {
		t.Errorf("follower status: %+v", st)
	}
}

// An entry the follower cannot apply stops it: unhealthy, a snapshot, and
// then the leader's data — never the entry skipped.
func TestE2EApplyErrorResynchronises(t *testing.T) {
	useSecret(t)
	l := startE2ELeader(t, e2eLeaderOpts{dir: t.TempDir()})
	defer l.stop(t)
	l.write(t, "blog", 0, 10)
	f := startE2EFollower(t, e2eFollowerOpts{dir: t.TempDir(), leader: l.addr})
	defer f.stop()
	waitE2E(t, "the follower to catch up", func() bool { return caughtUp(l, f) })

	gate := make(chan struct{})
	l.gate.Store(&gate)
	// An entry type this build does not know cannot be applied.
	if err := l.s.Binlog.Append(&binlog.BinlogEntry{Type: 99, BucketName: "docs", Key: []byte("x")}); err != nil {
		t.Fatal(err)
	}
	l.write(t, "blog", 10, 5)
	waitE2E(t, "the follower to ask for a snapshot", func() bool { return l.snapshots.Load() == 1 })
	st := f.s.replicationStatus()
	if st.Healthy || !strings.Contains(st.LastError, "unknown binlog entry type") {
		t.Errorf("follower stopped by a bad entry reports healthy=%v lastError=%q", st.Healthy, st.LastError)
	}
	close(gate)

	waitE2E(t, "the follower to resynchronise", func() bool { return caughtUp(l, f) && f.rc.IsHealthy() })
	assertSameContent(t, l, f)
}

// Retention keeps the leader's log under its limit while the follower keeps
// up, and a follower away for longer than the log holds takes a snapshot.
func TestE2ERetentionAndATooOldFollower(t *testing.T) {
	useSecret(t)
	cfg := binlog.BinlogConfig{MaxSize: 32 << 10, HardMaxSize: 64 << 10, RetentionInterval: -1}
	l := startE2ELeader(t, e2eLeaderOpts{dir: t.TempDir(), log: cfg})
	defer l.stop(t)
	fdir := t.TempDir()
	f := startE2EFollower(t, e2eFollowerOpts{dir: fdir, leader: l.addr})

	for i := 0; i < 6; i++ {
		l.write(t, "blog", i*40, 40)
		waitE2E(t, "the follower to confirm", func() bool {
			return caughtUp(l, f) && l.rs.followerViews()[0].ConfirmedLSN == l.s.Binlog.CurrentLSN()
		})
		if _, err := l.s.Binlog.EnforceRetention(); err != nil {
			t.Fatal(err)
		}
		if size := l.s.Binlog.Stats().FileSize; size > cfg.MaxSize {
			t.Fatalf("binlog is %d bytes with a current follower; the limit is %d", size, cfg.MaxSize)
		}
	}
	if l.s.Binlog.OldestLSN() <= 1 {
		t.Fatal("retention dropped nothing")
	}
	f.stop()

	// Away while the log passes its hard limit: its entries go.
	l.write(t, "wiki", 0, 400)
	res, err := l.s.Binlog.EnforceRetention()
	if err != nil || !res.Forced {
		t.Fatalf("retention past the hard limit: %+v, %v", res, err)
	}
	if size := l.s.Binlog.Stats().FileSize; size > cfg.HardMaxSize {
		t.Fatalf("binlog is %d bytes; the hard limit is %d", size, cfg.HardMaxSize)
	}

	f = startE2EFollower(t, e2eFollowerOpts{dir: fdir, leader: l.addr})
	defer f.stop()
	waitE2E(t, "the returning follower to catch up", func() bool { return caughtUp(l, f) })
	if n := l.snapshots.Load(); n != 1 {
		t.Errorf("the returning follower took %d snapshots, want 1", n)
	}
	assertSameContent(t, l, f)
}

// What test_replication.sh checked by hand, against two followers: writes
// and deletes arrive, a follower answers metadata searches and refuses
// writes, and both sides report their role and position.
func TestE2ETwoFollowersServeReadsAndRefuseWrites(t *testing.T) {
	useSecret(t)
	l := startE2ELeader(t, e2eLeaderOpts{dir: t.TempDir()})
	defer l.stop(t)
	for i := 1; i <= 10; i++ {
		addTestDoc(t, l.s, "test", fmt.Sprintf("doc-%d", i), "en", fmt.Sprintf("# Document %d", i),
			map[string][]string{"idx": {fmt.Sprint(i)}})
	}
	if err := l.s.deleteDocumentInternal("test", "doc-1", "en"); err != nil {
		t.Fatal(err)
	}
	fs := []*e2eFollower{
		startE2EFollower(t, e2eFollowerOpts{id: "follower-1", dir: t.TempDir(), leader: l.addr}),
		startE2EFollower(t, e2eFollowerOpts{id: "follower-2", dir: t.TempDir(), leader: l.addr}),
	}
	for _, f := range fs {
		defer f.stop()
		checkFollowerServes(t, l, f)
	}
	checkLeaderSeesFollowersCaughtUp(t, l)
	if got := nodeContent(t, fs[0].s)["bykey/"+string(storage.ByKeyKey("test", "doc-1", "en"))]; got != "" {
		t.Error("a document deleted on the leader is still on the follower")
	}
}

// checkFollowerServes checks a follower holds the leader's data, answers a
// metadata search, refuses a write and reports its role and position.
func checkFollowerServes(t *testing.T, l *e2eLeader, f *e2eFollower) {
	t.Helper()
	waitE2E(t, f.s.NodeID+" to catch up", func() bool { return caughtUp(l, f) })
	assertSameContent(t, l, f)
	rec := doRequest(t, f.s.handleSearch, map[string]any{"collection": "test", "filterMeta": map[string][]string{"idx": {"3"}}})
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "doc-3") {
		t.Errorf("%s metadata search: %d %s", f.s.NodeID, rec.Code, rec.Body.String())
	}
	rec = doRequest(t, f.s.guardWrite(f.s.handleAdd), map[string]any{"collection": "test", "key": "no", "lang": "en", "contentMd": "x"})
	if rec.Code != http.StatusForbidden {
		t.Errorf("%s accepted a write: %d", f.s.NodeID, rec.Code)
	}
	if st := f.s.replicationStatus(); st.Role != "follower" || st.CurrentLSN != l.s.Binlog.CurrentLSN() {
		t.Errorf("%s status: %+v", f.s.NodeID, st)
	}
}

// checkLeaderSeesFollowersCaughtUp waits for both followers' acknowledgements
// and checks how the leader reports them.
func checkLeaderSeesFollowersCaughtUp(t *testing.T, l *e2eLeader) {
	t.Helper()
	waitE2E(t, "both followers to confirm", func() bool {
		st := l.s.replicationStatus()
		return len(st.Followers) == 2 && st.Followers[0].ConfirmedLSN == st.CurrentLSN && st.Followers[1].ConfirmedLSN == st.CurrentLSN
	})
	st := l.s.replicationStatus()
	if st.Role != "leader" || st.CurrentLSN == 0 || !st.Healthy {
		t.Errorf("leader status: %+v", st)
	}
	for _, fi := range st.Followers {
		if fi.LagMs != 0 || fi.LagEntries != 0 || fi.Status != "healthy" || !fi.Connected {
			t.Errorf("caught-up follower shown as %+v", fi)
		}
	}
}
