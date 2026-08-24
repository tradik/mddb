package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"runtime"
	"time"

	"mddb/internal/envconf"
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

// ErrSearchBusy is returned when a query could not get a slot in time.
var ErrSearchBusy = errors.New("search concurrency limit reached")

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

// Acquire takes a slot, waiting up to the configured timeout. The returned
// release function must be called; it is safe to call on the error path too,
// where it does nothing.
func (l *SearchLimiter) Acquire(ctx context.Context) (release func(), err error) {
	if l == nil {
		return func() {}, nil
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
		return func() {}, ctx.Err()
	case <-timer.C:
		l.rejects.add(1)
		return func() {}, ErrSearchBusy
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

// withSearchLimit wraps a handler so heavy queries queue instead of piling up.
// A query that cannot get a slot in time gets 503 with Retry-After, which says
// "come back" rather than "this failed".
func (s *Server) withSearchLimit(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		release, err := s.SearchLimiter.Acquire(r.Context())
		if err != nil {
			if errors.Is(err, ErrSearchBusy) {
				inUse, _, rejected, capacity := s.SearchLimiter.Stats()
				slog.Warn("search rejected: concurrency limit reached",
					"inUse", inUse, "capacity", capacity, "rejectedTotal", rejected, "path", r.URL.Path)
				if s.Metrics != nil {
					s.Metrics.IncOp("search_rejected", "concurrency")
				}
				w.Header().Set("Retry-After", "1")
				http.Error(w, `{"error":"server is at its search concurrency limit, retry shortly"}`,
					http.StatusServiceUnavailable)
				return
			}
			// The client's own context ended; nothing to report.
			http.Error(w, `{"error":"request cancelled"}`, http.StatusRequestTimeout)
			return
		}
		defer release()
		next(w, r)
	}
}
