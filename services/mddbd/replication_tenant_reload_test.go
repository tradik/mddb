package main

import (
	"context"
	"testing"
	"time"

	"mddb/internal/binlog"

	bolt "go.etcd.io/bbolt"
)

// A tenant limit set on the leader is enforced by a running follower at
// once: the follower wrote the tenants bucket from the log but answered
// from the configurations it loaded at start until it was restarted.
func TestE2EFollowerEnforcesATenantLimitSetOnTheLeader(t *testing.T) {
	useSecret(t)
	l := startE2ELeader(t, e2eLeaderOpts{dir: t.TempDir(), log: binlog.BinlogConfig{RetentionInterval: -1}})
	defer l.stop(t)
	l.s.Tenants = NewTenantManager(l.s.DB)
	if err := l.s.Tenants.LoadAll(); err != nil {
		t.Fatal(err)
	}
	l.s.Tenants.SetBinlog(l.s.Binlog)
	l.write(t, "c", 0, 3)

	frozen := time.Unix(1_700_000_000, 0)
	f := startE2EFollower(t, e2eFollowerOpts{dir: t.TempDir(), leader: l.addr, setup: func(s *Server) {
		s.Tenants = NewTenantManager(s.DB)
		s.Tenants.FollowDB(func() *bolt.DB { return s.DB }) // a snapshot swaps s.DB
		if err := s.Tenants.LoadAll(); err != nil {
			t.Fatal(err)
		}
		s.tenantLimiter().now = func() time.Time { return frozen }
	}})
	defer f.stop()
	waitE2E(t, "the follower to catch up", func() bool { return caughtUp(l, f) })

	read := func() error {
		_, release, err := f.s.admitRequest(context.WithValue(context.Background(), authContextKey, tina), PermRead)
		if err == nil {
			release()
		}
		return err
	}
	for range 3 {
		if err := read(); err != nil {
			t.Fatalf("a read refused before any limit: %v", err)
		}
	}

	setLimits(t, l.s, "t1", TenantLimits{ReadPerSecond: 1})
	waitE2E(t, "the limit to reach the follower", func() bool { return caughtUp(l, f) })
	if err := read(); err != nil {
		t.Fatalf("the first read under the limit: %v", err)
	}
	if te := asTenantLimitError(read()); te == nil || te.Kind != limitReadRate || te.Tenant != "t1" {
		t.Fatal("the follower did not enforce the leader's new read limit")
	}
}
