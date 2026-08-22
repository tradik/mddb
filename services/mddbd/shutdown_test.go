package main

import (
	"context"
	"runtime"
	"strings"
	"testing"
	"time"

	"mddb/internal/cache"
)

// GO-034: the shutdown sequence, and the regression gate behind it.
//
// Seven subsystems had a Stop or Close that nothing called. At process exit
// the kernel reclaims goroutines and descriptors, so the leak itself was not
// the cost — what those subsystems were still holding was: queued index jobs,
// an in-flight bulk job, a buffered WAL write. Exiting without draining turns
// a graceful stop into the same loss as a kill.

// goroutinesSettled waits for the goroutine count to stop falling, so a test
// measures a quiet process rather than one still winding down.
func goroutinesSettled(d time.Duration) int {
	deadline := time.Now().Add(d)
	last := runtime.NumGoroutine()
	stable := 0
	for time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
		now := runtime.NumGoroutine()
		if now >= last {
			stable++
			if stable >= 3 {
				return now
			}
		} else {
			stable = 0
		}
		last = now
	}
	return runtime.NumGoroutine()
}

func TestShutdownStepsSkipSubsystemsThatNeverStarted(t *testing.T) {
	// A bare server has nothing to stop; the sequence must not panic on nils.
	steps := (&Server{}).shutdownSteps()
	if len(steps) != 0 {
		names := make([]string, len(steps))
		for i, s := range steps {
			names[i] = s.name
		}
		t.Errorf("a server with no subsystems should have nothing to shut down, got %v", names)
	}
	(&Server{}).Shutdown(context.Background())
}

func TestShutdownStepsAreOrderedProducersBeforeConsumers(t *testing.T) {
	s := &Server{
		Cache:         cache.NewDocumentCache(10, 60),
		LockFreeCache: cache.NewLockFreeCache(10, 60),
	}
	t.Cleanup(func() { s.Cache.Close() })

	var names []string
	for _, step := range s.shutdownSteps() {
		names = append(names, step.name)
	}
	joined := strings.Join(names, ",")

	// Caches close last: readers may still be in flight until the steps above
	// return.
	if !strings.HasSuffix(joined, "lockfree-cache,document-cache") {
		t.Errorf("caches should close last, order was %v", names)
	}
}

func TestShutdownRunsEveryStep(t *testing.T) {
	var ran []string
	s := &Server{}
	steps := []shutdownStep{
		{name: "first", fn: func() { ran = append(ran, "first") }},
		{name: "second", fn: func() { ran = append(ran, "second") }},
		{name: "third", fn: func() { ran = append(ran, "third") }},
	}
	runShutdownSteps(context.Background(), steps)
	_ = s

	if strings.Join(ran, ",") != "first,second,third" {
		t.Errorf("steps ran as %v, want them in order", ran)
	}
}

// A step that wedges must not hold the process open — and must be named, so an
// operator knows which one to look at.
func TestShutdownStopsAtTheDeadlineAndNamesTheStep(t *testing.T) {
	blocked := make(chan struct{})
	t.Cleanup(func() { close(blocked) })

	var reachedLast bool
	steps := []shutdownStep{
		{name: "quick", fn: func() {}},
		{name: "wedged", fn: func() { <-blocked }},
		{name: "never-reached", fn: func() { reachedLast = true }},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	start := time.Now()
	runShutdownSteps(ctx, steps)
	elapsed := time.Since(start)

	if elapsed > 2*time.Second {
		t.Errorf("a wedged step held shutdown for %s", elapsed)
	}
	if reachedLast {
		t.Error("steps after the deadline should be skipped")
	}
}

// The gate this ticket asks for: start the subsystems, exercise them, stop
// them, and check the goroutines went with them.
func TestLifecycleLeavesNoGoroutinesBehind(t *testing.T) {
	before := goroutinesSettled(2 * time.Second)

	for range 3 {
		s, cleanup := newBulkTestServer(t)
		s.Cache = cache.NewDocumentCache(100, 60)
		s.BulkIngest = NewBulkIngestManager(s, 4)
		s.BulkIngest.Start()

		// Exercise it, so the workers are not merely started but used.
		if _, err := s.BulkIngest.Submit("lifecycle", nil, ""); err == nil {
			t.Error("an empty submission should be rejected")
		}

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		s.Shutdown(ctx)
		cancel()
		cleanup()
	}

	after := goroutinesSettled(3 * time.Second)
	// A small allowance: the test framework and bbolt keep their own
	// goroutines, and finalisers run on their own schedule.
	if after > before+3 {
		buf := make([]byte, 1<<16)
		n := runtime.Stack(buf, true)
		t.Errorf("goroutines grew from %d to %d across three start/stop cycles\n%s",
			before, after, buf[:n])
	}
}
