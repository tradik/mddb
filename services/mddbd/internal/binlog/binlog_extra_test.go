package binlog

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func appendN(t *testing.T, bl *Binlog, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		e := &BinlogEntry{Type: BinlogPut, BucketName: "b", Key: []byte{byte(i)}, Value: []byte("v")}
		if err := bl.Append(e); err != nil {
			t.Fatalf("Append #%d: %v", i, err)
		}
	}
}

func TestBinlogEntryTypeString(t *testing.T) {
	cases := map[BinlogEntryType]string{
		BinlogPut:           "Put",
		BinlogDelete:        "Delete",
		BinlogDeleteBucket:  "DeleteBucket",
		BinlogCheckpoint:    "Checkpoint",
		BinlogEntryType(99): "Unknown(99)",
	}
	for typ, want := range cases {
		if got := typ.String(); got != want {
			t.Errorf("BinlogEntryType(%d).String() = %q, want %q", typ, got, want)
		}
	}
}

func TestBinlogOpsLen(t *testing.T) {
	bo := &BinlogOps{}
	if bo.Len() != 0 {
		t.Fatalf("empty Len() = %d, want 0", bo.Len())
	}
	bo.Put("bucket", []byte("k1"), []byte("v1"))
	bo.Delete("bucket", []byte("k2"))
	if bo.Len() != 2 {
		t.Fatalf("Len() = %d, want 2", bo.Len())
	}
	// FlushTo a real binlog appends the batch.
	bl, cleanup := newTestBinlog(t)
	defer cleanup()
	bo.FlushTo(bl)
	if got := bl.CurrentLSN(); got != 2 {
		t.Fatalf("after FlushTo CurrentLSN = %d, want 2", got)
	}
}

func TestBinlogOldestLSN(t *testing.T) {
	bl, cleanup := newTestBinlog(t)
	defer cleanup()
	if bl.OldestLSN() != 0 {
		t.Fatalf("fresh OldestLSN = %d, want 0", bl.OldestLSN())
	}
	appendN(t, bl, 5)
	if bl.OldestLSN() != 1 {
		t.Fatalf("OldestLSN = %d, want 1", bl.OldestLSN())
	}
	if bl.CurrentLSN() != 5 {
		t.Fatalf("CurrentLSN = %d, want 5", bl.CurrentLSN())
	}
}

