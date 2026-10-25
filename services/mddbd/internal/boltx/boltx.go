// Package boltx holds small helpers over bbolt shared by mddb's packages.
package boltx

import bolt "go.etcd.io/bbolt"

// EnsureBuckets creates the named top-level buckets that do not exist yet.
//
// It writes only when one is missing. Every manager used to create its
// buckets with an Update at startup, which takes the writer lock — and, on a
// database opened with its freelist deferred (#333), waits until the
// freelist has been rebuilt, which after a crash on a large file is an hour.
// On an existing database the buckets are all there, and a View answers
// that at once.
func EnsureBuckets(db *bolt.DB, names ...[]byte) error {
	missing := false
	if err := db.View(func(tx *bolt.Tx) error {
		for _, n := range names {
			if tx.Bucket(n) == nil {
				missing = true
				break
			}
		}
		return nil
	}); err != nil {
		return err
	}
	if !missing {
		return nil
	}
	return db.Update(func(tx *bolt.Tx) error {
		for _, n := range names {
			if _, err := tx.CreateBucketIfNotExists(n); err != nil {
				return err
			}
		}
		return nil
	})
}
