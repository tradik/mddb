package main

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"log/slog"
	"mddb/internal/binlog"
	proto "mddb/proto"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
)

// authorizeReplication gates the snapshot / binlog streams (SEC-001). Without
// it, RequestSnapshot streams the ENTIRE BoltDB — including auth_users (bcrypt
// hashes) and auth_apikeys — to any host that can reach the gRPC port, and
// StreamBinlog tails every write live, with no credentials. It accepts, in
// order: a matching replication secret, a verified mTLS client certificate, or
// an admin-authenticated context (the SEC-003 stream interceptor injects the
// claims). With none of those configured it refuses, naming the fix.
func (rs *ReplicationServer) authorizeReplication(ctx context.Context) error {
	// 1) Dedicated replication secret (best for cross-host links without full auth).
	if want := os.Getenv("MDDB_REPLICATION_SECRET"); want != "" {
		got := ""
		if md, ok := metadata.FromIncomingContext(ctx); ok {
			if v := md.Get("x-mddb-replication-secret"); len(v) > 0 {
				got = v[0]
			}
		}
		if subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1 {
			return nil
		}
		return status.Error(codes.PermissionDenied, "invalid replication secret")
	}

	// 2) A verified mTLS client certificate authenticates the peer.
	if p, ok := peer.FromContext(ctx); ok && p.AuthInfo != nil {
		if tlsInfo, ok := p.AuthInfo.(credentials.TLSInfo); ok && len(tlsInfo.State.VerifiedChains) > 0 {
			return nil
		}
	}

	// 3) Main auth enabled: require admin (claims injected by the stream interceptor).
	if rs.server.AuthManager != nil && rs.server.AuthManager.enabled {
		if err := rs.server.AuthManager.CheckPermission(ctx, "*", PermAdmin); err != nil {
			return status.Error(codes.PermissionDenied, "replication requires admin credentials")
		}
		return nil
	}

	// 4) Nothing configured — refuse rather than expose the whole database.
	return status.Error(codes.PermissionDenied,
		"replication is unauthenticated: set MDDB_REPLICATION_SECRET, enable MDDB_AUTH_ENABLED, or use mTLS")
}

// replicationProtocol is the version of the replication messages this build
// speaks (proto/mddb.proto). 0 is a release before 2.19.8, which sent none.
const replicationProtocol = 3

// Response headers a leader opens a binlog stream with.
const (
	replProtocolHeader = "x-mddb-replication-protocol"
	replLogIDHeader    = "x-mddb-log-id"
	replTermHeader     = "x-mddb-term" // a cluster leader's term (GO-051)
)

// ReplicationServer implements the MDDBReplication gRPC service (leader-side).
type ReplicationServer struct {
	proto.UnimplementedMDDBReplicationServer
	server    *Server
	followers map[string]*FollowerState
	mu        sync.RWMutex
	// streamSeq numbers binlog streams so each has its own subscription.
	streamSeq atomic.Uint64
}

// NewReplicationServer creates a new replication server. On a leader it also
// makes the node the start of its log: retention learns which entries the
// followers still need, and a position left from the node's own time as a
// follower is removed.
func NewReplicationServer(s *Server) *ReplicationServer {
	rs := &ReplicationServer{
		server:    s,
		followers: make(map[string]*FollowerState),
	}
	if s.cluster != nil {
		// Every cluster node may lead: retention keeps what its followers
		// need whenever it does. The position in the files is not used.
		s.Binlog.SetRetentionFloor(rs.retentionFloor)
	} else if s.ReplicationRole == "leader" && s.Binlog != nil {
		s.Binlog.SetRetentionFloor(rs.retentionFloor)
		if err := s.clearReplicationPosition(); err != nil {
			slog.Warn("replication: could not clear this node's old follower position", "err", err)
		}
	}
	return rs
}

// replicationGapCheck is how often an idle binlog stream checks that it has
// sent everything the log holds.
const replicationGapCheck = 500 * time.Millisecond

// errSnapshotRequired is the message of every refusal a follower answers by
// taking a snapshot; the code, FailedPrecondition, is what it acts on.
func errSnapshotRequired(why string) error {
	return status.Error(codes.FailedPrecondition, why+", snapshot required")
}

