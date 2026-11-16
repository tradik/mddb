package main

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"testing"
	"time"

	json "mddb/internal/jsonx"
	"mddb/internal/testsync"
	vec "mddb/internal/vector"

	bolt "go.etcd.io/bbolt"
)

// GO-054: the background side — the embedding queue parks instead of
// dropping, tenants share it in turn, budgets add up per tenant, and
// reindexes wait for a slot.

// overflowWorker is a worker with one queue slot per tenant, no patience and
// quick rechecks, not started.
func overflowWorker(t *testing.T) *EmbeddingWorker {
	t.Helper()
	t.Setenv("MDDB_EMBEDDING_QUEUE_WAIT", "0")
	old := parkOverflowRecheck
	parkOverflowRecheck = 5 * time.Millisecond
	t.Cleanup(func() { parkOverflowRecheck = old })
	return NewEmbeddingWorker(&mockEmbedding{dims: 3, model: "m"}, covAVectorStore(t, true), vec.NewVectorIndex(), 1)
}

func TestFullEmbeddingQueueParksInsteadOfDropping(t *testing.T) {
	w := overflowWorker(t)
	var mu sync.Mutex
	seen := map[string]int{}
	w.SetObserver(func(tenant string, _ time.Duration) {
		mu.Lock()
		seen[tenant]++
		mu.Unlock()
	})
	for _, id := range []string{"a", "b", "c"} {
		if !w.Enqueue(EmbeddingJob{Collection: "acme/c", DocID: id}) {
			t.Fatalf("job %s was abandoned", id)
		}
	}
	// One slot per tenant: another tenant still has room.
	if !w.Enqueue(EmbeddingJob{Collection: "other/c", DocID: "x"}) {
		t.Fatal("another tenant's job was abandoned")
	}
	if d := w.TenantDepths(); d["acme"] != 1 || d["other"] != 1 {
		t.Fatalf("depths = %v", d)
	}
	if p := w.ParkedDepths(); p["acme"] != 2 {
		t.Fatalf("parked = %v", p)
	}
	if _, _, dropped, _ := w.QueueStats(); dropped != 0 {
		t.Fatalf("dropped = %d", dropped)
	}
	w.Start(1)
	testsync.Wait(t, "parked jobs to run", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return seen["acme"] == 3 && seen["other"] == 1
	})
	w.Stop()
	if st := w.Pending().Stats(""); st.Pending != 0 || st.Failed != 0 {
		t.Fatalf("pending stats = %+v", st)
	}
}

func TestParkLimitDropsAndStopDrops(t *testing.T) {
	t.Setenv("MDDB_EMBEDDING_PARK_MAX", "1")
	w := overflowWorker(t)
	_ = w.Enqueue(EmbeddingJob{Collection: "c", DocID: "queued"})
	if !w.Enqueue(EmbeddingJob{Collection: "c", DocID: "parked"}) {
		t.Fatal("the first overflow should park")
	}
	if w.Enqueue(EmbeddingJob{Collection: "c", DocID: "dropped"}) {
		t.Fatal("past MDDB_EMBEDDING_PARK_MAX a job is dropped")
	}
	w.Stop()
	if w.Enqueue(EmbeddingJob{Collection: "c", DocID: "late"}) {
		t.Fatal("a stopped worker took a job")
	}
	if _, _, dropped, _ := w.QueueStats(); dropped != 2 {
		t.Fatalf("dropped = %d, want 2", dropped)
	}
}

// Content that chunks into nothing (only blank lines) has nothing to embed.
func TestBlankContentEmbedsNothing(t *testing.T) {
	w := overflowWorker(t)
	if got := w.processChunks(EmbeddingJob{Collection: "c", DocID: "d", ContentMD: "\n\n   \n"}); got != jobDone {
		t.Fatalf("outcome = %v", got)
	}
	w.Stop()
}

func TestParkRecheckMovesEarlier(t *testing.T) {
	w := overflowWorker(t)
	w.parkMu.Lock()
	w.scheduleLocked(time.Hour)
	w.scheduleLocked(time.Millisecond) // sooner: the timer is moved
	w.scheduleLocked(time.Hour)        // later: left alone
	due := w.parkDue
	w.parkMu.Unlock()
	if time.Until(due) > time.Second {
		t.Fatalf("the recheck stayed an hour away")
	}
	if minPositive(0, 3) != 3 || minPositive(2, 3) != 2 {
		t.Fatal("minPositive")
	}
	w.Stop()
}

func TestBudgetParkedJobWaitsForItsBudgetNotRoom(t *testing.T) {
	w := overflowWorker(t)
	limits := &budgetLimits{q: map[string]*CollectionQuota{"c": {EmbeddingCallsPerDay: 1}}}
	w.budget = newTestBudget(limits)
	w.budget.spend("c", "x")
	w.parkMu.Lock()
	w.parked = []EmbeddingJob{{Collection: "c", DocID: "budget"}, {Collection: "d", DocID: "room"}}
	w.parkMu.Unlock()
	w.queue.TryPush("", EmbeddingJob{Collection: "d", DocID: "filler"}) // no room for "d"
	w.releaseParked()
	if p := w.ParkedDepths(); p[""] != 2 {
		t.Fatalf("parked = %v, want both still parked", p)
	}
	w.Stop()
}

