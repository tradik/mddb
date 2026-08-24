package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

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

// SEC-015/SEC-016: a successful restore must serve the backup's data on the
// live handle immediately — the gRPC path used to copy underneath the open
// file, keep serving the old data and report success.
func TestRestoreFromBackup_ServesBackupDataWithoutRestart(t *testing.T) {
	s, cleanup := newTestServer(t)
	defer cleanup()
	g := &GRPCServer{server: s}
	ctx := context.Background()

	if _, _, err := s.addDocument("posts", "kept", "en", nil, "survives", 0, true); err != nil {
		t.Fatal(err)
	}
	backup := backupNow(t, s)

	if _, _, err := s.addDocument("posts", "later", "en", nil, "added after backup", 0, true); err != nil {
		t.Fatal(err)
	}
	// Prime the read cache with the post-backup document; the restore must
	// drop it, or a gRPC Get resurrects a document the backup never had.
	if _, err := g.Get(ctx, &proto.GetRequest{Collection: "posts", Key: "later", Lang: "en"}); err != nil {
		t.Fatal(err)
	}

	if err := s.restoreFromBackup(backup); err != nil {
		t.Fatal(err)
	}

	got, err := g.Get(ctx, &proto.GetRequest{Collection: "posts", Key: "kept", Lang: "en"})
	if err != nil {
		t.Fatalf("document from the backup not served after restore: %v", err)
	}
	if got.ContentMd != "survives" {
		t.Fatalf("ContentMd = %q, want %q", got.ContentMd, "survives")
	}
	if _, err := g.Get(ctx, &proto.GetRequest{Collection: "posts", Key: "later", Lang: "en"}); err == nil {
		t.Fatal("post-backup document still served after restore — stale handle or stale cache")
	}

	// Writes after the restore must land in the restored database.
	if _, _, err := s.addDocument("posts", "fresh", "en", nil, "post-restore write", 0, true); err != nil {
		t.Fatalf("write after restore failed: %v", err)
	}
}

// SEC-015: a restore that fails must leave the previous database open and
// serving — the HTTP path used to return an error with the database closed,
// or already overwritten by an unusable file.
func TestRestoreFromBackup_FailureRollsBack(t *testing.T) {
	s, cleanup := newTestServer(t)
	defer cleanup()

	if _, _, err := s.addDocument("posts", "kept", "en", nil, "still here", 0, true); err != nil {
		t.Fatal(err)
	}

	garbage := filepath.Join(t.TempDir(), "garbage.db")
	if err := os.WriteFile(garbage, []byte("this is not a bolt database"), 0600); err != nil {
		t.Fatal(err)
	}

	for _, from := range []string{garbage, filepath.Join(t.TempDir(), "missing.db")} {
		if err := s.restoreFromBackup(from); err == nil {
			t.Fatalf("restore from %s succeeded, want error", from)
		}
	}

	// The original database must still be open and intact.
	g := &GRPCServer{server: s}
	got, err := g.Get(context.Background(), &proto.GetRequest{Collection: "posts", Key: "kept", Lang: "en"})
	if err != nil {
		t.Fatalf("database unusable after failed restore: %v", err)
	}
	if got.ContentMd != "still here" {
		t.Fatalf("ContentMd = %q, want %q", got.ContentMd, "still here")
	}
	if _, _, err := s.addDocument("posts", "fresh", "en", nil, "write still works", 0, true); err != nil {
		t.Fatalf("write after failed restore: %v", err)
	}
	if _, err := os.Stat(s.Path + ".pre-restore"); !os.IsNotExist(err) {
		t.Fatal("pre-restore snapshot left behind after rollback")
	}
}

