package main

import (
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	vec "mddb/internal/vector"
)

// Writing the index checkpoints (SRCH-023).
//
// One goroutine looks at every collection every annCheckpointTick and writes
// the ones that are due, one at a time. A collection is due when its indexes
// changed since its file was written and either
// MDDB_VECTOR_INDEX_CHECKPOINT_INTERVAL_SEC has passed, or
// MDDB_VECTOR_INDEX_CHECKPOINT_CHANGES vectors were written and the last
// checkpoint is at least annMinSpacing, and twenty times its own duration,
// behind. A checkpoint is the whole file again, so that last rule is what
// keeps a collection under constant writes from spending more than a
// twentieth of the time being written out.
//
// Nothing depends on a checkpoint being recent. One that is behind costs the
// next start the vectors written since, added the usual way.

// annMinSpacing is the least time between two checkpoints of a collection
// that are brought forward by the number of changes.
var annMinSpacing = 10 * time.Second

// annLoop is the background writer.
type annLoop struct {
	stop chan struct{}
	done chan struct{}
}

// startCheckpoints starts the background writer; the first load calls it
// when it is over, and later loads find it running.
func (s *Server) startCheckpoints() {
	if !s.annEnabled() {
		return
	}
	p := &s.vectorLoad.persist
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.loop != nil {
		return
	}
	p.loop = &annLoop{stop: make(chan struct{}), done: make(chan struct{})}
	go s.runCheckpoints(p.loop)
}

// runCheckpoints writes what is due until the loop is stopped.
func (s *Server) runCheckpoints(l *annLoop) {
	defer close(l.done)
	t := time.NewTicker(annCheckpointTick)
	defer t.Stop()
	for {
		select {
		case <-l.stop:
			return
		case <-t.C:
			s.checkpointDue(false)
		}
	}
}

// checkpointsRunning reports whether the background writer was started.
func (s *Server) checkpointsRunning() bool {
	p := &s.vectorLoad.persist
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.loop != nil
}

// stopCheckpoints stops the background writer and writes every collection
// whose file is behind, so a clean stop leaves nothing for the next start
// to catch up on.
func (s *Server) stopCheckpoints() {
	p := &s.vectorLoad.persist
	p.mu.Lock()
	l := p.loop
	p.loop = nil
	p.mu.Unlock()
	if l == nil {
		return
	}
	close(l.stop)
	<-l.done
	s.checkpointDue(true)
}

// checkpointDue writes the collections that are due; final writes every
// collection that changed, whatever the time since its last checkpoint. A
// collection still loading is left alone, and one still training waits for
// that unless the server is stopping.
func (s *Server) checkpointDue(final bool) {
	load := s.vectorLoad.Load()
	if load == nil {
		return
	}
	for _, collection := range s.annCollections() {
		if load.collectionReady(collection) && (final || len(s.trainingCollections(collection)) == 0) {
			s.checkpointCollection(collection, final)
		}
	}
}

// annCollections is every collection an index holds a structure for or the
// last load recorded, sorted.
func (s *Server) annCollections() []string {
	seen := map[string]bool{}
	for _, c := range s.vectorLoad.persist.collections() {
		seen[c] = true
	}
	for _, p := range s.persistables() {
		for _, c := range p.Collections() {
			seen[c] = true
		}
	}
	out := make([]string, 0, len(seen))
	for c := range seen {
		out = append(out, c)
	}
	sort.Strings(out)
	return out
}

// checkpointCollection writes a collection's checkpoint if it is due.
func (s *Server) checkpointCollection(collection string, final bool) {
	p := &s.vectorLoad.persist
	stamp := s.annStamp(collection)
	if stamp == 0 {
		// No index holds anything for it: deleted, or never indexed.
		p.forget(collection)
		_ = os.Remove(s.annIndexPath(collection))
		return
	}
	st := p.entry(collection)
	p.mu.Lock()
	write := !(st.current && st.stamp == stamp) && (final || st.due(stamp, time.Now()))
	p.mu.Unlock()
	if write {
		s.writeCheckpoint(collection, st, stamp)
	}
}

// due reports whether a collection whose indexes are at stamp should be
// written now. Caller holds the persistence lock.
func (st *annCollection) due(stamp uint64, now time.Time) bool {
	if st.lastWrite.IsZero() {
		return true
	}
	elapsed := now.Sub(st.lastWrite)
	if elapsed >= annCheckpointInterval() {
		return true
	}
	behind := !st.current || stamp-st.stamp >= annCheckpointChanges()
	return behind && elapsed >= max(annMinSpacing, 20*st.lastDuration)
}

