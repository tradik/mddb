package binlog

import (
	"bufio"
	"fmt"
	"io"
	"log/slog"
	"time"
)

// Opening a log, and starting a new one.
//
// The database commits first and the binlog is appended afterwards, buffered,
// with the sync about 100 ms behind (MDDB_BINLOG_SYNC=interval) or before the
// append returns (commit). Either way a process that dies can leave commits
// in the database that are not in the log — the buffered ones, and any whose
// append had not been reached yet. Nothing in the log says which. So a leader
// whose log was not closed cleanly does not continue it: it starts a new log,
// under a new ID and far enough ahead in LSNs that no position a follower
// holds can be mistaken for one in it, and every follower takes a snapshot.

// resetJump is how far a new log starts ahead of the old one. Followers are
// sent entries before they are synced, so they can hold LSNs the restarted
// leader never saw on disk; the gap keeps the new log's LSNs clear of them.
const resetJump = 1 << 32

// tailScan is what reading an existing log file found.
type tailScan struct {
	first, last uint64
	firstTS     int64
	good        int64 // bytes of whole entries
}

// openLog reads what is on disk and decides whether the log continues.
func (b *Binlog) openLog() error {
	stat, err := b.file.Stat()
	if err != nil {
		return err
	}
	scan, err := b.scanExisting(stat.Size())
	if err != nil {
		return err
	}
	meta, status, err := loadMeta(b.metaPath())
	if err != nil {
		return err
	}

	last := meta.BaseLSN
	b.baseLSN = meta.BaseLSN
	if scan.first > 0 {
		b.baseLSN, last = scan.first-1, scan.last
	}
	b.lsn.Store(last)
	b.synced = last
	b.firstTS = scan.firstTS
	b.fileSize = scan.good
	// A follower's copy without an ID is one the follower has not yet
	// matched to a leader's log; it keeps 0 until it does.
	if b.logID = parseLogID(meta.LogID); b.logID == 0 && !b.cfg.Replica {
		b.logID = newLogID()
	}
	b.terms = meta.Terms
	if n := len(b.terms); n > 0 {
		b.term = b.terms[n-1].Term
	}

	why := b.untrusted(meta, status, last)
	b.dirty = why != ""
	if b.cfg.Replica {
		why = "" // reconciled with what the follower applied instead
	}
	if why != "" {
		slog.Warn("binlog cannot be continued; starting a new one — followers will resynchronise from a snapshot",
			"path", b.path, "reason", why, "lastLSN", last)
		if err := b.startNewLog(max(last, meta.BaseLSN, meta.CleanLSN) + resetJump); err != nil {
			return err
		}
	}
	return b.saveMeta(stateOpen)
}

// untrusted says why a leader's log cannot be continued, or "" when it can.
// A follower's copy is not started again for it — it is reconciled with what
// the follower applied — but is marked Dirty. A log without a sidecar was
// written before 2.19.8, which kept no such record; it is taken as it is, as
// that release would have.
func (b *Binlog) untrusted(meta logMeta, status metaStatus, last uint64) string {
	switch {
	case status == metaMissing:
		return ""
	case status == metaUnreadable:
		return "its sidecar is unreadable"
	case meta.State != stateClean:
		return "it was not closed cleanly"
	case meta.CleanLSN != last:
		return fmt.Sprintf("it was closed at LSN %d but ends at %d", meta.CleanLSN, last)
	}
	return ""
}

// scanExisting reads the log file through, dropping an incomplete tail.
func (b *Binlog) scanExisting(size int64) (tailScan, error) {
	var scan tailScan

	// Entries are read with the same decoder ScanFrom uses, so a torn or
	// corrupt tail is recognised the same way: the end of what can be trusted.
	// It used to be fatal — a crash in the middle of an append left a file the
	// server refused to start with — or, when the tear fell inside the first
	// eight bytes, was kept: the file is opened for appending, so every later
	// entry landed after bytes no reader could get past, and followers stopped
	// receiving anything written after the crash.
	reader := bufio.NewReaderSize(io.NewSectionReader(b.file, 0, size), binlogBufferSize)
	for {
		entry, n, err := readEntry(reader)
		if err != nil {
			break
		}
		if scan.first == 0 {
			scan.first, scan.firstTS = entry.LSN, entry.Timestamp
		}
		scan.last = entry.LSN
		scan.good += int64(n)
		b.recent.add(entry.LSN, entry.Timestamp)
	}

	if scan.good < size {
		slog.Warn("binlog ends in an incomplete entry, most likely a write cut short by a crash; dropping it",
			"path", b.path, "keptBytes", scan.good, "droppedBytes", size-scan.good, "lastLSN", scan.last)
		// Through the path, not b.file: on Windows a file opened with
		// O_APPEND has append access only, and truncating through it is
		// refused. The second handle shares the file, as Go opens it.
		if err := osTruncate(b.path, scan.good); err != nil {
			return scan, fmt.Errorf("truncating the incomplete tail: %w", err)
		}
	}
	return scan, nil
}

