package main

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"mddb/internal/envconf"

	json "mddb/internal/jsonx"

	bolt "go.etcd.io/bbolt"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Reads before the freelist is rebuilt (#333).
//
// After a crash the open walks every page to rebuild the freelist: an hour
// and more on a 100 GB file, with nothing served. Reads do not need the
// freelist — only page allocation does — so with the patched bbolt in
// third_party/bbolt the database opens after reading its meta pages, the
// server starts and answers reads at once, and the walk runs in the
// background. Writes get 503 (gRPC: Unavailable, MCP: an error) with the
// progress until it ends; work queued inside the server waits for it.
// MDDB_FREELIST_BACKGROUND=false restores the blocking open.

// errFreelistLoading is why a write is refused while the freelist loads.
var errFreelistLoading = errors.New("the database is still opening: reads work, writes resume when the freelist is rebuilt")

// backgroundFreelist is a freelist being rebuilt after the server started.
type backgroundFreelist struct {
	db    *bolt.DB
	path  string
	meta  boltMeta
	clock *startupClock // phase and progress, reported on /health
	done  atomic.Bool
	mu    sync.Mutex
	err   error
}

// loading reports whether writes are still waiting for the freelist.
func (b *backgroundFreelist) loading() bool { return b != nil && !b.done.Load() }

// failed is why the rebuild failed, or nil.
func (b *backgroundFreelist) failed() error {
	if b == nil {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.err
}

func backgroundFreelistEnabled() bool {
	return envconf.String("MDDB_FREELIST_BACKGROUND", "true") != "false"
}

// start runs the rebuild in the background.
func (b *backgroundFreelist) start() {
	if b != nil {
		go b.run(b.db, b.path, b.meta)
	}
}

// run prewarms the file when that helps, then loads the freelist.
func (b *backgroundFreelist) run(db *bolt.DB, path string, meta boltMeta) {
	start := time.Now()
	if fi, err := os.Stat(path); err == nil && shouldPrewarm(meta, fi.Size(), availableMemory()) {
		b.clock.begin("reading database into cache")
		size := fi.Size()
		if err := prewarmFile(path, func(done int64) { b.clock.setProgress(done, size) }); err != nil {
			slog.Warn("reading the database into the cache failed; rebuilding without it", "err", err)
		}
	}
	b.clock.begin("rebuilding freelist")
	stop := make(chan struct{})
	go sampleWalkProgress(b.clock, meta.bytesInUse(), stop)
	err := db.LoadFreelist()
	close(stop)
	if err == nil && !db.NoFreelistSync {
		// Store it now rather than at the first write, so a crash before
		// any write does not cost another walk.
		err = db.Update(func(*bolt.Tx) error { return nil })
	}
	b.mu.Lock()
	b.err = err
	b.mu.Unlock()
	b.done.Store(true)
	b.clock.begin("")
	if err != nil {
		slog.Error("rebuilding the freelist failed; the database stays read-only",
			"err", err, "remedy", "mddbd -verify-db FILE, then mddbd -repair-db FILE -repair-to NEWFILE (docs/BACKUP.md)")
		return
	}
	slog.Info("freelist rebuilt; writes resumed", "elapsed", time.Since(start).Round(time.Second))
}

// writeBlocked is why a write cannot run now, or nil.
func (s *Server) writeBlocked() error {
	if err := s.freelistLoad.failed(); err != nil {
		return fmt.Errorf("the database is read-only: rebuilding its freelist failed: %w", err)
	}
	if s.freelistLoad.loading() {
		return errFreelistLoading
	}
	return nil
}

// writeUnavailable answers a write refused by writeBlocked.
func (s *Server) writeUnavailable(w http.ResponseWriter, err error) {
	body := map[string]any{"error": err.Error()}
	if s.freelistLoad != nil {
		phase, _ := s.freelistLoad.clock.current()
		body["phase"] = phase
		if p := s.freelistLoad.clock.progress(); p != nil {
			body["progress"] = p
			if p.EtaSeconds > 0 {
				w.Header().Set("Retry-After", fmt.Sprint(min(p.EtaSeconds, 300)))
			}
		}
	}
	if w.Header().Get("Retry-After") == "" {
		w.Header().Set("Retry-After", "30")
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusServiceUnavailable)
	_ = json.NewEncoder(w).Encode(body)
}

// healthFreelist adds the rebuild to a /health body, and reports whether
// it is still running.
func (s *Server) healthFreelist(body map[string]any) {
	if !s.freelistLoad.loading() && s.freelistLoad.failed() == nil {
		return
	}
	body["status"] = "degraded"
	body["reads"] = true
	body["writes"] = false
	if err := s.freelistLoad.failed(); err != nil {
		body["error"] = err.Error()
		return
	}
	phase, _ := s.freelistLoad.clock.current()
	body["phase"] = phase
	if p := s.freelistLoad.clock.progress(); p != nil {
		body["progress"] = p
	}
}

// checkWritable is the gRPC form of the write guards: read-only mode, and
// a freelist still loading.
func (g *GRPCServer) checkWritable() error {
	if g.isReadOnly() {
		return status.Error(codes.PermissionDenied, "read-only mode")
	}
	if err := g.server.writeBlocked(); err != nil {
		return status.Error(codes.Unavailable, err.Error())
	}
	return nil
}
