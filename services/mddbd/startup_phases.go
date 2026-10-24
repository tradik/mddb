package main

import (
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

// Startup progress (#329).
//
// On a database of tens of gigabytes a start took minutes, and in that time
// the process logged nothing: every startup line appeared together, at the
// end. The health endpoint opened only after the database had been opened,
// so a container health check got "connection refused" and failed it, and an
// operator could not tell a slow start from a hang. Each phase is now logged
// as it starts and when it ends, with its duration, and /health answers from
// the first moment with the phase in progress.

type startupClock struct {
	mu      sync.Mutex
	started time.Time
	since   time.Time
	phase   string
	srv     atomic.Pointer[Server]
}

func newStartupClock() *startupClock {
	now := time.Now()
	return &startupClock{started: now, since: now}
}

// begin ends the current phase, logging how long it took, and starts the next.
func (c *startupClock) begin(phase string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now()
	if c.phase != "" {
		slog.Info("startup phase finished", "phase", c.phase, "elapsed", now.Sub(c.since).Round(time.Millisecond))
	}
	c.phase, c.since = phase, now
	slog.Info("startup phase", "phase", phase)
}

// done ends the last phase and logs the total.
func (c *startupClock) done() {
	c.begin("")
	c.mu.Lock()
	defer c.mu.Unlock()
	slog.Info("startup complete", "elapsed", time.Since(c.started).Round(time.Millisecond))
}

func (c *startupClock) current() (string, time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.phase, time.Since(c.started)
}

// healthHandler answers /health during startup: from the server once it
// exists, and before that with the phase in progress.
func (c *startupClock) healthHandler(w http.ResponseWriter, r *http.Request) {
	if s := c.srv.Load(); s != nil && s.Ready {
		s.handleHealth(w, r)
		return
	}
	phase, elapsed := c.current()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusServiceUnavailable)
	_, _ = fmt.Fprintf(w, `{"status":"warming_up","phase":%q,"elapsedSeconds":%d}`, phase, int(elapsed.Seconds()))
}

// startEarlyHealth listens on addr for /health while the server starts. The
// caller closes the returned server before the main one takes the address.
func (c *startupClock) startEarlyHealth(addr string) *http.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", c.healthHandler)
	mux.HandleFunc("/v1/health", c.healthHandler)
	srv := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Warn("early health server stopped", "err", err)
		}
	}()
	slog.Info("health endpoint listening while the server starts", "addr", addr)
	return srv
}
