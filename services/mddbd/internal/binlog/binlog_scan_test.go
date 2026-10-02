package binlog

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func newScanLog(t *testing.T, n int) *Binlog {
	t.Helper()
	bl, err := NewBinlog(filepath.Join(t.TempDir(), "s.db"), BinlogConfig{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = bl.Close() })
	for i := 0; i < n; i++ {
		if err := bl.Append(&BinlogEntry{Type: BinlogPut, BucketName: "b", Key: []byte{byte(i)}, Value: []byte("v")}); err != nil {
			t.Fatal(err)
		}
	}
	return bl
}

// A follower catching up from LSN 0 must start receiving entries as the file
// is read, and the scan must stop the moment the follower is gone.
func TestScanFromStopsWhenTheReceiverDoes(t *testing.T) {
	bl := newScanLog(t, 10)
	stop := errors.New("follower gone")
	var seen []uint64
	err := bl.ScanFrom(0, func(e *BinlogEntry) error {
		seen = append(seen, e.LSN)
		if len(seen) == 3 {
			return stop
		}
		return nil
	})
	if !errors.Is(err, stop) || len(seen) != 3 {
		t.Errorf("err = %v after %v; want the receiver's error after 3 entries", err, seen)
	}
}

func TestScanFromSkipsWhatTheFollowerHas(t *testing.T) {
	bl := newScanLog(t, 10)
	var first uint64
	_ = bl.ScanFrom(7, func(e *BinlogEntry) error {
		if first == 0 {
			first = e.LSN
		}
		return nil
	})
	if first != 8 {
		t.Errorf("first entry after LSN 7 = %d, want 8", first)
	}
}

// Answered before reading anything: the old ReadFrom read the whole file and
// only then said the follower needed a snapshot.
func TestScanFromReportsATrimmedLogUpFront(t *testing.T) {
	bl := newScanLog(t, 10)
	bl.mu.Lock()
	bl.oldestLSN = 5
	bl.mu.Unlock()
	called := false
	err := bl.ScanFrom(2, func(*BinlogEntry) error { called = true; return nil })
	if !errors.Is(err, ErrBinlogLSNTooOld) || called {
		t.Errorf("err = %v, called = %v; want ErrBinlogLSNTooOld before any entry", err, called)
	}
	if _, err := bl.ReadFrom(2); !errors.Is(err, ErrBinlogLSNTooOld) {
		t.Errorf("ReadFrom: %v", err)
	}
}

func TestScanFromAMissingFileIsAnError(t *testing.T) {
	bl := newScanLog(t, 1)
	// Pointed elsewhere rather than removed: Windows does not let an open
	// file be deleted.
	bl.path = filepath.Join(t.TempDir(), "gone.binlog")
	if err := bl.ScanFrom(0, func(*BinlogEntry) error { return nil }); err == nil {
		t.Error("scanning a binlog whose file is gone succeeded")
	}
}

// The tail of a write that did not complete ends the scan; everything
// before it is delivered.
func TestScanFromStopsAtATornTail(t *testing.T) {
	bl := newScanLog(t, 3)
	bl.mu.Lock()
	_ = bl.flush()
	_, _ = bl.file.Write([]byte{0, 0, 0, 0, 0, 0, 0, 9, 1}) // half a header
	bl.mu.Unlock()
	n := 0
	if err := bl.ScanFrom(0, func(*BinlogEntry) error { n++; return nil }); err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Errorf("delivered %d entries before the torn tail, want 3", n)
	}
}

// Each section of an entry can be cut short, and a corrupt length must not
// be trusted with an allocation.
func TestReadEntryRejectsShortAndImpossibleEntries(t *testing.T) {
	full := MarshalBinlogEntry(&BinlogEntry{LSN: 1, Type: BinlogPut, BucketName: "bucket", Key: []byte("key"), Value: []byte("value")})
	for cut := 1; cut < len(full); cut++ {
		if _, _, err := readEntry(bufio.NewReader(bytes.NewReader(full[:cut]))); err == nil {
			t.Fatalf("an entry cut to %d of %d bytes was read", cut, len(full))
		}
	}
	if e, _, err := readEntry(bufio.NewReader(bytes.NewReader(full))); err != nil || string(e.Value) != "value" {
		t.Fatalf("a whole entry: %v, %v", e, err)
	}

	huge := append([]byte(nil), full...)
	keyLenAt := binlogEntryHeaderSize + len("bucket")
	binary.BigEndian.PutUint32(huge[keyLenAt:], 0xFFFFFFF0)
	if _, _, err := readEntry(bufio.NewReader(bytes.NewReader(huge))); err == nil {
		t.Error("an entry claiming a 4 GB key was read")
	}
}

// A crash in the middle of an append leaves part of an entry at the end of
// the file. The server must start, keep every whole entry, and append after
// them — not after bytes no reader can get past.
func TestReopeningAfterATornAppendKeepsTheLogUsable(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "t.db")
	for _, cut := range []int{3, 12, 25} { // inside the LSN, the header, the body
		t.Run("", func(t *testing.T) {
			_ = os.Remove(filepath.Join(dir, "mddb.binlog"))
			bl, err := NewBinlog(db, BinlogConfig{})
			if err != nil {
				t.Fatal(err)
			}
			for i := 0; i < 3; i++ {
				_ = bl.Append(&BinlogEntry{Type: BinlogPut, BucketName: "b", Key: []byte{byte(i)}, Value: []byte("value")})
			}
			_ = bl.Close()

			torn := MarshalBinlogEntry(&BinlogEntry{LSN: 4, Type: BinlogPut, BucketName: "b", Key: []byte("k"), Value: []byte("v")})
			f, _ := os.OpenFile(filepath.Join(dir, "mddb.binlog"), os.O_APPEND|os.O_WRONLY, 0o600) //nolint:gosec // G304: under t.TempDir()
			_, _ = f.Write(torn[:cut])
			_ = f.Close()

			bl, err = NewBinlog(db, BinlogConfig{})
			if err != nil {
				t.Fatalf("a binlog with a torn tail would not open: %v", err)
			}
			defer func() { _ = bl.Close() }()
			if got := bl.CurrentLSN(); got != 3 {
				t.Errorf("CurrentLSN = %d, want 3", got)
			}
			_ = bl.Append(&BinlogEntry{Type: BinlogPut, BucketName: "b", Key: []byte("after"), Value: []byte("v")})
			entries, err := bl.ReadFrom(0)
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != 4 || string(entries[3].Key) != "after" {
				t.Errorf("read back %d entries; the one written after reopening must be readable", len(entries))
			}
		})
	}
}
