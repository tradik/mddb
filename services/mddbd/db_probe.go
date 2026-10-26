package main

import (
	"sync"
	"time"

	"mddb/internal/envconf"

	bolt "go.etcd.io/bbolt"
)

// A health check that answers while the database is busy (#350).
//
// /health opened a read transaction and waited for it. A read transaction
// cannot begin while a write that grows the file waits to remap, and that
// write waits for every reader already open — so during a long walk and a
// bulk ingest /health hung with everything else, and the orchestrator saw a
// dead process. The probe now runs on its own, one at a time; /health waits
// for it a short while, then reports how long the database has been busy.
// Only a wait past MDDB_HEALTH_STALL_LIMIT is reported as a failure.

// healthProbeWait is how long /health waits for the probe.
var healthProbeWait = 2 * time.Second

func healthStallLimit() time.Duration {
	return time.Duration(envconf.Int("MDDB_HEALTH_STALL_LIMIT", 300)) * time.Second
}

// dbProbe is the probe transaction in flight, if any.
type dbProbe struct {
	mu      sync.Mutex
	running chan struct{} // closed when the probe in flight ends
	since   time.Time     // when it began
	err     error         // the last probe's result
}

// probeDB opens an empty read transaction, or joins the one in flight, and
// waits up to wait. It returns how long the probe has been waiting if it has
// not finished, else the transaction's error.
func (s *Server) probeDB(wait time.Duration) (stalled time.Duration, err error) {
	p := &s.dbProbe
	p.mu.Lock()
	if p.running == nil {
		done := make(chan struct{})
		p.running, p.since = done, time.Now()
		go func() {
			err := s.DBView(func(*bolt.Tx) error { return nil })
			p.mu.Lock()
			p.err, p.running = err, nil
			p.mu.Unlock()
			close(done)
		}()
	}
	running, since := p.running, p.since
	p.mu.Unlock()

	select {
	case <-running:
		p.mu.Lock()
		defer p.mu.Unlock()
		return 0, p.err
	case <-time.After(wait):
		return time.Since(since), nil
	}
}
