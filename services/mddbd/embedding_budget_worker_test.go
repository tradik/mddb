package main

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	pb "mddb/proto"
)

// GO-043: the embedding budget in the worker, which parks documents, and in
// a reindex, which is refused with 429.

// waitFor polls cond for up to five seconds.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestEmbeddingWorkerParksDocumentsOverBudget(t *testing.T) {
	orig := budgetRecheckInterval
	budgetRecheckInterval = 10 * time.Millisecond
	t.Cleanup(func() { budgetRecheckInterval = orig })

	_, vs, vi := embWorkerSetup(t)
	provider := &embWorkerMockProvider{model: "m", dims: 3, vector: []float32{1, 2, 3}}
	w := NewEmbeddingWorker(provider, vs, vi, 10)
	limits := &budgetLimits{q: map[string]*CollectionQuota{"c": {EmbeddingCallsPerDay: 1}}}
	w.budget = newTestBudget(limits)
	w.Start(1)
	defer w.Stop()

	calls := func() int {
		provider.mu.Lock()
		defer provider.mu.Unlock()
		return provider.callCount
	}
	w.Enqueue(EmbeddingJob{Collection: "c", DocID: "d1", ContentMD: "first"})
	w.Enqueue(EmbeddingJob{Collection: "c", DocID: "d2", ContentMD: "second"})
	waitFor(t, "the second document to be parked", func() bool {
		w.parkMu.Lock()
		defer w.parkMu.Unlock()
		return len(w.parked) == 1
	})
	time.Sleep(50 * time.Millisecond) // several rechecks
	if calls() != 1 || !w.Pending().IsPending("c", "d2") || w.Pending().IsPending("c", "d1") {
		t.Fatalf("calls %d; d2 pending %v; d1 pending %v", calls(), w.Pending().IsPending("c", "d2"), w.Pending().IsPending("c", "d1"))
	}

	// Raising the quota releases it without waiting for midnight.
	limits.set("c", &CollectionQuota{EmbeddingCallsPerDay: 10})
	waitFor(t, "the parked document to be embedded", func() bool { return w.Pending().Stats("c").Pending == 0 })
	if calls() != 2 || w.Pending().Stats("c").Failed != 0 {
		t.Fatalf("calls %d, stats %+v", calls(), w.Pending().Stats("c"))
	}
}

func TestEmbeddingWorkerStopDropsParkTimer(t *testing.T) {
	_, vs, vi := embWorkerSetup(t)
	w := NewEmbeddingWorker(&embWorkerMockProvider{model: "m", dims: 3, vector: []float32{1, 2, 3}}, vs, vi, 1)
	limits := &budgetLimits{q: map[string]*CollectionQuota{"c": {EmbeddingCallsPerDay: 1}}}
	w.budget = newTestBudget(limits)
	w.budget.spend("c", "x")
	if got := w.processJob(EmbeddingJob{Collection: "c", DocID: "d", ContentMD: "body"}); got != jobParked {
		t.Fatalf("outcome = %v, want parked", got)
	}
	w.park(EmbeddingJob{Collection: "c", DocID: "d", ContentMD: "body"})
	w.Start(1)
	w.Stop()
	if w.parkTimer != nil {
		t.Fatal("Stop left the recheck timer running")
	}

	// A release racing a stop gives up instead of blocking on a full queue.
	limits.set("c", nil)
	w.jobs <- EmbeddingJob{Collection: "c", DocID: "filler"}
	done := make(chan struct{})
	go func() {
		w.releaseParked()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("releaseParked blocked after Stop")
	}
}

// budgetServer is a reindex server whose collection may embed one chunk a day.
func budgetServer(t *testing.T) *Server {
	t.Helper()
	s := reindexServer(t)
	s.Embedding = &mockEmbedding{dims: 3, model: "m"}
	setTestQuota(t, s, "docs", CollectionQuota{EmbeddingCallsPerDay: 1})
	return s
}

func TestReindexStopsWhenTheBudgetRunsOut(t *testing.T) {
	s := budgetServer(t)
	rec := doRequest(t, s.handleVectorReindex, map[string]any{"collection": "docs", "force": true})
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "embedding budget exhausted: 2 documents not reindexed") {
		t.Fatalf("partial reindex: %d %s", rec.Code, rec.Body.String())
	}
}

func TestReindexRefusedOnEverySurfaceWhenBudgetIsSpent(t *testing.T) {
	cases := []struct {
		name  string
		check func(t *testing.T, s *Server)
	}{
		{"REST", func(t *testing.T, s *Server) {
			rec := doRequest(t, s.handleVectorReindex, map[string]any{"collection": "docs"})
			wantHTTPQuota(t, rec, http.StatusTooManyRequests, quotaEmbeddingCalls)
			if rec.Header().Get("Retry-After") == "" {
				t.Fatal("no Retry-After")
			}
		}},
		{"REST async", func(t *testing.T, s *Server) {
			rec := doRequest(t, s.handleVectorReindex, map[string]any{"collection": "docs", "async": true})
			wantHTTPQuota(t, rec, http.StatusTooManyRequests, quotaEmbeddingCalls)
		}},
		{"gRPC", func(t *testing.T, s *Server) {
			_, err := NewGRPCServer(s).VectorReindex(context.Background(), &pb.VectorReindexRequest{Collection: "docs"})
			wantGRPCQuota(t, err)
		}},
		{"MCP", func(t *testing.T, s *Server) {
			wantMCPQuota(t, mcpCall(s, "vector_reindex", map[string]any{"collection": "docs"}), http.StatusTooManyRequests)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := budgetServer(t)
			s.quotas().budget.spend("docs", "already spent")
			tc.check(t, s)
		})
	}
}
