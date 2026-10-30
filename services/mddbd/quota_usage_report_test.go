package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	json "mddb/internal/jsonx"
	"mddb/proto"
)

// A collection's use of its quota, as the collection-config read reports it
// (GO-043): the documents, bytes and vectors the write gate counts and the
// embedding work spent today.

// collectionConfigBody reads GET /v1/collection-config for a collection.
func collectionConfigBody(t *testing.T, s *Server, collection string) map[string]any {
	t.Helper()
	rec := httptest.NewRecorder()
	s.handleCollectionConfig(rec, httptest.NewRequest(http.MethodGet, "/v1/collection-config?collection="+collection, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	return body
}

func TestCollectionConfigReportsQuotaUsage(t *testing.T) {
	gs, s := quotaTestServer(t)
	setTestQuota(t, s, "q", CollectionQuota{MaxDocuments: 10, MaxBytes: 1 << 20, EmbeddingCallsPerDay: 5})
	s.quotas().budget.spend("q", "twelve chars")

	usage, ok := collectionConfigBody(t, s, "q")["usage"].(map[string]any)
	if !ok {
		t.Fatal("no usage in the collection config of a collection with a quota")
	}
	if usage["documents"] != float64(2) || usage["bytes"].(float64) <= 0 || usage["vectors"] != float64(0) ||
		usage["embeddingCalls"] != float64(1) || usage["embeddingTokens"] != float64(3) {
		t.Errorf("usage = %v", usage)
	}

	resp, err := gs.GetCollectionConfig(context.Background(), &proto.GetCollectionConfigRequest{Collection: "q"})
	if err != nil {
		t.Fatal(err)
	}
	u := resp.GetUsage()
	if u.GetDocuments() != 2 || u.GetBytes() <= 0 || u.GetEmbeddingCalls() != 1 || u.GetEmbeddingTokens() != 3 {
		t.Errorf("gRPC usage = %v", u)
	}
}

func TestCollectionConfigWithoutQuotaHasNoUsage(t *testing.T) {
	gs, s := quotaTestServer(t)
	if err := s.CollectionManager.Set("q", &CollectionConfig{Description: "no quota"}); err != nil {
		t.Fatal(err)
	}
	if _, has := collectionConfigBody(t, s, "q")["usage"]; has {
		t.Error("usage reported for a collection without a quota")
	}
	resp, err := gs.GetCollectionConfig(context.Background(), &proto.GetCollectionConfigRequest{Collection: "q"})
	if err != nil {
		t.Fatal(err)
	}
	if resp.GetUsage() != nil {
		t.Errorf("gRPC usage = %v, want none", resp.GetUsage())
	}
}

func TestQuotaUsageToProtoNil(t *testing.T) {
	if quotaUsageToProto(nil) != nil {
		t.Error("nil usage should convert to nil")
	}
}

func TestEmbeddingBudgetToday(t *testing.T) {
	limits := &CollectionQuota{EmbeddingTokensPerDay: 100}
	b := newEmbeddingBudget(func(string) *CollectionQuota { return limits }, func(qe *QuotaError) error { return qe })
	b.spend("c", "abcdefgh")
	b.spend("c", "abcd")
	if got := b.today("c"); got.calls != 2 || got.tokens != 3 {
		t.Errorf("today = %+v, want 2 calls, 3 tokens", got)
	}
	if got := b.today("other"); got != (budgetUse{}) {
		t.Errorf("today(other) = %+v, want zero", got)
	}
}
