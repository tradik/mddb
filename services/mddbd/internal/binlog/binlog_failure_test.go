package binlog

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// When the disk says no (GO-050): a log that failed to take an entry takes
// nothing more and is not trusted at the next start; a sidecar or a
// retention rewrite that fails leaves the log as it was.

var errInjected = errors.New("injected")

// inject changes the file operations for one test.
func inject(t *testing.T, change func(o *fileOps)) {
	t.Helper()
	orig := ops.Load()
	o := *orig
	change(&o)
	ops.Store(&o)
	t.Cleanup(func() { ops.Store(orig) })
}

func failingSync(*os.File) error { return errInjected }

// failAfter is an os.OpenFile that fails from its (n+1)th call on.
func failAfter(n int) func(string, int, os.FileMode) (*os.File, error) {
	return func(name string, flag int, perm os.FileMode) (*os.File, error) {
		if n == 0 {
			return nil, errInjected
		}
		n--
		return os.OpenFile(name, flag, perm) // #nosec G304 -- test paths
	}
}

func TestAnAppendAfterCloseTakesBackTheCleanMark(t *testing.T) {
	dir := t.TempDir()
	bl := openIn(t, dir, BinlogConfig{})
	appendN(t, bl, 2)
	if err := bl.Close(); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := bl.Append(&BinlogEntry{Type: BinlogPut}); !errors.Is(err, ErrBinlogClosed) {
			t.Fatalf("append after close: %v", err)
		}
	}
	if err := bl.AppendReplicated([]*BinlogEntry{{LSN: 3}}); !errors.Is(err, ErrBinlogClosed) {
		t.Errorf("replicated append after close: %v", err)
	}
	again := openIn(t, dir, BinlogConfig{})
	defer func() { _ = again.Close() }()
	if again.CurrentLSN() < resetJump {
		t.Error("a log that missed a write after Close was continued")
	}
}

func TestASyncFailureStopsTheLog(t *testing.T) {
	dir := t.TempDir()
	bl := openIn(t, dir, BinlogConfig{Sync: SyncCommit})
	inject(t, func(o *fileOps) { o.sync = failingSync })
	if err := bl.Append(&BinlogEntry{Type: BinlogPut}); !errors.Is(err, errInjected) {
		t.Fatalf("append with a failing sync: %v", err)
	}
	if err := bl.Append(&BinlogEntry{Type: BinlogPut}); !errors.Is(err, errInjected) || !errors.Is(bl.Err(), errInjected) {
		t.Fatalf("the log took an entry after it failed: %v / %v", err, bl.Err())
	}
	if err := bl.Close(); !errors.Is(err, errInjected) {
		t.Errorf("close of a failed log: %v", err)
	}
	ops.Store(&fileOps{rename: os.Rename, truncate: os.Truncate, openFile: os.OpenFile, sync: (*os.File).Sync})
	again := openIn(t, dir, BinlogConfig{})
	defer func() { _ = again.Close() }()
	if again.CurrentLSN() < resetJump || again.Err() != nil {
		t.Error("a log that failed was continued")
	}
}

func TestAWriteOrFlushFailureStopsTheLog(t *testing.T) {
	bl := openIn(t, t.TempDir(), BinlogConfig{})
	appendN(t, bl, 1) // buffered
	_ = bl.file.Close()
	bl.mu.Lock()
	err := bl.flush()
	bl.mu.Unlock()
	if err == nil || !strings.Contains(err.Error(), "flush") {
		t.Fatalf("flush to a closed file: %v", err)
	}

	big := openIn(t, t.TempDir(), BinlogConfig{})
	_ = big.file.Close()
	if err := big.Append(&BinlogEntry{Type: BinlogPut, Value: make([]byte, binlogBufferSize+1)}); err == nil {
		t.Fatal("an entry larger than the buffer was written to a closed file")
	}
}

func TestSidecarWriteFailures(t *testing.T) {
	cases := map[string]func(t *testing.T){
		"create": func(t *testing.T) { inject(t, func(o *fileOps) { o.openFile = failAfter(0) }) },
		"sync":   func(t *testing.T) { inject(t, func(o *fileOps) { o.sync = failingSync }) },
		"rename": func(t *testing.T) {
			inject(t, func(o *fileOps) { o.rename = func(string, string) error { return errInjected } })
		},
	}
	for name, set := range cases {
		t.Run(name, func(t *testing.T) {
			set(t)
			if _, err := NewBinlog(filepath.Join(t.TempDir(), "x.db"), BinlogConfig{}); err == nil || !strings.Contains(err.Error(), "sidecar") {
				t.Errorf("open with a failing sidecar %s: %v", name, err)
			}
		})
	}
}

func TestASidecarThatCannotBeReadIsDistrusted(t *testing.T) {
	dir := t.TempDir()
	bl := openIn(t, dir, BinlogConfig{})
	appendN(t, bl, 1)
	_ = bl.Close()
	_ = os.Remove(bl.metaPath())
	if err := os.Mkdir(bl.metaPath(), 0o700); err != nil { // reading it fails
		t.Fatal(err)
	}
	meta, status, err := loadMeta(bl.metaPath())
	if status != metaUnreadable || err != nil || meta.LogID != "" {
		t.Errorf("loadMeta of a directory: %v %v %v", meta, status, err)
	}
}

