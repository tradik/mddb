package main

import (
	"context"
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
