package spell

import (
	"path/filepath"
	"testing"
	"time"

	bolt "go.etcd.io/bbolt"
)

// A manager follows the server's handle after a restore (2.18.3).
func TestFollowDB(t *testing.T) {
	db, err := bolt.Open(filepath.Join(t.TempDir(), "f.db"), 0o600, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	m := NewSpellManager(db)
	m.FollowDB(func() *bolt.DB { return db })
	if m.handle() != db {
		t.Error("handle does not follow the source")
	}
	_ = time.Second
}
