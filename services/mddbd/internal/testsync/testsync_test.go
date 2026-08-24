package testsync

import (
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// The property that makes polling worth the change: a condition already true
// costs nothing, so replacing a fixed sleep makes the suite faster on a fast
// machine as well as reliable on a slow one.
func TestWaitReturnsImmediatelyWhenTheConditionHolds(t *testing.T) {
	start := time.Now()
	Wait(t, "a condition that is already true", func() bool { return true })

	if elapsed := time.Since(start); elapsed > 2*time.Millisecond {
		t.Errorf("an already-true condition took %s", elapsed)
	}
}

func TestWaitForReturnsAsSoonAsTheConditionBecomesTrue(t *testing.T) {
	var done atomic.Bool
	go func() {
		time.Sleep(20 * time.Millisecond)
		done.Store(true)
	}()

	start := time.Now()
	WaitFor(t, time.Second, "the goroutine to finish", done.Load)
	elapsed := time.Since(start)

	if elapsed < 15*time.Millisecond {
		t.Errorf("returned in %s, before the work could have finished", elapsed)
	}
	// The point of polling: it does not wait out the whole timeout.
	if elapsed > 300*time.Millisecond {
		t.Errorf("took %s for work that finished in 20ms", elapsed)
	}
}

func TestWaitForCountsUp(t *testing.T) {
	var n atomic.Int64
	go func() {
		for i := 0; i < 3; i++ {
			time.Sleep(5 * time.Millisecond)
			n.Add(1)
		}
	}()

	WaitForCount(t, "three items", 3, func() int { return int(n.Load()) })
}

// The failure paths run under a fake TB: calling them on the real one would
// fail this test rather than exercise them.
type fakeTB struct {
	testing.TB
	failed bool
	msg    string
}

func (f *fakeTB) Helper() {}
func (f *fakeTB) Fatalf(format string, args ...any) {
	f.failed = true
	f.msg = format
	panic(sentinel{})
}

type sentinel struct{}

func runExpectingFatal(t *testing.T, body func(tb testing.TB)) *fakeTB {
	t.Helper()
	tb := &fakeTB{}
	func() {
		defer func() {
			if r := recover(); r != nil {
				if _, ok := r.(sentinel); !ok {
					panic(r)
				}
			}
		}()
		body(tb)
	}()
	return tb
}

func TestWaitForFailsAtTheDeadline(t *testing.T) {
	tb := runExpectingFatal(t, func(tb testing.TB) {
		WaitFor(tb, 20*time.Millisecond, "something that never happens", func() bool { return false })
	})

	if !tb.failed {
		t.Fatal("a condition that never holds should fail the test rather than hang")
	}
}

func TestWaitForCountReportsWhatItReached(t *testing.T) {
	// A bare boolean cannot say how far it got, which is the difference
	// between "the queue is stuck" and "the queue is slow". A short timeout,
	// because a test of the giving-up path must not wait out the real one.
	tb := &fakeTB{}
	func() {
		defer func() {
			if r := recover(); r != nil {
				if _, ok := r.(sentinel); !ok {
					panic(r)
				}
			}
		}()
		WaitForCountTimeout(tb, 20*time.Millisecond, "three items", 3, func() int { return 2 })
	}()

	if !tb.failed {
		t.Fatal("a count that never reaches its target should fail the test")
	}
	if !strings.Contains(tb.msg, "reached %d") {
		t.Errorf("the message does not report the count reached: %q", tb.msg)
	}
}
