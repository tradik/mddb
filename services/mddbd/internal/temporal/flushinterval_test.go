package temporal

import (
	"os"
	"testing"
	"time"
)

// TestMain shortens the flush interval for the whole package.
//
// The production value is 500 ms, and every test here records an event and
// then waits for it to land — so the suite spent most of its time waiting for
// a ticker whose period is not what any of these tests are about. The batching
// behaviour itself is unaffected: the same code path runs, just sooner.
func TestMain(m *testing.M) {
	flushInterval = 5 * time.Millisecond
	os.Exit(m.Run())
}