// SEC-017: the MCP tool is a third entry point into restore, and it did not go
// through the contract above — it hand-rolled close → copy → reopen with no
// validation, no restore lock, no rollback and no cache rebuild. An agent
// calling the tool with any readable file inside the backup directory would
// overwrite the live database with it and leave the server holding a closed
// handle.
func TestDirectClientRestore_FailureLeavesTheDatabaseServing(t *testing.T) {
	s, cleanup := newTestServer(t)
	defer cleanup()
	backupDir := t.TempDir()
	t.Setenv("MDDB_BACKUP_DIR", backupDir)

	if _, _, err := s.addDocument("posts", "kept", "en", nil, "still here", 0, true); err != nil {
		t.Fatal(err)
	}

	// A file that exists, sits inside the jail, and is not a database. The path
	// check passes; only opening it as bolt can tell the difference.
	if err := os.WriteFile(filepath.Join(backupDir, "garbage.db"),
		[]byte("this is not a bolt database"), 0600); err != nil {
		t.Fatal(err)
	}

	c := NewDirectClient(s)
	if _, err := c.Restore(context.Background(), &MCPRestoreRequest{From: "garbage.db"}); err == nil {
		t.Fatal("restore from a non-database succeeded")
	}

	// The previous database must still be open and serving.
	g := &GRPCServer{server: s}
	doc, err := g.Get(context.Background(), &proto.GetRequest{Collection: "posts", Key: "kept", Lang: "en"})
	if err != nil {
		t.Fatalf("database not readable after a failed MCP restore: %v", err)
	}
	if doc.ContentMd != "still here" {
		t.Fatalf("ContentMd = %q, want %q", doc.ContentMd, "still here")
	}
	if _, _, err := s.addDocument("posts", "after", "en", nil, "write still works", 0, true); err != nil {
		t.Fatalf("write after a failed MCP restore failed: %v", err)
	}
}

// SEC-017: and a successful MCP restore must behave like the other two — the
// restored data visible immediately, the pre-restore data gone.
func TestDirectClientRestore_ServesBackupDataWithoutRestart(t *testing.T) {
	s, cleanup := newTestServer(t)
	defer cleanup()
	backupDir := t.TempDir()
	t.Setenv("MDDB_BACKUP_DIR", backupDir)

	if _, _, err := s.addDocument("posts", "kept", "en", nil, "survives", 0, true); err != nil {
		t.Fatal(err)
	}

	c := NewDirectClient(s)
	if _, err := c.Backup(context.Background(), &MCPBackupRequest{To: "snap.db"}); err != nil {
		t.Fatalf("backup: %v", err)
	}

	if _, _, err := s.addDocument("posts", "later", "en", nil, "added after backup", 0, true); err != nil {
		t.Fatal(err)
	}
	// Prime the read cache, so a stale cache is distinguishable from a stale handle.
	g := &GRPCServer{server: s}
	ctx := context.Background()
	if _, err := g.Get(ctx, &proto.GetRequest{Collection: "posts", Key: "later", Lang: "en"}); err != nil {
		t.Fatal(err)
	}

	if _, err := c.Restore(context.Background(), &MCPRestoreRequest{From: "snap.db"}); err != nil {
		t.Fatalf("restore: %v", err)
	}

	got, err := g.Get(ctx, &proto.GetRequest{Collection: "posts", Key: "kept", Lang: "en"})
	if err != nil {
		t.Fatalf("document from the backup not served after restore: %v", err)
	}
	if got.ContentMd != "survives" {
		t.Fatalf("ContentMd = %q, want %q", got.ContentMd, "survives")
	}
	if _, err := g.Get(ctx, &proto.GetRequest{Collection: "posts", Key: "later", Lang: "en"}); err == nil {
		t.Fatal("post-backup document still served after restore — stale handle or stale cache")
	}
}

// SEC-018: the replication snapshot install is the third caller of the swap
// contract. It used to check nothing before renaming the received file over the
// live database, so a truncated stream destroyed the follower's data and left it
// holding a closed handle.
func TestReplicaSnapshotInstall_RejectsATruncatedStreamWithoutLosingData(t *testing.T) {
	s, cleanup := newTestServer(t)
	defer cleanup()

	if _, _, err := s.addDocument("posts", "kept", "en", nil, "still here", 0, true); err != nil {
		t.Fatal(err)
	}

	// What a half-received snapshot looks like on disk: a real file, at the
	// path the follower is about to install from, that is not a database.
	truncated := filepath.Join(t.TempDir(), "snapshot.tmp")
	if err := os.WriteFile(truncated, []byte("first few bytes of a bolt file"), 0600); err != nil {
		t.Fatal(err)
	}

	rc := &ReplicationClient{server: s}
	err := s.withRestoreLock(func() error { return rc.replaceDatabase(truncated) })
	if err == nil {
		t.Fatal("installing a truncated snapshot succeeded")
	}

	g := &GRPCServer{server: s}
	doc, err := g.Get(context.Background(), &proto.GetRequest{Collection: "posts", Key: "kept", Lang: "en"})
	if err != nil {
		t.Fatalf("follower database not readable after a rejected snapshot: %v", err)
	}
	if doc.ContentMd != "still here" {
		t.Fatalf("ContentMd = %q, want %q", doc.ContentMd, "still here")
	}
	if _, _, err := s.addDocument("posts", "after", "en", nil, "write still works", 0, true); err != nil {
		t.Fatalf("write after a rejected snapshot failed: %v", err)
	}
}

