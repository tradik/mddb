package main

import (
	"context"
	"errors"

	"mddb/proto"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Module switches over gRPC (GO-049): the methods call what GET and
// PUT /v1/features call, and the access gate applies the same permissions
// (access_rules_grpc.go).

// ListFeatures describes every module, as GET /v1/features.
func (g *GRPCServer) ListFeatures(_ context.Context, _ *proto.ListFeaturesRequest) (*proto.ListFeaturesResponse, error) {
	list := g.server.FeaturesList()
	out := &proto.ListFeaturesResponse{Features: make([]*proto.FeatureProto, 0, len(list.Features)), Version: list.Version}
	for _, st := range list.Features {
		out.Features = append(out.Features, featureToProto(st))
	}
	return out, nil
}

// SetFeature switches a module, as PUT /v1/features/{name}.
func (g *GRPCServer) SetFeature(ctx context.Context, req *proto.SetFeatureRequest) (*proto.SetFeatureResponse, error) {
	if err := g.corpusAdmin(ctx); err != nil { // a writable server and an administrator
		return nil, err
	}
	st, err := g.server.SetFeature(ctx, req.GetName(), req.GetEnabled())
	if err != nil {
		return nil, featureGRPCError(err)
	}
	return &proto.SetFeatureResponse{Feature: featureToProto(st), Version: g.server.Features.Version()}, nil
}

// featureGRPCError maps a failed switch as featureError maps it to HTTP.
func featureGRPCError(err error) error {
	switch {
	case errors.Is(err, errFeatureUnknown):
		return status.Error(codes.NotFound, err.Error())
	case errors.Is(err, errFeatureLocked):
		return status.Error(codes.FailedPrecondition, err.Error())
	case errors.Is(err, errFeatureUnavailable):
		return status.Error(codes.Unavailable, err.Error())
	}
	return status.Error(codes.Internal, err.Error())
}

// featureToProto converts one module's state.
func featureToProto(st FeatureState) *proto.FeatureProto {
	p := &proto.FeatureProto{Name: st.Name, Description: st.Description, Enabled: st.Enabled, Configured: st.Configured,
		DefaultEnabled: st.Default, Requires: st.Requires, LockedBy: st.LockedBy, BlockedBy: st.BlockedBy, UpdatedBy: st.UpdatedBy}
	if st.UpdatedAt != nil {
		p.UpdatedAt = st.UpdatedAt.Unix()
	}
	return p
}
