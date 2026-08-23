package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	proto "mddb/proto"

	bolt "go.etcd.io/bbolt"
	json "mddb/internal/jsonx"
)

// protoBatchDoc is a one-liner helper for tests that need a pointer to a
// trivially-valid BatchDocument. Avoids repeating the struct literal.
func protoBatchDoc(key string) *proto.BatchDocument {
	return &proto.BatchDocument{Key: key, Lang: "en", ContentMd: "# " + key}
}

// newBulkTestServer builds a server with the bulk_jobs bucket prepared.
// The default newTestServer skips optional buckets, so we add it here.
func newBulkTestServer(t *testing.T) (*Server, func()) {
	t.Helper()
	s, cleanup := newTestServer(t)
	if err := s.DB.Update(func(tx *bolt.Tx) error {
		_, err := tx.CreateBucketIfNotExists(bucketBulkJobs)
		return err
	}); err != nil {
		cleanup()
		t.Fatalf("create bucket: %v", err)
	}
	return s, cleanup
}

// waitForStatus polls for a job to reach one of the expected terminal statuses.
// Times out instead of hanging the test suite on a worker that never drains.
func waitForStatus(t *testing.T, m *BulkIngestManager, id string, timeout time.Duration, accept ...BulkJobStatus) *BulkIngestJob {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		job, err := m.Get(id)
		if err == nil {
			for _, s := range accept {
				if job.Status == s {
					return job
				}
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	job, _ := m.Get(id)
	t.Fatalf("job %s did not reach %v within %s (current: %+v)", id, accept, timeout, job)
	return nil
}

func TestBulkIngestManager_SubmitAndProcess(t *testing.T) {
	s, cleanup := newBulkTestServer(t)
	defer cleanup()

	m := NewBulkIngestManager(s, 8)
	m.Start()
	defer m.Stop()

	docs := []*proto.BatchDocument{
		{Key: "a", Lang: "en", ContentMd: "# Hello A"},
		{Key: "b", Lang: "en", ContentMd: "# Hello B"},
		{Key: "c", Lang: "en", ContentMd: "# Hello C"},
	}
	job, err := m.Submit("testcol", docs, "")
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if job.Status != BulkJobPending {
		t.Errorf("expected pending on submit, got %s", job.Status)
	}

	final := waitForStatus(t, m, job.ID, 2*time.Second, BulkJobCompleted, BulkJobFailed)
	if final.Total != 3 {
		t.Errorf("expected total=3, got %d", final.Total)
	}
	if final.Processed != 3 {
		t.Errorf("expected processed=3, got %d", final.Processed)
	}
	if final.Status != BulkJobCompleted {
		t.Errorf("expected completed, got %s (errors=%v)", final.Status, final.Errors)
	}
	if final.StartedAt == 0 || final.CompletedAt == 0 {
		t.Errorf("expected non-zero timestamps, got started=%d completed=%d", final.StartedAt, final.CompletedAt)
	}
}

func TestBulkIngestManager_SubmitValidation(t *testing.T) {
	s, cleanup := newBulkTestServer(t)
	defer cleanup()

	m := NewBulkIngestManager(s, 2)
	m.Start()
	defer m.Stop()

	if _, err := m.Submit("", []*proto.BatchDocument{{Key: "x"}}, ""); err == nil {
		t.Error("expected error for empty collection")
	}
	if _, err := m.Submit("c", nil, ""); err == nil {
		t.Error("expected error for empty documents")
	}
}

func TestBulkIngestManager_List(t *testing.T) {
	s, cleanup := newBulkTestServer(t)
	defer cleanup()

	m := NewBulkIngestManager(s, 8)
	m.Start()
	defer m.Stop()

	j1, _ := m.Submit("one", []*proto.BatchDocument{{Key: "a", ContentMd: "A"}}, "")
	time.Sleep(2 * time.Millisecond) // ensure distinct SubmittedAt
	j2, _ := m.Submit("two", []*proto.BatchDocument{{Key: "b", ContentMd: "B"}}, "")
	waitForStatus(t, m, j1.ID, 2*time.Second, BulkJobCompleted, BulkJobFailed)
	waitForStatus(t, m, j2.ID, 2*time.Second, BulkJobCompleted, BulkJobFailed)

	all, err := m.List("")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(all) != 2 {
		t.Errorf("expected 2 jobs, got %d", len(all))
	}
	// Sorted newest-first — j2 was submitted second.
	if all[0].ID != j2.ID {
		t.Errorf("expected newest first (j2=%s), got %s", j2.ID, all[0].ID)
	}

	filtered, _ := m.List("one")
	if len(filtered) != 1 || filtered[0].ID != j1.ID {
		t.Errorf("collection filter failed: %+v", filtered)
	}
}

func TestBulkIngestManager_CancelPendingOnly(t *testing.T) {
	s, cleanup := newBulkTestServer(t)
	defer cleanup()

	// Use a manager that never drains so the job stays pending.
	m := NewBulkIngestManager(s, 2)
	// Deliberately do NOT call Start — jobs remain queued.

	job, err := m.Submit("c", []*proto.BatchDocument{{Key: "a", ContentMd: "A"}}, "")
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if err := m.Cancel(job.ID); err != nil {
		t.Fatalf("cancel pending: %v", err)
	}
	got, _ := m.Get(job.ID)
	if got.Status != BulkJobCancelled {
		t.Errorf("expected cancelled, got %s", got.Status)
	}

	// Second cancel on already-cancelled job returns error.
	if err := m.Cancel(job.ID); err == nil {
		t.Error("expected error on re-cancel")
	}
}

func TestBulkIngestManager_QueueFull(t *testing.T) {
	s, cleanup := newBulkTestServer(t)
	defer cleanup()

	// Worker not started → queue fills up.
	m := NewBulkIngestManager(s, 1)

	_, err := m.Submit("c", []*proto.BatchDocument{{Key: "a", ContentMd: "A"}}, "")
	if err != nil {
		t.Fatalf("first submit: %v", err)
	}
	_, err = m.Submit("c", []*proto.BatchDocument{{Key: "b", ContentMd: "B"}}, "")
	if err == nil {
		t.Error("expected queue full error on second submit")
	}
}

func TestBulkIngestManager_RecoverOrphans(t *testing.T) {
	s, cleanup := newBulkTestServer(t)
	defer cleanup()

	m := NewBulkIngestManager(s, 2)

	// Plant a job record stuck in processing — simulates a server that crashed
	// with a job in flight.
	orphan := &BulkIngestJob{
		ID:         "orphan1",
		Collection: "c",
		Status:     BulkJobProcessing,
		Total:      10,
		Processed:  5,
	}
	if err := m.saveJob(orphan); err != nil {
		t.Fatalf("save orphan: %v", err)
	}

	m.recoverOrphans()

	got, err := m.Get("orphan1")
	if err != nil {
		t.Fatalf("get orphan: %v", err)
	}
	if got.Status != BulkJobFailed {
		t.Errorf("expected orphan marked failed, got %s", got.Status)
	}
	if len(got.Errors) == 0 {
		t.Error("expected failure reason recorded in errors")
	}
}

func TestBulkIngestManager_ConcurrentSubmits(t *testing.T) {
	s, cleanup := newBulkTestServer(t)
	defer cleanup()

	m := NewBulkIngestManager(s, 32)
	m.Start()
	defer m.Stop()

	const n = 8
	var wg sync.WaitGroup
	ids := make([]string, n)
	for i := 0; i < n; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			job, err := m.Submit("c", []*proto.BatchDocument{
				{Key: keyFromIndex(i), Lang: "en", ContentMd: "content"},
			}, "")
			if err == nil {
				ids[i] = job.ID
			}
		}()
	}
	wg.Wait()

	for i, id := range ids {
		if id == "" {
			continue
		}
		final := waitForStatus(t, m, id, 3*time.Second, BulkJobCompleted, BulkJobFailed)
		if final.Status != BulkJobCompleted {
			t.Errorf("job %d (%s): expected completed, got %s (errors=%v processed=%d total=%d added=%d updated=%d failed=%d)",
				i, id, final.Status, final.Errors, final.Processed, final.Total, final.Added, final.Updated, final.Failed)
		}
	}
}

