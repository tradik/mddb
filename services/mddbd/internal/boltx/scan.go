package boltx

import (
	"bytes"
	"time"

	bolt "go.etcd.io/bbolt"
)

// Scanning a bucket without holding one long read transaction (#350).
//
// A bbolt read transaction holds the mmap read lock for as long as it is
// open. A write that grows the file has to remap, which takes that lock
// exclusively and so waits for every open reader — and while it waits, no
// new read transaction can begin. One walk over a 100 GB database inside a
// single transaction therefore stopped every request on the server,
// /health included, until it finished. Scan reads in slices, each in its own
// short transaction, and resumes after the last key, so a remap waits at most
// one slice.

// ScanSlice bounds one transaction of a Scan.
var ScanSlice = struct {
	Keys int
	Time time.Duration
}{Keys: 10000, Time: 50 * time.Millisecond}

// Scan calls visit for every key of bucket that has prefix (all keys for a
// nil prefix), in order, across as many read transactions as it takes. k and
// v are valid only during the call. A visit returning false stops the scan.
// The keys seen are each key's state at the time of its slice: a scan is not
// a snapshot of the whole bucket.
func Scan(db *bolt.DB, bucket, prefix []byte, visit func(k, v []byte) bool) error {
	var resume []byte
	for {
		var next []byte
		stopped := false
		err := db.View(func(tx *bolt.Tx) error {
			b := tx.Bucket(bucket)
			if b == nil {
				return nil
			}
			c := b.Cursor()
			var k, v []byte
			switch {
			case resume != nil:
				k, v = c.Seek(resume)
				if bytes.Equal(k, resume) {
					k, v = c.Next()
				}
			case prefix != nil:
				k, v = c.Seek(prefix)
			default:
				k, v = c.First()
			}
			start, n := time.Now(), 0
			for ; k != nil && bytes.HasPrefix(k, prefix); k, v = c.Next() {
				if v == nil { // a nested bucket
					continue
				}
				if !visit(k, v) {
					stopped = true
					return nil
				}
				if n++; n >= ScanSlice.Keys || time.Since(start) >= ScanSlice.Time {
					next = append([]byte(nil), k...)
					return nil
				}
			}
			return nil
		})
		if err != nil || stopped || next == nil {
			return err
		}
		resume = next
	}
}

// ScanAll is Scan over every file the router holds collection data in.
func ScanAll(r Router, bucket, prefix []byte, visit func(k, v []byte) bool) error {
	for _, db := range r.All() {
		if err := Scan(db, bucket, prefix, visit); err != nil {
			return err
		}
	}
	return nil
}