func TestTenantEmbeddingBudget(t *testing.T) {
	limits := &budgetLimits{q: map[string]*CollectionQuota{"acme/a": {EmbeddingCallsPerDay: 100}}}
	b := newTestBudget(limits)
	tq := &TenantQuota{EmbeddingCallsPerDay: 2, EmbeddingTokensPerDay: 1000}
	b.tenantOf = func(c string) (string, *TenantQuota) {
		if t := TenantOfCollection(c); t == "acme" {
			return t, tq
		}
		return "", nil
	}
	b.spend("acme/a", "x")
	b.spend("acme/b", "y") // another collection of the tenant: the same budget
	b.spend("plain", "z")  // outside any tenant, without a budget: not counted
	qe := b.exhausted("acme/c")
	if qe == nil || qe.Tenant != "acme" || qe.Collection != "acme/c" || qe.Resource != quotaEmbeddingCalls || qe.RetryAfter <= 0 {
		t.Fatalf("tenant budget: %+v", qe)
	}
	if got := b.today(tenantBudgetKey("acme")); got.calls != 2 {
		t.Fatalf("tenant spend = %+v", got)
	}
	if b.today("acme/a").calls != 1 {
		t.Fatal("the collection's own budget was not counted")
	}
	tq.EmbeddingCallsPerDay = 0
	tq.EmbeddingTokensPerDay = 1
	if qe := b.exhausted("acme/c"); qe == nil || qe.Resource != quotaEmbeddingTokens {
		t.Fatalf("tokens: %+v", qe)
	}
	if b.exhausted("plain") != nil {
		t.Fatal("a collection outside the tenant was refused")
	}
	tq.EmbeddingTokensPerDay = 100
	if b.exhausted("acme/c") != nil {
		t.Fatal("refused with budget left")
	}
}

func TestReindexSlotsQueueTheJob(t *testing.T) {
	t.Setenv("MDDB_REINDEX_MAX_CONCURRENT", "1")
	s := reindexServer(t)
	s.Embedding = &mockEmbedding{dims: 3, model: "m"}
	ctx, release, err := s.reindexSlot(context.Background(), "docs", nil)
	if err != nil {
		t.Fatal(err)
	}
	// Held already: reindexVectors with this context does not wait for itself.
	if _, err := s.reindexVectors(ctx, "docs", true, nil); err != nil {
		t.Fatalf("reindex holding the slot: %v", err)
	}
	rec := doRequest(t, s.handleVectorReindex, map[string]any{"collection": "docs", "force": true, "async": true})
	var job vectorReindexJob
	_ = json.Unmarshal(rec.Body.Bytes(), &job)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("start: %d %s", rec.Code, rec.Body.String())
	}
	testsync.Wait(t, "the job to be queued", func() bool {
		j, _ := s.reindexJobs.get(job.ID)
		return j.Status == reindexQueued
	})
	// A queued job is still active: a second one for the collection is refused.
	if _, err := s.reindexJobs.start("docs", false); !errors.Is(err, errReindexRunning) {
		t.Fatalf("second job: %v", err)
	}
	// A synchronous reindex waits as long as its client does.
	short, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if _, err := s.reindexVectors(short, "docs", true, nil); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("sync reindex without a slot: %v", err)
	}
	release()
	testsync.Wait(t, "the job to finish", func() bool {
		j, _ := s.reindexJobs.get(job.ID)
		return j.Status == "completed"
	})
}

func TestTenantReindexSlot(t *testing.T) {
	t.Setenv("MDDB_TENANT_MAX_CONCURRENT_REINDEXES", "1")
	t.Setenv("MDDB_REINDEX_MAX_CONCURRENT", "1")
	s := reindexServer(t)
	_, release, err := s.reindexSlot(context.Background(), "acme/a", nil)
	if err != nil {
		t.Fatal(err)
	}
	if n := s.tenantLimiter().inUse("r|acme"); n != 1 {
		t.Fatalf("tenant slots in use = %d", n)
	}
	done, cancel := context.WithCancel(context.Background())
	cancel()
	waited := false
	if _, _, err := s.reindexSlot(done, "acme/b", func() { waited = true }); !errors.Is(err, context.Canceled) || !waited {
		t.Fatalf("tenant at its limit: %v (waited %v)", err, waited)
	}
	// Another tenant has its own slot, but the server's one is taken: the
	// tenant slot it took is given back.
	if _, _, err := s.reindexSlot(done, "other/a", nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("server at its limit: %v", err)
	}
	if n := s.tenantLimiter().inUse("r|other"); n != 0 {
		t.Fatalf("the tenant slot leaked: %d", n)
	}
	release()
}

func TestTenantManagerStoresAndReloads(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	t.Cleanup(cleanup)
	tm := NewTenantManager(s.DB)
	tm.FollowDB(func() *bolt.DB { return s.DB })
	tm.SetBinlog(nil)
	if err := tm.LoadAll(); err != nil {
		t.Fatal(err)
	}
	var nilTM *TenantManager
	if _, ok := nilTM.Get("x"); ok || nilTM.Names() != nil {
		t.Fatal("a nil manager has configurations")
	}
	if err := tm.Set("acme", TenantConfig{Quota: &TenantQuota{MaxDocuments: 5}}); err != nil {
		t.Fatal(err)
	}
	_ = s.DB.Update(func(tx *bolt.Tx) error { return tx.Bucket(bucketTenants).Put([]byte("broken"), []byte("{")) })
	fresh := NewTenantManager(s.DB)
	if err := fresh.LoadAll(); err != nil {
		t.Fatal(err)
	}
	if cfg, ok := fresh.Get("acme"); !ok || cfg.Quota.MaxDocuments != 5 {
		t.Fatalf("reloaded = %+v", cfg)
	}
	if names := fresh.Names(); len(names) != 1 {
		t.Fatalf("names = %v (a corrupt entry is skipped)", names)
	}
	// An empty configuration removes the tenant's.
	if err := tm.Set("acme", TenantConfig{}); err != nil {
		t.Fatal(err)
	}
	if _, ok := tm.Get("acme"); ok {
		t.Fatal("still configured")
	}
	if err := tm.Delete("never"); err != nil {
		t.Fatal(err)
	}
}
