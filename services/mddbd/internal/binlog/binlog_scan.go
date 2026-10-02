package binlog

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
)

// Reading the log back without holding it in memory.
//
// ReadFrom used to read the whole file into memory and decode every entry
// before returning any of them. A follower starting from LSN 0 against a 20 GB
// binlog therefore received nothing for hours while the leader built a 20 GB
// slice — reported as a stream that "sends ~1KB and then nothing" (#267). The
// log is now decoded one entry at a time and handed to the caller as it is
// read.

// ScanFrom calls fn for every entry with LSN > fromLSN, in file order, and
// stops at the first error fn returns. Returns ErrBinlogLSNTooOld when the
// entries after fromLSN are no longer in the file — checked before reading,
// not after.
//
// A truncated or corrupt entry ends the scan without an error: it is the tail
// of a write that did not complete, and nothing after it can be trusted.
func (b *Binlog) ScanFrom(fromLSN uint64, fn func(*BinlogEntry) error) error {
	b.mu.Lock()
	_ = b.flush() // pending writes must be readable
	oldest := b.oldestLSN
	b.mu.Unlock()

	if fromLSN > 0 && oldest > 0 && fromLSN < oldest {
		return ErrBinlogLSNTooOld
	}

	f, err := os.Open(b.path)
	if err != nil {
		return fmt.Errorf("failed to open binlog for reading: %w", err)
	}
	defer func() { _ = f.Close() }()

	r := bufio.NewReaderSize(f, binlogBufferSize)
	for {
		entry, _, err := readEntry(r)
		if err != nil {
			return nil // end of file, or a torn tail
		}
		if entry.LSN <= fromLSN {
			continue
		}
		if err := fn(entry); err != nil {
			return err
		}
	}
}

// readEntry reads one encoded entry from r and reports how many bytes it
// took. See MarshalBinlogEntry for the layout; the bytes are reassembled and
// decoded by UnmarshalBinlogEntry, so the checksum is verified as before.
func readEntry(r *bufio.Reader) (*BinlogEntry, int, error) {
	buf := make([]byte, binlogEntryHeaderSize)
	if _, err := io.ReadFull(r, buf); err != nil {
		return nil, 0, err
	}
	bucketLen := int(binary.BigEndian.Uint16(buf[17:19]))
	buf, err := readMore(r, buf, bucketLen+4)
	if err != nil {
		return nil, 0, err
	}
	keyLen := int(binary.BigEndian.Uint32(buf[len(buf)-4:]))
	if buf, err = readMore(r, buf, keyLen+4); err != nil {
		return nil, 0, err
	}
	valueLen := int(binary.BigEndian.Uint32(buf[len(buf)-4:]))
	if buf, err = readMore(r, buf, valueLen+4); err != nil {
		return nil, 0, err
	}
	entry, n, err := UnmarshalBinlogEntry(buf)
	return entry, n, err
}

// maxEntryPart bounds a single length field, so a corrupt length cannot make
// the reader allocate gigabytes before the checksum gets a chance to fail.
const maxEntryPart = 1 << 30

func readMore(r *bufio.Reader, buf []byte, n int) ([]byte, error) {
	if n < 0 || n > maxEntryPart {
		return nil, errors.New("binlog entry length out of range")
	}
	start := len(buf)
	buf = append(buf, make([]byte, n)...)
	_, err := io.ReadFull(r, buf[start:])
	return buf, err
}
