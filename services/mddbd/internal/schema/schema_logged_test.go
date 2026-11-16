package schema

import (
	"errors"
	"path/filepath"
	"testing"

	"mddb/internal/binlog"

	bolt "go.etcd.io/bbolt"
)

// A schema write is refused with its transaction when the node's log
// refuses it (GO-051), and a reload whose bucket cannot be made fails.
func TestSchemaWritesFollowTheLogsGate(t *testing.T) {
	dir := t.TempDir()
	db, err := bolt.Open(filepath.Join(dir, "s.db"), 0o600, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	bl, err := binlog.NewBinlog(filepath.Join(dir, "s.db"), binlog.BinlogConfig{RetentionInterval: -1})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = bl.Close() }()
	sm := NewSchemaManager(db)
	if err := sm.EnsureBucket(); err != nil {
		t.Fatal(err)
	}
	sm.SetBinlog(bl)
	gate := errors.New("not the leader")
	bl.SetWriteGate(func() error { return gate })
	if err := sm.Set("c", `{"type":"object"}`); !errors.Is(err, gate) {
		t.Fatalf("set: %v", err)
	}
	if err := sm.Delete("c"); !errors.Is(err, gate) {
		t.Fatalf("delete: %v", err)
	}

	ro, err := bolt.Open(filepath.Join(dir, "s.db.ro"), 0o600, nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = ro.Close()
	ro, err = bolt.Open(filepath.Join(dir, "s.db.ro"), 0o600, &bolt.Options{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ro.Close() }()
	if err := sm.Reload(ro); err == nil {
		t.Fatal("a reload onto a read-only file without the bucket")
	}
}
