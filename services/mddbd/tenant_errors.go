package main

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	json "mddb/internal/jsonx"
)

// TenantLimitError is a request refused by a tenant's or a credential's rate
// or concurrency limit (GO-054). Every surface answers it the same way: 429
// Too Many Requests with Retry-After over HTTP, RESOURCE_EXHAUSTED over gRPC,
// and a tool or GraphQL error carrying the same detail (code TENANT_LIMIT).
type TenantLimitError struct {
	Tenant string // "" for a global user's credential
	// Scope is "tenant" (the tenant as a whole) or "key" (one credential).
	Scope string
	// Kind is the limit: readRate, writeRate, searchConcurrency or
	// writeConcurrency.
	Kind       string
	Limit      int
	RetryAfter time.Duration
}

// Limit kinds, as reported in errors and metrics.
const (
	limitReadRate          = "readRate"
	limitWriteRate         = "writeRate"
	limitSearchConcurrency = "searchConcurrency"
	limitWriteConcurrency  = "writeConcurrency"
)

// Error names the limit and when to come back.
func (e *TenantLimitError) Error() string {
	who := fmt.Sprintf("tenant %q", e.Tenant)
	if e.Scope == "key" {
		who = "this credential"
	}
	return fmt.Sprintf("rate limited: %s is at its %s limit of %d, retry after %ds (429)",
		who, e.Kind, e.Limit, e.retryAfterSeconds())
}

// retryAfterSeconds rounds the wait up to whole seconds, at least one.
func (e *TenantLimitError) retryAfterSeconds() int64 {
	return max(int64((e.RetryAfter+time.Second-1)/time.Second), 1)
}

// detail is the refusal as structured data: the REST body, the MCP structured
// content and the GraphQL extensions.
func (e *TenantLimitError) detail() map[string]any {
	return map[string]any{
		"error":             e.Error(),
		"code":              "TENANT_LIMIT",
		"status":            http.StatusTooManyRequests,
		"tenant":            e.Tenant,
		"scope":             e.Scope,
		"limit":             e.Kind,
		"max":               e.Limit,
		"retryAfterSeconds": e.retryAfterSeconds(),
	}
}

// asTenantLimitError finds a TenantLimitError in err's chain.
func asTenantLimitError(err error) *TenantLimitError {
	var te *TenantLimitError
	if errors.As(err, &te) {
		return te
	}
	return nil
}

// tenantLimitDetail is the refusal's detail, nil when err is not one.
func tenantLimitDetail(err error) map[string]any {
	if te := asTenantLimitError(err); te != nil {
		return te.detail()
	}
	return nil
}

// writeTenantLimit answers a refusal over HTTP and reports whether err was one.
func writeTenantLimit(w http.ResponseWriter, err error) bool {
	te := asTenantLimitError(err)
	if te == nil {
		return false
	}
	w.Header().Set(headerContentType, "application/json")
	w.Header().Set("Retry-After", strconv.FormatInt(te.retryAfterSeconds(), 10))
	w.WriteHeader(http.StatusTooManyRequests)
	_ = json.NewEncoder(w).Encode(te.detail())
	return true
}
