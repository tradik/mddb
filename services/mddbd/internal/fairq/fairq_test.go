package fairq

import (
	"sync"
	"testing"
	"time"
)

func popAll(t *testing.T, q *Queue[int]) []string {
	t.Helper()
	var keys []string
	for {
		p, ok := q.TryPop()
		if !ok {
			return keys
		}
		keys = append(keys, p.Key)
	}
}

func TestTenantOf(t *testing.T) {
	for in, want := range map[string]string{
		"acme/notes": "acme", "acme/kb/a": "acme", "notes": "", "/x": "", "acme/": "", "": "",
	} {
		if got := TenantOf(in); got != want {
			t.Errorf("TenantOf(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRoundRobinAcrossKeys(t *testing.T) {
	q := New[int](100)
	for i := 0; i < 5; i++ {
		q.TryPush("noisy", i)
	}
	q.TryPush("quiet", 1)
	q.TryPush("other", 2)
	got := popAll(t, q)
	want := []string{"noisy", "quiet", "other", "noisy", "noisy", "noisy", "noisy"}
	if len(got) != len(want) {
		t.Fatalf("popped %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order = %v, want %v", got, want)
		}
	}
	if q.Len() != 0 || len(q.Depths()) != 0 {
		t.Fatalf("queue not empty: %d %v", q.Len(), q.Depths())
	}
}

func TestFIFOWithinAKeyAndWaitTime(t *testing.T) {
	q := New[int](3)
	now := time.Unix(100, 0)
	q.now = func() time.Time { return now }
	q.TryPush("a", 1)
	q.TryPush("a", 2)
	now = now.Add(2 * time.Second)
	p, _ := q.TryPop()
	if p.Value != 1 || p.Waited != 2*time.Second {
		t.Fatalf("popped %+v", p)
	}
	if d := q.Depths(); d["a"] != 1 {
		t.Fatalf("depths = %v", d)
	}
}

func TestFullKeyRefusesOnlyItself(t *testing.T) {
	q := New[int](0) // below 1 is 1
	if q.PerKey() != 1 {
		t.Fatalf("perKey = %d", q.PerKey())
	}
	if !q.TryPush("a", 1) || q.TryPush("a", 2) {
		t.Fatal("key a should hold exactly one value")
	}
	if !q.TryPush("b", 3) {
		t.Fatal("a full key a refused key b")
	}
	if q.Push("a", 4, 0, nil) {
		t.Fatal("Push without a wait queued into a full key")
	}
	if q.Push("a", 4, 10*time.Millisecond, nil) {
		t.Fatal("Push queued into a key nobody drained")
	}
	stop := make(chan struct{})
	close(stop)
	if q.Push("a", 4, time.Hour, stop) {
		t.Fatal("Push ignored stop")
	}
}

func TestPushWaitsForRoom(t *testing.T) {
	q := New[int](1)
	q.TryPush("a", 1)
	done := make(chan bool)
	go func() { done <- q.Push("a", 2, time.Minute, nil) }()
	time.Sleep(5 * time.Millisecond)
	q.TryPush("b", 9) // a change that frees nothing: the pusher waits on
	if _, ok := q.TryPop(); !ok {
		t.Fatal("nothing to pop")
	}
	if !<-done {
		t.Fatal("Push gave up although room was made")
	}
}

func TestPopWaitsAndStops(t *testing.T) {
	q := New[int](1)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		p, ok := q.Pop(nil)
		if !ok || p.Value != 7 {
			t.Errorf("Pop = %+v %v", p, ok)
		}
	}()
	time.Sleep(5 * time.Millisecond)
	q.TryPush("a", 7)
	wg.Wait()

	stop := make(chan struct{})
	close(stop)
	if _, ok := q.Pop(stop); ok {
		t.Fatal("Pop on an empty, stopped queue returned a value")
	}
	q.TryPush("a", 8)
	if p, ok := q.Pop(stop); !ok || p.Value != 8 {
		t.Fatal("Pop did not take a queued value")
	}
}

func TestConcurrentProducersAndConsumers(t *testing.T) {
	q := New[int](8)
	stop := make(chan struct{})
	var consumed sync.WaitGroup
	got := make(chan int, 400)
	for i := 0; i < 4; i++ {
		go func() {
			for {
				p, ok := q.Pop(stop)
				if !ok {
					return
				}
				got <- p.Value
				consumed.Done()
			}
		}()
	}
	var produced sync.WaitGroup
	for k := 0; k < 4; k++ {
		produced.Add(1)
		go func(key string) {
			defer produced.Done()
			for i := 0; i < 100; i++ {
				consumed.Add(1)
				if !q.Push(key, i, time.Minute, nil) {
					t.Error("push failed")
					consumed.Done()
				}
			}
		}(string(rune('a' + k)))
	}
	produced.Wait()
	consumed.Wait()
	close(stop)
	if len(got) != 400 {
		t.Fatalf("consumed %d of 400", len(got))
	}
}
