package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"

	"mddb/internal/binlog"
	proto "mddb/proto"

	bolt "go.etcd.io/bbolt"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Importing a collection on the target group's leader (GO-052).
//
// The meta leader, running a move, asks the target's leader to import the
// collection and reads its progress. The target pulls the source's export
// (placement_export.go) and applies every key through its own log, so its
// followers receive the collection as it arrives. It goes on applying the
// source's new writes until the meta leader ends the call — once the map
// names the target, or when the move is given up.

// errBucketDeleted stops an import the source answered with a whole bucket
// removed: that is no change to one collection that can be copied.
var errBucketDeleted = errors.New("the source removed a whole collection bucket; the move is abandoned")

// ImportCollection copies a collection from its group to this one.
func (rs *ReplicationServer) ImportCollection(req *proto.ImportCollectionRequest, stream proto.MDDBReplication_ImportCollectionServer) error {
	p, err := rs.placementFor(stream.Context())
	if err != nil {
		return err
	}
	if err := p.checkImport(req.Collection); err != nil {
		return status.Error(codes.FailedPrecondition, err.Error())
	}
	return p.importCollection(stream.Context(), req, stream.Send)
}

// checkImport admits an import: this node leads the group a move is copying
// the collection to.
func (p *placementRuntime) checkImport(c string) error {
	if !p.leads() {
		return errors.New("this node does not lead its group")
	}
	t := p.current()
	if t == nil || t.Entries[c].MovingTo != p.cfg.self {
		return fmt.Errorf("no move of %q to this group is in the map", c)
	}
	return nil
}

// importCollection pulls c from its source group and reports progress with
// report until ctx ends.
func (p *placementRuntime) importCollection(ctx context.Context, req *proto.ImportCollectionRequest,
	report func(*proto.ImportProgress) error) error {
	c := req.Collection
	p.local.mark(p.local.importing, c, true)
	defer p.local.mark(p.local.importing, c, false)
	// A copy left by a move given up earlier would keep keys the source
	// has since removed.
	if err := p.dropCopy(c); err != nil {
		return err
	}
	src, err := p.leaderClient(req.SourceGroup)
	if err != nil {
		return err
	}
	ex, err := src.ExportKeys(withReplicationSecret(ctx), &proto.ExportKeysRequest{Collection: c})
	if err != nil {
		return err
	}
	var prog proto.ImportProgress
	for {
		chunk, err := ex.Recv()
		if err != nil {
			return importEnd(ctx, err, p, c)
		}
		if err := p.applyCopied(c, chunk.Entries); err != nil {
			return err
		}
		if !prog.SnapshotDone {
			prog.KeysCopied += uint64(len(chunk.Entries))
		}
		prog.SnapshotDone = prog.SnapshotDone || chunk.SnapshotDone
		prog.AppliedLsn = max(prog.AppliedLsn, chunk.ThroughLsn)
		prog.SourceLsn = max(prog.SourceLsn, chunk.StartLsn, chunk.ThroughLsn)
		if err := report(&prog); err != nil {
			return err
		}
	}
}

// importEnd is how an import ends: the caller ending it is its normal end,
// after which the in-memory indexes that are rebuilt rather than updated
// learn the collection.
func importEnd(ctx context.Context, err error, p *placementRuntime, c string) error {
	if ctx.Err() == nil && !errors.Is(err, io.EOF) {
		return err
	}
	p.rebuildGeo(c)
	return nil
}

// rebuildGeo loads c's geo points into the in-memory geo indexes.
func (p *placementRuntime) rebuildGeo(c string) {
	s := p.s
	if s.GeoStore == nil || s.GeoIndex == nil || s.GeoHashIndex == nil {
		return
	}
	s.GeoIndex.Clear(c)
	s.GeoHashIndex.Clear(c)
	if _, err := s.GeoStore.Rebuild(s.GeoIndex, c); err != nil {
		slog.Warn("placement: rebuilding a collection's geo index failed", "collection", c, "err", err)
	}
	_, _ = s.GeoStore.RebuildHash(s.GeoHashIndex, c)
}

// applyCopied applies entries copied from another group through this
// node's log: collection keys to c's file, settings to the main file.
func (p *placementRuntime) applyCopied(c string, in []*proto.BinlogEntryProto) error {
	var file, main []*binlog.BinlogEntry
	for _, pe := range in {
		e := protoToEntry(pe)
		switch {
		case e.Type == binlog.BinlogDeleteBucket:
			return errBucketDeleted
		case isCollectionBucket([]byte(e.BucketName)):
			file = append(file, e)
		default:
			main = append(main, e)
		}
	}
	if len(file) > 0 {
		db, err := p.s.collectionStore().getOrCreate(c)
		if err != nil {
			return err
		}
		if err := logEntries(db.Update, p.s.Binlog, file); err != nil {
			return err
		}
	}
	if err := logEntries(p.s.DBUpdate, p.s.Binlog, main); err != nil {
		return err
	}
	ra := NewReplicationApplier(p.s)
	for _, e := range in {
		ra.updateInMemoryState(protoToEntry(e))
	}
	return nil
}

// logEntries applies entries in one transaction and logs them as this
// node's own writes.
func logEntries(update func(func(*bolt.Tx) error) error, bl *binlog.Binlog, entries []*binlog.BinlogEntry) error {
	if len(entries) == 0 {
		return nil
	}
	return binlog.Logged(update, bl, func(tx *bolt.Tx, bo *binlog.BinlogOps) error {
		for _, e := range entries {
			if err := applyEntry(tx, e); err != nil {
				return err
			}
			if e.Type == binlog.BinlogDelete {
				bo.Delete(e.BucketName, e.Key)
			} else {
				bo.Put(e.BucketName, e.Key, e.Value)
			}
		}
		return nil
	})
}
