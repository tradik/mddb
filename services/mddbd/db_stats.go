package main

import (
	"bytes"
	"fmt"
	"hash/crc32"
	"log/slog"
	"sort"
	"sync"
	"time"

	"mddb/internal/envconf"

	bolt "go.etcd.io/bbolt"
)

// Database statistics without the wait (#349).
//
// GET /v1/stats walked the docs, revision and metadata-index buckets on every
// call and then read every document again for a checksum. On a database of a
// few million documents that never finished: the metadata index alone holds
// tens of millions of keys, and the checksum read all of the documents' bytes.
// gRPC Stats, MCP stats and the Prometheus scrape each had their own copy of
// the walk.
//
// They now share one snapshot, computed one walk at a time in a single pass
// that counts and checksums together, and stamped with the database's commit
// counter:
//
//   - no commit since the walk: the snapshot is exact and returned at once;
//   - commits since, and the last walk was cheap (under a second, which is
//     most databases): walk again and wait, so counts stay current as before;
//   - commits since, and walks are expensive: return the snapshot with its
//     age, and refresh it in the background at most every MDDB_STATS_TTL.
//
// A caller with no snapshot at all waits at most MDDB_STATS_WAIT and is told
// the counts are pending if the walk takes longer.

// collectionCounts is one collection's line in a snapshot.
type collectionCounts struct {
	Documents   int
	Revisions   int
	MetaIndices int
	Embeddings  int
	Chunks      int
	Checksum    string
	checksum    uint32
}

// dbStatsSnapshot is one walk's result.
type dbStatsSnapshot struct {
	Collections map[string]*collectionCounts
	ComputedAt  time.Time
	Took        time.Duration
}

// cheapWalk is how long a walk may take for callers to wait for a fresh one.
const cheapWalk = time.Second

// slowWalk is the walk time worth a line in the log.
var slowWalk = 10 * time.Second

// sortedNames is the snapshot's collection names, sorted.
func (d *dbStatsSnapshot) sortedNames() []string {
	out := make([]string, 0, len(d.Collections))
	for c := range d.Collections {
		out = append(out, c)
	}
	sort.Strings(out)
	return out
}

// walkCache holds the results of expensive whole-database walks (#349):
// /v1/stats, /v1/vector-stats and /v1/storage share it, each under a key.
type walkCache struct {
	mu      sync.Mutex
	entries map[string]*walkEntry
}

type walkEntry struct {
	val        any
	computedAt time.Time
	took       time.Duration
	commits    uint64        // the commit counter when the walk began
	running    chan struct{} // closed when the walk in progress ends
}

func statsTTL() time.Duration {
	return time.Duration(envconf.Int("MDDB_STATS_TTL", 300)) * time.Second
}

func statsWait() time.Duration {
	return time.Duration(envconf.Int("MDDB_STATS_WAIT", 2)) * time.Second
}

// cachedWalk returns key's newest result, its time, and whether there is
// none yet. Exact when nothing was committed since the walk; walked again and
// waited for (up to wait) when the last walk was cheap; otherwise returned
// as it is while a background walk refreshes it at most every TTL. A failed
// walk keeps the previous result.
func (s *Server) cachedWalk(key string, wait time.Duration, compute func() (any, error)) (any, time.Time, bool) {
	commits := (&serverMetricsStats{s: s}).DBWriteTransactions()
	c := &s.walks
	c.mu.Lock()
	if c.entries == nil {
		c.entries = map[string]*walkEntry{}
	}
	e := c.entries[key]
	if e == nil {
		e = &walkEntry{}
		c.entries[key] = e
	}
	if e.val != nil && e.commits == commits {
		val, at := e.val, e.computedAt
		c.mu.Unlock()
		return val, at, false
	}
	cheap := e.val == nil || e.took < cheapWalk
	if (cheap || time.Since(e.computedAt) >= statsTTL()) && e.running == nil {
		e.running = make(chan struct{})
		go s.runWalk(e, commits, compute)
	}
	running, val, at := e.running, e.val, e.computedAt
	c.mu.Unlock()

	if cheap && running != nil && wait > 0 {
		select {
		case <-running:
		case <-time.After(wait):
		}
		c.mu.Lock()
		val, at = e.val, e.computedAt
		c.mu.Unlock()
	}
	return val, at, val == nil
}

