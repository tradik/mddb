package audit

import (
	"os"
	"testing"
	"time"
)

// TestMain shortens the webhook retry schedule for the whole package.
//
// The production schedule is 0/1s/5s/15s. Two tests here exhaust it to assert
// that a permanent failure is counted, which cost 21 real seconds each — most
// of this package's runtime, and none of its coverage. The same code path runs
// against the same number of attempts, only sooner.
func TestMain(m *testing.M) {
	webhookRetryBackoffs = []time.Duration{0, time.Millisecond, 2 * time.Millisecond, 3 * time.Millisecond}
	os.Exit(m.Run())
}
