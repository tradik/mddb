package main

import (
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"mddb/internal/envconf"
	vec "mddb/internal/vector"
)

// Index checkpoints: starting without rebuilding (SRCH-023).
//
// Only the vectors were on disk. The HNSW graph and what IVF, PQ, OPQ, SQ and
// SQ4 train from a collection were built again at every start: 200,000
// vectors took about a minute of graph inserts and one to two minutes of
// k-means, during which a search got flat or a 503. Each collection now has
// a checkpoint file with those structures (internal/vector/persist_*.go),
// written in the background and at a clean stop, and read back at start.
//
// The store stays the truth. A checkpoint is matched against the stored
// vectors one by one; what it does not know is added the usual way, and a
// structure that does not match — or a file that is damaged, of another
// format version or of another embedding model — is ignored and built as
// before. Nothing here can stop a start: every failure ends in the rebuild
// that every start used to do.
//
// Where the file is: with one storage file, "<database>.ann/<collection>.ann";
// with MDDB_STORAGE_LAYOUT=per-collection, "<collection>.db.ann" next to the
// collection's own file. Backups do not carry it, and a restore removes it.

// Where a collection's index came from at the last load.
const (
	annFromDisk = "disk"    // read from its checkpoint
	annRebuilt  = "rebuilt" // built from the stored vectors
	annPartial  = "partial" // some indexes read, some built
)

// annCheckpointTick is how often the background checks what is due.
var annCheckpointTick = 2 * time.Second

// annPersistEnabled reports whether MDDB_VECTOR_INDEX_PERSIST leaves
// checkpoints on. "false" is the behaviour before they existed: nothing is
// read and nothing is written.
func annPersistEnabled() bool {
	return envconf.String("MDDB_VECTOR_INDEX_PERSIST", "true") != "false"
}

// annCheckpointInterval is how long a changed collection waits at most for
// its next checkpoint.
func annCheckpointInterval() time.Duration {
	return time.Duration(max(envconf.Int("MDDB_VECTOR_INDEX_CHECKPOINT_INTERVAL_SEC", 300), 1)) * time.Second
}

// annCheckpointChanges is how many vector writes bring a checkpoint forward.
func annCheckpointChanges() uint64 {
	return uint64(max(envconf.Int("MDDB_VECTOR_INDEX_CHECKPOINT_CHANGES", 50000), 1)) // #nosec G115 -- at least 1
}

// annPersistence is what the server knows about its checkpoints.
type annPersistence struct {
	mu    sync.Mutex
	state map[string]*annCollection
	// writeMu lets one checkpoint be written at a time, which bounds the
	// memory and the disk traffic to one collection's.
	writeMu sync.Mutex
	loop    *annLoop // the background writer; nil until the first load ends
}

// annCollection is one collection's entry in /v1/vector-stats.
type annCollection struct {
	// Source is where the indexes came from at the last load.
	Source string `json:"source"`
	// Indexes says the same for each index that keeps a checkpoint.
	Indexes map[string]string `json:"indexes,omitempty"`
	// Reason is why something was rebuilt.
	Reason string `json:"reason,omitempty"`
	// LoadMs is how long reading and matching the checkpoint took.
	LoadMs int64 `json:"loadMs"`
	// CaughtUp is the vectors stored after the checkpoint and added to it.
	CaughtUp int `json:"caughtUp"`
	// Checkpoint is the last attempt to write one.
	Checkpoint *annCheckpointStatus `json:"checkpoint,omitempty"`

	stamp        uint64        // the indexes' stamp the file on disk was written at
	current      bool          // the file on disk is what stamp describes
	lastWrite    time.Time     // the last attempt
	lastDuration time.Duration // how long it took
}

// annCheckpointStatus is the outcome of one checkpoint write.
type annCheckpointStatus struct {
	At         string   `json:"at"`
	Bytes      int64    `json:"bytes"`
	DurationMs int64    `json:"durationMs"`
	Indexes    []string `json:"indexes"`
	Error      string   `json:"error,omitempty"`
}

// annPending is, per index read from a checkpoint, the stored vectors the
// checkpoint did not cover. An index that is not in it was not read from a
// checkpoint and takes every vector.
type annPending map[string]map[string]bool

// needs reports whether the named index still has to be given docID.
func (p annPending) needs(name, docID string) bool {
	missing, restored := p[name]
	return !restored || missing[docID]
}

