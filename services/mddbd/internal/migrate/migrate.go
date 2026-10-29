// Package migrate brings a database file to the on-disk schema this build
// writes (DOC-017).
//
// Every bbolt file MDDB keeps — the main file and, with
// MDDB_STORAGE_LAYOUT=per-collection, each collection file — records its
// schema version under the key "version" of the bucket "schema_version", as
// eight big-endian bytes. A file without it is version 0: everything written
// before 2.18.9.
//
// Run applies, in order, every step newer than the file's version, and
// records each step's version once that step has finished. A step must be
// idempotent — a crash part-way through leaves the old version recorded, so
// the step runs again from the start at the next open — and may commit its
// work in several transactions, so a large file is not rewritten in one.
//
// The server calls Run on every file before it reads or writes it: at
// startup, when a restore or a replication snapshot installs a file, and
// when a collection file is opened or created. A file already at the
// current version costs one read transaction.
package migrate

import (
	"encoding/binary"
	"fmt"
	"log/slog"
	"path/filepath"
	"time"

	bolt "go.etcd.io/bbolt"
)

// Bucket holds the schema version of the file it is in.
var Bucket = []byte("schema_version")

// versionKey is the key under Bucket that holds the version.
var versionKey = []byte("version")

// Step is one change to the on-disk format.
type Step struct {
	// Version is the schema version a file has once the step has run.
	// Steps are listed in increasing order.
	Version uint64
	// Name says what the step changes, for the log.
	Name string
	// Run changes the file. It must be safe to run again after it was
	// interrupted, and on a file it already changed.
	Run func(db *bolt.DB) error
}

// steps are the changes to the on-disk format, oldest first.
var steps = []Step{
	{Version: 1, Name: "key index entries normalised to the document identifier's letter case (DOC-017)", Run: normalizeByKey},
}

// Latest is the schema version this build writes.
func Latest() uint64 { return steps[len(steps)-1].Version }

// Version is the schema version recorded in db; 0 when none is.
func Version(db *bolt.DB) (uint64, error) {
	var v uint64
	err := db.View(func(tx *bolt.Tx) error {
		var err error
		v, err = readVersion(tx)
		return err
	})
	return v, err
}

func readVersion(tx *bolt.Tx) (uint64, error) {
	b := tx.Bucket(Bucket)
	if b == nil {
		return 0, nil
	}
	raw := b.Get(versionKey)
	if raw == nil {
		return 0, nil
	}
	if len(raw) != 8 {
		return 0, fmt.Errorf("schema version is %d bytes, want 8", len(raw))
	}
	return binary.BigEndian.Uint64(raw), nil
}

// setVersion records v as db's schema version.
func setVersion(db *bolt.DB, v uint64) error {
	return db.Update(func(tx *bolt.Tx) error {
		b, err := tx.CreateBucketIfNotExists(Bucket)
		if err != nil {
			return err
		}
		return b.Put(versionKey, binary.BigEndian.AppendUint64(nil, v))
	})
}

// Run brings db to the latest schema version.
func Run(db *bolt.DB) error { return run(db, steps) }

func run(db *bolt.DB, steps []Step) error {
	current, err := Version(db)
	if err != nil {
		return fmt.Errorf("reading the schema version of %s: %w", filepath.Base(db.Path()), err)
	}
	if latest := steps[len(steps)-1].Version; current > latest {
		// Written by a newer build. Its changes are not undone here, and
		// nothing is rewritten: the file is left as that build wrote it.
		slog.Warn("the database was written by a newer release; its schema is not changed",
			"file", db.Path(), "schemaVersion", current, "thisBuildWrites", latest)
		return nil
	}
	for _, step := range steps {
		if step.Version <= current {
			continue
		}
		start := time.Now()
		if err := step.Run(db); err != nil {
			return fmt.Errorf("schema step %d (%s) on %s: %w", step.Version, step.Name, filepath.Base(db.Path()), err)
		}
		if err := setVersion(db, step.Version); err != nil {
			return fmt.Errorf("recording schema version %d on %s: %w", step.Version, filepath.Base(db.Path()), err)
		}
		slog.Info("storage schema migrated", "file", db.Path(), "schemaVersion", step.Version,
			"step", step.Name, "elapsed", time.Since(start).Round(time.Millisecond))
	}
	return nil
}