func TestTruncateFailures(t *testing.T) {
	failTruncate := func(o *fileOps) { o.truncate = func(string, int64) error { return errInjected } }
	inject(t, failTruncate)

	// A torn tail that cannot be cut off.
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "mddb.binlog"), "torn")
	if _, err := NewBinlog(filepath.Join(dir, "x.db"), BinlogConfig{}); err == nil {
		t.Error("opened a log whose torn tail could not be removed")
	}

	// A log not closed, which cannot be emptied for the new one.
	dir = t.TempDir()
	writeMeta(t, filepath.Join(dir, "mddb.binlog.meta"), logMeta{Version: 1, EntryFormat: 1, State: stateOpen})
	if _, err := NewBinlog(filepath.Join(dir, "x.db"), BinlogConfig{}); err == nil {
		t.Error("continued a log that could not be restarted")
	}

	inject(t, func(o *fileOps) { o.truncate = os.Truncate })
	bl := openIn(t, t.TempDir(), BinlogConfig{})
	defer func() { _ = bl.Close() }()
	inject(t, failTruncate)
	if err := bl.Reset(); err == nil {
		t.Error("Reset reported success")
	}
	if err := bl.Rebase(1, 1); err == nil {
		t.Error("Rebase reported success")
	}
}

func TestRetentionRewriteFailuresLeaveTheLogAsItWas(t *testing.T) {
	size := entrySize()
	cases := map[string]func(t *testing.T){
		"temp file": func(t *testing.T) { inject(t, func(o *fileOps) { o.openFile = failAfter(0) }) },
		"sync":      func(t *testing.T) { inject(t, func(o *fileOps) { o.sync = failSyncOf(".compact") }) },
		"rename":    func(t *testing.T) { inject(t, func(o *fileOps) { o.rename = failRenameOf(".compact") }) },
	}
	for name, set := range cases {
		t.Run(name, func(t *testing.T) {
			bl := openIn(t, t.TempDir(), BinlogConfig{MaxSize: 4 * size, RetentionInterval: -1})
			defer func() { _ = bl.Close() }()
			appendN(t, bl, 10)
			set(t)
			if _, err := bl.EnforceRetention(); err == nil {
				t.Fatal("a failed rewrite reported success")
			}
			if e, err := bl.ReadFrom(0); err != nil || len(e) != 10 {
				t.Errorf("after a failed rewrite: %d entries, %v", len(e), err)
			}
		})
	}
}

func TestARewriteThatCannotReopenTheLogStopsIt(t *testing.T) {
	bl := openIn(t, t.TempDir(), BinlogConfig{MaxSize: entrySize(), RetentionInterval: -1})
	defer func() { _ = bl.Close() }()
	appendN(t, bl, 4)
	inject(t, func(o *fileOps) { o.openFile = failAfter(1) }) // the temp file opens, the log does not
	if _, err := bl.EnforceRetention(); err == nil || bl.Err() == nil {
		t.Errorf("rewrite without a log to reopen: %v, log error %v", err, bl.Err())
	}
}

func TestARewriteOfAClosedLogIsRefused(t *testing.T) {
	bl := openIn(t, t.TempDir(), BinlogConfig{})
	appendN(t, bl, 3)
	_ = bl.Close()
	if err := bl.rewriteFrom(cutPoint{offset: 1}, 2); !errors.Is(err, ErrBinlogClosed) {
		t.Errorf("rewrite of a closed log: %v", err)
	}
	closeQuietly(nil)
}

func TestRotateFailures(t *testing.T) {
	bl := openIn(t, t.TempDir(), BinlogConfig{})
	appendN(t, bl, 3)
	_ = os.Remove(bl.path)
	if err := bl.Rotate(2); err == nil {
		t.Error("Rotate of a log whose file is gone reported success")
	}
	bl.mu.Lock()
	bl.failed = errInjected
	bl.mu.Unlock()
	if err := bl.Rotate(2); !errors.Is(err, errInjected) {
		t.Errorf("Rotate of a failed log: %v", err)
	}
	_ = bl.Close()
}

func TestOpeningAFileThatCannotBeStatted(t *testing.T) {
	f, err := os.Create(filepath.Join(t.TempDir(), "mddb.binlog"))
	if err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	b := &Binlog{file: f, path: f.Name()}
	if err := b.openLog(); err == nil {
		t.Error("opened a log through a closed file")
	}
}

func TestRetentionWithNothingItMayDrop(t *testing.T) {
	size := entrySize()
	bl := openIn(t, t.TempDir(), BinlogConfig{MaxSize: 2 * size, HardMaxSize: 100 * size, RetentionInterval: -1})
	defer func() { _ = bl.Close() }()
	bl.SetRetentionFloor(func() (uint64, bool) { return 0, true }) // a follower that has nothing
	appendN(t, bl, 5)
	if res, err := bl.EnforceRetention(); err != nil || res.DroppedThrough != 0 || bl.OldestLSN() != 1 {
		t.Errorf("dropped entries a follower needs: %+v, %v", res, err)
	}
}

func failSyncOf(suffix string) func(*os.File) error {
	return func(f *os.File) error {
		if strings.HasSuffix(f.Name(), suffix) {
			return errInjected
		}
		return f.Sync()
	}
}

func failRenameOf(suffix string) func(string, string) error {
	return func(from, to string) error {
		if strings.HasSuffix(from, suffix) {
			return errInjected
		}
		return os.Rename(from, to)
	}
}
