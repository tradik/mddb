package main

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	vec "mddb/internal/vector"
)

// GO-054 / TEST-005: the noisy neighbour. Tenant t1 bulk-ingests and
// reindexes while tenant t2 searches. What must hold, and is asserted:
//
//   - t2's background job waits behind at most one of t1's per worker, not
//     behind t1's whole backlog (fair queues);
//   - t1 never holds more search slots than its limit, and t2 is never
//     refused while t1 is at it (per-tenant search concurrency);
//   - t1's bursts are refused as t1's, never as t2's (per-tenant limits).
//
// t2's search latency with and without the neighbour is measured and logged
// (p50, p95, ratio), but not asserted: on a shared CI runner the neighbour
// and the scheduler compete for the same cores, and a timing bound would
// fail for reasons unrelated to isolation.

func TestNoisyNeighbourQueuesAreFair(t *testing.T) {
	t.Setenv("MDDB_EMBEDDING_QUEUE_WAIT", "0")
	w := NewEmbeddingWorker(&mockEmbedding{dims: 3, model: "m"}, covAVectorStore(t, true), vec.NewVectorIndex(), EmbeddingQueueSize())
	var mu sync.Mutex
	var order []string
	w.SetObserver(func(tenant string, _ time.Duration) {
		mu.Lock()
		order = append(order, tenant)
		mu.Unlock()
	})
	const backlog = 300
	for i := 0; i < backlog; i++ {
		w.Enqueue(EmbeddingJob{Collection: "t1/bulk", DocID: fmt.Sprintf("d%d", i), ContentMD: "bulk document"})
	}
	w.Enqueue(EmbeddingJob{Collection: "t2/docs", DocID: "mine", ContentMD: "my document"})
	w.Start(2)
	waitUntil(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(order) == backlog+1
	})
	w.Stop()
	pos := indexOf(order, "t2")
	t.Logf("embedding queue: t2's job ran %d of %d (FIFO would have run it last)", pos+1, len(order))
	if pos < 0 || pos > 3 {
		t.Fatalf("t2's job ran at position %d behind t1's backlog of %d", pos, backlog)
	}
}

func TestNoisyNeighbourSearchSlots(t *testing.T) {
	t.Setenv("MDDB_SEARCH_MAX_CONCURRENT", "4")
	t.Setenv("MDDB_TENANT_QUEUE_TIMEOUT_MS", "5")
	s := limitServer(t)
	s.SearchLimiter = newSearchLimiter()
	setLimits(t, s, "t1", TenantLimits{MaxConcurrentSearches: 2})
	a := context.WithValue(context.Background(), authContextKey, tina)
	b := context.WithValue(context.Background(), authContextKey, nobby)

	var held []func()
	refused := 0
	for i := 0; i < 8; i++ { // t1 fires a burst of eight heavy queries
		_, release, err := s.searchSlot(a)
		if err != nil {
			refused++
			continue
		}
		held = append(held, release)
	}
	if len(held) != 2 || refused != 6 {
		t.Fatalf("t1 holds %d slots (%d refused), want 2 and 6", len(held), refused)
	}
	for i := 0; i < 2; i++ { // t2 still finds the rest of the server's slots
		_, release, err := s.searchSlot(b)
		if err != nil {
			t.Fatalf("t2 refused while t1 was at its limit: %v", err)
		}
		defer release()
	}
	for _, r := range held {
		r()
	}
	if s.tenantRefusals("t1")["tenant.searchConcurrency"] != 6 || len(s.tenantRefusals("t2")) != 0 {
		t.Fatalf("refusals t1=%v t2=%v", s.tenantRefusals("t1"), s.tenantRefusals("t2"))
	}
}

func TestNoisyNeighbourSearchLatency(t *testing.T) {
	t.Setenv("MDDB_SEARCH_MAX_CONCURRENT", "4")
	s := limitServer(t)
	s.SearchLimiter = newSearchLimiter()
	s.Embedding = &mockEmbedding{dims: 3, model: "m"}
	if s.VectorStore == nil {
		s.VectorStore = vec.NewVectorStore(s.DB)
		if err := s.VectorStore.EnsureBucket(); err != nil {
			t.Fatal(err)
		}
	}
	setLimits(t, s, "t1", TenantLimits{MaxConcurrentSearches: 1, MaxConcurrentWrites: 1, MaxConcurrentReindexes: 1})
	s.tenantLimiter().now = time.Now // rates are not limited here; slots are
	for i := 0; i < 50; i++ {
		addTestDoc(t, s, "t2/docs", fmt.Sprintf("d%d", i), "en", fmt.Sprintf("the quick brown fox number %d jumps", i), nil)
	}
	search := func() time.Duration {
		start := time.Now()
		rec := restRec(t, s, nobby, http.MethodPost, "/v1/fts", `{"collection":"t2/docs","query":"quick fox","limit":10}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("t2's search: %d %s", rec.Code, rec.Body.String())
		}
		return time.Since(start)
	}
	const rounds = 60
	measure := func() []time.Duration {
		out := make([]time.Duration, rounds)
		for i := range out {
			out[i] = search()
		}
		return out
	}
	_ = measure() // warm up
	quiet := measure()

	stop := make(chan struct{})
	var wg sync.WaitGroup
	var batches, reindexes atomic.Int64
	wg.Add(2)
	go func() { // t1's bulk ingest
		defer wg.Done()
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			docs := make([]string, 20)
			for j := range docs {
				docs[j] = fmt.Sprintf(`{"key":"b%d-%d","lang":"en","contentMd":"bulk text %d"}`, i, j, j)
			}
			restRec(t, s, tina, http.MethodPost, "/v1/add-batch", `{"collection":"t1/bulk","documents":[`+strings.Join(docs, ",")+`]}`)
			batches.Add(1)
		}
	}()
	go func() { // t1's reindex, over and over
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			_, _ = s.reindexVectors(context.Background(), "t1/bulk", true, nil)
			reindexes.Add(1)
		}
	}()
	time.Sleep(20 * time.Millisecond) // let the neighbour get going
	noisy := measure()
	close(stop)
	wg.Wait()

	q50, q95 := durPercentile(quiet, 50), durPercentile(quiet, 95)
	n50, n95 := durPercentile(noisy, 50), durPercentile(noisy, 95)
	t.Logf("t2 search alone: p50 %v p95 %v; beside t1 (%d batches of 20, %d reindexes): p50 %v p95 %v; p95 ratio %.2f",
		q50, q95, batches.Load(), reindexes.Load(), n50, n95, float64(n95)/float64(q95))
	if batches.Load() == 0 {
		t.Fatal("the neighbour never ran")
	}
}

// waitUntil polls cond for up to ten seconds.
func waitUntil(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met in time")
		}
		time.Sleep(time.Millisecond)
	}
}

// indexOf is the first position of s in list, -1 when absent.
func indexOf(list []string, s string) int {
	for i, v := range list {
		if v == s {
			return i
		}
	}
	return -1
}

// durPercentile is the p-th percentile of the durations.
func durPercentile(d []time.Duration, p int) time.Duration {
	sorted := append([]time.Duration(nil), d...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	return sorted[(len(sorted)-1)*p/100]
}
