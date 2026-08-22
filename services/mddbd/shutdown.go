package main

import (
	"context"
	"log/slog"
	"time"

	"mddb/internal/envconf"
)

// Ordered shutdown (GO-034).
//
// Seven subsystems shipped a Stop, Close or Shutdown that nothing ever called:
// the WAL, the bulk-ingest worker, the cron scheduler, the index queue, the
// temporal manager, MVCC and the HTTP/3 listener. At process exit the kernel
// reclaims their goroutines and descriptors either way — the cost is not the
// leak, it is what those subsystems were still holding: queued index jobs,
// an in-flight bulk job, a buffered WAL write. Exiting without draining turns
// a graceful stop into the same data loss as a kill.
//
// Order matters, and it is the reverse of the dependency order: stop producing
// work, drain what is queued, then close what the queues were writing to.

// shutdownTimeout bounds the whole sequence. A subsystem that will not stop
// must not hold the process open indefinitely — an operator's SIGTERM is
// followed by a SIGKILL soon enough anyway.
func shutdownTimeout() time.Duration {
	return time.Duration(envconf.Int("MDDB_SHUTDOWN_TIMEOUT_SEC", 15)) * time.Second
}

// shutdownStep is one named piece of the sequence.
type shutdownStep struct {
	name string
	fn   func()
}

// shutdownSteps returns the sequence for this server, skipping subsystems that
// were never started.
func (s *Server) shutdownSteps() []shutdownStep {
	var steps []shutdownStep
	add := func(name string, enabled bool, fn func()) {
		if enabled {
			steps = append(steps, shutdownStep{name: name, fn: fn})
		}
	}

	// 1. Stop accepting new work.
	add("http3", s.HTTP3 != nil, func() { _ = s.HTTP3.Close() })
	add("cron", s.CronScheduler != nil, func() { s.CronScheduler.Stop() })

	// 2. Drain the queues, so what was accepted is finished or recorded.
	add("bulk-ingest", s.BulkIngest != nil, func() { s.BulkIngest.Stop() })
	add("index-queue", s.IndexQueue != nil, func() { s.IndexQueue.Shutdown() })
	add("embedding-worker", s.EmbeddingWorker != nil, func() { s.EmbeddingWorker.Stop() })

	// 3. Stop the periodic workers; nothing is producing for them now.
	add("ttl", s.TTLManager != nil, func() { s.TTLManager.Stop() })
	add("temporal", s.TemporalManager != nil, func() { s.TemporalManager.Stop() })
	add("mvcc", s.MVCC != nil, func() { s.MVCC.Close() })
	add("adaptive-index", s.AdaptiveIndex != nil, func() { s.AdaptiveIndex.Close() })

	// 4. Flush and close what the drained queues were writing to.
	add("wal", s.WAL != nil, func() { _ = s.WAL.Close() })

	// 5. Caches last: they hold no durable state, but readers may still be in
	//    flight until the steps above return.
	add("lockfree-cache", s.LockFreeCache != nil, func() { s.LockFreeCache.Close() })
	add("document-cache", s.Cache != nil, func() { s.Cache.Close() })

	return steps
}

// Shutdown runs this server's sequence with the given deadline.
func (s *Server) Shutdown(ctx context.Context) {
	runShutdownSteps(ctx, s.shutdownSteps())
}

// runShutdownSteps executes steps in order until they are done or the deadline
// passes, reporting anything that did not finish.
//
// A step that hangs is logged by name rather than leaving an operator to guess
// which one wedged, and it is deliberately not waited on: it keeps running in
// its own goroutine while the process exits, which is strictly better than
// holding the process open until the operator's SIGKILL arrives.
func runShutdownSteps(ctx context.Context, steps []shutdownStep) {
	for _, step := range steps {
		if ctx.Err() != nil {
			slog.Warn("shutdown deadline reached; skipping remaining steps",
				"skippedFrom", step.name)
			return
		}
		start := time.Now()
		done := make(chan struct{})
		go func() {
			defer close(done)
			step.fn()
		}()

		select {
		case <-done:
			slog.Debug("shutdown step finished", "step", step.name, "elapsed", time.Since(start))
		case <-ctx.Done():
			slog.Warn("shutdown step did not finish in time", "step", step.name,
				"elapsed", time.Since(start))
			return
		}
	}
	slog.Info("shutdown complete")
}
