package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"runtime"
	"strconv"
	"time"

	"mddb/internal/envconf"
	json "mddb/internal/jsonx"
)

// Bounded concurrency for heavy queries (GO-033).
//
// A single-binary server has nowhere to shed load. Agents fire bursts of
// expensive searches in parallel, and each one holds working memory for as
// long as it runs — so past some width the process does not slow down, it
// dies. A semaphore turns that into a queue and then into an honest 503,
// which a client can retry, rather than an OOM that takes every other request
// with it.
//
// This bounds concurrency, not per-request cost; the allocation work in this
// ticket is what makes each request cheap enough for the bound to be generous.
//
// GO-053: the gate is searchSlot, in the search core, so REST, gRPC, GraphQL
// and MCP queue behind the same slots and are refused the same way: 503 with
// Retry-After over HTTP, RESOURCE_EXHAUSTED over gRPC, and a tool or GraphQL
// error carrying the same detail (code SEARCH_BUSY, retryAfterSeconds).

// ErrSearchBusy is returned when a query could not get a slot in time. The
// text is what gRPC and MCP callers read, so it says when to come back.
var ErrSearchBusy = errors.New("server is at its search concurrency limit, retry after 1s (503)")

// SearchLimiter admits a bounded number of concurrent heavy queries.
// A nil limiter admits everything, so call sites need no special case.
type SearchLimiter struct {
	slots   chan struct{}
	wait    time.Duration
	rejects uint64Counter
	peak    uint64Counter
	inUse   uint64Counter
}

// newSearchLimiter builds the limiter from the environment.
// MDDB_SEARCH_MAX_CONCURRENT defaults to the CPU count — beyond that, queries
// contend for cores anyway, so admitting more buys latency, not throughput.
// 0 disables the limiter entirely.
func newSearchLimiter() *SearchLimiter {
	n := envconf.Int("MDDB_SEARCH_MAX_CONCURRENT", runtime.NumCPU())
	if n <= 0 {
		return nil
	}
	waitMs := envconf.Int("MDDB_SEARCH_QUEUE_TIMEOUT_MS", 2000)
	if waitMs < 0 {
		waitMs = 0
	}
	return &SearchLimiter{
		slots: make(chan struct{}, n),
		wait:  time.Duration(waitMs) * time.Millisecond,
	}
}

// noRelease is the release function handed back when nothing was taken: no
// limiter is configured, the request was already admitted, or admission was
// refused.
func noRelease() {
	// Nothing was acquired, so there is nothing to give back.
}

// Acquire takes a slot, waiting up to the configured timeout. The returned
// release function must be called; it is safe to call on the error path too,
// where it does nothing.
func (l *SearchLimiter) Acquire(ctx context.Context) (release func(), err error) {
	if l == nil {
		return noRelease, nil
	}

	timer := time.NewTimer(l.wait)
	defer timer.Stop()

	select {
	case l.slots <- struct{}{}:
		l.peak.observeMax(l.inUse.add(1))
		return func() {
			l.inUse.add(^uint64(0)) // -1
			<-l.slots
		}, nil
	case <-ctx.Done():
		// The client gave up first; not our rejection to report.
		return noRelease, ctx.Err()
	case <-timer.C:
		l.rejects.add(1)
		return noRelease, ErrSearchBusy
	}
}

// Stats reports the numbers an operator needs: how many queries are running,
// the high-water mark, and how many were turned away.
func (l *SearchLimiter) Stats() (inUse, peak, rejected uint64, capacity int) {
	if l == nil {
		return 0, 0, 0, 0
	}
	return l.inUse.load(), l.peak.load(), l.rejects.load(), cap(l.slots)
}

// searchSlotKey marks a context whose request already holds a slot.
type searchSlotKey struct{}

// searchRetryAfterSeconds is the wait a refused query is told to observe.
const searchRetryAfterSeconds = 1

