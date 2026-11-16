package main

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"mddb/internal/binlog"
	proto "mddb/proto"

	bolt "go.etcd.io/bbolt"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// The leader's side of a stream, entry by entry (GO-050): what it refuses,
// and what it does when the live buffer and the file disagree.

func tailLog(t *testing.T, n int) *binlog.Binlog {
	t.Helper()
	bl, err := binlog.NewBinlog(filepath.Join(t.TempDir(), "x.db"), binlog.BinlogConfig{RetentionInterval: -1})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = bl.Close() })
	appendLeaderEntries(t, bl, n)
	return bl
}

func newTail(t *testing.T, bl *binlog.Binlog, sent uint64) (*binlogTail, *recordingStream) {
	t.Helper()
	stream := &recordingStream{ctx: context.Background()}
	return &binlogTail{log: bl, stream: stream, sent: sent}, stream
}

func TestAFollowerAheadOfTheLeaderIsRefused(t *testing.T) {
	s, bl, cleanup := replTestServer(t)
	defer cleanup()
	appendLeaderEntries(t, bl, 2)
	rs := NewReplicationServer(s)
	err := rs.StreamBinlog(&proto.StreamBinlogRequest{FollowerId: "f", FromLsn: 9}, &replBinlogStream{ctx: authedReplCtx(t, "s")})
	if status.Code(err) != codes.FailedPrecondition {
		t.Errorf("a follower ahead of the leader: %v", err)
	}
	if _, err := rs.AcknowledgeLSN(context.Background(), &proto.AcknowledgeLSNRequest{FollowerId: "f"}); status.Code(err) != codes.PermissionDenied {
		t.Errorf("an anonymous acknowledgement: %v", err)
	}
}

func TestStreamErrors(t *testing.T) {
	cases := map[error]codes.Code{
		nil:                               codes.OK,
		binlog.ErrBinlogLSNTooOld:         codes.FailedPrecondition,
		errStreamGap:                      codes.FailedPrecondition,
		status.Error(codes.Canceled, "x"): codes.Canceled,
		errors.New("disk"):                codes.Internal,
	}
	for in, want := range cases {
		if got := status.Code(streamError(in)); got != want {
			t.Errorf("%v: %v, want %v", in, got, want)
		}
	}
}

func TestLiveEntriesAndTheFile(t *testing.T) {
	bl := tailLog(t, 5)
	tail, stream := newTail(t, bl, 0)
	if err := tail.send(&binlog.BinlogEntry{LSN: 2}); !errors.Is(err, errStreamGap) {
		t.Errorf("an entry out of sequence was sent: %v", err)
	}
	// Entry 3 arrives live after 1 and 2 were dropped: the file fills in,
	// and 3 itself is not sent twice.
	if err := tail.live(&binlog.BinlogEntry{LSN: 3}); err != nil || tail.sent != 5 {
		t.Fatalf("after a gap: %v, sent through %d", err, tail.sent)
	}
	assertContiguous(t, stream.received(), 5)
	if err := tail.live(&binlog.BinlogEntry{LSN: 4}); err != nil || len(stream.received()) != 5 {
		t.Errorf("an entry already sent from the file: %v", err)
	}

	if err := bl.Rotate(4); err != nil { // the follower's next entry is gone
		t.Fatal(err)
	}
	behind, _ := newTail(t, bl, 1)
	if err := behind.live(&binlog.BinlogEntry{LSN: 5}); status.Code(streamError(err)) != codes.FailedPrecondition {
		t.Errorf("a gap the file cannot fill: %v", err)
	}
}

func TestFollowEndsWithTheLogOrWhenItFallsOutOfIt(t *testing.T) {
	bl := tailLog(t, 1)
	tail, _ := newTail(t, bl, 1)
	closed := make(chan *binlog.BinlogEntry)
	close(closed)
	if err := tail.follow(closed); err != nil {
		t.Errorf("a closed subscription: %v", err)
	}

	appendLeaderEntries(t, bl, 3)
	if err := bl.Rotate(3); err != nil {
		t.Fatal(err)
	}
	stale, _ := newTail(t, bl, 1)
	start := time.Now()
	if err := stale.follow(make(chan *binlog.BinlogEntry)); status.Code(err) != codes.FailedPrecondition {
		t.Errorf("an idle stream behind the log: %v", err)
	}
	if time.Since(start) > 5*time.Second {
		t.Error("the idle check took too long")
	}
}

// snapClient hands out one snapshot stream.
type snapClient struct {
	proto.MDDBReplicationClient
	chunks []*proto.SnapshotChunk
}

type snapStream struct {
	grpc.ClientStream
	*chunkStream
}

func (c *snapClient) RequestSnapshot(context.Context, *proto.SnapshotRequest, ...grpc.CallOption) (grpc.ServerStreamingClient[proto.SnapshotChunk], error) {
	return snapStream{chunkStream: &chunkStream{chunks: c.chunks}}, nil
}

func TestASnapshotThatIsNotADatabaseIsNotInstalled(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	rc := NewReplicationClient(s, ReplicationClientConfig{FollowerID: "f"})
	rc.client = &snapClient{chunks: []*proto.SnapshotChunk{{Data: []byte("not bolt"), LogId: 1}, {IsLast: true, LogId: 1}}}
	if err := rc.requestSnapshot(context.Background()); err == nil {
		t.Error("installed a snapshot that is not a database")
	}
	addTestDoc(t, s, "blog", "a", "en", "the old database serves on", nil)
}

func TestTwoWritersCreateOneCollectionFile(t *testing.T) {
	cf, err := openCollectionFiles(filepath.Join(t.TempDir(), "m.db.d"), func(string) *bolt.Options { return getOptimizedBoltOptions() })
	if err != nil {
		t.Fatal(err)
	}
	defer cf.closeAll()
	// Writers that all find no file and then queue for the lock: the first
	// creates it, the rest must take that one, not open a second.
	for round := range 150 { // the overlap is not certain in any one round
		name := fmt.Sprintf("c%d", round)
		start := make(chan struct{})
		got := make([]*bolt.DB, 8)
		var wg sync.WaitGroup
		for i := range got {
			wg.Go(func() {
				<-start
				got[i], _ = cf.getOrCreate(name)
			})
		}
		close(start)
		wg.Wait()
		for _, db := range got {
			if db == nil || db != got[0] {
				t.Fatalf("round %d: more than one handle for one collection", round)
			}
		}
	}
}

func TestAnIdleStreamWithNothingToSend(t *testing.T) {
	bl := tailLog(t, 2)
	tail, stream := newTail(t, bl, 2)
	if err := tail.idleCheck(make(chan *binlog.BinlogEntry)); err != nil || len(stream.received()) != 0 {
		t.Errorf("an idle check of a current stream: %v, sent %v", err, stream.received())
	}
}
