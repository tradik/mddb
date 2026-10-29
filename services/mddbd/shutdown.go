package main

import (
	"context"
	"log/slog"
	"os"
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

	//    Storage backends come here too (GO-021): the queues above may still
	//    have been writing document payloads to them, and closing an S3
	//    client mid-write would abort an upload the indexes already record.
	add("storage-backends", s.Backends != nil, s.CloseStorageBackends)

	//    The freelist (#333), after the last queued write: stored once, the
	//    next start reads it rather than walking the file. Commits after
	//    this one store it too.
	//    The gain counters (AGT-004) write their last minute of counts.
	add("gain", s.Gain != nil && s.DB != nil, func() {
		if err := s.Gain.Stop(); err != nil {
			slog.Warn("gain counters not stored", "err", err)
		}
	})

	add("freelist", s.DB != nil, func() {
		if err := persistFreelist(s.DB); err != nil {
			slog.Warn("the next start will walk the database", "err", err)
		}
		for _, db := range s.collectionFileDBs() { // #336
			if err := persistFreelist(db); err != nil {
				slog.Warn("the next start will walk a collection file", "file", db.Path(), "err", err)
			}
		}
	})

	// 5. Caches last: they hold no durable state, but readers may still be in
	//    flight until the steps above return.
	add("lockfree-cache", s.LockFreeCache != nil, func() { s.LockFreeCache.Close() })
	add("document-cache", s.Cache != nil, func() { s.Cache.Close() })

	// 6. The database files (#350). Closing waits for every open read
	//    transaction; as a step it is bounded by the deadline like the rest,
	//    where the deferred Close at the end of main waited forever.
	add("database", s.DB != nil, func() {
		s.closeCollectionFiles()
		if err := s.DB.Close(); err != nil {
			slog.Error("closing database", "err", err)
		}
	})

	return steps
}

// Shutdown runs this server's sequence with the given deadline and reports
// whether every step finished.
func (s *Server) Shutdown(ctx context.Context) bool {
	return runShutdownSteps(ctx, s.shutdownSteps())
}

// runShutdownSteps executes steps in order until they are done or the deadline
// passes, reporting anything that did not finish.
//
// A step that hangs is logged by name rather than leaving an operator to guess
// which one wedged, and it is deliberately not waited on: it keeps running in
// its own goroutine while the process exits, which is strictly better than
// holding the process open until the operator's SIGKILL arrives.
func runShutdownSteps(ctx context.Context, steps []shutdownStep) bool {
	for _, step := range steps {
		if ctx.Err() != nil {
			slog.Warn("shutdown deadline reached; skipping remaining steps",
				"skippedFrom", step.name)
			return false
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
			return false
		}
	}
	slog.Info("shutdown complete")
	return true
}

// shutdownExit ends the process when shutdown cannot finish (#350).
var shutdownExit = os.Exit

// exitOnSecondSignal ends the process at once on another signal from sigCh:
// an operator who repeats SIGTERM or Ctrl-C wants it gone, not waited for.
func exitOnSecondSignal(sigCh <-chan os.Signal) {
	sig := <-sigCh
	slog.Warn("second signal; exiting without finishing shutdown", "sig", sig)
	shutdownExit(1)
}

// finishShutdown runs the sequence and, if a step did not finish in time,
// exits rather than returning to the deferred closes that would wait for it.
// Committed data is on disk either way; the next start rebuilds what was not
// stored, such as the freelist.
func (s *Server) finishShutdown(ctx context.Context) {
	if !s.Shutdown(ctx) {
		slog.Warn("exiting with shutdown unfinished")
		shutdownExit(1)
	}
}
