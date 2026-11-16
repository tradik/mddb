package main

import (
	"bufio"
	"fmt"
	"log/slog"
	"maps"
	proto "mddb/proto"
	"slices"

	bolt "go.etcd.io/bbolt"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// A leader's side of a snapshot: every database file, as it is, in one
// stream.
//
// GO-050: a snapshot was one file, so a leader in the per-collection layout
// refused them and its followers could only be seeded by hand. Each chunk now
// names the file it belongs to — "" for the main file, a collection file's
// name otherwise — and the stream carries the main file and then every
// collection file.
//
// The files are read one after another, each in its own read transaction,
// while writes go on. That is sound because the snapshot's LSN is taken
// first: a commit reaches the database before its entry reaches the binlog,
// so every file read afterwards holds at least everything through that LSN,
// and the follower replays the log from it. Entries a file already contains
// are applied again and leave it as the leader has it.

const snapshotChunkSize = 1024 * 1024 // 1MB

// Storage layouts as the snapshot request names them.
const (
	layoutSingle        = "single"
	layoutPerCollection = "per-collection"
)

// storageLayoutName is this node's layout.
func storageLayoutName() string {
	if perCollectionLayout() {
		return layoutPerCollection
	}
	return layoutSingle
}

// errSnapshotLayout refuses a snapshot between nodes whose layouts differ:
// the files of one are not the files of the other.
func errSnapshotLayout(leader, follower string) error {
	return fmt.Errorf("the leader stores data in the %s layout and the follower in the %s layout; "+
		"a snapshot is the leader's files, so both need the same MDDB_STORAGE_LAYOUT "+
		"(mddbd -split-collections / -merge-collections converts a stopped node)", leader, follower)
}

// RequestSnapshot streams a full snapshot of the database to a follower
func (rs *ReplicationServer) RequestSnapshot(req *proto.SnapshotRequest, stream proto.MDDBReplication_RequestSnapshotServer) error {
	if err := rs.authorizeReplication(stream.Context()); err != nil {
		return err
	}
	if rs.server.Binlog == nil {
		return status.Error(codes.FailedPrecondition, "binlog not enabled on this node")
	}
	if req.FollowerId == "" {
		return status.Error(codes.InvalidArgument, "follower_id is required")
	}
	// A follower before 2.19.8 names no layout and has one file.
	follower := req.Layout
	if follower == "" {
		follower = layoutSingle
	}
	if leader := storageLayoutName(); leader != follower {
		return status.Error(codes.FailedPrecondition, errSnapshotLayout(leader, follower).Error())
	}

	slog.Info("Replication follower requested snapshot", "followerID", req.FollowerId)

	// The position first, the files after (see above).
	out := &snapshotSender{stream: stream, logID: rs.server.Binlog.LogID(), lsn: rs.server.Binlog.CurrentLSN()}
	if err := rs.sendSnapshotFiles(out); err != nil {
		slog.Warn("Replication snapshot failed", "followerID", req.FollowerId, "err", err)
		return status.Error(codes.Internal, err.Error())
	}

	slog.Info("replication snapshot completed", "followerID", req.FollowerId, "snapshotLSN", out.lsn, "files", out.files)
	return nil
}

// sendSnapshotFiles sends the main file, every collection file, and the
// chunk that ends the snapshot.
func (rs *ReplicationServer) sendSnapshotFiles(out *snapshotSender) error {
	s := rs.server
	s.restoreMu.RLock()
	defer s.restoreMu.RUnlock()

	if err := out.sendFile("", s.DB); err != nil {
		return err
	}
	if cf := s.collectionStore(); cf != nil {
		// The files open now, by name. A collection deleted while the
		// snapshot runs closes its file, the read fails, and the follower
		// asks again.
		files := cf.all()
		for _, c := range slices.Sorted(maps.Keys(files)) {
			if err := out.sendFile(collectionFileName(c), files[c]); err != nil {
				return err
			}
		}
	}
	return out.finish()
}

// snapshotSender writes database files to a snapshot stream as chunks.
type snapshotSender struct {
	stream proto.MDDBReplication_RequestSnapshotServer
	lsn    uint64
	logID  uint64
	files  int

	// The file being sent.
	name   string
	size   uint64
	offset uint64
}

// sendFile streams one database file from a read transaction.
func (o *snapshotSender) sendFile(name string, db *bolt.DB) error {
	return db.View(func(tx *bolt.Tx) error {
		o.name, o.offset = name, 0
		o.size = uint64(tx.Size()) // #nosec G115 -- db size always non-negative
		w := bufio.NewWriterSize(o, snapshotChunkSize)
		_, err := tx.WriteTo(w)
		if err == nil {
			err = w.Flush()
		}
		if err != nil {
			return fmt.Errorf("failed to send %q: %w", name, err)
		}
		o.files++
		return nil
	})
}

// Write sends p as one chunk of the current file.
func (o *snapshotSender) Write(p []byte) (int, error) {
	err := o.stream.Send(&proto.SnapshotChunk{
		Data:        p,
		Offset:      o.offset,
		TotalSize:   o.size,
		SnapshotLsn: o.lsn,
		LogId:       o.logID,
		File:        o.name,
	})
	if err != nil {
		return 0, fmt.Errorf("failed to send snapshot chunk: %w", err)
	}
	o.offset += uint64(len(p))
	return len(p), nil
}

// finish sends the chunk that says the snapshot is complete. A follower that
// reaches the end of the stream without it has part of a snapshot.
func (o *snapshotSender) finish() error {
	return o.stream.Send(&proto.SnapshotChunk{IsLast: true, SnapshotLsn: o.lsn, LogId: o.logID})
}
