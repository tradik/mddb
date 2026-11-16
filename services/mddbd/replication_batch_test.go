package main

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"mddb/internal/binlog"
	proto "mddb/proto"

	bolt "go.etcd.io/bbolt"
)

// Batched applying (GO-050 follow-up): what arrives together goes in one
// transaction per file, and nothing about the stream's end is lost.

// sliceReceiver hands out entries, then err.
type sliceReceiver struct {
	entries []*proto.BinlogEntryProto
	err     error
}

func (r *sliceReceiver) Recv() (*proto.BinlogEntryProto, error) {
	if len(r.entries) == 0 {
		return nil, r.err
	}
	e := r.entries[0]
	r.entries = r.entries[1:]
	return e, nil
}

func protoEntries(from, to uint64) []*proto.BinlogEntryProto {
	var out []*proto.BinlogEntryProto
	for lsn := from; lsn <= to; lsn++ {
		out = append(out, &proto.BinlogEntryProto{Lsn: lsn, Type: uint32(binlog.BinlogPut), BucketName: "custom", Key: []byte{byte(lsn)}, Value: []byte("v")}) // #nosec G115 -- a test key byte; wrapping is fine
	}
	return out
}

func TestAStreamIsAppliedInBatches(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	rc := NewReplicationClient(s, ReplicationClientConfig{})
	rc.running.Store(true)
	in := make(chan received, applyBatchMax+10)
	ctx := context.Background()
	receiveEntries(ctx, &sliceReceiver{entries: protoEntries(1, applyBatchMax+5), err: io.EOF}, in)
	// What has arrived is taken in one batch, up to the limit.
	peek := make(chan received, applyBatchMax+10)
	for range applyBatchMax + 5 {
		peek <- received{entry: &binlog.BinlogEntry{}}
	}
	if batch, _ := drainBatch(peek, &binlog.BinlogEntry{}); len(batch) != applyBatchMax {
		t.Errorf("a batch of %d", len(batch))
	}
	if err := rc.applyStream(ctx, in); err != nil {
		t.Fatalf("a stream the leader closed: %v", err)
	}
	if got := rc.applier.LastAppliedLSN(); got != applyBatchMax+5 {
		t.Fatalf("applied through %d", got)
	}
}

func TestTheEndOfAStreamArrivesAfterItsEntries(t *testing.T) {
	first := &binlog.BinlogEntry{LSN: 1}
	in := make(chan received, 3)
	in <- received{entry: &binlog.BinlogEntry{LSN: 2}}
	in <- received{err: io.ErrUnexpectedEOF}
	batch, ended := drainBatch(in, first)
	if len(batch) != 2 || !errors.Is(ended, io.ErrUnexpectedEOF) {
		t.Errorf("batch %d, end %v", len(batch), ended)
	}

	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	rc := NewReplicationClient(s, ReplicationClientConfig{})
	rc.running.Store(true)
	ch := make(chan received, 2)
	ch <- received{entry: protoToEntry(protoEntries(1, 1)[0])}
	ch <- received{err: errUnit}
	if err := rc.applyStream(context.Background(), ch); err == nil || rc.applier.LastAppliedLSN() != 1 {
		t.Errorf("an error after an entry: %v, applied %d", err, rc.applier.LastAppliedLSN())
	}
	bad := make(chan received, 1)
	bad <- received{entry: &binlog.BinlogEntry{LSN: 9}}
	if err := rc.applyStream(context.Background(), bad); err == nil || !rc.needSnapshot.Load() {
		t.Errorf("an entry out of sequence: %v", err)
	}
}

func TestStoppingWhileTheStreamWaits(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	rc := NewReplicationClient(s, ReplicationClientConfig{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := rc.applyStream(ctx, make(chan received)); err != nil {
		t.Errorf("a stopped client: %v", err)
	}
	// The receiver does not block on a reader that is gone.
	done := make(chan struct{})
	go func() {
		receiveEntries(ctx, &sliceReceiver{entries: protoEntries(1, 2)}, make(chan received))
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the receiver blocked after its reader stopped")
	}
}

func TestABatchIsOneTransactionPerFile(t *testing.T) {
	s := perCollectionServer(t)
	ra := NewReplicationApplier(s)
	doc := func(lsn uint64, c string) *binlog.BinlogEntry {
		return &binlog.BinlogEntry{LSN: lsn, Type: binlog.BinlogPut, BucketName: "docs", Key: []byte("doc|" + c + "|" + c + "|k|en"), Value: []byte("v")}
	}
	fts := func(lsn uint64) *binlog.BinlogEntry {
		return &binlog.BinlogEntry{LSN: lsn, Type: binlog.BinlogPut, BucketName: "custom", Key: []byte{byte(lsn)}, Value: []byte("t")} // #nosec G115 -- a test key byte; wrapping is fine
	}
	batch := []*binlog.BinlogEntry{
		doc(1, "blog"), fts(2), doc(3, "blog"), fts(4),
		{LSN: 5, Type: binlog.BinlogDeleteBucket, BucketName: "docs"},
		doc(6, "wiki"),
	}
	groups := ra.plan(batch)
	want := []struct {
		target     string
		everywhere bool
		n          int
		through    uint64
	}{{"blog", false, 2, 1}, {"", false, 2, 4}, {"", true, 1, 5}, {"wiki", false, 1, 6}}
	if len(groups) != len(want) {
		t.Fatalf("%d groups: %+v", len(groups), groups)
	}
	for i, w := range want {
		g := groups[i]
		if g.target != w.target || g.everywhere != w.everywhere || len(g.entries) != w.n || g.through != w.through {
			t.Errorf("group %d: %s/%v/%d through %d; want %+v", i, g.target, g.everywhere, len(g.entries), g.through, w)
		}
	}
	if err := ra.ApplyBatch(batch); err != nil || ra.LastAppliedLSN() != 6 {
		t.Fatalf("apply: %v, at %d", err, ra.LastAppliedLSN())
	}
	if pos, _ := s.loadReplicationPosition(); pos.LSN != 6 {
		t.Errorf("recorded position %d", pos.LSN)
	}
	var got []byte
	_ = s.DB.View(func(tx *bolt.Tx) error { got = tx.Bucket([]byte("custom")).Get([]byte{4}); return nil })
	if string(got) != "t" {
		t.Error("the main file's entries were not applied")
	}
}
