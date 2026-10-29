package main

import (
	"encoding/binary"
	"path/filepath"
	"strings"
	"testing"
	"time"

	bolt "go.etcd.io/bbolt"

	"mddb/internal/binlog"
	"mddb/internal/migrate"
)

// DOC-017: where the schema migration runs besides a plain startup, and what
// happens when it cannot finish. The upgrade itself is in
// upgrade_doc017_test.go.

// A file whose migration fails is not installed: the previous database
// comes back, as for any other file that will not open.
func TestSwapDatabaseRollsBackWhenTheMigrationFails(t *testing.T) {
	s, cleanup := newTestServer(t)
	defer cleanup()
	seedKeptDocument(t, s, "still here")

	source := backupNow(t, s)
	breakSchemaVersion(t, source)

	err := s.withRestoreLock(func() error {
		return s.swapDatabase(source, func(dst string) error { return copyFile(source, dst) })
	})
	if err == nil || !strings.Contains(err.Error(), "schema version") {
		t.Fatalf("swap of a file whose migration fails = %v, want the migration's error", err)
	}
	assertDatabaseStillServing(t, s, "still here")
}

// A collection file whose migration fails keeps every collection file from
// being handed out — the startup check then stops the server — while the
// files that did migrate keep their new version.
func TestCollectionFilesAreNotServedWhenOneFailsToMigrate(t *testing.T) {
	t.Setenv("MDDB_STORAGE_LAYOUT", "per-collection")
	main := unpackPerCollectionFixture(t)
	broken := filepath.Join(collectionDir(main), "broken.db")
	db, err := bolt.Open(broken, 0o600, &bolt.Options{Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	breakSchemaVersion(t, broken)

	s := serverOnFixture(t, main)
	if cf := s.collectionStore(); cf != nil {
		t.Fatal("collection files were served although one could not be migrated")
	}
	// Closed again: the files can be opened by hand and the healthy one
	// carries the version it reached.
	cf, err := openCollectionFiles(collectionDir(main), collectionFileOptions)
	if err != nil {
		t.Fatal(err)
	}
	defer cf.closeAll()
	if v, err := migrate.Version(cf.get("docs")); err != nil || v != migrate.Latest() {
		t.Fatalf("the healthy collection file is at version %d (%v), want %d", v, err, migrate.Latest())
	}
}

// breakSchemaVersion stores a schema version no build can read.
func breakSchemaVersion(t *testing.T, path string) {
	t.Helper()
	db, err := bolt.Open(path, 0o600, &bolt.Options{Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if err := db.Update(func(tx *bolt.Tx) error {
		b, err := tx.CreateBucketIfNotExists(migrate.Bucket)
		if err != nil {
			return err
		}
		return b.Put([]byte("version"), []byte("bad"))
	}); err != nil {
		t.Fatal(err)
	}
}

// A leader on an older release replicates index entries as written; the
// follower stores them as this build reads them.
func TestReplicatedKeyIndexEntriesAreNormalised(t *testing.T) {
	s, cleanup := newTestServer(t)
	defer cleanup()
	defer s.closeCollectionFiles()

	ra := NewReplicationApplier(s)
	raw := []byte("bykey|c|README.md|EN")
	if err := ra.Apply(&binlog.BinlogEntry{LSN: 1, Type: binlog.BinlogPut, BucketName: "bykey", Key: raw, Value: []byte("c|readme.md|en")}); err != nil {
		t.Fatal(err)
	}
	if got := byKeyEntries(t, s, "c"); len(got) != 1 || got["bykey|c|readme.md|en"] != "c|readme.md|en" {
		t.Fatalf("index after a replicated put = %v", got)
	}
	if err := ra.Apply(&binlog.BinlogEntry{LSN: 2, Type: binlog.BinlogDelete, BucketName: "bykey", Key: raw}); err != nil {
		t.Fatal(err)
	}
	if got := byKeyEntries(t, s, "c"); len(got) != 0 {
		t.Fatalf("index after a replicated delete = %v", got)
	}
}

// The version is stored where docs/STORAGE-LAYOUT.md says it is.
func TestSchemaVersionIsWhereTheDocsSayItIs(t *testing.T) {
	t.Setenv("MDDB_STORAGE_LAYOUT", "single")
	s := serverOnFixture(t, filepath.Join(t.TempDir(), "fresh.db"))
	var raw []byte
	if err := s.DBView(func(tx *bolt.Tx) error {
		raw = append(raw, tx.Bucket([]byte("schema_version")).Get([]byte("version"))...)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(raw) != 8 || binary.BigEndian.Uint64(raw) != migrate.Latest() {
		t.Fatalf("schema_version/version = %x, want %d as 8 big-endian bytes", raw, migrate.Latest())
	}
}
