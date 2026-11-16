package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	proto "mddb/proto"
	"os"
	"path/filepath"
	"time"
)

// A follower's side of a snapshot: receive the leader's files next to the
// database, swap them in, and continue from the snapshot's position.

// errSnapshotIncomplete is a snapshot stream that ended without the chunk
// that completes it.
var errSnapshotIncomplete = errors.New("the snapshot stream ended before the snapshot was complete")

// errSnapshotFromOldLeader is a per-collection follower asking a leader that
// only knows one-file snapshots.
var errSnapshotFromOldLeader = errors.New("the leader runs a release before 2.19.8, whose snapshots are one file: " +
	"a follower with MDDB_STORAGE_LAYOUT=per-collection needs a leader on 2.19.8 or later in the same layout")

// chunkReceiver is the stream a snapshot arrives on.
type chunkReceiver interface {
	Recv() (*proto.SnapshotChunk, error)
}

// requestSnapshot downloads a full database snapshot from the leader and
// makes it this node's database.
func (rc *ReplicationClient) requestSnapshot(ctx context.Context) error {
	stream, err := rc.client.RequestSnapshot(withReplicationSecret(ctx), &proto.SnapshotRequest{
		FollowerId:      rc.followerID,
		CurrentLsn:      rc.applier.LastAppliedLSN(),
		Layout:          storageLayoutName(),
		ProtocolVersion: replicationProtocol,
	})
	if err != nil {
		return fmt.Errorf("failed to request snapshot: %w", err)
	}

	tmpPath := rc.server.Path + ".snapshot.tmp"
	defer removeSnapshotFiles(tmpPath)
	pos, err := receiveSnapshot(stream, tmpPath)
	if err != nil {
		return err
	}
	slog.Info("replication snapshot received", "snapshotLSN", pos.LSN)

	if err := rc.installSnapshot(tmpPath); err != nil {
		return fmt.Errorf("failed to replace database: %w", err)
	}
	return rc.adoptSnapshot(pos)
}

// removeSnapshotFiles deletes what a snapshot left next to the database.
func removeSnapshotFiles(tmpPath string) {
	_ = os.Remove(tmpPath)
	_ = os.RemoveAll(collectionDir(tmpPath))
}

// receiveSnapshot writes the stream's files — the main file at tmpPath, the
// collection files in the directory beside it, as a backup lays them out —
// and returns the position the snapshot was taken at.
func receiveSnapshot(stream chunkReceiver, tmpPath string) (replPosition, error) {
	w := &snapshotWriter{mainPath: tmpPath}
	defer w.close()
	if perCollectionLayout() {
		// Present even when the leader has no collection yet: the swap
		// tells "no collections" from "not a per-collection backup" by it.
		if err := os.MkdirAll(collectionDir(tmpPath), 0o700); err != nil {
			return replPosition{}, fmt.Errorf("failed to create the snapshot directory: %w", err)
		}
	}

	var pos replPosition
	complete := false
	for !complete {
		chunk, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			return pos, fmt.Errorf("failed to receive snapshot chunk: %w", err)
		}
		if err := w.write(chunk); err != nil {
			return pos, fmt.Errorf("failed to write snapshot chunk: %w", err)
		}
		pos = replPosition{LSN: chunk.SnapshotLsn, LogID: chunk.LogId}
		complete = chunk.IsLast
	}
	return pos, snapshotVerdict(pos, complete)
}

// snapshotVerdict says whether what was received can be installed. A leader
// from before 2.19.8 sends one file, no log ID and no closing chunk; the end
// of its stream is the end of its snapshot.
func snapshotVerdict(pos replPosition, complete bool) error {
	fromOldLeader := pos.LogID == 0
	switch {
	case fromOldLeader && perCollectionLayout():
		return errSnapshotFromOldLeader
	case !fromOldLeader && !complete:
		return errSnapshotIncomplete
	}
	return nil
}

// snapshotWriter puts each chunk in the file it names.
type snapshotWriter struct {
	mainPath string
	cur      *os.File
	curName  string
}

func (w *snapshotWriter) write(chunk *proto.SnapshotChunk) error {
	if len(chunk.Data) == 0 {
		return nil
	}
	if w.cur == nil || chunk.File != w.curName {
		if err := w.open(chunk.File); err != nil {
			return err
		}
	}
	_, err := w.cur.Write(chunk.Data)
	return err
}

// open starts the file a chunk names. The name comes off the network, so it
// is only ever the main file or a name a collection file can have — never a
// path.
func (w *snapshotWriter) open(name string) error {
	w.close()
	path := w.mainPath
	if name != "" {
		if _, valid := collectionFromFileName(name); !valid || !perCollectionLayout() {
			return fmt.Errorf("the snapshot names a file this node cannot take: %q", name)
		}
		path = filepath.Join(collectionDir(w.mainPath), name)
	}
	// #nosec G304 -- next to the database; the name is validated above
	f, err := os.Create(filepath.Clean(path))
	if err != nil {
		return err
	}
	w.cur, w.curName = f, name
	return nil
}

func (w *snapshotWriter) close() {
	if w.cur != nil {
		_ = w.cur.Close()
		w.cur = nil
	}
}

// installSnapshot replaces the database, and in the per-collection layout
// every collection file, with the received ones, and rebuilds in-memory state
// atomically with respect to live readers (GO-004). The restore write lock
// drains in-flight DBView/DBUpdate calls and blocks new ones, so no handler
// observes a closed or half-swapped *bolt.DB. It is a restore whose backup is
// the snapshot, and goes the same way: the collection files first, put back
// if the main file then fails to swap.
func (rc *ReplicationClient) installSnapshot(tmpPath string) error {
	s := rc.server
	return s.withRestoreLock(func() error {
		undo, err := s.swapCollectionFiles(collectionDir(tmpPath))
		if err != nil {
			return err
		}
		if err := rc.replaceDatabase(tmpPath); err != nil {
			undo()
			return err
		}
		_ = os.RemoveAll(collectionDir(s.Path) + ".pre-restore")
		return nil
	})
}

// replaceDatabase swaps the live database for a received snapshot.
//
// SEC-018: this used to be a third hand-rolled close-rename-reopen. It did not
// check that the snapshot was a database, so a truncated stream was
// indistinguishable from a good one until the reopen — by which point the live
// database had already been renamed over. Any failure left the follower holding
// a closed handle with no way back, and it reopened with hand-written bolt
// options rather than the tuned ones, so a follower that had ever taken a
// snapshot ran with a different mmap and lock configuration than everything
// else in the process.
//
// The snapshot is renamed rather than copied: it is a temp file this client
// created and is about to delete, and copying a multi-gigabyte file to throw
// the original away is waste.
func (rc *ReplicationClient) replaceDatabase(snapshotPath string) error {
	return rc.server.swapDatabase(snapshotPath, func(dst string) error {
		return os.Rename(snapshotPath, dst)
	})
}

// adoptSnapshot makes the snapshot's position this follower's: written to
// the database, so a restart resumes from it, and to the follower's own log,
// which continues the leader's from there.
func (rc *ReplicationClient) adoptSnapshot(pos replPosition) error {
	if err := rc.server.saveReplicationPosition(pos); err != nil {
		return fmt.Errorf("failed to record the snapshot position: %w", err)
	}
	rc.applier.SetPosition(pos)
	rc.rebaseLog(pos)
	rc.lastAppliedAt.Store(time.Now().Unix())
	rc.needSnapshot.Store(false)
	rc.setError(nil)
	slog.Info("replication snapshot applied", "snapshotLSN", pos.LSN)
	return nil
}