// annEnabled reports whether this server keeps checkpoints: the switch is
// on and the database is a file to put them next to.
func (s *Server) annEnabled() bool {
	return annPersistEnabled() && s.Path != ""
}

// annIndexDir is the directory the checkpoints are in.
func (s *Server) annIndexDir() string {
	if perCollectionLayout() {
		return collectionDir(s.Path)
	}
	return s.Path + ".ann"
}

// annIndexPath is a collection's checkpoint file.
func (s *Server) annIndexPath(collection string) string {
	name := collectionFileName(collection)
	if !perCollectionLayout() {
		name = strings.TrimSuffix(name, ".db")
	}
	return filepath.Join(s.annIndexDir(), name+".ann")
}

// annFingerprint identifies how a collection's vectors were produced: the
// model and variant the store recorded for it. A checkpoint made under
// another is not used.
func (s *Server) annFingerprint(collection string) string {
	p, _, err := s.VectorStore.Provenance(collection)
	if err != nil {
		return ""
	}
	return p.Model + "\x00" + p.Variant
}

// persistables are the indexes that keep a checkpoint, by name.
func (s *Server) persistables() map[string]vec.Persistable {
	out := map[string]vec.Persistable{}
	for name, searcher := range s.VectorSearchers {
		if p, ok := searcher.(vec.Persistable); ok {
			out[name] = p
		}
	}
	return out
}

// annStamp changes whenever a checkpoint of the collection would differ
// from the last: the sum of what each index counts, each of which only
// grows. Zero means no index has built anything for it.
func (s *Server) annStamp(collection string) uint64 {
	var stamp uint64
	for _, p := range s.persistables() {
		stamp += p.PersistStamp(collection)
	}
	return stamp
}

// restoreIndexes gives the indexes their structures from the collection's
// checkpoint and records where each came from. vectors is everything the
// store holds for the collection. The result says which vectors each
// restored index still needs; nil when nothing was restored.
func (s *Server) restoreIndexes(collection string, vectors map[string][]float32) annPending {
	if !s.annEnabled() || len(vectors) == 0 {
		return nil
	}
	start := time.Now()
	st := &annCollection{Source: annRebuilt, Indexes: map[string]string{}}
	pending := annPending{}

	cp, err := s.readCheckpoint(collection)
	if err != nil {
		st.Reason = err.Error()
	}
	for name, p := range s.persistables() {
		st.Indexes[name] = annRebuilt
		if cp == nil {
			continue
		}
		missing, err := cp.Restore(name, p, vectors)
		if err != nil {
			st.Reason = joinReasons(st.Reason, name+": "+err.Error())
			continue
		}
		st.Indexes[name] = annFromDisk
		pending[name] = make(map[string]bool, len(missing))
		for _, id := range missing {
			pending[name][id] = true
		}
		st.CaughtUp = max(st.CaughtUp, len(missing))
	}
	st.Source = annSource(st.Indexes)
	st.LoadMs = time.Since(start).Milliseconds()
	// A checkpoint that was read whole and covered everything is still the
	// one to keep; anything else is written again once the load is done.
	if st.Source == annFromDisk && st.CaughtUp == 0 {
		st.stamp, st.current, st.lastWrite = s.annStamp(collection), true, time.Now()
	}
	s.vectorLoad.persist.set(collection, st)
	s.reportIndexLoad(collection, st)
	return pending
}

// readCheckpoint reads the collection's checkpoint and checks that it is
// this collection's, under the embedding model the store records now.
func (s *Server) readCheckpoint(collection string) (*vec.Checkpoint, error) {
	cp, err := vec.ReadCheckpoint(s.annIndexPath(collection))
	switch {
	case errors.Is(err, os.ErrNotExist):
		return nil, errors.New("no checkpoint")
	case err != nil:
		return nil, err
	case cp.Meta.Collection != collection:
		return nil, errors.New("the checkpoint is of another collection")
	case cp.Meta.Fingerprint != s.annFingerprint(collection):
		return nil, errors.New("the embedding model changed since the checkpoint")
	}
	return cp, nil
}

// annSource sums up where the indexes came from.
func annSource(indexes map[string]string) string {
	disk := 0
	for _, from := range indexes {
		if from == annFromDisk {
			disk++
		}
	}
	switch disk {
	case 0:
		return annRebuilt
	case len(indexes):
		return annFromDisk
	}
	return annPartial
}

