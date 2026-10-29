package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/vektah/gqlparser/v2/gqlerror"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// GO-043: how a quota refusal reads on each transport.

func TestQuotaErrorText(t *testing.T) {
	cases := []struct {
		qe   QuotaError
		want string
	}{
		{QuotaError{Collection: "c", Resource: quotaDocuments, Limit: 2, Used: 2},
			`quota exceeded: documents of collection "c" is 2, limit 2 (507)`},
		{QuotaError{Resource: quotaVectorRAM, Limit: 8, Used: 12},
			`quota exceeded: vectorRam is 12, limit 8 (507)`},
		{QuotaError{Resource: quotaDiskReserve, Limit: 1000, Used: 10},
			`quota exceeded: 10 bytes available on disk, below the reserve of 1000 (507)`},
		{QuotaError{Collection: "c", Resource: quotaEmbeddingCalls, Limit: 1, Used: 1, RetryAfter: 1500 * time.Millisecond},
			`quota exceeded: embeddingCalls of collection "c" is 1, limit 1 (429, retry after 2s)`},
	}
	for _, tc := range cases {
		if got := tc.qe.Error(); got != tc.want {
			t.Errorf("Error() = %q, want %q", got, tc.want)
		}
	}
}

func TestBadMapsTheEmbeddingBudgetTo429(t *testing.T) {
	qe := &QuotaError{Collection: "c", Resource: quotaEmbeddingTokens, Limit: 5, Used: 9, RetryAfter: 90 * time.Second}
	rec := httptest.NewRecorder()
	bad(rec, fmt.Errorf("reindex: %w", qe))
	if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") != "90" {
		t.Fatalf("status %d, Retry-After %q", rec.Code, rec.Header().Get("Retry-After"))
	}
	if !strings.Contains(rec.Body.String(), `"retryAfterSeconds":90`) {
		t.Fatalf("body = %s", rec.Body.String())
	}
	// Any other error is still a 400.
	rec = httptest.NewRecorder()
	bad(rec, errors.New("plain"))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("plain error: %d", rec.Code)
	}
}

func TestGRPCErrorKeepsOtherCodes(t *testing.T) {
	if got := status.Code(grpcError(codes.Internal, errors.New("x"))); got != codes.Internal {
		t.Fatalf("code = %v", got)
	}
	if got := status.Code(grpcError(codes.Internal, &QuotaError{Resource: quotaBytes})); got != codes.ResourceExhausted {
		t.Fatalf("code = %v", got)
	}
}

func TestGraphQLErrorPresenter(t *testing.T) {
	ctx := context.Background()
	if got := graphQLErrorPresenter(ctx, errors.New("plain")); got.Extensions != nil {
		t.Fatalf("a plain error gained extensions: %v", got.Extensions)
	}
	// A gqlerror that already has extensions keeps them alongside the quota.
	qe := &QuotaError{Collection: "c", Resource: quotaDocuments, Limit: 1, Used: 1}
	wrapped := &gqlerror.Error{Err: qe, Message: qe.Error(), Extensions: map[string]any{"trace": "t1"}}
	got := graphQLErrorPresenter(ctx, wrapped)
	if got.Extensions["trace"] != "t1" || got.Extensions["code"] != "QUOTA_EXCEEDED" || got.Extensions["status"] != http.StatusInsufficientStorage {
		t.Fatalf("extensions = %v", got.Extensions)
	}
}

func TestMCPQuotaContentOnlyForQuotaErrors(t *testing.T) {
	if mcpQuotaContent(errors.New("plain")) != nil {
		t.Fatal("structured content for a plain error")
	}
	if d := mcpQuotaContent(&QuotaError{Resource: quotaBytes}); d["resource"] != quotaBytes {
		t.Fatalf("detail = %v", d)
	}
}
