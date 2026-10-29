package main

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	json "mddb/internal/jsonx"
	pb "mddb/proto"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// GO-043: the quota as configuration — validation, who may change it, and
// how each config surface stores, keeps and removes it.

func TestCollectionQuotaValidateAndPredicates(t *testing.T) {
	var nilQuota *CollectionQuota
	if nilQuota.Validate() != nil || !nilQuota.isZero() || nilQuota.limitsStorage() || nilQuota.limitsEmbedding() {
		t.Fatal("a nil quota limits nothing and is valid")
	}
	for _, q := range []CollectionQuota{{MaxDocuments: -1}, {MaxBytes: -1}, {MaxVectors: -1},
		{EmbeddingCallsPerDay: -1}, {EmbeddingTokensPerDay: -1}} {
		if err := q.Validate(); err == nil || !strings.Contains(err.Error(), "must be >= 0") {
			t.Errorf("%+v: err = %v", q, err)
		}
	}
	storage, budget := &CollectionQuota{MaxBytes: 1}, &CollectionQuota{EmbeddingTokensPerDay: 1}
	if !storage.limitsStorage() || storage.limitsEmbedding() || budget.limitsStorage() || !budget.limitsEmbedding() {
		t.Fatal("limits predicates disagree with the fields")
	}
	if !nilQuota.equal(&CollectionQuota{}) || storage.equal(nil) || !storage.equal(&CollectionQuota{MaxBytes: 1}) || storage.equal(budget) {
		t.Fatal("equal disagrees")
	}
}

func TestCollectionManagerKeepsAndRemovesQuota(t *testing.T) {
	s, cleanup := newTestServerForCollectionConfig(t)
	defer cleanup()
	cm := s.CollectionManager
	if err := cm.Set("c", &CollectionConfig{Quota: &CollectionQuota{}}); err != nil {
		t.Fatal(err)
	}
	if cfg, _ := cm.Get("c"); cfg.Quota != nil {
		t.Fatal("an empty quota on a new config was stored")
	}
	if err := cm.Set("c", &CollectionConfig{Quota: &CollectionQuota{MaxDocuments: 5}}); err != nil {
		t.Fatal(err)
	}
	if err := cm.Set("c", &CollectionConfig{Description: "renamed"}); err != nil {
		t.Fatal(err)
	}
	if cfg, _ := cm.Get("c"); cfg.Quota == nil || cfg.Quota.MaxDocuments != 5 {
		t.Fatalf("a config written without a quota dropped the stored one: %+v", cfg.Quota)
	}
	if err := cm.Set("c", &CollectionConfig{Quota: &CollectionQuota{}}); err != nil {
		t.Fatal(err)
	}
	if cfg, _ := cm.Get("c"); cfg.Quota != nil {
		t.Fatalf("an empty quota did not remove it: %+v", cfg.Quota)
	}
}

// authedQuotaServer is a quota test server with authentication on.
func authedQuotaServer(t *testing.T) (*GRPCServer, *Server, context.Context, context.Context) {
	t.Helper()
	gs, s := quotaTestServer(t)
	s.AuthManager = NewAuthManager(s.DB, AuthConfig{JWTSecret: "test-secret", JWTExpiry: time.Hour,
		AdminUsername: "admin", AdminPassword: "secret"})
	if err := s.AuthManager.EnsureBuckets(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AuthManager.CreateUser("bob", "pw-bob-1"); err != nil {
		t.Fatal(err)
	}
	if err := s.AuthManager.SetPermission(&Permission{Username: "bob", Collection: "*", Read: true, Write: true}); err != nil {
		t.Fatal(err)
	}
	admin := context.WithValue(context.Background(), authContextKey, &JWTClaims{Username: "admin", Admin: true})
	user := context.WithValue(context.Background(), authContextKey, &JWTClaims{Username: "bob"})
	return gs, s, admin, user
}

func TestCheckQuotaChange(t *testing.T) {
	_, s, admin, user := authedQuotaServer(t)
	setTestQuota(t, s, "q", CollectionQuota{MaxDocuments: 5})
	cases := []struct {
		name string
		ctx  context.Context
		next *CollectionQuota
		want error
	}{
		{"not sent", user, nil, nil},
		{"unchanged", user, &CollectionQuota{MaxDocuments: 5}, nil},
		{"admin", admin, &CollectionQuota{MaxDocuments: 9}, nil},
		{"writer", user, &CollectionQuota{MaxDocuments: 9}, errQuotaAdminOnly},
		{"anonymous", context.Background(), &CollectionQuota{}, errQuotaAdminOnly},
	}
	for _, tc := range cases {
		if err := s.checkQuotaChange(tc.ctx, "q", tc.next); err != tc.want {
			t.Errorf("%s: err = %v, want %v", tc.name, err, tc.want)
		}
	}
	if err := s.checkQuotaChange(admin, "q", &CollectionQuota{MaxBytes: -1}); err == nil || err == errQuotaAdminOnly {
		t.Errorf("invalid quota: err = %v", err)
	}
}

// putConfig sends a REST collection-config write.
func putConfig(s *Server, ctx context.Context, body map[string]any) *httptest.ResponseRecorder {
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPut, "/v1/collection-config", bytes.NewReader(b)).WithContext(ctx)
	rec := httptest.NewRecorder()
	s.handleCollectionConfig(rec, req)
	return rec
}

