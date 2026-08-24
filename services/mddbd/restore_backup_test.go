package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"mddb/internal/binlog"
	proto "mddb/proto"

	bolt "go.etcd.io/bbolt"
)

// backupNow snapshots the live database with a consistent bolt transaction.
func backupNow(t *testing.T, s *Server) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "backup.db")
	if err := s.DBView(func(tx *bolt.Tx) error {
		return tx.CopyFile(path, 0600)
	}); err != nil {
		t.Fatal(err)
	}
	return path
}

// The three entry points into swapDatabase.
//
// Table-driven on purpose: the contract is the same for all three, and until
// SEC-017/SEC-018 it was not. Each had grown its own close-copy-reopen and each
// was broken differently — the MCP tool with no validation or rollback at all,
// the replication install with no validation and hand-written bolt options.
// Writing these as three near-identical tests would say the opposite of what
// the fix is.
var swapCallers = []struct {
	name string
	// swap installs the database at source (an absolute path) through whatever
	// route this caller takes to swapDatabase.
	swap func(t *testing.T, s *Server, source string) error
}{
	{
		name: "restore",
		swap: func(_ *testing.T, s *Server, source string) error {
			return s.restoreFromBackup(source)
		},
	},
	{
		name: "mcp tool",
		swap: func(t *testing.T, s *Server, source string) error {
			t.Setenv("MDDB_BACKUP_DIR", filepath.Dir(source))
			_, err := NewDirectClient(s).Restore(context.Background(),
				&MCPRestoreRequest{From: filepath.Base(source)})
			return err
		},
	},
	{
		name: "replication snapshot",
		swap: func(_ *testing.T, s *Server, source string) error {
			rc := &ReplicationClient{server: s}
			return s.withRestoreLock(func() error { return rc.replaceDatabase(source) })
		},
	},
}

// SEC-015/016/017/018: a source that is not a usable database must be refused
// before the live file is touched, whichever entry point asks.
//
// The failure this guards is not theoretical for any of them. The MCP tool
// accepted any readable file in the backup directory and overwrote the live
// database with it; the replication install accepted a half-received stream.
// In both cases the copy or rename succeeds — bytes are bytes — and only the
// reopen discovers the problem, by which point the original is gone.
func TestSwapContract_RejectsAnUnusableSourceWithoutLosingData(t *testing.T) {
	sources := map[string]func(t *testing.T) string{
		"not a database": func(t *testing.T) string {
			path := filepath.Join(t.TempDir(), "garbage.db")
			if err := os.WriteFile(path, []byte("this is not a bolt database"), 0600); err != nil {
				t.Fatal(err)
			}
			return path
		},
		"missing file": func(t *testing.T) string {
			return filepath.Join(t.TempDir(), "missing.db")
		},
	}

	for _, caller := range swapCallers {
		for sourceName, makeSource := range sources {
			t.Run(caller.name+"/"+sourceName, func(t *testing.T) {
				s, cleanup := newTestServer(t)
				defer cleanup()
				seedKeptDocument(t, s, "still here")
				before := s.DB

				if err := caller.swap(t, s, makeSource(t)); err == nil {
					t.Fatal("swap succeeded on a source that is not a usable database")
				}

				// The live handle must be the same object, not a replacement
				// installed by the rollback. This is what separates validating
				// the source from relying on the rollback to undo the damage:
				// both leave a working database, but only one never put it at
				// risk. Without it, removing the validation passes this test.
				if s.DB != before {
					t.Fatal("the live database handle was replaced — the source was " +
						"not rejected before the live file was touched")
				}

				assertDatabaseStillServing(t, s, "still here")
				if _, err := os.Stat(s.Path + ".pre-restore"); !os.IsNotExist(err) {
					t.Fatalf("rollback snapshot left behind at %s.pre-restore (err=%v)", s.Path, err)
				}
			})
		}
	}
}

// SEC-015/016/017/018: a successful swap must serve the new database on the
// live handle immediately. The gRPC path used to copy underneath the open file
// and keep serving the old data; the MCP path left the caches holding documents
// the new database does not have.
func TestSwapContract_ServesTheNewDataImmediately(t *testing.T) {
	for _, caller := range swapCallers {
		t.Run(caller.name, func(t *testing.T) {
			s, cleanup := newTestServer(t)
			defer cleanup()
			seedKeptDocument(t, s, "survives")

			source := backupNow(t, s)

			if _, _, err := s.addDocument("posts", "later", "en", nil, "added after the snapshot", 0, true); err != nil {
				t.Fatal(err)
			}
			// Prime the read cache with the newer document, so a stale cache is
			// distinguishable from a stale file handle.
			g := &GRPCServer{server: s}
			ctx := context.Background()
			if _, err := g.Get(ctx, &proto.GetRequest{Collection: "posts", Key: "later", Lang: "en"}); err != nil {
				t.Fatal(err)
			}

			if err := caller.swap(t, s, source); err != nil {
				t.Fatalf("swap: %v", err)
			}

			assertDatabaseStillServing(t, s, "survives")
			if _, err := g.Get(ctx, &proto.GetRequest{Collection: "posts", Key: "later", Lang: "en"}); err == nil {
				t.Fatal("document from after the snapshot still served — stale handle or stale cache")
			}
		})
	}
}

