package main

import (
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	bolt "go.etcd.io/bbolt"
)

func TestCovHGainFlushKeepsCountsWhenAWriteFails(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	g := NewGainRecorder(func() *bolt.DB { return s.DB })
	g.now = func() time.Time { return time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC) }
	g.add(GainCounters{TokensReturned: 5})

	// A day key too long for bbolt fails the write; the whole flush rolls
	// back and every count is kept for the next try.
	badDay := strings.Repeat("d", bolt.MaxKeySize)
	g.mu.Lock()
	g.pending[badDay] = &GainCounters{TokensAvoided: 7}
	g.mu.Unlock()
	if err := g.Flush(); err == nil {
		t.Fatal("an oversized key must fail the flush")
	}
	g.mu.Lock()
	day := g.pending["2026-10-29"]
	kept := day != nil && day.TokensReturned == 5 && g.pending[badDay].TokensAvoided == 7
	delete(g.pending, badDay)
	g.mu.Unlock()
	if !kept {
		t.Fatal("counts were lost by a failed flush")
	}
	_ = s.DB.View(func(tx *bolt.Tx) error {
		if b := tx.Bucket(bucketStats); b != nil && b.Get(gainKey("2026-10-29")) != nil {
			t.Error("a failed flush stored a day")
		}
		return nil
	})

	// The next flush stores what was kept.
	if err := g.Flush(); err != nil {
		t.Fatal(err)
	}
	_ = s.DB.View(func(tx *bolt.Tx) error {
		if c := readGainDay(tx.Bucket(bucketStats), "2026-10-29"); c.TokensReturned != 5 {
			t.Errorf("stored day: %+v", c)
		}
		return nil
	})
}

// TestGain_StopWaitsForAFlushInProgress is #381's root cause. Flush takes the
// pending counts before it writes and puts them back when the write fails. A
// Stop whose flush ran in between saw nothing pending and returned nil, so
// "stop on a closed database reports it" failed whenever the ticker was
// mid-flush. Flushes now run one at a time: Stop's flush waits, then sees the
// counts the failed flush put back.
func TestGain_StopWaitsForAFlushInProgress(t *testing.T) {
	closed, err := bolt.Open(filepath.Join(t.TempDir(), "c.db"), 0o600, nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = closed.Close()
	inWrite, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	g := NewGainRecorder(func() *bolt.DB {
		if calls.Add(1) == 1 { // the first flush holds its write open
			close(inWrite)
			<-release
		}
		return closed
	})
	g.Search(gainKind{Type: "fts"})
	first := make(chan error, 1)
	go func() { first <- g.Flush() }()
	<-inWrite // the first flush has taken the counts and is writing them

	// Without the lock, a flush now finds nothing pending and returns nil.
	if g.flushMu.TryLock() {
		g.flushMu.Unlock()
		t.Fatal("a flush is writing, yet another flush could start")
	}
	stopped := make(chan error, 1)
	go func() { stopped <- g.Stop() }()
	close(release)
	if err := <-first; err == nil {
		t.Fatal("the first flush wrote to a closed database")
	}
	if err := <-stopped; err == nil {
		t.Fatal("Stop lost the counts a failed flush put back")
	}
	if n := calls.Load(); n != 2 {
		t.Fatalf("db() called %d times, want 2 (one per flush)", n)
	}
}