func TestRESTCollectionConfigQuota(t *testing.T) {
	_, s, admin, user := authedQuotaServer(t)
	quota := map[string]any{"maxDocuments": 3, "embeddingCallsPerDay": 10}
	if rec := putConfig(s, user, map[string]any{"collection": "q", "quota": quota}); rec.Code != http.StatusForbidden {
		t.Fatalf("writer setting a quota: %d %s", rec.Code, rec.Body.String())
	}
	if rec := putConfig(s, admin, map[string]any{"collection": "q", "quota": map[string]any{"maxBytes": -1}}); rec.Code != http.StatusBadRequest {
		t.Fatalf("negative quota: %d", rec.Code)
	}
	if rec := putConfig(s, admin, map[string]any{"collection": "q", "quota": quota}); rec.Code != http.StatusOK {
		t.Fatalf("admin setting a quota: %d %s", rec.Code, rec.Body.String())
	}
	// The tenant may still edit the rest of its config: an omitted quota is
	// no change.
	if rec := putConfig(s, user, map[string]any{"collection": "q", "description": "mine"}); rec.Code != http.StatusOK {
		t.Fatalf("writer editing the description: %d %s", rec.Code, rec.Body.String())
	}
	req := httptest.NewRequest(http.MethodGet, "/v1/collection-config?collection=q", nil).WithContext(admin)
	rec := httptest.NewRecorder()
	s.handleCollectionConfig(rec, req)
	if !strings.Contains(rec.Body.String(), `"quota":{"maxDocuments":3,"embeddingCallsPerDay":10}`) {
		t.Fatalf("GET = %s", rec.Body.String())
	}
}

func TestGRPCCollectionConfigQuota(t *testing.T) {
	gs, _, admin, user := authedQuotaServer(t)
	set := func(ctx context.Context, q *pb.CollectionQuotaProto) error {
		_, err := gs.SetCollectionConfig(ctx, &pb.SetCollectionConfigRequest{Collection: "q", Quota: q})
		return err
	}
	if err := set(user, &pb.CollectionQuotaProto{MaxDocuments: 3}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("writer: %v", err)
	}
	if err := set(admin, &pb.CollectionQuotaProto{MaxVectors: -1}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("negative: %v", err)
	}
	if err := set(admin, &pb.CollectionQuotaProto{MaxDocuments: 3, MaxBytes: 4, MaxVectors: 5,
		EmbeddingCallsPerDay: 6, EmbeddingTokensPerDay: 7}); err != nil {
		t.Fatal(err)
	}
	if err := set(user, nil); err != nil {
		t.Fatalf("writer leaving the quota alone: %v", err)
	}
	resp, err := gs.GetCollectionConfig(admin, &pb.GetCollectionConfigRequest{Collection: "q"})
	if err != nil {
		t.Fatal(err)
	}
	q := resp.Config.GetQuota()
	if q.GetMaxDocuments() != 3 || q.GetMaxBytes() != 4 || q.GetMaxVectors() != 5 || q.GetEmbeddingCallsPerDay() != 6 || q.GetEmbeddingTokensPerDay() != 7 {
		t.Fatalf("quota read back as %v", q)
	}
	if err := set(admin, &pb.CollectionQuotaProto{}); err != nil {
		t.Fatal(err)
	}
	resp, _ = gs.GetCollectionConfig(admin, &pb.GetCollectionConfigRequest{Collection: "q"})
	if resp.Config.GetQuota() != nil {
		t.Fatalf("an all-zero quota did not remove it: %v", resp.Config.GetQuota())
	}
}

func TestMCPCollectionConfigQuota(t *testing.T) {
	_, s := quotaTestServer(t)
	res := mcpCall(s, "set_collection_config", map[string]any{"collection": "q", "quota": map[string]any{
		"max_documents": float64(3), "max_bytes": float64(4), "max_vectors": float64(5),
		"embedding_calls_per_day": float64(6), "embedding_tokens_per_day": float64(7)}})
	if res["isError"] == true {
		t.Fatalf("set_collection_config: %v", res)
	}
	want := CollectionQuota{MaxDocuments: 3, MaxBytes: 4, MaxVectors: 5, EmbeddingCallsPerDay: 6, EmbeddingTokensPerDay: 7}
	if q := s.collectionQuota("q"); q == nil || *q != want {
		t.Fatalf("stored quota %+v", q)
	}
	res = mcpCall(s, "set_collection_config", map[string]any{"collection": "q", "quota": map[string]any{"max_documents": float64(-1)}})
	if res["isError"] != true {
		t.Fatalf("negative quota accepted: %v", res)
	}
}
