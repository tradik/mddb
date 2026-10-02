package main

import (
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	bolt "go.etcd.io/bbolt"
)

// #266 and #270: databases bbolt cannot open, and what mddb does with them.

// writeTestDB creates a database with one bucket large enough that its tree
// has branch pages, written in one transaction so every branch page is live.
func writeTestDB(t *testing.T, path string, keys int) {
	t.Helper()
	db, err := bolt.Open(path, 0o600, &bolt.Options{NoFreelistSync: true})
	if err != nil {
		t.Fatal(err)
	}
	err = db.Update(func(tx *bolt.Tx) error {
		b, err := tx.CreateBucketIfNotExists([]byte("docs"))
		if err != nil {
			return err
		}
		nested, err := b.CreateBucketIfNotExists([]byte("nested"))
		if err != nil {
			return err
		}
		if err := nested.Put([]byte("inner"), []byte("value")); err != nil {
			return err
		}
		if err := b.SetSequence(42); err != nil {
			return err
		}
		for i := 0; i < keys; i++ {
			if err := b.Put([]byte(fmt.Sprintf("key-%06d", i)), []byte(strings.Repeat("v", 100))); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
}

// corruptBranchPage points the second child of a branch page at the first
// child's page: the page tree then references one page twice, which is the
// damage reported in #270 ("page 119988: multiple references").
func corruptBranchPage(t *testing.T, path string) {
	t.Helper()
	data, err := os.ReadFile(path) //nolint:gosec // G304: a file this test created
	if err != nil {
		t.Fatal(err)
	}
	// meta page 0: page header (16 bytes), then magic, version, pageSize.
	pageSize := int(binary.LittleEndian.Uint32(data[16+8:]))
	const branchFlag = 0x01
	for off := 2 * pageSize; off+pageSize <= len(data); off += pageSize {
		flags := binary.LittleEndian.Uint16(data[off+8:])
		count := binary.LittleEndian.Uint16(data[off+10:])
		if flags != branchFlag || count < 2 {
			continue
		}
		// branch elements follow the header: pos(4) ksize(4) pgid(8).
		first := binary.LittleEndian.Uint64(data[off+16+8:])
		binary.LittleEndian.PutUint64(data[off+16+16+8:], first)
		if err := os.WriteFile(path, data, 0o600); err != nil { //nolint:gosec // G703: a file this test created
			t.Fatal(err)
		}
		return
	}
	t.Fatal("no branch page to corrupt; write more keys")
}

func TestAHealthyDatabasePassesTheCheck(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ok.db")
	writeTestDB(t, path, 5000)
	if err := verifyDatabase(path); err != nil {
		t.Fatalf("a healthy database failed: %v", err)
	}
}

// The check runs in a child process because bbolt's walk of a damaged file
// cannot be contained in the process that runs it. Here the child fails and
// the test process — the server, in production — carries on.
func TestADamagedDatabaseFailsTheCheckWithBboltsReason(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.db")
	writeTestDB(t, path, 5000)
	corruptBranchPage(t, path)

	err := verifyDatabase(path)
	if err == nil {
		t.Fatal("a database whose tree references one page twice passed the check")
	}
	// Which of bbolt's two failures wins is a race — its walker goroutine
	// can fault on the rolled-back transaction before the reported panic
	// (seen on Windows CI) — and the reason the check is a child process.
	// Either is reported, not a stack trace.
	if msg := err.Error(); !strings.Contains(msg, "multiple references") && !strings.Contains(msg, "nil pointer") {
		t.Errorf("the error should carry bbolt's description, got: %v", err)
	}
}

func TestOpenBoltTurnsAPanicIntoAnError(t *testing.T) {
	// A directory is not a database; bbolt errors rather than panics, which
	// is the ordinary path through the wrapper.
	if _, err := openBolt(t.TempDir(), &bolt.Options{ReadOnly: true}); err == nil {
		t.Error("opening a directory succeeded")
	}
}

// #266: restoring a backup bbolt cannot open used to close the live
// database, move it aside, and then panic — leaving the server with nothing.
// It must now be refused before anything is touched.
func TestRestoringADamagedBackupLeavesTheServerServing(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	addTestDoc(t, s, "blog", "kept", "en", "# still here", nil)

	backup := filepath.Join(t.TempDir(), "bad.db")
	writeTestDB(t, backup, 5000)
	corruptBranchPage(t, backup)

	err := s.withRestoreLock(func() error {
		return s.swapDatabase(backup, func(dst string) error { return copyFile(backup, dst) })
	})
	if err == nil {
		t.Fatal("a damaged backup was restored")
	}
	if !strings.Contains(err.Error(), "not a usable database") {
		t.Errorf("unexpected error: %v", err)
	}
	if _, statErr := os.Stat(s.Path + ".pre-restore"); statErr == nil {
		t.Error("the live database was moved aside for a backup that was never going to open")
	}
	if got := countDocs(t, s, "blog"); got != 1 {
		t.Errorf("the server no longer serves its documents: %d", got)
	}
}

// #266: a backup taken while writes are landing must be one bbolt accepts.
// The old HTTP and MCP backups copied the file, and a copy taken mid-write
// failed on restore.
func TestABackupTakenDuringWritesRestores(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	for i := 0; i < 200; i++ {
		addTestDoc(t, s, "blog", fmt.Sprintf("seed-%d", i), "en", "# seed", nil)
	}

	var stop atomic.Bool
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; !stop.Load(); i++ {
			// not addTestDoc: t.Fatal must not be called off the test goroutine
			_, _, _ = s.addDocument("blog", fmt.Sprintf("during-%d", i), "en", nil, "# during", 0, true)
		}
	}()

	dst := filepath.Join(t.TempDir(), "during.db")
	err := s.backupTo(dst)
	stop.Store(true)
	wg.Wait()
	if err != nil {
		t.Fatalf("backup: %v", err)
	}
	if err := verifyDatabase(dst); err != nil {
		t.Fatalf("the backup does not open: %v", err)
	}
	if err := s.restoreFromBackup(dst); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if got := countDocs(t, s, "blog"); got < 200 {
		t.Errorf("restored %d documents, want at least the 200 written before the backup", got)
	}
}

// #270: what can be read of a damaged database is rebuilt into a new file
// that passes the check.
func TestRepairRebuildsADamagedDatabase(t *testing.T) {
	dir := t.TempDir()
	src, dst := filepath.Join(dir, "bad.db"), filepath.Join(dir, "repaired.db")
	writeTestDB(t, src, 5000)
	corruptBranchPage(t, src)

	if code := runRepair(src, dst); code != 0 && code != 3 {
		t.Fatalf("repair exited %d", code)
	}
	if err := verifyDatabase(dst); err != nil {
		t.Fatalf("the repaired database does not pass: %v", err)
	}
	db, err := bolt.Open(dst, 0o600, &bolt.Options{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	_ = db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte("docs"))
		if b == nil {
			t.Fatal("the docs bucket is gone")
		}
		if b.Sequence() != 42 {
			t.Errorf("bucket sequence = %d, want 42", b.Sequence())
		}
		if v := b.Bucket([]byte("nested")).Get([]byte("inner")); string(v) != "value" {
			t.Errorf("nested bucket value = %q", v)
		}
		if n := b.Stats().KeyN; n < 4000 {
			t.Errorf("only %d keys recovered from a database with one damaged branch", n)
		}
		return nil
	})
}

func TestRepairRefusesToOverwriteAndNeedsADestination(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "a.db")
	writeTestDB(t, src, 10)
	if code := runRepair(src, ""); code != 2 {
		t.Errorf("no destination: exit %d, want 2", code)
	}
	if _, err := repairDatabase(src, src); err == nil {
		t.Error("repair overwrote an existing file")
	}
}