// startNewLog empties the file and continues at base under a new log ID.
// The caller holds b.mu (or is the only user, at open) and saves the sidecar.
func (b *Binlog) startNewLog(base uint64) error {
	if err := b.restartAt(base, newLogID()); err != nil {
		return err
	}
	// A new log has no history but the term its writes will carry.
	b.terms = nil
	if b.term != 0 {
		b.terms = []TermStart{{Term: b.term, Start: base + 1}}
	}
	return nil
}

// restartAt empties the file and continues after base as log id.
func (b *Binlog) restartAt(base, id uint64) error {
	b.writer.Reset(b.file) // whatever is buffered belongs to the old log
	if err := osTruncate(b.path, 0); err != nil {
		return fmt.Errorf("failed to truncate binlog: %w", err)
	}
	b.fileSize, b.firstTS = 0, 0
	b.baseLSN, b.logID = base, id
	b.lsn.Store(base)
	b.synced = base
	b.recent = timeRing{}
	b.failed = nil
	// Whatever the database held that the old log did not, followers now
	// take from a snapshot.
	b.dirty, b.unlogged = false, false
	return nil
}

// Reset starts a new log: the file is emptied, the LSN jumps ahead and the
// log gets a new ID, so every follower takes a snapshot. For when the
// database stops being what the log describes — a restore.
func (b *Binlog) Reset() error {
	b.scanMu.Lock() // no reader is half-way through the old file
	defer b.scanMu.Unlock()
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := b.startNewLog(b.lsn.Load() + resetJump); err != nil {
		return err
	}
	return b.saveMeta(stateOpen)
}

// Rebase makes this the log id continuing after lsn, with nothing in it: a
// follower's copy after a snapshot, or after it was found not to match what
// the follower applied.
func (b *Binlog) Rebase(lsn, id uint64) error {
	b.scanMu.Lock()
	defer b.scanMu.Unlock()
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := b.restartAt(lsn, id); err != nil {
		return err
	}
	b.terms = nil
	return b.saveMeta(stateOpen)
}

// Rotate drops the entries before keepFromLSN. Rotate(0) starts a new log
// (see Reset).
func (b *Binlog) Rotate(keepFromLSN uint64) error {
	if keepFromLSN == 0 {
		return b.Reset()
	}
	return b.dropBefore(keepFromLSN)
}

// LogID identifies this log; it changes when a new log is started.
func (b *Binlog) LogID() uint64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.logID
}

// BaseLSN is the LSN before the first entry in the file: a reader at or
// after it can continue from the file, one before it cannot.
func (b *Binlog) BaseLSN() uint64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.baseLSN
}

// Stats returns binlog statistics
func (b *Binlog) Stats() BinlogStats {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.subMu.RLock()
	subCount := len(b.subscribers)
	b.subMu.RUnlock()

	return BinlogStats{
		CurrentLSN:    b.lsn.Load(),
		OldestLSN:     b.oldestLocked(),
		BaseLSN:       b.baseLSN,
		FileSize:      b.fileSize,
		MaxSize:       b.cfg.MaxSize,
		MaxAgeSeconds: int64(b.cfg.MaxAge / time.Second),
		Subscribers:   subCount,
		Path:          b.path,
		LogID:         FormatLogID(b.logID),
		Sync:          string(b.cfg.Sync),
	}
}

// BinlogStats contains binlog statistics
type BinlogStats struct {
	CurrentLSN    uint64 `json:"current_lsn"`
	OldestLSN     uint64 `json:"oldest_lsn"`
	BaseLSN       uint64 `json:"base_lsn"`
	FileSize      int64  `json:"file_size"`
	MaxSize       int64  `json:"max_size"`
	MaxAgeSeconds int64  `json:"max_age_seconds"`
	Subscribers   int    `json:"subscribers"`
	Path          string `json:"path"`
	LogID         string `json:"log_id"`
	Sync          string `json:"sync"`
}
