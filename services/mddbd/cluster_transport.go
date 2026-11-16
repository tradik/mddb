package main

import (
	"context"
	"crypto/tls"
	"sync"

	"mddb/internal/cluster"
	proto "mddb/proto"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
)

// peerTransport reaches the other nodes of the cluster over the replication
// service, authenticated as followers are: the replication secret, or the
// client certificate of MDDB_REPLICATION_TLS_*.
type peerTransport struct {
	tls   *tls.Config
	extra []grpc.DialOption // set before the node starts (a test's network)
	mu    sync.Mutex
	conns map[string]*grpc.ClientConn
}

func newPeerTransport(tlsCfg *tls.Config) *peerTransport {
	return &peerTransport{tls: tlsCfg, conns: map[string]*grpc.ClientConn{}}
}

// client is the replication client for addr, dialled once and kept.
func (t *peerTransport) client(addr string) (proto.MDDBReplicationClient, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if c, ok := t.conns[addr]; ok {
		return proto.NewMDDBReplicationClient(c), nil
	}
	creds := insecure.NewCredentials()
	if t.tls != nil {
		creds = credentials.NewTLS(t.tls)
	}
	c, err := grpc.NewClient(addr, append([]grpc.DialOption{grpc.WithTransportCredentials(creds)}, t.extra...)...)
	if err != nil {
		return nil, err
	}
	t.conns[addr] = c
	return proto.NewMDDBReplicationClient(c), nil
}

// close drops every connection.
func (t *peerTransport) close() {
	t.mu.Lock()
	defer t.mu.Unlock()
	for addr, c := range t.conns {
		_ = c.Close()
		delete(t.conns, addr)
	}
}

func (t *peerTransport) RequestVote(ctx context.Context, to cluster.Peer, req cluster.VoteRequest) (cluster.VoteReply, error) {
	c, err := t.client(to.Addr)
	if err != nil {
		return cluster.VoteReply{}, err
	}
	r, err := c.RequestVote(withReplicationSecret(ctx), &proto.VoteRequest{
		Term: req.Term, CandidateId: req.Candidate, LastLsn: req.Last.LSN, LastTerm: req.Last.Term,
	})
	if err != nil {
		return cluster.VoteReply{}, err
	}
	return cluster.VoteReply{Term: r.Term, Granted: r.Granted, Reason: r.Reason}, nil
}

func (t *peerTransport) Heartbeat(ctx context.Context, to cluster.Peer, req cluster.HeartbeatRequest) (cluster.HeartbeatReply, error) {
	c, err := t.client(to.Addr)
	if err != nil {
		return cluster.HeartbeatReply{}, err
	}
	r, err := c.Heartbeat(withReplicationSecret(ctx), &proto.HeartbeatRequest{
		Term: req.Term, LeaderId: req.Leader, LeaderHttp: req.LeaderHTTP, Release: req.Release,
	})
	if err != nil {
		return cluster.HeartbeatReply{}, err
	}
	return cluster.HeartbeatReply{
		Term: r.Term, OK: r.Ok, HTTP: r.HttpAddr, State: cluster.State(r.State),
		Last: cluster.Position{LSN: r.LastLsn, Term: r.LastTerm},
	}, nil
}

func (t *peerTransport) Probe(ctx context.Context, to cluster.Peer) (cluster.ProbeReply, error) {
	c, err := t.client(to.Addr)
	if err != nil {
		return cluster.ProbeReply{}, err
	}
	r, err := c.ReplicationStatus(withReplicationSecret(ctx), &proto.ReplicationStatusRequest{})
	if err != nil {
		return cluster.ProbeReply{}, err
	}
	return cluster.ProbeReply{
		Term: r.Term, State: cluster.State(r.Role), Leader: r.LeaderId, HTTP: r.HttpAddr,
		Last: cluster.Position{LSN: r.CurrentLsn, Term: r.LastTerm},
	}, nil
}

func (t *peerTransport) StepDown(ctx context.Context, to cluster.Peer, term uint64, successor string) (cluster.StepDownReply, error) {
	c, err := t.client(to.Addr)
	if err != nil {
		return cluster.StepDownReply{}, err
	}
	r, err := c.StepDown(withReplicationSecret(ctx), &proto.StepDownRequest{Term: term, ToNode: successor})
	if err != nil {
		return cluster.StepDownReply{}, err
	}
	return cluster.StepDownReply{Term: r.Term, Last: cluster.Position{LSN: r.LastLsn, Term: r.LastTerm}}, nil
}
