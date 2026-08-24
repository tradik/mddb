package indexqueue

import (
	"errors"
	"mddb/internal/testsync"
	"testing"

	"mddb/internal/binlog"

	bolt "go.etcd.io/bbolt"
)

// failStore is a Store whose writes always fail, to drive the worker's
// failure-counting branch.
type failStore struct{}

func (failStore) DBUpdate(func(*bolt.Tx) error) error { return errors.New("db down") }
func (failStore) IdxMetaBucket() []byte               { return []byte("idxmeta") }
func (failStore) Binlog() *binlog.Binlog              { return nil }

// waitFor delegates to the shared helper, keeping this file's call sites as
// they were (TEST-004).
//
// The loop it replaces was the third hand-rolled copy in the tree, each with
// its own interval and its own deadline — this one 2 s, which is the sort of
// number that holds on a developer's machine and not on a loaded runner.
func waitFor(t *testing.T, cond func() bool, msg string) {
	t.Helper()
	testsync.Wait(t, msg, cond)
}

// TestSetStoreWiresPersistence covers SetStore: the queue is built before the
// store exists (as in main), wired afterwards, then processes a job.
func TestSetStoreWiresPersistence(t *testing.T) {
	st, done := newTestStore(t)
	defer done()

	iq := NewIndexQueue(nil, 1) // store wired below, before any Enqueue
	defer iq.Shutdown()
	iq.SetStore(st)

	if err := iq.Enqueue(&IndexJob{Collection: "c", DocID: "d", NewMeta: map[string][]string{"k": {"v"}}}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { p, _, _, _ := iq.Stats(); return p == 1 }, "job not processed after SetStore")
}

// TestWorkerCountsFailures covers the worker's error branch: a failing store
// makes processJob error, which must increment the failed counter.
func TestWorkerCountsFailures(t *testing.T) {
	iq := NewIndexQueue(failStore{}, 1)
	defer iq.Shutdown()

	if err := iq.Enqueue(&IndexJob{Collection: "c", DocID: "d", NewMeta: map[string][]string{"k": {"v"}}}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { _, f, _, _ := iq.Stats(); return f == 1 }, "worker did not count the failure")
}
