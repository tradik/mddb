package main

import (
	"encoding/binary"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	"mddb/internal/envconf"

	bolt "go.etcd.io/bbolt"
)

// Opening a large database (#333).
//
// Without MDDB_FREELIST_SYNC, bbolt does not store its list of free pages;
// every open rebuilds it by walking every page of the file, one page fault at
// a time. On a 101 GB database that took 101 minutes, at the ~17 MB/s a
// virtual disk manages for random 4 KiB reads, and it grows with the file.
//
// Two things cut it without making commits slower:
//
//   - A clean shutdown writes the freelist once. bbolt reads NoFreelistSync
//     at each commit, so one commit with it off stores the list, and the next
//     open reads one page. Any commit after that start clears the stored list
//     again, so after a crash the open still walks — which is what keeps it
//     correct.
//   - When the walk is unavoidable and the file fits in memory, the file is
//     read front to back first. Sequential reads run at the disk's bandwidth
//     rather than its random-read latency, and the walk then finds every page
//     in the page cache.
//
// Serving reads before the walk ends is not possible from outside bbolt: a
// read-only handle holds a shared lock, and the read-write open that does
// the walk needs an exclusive one.

// boltMeta is what an open needs to know before bbolt starts: the newest
// valid meta page of the file.
type boltMeta struct {
	pageSize uint32
	freelist uint64 // page of the stored freelist, or noFreelist
	pgid     uint64 // high-water mark: pages in use, including free ones
	txid     uint64
}

const (
	boltMagic       = 0xED0CDAED
	boltVersion     = 2
	noFreelist      = ^uint64(0)
	boltPageHeader  = 16 // id, flags, count, overflow
	boltMetaSumSpan = 56 // bytes covered by the meta checksum
)

// needsWalk reports whether opening this file read-write rebuilds the
// freelist by walking every page.
func (m boltMeta) needsWalk() bool { return m.freelist == noFreelist }

// bytesInUse is the part of the file the walk can touch.
func (m boltMeta) bytesInUse() int64 {
	if m.pageSize == 0 || m.pgid > math.MaxInt64/uint64(m.pageSize) {
		return math.MaxInt64
	}
	return int64(m.pgid * uint64(m.pageSize)) // #nosec G115 -- bounded above
}

// readBoltMeta reads the two meta pages and returns the newer valid one.
func readBoltMeta(path string) (boltMeta, error) {
	f, err := os.Open(filepath.Clean(path)) // #nosec G304 -- the operator's database path
	if err != nil {
		return boltMeta{}, err
	}
	defer func() { _ = f.Close() }()

	first := make([]byte, boltPageHeader+64)
	if _, err := io.ReadFull(f, first); err != nil {
		return boltMeta{}, err
	}
	best, ok := parseBoltMeta(first)
	pageSize := int64(4096)
	if ok {
		pageSize = int64(best.pageSize)
	}
	second := make([]byte, boltPageHeader+64)
	if _, err := f.ReadAt(second, pageSize); err == nil {
		if m, ok2 := parseBoltMeta(second); ok2 && (!ok || m.txid > best.txid) {
			best, ok = m, true
		}
	}
	if !ok {
		return boltMeta{}, errors.New("no valid bbolt meta page")
	}
	return best, nil
}

func parseBoltMeta(page []byte) (boltMeta, bool) {
	b := page[boltPageHeader:]
	le := binary.LittleEndian
	if le.Uint32(b[0:]) != boltMagic || le.Uint32(b[4:]) != boltVersion {
		return boltMeta{}, false
	}
	h := fnv.New64a()
	_, _ = h.Write(b[:boltMetaSumSpan])
	if h.Sum64() != le.Uint64(b[boltMetaSumSpan:]) {
		return boltMeta{}, false
	}
	return boltMeta{pageSize: le.Uint32(b[8:]), freelist: le.Uint64(b[32:]),
		pgid: le.Uint64(b[40:]), txid: le.Uint64(b[48:])}, true
}