// StreamBinlog streams binlog entries from a given LSN. The stream stays open for continuous tailing.
func (rs *ReplicationServer) StreamBinlog(req *proto.StreamBinlogRequest, stream proto.MDDBReplication_StreamBinlogServer) error {
	if err := rs.authorizeReplication(stream.Context()); err != nil {
		return err
	}
	bl := rs.server.Binlog
	if bl == nil {
		return status.Error(codes.FailedPrecondition, "binlog not enabled on this node")
	}
	if req.FollowerId == "" {
		return status.Error(codes.InvalidArgument, "follower_id is required")
	}
	if c := rs.server.cluster; c != nil {
		if err := c.checkStream(req); err != nil {
			return err
		}
	}
	if err := rs.checkFollowerPosition(req); err != nil {
		return err
	}
	// The handshake's answer: which protocol, which log and — in a cluster —
	// which term. A follower from before 2.19.8 does not read it.
	_ = stream.SendHeader(metadata.Pairs(
		replProtocolHeader, fmt.Sprint(replicationProtocol),
		replLogIDHeader, binlog.FormatLogID(bl.LogID()),
		replTermHeader, fmt.Sprint(bl.Term())))

	state := rs.trackFollower(req.FollowerId, peerAddr(stream.Context()), req.FromLsn)
	defer rs.releaseFollower(state)
	slog.Info("replication follower streaming binlog", "followerID", req.FollowerId, "fromLSN", req.FromLsn)

	// A follower that reconnects under the same ID opens its new stream
	// before the old one has noticed it is dead, so the subscription is this
	// stream's own (#267).
	subID := fmt.Sprintf("%s#%d", req.FollowerId, rs.streamSeq.Add(1))
	// Subscribe before reading the file: an entry appended between the end of
	// the file read and a later subscription reached neither, and the
	// follower silently diverged.
	ch := bl.Subscribe(subID)
	defer bl.Unsubscribe(subID)

	tail := &binlogTail{log: bl, stream: stream, sent: req.FromLsn}
	// 1. Historical entries, streamed from the file as they are read.
	if err := tail.catchUp(); err != nil {
		return err
	}
	// 2. Live entries.
	return tail.follow(ch)
}

// checkFollowerPosition refuses a follower whose position is not one in this
// leader's log: it names another log, or an LSN this log has not reached.
// Either way the follower's data is not a prefix of the leader's history,
// and sending it the entries after its LSN would build on that.
func (rs *ReplicationServer) checkFollowerPosition(req *proto.StreamBinlogRequest) error {
	bl := rs.server.Binlog
	if req.LogId != 0 && req.LogId != bl.LogID() {
		return errSnapshotRequired("the follower's position is in another log")
	}
	if req.FromLsn > bl.CurrentLSN() {
		return errSnapshotRequired("the follower is ahead of this leader's log")
	}
	return nil
}

func peerAddr(ctx context.Context) string {
	if p, ok := peer.FromContext(ctx); ok {
		return p.Addr.String()
	}
	return ""
}

// binlogTail sends one follower the log from where it is.
type binlogTail struct {
	log    *binlog.Binlog
	stream proto.MDDBReplication_StreamBinlogServer
	sent   uint64
}

// errStreamGap means the log handed the stream an entry that does not follow
// the last one sent — the file changed under the read.
var errStreamGap = errors.New("binlog entry out of sequence")

// send passes on the entry after the last one sent, and nothing else: a
// follower applies what it is given, so a skipped entry is a silent hole.
func (t *binlogTail) send(entry *binlog.BinlogEntry) error {
	if entry.LSN != t.sent+1 {
		return errStreamGap
	}
	p := entryToProto(entry)
	if p.Term == 0 {
		p.Term = t.log.TermAt(entry.LSN) // read back from the file, which keeps no terms
	}
	if err := t.stream.Send(p); err != nil {
		return err
	}
	t.sent = entry.LSN
	return nil
}

// catchUp sends what the file holds after the last entry sent.
func (t *binlogTail) catchUp() error {
	return streamError(t.log.ScanFrom(t.sent, t.send))
}

// streamError is what a follower is told when the stream cannot go on:
// FailedPrecondition, which it answers with a snapshot, when the log no
// longer holds what it needs.
func streamError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, binlog.ErrBinlogLSNTooOld):
		return errSnapshotRequired("LSN too old")
	case errors.Is(err, errStreamGap):
		return errSnapshotRequired("the log no longer continues from the follower's LSN")
	}
	if _, isStatus := status.FromError(err); isStatus {
		return err
	}
	return status.Error(codes.Internal, err.Error())
}

