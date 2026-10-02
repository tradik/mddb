package binlog

import (
	"bufio"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"
)

// Binlog implements a binary replication log for leader-follower replication.
// The leader appends all BoltDB mutations (Put/Delete per bucket) to the binlog.
// Followers stream entries from a given LSN to replicate state.
type Binlog struct {
	mu     sync.Mutex
	file   *os.File
	writer *bufio.Writer
	path   string

	lsn       atomic.Uint64
	oldestLSN uint64 // oldest LSN still in the file
	fileSize  int64

	// Retention
	maxSize int64         // max binlog file size (default 256MB)
	maxAge  time.Duration // max retention (default 24h)

	// Real-time subscribers (followers tailing the log)
	subscribers map[string]chan *BinlogEntry
	subMu       sync.RWMutex

	// Periodic flush
	flusher   chan struct{}
	done      chan struct{}
	closeOnce sync.Once
}

// BinlogConfig holds configuration for the binlog
type BinlogConfig struct {
	Path    string        // file path (default: alongside DB file)
	MaxSize int64         // max segment size in bytes (default: 256MB)
	MaxAge  time.Duration // max retention time (default: 24h)
}

const (
	defaultBinlogMaxSize = 256 * 1024 * 1024 // 256MB
	defaultBinlogMaxAge  = 24 * time.Hour
	binlogBufferSize     = 256 * 1024 // 256KB write buffer
	binlogSubscriberCap  = 4096       // channel buffer for subscribers
	binlogFlushInterval  = 100 * time.Millisecond
)

// NewBinlog creates a new binlog at the given path.
// If dbPath is provided and config.Path is empty, the binlog is placed alongside the DB file.
func NewBinlog(dbPath string, cfg BinlogConfig) (*Binlog, error) {
	binlogPath := cfg.Path
	if binlogPath == "" {
		binlogPath = filepath.Join(filepath.Dir(dbPath), "mddb.binlog")
	}

	// #nosec G304 -- Path is constructed safely
	file, err := os.OpenFile(filepath.Clean(binlogPath), os.O_CREATE|os.O_RDWR|os.O_APPEND, 0600)
	if err != nil {
		return nil, fmt.Errorf("failed to open binlog: %w", err)
	}

	stat, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("failed to stat binlog: %w", err)
	}

	maxSize := cfg.MaxSize
	if maxSize <= 0 {
		maxSize = defaultBinlogMaxSize
	}
	maxAge := cfg.MaxAge
	if maxAge <= 0 {
		maxAge = defaultBinlogMaxAge
	}

	b := &Binlog{
		file:        file,
		writer:      bufio.NewWriterSize(file, binlogBufferSize),
		path:        binlogPath,
		fileSize:    stat.Size(),
		maxSize:     maxSize,
		maxAge:      maxAge,
		subscribers: make(map[string]chan *BinlogEntry),
		flusher:     make(chan struct{}, 1),
		done:        make(chan struct{}),
	}

	// Recover LSN from existing data
	if stat.Size() > 0 {
		if err := b.recoverLSN(); err != nil {
			_ = file.Close()
			return nil, fmt.Errorf("failed to recover binlog LSN: %w", err)
		}
	} else {
		b.oldestLSN = 0
	}

	// Start periodic flusher
	go b.periodicFlusher()

	return b, nil
}

// recoverLSN scans the binlog file to find the oldest and latest LSN
func (b *Binlog) recoverLSN() error {
	if _, err := b.file.Seek(0, io.SeekStart); err != nil {
		return err
	}

	// Entries are read with the same decoder ScanFrom uses, so a torn or
	// corrupt tail is recognised the same way: the end of what can be trusted.
	// It used to be fatal — a crash in the middle of an append left a file the
	// server refused to start with — or, when the tear fell inside the first
	// eight bytes, was kept: the file is opened for appending, so every later
	// entry landed after bytes no reader could get past, and followers stopped
	// receiving anything written after the crash.
	reader := bufio.NewReaderSize(b.file, binlogBufferSize)
	var firstLSN, lastLSN uint64
	var good int64
	for {
		entry, n, err := readEntry(reader)
		if err != nil {
			break
		}
		if firstLSN == 0 {
			firstLSN = entry.LSN
		}
		lastLSN = entry.LSN
		good += int64(n)
	}

	if good < b.fileSize {
		slog.Warn("binlog ends in an incomplete entry, most likely a write cut short by a crash; dropping it",
			"path", b.path, "keptBytes", good, "droppedBytes", b.fileSize-good, "lastLSN", lastLSN)
		if err := b.file.Truncate(good); err != nil {
			return fmt.Errorf("truncating the incomplete tail: %w", err)
		}
		b.fileSize = good
	}

	b.oldestLSN = firstLSN
	b.lsn.Store(lastLSN)

	// Seek back to end for appending
	if _, err := b.file.Seek(0, io.SeekEnd); err != nil {
		return err
	}
	return nil
}

