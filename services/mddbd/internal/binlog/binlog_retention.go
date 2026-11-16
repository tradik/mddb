package binlog

import (
	"bufio"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"time"
)

// Retention: keeping the log within its size and age.
//
// MaxSize and MaxAge were stored and never read, so the file grew for as long
// as the server ran. They are enforced here. When the file passes MaxSize its
// oldest entries are dropped until half of MaxSize is left; entries older
// than MaxAge are dropped whatever the size.
//
// An entry a follower has not confirmed is kept past both limits, so that a
// follower that is merely behind catches up from the log — up to HardMaxSize.
// Beyond that the leader's disk matters more: the entries go, and that
// follower is sent a snapshot when it asks for them (ErrBinlogLSNTooOld).
//
// The file is one file, so dropping its head means writing its tail to a new
// file and renaming it into place. The tail that existed when retention
// started is copied without holding up appends; only what was appended
// meanwhile is copied under the lock.

// RetentionFloorFunc reports the lowest LSN confirmed by the followers whose
// entries are being kept, and whether there is such a follower at all.
type RetentionFloorFunc func() (lsn uint64, held bool)

// SetRetentionFloor tells retention which entries followers still need.
func (b *Binlog) SetRetentionFloor(fn RetentionFloorFunc) {
	b.floor.Store(&fn)
}

// RetentionResult is what one retention pass removed.
type RetentionResult struct {
	DroppedThrough uint64 // last LSN removed; 0 when nothing was
	DroppedBytes   int64
	Forced         bool // entries a follower had not confirmed were removed
}

// ageSlack spaces out age retention: it runs once the oldest entry is
// MaxAge/ageSlack past due, not for every entry that crosses the line.
const ageSlack = 24

// cutPoint is where the kept part of the file begins.
type cutPoint struct {
	offset      int64  // bytes dropped from the head
	lastDropped uint64 // LSN of the last entry dropped
	keepTS      int64  // timestamp of the first entry kept, 0 when none is
	forced      bool
}

// dropRule decides whether the entry with remaining bytes from it to the end
// of the file is dropped, and whether a follower still needed it.
type dropRule func(e *BinlogEntry, remaining int64) (drop, forced bool)

// EnforceRetention applies the size and age limits once. It does nothing
// while a reader is in the file; the next pass does it.
func (b *Binlog) EnforceRetention() (RetentionResult, error) {
	return b.enforce(time.Now())
}

func (b *Binlog) enforce(now time.Time) (RetentionResult, error) {
	if !b.scanMu.TryLock() {
		return RetentionResult{}, nil
	}
	defer b.scanMu.Unlock()

	size, rule := b.retentionRule(now)
	if rule == nil {
		return RetentionResult{}, nil
	}
	cut, err := b.planCut(size, rule)
	if err == nil && cut.offset > 0 {
		err = b.rewriteFrom(cut, size)
	}
	if err != nil {
		slog.Warn("binlog retention failed", "path", b.path, "err", err)
		return RetentionResult{}, err
	}
	if cut.offset == 0 {
		return RetentionResult{}, nil
	}
	res := RetentionResult{DroppedThrough: cut.lastDropped, DroppedBytes: cut.offset, Forced: cut.forced}
	slog.Info("binlog retention dropped the oldest entries", "path", b.path,
		"droppedThroughLSN", res.DroppedThrough, "droppedBytes", res.DroppedBytes)
	if res.Forced {
		slog.Warn("binlog passed its hard size limit: entries a follower had not confirmed were dropped; "+
			"a follower behind this LSN resynchronises from a snapshot",
			"path", b.path, "droppedThroughLSN", res.DroppedThrough, "hardMaxSize", b.cfg.HardMaxSize)
	}
	return res, nil
}

// retentionRule is the file's size and the rule for this pass, nil when
// neither limit is passed.
func (b *Binlog) retentionRule(now time.Time) (int64, dropRule) {
	b.mu.Lock()
	stopped := b.closed || b.flush() != nil
	size, firstTS := b.fileSize, b.firstTS
	b.mu.Unlock()

	ageCutoff := now.Add(-b.cfg.MaxAge).UnixNano()
	over := size > b.cfg.MaxSize
	aged := firstTS != 0 && firstTS < ageCutoff-int64(b.cfg.MaxAge/ageSlack)
	if stopped || (!over && !aged) {
		return size, nil
	}

	floor, held := uint64(0), false
	if fn := b.floor.Load(); fn != nil {
		floor, held = (*fn)()
	}
	return size, func(e *BinlogEntry, remaining int64) (bool, bool) {
		needed := held && e.LSN > floor
		if remaining > b.cfg.HardMaxSize {
			return true, needed
		}
		due := (over && remaining > b.cfg.MaxSize/2) || e.Timestamp < ageCutoff
		return due && !needed, false
	}
}