func (s *Server) runWalk(e *walkEntry, commits uint64, compute func() (any, error)) {
	start := time.Now()
	val, err := compute()
	took := time.Since(start)
	if err != nil {
		slog.Warn("computing database statistics failed", "err", err)
	} else if took > slowWalk {
		slog.Info("database statistics computed", "took", took.Round(time.Second), "nextAfter", statsTTL())
	}
	s.walks.mu.Lock()
	if err == nil {
		e.val, e.computedAt, e.took, e.commits = val, time.Now(), took, commits
	}
	done := e.running
	e.running = nil
	s.walks.mu.Unlock()
	close(done)
}

// dbStats is the shared counts snapshot, or nil while the first walk runs.
func (s *Server) dbStats(wait time.Duration) *dbStatsSnapshot {
	val, _, _ := s.cachedWalk("stats", wait, func() (any, error) { return s.walkStats() })
	snap, _ := val.(*dbStatsSnapshot)
	return snap
}

// walkStats counts every collection in one pass.
func (s *Server) walkStats() (*dbStatsSnapshot, error) {
	start := time.Now()
	snap := &dbStatsSnapshot{Collections: map[string]*collectionCounts{}}
	get := func(name []byte) *collectionCounts {
		cc := snap.Collections[string(name)]
		if cc == nil {
			cc = &collectionCounts{}
			snap.Collections[string(name)] = cc
		}
		return cc
	}
	err := s.DBViewAll(func(tx *bolt.Tx) error {
		if b := tx.Bucket(s.BucketNames.Docs); b != nil {
			c := b.Cursor()
			for k, v := c.First(); k != nil; k, v = c.Next() {
				if name, ok := statsCollection(k); ok {
					cc := get(name)
					cc.Documents++
					// The checksum the separate pass used to compute: key and
					// the first 64 bytes of the value, XORed.
					h := crc32.ChecksumIEEE(k)
					h ^= crc32.ChecksumIEEE(v[:min(len(v), 64)])
					cc.checksum ^= h
				}
			}
		}
		count := func(bucket []byte, add func(*collectionCounts)) {
			if b := tx.Bucket(bucket); b != nil {
				c := b.Cursor()
				for k, _ := c.First(); k != nil; k, _ = c.Next() {
					if name, ok := statsCollection(k); ok {
						add(get(name))
					}
				}
			}
		}
		count(s.BucketNames.Rev, func(cc *collectionCounts) { cc.Revisions++ })
		count(s.BucketNames.IdxMeta, func(cc *collectionCounts) { cc.MetaIndices++ })
		return nil
	})
	if err != nil {
		return nil, err
	}
	if s.VectorStore != nil {
		if counts, cerr := s.VectorStore.CountByCollection(); cerr == nil {
			for name, n := range counts {
				get([]byte(name)).Embeddings = n
			}
		}
		if counts, cerr := s.VectorStore.CountChunksByCollection(); cerr == nil {
			for name, n := range counts {
				get([]byte(name)).Chunks = n
			}
		}
	}
	for _, cc := range snap.Collections {
		cc.Checksum = fmt.Sprintf("%08x", cc.checksum)
	}
	snap.ComputedAt, snap.Took = time.Now(), time.Since(start)
	return snap, nil
}

// statsCollection is the collection segment of "<tag>|<collection>|...",
// without allocating.
func statsCollection(key []byte) ([]byte, bool) {
	i := bytes.IndexByte(key, '|')
	if i < 0 {
		return nil, false
	}
	rest := key[i+1:]
	j := bytes.IndexByte(rest, '|')
	if j <= 0 {
		return nil, false
	}
	return rest[:j], true
}
