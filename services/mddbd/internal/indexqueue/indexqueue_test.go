package indexqueue

import (
	"context"
	"errors"
	"mddb/internal/binlog"
	"mddb/internal/storage"
	"mddb/internal/testsync"
	"os"
	"testing"
	"time"

	bolt "go.etcd.io/bbolt"
)

// TestIndexQueue_EnqueueFallbackWhenFull — GO-010: a full queue must NOT drop
// the job; Enqueue indexes it synchronously (fallback) so it is never lost.
// processedCount reports how many jobs the queue has finished, which is the
// condition every assertion in this file is really waiting on. Polling it
// replaced fixed sleeps of 100–500 ms: those passed here and failed on a
// loaded runner, and they cost their full duration even when the queue had
// already drained (TEST-004).
func processedCount(iq *IndexQueue) func() int {
	return func() int {
		processed, _, _, _ := iq.Stats()
		return testsync.CountOf(processed)
	}
}

func TestIndexQueue_EnqueueFallbackWhenFull(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()

	// No workers drain this queue, and the buffer holds exactly one job, so
	// the second Enqueue is guaranteed to hit the full-queue fallback path.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	iq := &IndexQueue{
		store:  s,
		queue:  make(chan *IndexJob, 1),
		ctx:    ctx,
		cancel: cancel,
	}
	iq.queue <- &IndexJob{Collection: "c", DocID: "filler"} // buffer now full

	job := &IndexJob{
		Collection: "c",
		DocID:      "doc1",
		NewMeta:    map[string][]string{"tag": {"x"}},
	}
	if err := iq.Enqueue(job); err != nil {
		t.Fatalf("Enqueue fallback returned error: %v", err)
	}

	_, _, fallbacks, _ := iq.Stats()
	if fallbacks != 1 {
		t.Errorf("fallbacks = %d, want 1", fallbacks)
	}

	// The fallback must have written the meta index entry synchronously.
	found := false
	_ = s.DB.View(func(tx *bolt.Tx) error {
		bIdx := tx.Bucket([]byte("idxmeta"))
		key := append(storage.MetaKeyPrefix("c", "tag", "x"), []byte("doc1")...)
		found = bIdx.Get(key) != nil
		return nil
	})
	if !found {
		t.Error("full-queue job was not indexed synchronously — job lost (GO-010)")
	}
}

