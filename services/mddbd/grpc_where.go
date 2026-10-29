package main

import (
	"context"

	proto "mddb/proto"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// gRPC for operations by metadata filter (GO-045), on the same RunWhere as
// REST and MCP. A dry run only reads, so a read-only server answers it.
func (g *GRPCServer) where(ctx context.Context, op WhereOp, req *proto.WhereRequest) (*proto.WhereResponse, error) {
	perm := PermWrite
	if req.DryRun {
		perm = PermRead
	} else if err := g.checkWritable(); err != nil {
		return nil, err
	}
	if err := g.permit(ctx, req.Collection, perm); err != nil {
		return nil, err
	}
	wr := WhereRequest{Collection: req.Collection, FilterMeta: metaValuesFromProto(req.FilterMeta),
		Filter: metaFilterFromProto(req.Filter), Limit: int(req.Limit), Cursor: req.Cursor, DryRun: req.DryRun,
		Meta: metaValuesFromProto(req.Meta), TTL: req.Ttl}
	res, err := g.server.RunWhere(op, wr)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	return &proto.WhereResponse{Matched: safeInt32(res.Matched), Affected: safeInt32(res.Affected), Sample: res.Sample,
		NextCursor: res.NextCursor, DryRun: res.DryRun, Errors: res.Errors}, nil
}

// DeleteWhere deletes every document a metadata filter matches.
func (g *GRPCServer) DeleteWhere(ctx context.Context, req *proto.WhereRequest) (*proto.WhereResponse, error) {
	return g.where(ctx, WhereDelete, req)
}

// UpdateWhere merges metadata into every document a filter matches.
func (g *GRPCServer) UpdateWhere(ctx context.Context, req *proto.WhereRequest) (*proto.WhereResponse, error) {
	return g.where(ctx, WhereUpdate, req)
}

// SetTTLWhere sets the time to live of every document a filter matches.
func (g *GRPCServer) SetTTLWhere(ctx context.Context, req *proto.WhereRequest) (*proto.WhereResponse, error) {
	return g.where(ctx, WhereSetTTL, req)
}