// Append writes a new entry to the binlog. The LSN is assigned automatically.
// This should be called AFTER a successful BoltDB commit.
func (b *Binlog) Append(entry *BinlogEntry) error {
	b.mu.Lock()

	// Assign LSN
	newLSN := b.lsn.Add(1)
	entry.LSN = newLSN
	entry.Timestamp = time.Now().UnixNano()

	// Serialize
	data := MarshalBinlogEntry(entry)

	// Write to buffer
	n, err := b.writer.Write(data)
	if err != nil {
		b.mu.Unlock()
		return fmt.Errorf("failed to write binlog entry: %w", err)
	}
	b.fileSize += int64(n)

	if b.oldestLSN == 0 {
		b.oldestLSN = newLSN
	}

	// Trigger async flush
	select {
	case b.flusher <- struct{}{}:
	default:
	}

	b.mu.Unlock()

	// Notify subscribers (outside lock)
	b.notifySubscribers(entry)

	return nil
}

// AppendBatch writes multiple entries in a single lock acquisition.
// All entries get sequential LSNs.
func (b *Binlog) AppendBatch(entries []*BinlogEntry) error {
	if len(entries) == 0 {
		return nil
	}

	b.mu.Lock()

	now := time.Now().UnixNano()
	for _, entry := range entries {
		newLSN := b.lsn.Add(1)
		entry.LSN = newLSN
		entry.Timestamp = now

		data := MarshalBinlogEntry(entry)
		n, err := b.writer.Write(data)
		if err != nil {
			b.mu.Unlock()
			return fmt.Errorf("failed to write binlog batch entry: %w", err)
		}
		b.fileSize += int64(n)

		if b.oldestLSN == 0 {
			b.oldestLSN = newLSN
		}
	}

	// Trigger async flush
	select {
	case b.flusher <- struct{}{}:
	default:
	}

	b.mu.Unlock()

	// Notify subscribers
	for _, entry := range entries {
		b.notifySubscribers(entry)
	}

	return nil
}

// ReadFrom reads all entries with LSN > fromLSN into memory.
// Returns ErrBinlogLSNTooOld if the requested LSN is no longer in the binlog.
// For anything that may be large — a follower catching up — use ScanFrom.
func (b *Binlog) ReadFrom(fromLSN uint64) ([]*BinlogEntry, error) {
	var entries []*BinlogEntry
	err := b.ScanFrom(fromLSN, func(e *BinlogEntry) error {
		entries = append(entries, e)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return entries, nil
}

// Subscribe creates a channel that receives new binlog entries in real-time.
// Used by followers to tail the binlog.
func (b *Binlog) Subscribe(id string) <-chan *BinlogEntry {
	b.subMu.Lock()
	defer b.subMu.Unlock()

	ch := make(chan *BinlogEntry, binlogSubscriberCap)
	b.subscribers[id] = ch
	return ch
}

// Unsubscribe removes a subscriber.
func (b *Binlog) Unsubscribe(id string) {
	b.subMu.Lock()
	defer b.subMu.Unlock()

	if ch, ok := b.subscribers[id]; ok {
		close(ch)
		delete(b.subscribers, id)
	}
}

// CurrentLSN returns the current (latest) LSN
func (b *Binlog) CurrentLSN() uint64 {
	return b.lsn.Load()
}

// OldestLSN returns the oldest LSN still in the binlog
func (b *Binlog) OldestLSN() uint64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.oldestLSN
}

// Rotate truncates the binlog, keeping only entries from keepFromLSN onwards.
// If keepFromLSN is 0, truncates everything.
func (b *Binlog) Rotate(keepFromLSN uint64) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	// Flush first
	if err := b.flush(); err != nil {
		return err
	}

	if keepFromLSN == 0 {
		// Full truncate
		return b.truncate()
	}

	// Read entries to keep
	f, err := os.Open(b.path)
	if err != nil {
		return err
	}

	allData, err := io.ReadAll(f)
	_ = f.Close()
	if err != nil {
		return err
	}

	// Find entries to keep
	var keepData []byte
	pos := 0
	for pos < len(allData) {
		entry, n, err := UnmarshalBinlogEntry(allData[pos:])
		if err != nil {
			break
		}
		if entry.LSN >= keepFromLSN {
			keepData = allData[pos:]
			break
		}
		pos += n
	}

	// Rewrite file
	if err := b.file.Close(); err != nil {
		return err
	}

	// #nosec G304 -- Path is constructed safely
	file, err := os.OpenFile(filepath.Clean(b.path), os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}

	b.file = file
	b.writer = bufio.NewWriterSize(file, binlogBufferSize)

	if len(keepData) > 0 {
		n, err := b.writer.Write(keepData)
		if err != nil {
			return err
		}
		b.fileSize = int64(n)
		b.oldestLSN = keepFromLSN
	} else {
		b.fileSize = 0
		b.oldestLSN = 0
	}

	return b.flush()
}