// countDocs counts a collection's documents through the by-key index.
func countDocs(t *testing.T, s *Server, collection string) int {
	t.Helper()
	n := 0
	prefix := []byte("bykey|" + collection + "|")
	_ = s.DBView(func(tx *bolt.Tx) error {
		c := tx.Bucket(s.BucketNames.ByKey).Cursor()
		for k, _ := c.Seek(prefix); k != nil && strings.HasPrefix(string(k), string(prefix)); k, _ = c.Next() {
			n++
		}
		return nil
	})
	return n
}

func TestVerifyTimeoutIsConfigurable(t *testing.T) {
	t.Setenv("MDDB_VERIFY_TIMEOUT", "")
	if got := verifyTimeout(); got.Hours() != 1 {
		t.Errorf("default = %v, want 1h", got)
	}
	t.Setenv("MDDB_VERIFY_TIMEOUT", "90m")
	if got := verifyTimeout(); got.Minutes() != 90 {
		t.Errorf("MDDB_VERIFY_TIMEOUT=90m gave %v", got)
	}
	t.Setenv("MDDB_VERIFY_TIMEOUT", "nonsense")
	if got := verifyTimeout(); got.Hours() != 1 {
		t.Errorf("an unparseable value should fall back to 1h, got %v", got)
	}
}

func TestAFileThatIsNotADatabaseFailsTheCheck(t *testing.T) {
	path := filepath.Join(t.TempDir(), "text.db")
	if err := os.WriteFile(path, []byte(strings.Repeat("not a database ", 1000)), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := verifyDatabase(path); err == nil {
		t.Error("a text file passed as a database")
	}
	if code := runVerifyChild(filepath.Join(t.TempDir(), "missing.db")); code != 1 {
		t.Errorf("checking a missing file exited %d, want 1", code)
	}
}

func TestFailureReasonPrefersBboltsWords(t *testing.T) {
	if got := failureReason("goroutine 1\npanic: freepages: page 4: multiple references\nstack"); got != "freepages: page 4: multiple references" {
		t.Errorf("panic output: %q", got)
	}
	if got := failureReason("a\nb\nc\nd\ne\nf\ng\nh"); got != "c | d | e | f | g | h" {
		t.Errorf("plain output: %q", got)
	}
}

func TestRepairingAHealthyDatabaseCopiesEverything(t *testing.T) {
	dir := t.TempDir()
	src, dst := filepath.Join(dir, "ok.db"), filepath.Join(dir, "copy.db")
	writeTestDB(t, src, 25000) // more than one write batch
	if code := runRepair(src, dst); code != 0 {
		t.Fatalf("repair of a healthy database exited %d", code)
	}
	report, err := repairDatabase(src, filepath.Join(dir, "again.db"))
	if err != nil || len(report.Damaged) != 0 || report.Keys != 25001 {
		t.Errorf("report = %+v, %v; want 25001 keys and nothing damaged", report, err)
	}
	if code := runRepair(filepath.Join(dir, "missing.db"), filepath.Join(dir, "x.db")); code != 1 {
		t.Errorf("repairing a missing file exited %d, want 1", code)
	}
}

func TestBackupCanSkipTheCheck(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	t.Setenv("MDDB_BACKUP_VERIFY", "false")
	dst := filepath.Join(t.TempDir(), "fast.db")
	if err := s.backupTo(dst); err != nil {
		t.Fatal(err)
	}
	if err := s.backupTo(filepath.Join(t.TempDir(), "no-such-dir", "x.db")); err == nil {
		t.Error("a backup into a missing directory succeeded")
	}
}
