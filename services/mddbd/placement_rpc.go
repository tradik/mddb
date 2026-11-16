package main

import (
	"context"
	"errors"
	"fmt"

	proto "mddb/proto"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// The placement's messages between nodes (GO-052), on the replication
// service and authorised as replication is: a peer holds the replication
// secret, a client certificate or admin credentials.

var errNoPlacement = status.Error(codes.FailedPrecondition, "this node is not in a placement (MDDB_PLACEMENT_GROUPS)")

// placementFor authorises a peer and returns the node's placement.
func (rs *ReplicationServer) placementFor(ctx context.Context) (*placementRuntime, error) {
	if err := rs.authorizeReplication(ctx); err != nil {
		return nil, err
	}
	if rs.server.placement == nil {
		return nil, errNoPlacement
	}
	return rs.server.placement, nil
}

// GetPlacement answers with the map this node holds.
func (rs *ReplicationServer) GetPlacement(ctx context.Context, _ *proto.GetPlacementRequest) (*proto.PlacementMap, error) {
	p, err := rs.placementFor(ctx)
	if err != nil {
		return nil, err
	}
	t := p.current()
	if t == nil {
		return nil, status.Error(codes.Unavailable, "this node holds no placement map yet")
	}
	return t.toProto(p.leaderOf(p.cfg.meta)), nil
}

// PushPlacement takes a newer map from the meta leader at once.
func (rs *ReplicationServer) PushPlacement(ctx context.Context, m *proto.PlacementMap) (*proto.PushPlacementResponse, error) {
	p, err := rs.placementFor(ctx)
	if err != nil {
		return nil, err
	}
	p.adopt(tableFromProto(m))
	return &proto.PushPlacementResponse{Version: p.current().Version}, nil
}

// PinCollections records, on the meta leader, the collections a group holds
// that the map does not place yet. A collection the map already places is
// left where it is.
func (rs *ReplicationServer) PinCollections(ctx context.Context, req *proto.PinCollectionsRequest) (*proto.PlacementMap, error) {
	p, err := rs.placementFor(ctx)
	if err != nil {
		return nil, err
	}
	if !p.leadsMeta() {
		return nil, status.Error(codes.FailedPrecondition, "this node does not lead the meta group")
	}
	if _, ok := p.cfg.groups[req.Group]; !ok {
		return nil, status.Error(codes.InvalidArgument, "unknown group "+req.Group)
	}
	t, err := p.pin(req)
	if err != nil {
		return nil, status.Error(codes.Unavailable, err.Error())
	}
	return t.toProto(p.s.NodeID), nil
}

// pin places req's collections the map does not name yet in req's group.
func (p *placementRuntime) pin(req *proto.PinCollectionsRequest) (*placementTable, error) {
	return p.changeMap(func(t *placementTable) (bool, error) {
		write := false
		for _, c := range req.Collections {
			if _, placed := t.Entries[c]; !placed && c != "" {
				t.Entries[c] = placementEntry{Group: req.Group}
				write = true
			}
		}
		return write, nil
	})
}

// FenceCollection stops, on the source group's leader, writes to a
// collection being moved, and says through which LSN its writes are in the
// log; with release it lifts the fence.
func (rs *ReplicationServer) FenceCollection(ctx context.Context, req *proto.FenceCollectionRequest) (*proto.FenceCollectionResponse, error) {
	p, err := rs.placementFor(ctx)
	if err != nil {
		return nil, err
	}
	if req.Release {
		p.local.lift(req.Collection)
		return &proto.FenceCollectionResponse{}, nil
	}
	through, err := p.fence(req.Collection)
	if err != nil {
		return nil, status.Error(codes.FailedPrecondition, err.Error())
	}
	return &proto.FenceCollectionResponse{ThroughLsn: through}, nil
}

// fence holds writes to c here and returns the LSN no write to c follows.
func (p *placementRuntime) fence(c string) (uint64, error) {
	if !p.leads() {
		return 0, errors.New("this node does not lead its group")
	}
	if g, _ := p.owner(c); g != p.cfg.self {
		return 0, fmt.Errorf("collection %q is not held by this group", c)
	}
	if err := p.local.fence(c, p.cfg.fenceTimeout); err != nil {
		return 0, err
	}
	// From here the entry gate refuses c. A write whose transaction was
	// already past the gate holds the log's lock while it takes its LSNs;
	// taking the lock once means every such write has its LSN.
	p.s.Binlog.StableLSN()
	return p.s.Binlog.CurrentLSN(), nil
}

// peerClient is the replication client of node n.
func (p *placementRuntime) peerClient(n placementNode) (proto.MDDBReplicationClient, error) {
	return p.s.cluster.tr.client(n.GRPC)
}

// leaderClient is the replication client of group's leader.
func (p *placementRuntime) leaderClient(group string) (proto.MDDBReplicationClient, error) {
	n, ok := p.leaderNode(group)
	if !ok {
		return nil, fmt.Errorf("node group %q has no known leader", group)
	}
	return p.peerClient(n)
}