func TestBinlogRotatePartial(t *testing.T) {
	bl, cleanup := newTestBinlog(t)
	defer cleanup()
	appendN(t, bl, 5)
	if err := bl.Rotate(3); err != nil {
		t.Fatalf("Rotate(3): %v", err)
	}
	if got := bl.OldestLSN(); got != 3 {
		t.Fatalf("after Rotate OldestLSN = %d, want 3", got)
	}
	entries, err := bl.ReadFrom(0)
	if err != nil {
		t.Fatalf("ReadFrom(0): %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("kept %d entries, want 3 (LSN 3,4,5)", len(entries))
	}
	if entries[0].LSN != 3 {
		t.Fatalf("first kept LSN = %d, want 3", entries[0].LSN)
	}
	// Requesting an LSN now older than the retained window must error.
	if _, err := bl.ReadFrom(2); !errors.Is(err, ErrBinlogLSNTooOld) {
		t.Fatalf("ReadFrom(2) err = %v, want ErrBinlogLSNTooOld", err)
	}
}

func TestBinlogRotateFullTruncate(t *testing.T) {
	bl, cleanup := newTestBinlog(t)
	defer cleanup()
	appendN(t, bl, 4)
	if err := bl.Rotate(0); err != nil {
		t.Fatalf("Rotate(0): %v", err)
	}
	entries, err := bl.ReadFrom(0)
	if err != nil {
		t.Fatalf("ReadFrom(0): %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("after full truncate got %d entries, want 0", len(entries))
	}
}

func TestBinlogReopenRecoversLSN(t *testing.T) {
	path := filepath.Join(t.TempDir(), "reopen.binlog")
	bl, err := NewBinlog("", BinlogConfig{Path: path})
	if err != nil {
		t.Fatalf("NewBinlog: %v", err)
	}
	appendN(t, bl, 3)
	if err := bl.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	bl2, err := NewBinlog("", BinlogConfig{Path: path})
	if err != nil {
		t.Fatalf("reopen NewBinlog: %v", err)
	}
	defer func() { _ = bl2.Close() }()
	if got := bl2.CurrentLSN(); got != 3 {
		t.Fatalf("recovered CurrentLSN = %d, want 3", got)
	}
	if got := bl2.OldestLSN(); got != 1 {
		t.Fatalf("recovered OldestLSN = %d, want 1", got)
	}
}

func TestUnmarshalBinlogEntryErrors(t *testing.T) {
	t.Run("too short", func(t *testing.T) {
		if _, _, err := UnmarshalBinlogEntry([]byte{1, 2, 3}); err == nil {
			t.Fatal("expected error for short data")
		}
	})

	valid := MarshalBinlogEntry(&BinlogEntry{
		LSN: 1, Type: BinlogPut, BucketName: "b", Key: []byte("k"), Value: []byte("v"),
	})

	t.Run("truncated body", func(t *testing.T) {
		// Header is intact but the key-length field is cut off.
		if _, _, err := UnmarshalBinlogEntry(valid[:binlogEntryHeaderSize+1]); err == nil {
			t.Fatal("expected error for truncated body")
		}
	})

	t.Run("checksum mismatch", func(t *testing.T) {
		corrupt := make([]byte, len(valid))
		copy(corrupt, valid)
		corrupt[len(corrupt)-1] ^= 0xFF // flip a checksum byte
		if _, _, err := UnmarshalBinlogEntry(corrupt); err == nil {
			t.Fatal("expected checksum mismatch error")
		}
	})

	t.Run("round trip ok", func(t *testing.T) {
		entry, n, err := UnmarshalBinlogEntry(valid)
		if err != nil {
			t.Fatalf("UnmarshalBinlogEntry: %v", err)
		}
		if n != len(valid) {
			t.Fatalf("consumed %d bytes, want %d", n, len(valid))
		}
		if entry.BucketName != "b" || string(entry.Key) != "k" || string(entry.Value) != "v" {
			t.Fatalf("round-trip mismatch: %+v", entry)
		}
	})
}

func TestBinlogNewOpenError(t *testing.T) {
	// O_CREATE does not create parent directories, so a path under a missing
	// directory cannot be opened.
	bad := filepath.Join(t.TempDir(), "no-such-dir", "f.binlog")
	if _, err := NewBinlog("", BinlogConfig{Path: bad}); err == nil {
		t.Fatal("expected error opening binlog under a missing directory")
	}
}

func TestBinlogRecoverCorruptTail(t *testing.T) {
	path := filepath.Join(t.TempDir(), "corrupt.binlog")
	bl, err := NewBinlog("", BinlogConfig{Path: path})
	if err != nil {
		t.Fatalf("NewBinlog: %v", err)
	}
	appendN(t, bl, 2)
	if err := bl.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	// Append a partial entry (an LSN with no following header) to corrupt the tail.
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600) // #nosec G304 -- path is under t.TempDir()
	if err != nil {
		t.Fatalf("open for corruption: %v", err)
	}
	if _, err := f.Write([]byte{0, 0, 0, 0, 0, 0, 0, 9}); err != nil {
		t.Fatalf("write garbage: %v", err)
	}
	_ = f.Close()
	// A torn tail is a crash mid-append: dropped, not fatal.
	assertRecoveredTo(t, path, 2)
}

// assertRecoveredTo reopens a binlog with a torn tail and checks it kept the
// whole entries and nothing else.
func assertRecoveredTo(t *testing.T, path string, wantLSN uint64) {
	t.Helper()
	bl, err := NewBinlog("", BinlogConfig{Path: path})
	if err != nil {
		t.Fatalf("a torn tail stopped the binlog opening: %v", err)
	}
	defer func() { _ = bl.Close() }()
	if got := bl.CurrentLSN(); got != wantLSN {
		t.Errorf("CurrentLSN = %d, want %d", got, wantLSN)
	}
	entries, err := bl.ReadFrom(0)
	if err != nil || uint64(len(entries)) != wantLSN {
		t.Errorf("ReadFrom = %d entries, %v; want %d", len(entries), err, wantLSN)
	}
}

func TestBinlogSubscribeNotify(t *testing.T) {
	bl, cleanup := newTestBinlog(t)
	defer cleanup()
	ch := bl.Subscribe("sub1")
	appendN(t, bl, 1)
	select {
	case e := <-ch:
		if e.LSN != 1 {
			t.Fatalf("received LSN %d, want 1", e.LSN)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for subscriber notification")
	}
	bl.Unsubscribe("sub1")
}

func TestBinlogCloseIdempotent(t *testing.T) {
	bl, cleanup := newTestBinlog(t)
	defer cleanup() // cleanup also calls Close; closeOnce makes it a no-op
	if err := bl.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := bl.Close(); err != nil {
		t.Fatalf("second Close should be a no-op, got: %v", err)
	}
}

func TestBinlogCloseWithSubscribers(t *testing.T) {
	bl, cleanup := newTestBinlog(t)
	defer cleanup()
	ch := bl.Subscribe("s1")
	if err := bl.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	// Close must have closed the subscriber channel.
	if _, open := <-ch; open {
		t.Fatal("subscriber channel should be closed after Close")
	}
}

func TestBinlogAppendBatchEmpty(t *testing.T) {
	bl, cleanup := newTestBinlog(t)
	defer cleanup()
	if err := bl.AppendBatch(nil); err != nil {
		t.Fatalf("AppendBatch(nil): %v", err)
	}
	if got := bl.CurrentLSN(); got != 0 {
		t.Fatalf("CurrentLSN after empty batch = %d, want 0", got)
	}
}

func TestBinlogRotateKeepNothing(t *testing.T) {
	bl, cleanup := newTestBinlog(t)
	defer cleanup()
	appendN(t, bl, 3)
	// keepFromLSN beyond every stored LSN keeps no entries.
	if err := bl.Rotate(999); err != nil {
		t.Fatalf("Rotate(999): %v", err)
	}
	if got := bl.OldestLSN(); got != 0 {
		t.Fatalf("OldestLSN after keep-nothing rotate = %d, want 0", got)
	}
	entries, err := bl.ReadFrom(0)
	if err != nil {
		t.Fatalf("ReadFrom(0): %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("got %d entries, want 0", len(entries))
	}
}

func TestCopyBytesNil(t *testing.T) {
	if got := copyBytes(nil); got != nil {
		t.Fatalf("copyBytes(nil) = %v, want nil", got)
	}
	src := []byte("data")
	dst := copyBytes(src)
	if string(dst) != "data" {
		t.Fatalf("copyBytes copy = %q, want %q", dst, "data")
	}
	dst[0] = 'X' // mutating the copy must not affect the source
	if string(src) != "data" {
		t.Fatalf("source mutated: %q", src)
	}
}

func TestBinlogPeriodicFlush(t *testing.T) {
	bl, cleanup := newTestBinlog(t)
	defer cleanup()
	appendN(t, bl, 1)
	// Wait past one flush interval so the periodic ticker fires.
	time.Sleep(150 * time.Millisecond)
	if got := bl.CurrentLSN(); got != 1 {
		t.Fatalf("CurrentLSN = %d, want 1", got)
	}
}

func TestBinlogRecoverTruncatedEntry(t *testing.T) {
	path := filepath.Join(t.TempDir(), "trunc.binlog")
	bl, err := NewBinlog("", BinlogConfig{Path: path})
	if err != nil {
		t.Fatalf("NewBinlog: %v", err)
	}
	appendN(t, bl, 1)
	if err := bl.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	// Append a second entry header that claims a 255-byte bucket name but
	// supplies no body: a write cut short mid-entry.
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600) // #nosec G304 -- path is under t.TempDir()
	if err != nil {
		t.Fatalf("open for corruption: %v", err)
	}
	hdr := make([]byte, 8+11) // LSN(8) + type(1)+timestamp(8)+bucketNameLen(2)
	hdr[len(hdr)-1] = 0xFF    // bucketNameLen = 255
	if _, err := f.Write(hdr); err != nil {
		t.Fatalf("write header: %v", err)
	}
	_ = f.Close()
	assertRecoveredTo(t, path, 1)
}

func TestUnmarshalBinlogEntryTruncationOffsets(t *testing.T) {
	valid := MarshalBinlogEntry(&BinlogEntry{
		LSN: 1, Type: BinlogPut, BucketName: "b", Key: []byte("k"), Value: []byte("v"),
	})
	// Each length triggers a distinct truncation branch in UnmarshalBinlogEntry.
	for _, n := range []int{19, 24, 25, 29, 30} {
		if _, _, err := UnmarshalBinlogEntry(valid[:n]); err == nil {
			t.Errorf("UnmarshalBinlogEntry(valid[:%d]) = nil error, want truncation error", n)
		}
	}
}