// TestIndexQueue_EnqueueFallbackError — when the synchronous fallback fails to
// write (here: the DB is closed), Enqueue surfaces the error and counts it as a
// failure rather than swallowing it.
func TestIndexQueue_EnqueueFallbackError(t *testing.T) {
	s, cleanup := newTestStore(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	iq := &IndexQueue{store: s, queue: make(chan *IndexJob, 1), ctx: ctx, cancel: cancel}
	iq.queue <- &IndexJob{Collection: "c", DocID: "filler"} // full

	cleanup() // close the DB so the fallback's write transaction fails

	err := iq.Enqueue(&IndexJob{Collection: "c", DocID: "doc1", NewMeta: map[string][]string{"k": {"v"}}})
	if err == nil {
		t.Fatal("expected an error from the fallback when the DB is closed")
	}
	if _, failed, fallbacks, _ := iq.Stats(); failed != 1 || fallbacks != 1 {
		t.Errorf("failed=%d fallbacks=%d, want 1 and 1", failed, fallbacks)
	}
}

// TestIndexQueue_EnqueueAfterShutdown — Enqueue returns ErrQueueClosed (and does
// not panic) once the queue has been shut down.
func TestIndexQueue_EnqueueAfterShutdown(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()

	iq := NewIndexQueue(s, 2)
	iq.Shutdown()

	err := iq.Enqueue(&IndexJob{Collection: "c", DocID: "d", NewMeta: map[string][]string{"k": {"v"}}})
	if !errors.Is(err, ErrQueueClosed) {
		t.Errorf("Enqueue after shutdown: got %v, want ErrQueueClosed", err)
	}
}

// testStore is a stub Store backed by a temp BoltDB, used to exercise the queue
// without the Server god-object. DB is exported so tests can verify the
// metadata-index entries the queue writes.
type testStore struct {
	DB *bolt.DB
}

func (s *testStore) DBUpdate(fn func(*bolt.Tx) error) error { return s.DB.Update(fn) }
func (s *testStore) IdxMetaBucket() []byte                  { return []byte("idxmeta") }
func (s *testStore) Binlog() *binlog.Binlog                 { return nil }

// newTestStore creates a testStore with a temp BoltDB and the required buckets.
func newTestStore(t *testing.T) (*testStore, func()) {
	t.Helper()

	f, err := os.CreateTemp("", "iq_test_*.db")
	if err != nil {
		t.Fatal(err)
	}
	_ = f.Close()

	db, err := bolt.Open(f.Name(), 0600, &bolt.Options{Timeout: 1 * time.Second})
	if err != nil {
		_ = os.Remove(f.Name())
		t.Fatal(err)
	}

	// Create required buckets
	err = db.Update(func(tx *bolt.Tx) error {
		for _, name := range []string{"docs", "idxmeta", "rev", "bykey"} {
			if _, err := tx.CreateBucketIfNotExists([]byte(name)); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		_ = db.Close()
		_ = os.Remove(f.Name())
		t.Fatal(err)
	}

	cleanup := func() {
		_ = db.Close()
		_ = os.Remove(f.Name())
	}

	return &testStore{DB: db}, cleanup
}

// --- NewIndexQueue ---

func TestNewIndexQueue_DefaultWorkers(t *testing.T) {
	srv, cleanup := newTestStore(t)
	defer cleanup()

	iq := NewIndexQueue(srv, 0) // 0 => default 4 workers
	defer iq.Shutdown()

	if iq.workers != 4 {
		t.Errorf("expected default 4 workers, got %d", iq.workers)
	}
	if iq.store != srv {
		t.Error("server reference mismatch")
	}
}

func TestNewIndexQueue_NegativeWorkers(t *testing.T) {
	srv, cleanup := newTestStore(t)
	defer cleanup()

	iq := NewIndexQueue(srv, -1) // negative => default 4 workers
	defer iq.Shutdown()

	if iq.workers != 4 {
		t.Errorf("expected default 4 workers for negative input, got %d", iq.workers)
	}
}

func TestNewIndexQueue_CustomWorkers(t *testing.T) {
	srv, cleanup := newTestStore(t)
	defer cleanup()

	iq := NewIndexQueue(srv, 8)
	defer iq.Shutdown()

	if iq.workers != 8 {
		t.Errorf("expected 8 workers, got %d", iq.workers)
	}
}

func TestNewIndexQueue_SingleWorker(t *testing.T) {
	srv, cleanup := newTestStore(t)
	defer cleanup()

	iq := NewIndexQueue(srv, 1)
	defer iq.Shutdown()

	if iq.workers != 1 {
		t.Errorf("expected 1 worker, got %d", iq.workers)
	}
}

// --- Stats ---

func TestIndexQueue_Stats_Initial(t *testing.T) {
	srv, cleanup := newTestStore(t)
	defer cleanup()

	iq := NewIndexQueue(srv, 2)
	defer iq.Shutdown()

	processed, failed, _, qLen := iq.Stats()
	if processed != 0 {
		t.Errorf("initial processed should be 0, got %d", processed)
	}
	if failed != 0 {
		t.Errorf("initial failed should be 0, got %d", failed)
	}
	if qLen != 0 {
		t.Errorf("initial queue length should be 0, got %d", qLen)
	}
}

// --- Enqueue and processJob ---

func TestIndexQueue_EnqueueAndProcess(t *testing.T) {
	srv, cleanup := newTestStore(t)
	defer cleanup()

	iq := NewIndexQueue(srv, 2)
	defer iq.Shutdown()

	job := &IndexJob{
		Collection: "blog",
		DocID:      "doc1",
		OldMeta:    nil,
		NewMeta:    map[string][]string{"tag": {"go", "db"}, "author": {"alice"}},
	}

	_ = iq.Enqueue(job)

	testsync.WaitForCount(t, "the job to be processed", 1, processedCount(iq))

	processed, failed, _, _ := iq.Stats()
	if processed != 1 {
		t.Errorf("expected 1 processed, got %d", processed)
	}
	if failed != 0 {
		t.Errorf("expected 0 failed, got %d", failed)
	}

	// Verify indices were created in BoltDB
	err := srv.DB.View(func(tx *bolt.Tx) error {
		bIdx := tx.Bucket([]byte("idxmeta"))

		// Check meta|blog|tag|go|doc1
		key1 := storage.MetaKeyPrefix("blog", "tag", "go")
		key1 = append(key1, []byte("doc1")...)
		v1 := bIdx.Get(key1)
		if v1 == nil {
			t.Error("expected index entry for tag=go, got nil")
		}

		// Check meta|blog|tag|db|doc1
		key2 := storage.MetaKeyPrefix("blog", "tag", "db")
		key2 = append(key2, []byte("doc1")...)
		v2 := bIdx.Get(key2)
		if v2 == nil {
			t.Error("expected index entry for tag=db, got nil")
		}

		// Check meta|blog|author|alice|doc1
		key3 := storage.MetaKeyPrefix("blog", "author", "alice")
		key3 = append(key3, []byte("doc1")...)
		v3 := bIdx.Get(key3)
		if v3 == nil {
			t.Error("expected index entry for author=alice, got nil")
		}

		return nil
	})
	if err != nil {
		t.Fatalf("DB.View failed: %v", err)
	}
}

func TestIndexQueue_EnqueueUpdateMeta(t *testing.T) {
	srv, cleanup := newTestStore(t)
	defer cleanup()

	iq := NewIndexQueue(srv, 1)
	defer iq.Shutdown()

	// First: add initial metadata
	job1 := &IndexJob{
		Collection: "blog",
		DocID:      "doc1",
		OldMeta:    nil,
		NewMeta:    map[string][]string{"tag": {"go"}},
	}
	_ = iq.Enqueue(job1)
	testsync.WaitForCount(t, "the first job to be processed", 1, processedCount(iq))

	// Second: update metadata (remove "go", add "python")
	job2 := &IndexJob{
		Collection: "blog",
		DocID:      "doc1",
		OldMeta:    map[string][]string{"tag": {"go"}},
		NewMeta:    map[string][]string{"tag": {"python"}},
	}
	_ = iq.Enqueue(job2)
	testsync.WaitForCount(t, "the second job to be processed", 2, processedCount(iq))

	processed, failed, _, _ := iq.Stats()
	if processed != 2 {
		t.Errorf("expected 2 processed, got %d", processed)
	}
	if failed != 0 {
		t.Errorf("expected 0 failed, got %d", failed)
	}

	// Verify old index removed, new index created
	err := srv.DB.View(func(tx *bolt.Tx) error {
		bIdx := tx.Bucket([]byte("idxmeta"))

		// Old index should be gone
		oldKey := storage.MetaKeyPrefix("blog", "tag", "go")
		oldKey = append(oldKey, []byte("doc1")...)
		if v := bIdx.Get(oldKey); v != nil {
			t.Error("old index entry for tag=go should be deleted")
		}

		// New index should exist
		newKey := storage.MetaKeyPrefix("blog", "tag", "python")
		newKey = append(newKey, []byte("doc1")...)
		if v := bIdx.Get(newKey); v == nil {
			t.Error("new index entry for tag=python should exist")
		}

		return nil
	})
	if err != nil {
		t.Fatalf("DB.View failed: %v", err)
	}
}

func TestIndexQueue_ProcessJob_DeleteOldMeta(t *testing.T) {
	srv, cleanup := newTestStore(t)
	defer cleanup()

	iq := NewIndexQueue(srv, 1)
	defer iq.Shutdown()

	// Manually add an index entry first
	err := srv.DB.Update(func(tx *bolt.Tx) error {
		bIdx := tx.Bucket([]byte("idxmeta"))
		key := storage.MetaKeyPrefix("blog", "cat", "tech")
		key = append(key, []byte("d1")...)
		return bIdx.Put(key, []byte("1"))
	})
	if err != nil {
		t.Fatal(err)
	}

	// Process job that removes old meta and adds new
	job := &IndexJob{
		Collection: "blog",
		DocID:      "d1",
		OldMeta:    map[string][]string{"cat": {"tech"}},
		NewMeta:    map[string][]string{"cat": {"science"}},
	}
	_ = iq.Enqueue(job)
	testsync.WaitForCount(t, "the job to be indexed", 1, processedCount(iq))

	err = srv.DB.View(func(tx *bolt.Tx) error {
		bIdx := tx.Bucket([]byte("idxmeta"))

		oldKey := storage.MetaKeyPrefix("blog", "cat", "tech")
		oldKey = append(oldKey, []byte("d1")...)
		if v := bIdx.Get(oldKey); v != nil {
			t.Error("old meta index should be deleted")
		}

		newKey := storage.MetaKeyPrefix("blog", "cat", "science")
		newKey = append(newKey, []byte("d1")...)
		if v := bIdx.Get(newKey); v == nil {
			t.Error("new meta index should exist")
		}

		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestIndexQueue_ProcessJob_NilNewMeta(t *testing.T) {
	srv, cleanup := newTestStore(t)
	defer cleanup()

	iq := NewIndexQueue(srv, 1)
	defer iq.Shutdown()

	// Add an index entry
	err := srv.DB.Update(func(tx *bolt.Tx) error {
		bIdx := tx.Bucket([]byte("idxmeta"))
		key := storage.MetaKeyPrefix("blog", "tag", "go")
		key = append(key, []byte("d1")...)
		return bIdx.Put(key, []byte("1"))
	})
	if err != nil {
		t.Fatal(err)
	}

	// Job with nil NewMeta should only delete old indices
	job := &IndexJob{
		Collection: "blog",
		DocID:      "d1",
		OldMeta:    map[string][]string{"tag": {"go"}},
		NewMeta:    nil,
	}
	_ = iq.Enqueue(job)
	testsync.WaitForCount(t, "the job to be indexed", 1, processedCount(iq))

	err = srv.DB.View(func(tx *bolt.Tx) error {
		bIdx := tx.Bucket([]byte("idxmeta"))
		key := storage.MetaKeyPrefix("blog", "tag", "go")
		key = append(key, []byte("d1")...)
		if v := bIdx.Get(key); v != nil {
			t.Error("old meta index should be deleted")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestIndexQueue_ProcessJob_NilOldMeta(t *testing.T) {
	srv, cleanup := newTestStore(t)
	defer cleanup()

	iq := NewIndexQueue(srv, 1)
	defer iq.Shutdown()

	// Job with nil OldMeta should only create new indices
	job := &IndexJob{
		Collection: "docs",
		DocID:      "d2",
		OldMeta:    nil,
		NewMeta:    map[string][]string{"type": {"article"}},
	}
	_ = iq.Enqueue(job)
	testsync.WaitForCount(t, "the job to be indexed", 1, processedCount(iq))

	err := srv.DB.View(func(tx *bolt.Tx) error {
		bIdx := tx.Bucket([]byte("idxmeta"))
		key := storage.MetaKeyPrefix("docs", "type", "article")
		key = append(key, []byte("d2")...)
		if v := bIdx.Get(key); v == nil {
			t.Error("new meta index should exist")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestIndexQueue_MultipleJobs(t *testing.T) {
	srv, cleanup := newTestStore(t)
	defer cleanup()

	iq := NewIndexQueue(srv, 4)
	defer iq.Shutdown()

	// Enqueue multiple jobs
	for i := 0; i < 20; i++ {
		job := &IndexJob{
			Collection: "blog",
			DocID:      "doc" + string(rune('A'+i)),
			OldMeta:    nil,
			NewMeta:    map[string][]string{"idx": {"val"}},
		}
		_ = iq.Enqueue(job)
	}

	testsync.WaitForCount(t, "all twenty jobs to be processed", 20, processedCount(iq))

	processed, failed, _, _ := iq.Stats()
	if processed != 20 {
		t.Errorf("expected 20 processed, got %d", processed)
	}
	if failed != 0 {
		t.Errorf("expected 0 failed, got %d", failed)
	}
}

// --- Shutdown ---

func TestIndexQueue_Shutdown(t *testing.T) {
	srv, cleanup := newTestStore(t)
	defer cleanup()

	iq := NewIndexQueue(srv, 2)

	// Enqueue a job before shutdown
	_ = iq.Enqueue(&IndexJob{
		Collection: "c",
		DocID:      "d",
		NewMeta:    map[string][]string{"k": {"v"}},
	})

	// Shutdown should complete without panic or hanging
	done := make(chan struct{})
	go func() {
		iq.Shutdown()
		close(done)
	}()

	select {
	case <-done:
		// OK
	case <-time.After(5 * time.Second):
		t.Fatal("Shutdown timed out")
	}
}

func TestIndexQueue_ShutdownPreventsNewEnqueue(t *testing.T) {
	srv, cleanup := newTestStore(t)
	defer cleanup()

	iq := NewIndexQueue(srv, 1)
	iq.Shutdown()

	// After shutdown, channel is closed so Enqueue will panic with "send on closed channel".
	// Verify the queue was shut down properly by checking stats.
	processed, failed, _, queueLen := iq.Stats()
	if queueLen != 0 {
		t.Errorf("expected empty queue after shutdown, got %d", queueLen)
	}
	_ = processed
	_ = failed
}

// --- processJob directly ---

func TestProcessJob_EmptyJob(t *testing.T) {
	srv, cleanup := newTestStore(t)
	defer cleanup()

	iq := NewIndexQueue(srv, 1)
	defer iq.Shutdown()

	// Process job with no old or new meta
	job := &IndexJob{
		Collection: "blog",
		DocID:      "d1",
		OldMeta:    nil,
		NewMeta:    nil,
	}

	err := iq.processJob(job)
	if err != nil {
		t.Fatalf("processJob with empty job failed: %v", err)
	}
}

func TestProcessJob_MultipleValues(t *testing.T) {
	srv, cleanup := newTestStore(t)
	defer cleanup()

	iq := NewIndexQueue(srv, 1)
	defer iq.Shutdown()

	job := &IndexJob{
		Collection: "c",
		DocID:      "d1",
		OldMeta:    nil,
		NewMeta: map[string][]string{
			"tags":   {"a", "b", "c"},
			"author": {"alice", "bob"},
		},
	}

	err := iq.processJob(job)
	if err != nil {
		t.Fatalf("processJob failed: %v", err)
	}

	// Verify all 5 index entries created
	count := 0
	err = srv.DB.View(func(tx *bolt.Tx) error {
		bIdx := tx.Bucket([]byte("idxmeta"))
		c := bIdx.Cursor()
		for k, _ := c.First(); k != nil; k, _ = c.Next() {
			count++
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if count != 5 {
		t.Errorf("expected 5 index entries, got %d", count)
	}
}

// --- IndexJob struct ---

func TestIndexJob_Fields(t *testing.T) {
	job := &IndexJob{
		Collection: "blog",
		DocID:      "post-1",
		OldMeta:    map[string][]string{"old": {"val"}},
		NewMeta:    map[string][]string{"new": {"val"}},
	}

	if job.Collection != "blog" {
		t.Errorf("Collection: got %q", job.Collection)
	}
	if job.DocID != "post-1" {
		t.Errorf("DocID: got %q", job.DocID)
	}
	if len(job.OldMeta) != 1 {
		t.Errorf("OldMeta length: got %d", len(job.OldMeta))
	}
	if len(job.NewMeta) != 1 {
		t.Errorf("NewMeta length: got %d", len(job.NewMeta))
	}
}

func TestIndexQueue_StatsAfterProcessing(t *testing.T) {
	srv, cleanup := newTestStore(t)
	defer cleanup()

	iq := NewIndexQueue(srv, 2)
	defer iq.Shutdown()

	// Enqueue 5 jobs
	for i := 0; i < 5; i++ {
		_ = iq.Enqueue(&IndexJob{
			Collection: "c",
			DocID:      string(rune('a' + i)),
			NewMeta:    map[string][]string{"k": {"v"}},
		})
	}

	testsync.WaitForCount(t, "all five jobs to be processed", 5, processedCount(iq))

	processed, failed, _, qLen := iq.Stats()
	if processed != 5 {
		t.Errorf("expected 5 processed, got %d", processed)
	}
	if failed != 0 {
		t.Errorf("expected 0 failed, got %d", failed)
	}
	if qLen != 0 {
		t.Errorf("expected 0 queue length, got %d", qLen)
	}
}
