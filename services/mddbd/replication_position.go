package main

import (
	"encoding/binary"

	bolt "go.etcd.io/bbolt"
)

// Where a follower is in its leader's log, kept in the database (GO-050).
//
// The position used to live in memory, so a restarted follower asked for the
// log from the start. It is now written by the transaction that applies the
// entry: the change and "applied through LSN n" reach the disk together or
// not at all, and a restart resumes after the last entry that is really in
// the file — no entry applied twice into a gap, none skipped.
//
// The record is in the "replmeta" bucket of the file the entry went to. In
// the per-collection layout that is the collection's file, so every file
// carries the position of its own last entry; entries are applied one after
// another, which makes the highest of them the follower's position.
//
// The bucket is the node's own: it is not replicated and not in the binlog.
// A leader removes it when it starts, so no snapshot hands a follower a
// position from the leader's own time as a follower.

var (
	bucketReplMeta = []byte("replmeta")
	keyReplApplied = []byte("applied")
)

// replPosition is a place in a leader's log.
type replPosition struct {
	LSN   uint64
	LogID uint64 // the log LSN is a position in; 0 when the leader has no IDs
}

// stampApplied records in tx that the file holds everything through pos.
func stampApplied(tx *bolt.Tx, pos replPosition) error {
	b, err := tx.CreateBucketIfNotExists(bucketReplMeta)
	if err != nil {
		return err
	}
	var v [16]byte
	binary.BigEndian.PutUint64(v[:8], pos.LSN)
	binary.BigEndian.PutUint64(v[8:], pos.LogID)
	return b.Put(keyReplApplied, v[:])
}

// readApplied is the position recorded in tx's file, zero when there is none.
func readApplied(tx *bolt.Tx) replPosition {
	b := tx.Bucket(bucketReplMeta)
	if b == nil {
		return replPosition{}
	}
	v := b.Get(keyReplApplied)
	if len(v) != 16 {
		return replPosition{}
	}
	return replPosition{LSN: binary.BigEndian.Uint64(v[:8]), LogID: binary.BigEndian.Uint64(v[8:])}
}

// loadReplicationPosition is the follower's position: the highest one
// recorded in the main file or any collection file.
func (s *Server) loadReplicationPosition() (replPosition, error) {
	var pos replPosition
	read := func(tx *bolt.Tx) error {
		if p := readApplied(tx); p.LSN >= pos.LSN {
			pos = p
		}
		return nil
	}
	if err := s.DBView(read); err != nil {
		return replPosition{}, err
	}
	err := s.DBViewAll(read)
	return pos, err
}

// saveReplicationPosition records pos in the main file, outside any entry's
// transaction: after a snapshot, whose files are the leader's as of pos.
func (s *Server) saveReplicationPosition(pos replPosition) error {
	return s.DBUpdate(func(tx *bolt.Tx) error { return stampApplied(tx, pos) })
}

// clearReplicationPosition removes the record from every file. A leader does
// it at start: its files are where the log begins, not a position in one.
func (s *Server) clearReplicationPosition() error {
	s.restoreMu.RLock()
	defer s.restoreMu.RUnlock()
	for _, db := range append([]*bolt.DB{s.DB}, s.collectionDBs()...) {
		if err := dropReplMeta(db); err != nil {
			return err
		}
	}
	return nil
}

// dropReplMeta removes the bucket from db, without a write when it has none.
func dropReplMeta(db *bolt.DB) error {
	present := false
	if err := db.View(func(tx *bolt.Tx) error {
		present = tx.Bucket(bucketReplMeta) != nil
		return nil
	}); err != nil || !present {
		return err
	}
	return db.Update(func(tx *bolt.Tx) error { return tx.DeleteBucket(bucketReplMeta) })
}
