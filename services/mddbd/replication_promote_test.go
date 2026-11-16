package main

import (
	"path/filepath"
	"testing"
)

// Manual failover today, and what GO-051's election stands on: a follower
// stopped cleanly and restarted as the leader continues the log it copied,
// under the same ID, so the other followers continue from it; one that had
// applied more than the new leader is refused as ahead and takes a snapshot.
func TestE2EAPromotedFollowerContinuesTheLog(t *testing.T) {
	useSecret(t)
	l := startE2ELeader(t, e2eLeaderOpts{dir: t.TempDir()})
	l.write(t, "blog", 0, 20)
	pdir := t.TempDir()
	promoted := startE2EFollower(t, e2eFollowerOpts{id: "promoted", dir: pdir, leader: l.addr})
	other := startE2EFollower(t, e2eFollowerOpts{id: "other", dir: t.TempDir(), leader: l.addr})
	waitE2E(t, "both followers to catch up", func() bool { return caughtUp(l, promoted) && caughtUp(l, other) })
	logID := l.s.Binlog.LogID()
	promoted.stop()
	l.stop(t)

	nl := startE2ELeader(t, e2eLeaderOpts{dir: pdir, db: "follower.db"})
	defer nl.stop(t)
	if nl.s.Binlog.LogID() != logID {
		t.Fatalf("the promoted node started log %x; the followers are in %x", nl.s.Binlog.LogID(), logID)
	}
	if pos, _ := nl.s.loadReplicationPosition(); pos.LSN != 0 {
		t.Errorf("the new leader kept its follower position %+v", pos)
	}
	other.stop()
	nl.write(t, "wiki", 0, 5)
	again := startE2EFollower(t, e2eFollowerOpts{id: "other", dir: filepath.Dir(other.s.Path), leader: nl.addr})
	defer again.stop()
	waitE2E(t, "the other follower to follow the new leader", func() bool { return caughtUp(nl, again) })
	if n := nl.snapshots.Load(); n != 0 {
		t.Errorf("the other follower took %d snapshots; it was in the new leader's log", n)
	}
	assertSameContent(t, nl, again)
}
