// Package testsync waits for asynchronous work to finish, instead of guessing
// how long it takes (TEST-004).
//
// Tests of the queue, the audit batcher and the temporal histograms used to
// enqueue work, `time.Sleep` a fixed 100–700 ms, and assert. That passes on a
// developer's machine and fails on a loaded CI runner, which is the worst
// shape a test can have: it is green where it is cheap to run and red where
// it matters. Eight of the twenty-seven patches in a downstream Windows fork
// were nothing but this, on our tests.
//
// Polling fixes both ends. A condition that is already true returns
// immediately, so the suite gets *faster* than a fixed sleep on a fast
// machine; a condition that needs longer gets the full timeout before anyone
// calls it a failure. That is why the timeouts here are generous — they cost
// nothing until something is actually wrong.
package testsync

import (
	"math"
	"testing"
	"time"
)

// DefaultTimeout is long enough for a loaded runner and short enough that a
// genuinely stuck test still fails inside a CI step's patience.
const DefaultTimeout = 10 * time.Second

// pollInterval is how often cond is re-evaluated. Small enough that a fast
// condition is not held up by the poller, large enough not to spin a core.
const pollInterval = 5 * time.Millisecond

// WaitFor blocks until cond returns true, or fails the test at the deadline.
//
// `what` completes the sentence "timed out waiting for …", so write it as the
// thing being awaited: "the job to be indexed", not "job" or "condition".
func WaitFor(t testing.TB, timeout time.Duration, what string, cond func() bool) {
	t.Helper()

	if cond() {
		return
	}

	deadline := time.Now().Add(timeout)
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for range ticker.C {
		if cond() {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out after %s waiting for %s", timeout, what)
		}
	}
}

// Wait is WaitFor with DefaultTimeout, which is what nearly every caller wants.
func Wait(t testing.TB, what string, cond func() bool) {
	t.Helper()
	WaitFor(t, DefaultTimeout, what, cond)
}

// WaitForCount waits until count() reaches want, and reports what it actually
// reached — "wanted 3, reached 2" localises a failure that a bare boolean
// cannot.
func WaitForCount(t testing.TB, what string, want int, count func() int) {
	t.Helper()
	WaitForCountTimeout(t, DefaultTimeout, what, want, count)
}

// WaitForCountTimeout is WaitForCount with the deadline supplied.
//
// Exported so a test of a failure path can use a short one. Without it, every
// test that asserts this helper gives up would wait out DefaultTimeout — which
// would make the helper written to speed the suite up the slowest thing in it.
func WaitForCountTimeout(t testing.TB, timeout time.Duration, what string, want int, count func() int) {
	t.Helper()

	deadline := time.Now().Add(timeout)
	for {
		last := count()
		if last >= want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out after %s waiting for %s: wanted %d, reached %d",
				timeout, what, want, last)
		}
		time.Sleep(pollInterval)
	}
}

// CountOf adapts an unsigned counter for WaitForCount, saturating instead of
// wrapping.
//
// The stats these tests poll are uint64 and the helper takes an int, and a
// bare conversion is both a lint finding (gosec G115) and, on a 32-bit build,
// a real one: a wrapped counter reads as a queue that went backwards. Neither
// matters at the values a test reaches, which is exactly why it is worth
// getting right once here rather than annotating it away at each call site.
func CountOf(v uint64) int {
	if v > math.MaxInt {
		return math.MaxInt
	}
	return int(v)
}
