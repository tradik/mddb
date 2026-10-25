package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"mddb/internal/boltx"

	bolt "go.etcd.io/bbolt"
)

// Moving collections between the layouts (#336), offline, with the server
// stopped: `mddbd -split-collections FILE` moves every collection's keys
// out of the main file into "<FILE>.d/<collection>.db", and
// `mddbd -merge-collections FILE` moves them back. Keys are copied in
// batches, counted, and only then deleted from where they came from, so an
// interrupted run leaves every key in at least one place and can be run
// again.

const migrateBatchBytes = 32 << 20

type migrateReport struct {
	Keys        int
	Collections map[string]int
	Skipped     int // keys of a collection bucket with no collection in them
}

// runSplitCollections is `mddbd -split-collections FILE`.
func runSplitCollections(path string) int {
	r, err := splitCollections(path)
	if r != nil {
		fmt.Printf("moved %d keys of %d collections into %s\n", r.Keys, len(r.Collections), collectionDir(path))
		if r.Skipped > 0 {
			fmt.Printf("left %d keys that name no collection in the main file\n", r.Skipped)
		}
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	fmt.Println("start the server with MDDB_STORAGE_LAYOUT=per-collection; mddbd -compact-db gives the main file's space back")
	return 0
}

// runMergeCollections is `mddbd -merge-collections FILE`.
func runMergeCollections(path string) int {
	r, err := mergeCollections(path)
	if r != nil {
		fmt.Printf("moved %d keys of %d collections back into %s\n", r.Keys, len(r.Collections), path)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	fmt.Println("start the server without MDDB_STORAGE_LAYOUT (or with =single)")
	return 0
}

func openForMigration(path string) (*bolt.DB, error) {
	db, err := openBolt(path, boltOptionsFor(path))
	if err != nil {
		return nil, fmt.Errorf("opening %s: %w (stop the server first)", path, err)
	}
	return db, nil
}

// splitCollections moves collection keys from the main file to one file per
// collection.
func splitCollections(path string) (*migrateReport, error) {
	if _, err := os.Stat(path); err != nil {
		return nil, err
	}
	main, err := openForMigration(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = main.Close() }()
	files, err := openCollectionFiles(collectionDir(path), collectionFileOptions)
	if err != nil {
		return nil, err
	}
	defer files.closeAll()

	report := &migrateReport{Collections: map[string]int{}}
	for _, name := range collectionBuckets {
		moved, skipped, err := splitBucket(main, files, string(name), report)
		if err != nil {
			return report, fmt.Errorf("bucket %s: %w", name, err)
		}
		if err := deleteKeys(main, name, moved); err != nil {
			return report, fmt.Errorf("bucket %s: removing moved keys: %w", name, err)
		}
		report.Skipped += skipped
	}
	if err := persistFreelist(main); err != nil {
		return report, err
	}
	for _, db := range files.all() {
		_ = persistFreelist(db)
	}
	return report, nil
}

// splitBucket copies one bucket's keys to their collections' files and
// returns the keys copied, after checking every one arrived.
func splitBucket(main *bolt.DB, files *collectionFiles, bucket string, report *migrateReport) ([][]byte, int, error) {
	type kv struct{ k, v []byte }
	pending := map[string][]kv{}
	size := 0
	var moved [][]byte
	skipped := 0

	flush := func() error {
		for c, items := range pending {
			db, err := files.getOrCreate(c)
			if err != nil {
				return err
			}
			if err := db.Update(func(tx *bolt.Tx) error {
				b, err := tx.CreateBucketIfNotExists([]byte(bucket))
				if err != nil {
					return err
				}
				for _, it := range items {
					if err := b.Put(it.k, it.v); err != nil {
						return err
					}
				}
				return nil
			}); err != nil {
				return err
			}
		}
		pending, size = map[string][]kv{}, 0
		return nil
	}

	err := main.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(bucket))
		if b == nil {
			return nil
		}
		return b.ForEach(func(k, v []byte) error {
			if v == nil { // a nested bucket: not part of mddb's schema
				skipped++
				return nil
			}
			c, ok := keyCollection(bucket, k)
			if !ok {
				skipped++
				return nil
			}
			item := kv{append([]byte(nil), k...), append([]byte(nil), v...)}
			pending[c] = append(pending[c], item)
			moved = append(moved, item.k)
			report.Keys++
			report.Collections[c]++
			if size += len(k) + len(v); size >= migrateBatchBytes {
				return flush()
			}
			return nil
		})
	})
	if err == nil {
		err = flush()
	}
	if err != nil {
		return nil, skipped, err
	}
	// Every key must be in its file before any is deleted from the main one.
	for _, k := range moved {
		c, _ := keyCollection(bucket, k)
		found := false
		_ = files.get(c).View(func(tx *bolt.Tx) error {
			found = tx.Bucket([]byte(bucket)) != nil && tx.Bucket([]byte(bucket)).Get(k) != nil
			return nil
		})
		if !found {
			return nil, skipped, fmt.Errorf("key %q did not arrive in %s", k, collectionFileName(c))
		}
	}
	return moved, skipped, nil
}

