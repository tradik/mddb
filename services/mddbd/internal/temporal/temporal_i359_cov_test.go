package temporal

import (
	"encoding/binary"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"

	"mddb/internal/boltx"
	"mddb/internal/testsync"

	bolt "go.etcd.io/bbolt"
)

// countingRouter routes everything to one file, counts the writes the event
// writer asks for, and can refuse them.
type countingRouter struct {
	boltx.Single
	writes atomic.Int64
	fail   bool
}

func (r *countingRouter) ForWrite(c string) (*bolt.DB, error) {
	r.writes.Add(1)
	if r.fail {
		return nil, errors.New("no file for " + c)
	}
	return r.Single.ForWrite(c)
}

// putRaw stores key/value pairs in a bucket as they are, damaged or not.
func putRaw(t *testing.T, db *bolt.DB, bucket []byte, kv ...string) {
	t.Helper()
	if err := db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucket)
		for i := 0; i+1 < len(kv); i += 2 {
			if err := b.Put([]byte(kv[i]), []byte(kv[i+1])); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func evtValue(ts int64) string {
	return fmt.Sprintf(`{"docId":"d","eventType":"create","timestamp":%d}`, ts)
}

// #359: the event writer goes through the router set on the manager. A
// collection whose file cannot be opened is skipped, and a file without the
// temporal buckets is left alone; the writer keeps going after both.
func TestTemporalWriterSkipsCollectionsItCannotWrite(t *testing.T) {
	for name, fail := range map[string]bool{"no file": true, "no buckets": false} {
		t.Run(name, func(t *testing.T) {
			db, cleanup := newTestDB(t)
			defer cleanup()
			tm := NewTemporalManager(db)
			r := &countingRouter{Single: boltx.Single{DB: db}, fail: fail}
			tm.SetRouter(r)
			tm.Start()
			defer tm.Stop()

			// The writer flushes one collection at a time, so the second
			// write it asks for comes after the first flush has finished.
			tm.RecordAsync("a", "d", EventAccess, "")
			testsync.Wait(t, "the first flush", func() bool { return r.writes.Load() >= 1 })
			tm.RecordAsync("b", "d", EventAccess, "")
			testsync.Wait(t, "the second flush", func() bool { return r.writes.Load() >= 2 })

			if err := tm.EnsureBuckets(); err != nil {
				t.Fatal(err)
			}
			if got, err := tm.QueryRange("a", "d", 0, 1<<62, "", 0); err != nil || len(got) != 0 {
				t.Fatalf("events stored where none could be = %v %v", got, err)
			}
		})
	}
}

// A full batch is written as soon as it fills, without waiting for a tick.
func TestTemporalWriterFlushesAFullBatch(t *testing.T) {
	db, cleanup := newTestDB(t)
	defer cleanup()
	tm := NewTemporalManager(db)
	if err := tm.EnsureBuckets(); err != nil {
		t.Fatal(err)
	}
	const n = 1500
	for range n {
		tm.RecordAsync("c", "d", EventCreate, "")
	}
	tm.Start()
	defer tm.Stop()
	testsync.WaitForCount(t, "every queued event to be stored", n, func() int {
		got, _ := tm.QueryRange("c", "d", 0, 1<<62, "", n+1)
		return len(got)
	})
}

// Readers find nothing without the buckets and report a closed file.
func TestTemporalReadersWithoutBucketsOrFile(t *testing.T) {
	db, cleanup := newTestDB(t)
	defer cleanup()
	tm := NewTemporalManager(db)
	if got, err := tm.QueryRange("c", "d", 0, 10, "", 0); err != nil || got != nil {
		t.Fatalf("QueryRange without buckets = %v %v", got, err)
	}
	if got, err := tm.GetHotDocs("c", 0, 0); err != nil || got != nil {
		t.Fatalf("GetHotDocs without buckets = %v %v", got, err)
	}
	if got, err := tm.ComputeHistogram("c", "", "day", 0, 10); err != nil || got != nil {
		t.Fatalf("ComputeHistogram without buckets = %v %v", got, err)
	}

	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := tm.GetHotDocs("c", 0, 0); err == nil {
		t.Error("GetHotDocs on a closed file succeeded")
	}
	if _, err := tm.ComputeHistogram("c", "", "day", 0, 10); err == nil {
		t.Error("ComputeHistogram on a closed file succeeded")
	}
}

// Damaged events are skipped, a scan stops at the next collection and at
// its limit, and the histogram comes out in time order.
func TestTemporalEventScansSkipDamagedEntries(t *testing.T) {
	db, cleanup := newTestDB(t)
	defer cleanup()
	tm := NewTemporalManager(db)
	if err := tm.EnsureBuckets(); err != nil {
		t.Fatal(err)
	}
	const day = 24 * 60 * 60
	putRaw(t, db, bucketTemporal,
		"evt|c|d|create|1", evtValue(5*day),
		"evt|c|d|create|2", evtValue(1*day),
		"evt|c|d|create|3", "{damaged",
		"evt|e|d|create|1", evtValue(1*day),
	)

	if got, err := tm.QueryRange("c", "d", 0, 10*day, "", 0); err != nil || len(got) != 2 {
		t.Fatalf("QueryRange = %v %v, want the two readable events", got, err)
	}
	if got, err := tm.scanCollectionEvents("c", "", 0, 10*day, 1); err != nil || len(got) != 1 {
		t.Fatalf("scan with limit 1 = %v %v", got, err)
	}
	if got, err := tm.scanCollectionEvents("c", "update", 0, 10*day, 0); err != nil || len(got) != 0 {
		t.Fatalf("scan for another event type = %v %v", got, err)
	}
	buckets, err := tm.ComputeHistogram("c", "", "day", 0, 10*day)
	if err != nil || len(buckets) != 2 || buckets[0].From > buckets[1].From {
		t.Fatalf("histogram = %v %v, want two days in order", buckets, err)
	}
}

// The hot list skips damaged counters and entries older than since, and
// stops at the next collection.
func TestTemporalHotDocsSkipDamagedAndOldEntries(t *testing.T) {
	db, cleanup := newTestDB(t)
	defer cleanup()
	tm := NewTemporalManager(db)
	if err := tm.EnsureBuckets(); err != nil {
		t.Fatal(err)
	}
	counter := func(count uint64, lastTS int64) string {
		var buf [16]byte
		binary.LittleEndian.PutUint64(buf[:8], count)
		binary.LittleEndian.PutUint64(buf[8:], uint64(lastTS)) //nolint:gosec // test values are positive
		return string(buf[:])
	}
	putRaw(t, db, bucketTemporalHot,
		"hot|c|new", counter(3, 200),
		"hot|c|old", counter(9, 50),
		"hot|c|bad", "short",
		"hot|e|other", counter(1, 200),
	)
	got, err := tm.GetHotDocs("c", 0, 100)
	if err != nil || len(got) != 1 || got[0].DocID != "new" {
		t.Fatalf("hot docs since 100 = %v %v, want only new", got, err)
	}
}
