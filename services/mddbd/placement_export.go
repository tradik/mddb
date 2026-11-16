package main

import (
	"context"
	"time"

	"mddb/internal/binlog"
	"mddb/internal/boltx"
	proto "mddb/proto"

	bolt "go.etcd.io/bbolt"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Exporting a collection or the global settings (GO-052).
//
// The stream begins with a copy of every key — read in short transactions,
// so writers are not stalled behind it — and goes on, for a collection, with
// every log entry that changes it, as it is written. The copy is taken
// after the log's stable LSN is noted, so it holds at least every write
// through that LSN; the entries after it are sent again. Applying them over
// a copy that already holds some of them leaves each key where the last of
// them put it, which is where the source has it.

// exportChunkSize is how many entries one message of the copy carries.
const exportChunkSize = 256

// exportProgressEvery is how often the stream says how far it has read the
// log while no entry of the collection comes.
const exportProgressEvery = 50 * time.Millisecond

// ExportKeys streams a collection's keys and then its changes, or the
// global settings, to the node copying them.
func (rs *ReplicationServer) ExportKeys(req *proto.ExportKeysRequest, stream proto.MDDBReplication_ExportKeysServer) error {
	p, err := rs.placementFor(stream.Context())
	if err != nil {
		return err
	}
	if err := p.checkExport(req); err != nil {
		return err
	}
	bl := p.s.Binlog
	start := bl.StableLSN()
	if err := p.copyKeys(req, chunkSender{stream}); err != nil {
		return err
	}
	if err := stream.Send(&proto.ExportKeysChunk{SnapshotDone: true, StartLsn: start, ThroughLsn: start}); err != nil {
		return err
	}
	if req.Global {
		return nil // a copy of the settings; the copier asks again
	}
	return p.tailCollection(stream, req.Collection, start)
}

// checkExport admits an export: of a collection from the leader of the
// group holding it, of the settings from the meta group's leader.
func (p *placementRuntime) checkExport(req *proto.ExportKeysRequest) error {
	if req.Global {
		if !p.leadsMeta() {
			return status.Error(codes.FailedPrecondition, "this node does not lead the meta group")
		}
		return nil
	}
	if req.Collection == "" {
		return status.Error(codes.InvalidArgument, "collection is required")
	}
	if !p.leads() {
		return status.Error(codes.FailedPrecondition, "this node does not lead its group")
	}
	if g, _ := p.owner(req.Collection); g != p.cfg.self {
		return status.Error(codes.FailedPrecondition, "collection "+req.Collection+" is not held by this group")
	}
	return nil
}

// chunkSender batches copied keys into messages.
type chunkSender struct {
	stream interface {
		Send(*proto.ExportKeysChunk) error
	}
}

// copyKeys sends every key of what req names, in chunks.
func (p *placementRuntime) copyKeys(req *proto.ExportKeysRequest, out chunkSender) error {
	var batch []*proto.BinlogEntryProto
	emit := func(bucket string, k, v []byte) error {
		batch = append(batch, &proto.BinlogEntryProto{Type: uint32(binlog.BinlogPut), BucketName: bucket,
			Key: append([]byte(nil), k...), Value: append([]byte(nil), v...)})
		if len(batch) < exportChunkSize {
			return nil
		}
		err := out.stream.Send(&proto.ExportKeysChunk{Entries: batch})
		batch = nil
		return err
	}
	var err error
	if req.Global {
		err = scanBuckets(p.s.DB, globalBuckets, keepAll, emit)
	} else {
		err = p.copyCollection(req.Collection, emit)
	}
	if err != nil || len(batch) == 0 {
		return err
	}
	return out.stream.Send(&proto.ExportKeysChunk{Entries: batch})
}

// copyCollection sends every key of c's file and c's settings.
func (p *placementRuntime) copyCollection(c string, emit func(string, []byte, []byte) error) error {
	if cf := p.s.collectionStore(); cf != nil {
		if db := cf.get(c); db != nil {
			if err := scanBuckets(db, bucketNames(db), keepAll, emit); err != nil {
				return err
			}
		}
	}
	return scanBuckets(p.s.DB, settingsBucketNames(), ownedBy(c), emit)
}

// bucketNames is every top-level bucket of db.
func bucketNames(db *bolt.DB) []string {
	var out []string
	_ = db.View(func(tx *bolt.Tx) error {
		return tx.ForEach(func(name []byte, _ *bolt.Bucket) error {
			out = append(out, string(name))
			return nil
		})
	})
	return out
}

// scanBuckets emits each key of buckets in db that keep admits.
func scanBuckets(db *bolt.DB, buckets []string, keep func(string, []byte, []byte) bool,
	emit func(string, []byte, []byte) error) error {
	for _, b := range buckets {
		var emitErr error
		err := boltx.Scan(db, []byte(b), nil, func(k, v []byte) bool {
			if v == nil || !keep(b, k, v) {
				return true // a nested bucket, or another collection's key
			}
			emitErr = emit(b, k, v)
			return emitErr == nil
		})
		if err == nil {
			err = emitErr
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// tailCollection streams the log entries after start that change c, until
// the copier ends the stream.
func (p *placementRuntime) tailCollection(stream proto.MDDBReplication_ExportKeysServer, c string, start uint64) error {
	bl := p.s.Binlog
	sink := &exportSink{stream: stream, collection: c, log: bl}
	subID := "placement-export:" + c + ":" + time.Now().Format(time.RFC3339Nano)
	ch := bl.Subscribe(subID)
	defer bl.Unsubscribe(subID)
	tail := &binlogTail{log: bl, stream: sink, sent: start}
	if err := tail.catchUp(); err != nil {
		return err
	}
	return tail.follow(ch)
}

// exportSink passes on the entries of one collection and, now and then,
// how far the log has been read.
type exportSink struct {
	stream     proto.MDDBReplication_ExportKeysServer
	collection string
	log        *binlog.Binlog
	reported   time.Time
}

func (e *exportSink) Context() context.Context { return e.stream.Context() }

// Send forwards a log entry of the collection, and reports progress for the
// others: at most every exportProgressEvery, and always for the log's last
// entry, so a copier waiting for an LSN sees it reached.
func (e *exportSink) Send(entry *proto.BinlogEntryProto) error {
	if entryCollection(protoToEntry(entry)) == e.collection || deletesCollectionBucket(entry) {
		e.reported = time.Now()
		return e.stream.Send(&proto.ExportKeysChunk{Entries: []*proto.BinlogEntryProto{entry}, ThroughLsn: entry.Lsn})
	}
	if time.Since(e.reported) < exportProgressEvery && entry.Lsn < e.log.CurrentLSN() {
		return nil
	}
	e.reported = time.Now()
	return e.stream.Send(&proto.ExportKeysChunk{ThroughLsn: entry.Lsn})
}

// deletesCollectionBucket reports an entry removing a whole collection
// bucket: it touches every collection.
func deletesCollectionBucket(e *proto.BinlogEntryProto) bool {
	return binlog.BinlogEntryType(e.Type) == binlog.BinlogDeleteBucket && isCollectionBucket([]byte(e.BucketName))
}