// deleteKeys deletes keys from a bucket in bounded transactions.
func deleteKeys(db *bolt.DB, bucket []byte, keys [][]byte) error {
	for start := 0; start < len(keys); start += 10000 {
		end := min(start+10000, len(keys))
		if err := db.Update(func(tx *bolt.Tx) error {
			b := tx.Bucket(bucket)
			if b == nil {
				return nil
			}
			for _, k := range keys[start:end] {
				if err := b.Delete(k); err != nil {
					return err
				}
			}
			return nil
		}); err != nil {
			return err
		}
	}
	return nil
}

// mergeCollections moves every collection file's keys back into the main
// file and removes the files.
func mergeCollections(path string) (*migrateReport, error) {
	if _, err := os.Stat(path); err != nil {
		return nil, err
	}
	dir := collectionDir(path)
	if _, err := os.Stat(dir); err != nil {
		return nil, fmt.Errorf("no collection files at %s", dir)
	}
	main, err := openForMigration(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = main.Close() }()
	if err := boltx.EnsureBuckets(main, collectionBuckets...); err != nil {
		return nil, err
	}
	files, err := openCollectionFiles(dir, collectionFileOptions)
	if err != nil {
		return nil, err
	}
	defer files.closeAll()

	report := &migrateReport{Collections: map[string]int{}}
	for _, c := range files.names() {
		n, err := copyAllKeys(files.get(c), main)
		if err != nil {
			return report, fmt.Errorf("collection %q: %w", c, err)
		}
		report.Keys += n
		report.Collections[c] = n
		if err := files.remove(c); err != nil {
			return report, fmt.Errorf("collection %q: removing its file: %w", c, err)
		}
	}
	if err := persistFreelist(main); err != nil {
		return report, err
	}
	if err := os.Remove(dir); err != nil && !errors.Is(err, os.ErrNotExist) {
		return report, fmt.Errorf("%s is not empty: %w", filepath.Base(dir), err)
	}
	return report, nil
}

// copyAllKeys copies every top-level bucket of src into dst, in batches, and
// checks the copy.
func copyAllKeys(src, dst *bolt.DB) (int, error) {
	total := 0
	err := src.View(func(stx *bolt.Tx) error {
		return stx.ForEach(func(name []byte, sb *bolt.Bucket) error {
			var batch [][2][]byte
			size := 0
			flush := func() error {
				if len(batch) == 0 {
					return nil
				}
				err := dst.Update(func(tx *bolt.Tx) error {
					b, err := tx.CreateBucketIfNotExists(name)
					if err != nil {
						return err
					}
					for _, kv := range batch {
						if err := b.Put(kv[0], kv[1]); err != nil {
							return err
						}
					}
					return nil
				})
				batch, size = nil, 0
				return err
			}
			if err := sb.ForEach(func(k, v []byte) error {
				if v == nil {
					return nil
				}
				batch = append(batch, [2][]byte{append([]byte(nil), k...), append([]byte(nil), v...)})
				total++
				if size += len(k) + len(v); size >= migrateBatchBytes {
					return flush()
				}
				return nil
			}); err != nil {
				return err
			}
			if err := flush(); err != nil {
				return err
			}
			// Check the copy before the source is removed.
			return dst.View(func(dtx *bolt.Tx) error {
				db := dtx.Bucket(name)
				return sb.ForEach(func(k, v []byte) error {
					if v != nil && (db == nil || !bytes.Equal(db.Get(k), v)) {
						return fmt.Errorf("key %q of %s did not arrive intact", k, name)
					}
					return nil
				})
			})
		})
	})
	return total, err
}