// A replication snapshot is moved, not copied: it is a temp file the follower
// created and is about to delete, and copying a multi-gigabyte database in
// order to throw the original away is waste. Asserted because "copies" and
// "moves" are indistinguishable from outside, right up until a disk fills.
func TestReplicaSnapshotInstall_ConsumesTheSnapshotFile(t *testing.T) {
	s, cleanup := newTestServer(t)
	defer cleanup()
	seedKeptDocument(t, s, "survives")

	snapshot := backupNow(t, s)

	rc := &ReplicationClient{server: s}
	if err := s.withRestoreLock(func() error { return rc.replaceDatabase(snapshot) }); err != nil {
		t.Fatalf("snapshot install: %v", err)
	}

	if _, err := os.Stat(snapshot); !os.IsNotExist(err) {
		t.Fatalf("snapshot still at %s after install (err=%v) — it was copied, not moved", snapshot, err)
	}
}

// The rollback is the reason this contract exists, and until these two tests it
// had never run: every failure test above supplies a source that validation
// rejects, so the swap returns before touching the live file. Coverage put the
// whole rollback closure and both of its call sites at zero executions.
//
// Reaching it needs a source that validates and an install that then fails,
// which is what makes install a parameter rather than a hardcoded copy.

func TestSwapDatabase_RollsBackWhenTheInstallFails(t *testing.T) {
	s, cleanup := newTestServer(t)
	defer cleanup()
	seedKeptDocument(t, s, "still here")

	// The backup is taken before the lock, not inside it: backupNow reads
	// through DBView, and withRestoreLock exists precisely to drain and block
	// those. Calling it inside deadlocks the test against itself.
	source := backupNow(t, s)

	wantErr := errors.New("no space left on device")
	err := s.withRestoreLock(func() error {
		return s.swapDatabase(source, func(string) error { return wantErr })
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("swapDatabase error = %v, want it to wrap %v", err, wantErr)
	}

	assertDatabaseStillServing(t, s, "still here")
}

func TestSwapDatabase_RollsBackWhenTheInstalledFileWillNotOpen(t *testing.T) {
	s, cleanup := newTestServer(t)
	defer cleanup()
	seedKeptDocument(t, s, "still here")

	source := backupNow(t, s) // before the lock — see the note above

	// install succeeds and leaves something unopenable at the live path — the
	// case no checksum catches and only the reopen discovers.
	err := s.withRestoreLock(func() error {
		return s.swapDatabase(source, func(dst string) error {
			return os.WriteFile(dst, []byte("installed, but not a database"), 0600)
		})
	})
	if err == nil {
		t.Fatal("swapDatabase succeeded after installing a file bolt cannot open")
	}

	assertDatabaseStillServing(t, s, "still here")
	if _, statErr := os.Stat(s.Path + ".pre-restore"); !os.IsNotExist(statErr) {
		t.Fatalf("rollback snapshot still at %s.pre-restore (err=%v)", s.Path, statErr)
	}
}

func seedKeptDocument(t *testing.T, s *Server, content string) {
	t.Helper()
	if _, _, err := s.addDocument("posts", "kept", "en", nil, content, 0, true); err != nil {
		t.Fatal(err)
	}
}

// assertDatabaseStillServing checks the live handle both reads and writes.
// A read alone can be answered from cache while the database underneath is
// closed, which is exactly how a dead server looked healthy in SEC-017.
func assertDatabaseStillServing(t *testing.T, s *Server, wantContent string) {
	t.Helper()

	g := &GRPCServer{server: s}
	doc, err := g.Get(context.Background(), &proto.GetRequest{Collection: "posts", Key: "kept", Lang: "en"})
	if err != nil {
		t.Fatalf("database not readable: %v", err)
	}
	if doc.ContentMd != wantContent {
		t.Fatalf("ContentMd = %q, want %q", doc.ContentMd, wantContent)
	}
	if _, _, err := s.addDocument("posts", "written-after", "en", nil, "write works", 0, true); err != nil {
		t.Fatalf("database not writable: %v", err)
	}
}

// A restore must reset the binlog, or followers apply their old LSN stream on
// top of a database that no longer has the rows those entries assume. No test
// covered this: every server in the restore suite runs with s.Binlog nil, so
// the branch that does it had never executed.
func TestRestore_ResetsTheBinlogSoFollowersReSnapshot(t *testing.T) {
	s, bl, cleanup := replTestServer(t)
	defer cleanup()

	seedKeptDocument(t, s, "survives")
	source := backupNow(t, s)

	// Put entries in the binlog, so a reset is distinguishable from a binlog
	// that was empty all along.
	for i := 0; i < 5; i++ {
		if err := bl.Append(&binlog.BinlogEntry{
			Type:       binlog.BinlogPut,
			BucketName: "docs",
			Key:        []byte("doc|posts|streamed|en"),
			Value:      []byte("streamed payload"),
		}); err != nil {
			t.Fatal(err)
		}
	}
	if bl.CurrentLSN() == 0 {
		t.Fatal("binlog did not advance; the rest of this test would prove nothing")
	}

	if err := s.restoreFromBackup(source); err != nil {
		t.Fatalf("restore: %v", err)
	}

	if got := bl.OldestLSN(); got != 0 {
		t.Fatalf("oldest LSN = %d after restore, want 0 — a follower would replay "+
			"entries recorded against the database the restore replaced", got)
	}
	if got := bl.Stats().FileSize; got != 0 {
		t.Fatalf("binlog file still holds %d bytes after restore", got)
	}
}
