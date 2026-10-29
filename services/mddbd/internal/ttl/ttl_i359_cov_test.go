package ttl

import (
	"path/filepath"
	"testing"
	"time"

	"mddb/internal/boltx"

	bolt "go.etcd.io/bbolt"
)

// A manager given a router (#336, #359) writes and reads through it, and a
// file the router names without the TTL bucket expires nothing.
func TestTTLRouterAndAFileWithoutTheBucket(t *testing.T) {
	db, err := bolt.Open(filepath.Join(t.TempDir(), "ttl.db"), 0o600, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	other, err := bolt.Open(filepath.Join(t.TempDir(), "other.db"), 0o600, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = other.Close() }()

	tm := NewTTLManager(db, stubReaper{})
	tm.SetRouter(boltx.Single{DB: other})
	// No bucket in the routed file: cleanup finds nothing and does not fail.
	tm.cleanup()

	routed := NewTTLManager(other, stubReaper{})
	if err := routed.EnsureBuckets(); err != nil {
		t.Fatal(err)
	}
	if err := tm.Set("c", "d", time.Now().Add(time.Hour).Unix()); err != nil {
		t.Fatalf("Set through the router: %v", err)
	}
	if err := tm.Remove("c", "d"); err != nil {
		t.Fatalf("Remove through the router: %v", err)
	}
}
