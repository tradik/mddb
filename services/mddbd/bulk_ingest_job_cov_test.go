package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"mddb/internal/metrics"
	proto "mddb/proto"

	bolt "go.etcd.io/bbolt"
)

// #381: bulk_ingest_job.go's coverage swung between 88% and 90% because the
// Stop drain ran only when a job happened to be queued when Stop came. These
// tests reach every branch without the worker: they call process, Stop and
// the status helpers directly, so nothing depends on timing.

// TestBulkIngestManager_StopCancelsQueuedJobs: a job still queued when Stop
// runs ends cancelled, never stuck in pending.
func TestBulkIngestManager_StopCancelsQueuedJobs(t *testing.T) {
	s, cleanup := newBulkTestServer(t)
	defer cleanup()
	m := NewBulkIngestManager(s, 0) // 0 takes the default buffer
	if cap(m.queue) != 64 {
		t.Fatalf("default queue cap %d, want 64", cap(m.queue))
	}
	job, err := m.Submit("c", []*proto.BatchDocument{protoBatchDoc("a")}, "")
	if err != nil {
		t.Fatal(err)
	}
	m.Stop() // no worker: the job is still in the queue
	got, err := m.Get(job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != BulkJobCancelled || got.CompletedAt == 0 || len(got.Errors) != 1 || got.Errors[0] != "server shutdown" {
		t.Fatalf("queued job at Stop: %+v", got)
	}
}

// TestBulkIngestManager_ProcessEdges runs process directly: a job that is gone
// or cancelled is skipped, a chunk whose every document fails marks the job
// failed, and a finished job is counted in the metrics.
func TestBulkIngestManager_ProcessEdges(t *testing.T) {
	s, cleanup := newBulkTestServer(t)
	defer cleanup()
	s.Metrics = metrics.NewMetrics(true, &serverMetricsStats{s: s})
	m := NewBulkIngestManager(s, 4)

	// A job with no record, and a cancelled one, are skipped untouched.
	m.process(bulkWorkItem{jobID: "missing", collection: "c", docs: []*proto.BatchDocument{protoBatchDoc("a")}})
	cancelled, err := m.Submit("c", []*proto.BatchDocument{protoBatchDoc("a")}, "")
	if err != nil {
		t.Fatal(err)
	}
	<-m.queue
	if err := m.Cancel(cancelled.ID); err != nil {
		t.Fatal(err)
	}
	m.process(bulkWorkItem{jobID: cancelled.ID, collection: "c", docs: []*proto.BatchDocument{protoBatchDoc("a")}})
	if got, _ := m.Get(cancelled.ID); got.Status != BulkJobCancelled || got.StartedAt != 0 {
		t.Fatalf("a cancelled job ran: %+v", got)
	}

	// Every document invalid: the batch errors and the job fails.
	bad := []*proto.BatchDocument{{Lang: "en", ContentMd: "no key"}}
	job, err := m.Submit("c", bad, "")
	if err != nil {
		t.Fatal(err)
	}
	m.process(<-m.queue)
	got, err := m.Get(job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != BulkJobFailed || got.Failed != 1 || got.Processed != 1 || len(got.Errors) == 0 {
		t.Fatalf("all-invalid job: %+v", got)
	}

	// One valid, one invalid: the batch succeeds and records the one error.
	// The callback URL only proves process hands the job to fireCallback;
	// a malformed one returns at once (TestBulkIngestManager_FireCallback
	// covers delivery).
	mixed, err := m.Submit("c", []*proto.BatchDocument{protoBatchDoc("ok"), bad[0]}, "http://%%%/broken")
	if err != nil {
		t.Fatal(err)
	}
	m.process(<-m.queue)
	if got, _ := m.Get(mixed.ID); got.Status != BulkJobCompleted || got.Added != 1 || got.Failed != 1 || len(got.Errors) != 1 {
		t.Fatalf("mixed job: %+v", got)
	}
	rec := httptest.NewRecorder()
	s.Metrics.HandleMetrics(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if !strings.Contains(rec.Body.String(), `bulk_ingest_job`) {
		t.Fatal("the finished job was not counted in the metrics")
	}
}

// TestBulkIngestManager_StatusHelpers covers the transitions the worker
// reaches only on shutdown, restart or a failing database.
func TestBulkIngestManager_StatusHelpers(t *testing.T) {
	s, cleanup := newBulkTestServer(t)
	defer cleanup()
	m := NewBulkIngestManager(s, 4)

	// An unknown job: transitionStatus reports it, markTerminal swallows it.
	if err := m.transitionStatus("missing", BulkJobFailed, "", 0); err == nil {
		t.Fatal("transition of an unknown job")
	}
	m.markTerminal("missing", BulkJobCancelled, "server shutdown")
	if err := m.Cancel("missing"); err == nil {
		t.Fatal("cancel of an unknown job")
	}

	// An explicit completion time is kept as given.
	job, err := m.Submit("c", []*proto.BatchDocument{protoBatchDoc("a")}, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := m.transitionStatus(job.ID, BulkJobCompleted, "", 1234); err != nil {
		t.Fatal(err)
	}
	if got, _ := m.Get(job.ID); got.CompletedAt != 1234 || len(got.Errors) != 0 {
		t.Fatalf("completedAt: %+v", got)
	}

	// A record that is not JSON is skipped by List and by recoverOrphans.
	if err := s.DB.Update(func(tx *bolt.Tx) error {
		return tx.Bucket(bucketBulkJobs).Put([]byte("garbage"), []byte("{not json"))
	}); err != nil {
		t.Fatal(err)
	}
	m.recoverOrphans()
	jobs, err := m.List("")
	if err != nil || len(jobs) != 1 || jobs[0].ID != job.ID {
		t.Fatalf("list with a garbage record: %v %+v", err, jobs)
	}

	// A read-only database: reads work, every write fails.
	path := s.DB.Path()
	if err := s.DB.Close(); err != nil {
		t.Fatal(err)
	}
	ro, err := bolt.Open(path, 0o600, &bolt.Options{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	s.DB = ro
	if _, err := m.Submit("c", []*proto.BatchDocument{protoBatchDoc("b")}, ""); err == nil {
		t.Fatal("submit stored a job in a read-only database")
	}
	if err := m.transitionStatus(job.ID, BulkJobFailed, "x", 0); err == nil {
		t.Fatal("transition saved to a read-only database")
	}

	// A closed database: List reports it.
	if err := ro.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := m.List(""); err == nil {
		t.Fatal("list on a closed database")
	}
}

// TestBulkIngestManager_FireCallback calls fireCallback directly for each
// outcome. process starts it in a goroutine that can outlive a test, so
// which of its lines ran used to depend on timing (#381).
func TestBulkIngestManager_FireCallback(t *testing.T) {
	s, cleanup := newBulkTestServer(t)
	defer cleanup()
	m := NewBulkIngestManager(s, 1)

	delivered := make(chan string, 1)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		delivered <- r.Header.Get("X-MDDB-Event")
	}))
	defer ts.Close()
	m.fireCallback(&BulkIngestJob{ID: "ok", Status: BulkJobCompleted, CallbackURL: ts.URL})
	if got := <-delivered; got != "bulk_ingest.completed" {
		t.Fatalf("event header %q", got)
	}

	// A malformed URL and an unreachable one are logged, not fatal.
	m.fireCallback(&BulkIngestJob{ID: "bad", CallbackURL: "http://%%%/broken"})
	m.fireCallback(&BulkIngestJob{ID: "refused", CallbackURL: "http://127.0.0.1:1/hook"})
}

// TestBulkIngestManager_ListOrder: newest first by submittedAt, ties broken
// by ID, other collections left out. The records are planted, so the order does not depend on whether two
// submits fell in the same wall-clock second (#381).
func TestBulkIngestManager_ListOrder(t *testing.T) {
	s, cleanup := newBulkTestServer(t)
	defer cleanup()
	m := NewBulkIngestManager(s, 1)
	for _, j := range []*BulkIngestJob{
		{ID: "bulk_1_a", Collection: "c", Status: BulkJobCompleted, SubmittedAt: 100},
		{ID: "bulk_3_c", Collection: "c", Status: BulkJobCompleted, SubmittedAt: 200},
		{ID: "bulk_2_b", Collection: "c", Status: BulkJobCompleted, SubmittedAt: 200},
		{ID: "bulk_4_d", Collection: "other", Status: BulkJobCompleted, SubmittedAt: 300},
	} {
		if err := m.saveJob(j); err != nil {
			t.Fatal(err)
		}
	}
	jobs, err := m.List("c")
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, j := range jobs {
		ids = append(ids, j.ID)
	}
	if got := strings.Join(ids, ","); got != "bulk_3_c,bulk_2_b,bulk_1_a" {
		t.Fatalf("order %s", got)
	}
}