// planCut reads the first size bytes of the file from the start and finds
// where the entries the rule keeps begin.
func (b *Binlog) planCut(size int64, rule dropRule) (cutPoint, error) {
	var cut cutPoint
	f, err := os.Open(b.path)
	if err != nil {
		return cut, err
	}
	defer func() { _ = f.Close() }()

	r := bufio.NewReaderSize(io.NewSectionReader(f, 0, size), binlogBufferSize)
	for {
		e, n, err := readEntry(r)
		if err != nil {
			return cut, nil // everything goes
		}
		drop, forced := rule(e, size-cut.offset)
		if !drop {
			cut.keepTS = e.Timestamp
			return cut, nil
		}
		cut.forced = cut.forced || forced
		cut.offset += int64(n)
		cut.lastDropped = e.LSN
	}
}

// rewriteFrom replaces the file with its content from cut on. size is the
// length the file had when the cut was planned.
func (b *Binlog) rewriteFrom(cut cutPoint, size int64) error {
	tmp := b.path + ".compact"
	// #nosec G304 -- next to the binlog, whose path the operator configured
	dst, err := osOpenFile(filepath.Clean(tmp), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	src, err := os.Open(b.path)
	if err == nil {
		err = copyRange(dst, src, cut.offset, size)
	}
	if err == nil {
		err = b.swapIn(dst, src, cut, size)
	}
	if err != nil {
		closeQuietly(src)
		_ = dst.Close()
		_ = os.Remove(tmp)
	}
	return err
}

// swapIn copies what was appended since the plan, then puts the new file in
// the old one's place. On success it has closed dst and src.
func (b *Binlog) swapIn(dst, src *os.File, cut cutPoint, size int64) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return ErrBinlogClosed
	}
	err := b.flush()
	if err == nil {
		err = copyRange(dst, src, size, b.fileSize)
	}
	if err == nil {
		err = syncFile(dst)
	}
	if err != nil {
		return err
	}
	// The old file is replaced from here. Every handle on it is closed
	// first: Windows does not rename over an open file.
	_ = dst.Close()
	_ = src.Close()
	_ = b.file.Close()
	renameErr := osRename(dst.Name(), b.path)
	// #nosec G304 -- the binlog's own path
	file, err := osOpenFile(filepath.Clean(b.path), os.O_CREATE|os.O_RDWR|os.O_APPEND, 0600)
	if err != nil {
		return b.fail(err)
	}
	b.file = file
	b.writer.Reset(file)
	if renameErr != nil {
		return renameErr
	}
	b.fileSize -= cut.offset
	b.baseLSN = cut.lastDropped
	b.firstTS = cut.keepTS
	if b.firstTS == 0 {
		// Everything planned was dropped; what is left was appended
		// meanwhile, and its first timestamp is in the ring.
		b.firstTS, _ = b.recent.at(cut.lastDropped + 1)
	}
	return b.saveMeta(stateOpen)
}

// dropBefore removes the entries before keepFrom, waiting for readers.
func (b *Binlog) dropBefore(keepFrom uint64) error {
	b.scanMu.Lock()
	defer b.scanMu.Unlock()

	b.mu.Lock()
	err := b.flush()
	size := b.fileSize
	b.mu.Unlock()
	if err != nil {
		return err
	}
	cut, err := b.planCut(size, func(e *BinlogEntry, _ int64) (bool, bool) {
		return e.LSN < keepFrom, false
	})
	if err != nil || cut.offset == 0 {
		return err
	}
	return b.rewriteFrom(cut, size)
}

// retentionLoop runs retention until the log is closed.
func (b *Binlog) retentionLoop() {
	ticker := time.NewTicker(b.cfg.RetentionInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			_, _ = b.EnforceRetention()
		case <-b.done:
			return
		}
	}
}

func copyRange(dst io.Writer, src io.ReaderAt, from, to int64) error {
	_, err := io.Copy(dst, io.NewSectionReader(src, from, to-from))
	return err
}

func closeQuietly(f *os.File) {
	if f != nil {
		_ = f.Close()
	}
}
