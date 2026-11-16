package ttl

import (
	"testing"
	"time"

	bolt "go.etcd.io/bbolt"
)

// A TTL write that fails part-way fails whole (GO-051: its binlog entries
// are written by the transaction): here the forward key is taken by a
// nested bucket, so it cannot be stored as a key.
func TestASetThatCannotStoreItsKeyFails(t *testing.T) {
	mgr, db, cleanup := newTestTTLManager(t)
	defer cleanup()
	const at = 2000000000
	if err := db.Update(func(tx *bolt.Tx) error {
		_, err := tx.Bucket(bucketTTL).CreateBucket(ttlKey(at, "c", "d"))
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := mgr.Set("c", "d", at); err == nil {
		t.Fatal("stored a TTL over a nested bucket")
	}
}

// The cleanup loop ends when the manager stops.
func TestCleanupStopsWithTheManager(t *testing.T) {
	mgr, _, cleanup := newTestTTLManager(t)
	defer cleanup()
	mgr.StartCleanup(time.Hour)
	mgr.Stop()
	time.Sleep(20 * time.Millisecond) // the loop sees the stop and returns
}
