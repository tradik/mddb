package binlog

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"
)

// Binlog implements a binary replication log for leader-follower replication.
// The leader appends all BoltDB mutations (Put/Delete per bucket) to the binlog.
// Followers stream entries from a given LSN to replicate state, and keep the
// entries they apply in a log of their own under the leader's LSNs.
//
// Entries in the file are consecutive: the first one is baseLSN+1 and the last
// one is the current LSN. What the file cannot say about itself — which log it
// is, where an empty one continues, whether it was closed cleanly — is in the
// sidecar next to it (binlog_meta.go).
type Binlog struct {
	mu     sync.Mutex
	file   *os.File
	writer *bufio.Writer
	path   string
	cfg    BinlogConfig

	lsn      atomic.Uint64
	baseLSN  uint64 // LSN of the entry before the first one in the file
	firstTS  int64  // timestamp of the oldest entry in the file, 0 when unknown
	fileSize int64
	logID    uint64
	failed   error // first write error; the log accepts nothing after it
	closed   bool
	spoiled  bool // an append arrived after Close and took the clean mark back
	dirty    bool // found not closed cleanly at open (binlog_recover.go)
	unlogged bool // the gate refused an append made after its commit
	recent   timeRing
	synced   uint64 // last LSN known to be on disk

	// Election terms (GO-051, binlog_terms.go): the term the node's own
	// appends carry, and where each term begins in the log.
	term  uint64
	terms []TermStart

	// gate, when set, refuses the node's own appends (a cluster follower);
	// ackWait, when set, holds a logged write until enough followers have it.
	gate    atomic.Pointer[WriteGate]
	ackWait atomic.Pointer[AckWaiter]
	// entryGate, when set, refuses a logged write by what it writes (a
	// collection placed in another group, GO-052).
	entryGate atomic.Pointer[EntryGate]
	// pending holds the first LSN of each logged write whose transaction
	// has not returned yet (StableLSN).
	pending map[uint64]struct{}

	// scanMu keeps the file from being rewritten under a reader: scans hold
	// it shared, retention takes it exclusively or leaves the file alone.
	scanMu sync.RWMutex
	floor  atomic.Pointer[RetentionFloorFunc]

	// Real-time subscribers (followers tailing the log)
	subscribers map[string]chan *BinlogEntry
	subMu       sync.RWMutex

	// Periodic flush
	flusher   chan struct{}
	done      chan struct{}
	closeOnce sync.Once
}

// SyncMode is when an appended entry reaches the disk.
type SyncMode string

const (
	// SyncInterval returns from an append once the entry is buffered and
	// syncs in the background, about every 100 ms. A crash can lose the
	// entries of that window; the next start notices and starts a new log.
	SyncInterval SyncMode = "interval"
	// SyncCommit syncs before an append returns.
	SyncCommit SyncMode = "commit"
)

// BinlogConfig holds configuration for the binlog
type BinlogConfig struct {
	Path    string        // file path (default: alongside DB file)
	MaxSize int64         // size retention trims the file at (default: 256MB)
	MaxAge  time.Duration // age retention drops entries at (default: 24h)
	// HardMaxSize is the size entries a follower has not confirmed are kept
	// up to (default: 4 × MaxSize). Past it they are dropped and that
	// follower takes a snapshot.
	HardMaxSize int64
	Sync        SyncMode // default: SyncInterval
	// Replica marks a follower's copy of the leader's log: its entries keep
	// the leader's LSNs, and an unclean stop does not start a new log — the
	// follower reconciles it with what it applied (Rebase).
	Replica bool
	// RetentionInterval is how often retention runs (default: 1 minute;
	// negative: only when EnforceRetention is called).
	RetentionInterval time.Duration
}

const (
	defaultBinlogMaxSize     = 256 * 1024 * 1024 // 256MB
	defaultBinlogMaxAge      = 24 * time.Hour
	defaultRetentionInterval = time.Minute
	hardMaxFactor            = 4
	binlogBufferSize         = 256 * 1024 // 256KB write buffer
	binlogSubscriberCap      = 4096       // channel buffer for subscribers
	binlogFlushInterval      = 100 * time.Millisecond
)

// Errors of the append path.
var (
	// ErrBinlogLSNTooOld is returned when the requested LSN is no longer in the binlog
	ErrBinlogLSNTooOld = errors.New("requested LSN is older than the binlog's oldest entry; full snapshot required")
	// ErrBinlogClosed is returned by an append after Close.
	ErrBinlogClosed = errors.New("binlog is closed")
	// ErrLSNGap is returned when a replicated entry does not continue the log.
	ErrLSNGap = errors.New("replicated entry does not follow the log's last LSN")
)

// withDefaults fills in what the caller left unset.
func (cfg BinlogConfig) withDefaults() BinlogConfig {
	if cfg.MaxSize <= 0 {
		cfg.MaxSize = defaultBinlogMaxSize
	}
	if cfg.MaxAge <= 0 {
		cfg.MaxAge = defaultBinlogMaxAge
	}
	if cfg.HardMaxSize < cfg.MaxSize {
		cfg.HardMaxSize = cfg.MaxSize * hardMaxFactor
	}
	if cfg.Sync != SyncCommit {
		cfg.Sync = SyncInterval
	}
	if cfg.RetentionInterval == 0 {
		cfg.RetentionInterval = defaultRetentionInterval
	}
	return cfg
}

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

	b := &Binlog{
		file:        file,
		writer:      bufio.NewWriterSize(file, binlogBufferSize),
		path:        binlogPath,
		cfg:         cfg.withDefaults(),
		subscribers: make(map[string]chan *BinlogEntry),
		pending:     make(map[uint64]struct{}),
		flusher:     make(chan struct{}, 1),
		done:        make(chan struct{}),
	}
	if err := b.openLog(); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("failed to recover binlog: %w", err)
	}

	go b.periodicFlusher()
	if b.cfg.RetentionInterval > 0 {
		go b.retentionLoop()
	}
	return b, nil
}

