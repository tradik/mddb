package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// GO-033: the limiter turns a burst of expensive queries into a queue and then
// into an honest 503, instead of letting an unbounded number of them hold
// working memory at once.

func limiterWith(t *testing.T, capacity int, timeoutMs string) *SearchLimiter {
	t.Helper()
	t.Setenv("MDDB_SEARCH_MAX_CONCURRENT", strconv.Itoa(capacity))
	t.Setenv("MDDB_SEARCH_QUEUE_TIMEOUT_MS", timeoutMs)
	return newSearchLimiter()
}

func TestLimiterAdmitsUpToCapacity(t *testing.T) {
	l := limiterWith(t, 2, "10")

	r1, err := l.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	r2, err := l.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	inUse, peak, _, capacity := l.Stats()
	if inUse != 2 || peak != 2 || capacity != 2 {
		t.Errorf("stats = inUse %d, peak %d, cap %d; want 2, 2, 2", inUse, peak, capacity)
	}

	// The third has to wait, and gives up when the queue timeout passes.
	if _, err := l.Acquire(context.Background()); !errors.Is(err, ErrSearchBusy) {
		t.Errorf("a query beyond capacity should be rejected, got %v", err)
	}
	if _, _, rejected, _ := l.Stats(); rejected != 1 {
		t.Errorf("rejections = %d, want 1", rejected)
	}

	r1()
	r2()
	if inUse, _, _, _ := l.Stats(); inUse != 0 {
		t.Errorf("releasing should free the slots, inUse = %d", inUse)
	}

	// With slots free again, a query gets through.
	r3, err := l.Acquire(context.Background())
	if err != nil {
		t.Errorf("a released slot should be reusable, got %v", err)
	}
	r3()
}

func TestLimiterWaitsRatherThanRejectingImmediately(t *testing.T) {
	l := limiterWith(t, 1, "500")
	release, err := l.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	// Free the slot while a second caller is queued; it should be admitted.
	go func() {
		time.Sleep(50 * time.Millisecond)
		release()
	}()

	start := time.Now()
	r2, err := l.Acquire(context.Background())
	if err != nil {
		t.Fatalf("the queued query should have been admitted, got %v", err)
	}
	r2()
	if elapsed := time.Since(start); elapsed < 40*time.Millisecond {
		t.Errorf("it should have waited for the slot, returned after %s", elapsed)
	}
}

func TestLimiterHonoursClientCancellation(t *testing.T) {
	l := limiterWith(t, 1, "5000")
	release, err := l.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer release()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := l.Acquire(ctx); !errors.Is(err, context.Canceled) {
		t.Errorf("a cancelled client should get its own error, got %v", err)
	}
	// A client that walked away is not a rejection by us.
	if _, _, rejected, _ := l.Stats(); rejected != 0 {
		t.Errorf("cancellation should not count as a rejection, got %d", rejected)
	}
}

func TestDisabledLimiterAdmitsEverything(t *testing.T) {
	for _, l := range []*SearchLimiter{limiterWith(t, 0, "10"), nil} {
		for range 100 {
			release, err := l.Acquire(context.Background())
			if err != nil {
				t.Fatalf("a disabled limiter must admit every query, got %v", err)
			}
			release()
		}
		if inUse, _, rejected, capacity := l.Stats(); inUse != 0 || rejected != 0 || capacity != 0 {
			t.Errorf("a disabled limiter reports nothing: %d, %d, %d", inUse, rejected, capacity)
		}
	}
}

func TestPeakIsAHighWaterMark(t *testing.T) {
	l := limiterWith(t, 4, "10")
	var releases []func()
	for range 3 {
		r, err := l.Acquire(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		releases = append(releases, r)
	}
	for _, r := range releases {
		r()
	}
	if inUse, peak, _, _ := l.Stats(); inUse != 0 || peak != 3 {
		t.Errorf("inUse %d, peak %d; want 0, 3 — the peak must not fall back", inUse, peak)
	}
}

func TestHandlerRejectsWithRetryable503(t *testing.T) {
	s := &Server{SearchLimiter: limiterWith(t, 1, "10")}
	blocked := make(chan struct{})
	var served atomic.Int32

	handler := s.withSearchLimit(func(w http.ResponseWriter, _ *http.Request) {
		served.Add(1)
		<-blocked
		w.WriteHeader(http.StatusOK)
	})

	// Occupy the only slot.
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		handler(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/v1/fts", nil))
	}()
	for served.Load() == 0 {
		time.Sleep(time.Millisecond)
	}

	rec := httptest.NewRecorder()
	handler(rec, httptest.NewRequest(http.MethodPost, "/v1/fts", nil))

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", rec.Code)
	}
	if rec.Header().Get("Retry-After") == "" {
		t.Error("a 503 from overload should say when to come back")
	}
	if served.Load() != 1 {
		t.Errorf("the rejected request must not reach the handler, served = %d", served.Load())
	}

	close(blocked)
	wg.Wait()
}

func TestHandlerReleasesTheSlotAfterServing(t *testing.T) {
	s := &Server{SearchLimiter: limiterWith(t, 1, "10")}
	handler := s.withSearchLimit(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	for range 5 {
		rec := httptest.NewRecorder()
		handler(rec, httptest.NewRequest(http.MethodPost, "/v1/fts", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("sequential requests should all succeed, got %d", rec.Code)
		}
	}
	if inUse, _, _, _ := s.SearchLimiter.Stats(); inUse != 0 {
		t.Errorf("every slot should be back, inUse = %d", inUse)
	}
}

func TestHandlerReportsClientCancellation(t *testing.T) {
	s := &Server{SearchLimiter: limiterWith(t, 1, "5000")}
	release, err := s.SearchLimiter.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer release()

	handler := s.withSearchLimit(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("the handler should not run for a cancelled request")
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	rec := httptest.NewRecorder()
	handler(rec, httptest.NewRequest(http.MethodPost, "/v1/fts", nil).WithContext(ctx))

	if rec.Code != http.StatusRequestTimeout {
		t.Errorf("status = %d, want 408 for a client that walked away", rec.Code)
	}
}

// Under a burst, the limiter must admit no more than its capacity at once.
func TestConcurrencyNeverExceedsCapacity(t *testing.T) {
	const capacity = 3
	l := limiterWith(t, capacity, "2000")

	var current, maxSeen atomic.Int32
	var wg sync.WaitGroup
	for range 40 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			release, err := l.Acquire(context.Background())
			if err != nil {
				return // rejected under load is a valid outcome
			}
			defer release()
			now := current.Add(1)
			for {
				peak := maxSeen.Load()
				if now <= peak || maxSeen.CompareAndSwap(peak, now) {
					break
				}
			}
			time.Sleep(time.Millisecond)
			current.Add(-1)
		}()
	}
	wg.Wait()

	if maxSeen.Load() > capacity {
		t.Errorf("%d queries ran at once, capacity is %d", maxSeen.Load(), capacity)
	}
}
