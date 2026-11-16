package main

import (
	"context"
	"fmt"
	"time"

	"mddb/internal/binlog"
	proto "mddb/proto"
)

// Applying the stream in batches (GO-050 follow-up, found by TEST-005).
//
// The follower applied every entry in a bbolt transaction of its own, and
// each commit is a sync to disk. A document write is several entries (the
// document, its key index, its revision, its metadata index), so on a real
// disk the follower managed about 20 documents a second while the leader
// took 200: under a steady 20 writes a second it fell further behind for as
// long as the load lasted, and a follower that had been away never caught
// up. On tmpfs, where a sync is free, the same follower kept up.
//
// Entries are now received on their own goroutine and applied as they
// come: whatever has arrived while the last batch was being applied — up to
// applyBatchMax entries — goes into the next one, as one transaction per
// database file, with the position stamped in that same transaction. An idle
// stream still applies each entry at once; a busy one syncs once per batch
// instead of once per entry.

// applyBatchMax bounds a batch: the transaction, and what is re-applied
// after a crash in the middle of one.
const applyBatchMax = 1024

// received is one result of the stream: an entry, or the error that ended it.
type received struct {
	entry *binlog.BinlogEntry
	err   error
}

// entryReceiver is the binlog stream as the follower reads it.
type entryReceiver interface {
	Recv() (*proto.BinlogEntryProto, error)
}

// receiveEntries passes the stream's entries to out until it ends or ctx is
// done; the error that ended it is the last thing sent.
func receiveEntries(ctx context.Context, stream entryReceiver, out chan<- received) {
	for {
		e, err := stream.Recv()
		r := received{err: err}
		if err == nil {
			r.entry = protoToEntry(e)
		}
		select {
		case out <- r:
		case <-ctx.Done():
			return
		}
		if err != nil {
			return
		}
	}
}

// applyStream applies what receiveEntries passes on, a batch at a time,
// until the stream ends or an entry cannot be applied.
func (rc *ReplicationClient) applyStream(ctx context.Context, in <-chan received) error {
	for {
		var first received
		select {
		case first = <-in:
		case <-ctx.Done():
			return rc.streamEnded(ctx.Err())
		}
		if first.err != nil {
			return rc.streamEnded(first.err)
		}
		batch, ended := drainBatch(in, first.entry)
		if err := rc.applyEntries(batch); err != nil {
			return err
		}
		if ended != nil {
			return rc.streamEnded(ended)
		}
	}
}

// drainBatch adds to first whatever has already arrived, without waiting,
// and returns the error that ended the stream if it came among them.
func drainBatch(in <-chan received, first *binlog.BinlogEntry) ([]*binlog.BinlogEntry, error) {
	batch := []*binlog.BinlogEntry{first}
	for len(batch) < applyBatchMax {
		select {
		case r := <-in:
			if r.err != nil {
				return batch, r.err
			}
			batch = append(batch, r.entry)
		default:
			return batch, nil
		}
	}
	return batch, nil
}

// applyEntries applies a batch and adds it to the follower's log. A batch
// that does not continue from the last entry applied, or that cannot be
// applied, ends replication from the log: the follower's data is no longer
// known to be the leader's, and only a snapshot makes it so again.
func (rc *ReplicationClient) applyEntries(batch []*binlog.BinlogEntry) error {
	err := checkSequence(rc.applier.LastAppliedLSN(), batch)
	if err == nil {
		err = rc.applier.ApplyBatch(batch)
	}
	if err != nil {
		rc.needSnapshot.Store(true)
		return fmt.Errorf("replication stopped, resynchronising from a snapshot: %w", err)
	}
	rc.appendLog(batch)
	rc.connected.Store(true)
	rc.lastAppliedAt.Store(time.Now().Unix())
	return nil
}

// applyEntry applies one entry; see applyEntries.
func (rc *ReplicationClient) applyEntry(entry *binlog.BinlogEntry) error {
	return rc.applyEntries([]*binlog.BinlogEntry{entry})
}

// checkSequence says whether batch continues from last, entry by entry.
func checkSequence(last uint64, batch []*binlog.BinlogEntry) error {
	for i, e := range batch {
		if want := last + uint64(i) + 1; e.LSN != want { // #nosec G115 -- i is a slice index
			return fmt.Errorf("the leader sent LSN %d where %d was due", e.LSN, want)
		}
	}
	return nil
}