// A snapshot install moves the file rather than copying it — the follower owns
// the temp file and copying a multi-gigabyte database to then delete the
// original is waste. Asserting it because "rename" and "copy" are otherwise
// indistinguishable from the outside, right up until a disk fills.
func TestReplicaSnapshotInstall_ConsumesTheSnapshotFile(t *testing.T) {
	s, cleanup := newTestServer(t)
	defer cleanup()

	if _, _, err := s.addDocument("posts", "old", "en", nil, "pre-snapshot", 0, true); err != nil {
		t.Fatal(err)
	}

	source, sourceCleanup := newTestServer(t)
	if _, _, err := source.addDocument("posts", "new", "en", nil, "from the leader", 0, true); err != nil {
		t.Fatal(err)
	}
	snapshot := backupNow(t, source)
	sourceCleanup()

	rc := &ReplicationClient{server: s}
	if err := s.withRestoreLock(func() error { return rc.replaceDatabase(snapshot) }); err != nil {
		t.Fatalf("snapshot install: %v", err)
	}

	if _, err := os.Stat(snapshot); !os.IsNotExist(err) {
		t.Fatalf("snapshot file still at %s after install (err=%v) — it was copied, not moved", snapshot, err)
	}

	g := &GRPCServer{server: s}
	ctx := context.Background()
	got, err := g.Get(ctx, &proto.GetRequest{Collection: "posts", Key: "new", Lang: "en"})
	if err != nil {
		t.Fatalf("leader's document not served after snapshot install: %v", err)
	}
	if got.ContentMd != "from the leader" {
		t.Fatalf("ContentMd = %q, want %q", got.ContentMd, "from the leader")
	}
	if _, err := g.Get(ctx, &proto.GetRequest{Collection: "posts", Key: "old", Lang: "en"}); err == nil {
		t.Fatal("pre-snapshot document still served — stale handle or stale cache")
	}
}

// The rollback is the reason this contract exists, and until these two tests it
// had never run: every existing failure test supplied a source that validation
// rejected, so the swap returned before touching the live file. Coverage showed
// the whole rollback closure and both of its call sites at zero executions.
//
// Reaching it needs a source that validates and an install that then fails,
// which is what makes install a parameter rather than a hardcoded copy.

func TestSwapDatabase_RollsBackWhenTheInstallFails(t *testing.T) {
	s, cleanup := newTestServer(t)
	defer cleanup()

	if _, _, err := s.addDocument("posts", "kept", "en", nil, "still here", 0, true); err != nil {
		t.Fatal(err)
	}
	source := backupNow(t, s) // a real database, so validation passes

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

	if _, _, err := s.addDocument("posts", "kept", "en", nil, "still here", 0, true); err != nil {
		t.Fatal(err)
	}
	source := backupNow(t, s)

	// install succeeds and leaves something unopenable at the live path — the
	// case a checksum cannot catch and only the reopen discovers.
	err := s.withRestoreLock(func() error {
		return s.swapDatabase(source, func(dst string) error {
			return os.WriteFile(dst, []byte("installed, but not a database"), 0600)
		})
	})
	if err == nil {
		t.Fatal("swapDatabase succeeded after installing a file bolt cannot open")
	}

	assertDatabaseStillServing(t, s, "still here")

	// And the rollback must have replaced the unopenable file rather than
	// leaving it beside a snapshot nobody looks at again.
	if _, statErr := os.Stat(s.Path + ".pre-restore"); !os.IsNotExist(statErr) {
		t.Fatalf("rollback snapshot still at %s.pre-restore (err=%v)", s.Path, statErr)
	}
}

// assertDatabaseStillServing checks the live handle both reads and writes —
// a read alone can be answered from cache while the database underneath is
// closed, which is how a dead server looks healthy.
func assertDatabaseStillServing(t *testing.T, s *Server, wantContent string) {
	t.Helper()

	g := &GRPCServer{server: s}
	doc, err := g.Get(context.Background(), &proto.GetRequest{Collection: "posts", Key: "kept", Lang: "en"})
	if err != nil {
		t.Fatalf("database not readable after rollback: %v", err)
	}
	if doc.ContentMd != wantContent {
		t.Fatalf("ContentMd = %q, want %q", doc.ContentMd, wantContent)
	}
	if _, _, err := s.addDocument("posts", "written-after", "en", nil, "write works", 0, true); err != nil {
		t.Fatalf("database not writable after rollback: %v", err)
	}
}