// shouldPrewarm decides whether to read the file through before opening it.
// MDDB_PREWARM is auto (default), on or off. Auto reads it only when the
// open is going to walk and the file fits in half the memory available, so
// the read cannot push itself out of the cache before the walk uses it.
func shouldPrewarm(meta boltMeta, fileSize int64, availableMemory uint64) bool {
	switch strings.ToLower(envconf.String("MDDB_PREWARM", "auto")) {
	case "on", "true":
		return true
	case "off", "false":
		return false
	}
	return meta.needsWalk() && availableMemory > 0 && uint64(fileSize) <= availableMemory/2 // #nosec G115 -- a file size is never negative
}

// prewarmFile reads path from start to end twice, reporting progress, so the
// pages are in the page cache when bbolt walks them. One pass is not enough
// when memory is already full of cache: pages read once sit on the inactive
// list and the rest of the pass evicts them (measured: 52-89% still cached
// after a 2.5 GB read, and an open of 7-27 s instead of 0.4 s). The second
// pass reads from the cache and promotes the pages to the active list.
func prewarmFile(path string, progress func(done int64)) error {
	for pass := 0; pass < 2; pass++ {
		p := progress
		if pass == 1 {
			p = nil
		}
		if err := readThrough(path, p); err != nil {
			return err
		}
	}
	return nil
}

func readThrough(path string, progress func(done int64)) error {
	f, err := os.Open(filepath.Clean(path)) // #nosec G304 -- the operator's database path
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	buf := make([]byte, 8<<20)
	var done int64
	for {
		n, err := f.Read(buf)
		done += int64(n)
		if progress != nil {
			progress(done)
		}
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

// persistFreelist stores the freelist with one commit, so the next open of
// a cleanly closed database reads it instead of walking every page. It is
// the last write of a shutdown; a write after it only costs the next start
// its shortcut.
func persistFreelist(db *bolt.DB) error {
	if db == nil || db.IsReadOnly() || !db.NoFreelistSync {
		return nil
	}
	// The field is read by Commit under the writer lock this Update holds.
	if err := db.Update(func(*bolt.Tx) error {
		db.NoFreelistSync = false
		return nil
	}); err != nil {
		return fmt.Errorf("storing the freelist: %w", err)
	}
	slog.Info("freelist stored; the next start reads it instead of walking the file")
	return nil
}

// openDatabase opens the bolt file, reporting the walk's progress on the
// startup clock, and reads the file into the cache first when that is
// faster. It reports whether the open walked the file.
func openDatabase(clock *startupClock, path string, opts *bolt.Options) (*bolt.DB, bool, error) {
	meta, metaErr := readBoltMeta(path)
	walk := metaErr == nil && meta.needsWalk()
	if walk {
		slog.Info("the freelist is not stored, so opening walks every page",
			"why", "the last shutdown was not clean, or the server ran with MDDB_FREELIST_SYNC=false and an older version",
			"bytes", meta.bytesInUse())
	}

	prewarmed := false
	if fi, err := os.Stat(path); walk && err == nil && shouldPrewarm(meta, fi.Size(), availableMemory()) {
		clock.begin("reading database into cache")
		size := fi.Size()
		if err := prewarmFile(path, func(done int64) { clock.setProgress(done, size) }); err != nil {
			slog.Warn("reading the database into the cache failed; opening without it", "err", err)
		}
		prewarmed = true
		clock.begin("opening database")
	}

	stop := make(chan struct{})
	if walk && !prewarmed {
		go sampleWalkProgress(clock, meta.bytesInUse(), stop)
	}
	db, err := openBolt(path, opts)
	close(stop)
	return db, walk, err
}

// sampleWalkProgress reports the bytes the walk has read from storage. The
// walk reads each page in use once, so their total is the end.
func sampleWalkProgress(clock *startupClock, total int64, stop <-chan struct{}) {
	base, ok := storageReadBytes()
	if !ok || total <= 0 {
		return
	}
	t := time.NewTicker(2 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
			if cur, ok := storageReadBytes(); ok {
				clock.setProgress(min(cur-base, total*99/100), total) // an estimate: never 100%
			}
		}
	}
}
