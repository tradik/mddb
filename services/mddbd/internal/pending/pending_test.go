package pending

import (
	"context"
	"sort"
	"sync/atomic"
	"testing"
	"time"
)

func TestTracker_CountsAndOldest(t *testing.T) {
	tr := New()
	clock := time.Unix(1000, 0)
	tr.now = func() time.Time { return clock }
	tr.Add("a", "d1")
	clock = clock.Add(time.Second)
	tr.Add("a", "d1") // a second job for the same document
	tr.Add("a", "d2")
	tr.Add("b", "x")

	st := tr.Stats("a")
	if st.Pending != 3 || !st.OldestPendingAt.Equal(time.Unix(1000, 0)) {
		t.Fatalf("stats a: %+v", st)
	}
	if all := tr.Stats(""); all.Pending != 4 {
		t.Fatalf("all: %+v", all)
	}
	if !tr.IsPending("a", "d1") || tr.IsPending("a", "nope") {
		t.Fatal("IsPending")
	}
	docs := tr.PendingDocs("a")
	sort.Strings(docs)
	if len(docs) != 2 || docs[0] != "d1" {
		t.Fatalf("PendingDocs: %v", docs)
	}

	tr.Done("a", "d1", false)
	if !tr.IsPending("a", "d1") {
		t.Fatal("one of two jobs for d1 is still pending")
	}
	tr.Done("a", "d1", true)
	tr.Done("a", "d2", false)
	tr.Done("a", "ghost", true) // Done without Add only counts
	st = tr.Stats("a")
	if st.Pending != 0 || st.Failed != 2 || st.Completed != 2 || !st.OldestPendingAt.IsZero() {
		t.Fatalf("after done: %+v", st)
	}
	if all := tr.Stats(""); all.Failed != 2 || all.Pending != 1 {
		t.Fatalf("all after: %+v", all)
	}
}

func TestTracker_DrainedCallback(t *testing.T) {
	tr := New()
	var drained atomic.Int32
	var last atomic.Value
	tr.OnDrained(func(c string) { drained.Add(1); last.Store(c) })
	tr.Add("a", "d1")
	tr.Add("a", "d2")
	tr.Done("a", "d1", false)
	if drained.Load() != 0 {
		t.Fatal("not drained yet")
	}
	tr.Done("a", "d2", false)
	if drained.Load() != 1 || last.Load() != "a" {
		t.Fatalf("drained %d %v", drained.Load(), last.Load())
	}
	tr.Done("a", "d3", false) // counter only: does not drain again
	if drained.Load() != 1 {
		t.Fatal("a Done without work must not fire drained")
	}
}

func TestTracker_Wait(t *testing.T) {
	tr := New()
	if !tr.Wait(context.Background(), "a", nil) {
		t.Fatal("nothing pending returns at once")
	}
	tr.Add("a", "d1")
	tr.Add("a", "d2")

	// Waiting on one document returns when that document is done.
	done := make(chan bool)
	go func() { done <- tr.Wait(context.Background(), "a", func(id string) bool { return id == "d1" }) }()
	time.Sleep(10 * time.Millisecond)
	tr.Done("a", "d1", false)
	if !<-done {
		t.Fatal("wait for d1")
	}

	// Waiting on the collection times out while d2 is pending.
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if tr.Wait(ctx, "a", nil) {
		t.Fatal("d2 still pending: wait must time out")
	}
	if !tr.Wait(context.Background(), "a", func(id string) bool { return id == "other" }) {
		t.Fatal("no matching document pending")
	}
}

func TestTracker_NilIsInert(t *testing.T) {
	var tr *Tracker
	tr.OnDrained(func(string) {})
	tr.Add("a", "d")
	tr.Done("a", "d", true)
	if tr.Stats("a").Pending != 0 || tr.IsPending("a", "d") || tr.PendingDocs("a") != nil || !tr.Wait(context.Background(), "a", nil) {
		t.Fatal("nil tracker must be inert")
	}
}
