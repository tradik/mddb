package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	json "mddb/internal/jsonx"

	"github.com/99designs/gqlgen/graphql"
	"github.com/vektah/gqlparser/v2/gqlerror"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Quota resources, as reported in errors, webhooks and metrics (GO-043).
const (
	quotaDocuments       = "documents"
	quotaBytes           = "bytes"
	quotaVectors         = "vectors"
	quotaDiskReserve     = "diskReserve"
	quotaVectorRAM       = "vectorRam"
	quotaEmbeddingCalls  = "embeddingCalls"
	quotaEmbeddingTokens = "embeddingTokens"
)

// QuotaError is a write or embedding call refused by a quota. Every transport
// maps it the same way: storage limits are 507 Insufficient Storage, the
// embedding budget is 429 Too Many Requests with Retry-After, and gRPC answers
// RESOURCE_EXHAUSTED for both.
type QuotaError struct {
	Collection string // "" for the server-wide limits (diskReserve, vectorRam)
	Resource   string
	Limit      int64
	Used       int64
	// RetryAfter is set for the embedding budget only: the time until it
	// resets. Storage limits do not reset by themselves.
	RetryAfter time.Duration
}

// Error names the limit and the status every transport maps it to.
func (e *QuotaError) Error() string {
	if e.Resource == quotaDiskReserve {
		return fmt.Sprintf("quota exceeded: %d bytes available on disk, below the reserve of %d (507)", e.Used, e.Limit)
	}
	what := fmt.Sprintf("%s of collection %q", e.Resource, e.Collection)
	if e.Collection == "" {
		what = e.Resource
	}
	msg := fmt.Sprintf("quota exceeded: %s is %d, limit %d", what, e.Used, e.Limit)
	if e.RetryAfter > 0 {
		return fmt.Sprintf("%s (429, retry after %ds)", msg, e.retryAfterSeconds())
	}
	return msg + " (507)"
}

// Status is the HTTP status of the refusal.
func (e *QuotaError) Status() int {
	if e.RetryAfter > 0 {
		return http.StatusTooManyRequests
	}
	return http.StatusInsufficientStorage
}

// retryAfterSeconds rounds the wait up, so a client never retries early.
func (e *QuotaError) retryAfterSeconds() int64 {
	return int64((e.RetryAfter + time.Second - 1) / time.Second)
}

// detail is the error as structured data, shared by the REST body, the MCP
// structured content and the GraphQL extensions.
func (e *QuotaError) detail() map[string]any {
	d := map[string]any{
		"error":      e.Error(),
		"code":       "QUOTA_EXCEEDED",
		"status":     e.Status(),
		"collection": e.Collection,
		"resource":   e.Resource,
		"limit":      e.Limit,
		"used":       e.Used,
	}
	if e.RetryAfter > 0 {
		d["retryAfterSeconds"] = e.retryAfterSeconds()
	}
	return d
}

// asQuotaError finds a QuotaError in err's chain.
func asQuotaError(err error) *QuotaError {
	var qe *QuotaError
	if errors.As(err, &qe) {
		return qe
	}
	return nil
}

// writeQuotaError answers a quota refusal over HTTP and reports whether err
// was one. Called from bad(), so every REST handler that reports a write
// failure maps it without knowing about quotas.
func writeQuotaError(w http.ResponseWriter, err error) bool {
	qe := asQuotaError(err)
	if qe == nil {
		return false
	}
	w.Header().Set("Content-Type", "application/json")
	if qe.RetryAfter > 0 {
		w.Header().Set("Retry-After", strconv.FormatInt(qe.retryAfterSeconds(), 10))
	}
	w.WriteHeader(qe.Status())
	_ = json.NewEncoder(w).Encode(qe.detail())
	return true
}

// grpcError is status.Error(code, err) except for a quota refusal, which is
// RESOURCE_EXHAUSTED whatever the call site's default code.
func grpcError(code codes.Code, err error) error {
	if asQuotaError(err) != nil {
		code = codes.ResourceExhausted
	}
	return status.Error(code, err.Error())
}

// graphQLErrorPresenter is gqlgen's default presenter plus the quota detail
// in the error's extensions, so a GraphQL client can tell a full collection
// from a broken request.
func graphQLErrorPresenter(ctx context.Context, err error) *gqlerror.Error {
	gerr := graphql.DefaultErrorPresenter(ctx, err)
	if qe := asQuotaError(err); qe != nil {
		if gerr.Extensions == nil {
			gerr.Extensions = map[string]any{}
		}
		for k, v := range qe.detail() {
			gerr.Extensions[k] = v
		}
	}
	return gerr
}

// mcpQuotaContent is the structured content of a failed tool call refused by
// a quota, nil for any other failure.
func mcpQuotaContent(err error) map[string]any {
	if qe := asQuotaError(err); qe != nil {
		return qe.detail()
	}
	return nil
}