// searchSlot is the one gate every surface's heavy query passes (GO-053):
// REST through withSearchLimit, and gRPC, GraphQL and MCP through the search
// core they share (shapedFTS, shapedListing, shapedCodeGraph, RunEval and the
// direct client's vector, hybrid, cross-collection, symbol and aggregate
// calls). The returned context carries the slot, so a request that passes the
// gate again further down — a REST route that reaches the core, an evaluation
// running its queries — holds one slot, not two. Callers pass the returned
// context on and call release.
//
// GO-054: a tenant with a MaxConcurrentSearches limit first takes one of its
// own slots, so it can never hold more of the server's than that — the rest
// stay free for the other tenants. A tenant at its limit is refused with a
// TenantLimitError (429), not ErrSearchBusy: the server is not busy, the
// tenant is.
func (s *Server) searchSlot(ctx context.Context) (context.Context, func(), error) {
	if ctx.Value(searchSlotKey{}) != nil {
		return ctx, noRelease, nil
	}
	releaseTenant, err := s.tenantSearchSlot(ctx)
	if err != nil {
		return ctx, noRelease, err
	}
	if s.SearchLimiter == nil {
		if releaseTenant == nil {
			return ctx, noRelease, nil
		}
		return context.WithValue(ctx, searchSlotKey{}, true), releaseTenant, nil
	}
	release, err := s.SearchLimiter.Acquire(ctx)
	if err != nil {
		if releaseTenant != nil {
			releaseTenant()
		}
		if errors.Is(err, ErrSearchBusy) {
			s.reportSearchBusy(ctx)
		}
		return ctx, release, err
	}
	if releaseTenant != nil {
		release = bothReleases(release, releaseTenant)
	}
	return context.WithValue(ctx, searchSlotKey{}, true), release, nil
}

// bothReleases releases a server slot, then the tenant's.
func bothReleases(server, tenant func()) func() {
	return func() {
		server()
		tenant()
	}
}

// reportSearchBusy logs and counts one refused query.
func (s *Server) reportSearchBusy(ctx context.Context) {
	inUse, _, rejected, capacity := s.SearchLimiter.Stats()
	slog.Warn("search rejected: concurrency limit reached",
		"inUse", inUse, "capacity", capacity, "rejectedTotal", rejected, "surface", shapeScopeOf(ctx).surface.String())
	if s.Metrics != nil {
		s.Metrics.IncOp("search_rejected", "concurrency")
	}
}

// limitedSearch runs one heavy query holding a search slot, or refuses it
// with ErrSearchBusy when none frees up in time.
func limitedSearch[T any](ctx context.Context, s *Server, run func(context.Context) (T, error)) (T, error) {
	ctx, release, err := s.searchSlot(ctx)
	if err != nil {
		var none T
		return none, err
	}
	defer release()
	return run(ctx)
}

// searchBusyDetail is the refusal as structured data: the REST body, the MCP
// structured content and the GraphQL extensions. nil when err is not one.
func searchBusyDetail(err error) map[string]any {
	if !errors.Is(err, ErrSearchBusy) {
		return nil
	}
	return map[string]any{
		"error":             ErrSearchBusy.Error(),
		"code":              "SEARCH_BUSY",
		"status":            http.StatusServiceUnavailable,
		"retryAfterSeconds": searchRetryAfterSeconds,
	}
}

// writeSearchBusy answers a refused query over HTTP — 503 with Retry-After,
// "come back" rather than "this failed" — and reports whether err was one.
func writeSearchBusy(w http.ResponseWriter, err error) bool {
	detail := searchBusyDetail(err)
	if detail == nil {
		return false
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Retry-After", strconv.Itoa(searchRetryAfterSeconds))
	w.WriteHeader(http.StatusServiceUnavailable)
	_ = json.NewEncoder(w).Encode(detail)
	return true
}

// withSearchLimit is the gate for the REST query routes, whose handlers do
// not all run through the shared core. The request's context carries the
// slot on, so the core does not take a second one.
func (s *Server) withSearchLimit(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, release, err := s.searchSlot(r.Context())
		if err != nil {
			if !writeSearchBusy(w, err) && !writeTenantLimit(w, err) { // GO-054: the tenant's search limit
				// The client's own context ended; nothing to report.
				http.Error(w, `{"error":"request cancelled"}`, http.StatusRequestTimeout)
			}
			return
		}
		defer release()
		next(w, r.WithContext(ctx))
	}
}