// Close flushes and closes the binlog. It is safe to call Close more than once.
func (b *Binlog) Close() error {
	var closeErr error
	b.closeOnce.Do(func() {
		close(b.done)

		b.mu.Lock()
		defer b.mu.Unlock()

		if err := b.flush(); err != nil {
			closeErr = err
			return
		}

		// Close all subscriber channels
		b.subMu.Lock()
		for id, ch := range b.subscribers {
			close(ch)
			delete(b.subscribers, id)
		}
		b.subMu.Unlock()

		closeErr = b.file.Close()
	})
	return closeErr
}

// Stats returns binlog statistics
func (b *Binlog) Stats() BinlogStats {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.subMu.RLock()
	subCount := len(b.subscribers)
	b.subMu.RUnlock()

	return BinlogStats{
		CurrentLSN:  b.lsn.Load(),
		OldestLSN:   b.oldestLSN,
		FileSize:    b.fileSize,
		Subscribers: subCount,
		Path:        b.path,
	}
}

// BinlogStats contains binlog statistics
type BinlogStats struct {
	CurrentLSN  uint64 `json:"current_lsn"`
	OldestLSN   uint64 `json:"oldest_lsn"`
	FileSize    int64  `json:"file_size"`
	Subscribers int    `json:"subscribers"`
	Path        string `json:"path"`
}

// flush flushes the write buffer and syncs to disk
func (b *Binlog) flush() error {
	if err := b.writer.Flush(); err != nil {
		return fmt.Errorf("failed to flush binlog: %w", err)
	}
	if err := b.file.Sync(); err != nil {
		return fmt.Errorf("failed to sync binlog: %w", err)
	}
	return nil
}

// truncate clears the entire binlog
func (b *Binlog) truncate() error {
	if err := b.file.Close(); err != nil {
		return err
	}

	// #nosec G304 -- Path is constructed safely
	file, err := os.OpenFile(filepath.Clean(b.path), os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0600)
	if err != nil {
		return fmt.Errorf("failed to truncate binlog: %w", err)
	}

	b.file = file
	b.writer = bufio.NewWriterSize(file, binlogBufferSize)
	b.fileSize = 0
	b.oldestLSN = 0

	return nil
}

// periodicFlusher flushes the binlog periodically
func (b *Binlog) periodicFlusher() {
	ticker := time.NewTicker(binlogFlushInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			b.mu.Lock()
			_ = b.flush()
			b.mu.Unlock()
		case <-b.flusher:
			b.mu.Lock()
			_ = b.flush()
			b.mu.Unlock()
		case <-b.done:
			return
		}
	}
}

// notifySubscribers sends an entry to all active subscribers
func (b *Binlog) notifySubscribers(entry *BinlogEntry) {
	b.subMu.RLock()
	defer b.subMu.RUnlock()

	for _, ch := range b.subscribers {
		select {
		case ch <- entry:
		default:
			// Subscriber is too slow, drop entry.
			// The follower will need to re-sync from file.
		}
	}
}

// ErrBinlogLSNTooOld is returned when the requested LSN is no longer in the binlog
var ErrBinlogLSNTooOld = fmt.Errorf("requested LSN is older than the binlog's oldest entry; full snapshot required")