// joinReasons appends a reason to the ones before it.
func joinReasons(reasons, reason string) string {
	if reasons == "" {
		return reason
	}
	return reasons + "; " + reason
}

// reportIndexLoad logs and measures one collection's load.
func (s *Server) reportIndexLoad(collection string, st *annCollection) {
	if st.Source == annFromDisk {
		slog.Info("vector indexes read from their checkpoint", "collection", collection,
			"caughtUp", st.CaughtUp, "elapsed", time.Duration(st.LoadMs)*time.Millisecond)
	} else {
		slog.Info("vector indexes are built from the stored vectors", "collection", collection,
			"source", st.Source, "reason", st.Reason)
	}
	s.Metrics.SetGauge("mddb_vector_index_checkpoint_load_seconds",
		"Time spent reading and matching a collection's index checkpoint at the last load.",
		`collection="`+collection+`",source="`+st.Source+`"`, float64(st.LoadMs)/1000)
}

// set records a collection's load.
func (p *annPersistence) set(collection string, st *annCollection) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.state == nil {
		p.state = map[string]*annCollection{}
	}
	p.state[collection] = st
}

// entry is a collection's state, created when it has none: a collection
// first written while the server runs.
func (p *annPersistence) entry(collection string) *annCollection {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.state == nil {
		p.state = map[string]*annCollection{}
	}
	st := p.state[collection]
	if st == nil {
		st = &annCollection{Source: annRebuilt}
		p.state[collection] = st
	}
	return st
}

// forget drops a collection's state.
func (p *annPersistence) forget(collection string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.state, collection)
}

// fromDisk reports whether the named index of a collection was read from a
// checkpoint at the last load, and so has nothing to train.
func (p *annPersistence) fromDisk(collection, name string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	st := p.state[collection]
	return st != nil && st.Indexes[name] == annFromDisk
}

// status is a copy of a collection's state for a response, or nil.
func (p *annPersistence) status(collection string) *annCollection {
	p.mu.Lock()
	defer p.mu.Unlock()
	st := p.state[collection]
	if st == nil {
		return nil
	}
	c := *st
	return &c
}

// collections is every collection with a state, sorted.
func (p *annPersistence) collections() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]string, 0, len(p.state))
	for c := range p.state {
		out = append(out, c)
	}
	sort.Strings(out)
	return out
}

// needsTraining reports whether any index that has to be trained before it
// answers was not read from the collection's checkpoint.
func (s *Server) needsTraining(collection string) bool {
	for name, searcher := range s.VectorSearchers {
		if _, ok := searcher.(vec.BuildReporter); ok && !s.vectorLoad.persist.fromDisk(collection, name) {
			return true
		}
	}
	return false
}

// annSummary is the checkpoint block of /v1/vector-stats: the settings in
// force. Each collection's own entry is under the collection.
func (s *Server) annSummary() map[string]any {
	return map[string]any{
		"enabled":                   s.annEnabled(),
		"formatVersion":             vec.PersistFormatVersion,
		"checkpointIntervalSeconds": int(annCheckpointInterval().Seconds()),
		"checkpointChanges":         annCheckpointChanges(),
	}
}

// annHealth is the checkpoint block of /health: how many collections were
// read from disk at the last load and how many were built, or nil when
// checkpoints are off or nothing was loaded.
func (s *Server) annHealth() map[string]any {
	if !s.annEnabled() {
		return nil
	}
	counts := map[string]int{}
	for _, c := range s.vectorLoad.persist.collections() {
		if st := s.vectorLoad.persist.status(c); st != nil {
			counts[st.Source]++
		}
	}
	if len(counts) == 0 {
		return nil
	}
	return map[string]any{"fromDisk": counts[annFromDisk], "rebuilt": counts[annRebuilt], "partial": counts[annPartial]}
}

// addPersistenceStats adds to a collection's /v1/vector-stats entry where
// its indexes came from, its last checkpoint, and the tombstones POST
// /v1/vector-compact would clear.
func (s *Server) addPersistenceStats(collection string, info map[string]any) {
	if st := s.vectorLoad.persist.status(collection); st != nil {
		info["indexPersistence"] = st
	}
	if h := s.hnswIndex(); h != nil {
		info["hnswTombstones"] = h.DeletedSince(collection)
	}
}
