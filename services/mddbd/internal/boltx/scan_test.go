package boltx

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"

	bolt "go.etcd.io/bbolt"
)

func scanDB(t *testing.T, n int) *bolt.DB {
	t.Helper()
	db, err := bolt.Open(filepath.Join(t.TempDir(), "s.db"), 0o600, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	_ = db.Update(func(tx *bolt.Tx) error {
		b, _ := tx.CreateBucketIfNotExists([]byte("b"))
		for i := 0; i < n; i++ {
			_ = b.Put([]byte(fmt.Sprintf("a|%05d", i)), []byte("v"))
		}
		_ = b.Put([]byte("z|other"), []byte("v"))
		_, _ = b.CreateBucket([]byte("a|nested"))
		return nil
	})
	return db
}

func TestScanCrossesSlices(t *testing.T) {
	db := scanDB(t, 2500)
	old := ScanSlice
	ScanSlice.Keys = 100
	defer func() { ScanSlice = old }()

	seen, last := 0, ""
	if err := Scan(db, []byte("b"), []byte("a|"), func(k, _ []byte) bool {
		if string(k) <= last {
			t.Fatalf("out of order or repeated: %s after %s", k, last)
		}
		last = string(k)
		seen++
		return true
	}); err != nil {
		t.Fatal(err)
	}
	if seen != 2500 {
		t.Errorf("saw %d keys, want 2500", seen)
	}
	all := 0
	_ = Scan(db, []byte("b"), nil, func(_, _ []byte) bool { all++; return true })
	if all != 2501 {
		t.Errorf("whole bucket: %d", all)
	}
	stop := 0
	_ = Scan(db, []byte("b"), nil, func(_, _ []byte) bool { stop++; return stop < 5 })
	if stop != 5 {
		t.Errorf("stop after 5: %d", stop)
	}
	if err := Scan(db, []byte("missing"), nil, func(_, _ []byte) bool { return true }); err != nil {
		t.Error(err)
	}
	_ = db.Close()
	if err := Scan(db, []byte("b"), nil, func(_, _ []byte) bool { return true }); err == nil {
		t.Error("closed database")
	}
}

// A write that grows the file needs the mmap lock; with the scan in short
// transactions it completes while the scan is still going.
func TestScanLetsAGrowingWriteThrough(t *testing.T) {
	db := scanDB(t, 3000)
	old := ScanSlice
	ScanSlice.Keys = 50
	defer func() { ScanSlice = old }()

	wrote := make(chan time.Duration, 1)
	started := make(chan struct{})
	go func() {
		<-started
		t0 := time.Now()
		_ = db.Update(func(tx *bolt.Tx) error {
			b := tx.Bucket([]byte("b"))
			for i := 0; i < 2000; i++ {
				_ = b.Put([]byte(fmt.Sprintf("grow|%05d", i)), make([]byte, 4096)) // past the initial map
			}
			return nil
		})
		wrote <- time.Since(t0)
	}()
	seen := 0
	writeDone := false
	_ = Scan(db, []byte("b"), []byte("a|"), func(_, _ []byte) bool {
		if seen++; seen == 10 {
			close(started)
		}
		if seen%50 == 0 { // give the writer the gap between slices
			time.Sleep(2 * time.Millisecond)
		}
		select {
		case <-wrote:
			writeDone = true
		default:
		}
		return true
	})
	if !writeDone {
		select {
		case <-wrote:
			t.Log("the write finished as the scan ended")
		case <-time.After(5 * time.Second):
			t.Fatal("the growing write never completed")
		}
	}
	if seen != 3000 {
		t.Errorf("scan saw %d keys", seen)
	}
}

func TestScanAll(t *testing.T) {
	a, b := scanDB(t, 3), scanDB(t, 4)
	n := 0
	if err := ScanAll(multi{a, b}, []byte("b"), []byte("a|"), func(_, _ []byte) bool { n++; return true }); err != nil || n != 7 {
		t.Errorf("both files: %d %v", n, err)
	}
	_ = b.Close()
	if err := ScanAll(multi{a, b}, []byte("b"), nil, func(_, _ []byte) bool { return true }); err == nil {
		t.Error("a closed file")
	}
}

type multi []*bolt.DB

func (m multi) ForRead(string) *bolt.DB           { return m[0] }
func (m multi) ForWrite(string) (*bolt.DB, error) { return m[0], nil }
func (m multi) All() []*bolt.DB                   { return m }
