package main

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"mddb/internal/binlog"
	proto "mddb/proto"
)

// The binlog stream a follower tails (#267 comment). Three ways it lost or
// withheld entries, each without an error on either side: the whole history
// was read into memory before the first entry was sent; an entry appended
// between that read and the subscription reached neither; and a follower
// that fell more than the subscription buffer behind had the excess dropped.

// recordingStream records what the leader sends and lets a test act on each
// send — append more entries, or hold the stream to make it fall behind.
type recordingStream struct {
	proto.MDDBReplication_StreamBinlogServer
	ctx    context.Context
	mu     sync.Mutex
	lsns   []uint64
	onSend func(n int)
}

func (s *recordingStream) Context() context.Context { return s.ctx }

func (s *recordingStream) Send(e *proto.BinlogEntryProto) error {
	s.mu.Lock()
	s.lsns = append(s.lsns, e.Lsn)
	n := len(s.lsns)
	s.mu.Unlock()
	if s.onSend != nil {
		s.onSend(n)
	}
	return nil
}

func (s *recordingStream) received() []uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]uint64(nil), s.lsns...)
}

func appendN(t *testing.T, bl *binlog.Binlog, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		if err := bl.Append(&binlog.BinlogEntry{Type: binlog.BinlogPut, BucketName: "docs", Key: []byte(fmt.Sprint(i)), Value: []byte("v")}); err != nil {
			t.Fatal(err)
		}
	}
}

// runBinlogStream starts a stream and waits until it has sent through want.
func runBinlogStream(t *testing.T, rs *ReplicationServer, id string, stream *recordingStream, want uint64) (cancel func(), done <-chan error) {
	t.Helper()
	ctx, cancelCtx := context.WithCancel(stream.ctx)
	stream.ctx = ctx
	errc := make(chan error, 1)
	go func() { errc <- rs.StreamBinlog(&proto.StreamBinlogRequest{FollowerId: id}, stream) }()

	deadline := time.Now().Add(10 * time.Second)
	for {
		got := stream.received()
		if len(got) > 0 && got[len(got)-1] >= want {
			break
		}
		if time.Now().After(deadline) {
			cancelCtx()
			t.Fatalf("stream reached LSN %v, want %d", lastOf(got), want)
		}
		time.Sleep(5 * time.Millisecond)
	}
	return cancelCtx, errc
}

func lastOf(l []uint64) uint64 {
	if len(l) == 0 {
		return 0
	}
	return l[len(l)-1]
}

// assertContiguous checks the follower got every LSN from 1 to want, once,
// in order — the property replication depends on.
func assertContiguous(t *testing.T, got []uint64, want uint64) {
	t.Helper()
	if uint64(len(got)) != want {
		t.Fatalf("received %d entries, want %d", len(got), want)
	}
	for i, lsn := range got {
		if lsn != uint64(i)+1 {
			t.Fatalf("entry %d has LSN %d, want %d — the follower has a gap", i, lsn, i+1)
		}
	}
}

func TestEntriesWrittenDuringTheReplayReachTheFollower(t *testing.T) {
	s, bl, cleanup := replTestServer(t)
	defer cleanup()
	rs := NewReplicationServer(s)
	appendN(t, bl, 100)

	stream := &recordingStream{ctx: authedReplCtx(t, "s")}
	var once sync.Once
	stream.onSend = func(int) {
		// Writes keep arriving while the history is being sent.
		once.Do(func() { appendN(t, bl, 50) })
	}
	cancel, done := runBinlogStream(t, rs, "f1", stream, 150)
	cancel()
	<-done

	assertContiguous(t, stream.received(), 150)
}

func TestAFollowerThatFallsBehindTheBufferGetsEveryEntry(t *testing.T) {
	s, bl, cleanup := replTestServer(t)
	defer cleanup()
	rs := NewReplicationServer(s)
	appendN(t, bl, 1)

	release := make(chan struct{})
	stream := &recordingStream{ctx: authedReplCtx(t, "s")}
	stream.onSend = func(n int) {
		if n == 2 {
			<-release // the follower stalls on its second entry
		}
	}
	ctx, cancelCtx := context.WithCancel(stream.ctx)
	stream.ctx = ctx
	done := make(chan error, 1)
	go func() { done <- rs.StreamBinlog(&proto.StreamBinlogRequest{FollowerId: "f1"}, stream) }()

	for len(stream.received()) < 1 {
		time.Sleep(5 * time.Millisecond)
	}
	// A bulk import while the follower is stalled: more than the subscription
	// buffer holds, so the binlog drops the excess.
	const burst = 6000
	appendN(t, bl, burst)
	close(release)

	deadline := time.Now().Add(10 * time.Second)
	for lastOf(stream.received()) < burst+1 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	cancelCtx()
	<-done
	assertContiguous(t, stream.received(), burst+1)
}

// A follower that reconnects under the same ID before its old stream has
// ended: the old stream's cleanup must not take the new stream with it.
func TestAReconnectedFollowerKeepsItsStream(t *testing.T) {
	s, bl, cleanup := replTestServer(t)
	defer cleanup()
	rs := NewReplicationServer(s)
	appendN(t, bl, 1)

	old := &recordingStream{ctx: authedReplCtx(t, "s")}
	cancelOld, oldDone := runBinlogStream(t, rs, "f1", old, 1)
	fresh := &recordingStream{ctx: authedReplCtx(t, "s")}
	cancelFresh, freshDone := runBinlogStream(t, rs, "f1", fresh, 1)
	defer func() { cancelFresh(); <-freshDone }()

	cancelOld()
	<-oldDone

	if got := bl.Stats().Subscribers; got != 1 {
		t.Errorf("subscribers after the old stream ended = %d, want 1", got)
	}
	rs.mu.RLock()
	_, tracked := rs.followers["f1"]
	rs.mu.RUnlock()
	if !tracked {
		t.Error("the old stream's cleanup removed the reconnected follower")
	}

	appendN(t, bl, 1)
	deadline := time.Now().Add(5 * time.Second)
	for lastOf(fresh.received()) < 2 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if lastOf(fresh.received()) != 2 {
		t.Errorf("the reconnected stream stopped receiving: %v", fresh.received())
	}
}
