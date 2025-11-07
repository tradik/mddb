package main

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// WAL implements Write-Ahead Logging for durability and performance
type WAL struct {
	file       *os.File
	writer     *bufio.Writer
	mu         sync.Mutex
	path       string
	syncPolicy SyncPolicy
	entries    uint64
	size       int64
	flusher    chan struct{}
	done       chan struct{}
}

// SyncPolicy determines when to fsync
type SyncPolicy int

const (
	SyncAlways    SyncPolicy = iota // Fsync after every write (safest, slowest)
	SyncPeriodic                    // Fsync every N ms (balanced)
	SyncBatch                       // Fsync after N entries (fastest, less safe)
	SyncNever                       // Never fsync (testing only)
)

// WALEntry represents a single log entry
type WALEntry struct {
	Type      EntryType
	Timestamp int64
	Data      []byte
	Checksum  uint32
}

// EntryType defines the type of WAL entry
type EntryType byte

const (
	EntryTypeAdd    EntryType = 1
	EntryTypeUpdate EntryType = 2
	EntryTypeDelete EntryType = 3
	EntryTypeCommit EntryType = 4
)

// NewWAL creates a new Write-Ahead Log
func NewWAL(dbPath string, policy SyncPolicy) (*WAL, error) {
	walPath := filepath.Join(filepath.Dir(dbPath), "mddb.wal")
	
	file, err := os.OpenFile(walPath, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0644)
	if err != nil {
		return nil, fmt.Errorf("failed to open WAL: %w", err)
	}
	
	stat, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, fmt.Errorf("failed to stat WAL: %w", err)
	}
	
	wal := &WAL{
		file:       file,
		writer:     bufio.NewWriterSize(file, 256*1024), // 256KB buffer
		path:       walPath,
		syncPolicy: policy,
		size:       stat.Size(),
		flusher:    make(chan struct{}, 1),
		done:       make(chan struct{}),
	}
	
	// Start background flusher for periodic sync
	if policy == SyncPeriodic {
		go wal.periodicFlusher()
	}
	
	return wal, nil
}

// Write appends an entry to the WAL
func (w *WAL) Write(entry *WALEntry) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	
	// Calculate checksum
	entry.Checksum = crc32.ChecksumIEEE(entry.Data)
	
	// Serialize entry
	buf := make([]byte, 0, len(entry.Data)+32)
	
	// Header: [type:1][timestamp:8][dataLen:4][checksum:4]
	buf = append(buf, byte(entry.Type))
	buf = binary.BigEndian.AppendUint64(buf, uint64(entry.Timestamp))
	buf = binary.BigEndian.AppendUint32(buf, uint32(len(entry.Data)))
	buf = binary.BigEndian.AppendUint32(buf, entry.Checksum)
	
	// Data
	buf = append(buf, entry.Data...)
	
	// Write to buffer
	n, err := w.writer.Write(buf)
	if err != nil {
		return fmt.Errorf("failed to write WAL entry: %w", err)
	}
	
	w.size += int64(n)
	w.entries++
	
	// Sync based on policy
	switch w.syncPolicy {
	case SyncAlways:
		if err := w.flush(); err != nil {
			return err
		}
	case SyncBatch:
		if w.entries%100 == 0 { // Sync every 100 entries
			if err := w.flush(); err != nil {
				return err
			}
		}
	case SyncPeriodic:
		// Trigger async flush
		select {
		case w.flusher <- struct{}{}:
		default:
		}
	}
	
	return nil
}

// flush flushes buffer and syncs to disk
func (w *WAL) flush() error {
	if err := w.writer.Flush(); err != nil {
		return fmt.Errorf("failed to flush WAL buffer: %w", err)
	}
	if err := w.file.Sync(); err != nil {
		return fmt.Errorf("failed to sync WAL: %w", err)
	}
	return nil
}

// periodicFlusher flushes WAL periodically
func (w *WAL) periodicFlusher() {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	
	for {
		select {
		case <-ticker.C:
			w.mu.Lock()
			_ = w.flush()
			w.mu.Unlock()
		case <-w.flusher:
			w.mu.Lock()
			_ = w.flush()
			w.mu.Unlock()
		case <-w.done:
			return
		}
	}
}

// Read reads all entries from WAL
func (w *WAL) Read() ([]*WALEntry, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	
	// Seek to beginning
	if _, err := w.file.Seek(0, 0); err != nil {
		return nil, fmt.Errorf("failed to seek WAL: %w", err)
	}
	
	reader := bufio.NewReader(w.file)
	var entries []*WALEntry
	
	for {
		entry, err := w.readEntry(reader)
		if err == io.EOF {
			break
		}
		if err != nil {
			return entries, fmt.Errorf("failed to read WAL entry: %w", err)
		}
		entries = append(entries, entry)
	}
	
	return entries, nil
}

// readEntry reads a single entry from reader
func (w *WAL) readEntry(reader *bufio.Reader) (*WALEntry, error) {
	// Read header
	header := make([]byte, 17) // 1+8+4+4
	if _, err := io.ReadFull(reader, header); err != nil {
		return nil, err
	}
	
	entryType := EntryType(header[0])
	timestamp := int64(binary.BigEndian.Uint64(header[1:9]))
	dataLen := binary.BigEndian.Uint32(header[9:13])
	checksum := binary.BigEndian.Uint32(header[13:17])
	
	// Read data
	data := make([]byte, dataLen)
	if _, err := io.ReadFull(reader, data); err != nil {
		return nil, err
	}
	
	// Verify checksum
	if crc32.ChecksumIEEE(data) != checksum {
		return nil, fmt.Errorf("WAL entry checksum mismatch")
	}
	
	return &WALEntry{
		Type:      entryType,
		Timestamp: timestamp,
		Data:      data,
		Checksum:  checksum,
	}, nil
}

// Truncate truncates the WAL (after successful checkpoint)
func (w *WAL) Truncate() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	
	// Close current file
	if err := w.file.Close(); err != nil {
		return err
	}
	
	// Recreate empty file
	file, err := os.OpenFile(w.path, os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0644)
	if err != nil {
		return fmt.Errorf("failed to truncate WAL: %w", err)
	}
	
	w.file = file
	w.writer = bufio.NewWriterSize(file, 256*1024)
	w.size = 0
	w.entries = 0
	
	return nil
}

// Close closes the WAL
func (w *WAL) Close() error {
	close(w.done)
	
	w.mu.Lock()
	defer w.mu.Unlock()
	
	if err := w.flush(); err != nil {
		return err
	}
	
	return w.file.Close()
}

// Stats returns WAL statistics
func (w *WAL) Stats() (entries uint64, size int64) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.entries, w.size
}