// follow sends live entries. The subscription is a bounded buffer and drops
// entries when a follower falls behind — during the replay, or under a bulk
// import. LSNs are consecutive, so a gap is detectable: the missing entries
// are read back from the file instead of being lost. When the dropped
// entries were the last ones written, no later entry arrives to reveal the
// gap, so an idle stream also compares what it has sent with the log's
// current LSN.
func (t *binlogTail) follow(ch <-chan *binlog.BinlogEntry) error {
	idle := time.NewTicker(replicationGapCheck)
	defer idle.Stop()
	ctx := t.stream.Context()
	for {
		select {
		case <-idle.C:
			if err := t.idleCheck(ch); err != nil {
				return err
			}
		case entry, ok := <-ch:
			if !ok {
				return nil // channel closed (binlog shutting down)
			}
			if err := t.live(entry); err != nil {
				return streamError(err)
			}
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// idleCheck reads the file when the subscription is empty and the log holds
// entries the stream has not sent.
func (t *binlogTail) idleCheck(ch <-chan *binlog.BinlogEntry) error {
	if len(ch) == 0 && t.sent < t.log.CurrentLSN() {
		return t.catchUp()
	}
	return nil
}

// live sends an entry from the subscription, reading the file first when
// entries between the last one sent and this one were dropped.
func (t *binlogTail) live(entry *binlog.BinlogEntry) error {
	if entry.LSN <= t.sent {
		return nil // already sent from the file
	}
	if entry.LSN != t.sent+1 {
		if err := t.catchUp(); err != nil {
			return err
		}
		if entry.LSN <= t.sent {
			return nil
		}
	}
	return t.send(entry)
}

// ReplicationStatus returns the current replication state
func (rs *ReplicationServer) ReplicationStatus(_ context.Context, _ *proto.ReplicationStatusRequest) (*proto.ReplicationStatusResponse, error) {
	st := rs.server.replicationStatus()
	if rs.server.Binlog != nil {
		st.Followers = rs.followerViews()
	}
	return st.toProto(), nil
}

// AcknowledgeLSN processes LSN acknowledgment from a follower: the entries
// through it need not be kept for that follower any more, and the answer
// carries how far behind the leader measures it to be.
//
// It is authorised as the streams are: an acknowledgement decides which
// entries retention keeps, and an anonymous caller could otherwise hold the
// log at its hard limit or let a follower's entries go.
func (rs *ReplicationServer) AcknowledgeLSN(ctx context.Context, req *proto.AcknowledgeLSNRequest) (*proto.AcknowledgeLSNResponse, error) {
	if err := rs.authorizeReplication(ctx); err != nil {
		return nil, err
	}
	rs.mu.Lock()
	if fs, ok := rs.followers[req.FollowerId]; ok {
		fs.ConfirmedLSN = req.ConfirmedLsn
		fs.LastSeenAt = time.Now().Unix()
	}
	rs.mu.Unlock()
	if c := rs.server.cluster; c != nil {
		c.acks.record(req.FollowerId, req.ConfirmedLsn)
	}

	resp := &proto.AcknowledgeLSNResponse{Ok: true}
	if bl := rs.server.Binlog; bl != nil {
		resp.LeaderLsn = bl.CurrentLSN()
		resp.LagMs = measureLag(bl, req.ConfirmedLsn, time.Now()).Ms
	}
	return resp, nil
}

// entryToProto converts a binlog.BinlogEntry to the protobuf representation
func entryToProto(e *binlog.BinlogEntry) *proto.BinlogEntryProto {
	return &proto.BinlogEntryProto{
		Lsn:        e.LSN,
		Type:       uint32(e.Type),
		Timestamp:  e.Timestamp,
		BucketName: e.BucketName,
		Key:        e.Key,
		Value:      e.Value,
		Checksum:   e.Checksum,
		Term:       e.Term,
	}
}

// protoToEntry converts a protobuf BinlogEntryProto to internal binlog.BinlogEntry
func protoToEntry(p *proto.BinlogEntryProto) *binlog.BinlogEntry {
	return &binlog.BinlogEntry{
		LSN:        p.Lsn,
		Type:       binlog.BinlogEntryType(p.Type), // #nosec G115 -- entry type always within byte range
		Timestamp:  p.Timestamp,
		BucketName: p.BucketName,
		Key:        p.Key,
		Value:      p.Value,
		Checksum:   p.Checksum,
		Term:       p.Term,
	}
}
