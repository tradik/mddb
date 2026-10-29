package main

import (
	"strings"
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