// Append writes a new entry to the binlog. The LSN is assigned automatically.
// It is for writes made outside a transaction Logged runs; such a write is
// committed before it is appended, so two of them racing on one key can
// reach the log in the other order than the database (see Logged).
func (b *Binlog) Append(entry *BinlogEntry) error {
	return b.AppendBatch([]*BinlogEntry{entry})
}

// AppendBatch writes multiple entries in a single lock acquisition.
// All entries get sequential LSNs.
func (b *Binlog) AppendBatch(entries []*BinlogEntry) error {
	if len(entries) == 0 {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := b.checkGate(); err != nil {
		b.unlogged = true
		return err
	}
	b.number(entries)
	return b.writeLocked(entries, b.cfg.Sync == SyncCommit)
}

// number gives entries the next LSNs, the node's term and the time. The
// caller holds b.mu.
func (b *Binlog) number(entries []*BinlogEntry) {
	now := time.Now().UnixNano()
	next := b.lsn.Load()
	for _, entry := range entries {
		next++
		entry.LSN = next
		entry.Term = b.term
		entry.Timestamp = now
	}
}

// writeLocked puts entries, LSNs already set, at the end of the log and
// hands them to the subscribers, in LSN order. sync writes them through to
// the disk before it returns. The caller holds b.mu.
func (b *Binlog) writeLocked(entries []*BinlogEntry, sync bool) error {
	if b.closed {
		b.spoilCleanClose()
		return ErrBinlogClosed
	}
	if b.failed != nil {
		return b.failed
	}
	for _, entry := range entries {
		n, err := b.writer.Write(MarshalBinlogEntry(entry))
		if err != nil {
			return b.fail(fmt.Errorf("failed to write binlog entry: %w", err))
		}
		b.fileSize += int64(n)
		b.lsn.Store(entry.LSN)
		if b.firstTS == 0 {
			b.firstTS = entry.Timestamp
		}
		b.recent.add(entry.LSN, entry.Timestamp)
	}
	b.notifySubscribers(entries)
	if sync {
		return b.flush()
	}
	select {
	case b.flusher <- struct{}{}:
	default:
	}
	return nil
}

// fail records the first write error. A log that failed to take an entry
// has a hole followers cannot see past, so it takes nothing more; it is not
// marked clean at Close, and the next start begins a new log.
func (b *Binlog) fail(err error) error {
	if b.failed == nil {
		b.failed = err
	}
	return err
}

// Err is the write error that stopped the log, or nil.
func (b *Binlog) Err() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.failed
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

// OldestLSN returns the oldest LSN still in the binlog, 0 when it is empty.
func (b *Binlog) OldestLSN() uint64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.oldestLocked()
}

func (b *Binlog) oldestLocked() uint64 {
	if b.lsn.Load() == b.baseLSN {
		return 0
	}
	return b.baseLSN + 1
}

// Close flushes and closes the binlog. It is safe to call Close more than once.
//
// A log that closed with everything on disk is marked clean. An append that
// arrives afterwards — a writer still running at shutdown — takes the mark
// back, so the next start does not trust a log that is missing that write.
func (b *Binlog) Close() error {
	var closeErr error
	b.closeOnce.Do(func() {
		close(b.done)

		b.mu.Lock()
		defer b.mu.Unlock()
		b.closed = true

		b.subMu.Lock()
		for id, ch := range b.subscribers {
			close(ch)
			delete(b.subscribers, id)
		}
		b.subMu.Unlock()

		closeErr = b.flush()
		if closeErr == nil {
			closeErr = b.saveMeta(stateClean)
		}
		if err := b.file.Close(); closeErr == nil {
			closeErr = err
		}
	})
	return closeErr
}

// spoilCleanClose takes back the clean mark of a closed log.
func (b *Binlog) spoilCleanClose() {
	if b.spoiled {
		return
	}
	b.spoiled = true
	_ = b.saveMeta(stateOpen)
}

// flush flushes the write buffer and syncs to disk. An error stops the log.
func (b *Binlog) flush() error {
	if b.failed != nil {
		return b.failed
	}
	if err := b.writer.Flush(); err != nil {
		return b.fail(fmt.Errorf("failed to flush binlog: %w", err))
	}
	if err := syncFile(b.file); err != nil {
		return b.fail(fmt.Errorf("failed to sync binlog: %w", err))
	}
	b.synced = b.lsn.Load()
	return nil
}

// periodicFlusher flushes the binlog periodically
func (b *Binlog) periodicFlusher() {
	ticker := time.NewTicker(binlogFlushInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
		case <-b.flusher:
		case <-b.done:
			return
		}
		b.mu.Lock()
		if !b.closed {
			_ = b.flush()
		}
		b.mu.Unlock()
	}
}

// notifySubscribers sends entries to all active subscribers. It is called
// under b.mu, so every subscriber sees the entries in LSN order.
func (b *Binlog) notifySubscribers(entries []*BinlogEntry) {
	b.subMu.RLock()
	defer b.subMu.RUnlock()

	for _, ch := range b.subscribers {
		for _, entry := range entries {
			select {
			case ch <- entry:
			default:
				// Subscriber is too slow, drop entry.
				// The stream reads it back from the file.
			}
		}
	}
}
