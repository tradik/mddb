package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	json "mddb/internal/jsonx"
	"mddb/internal/metrics"
	"mddb/internal/webhooks"
	pb "mddb/proto"
)

// GO-043: how refusals and quota use are reported — the webhook and /metrics.

func TestQuotaExceededWebhookIsDebounced(t *testing.T) {
	gs, s := quotaTestServer(t)
	var mu sync.Mutex
	var got []map[string]any
	hook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var p map[string]any
		_ = json.NewDecoder(r.Body).Decode(&p)
		mu.Lock()
		got = append(got, p)
		mu.Unlock()
	}))
	t.Cleanup(hook.Close)
	if _, err := s.WebhookManager.Register(hook.URL, []string{webhooks.EventQuotaExceeded}, "q"); err != nil {
		t.Fatal(err)
	}
	setTestQuota(t, s, "q", CollectionQuota{MaxDocuments: 2})
	for i := 0; i < 3; i++ {
		_, err := gs.Add(context.Background(), &pb.AddRequest{Collection: "q", Key: "n", Lang: "en", ContentMd: "new"})
		wantGRPCQuota(t, err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		mu.Lock()
		n := len(got)
		mu.Unlock()
		if n > 0 || time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(100 * time.Millisecond) // room for a wrongly sent second one
	mu.Lock()
	defer mu.Unlock()
	if len(got) != 1 {
		t.Fatalf("%d webhooks for three refusals, want 1", len(got))
	}
	detail, _ := got[0]["detail"].(map[string]any)
	if got[0]["event"] != webhooks.EventQuotaExceeded || got[0]["collection"] != "q" || detail["resource"] != quotaDocuments {
		t.Fatalf("payload = %v", got[0])
	}
}

func TestQuotaReportFiresAgainAfterInterval(t *testing.T) {
	_, s := quotaTestServer(t)
	orig := quotaReportInterval
	quotaReportInterval = 0
	t.Cleanup(func() { quotaReportInterval = orig })
	s.WebhookManager = nil
	qe := &QuotaError{Collection: "q", Resource: quotaDocuments, Limit: 1, Used: 1}
	_ = s.reportQuota(qe)
	first := s.quotas().reported["q|documents"]
	time.Sleep(time.Millisecond)
	if err := s.reportQuota(qe); !errors.Is(err, qe) {
		t.Fatalf("reportQuota returned %v", err)
	}
	if s.quotas().reported["q|documents"] == first {
		t.Fatal("a refusal after the interval was not reported again")
	}
}

func TestQuotaMetrics(t *testing.T) {
	_, s := quotaTestServer(t)
	s.Metrics = metrics.NewMetrics(true, &serverMetricsStats{s: s})
	scrape := func() string {
		rec := httptest.NewRecorder()
		s.Metrics.HandleMetrics(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
		return rec.Body.String()
	}
	if out := scrape(); strings.Contains(out, "mddb_collection_fullness") || strings.Contains(out, "mddb_embedding_budget_used") {
		t.Fatal("quota gauges emitted on a server without quotas")
	}

	s.Metrics = metrics.NewMetrics(true, &serverMetricsStats{s: s}) // a fresh 15s cache
	setTestQuota(t, s, "q", CollectionQuota{MaxDocuments: 4, MaxBytes: 1 << 20, MaxVectors: 10, EmbeddingCallsPerDay: 4})
	setTestQuota(t, s, "budget-only", CollectionQuota{EmbeddingTokensPerDay: 100})
	s.quotas().budget.spend("q", "text")
	out := scrape()
	for _, want := range []string{
		`mddb_collection_fullness{collection="q",resource="documents"} 0.5`,
		`mddb_collection_fullness{collection="q",resource="bytes"} `,
		`mddb_collection_fullness{collection="q",resource="vectors"} 0`,
		`mddb_embedding_budget_used{collection="budget-only"} 0`,
		`mddb_embedding_budget_used{collection="q"} 0.25`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("metrics missing %q", want)
		}
	}
}

func TestQuotaGaugesWithoutCollectionManager(t *testing.T) {
	s := &Server{}
	if f, b := s.quotaGauges(); f != nil || b != nil {
		t.Fatalf("got %v %v", f, b)
	}
	if q := s.collectionQuota("x"); q != nil {
		t.Fatalf("collectionQuota = %v", q)
	}
}
