package main

import (
	"context"
	"strconv"

	"mddb/internal/binlog"
	"mddb/internal/cluster"
	proto "mddb/proto"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// The election's messages on the replication service (GO-051), and the
// checks a cluster leader makes before it streams its log to a follower.
// They are authorised as replication is: a peer is a node holding the
// replication secret, a client certificate or admin credentials.

var errNotInCluster = status.Error(codes.FailedPrecondition, "this node is not in a cluster (MDDB_CLUSTER_PEERS)")

// clusterFor authorises a peer and returns the node's cluster.
func (rs *ReplicationServer) clusterFor(ctx context.Context) (*clusterRuntime, error) {
	if err := rs.authorizeReplication(ctx); err != nil {
		return nil, err
	}
	if rs.server.cluster == nil {
		return nil, errNotInCluster
	}
	return rs.server.cluster, nil
}

// RequestVote answers a candidate.
func (rs *ReplicationServer) RequestVote(ctx context.Context, req *proto.VoteRequest) (*proto.VoteResponse, error) {
	c, err := rs.clusterFor(ctx)
	if err != nil {
		return nil, err
	}
	r := c.node.HandleVote(cluster.VoteRequest{
		Term: req.Term, Candidate: req.CandidateId,
		Last: cluster.Position{LSN: req.LastLsn, Term: req.LastTerm},
	})
	return &proto.VoteResponse{Term: r.Term, Granted: r.Granted, Reason: r.Reason}, nil
}

// Heartbeat answers the leader.
func (rs *ReplicationServer) Heartbeat(ctx context.Context, req *proto.HeartbeatRequest) (*proto.HeartbeatResponse, error) {
	c, err := rs.clusterFor(ctx)
	if err != nil {
		return nil, err
	}
	r := c.node.HandleHeartbeat(cluster.HeartbeatRequest{
		Term: req.Term, Leader: req.LeaderId, LeaderHTTP: req.LeaderHttp, Release: req.Release,
	})
	return &proto.HeartbeatResponse{
		Term: r.Term, Ok: r.OK, LastLsn: r.Last.LSN, LastTerm: r.Last.Term,
		HttpAddr: r.HTTP, State: string(r.State),
	}, nil
}

// StepDown hands the leadership over for a promotion.
func (rs *ReplicationServer) StepDown(ctx context.Context, req *proto.StepDownRequest) (*proto.StepDownResponse, error) {
	c, err := rs.clusterFor(ctx)
	if err != nil {
		return nil, err
	}
	r, err := c.node.StepDown(req.Term, req.ToNode)
	if err != nil {
		return &proto.StepDownResponse{Term: r.Term, Reason: err.Error()}, status.Error(codes.FailedPrecondition, err.Error())
	}
	return &proto.StepDownResponse{Term: r.Term, Ok: true, LastLsn: r.Last.LSN, LastTerm: r.Last.Term}, nil
}

// Reasons in the ErrorInfo of a refused stream.
const (
	reasonLogDiverged = "LOG_DIVERGED"
	matchLSNKey       = "match_lsn"
)

// checkStream is what a cluster leader checks before it streams to a
// follower that sent its term: that the follower's term is not later (this
// node would be a stale leader), that this node leads, and that the
// follower's log is a prefix of this one. A log that parted from this one
// is refused with the last LSN they share, so the follower can set its own
// later entries aside before it takes a snapshot.
func (c *clusterRuntime) checkStream(req *proto.StreamBinlogRequest) error {
	if req.Term == 0 && len(req.TermHistory) == 0 {
		return nil // a follower outside a cluster
	}
	if req.Term > c.node.Term() {
		c.node.Observe(req.Term)
		return status.Error(codes.Aborted, "this node's term is behind the follower's; it does not lead")
	}
	if c.node.State() != cluster.Leader {
		return status.Error(codes.Unavailable, "this node is not the leader")
	}
	bl := c.s.Binlog
	match := binlog.CommonPrefix(bl.Terms(), bl.CurrentLSN(), termsFromProto(req.TermHistory), req.FromLsn)
	if match >= req.FromLsn {
		return nil
	}
	st := status.New(codes.FailedPrecondition, "the follower's log parts from the leader's after LSN "+
		strconv.FormatUint(match, 10)+", snapshot required")
	if withInfo, err := st.WithDetails(&errdetails.ErrorInfo{
		Reason: reasonLogDiverged, Domain: "mddb",
		Metadata: map[string]string{matchLSNKey: strconv.FormatUint(match, 10)},
	}); err == nil {
		st = withInfo
	}
	return st.Err()
}

// divergedAt is the match LSN of a stream refused because the logs parted.
func divergedAt(err error) (uint64, bool) {
	for _, d := range status.Convert(err).Details() {
		if info, ok := d.(*errdetails.ErrorInfo); ok && info.Reason == reasonLogDiverged {
			lsn, perr := strconv.ParseUint(info.Metadata[matchLSNKey], 10, 64)
			return lsn, perr == nil
		}
	}
	return 0, false
}

func termsFromProto(in []*proto.TermStart) []binlog.TermStart {
	out := make([]binlog.TermStart, 0, len(in))
	for _, t := range in {
		out = append(out, binlog.TermStart{Term: t.Term, Start: t.StartLsn})
	}
	return out
}

func termsToProto(in []binlog.TermStart) []*proto.TermStart {
	out := make([]*proto.TermStart, 0, len(in))
	for _, t := range in {
		out = append(out, &proto.TermStart{Term: t.Term, StartLsn: t.Start})
	}
	return out
}