// writeCheckpoint writes a collection's checkpoint and records the outcome.
// stamp was read before the indexes are captured, so a write that lands
// while the file is being written leaves the collection due again.
//
// The restore read lock is held throughout, taken before writeMu: a restore
// or replication snapshot (which takes the write lock, then writeMu to drop
// the files) waits for a write in progress instead of having the database
// closed under it, and never the other way round.
func (s *Server) writeCheckpoint(collection string, st *annCollection, stamp uint64) {
	s.restoreMu.RLock()
	defer s.restoreMu.RUnlock()
	p := &s.vectorLoad.persist
	p.writeMu.Lock()
	defer p.writeMu.Unlock()

	start := time.Now()
	path := s.annIndexPath(collection)
	meta := vec.CheckpointMeta{
		Collection: collection, Fingerprint: s.annFingerprint(collection),
		Vectors:     uint64(s.VectorIndex.CollectionSize(collection)), // #nosec G115 -- a count
		CreatedUnix: start.Unix(),
	}
	info, err := vec.WriteCheckpoint(path, meta, s.VectorSearchers)
	took := time.Since(start)
	status := &annCheckpointStatus{At: start.UTC().Format(time.RFC3339), Bytes: info.Bytes,
		DurationMs: took.Milliseconds(), Indexes: info.Sections}
	switch {
	case err != nil:
		status.Error = err.Error()
		slog.Warn("writing a vector index checkpoint failed; the next start builds what it would have held",
			"collection", collection, "err", err)
	case len(info.Sections) == 0:
		_ = os.Remove(path) // nothing built any more: the old file describes what is gone
	default:
		slog.Debug("vector index checkpoint written", "collection", collection, "bytes", info.Bytes, "elapsed", took)
	}

	p.mu.Lock()
	st.Checkpoint, st.stamp, st.current, st.lastWrite, st.lastDuration = status, stamp, err == nil, start, took
	p.mu.Unlock()
	labels := `collection="` + collection + `"`
	s.Metrics.SetGauge("mddb_vector_index_checkpoint_write_seconds",
		"Time the last index checkpoint of a collection took to write.", labels, took.Seconds())
	s.Metrics.SetGauge("mddb_vector_index_checkpoint_bytes",
		"Size of a collection's index checkpoint file.", labels, float64(info.Bytes))
}

// invalidateCheckpoints removes every checkpoint and forgets what was known
// about them: the database they were written for has been replaced, by a
// restore or a replication snapshot. The reload that follows finds none and
// builds the indexes from the new database.
func (s *Server) invalidateCheckpoints() {
	if s.Path == "" {
		return
	}
	p := &s.vectorLoad.persist
	p.writeMu.Lock()
	defer p.writeMu.Unlock()
	removeCheckpoints(s.Path+".ann", ".ann", nil)
	removeCheckpoints(collectionDir(s.Path), ".db.ann", nil)
	p.mu.Lock()
	p.state = nil
	p.mu.Unlock()
}

// sweepCheckpoints removes the checkpoints of collections that no longer
// have vectors, and the ones the other storage layout left behind.
func (s *Server) sweepCheckpoints(counts map[string]int) {
	if !s.annEnabled() {
		return
	}
	p := &s.vectorLoad.persist
	p.writeMu.Lock()
	defer p.writeMu.Unlock()
	keep := make(map[string]bool, len(counts))
	for c := range counts {
		keep[filepath.Base(s.annIndexPath(c))] = true
	}
	if perCollectionLayout() {
		removeCheckpoints(collectionDir(s.Path), ".db.ann", keep)
		removeCheckpoints(s.Path+".ann", ".ann", nil)
		return
	}
	removeCheckpoints(s.Path+".ann", ".ann", keep)
	removeCheckpoints(collectionDir(s.Path), ".db.ann", nil)
}

// removeCheckpoints deletes the checkpoint files in dir — the names ending
// in suffix, and the temporary file a write that was killed left next to
// one — except those named in keep. A directory left empty goes too, unless
// it is the collection files' own.
func removeCheckpoints(dir, suffix string, keep map[string]bool) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		name := e.Name()
		file := strings.TrimSuffix(name, ".tmp")
		if !strings.HasSuffix(file, suffix) || (keep[file] && file == name) {
			continue
		}
		_ = os.Remove(filepath.Join(dir, name))
	}
	if suffix == ".ann" {
		_ = os.Remove(dir) // fails, as it should, while anything is left in it
	}
}
