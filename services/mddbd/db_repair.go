package main

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	bolt "go.etcd.io/bbolt"
)

// Rebuilding a damaged database from what can still be read (#270).
//
// A database whose page tree is damaged cannot be opened for writing — the
// freelist walk panics — and `bbolt compact` hangs on it for the reason given
// in db_integrity.go. What still works is reading it: opened read-only, bbolt
// skips the walk, and the buckets and keys reachable from the root can be
// copied, one by one, into a new file whose pages bbolt lays out afresh. That
// is what the reporter of #270 did by hand with export and add-batch; this
// does it for every bucket, including users, keys and configuration.
//
// A bucket that cannot be read to the end is reported, and whatever was read
// of it is kept.

// repairBatch bounds a write transaction, which holds every page it dirties
// in memory until it commits.
const repairBatch = 10000

// repairReport is what a repair copied and what it could not.
type repairReport struct {
	Keys    int
	Buckets int
	Damaged []string // buckets that could not be read to the end, and why
}

// repairDatabase copies every bucket and key readable in src into a new
// database at dst, which must not exist.
func repairDatabase(src, dst string) (*repairReport, error) {
	if _, err := os.Stat(dst); err == nil {
		return nil, fmt.Errorf("%s already exists; repair writes a new file", dst)
	}
	in, err := openBolt(src, &bolt.Options{ReadOnly: true, Timeout: 5 * time.Second})
	if err != nil {
		return nil, err
	}
	defer func() { _ = in.Close() }()
	out, err := bolt.Open(dst, 0o600, &bolt.Options{Timeout: 5 * time.Second})
	if err != nil {
		return nil, err
	}
	w := &repairWriter{db: out}
	report := &repairReport{}

	err = in.View(func(tx *bolt.Tx) error {
		return tx.ForEach(func(name []byte, b *bolt.Bucket) error {
			path := []string{string(name)}
			if err := w.copyBucket(b, path, report); err != nil {
				report.Damaged = append(report.Damaged, fmt.Sprintf("%s: %v", name, err))
			}
			return nil
		})
	})
	if commitErr := w.commit(); err == nil {
		err = commitErr
	}
	if closeErr := out.Close(); err == nil {
		err = closeErr
	}
	return report, err
}

// repairWriter writes into the new database in bounded transactions.
type repairWriter struct {
	db  *bolt.DB
	tx  *bolt.Tx
	n   int
	got map[string]*bolt.Bucket // buckets resolved in the current transaction
}

// copyBucket copies one bucket and everything under it. A panic while reading
// it — a damaged page — ends this bucket only.
func (w *repairWriter) copyBucket(b *bolt.Bucket, path []string, report *repairReport) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("unreadable past this point: %v", r)
		}
	}()
	dst, err := w.bucket(path)
	if err != nil {
		return err
	}
	if err := dst.SetSequence(b.Sequence()); err != nil {
		return err
	}
	report.Buckets++

	c := b.Cursor()
	for k, v := c.First(); k != nil; k, v = c.Next() {
		if v == nil {
			if child := b.Bucket(k); child != nil {
				if err := w.copyBucket(child, append(path[:len(path):len(path)], string(k)), report); err != nil {
					report.Damaged = append(report.Damaged, fmt.Sprintf("%s/%s: %v", strings.Join(path, "/"), k, err))
				}
				continue
			}
		}
		if err := w.put(path, k, v); err != nil {
			return err
		}
		report.Keys++
	}
	return nil
}

func (w *repairWriter) put(path []string, k, v []byte) error {
	b, err := w.bucket(path)
	if err != nil {
		return err
	}
	if err := b.Put(append([]byte(nil), k...), append([]byte(nil), v...)); err != nil {
		return err
	}
	w.n++
	if w.n >= repairBatch {
		return w.commit()
	}
	return nil
}

// bucket resolves a bucket path in the current write transaction, creating
// it as needed.
func (w *repairWriter) bucket(path []string) (*bolt.Bucket, error) {
	if w.tx == nil {
		tx, err := w.db.Begin(true)
		if err != nil {
			return nil, err
		}
		w.tx, w.n, w.got = tx, 0, map[string]*bolt.Bucket{}
	}
	key := strings.Join(path, "\x00")
	if b, ok := w.got[key]; ok {
		return b, nil
	}
	var b *bolt.Bucket
	var err error
	for i, name := range path {
		if i == 0 {
			b, err = w.tx.CreateBucketIfNotExists([]byte(name))
		} else {
			b, err = b.CreateBucketIfNotExists([]byte(name))
		}
		if err != nil {
			return nil, err
		}
	}
	w.got[key] = b
	return b, nil
}

func (w *repairWriter) commit() error {
	if w.tx == nil {
		return nil
	}
	err := w.tx.Commit()
	w.tx = nil
	return err
}

// runRepair is `mddbd -repair-db SRC -repair-to DST`.
func runRepair(src, dst string) int {
	if dst == "" {
		fmt.Fprintln(os.Stderr, "-repair-db needs -repair-to: the path of the new database to write")
		return 2
	}
	report, err := repairDatabase(src, dst)
	if report != nil {
		fmt.Printf("copied %d keys in %d buckets into %s\n", report.Keys, report.Buckets, dst)
		for _, d := range report.Damaged {
			fmt.Printf("damaged: %s\n", d)
		}
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if err := verifyDatabase(dst); err != nil {
		fmt.Fprintln(os.Stderr, errors.New("the repaired database does not pass the check: "+err.Error()))
		return 1
	}
	fmt.Println("the repaired database passes bbolt's integrity check")
	if report != nil && len(report.Damaged) > 0 {
		return 3 // usable, but not everything was recovered
	}
	return 0
}