func TestAppendCapped(t *testing.T) {
	var out []string
	for i := 0; i < 5; i++ {
		out = appendCapped(out, "err", 3)
	}
	if len(out) != 3 {
		t.Errorf("expected 3 entries (cap enforced), got %d", len(out))
	}
}

func TestBulkJobString(t *testing.T) {
	j := &BulkIngestJob{
		ID: "abc", Status: BulkJobCompleted,
		Total: 10, Processed: 10, Added: 7, Updated: 2, Failed: 1,
	}
	s := j.String()
	if s == "" || len(s) < 10 {
		t.Errorf("unexpected format: %q", s)
	}
}

func keyFromIndex(i int) string {
	return fmt.Sprintf("doc_%d", i)
}

func TestBulkIngestManager_CallbackFires(t *testing.T) {
	s, cleanup := newBulkTestServer(t)
	defer cleanup()

	// Spin a one-shot server that records the delivered payload so we can
	// assert the webhook body matches the final job record.
	received := make(chan *BulkIngestJob, 1)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var got BulkIngestJob
		if err := json.NewDecoder(r.Body).Decode(&got); err == nil {
			select {
			case received <- &got:
			default:
			}
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	m := NewBulkIngestManager(s, 4)
	m.Start()
	defer m.Stop()

	job, err := m.Submit("c", []*proto.BatchDocument{protoBatchDoc("a")}, ts.URL)
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	waitForStatus(t, m, job.ID, 2*time.Second, BulkJobCompleted, BulkJobFailed)

	select {
	case got := <-received:
		if got.ID != job.ID {
			t.Errorf("callback payload id=%s; want %s", got.ID, job.ID)
		}
	case <-time.After(1500 * time.Millisecond):
		t.Error("callback webhook was not fired within 1.5s")
	}
}

func TestBulkIngestManager_CallbackBadURL(t *testing.T) {
	s, cleanup := newBulkTestServer(t)
	defer cleanup()
	m := NewBulkIngestManager(s, 2)
	m.Start()
	defer m.Stop()

	// Deliberately malformed URL — fireCallback should log-and-continue, not
	// panic; job status must still reach a terminal state.
	job, err := m.Submit("c", []*proto.BatchDocument{protoBatchDoc("a")}, "http://%%%/broken")
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	final := waitForStatus(t, m, job.ID, 2*time.Second, BulkJobCompleted, BulkJobFailed)
	if final.Status != BulkJobCompleted {
		t.Errorf("expected completed despite bad callback URL, got %s", final.Status)
	}
}

// httpBulkTestServer wires up a server with BulkIngest manager so HTTP handlers
// have a populated target. The manager worker is deliberately started so
// submitted jobs actually drain during the test.
func httpBulkTestServer(t *testing.T) (*Server, func()) {
	t.Helper()
	s, cleanup := newBulkTestServer(t)
	s.BulkIngest = NewBulkIngestManager(s, 8)
	s.BulkIngest.Start()
	return s, func() {
		s.BulkIngest.Stop()
		cleanup()
	}
}
