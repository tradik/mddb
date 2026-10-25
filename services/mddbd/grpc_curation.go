package main

import (
	"context"

	proto "mddb/proto"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// facetsToProto converts the in-memory FacetResult shape into the map<>
// shape used by FTSResponse and HybridSearchResponse proto.
func facetsToProto(in FacetResult) map[string]*proto.FacetBucketList {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]*proto.FacetBucketList, len(in))
	for k, buckets := range in {
		pb := make([]*proto.FacetBucketProto, len(buckets))
		for i, b := range buckets {
			pb[i] = &proto.FacetBucketProto{Value: b.Value, Count: safeInt32(b.Count)}
		}
		out[k] = &proto.FacetBucketList{Buckets: pb}
	}
	return out
}

// --- Curation RPCs ---

func curationToProto(r *CurationRule) *proto.CurationRuleProto {
	if r == nil {
		return nil
	}
	pins := make([]*proto.PinnedDocProto, len(r.Pins))
	for i, p := range r.Pins {
		pins[i] = &proto.PinnedDocProto{Key: p.Key, Lang: p.Lang, Position: safeInt32(p.Position)}
	}
	return &proto.CurationRuleProto{
		Id:         r.ID,
		Collection: r.Collection,
		Query:      r.Query,
		MatchMode:  r.MatchMode,
		Pins:       pins,
		Hides:      r.Hides,
		Enabled:    r.Enabled,
		CreatedAt:  r.CreatedAt,
		UpdatedAt:  r.UpdatedAt,
	}
}

func protoToCuration(p *proto.CurationRuleProto) *CurationRule {
	if p == nil {
		return nil
	}
	pins := make([]PinnedDoc, len(p.Pins))
	for i, pp := range p.Pins {
		pins[i] = PinnedDoc{Key: pp.Key, Lang: pp.Lang, Position: int(pp.Position)}
	}
	return &CurationRule{
		ID:         p.Id,
		Collection: p.Collection,
		Query:      p.Query,
		MatchMode:  p.MatchMode,
		Pins:       pins,
		Hides:      p.Hides,
		Enabled:    p.Enabled,
		CreatedAt:  p.CreatedAt,
		UpdatedAt:  p.UpdatedAt,
	}
}

// ListCurationRules implements the ListCurationRules RPC.
func (g *GRPCServer) ListCurationRules(ctx context.Context, req *proto.ListCurationRulesRequest) (*proto.ListCurationRulesResponse, error) {
	if g.server.CurationManager == nil {
		return nil, status.Error(codes.FailedPrecondition, "curation manager not initialized")
	}
	if g.server.AuthManager != nil {
		scope := req.Collection
		if scope == "" {
			scope = "*"
		}
		if err := g.server.AuthManager.CheckPermission(ctx, scope, PermRead); err != nil {
			return nil, status.Error(codes.PermissionDenied, err.Error())
		}
	}
	var rules []*CurationRule
	if req.Collection == "" {
		rules = g.server.CurationManager.ListAll()
	} else {
		rules = g.server.CurationManager.ListByCollection(req.Collection)
	}
	out := make([]*proto.CurationRuleProto, len(rules))
	for i, r := range rules {
		out[i] = curationToProto(r)
	}
	return &proto.ListCurationRulesResponse{Rules: out, Total: safeInt32(len(out)), Collection: req.Collection}, nil
}

// CreateCurationRule implements the CreateCurationRule RPC.
func (g *GRPCServer) CreateCurationRule(ctx context.Context, req *proto.CreateCurationRuleRequest) (*proto.CurationRuleProto, error) {
	if err := g.checkWritable(); err != nil {
		return nil, err
	}
	if g.server.CurationManager == nil {
		return nil, status.Error(codes.FailedPrecondition, "curation manager not initialized")
	}
	if req.Rule == nil {
		return nil, status.Error(codes.InvalidArgument, "missing rule")
	}
	rule := protoToCuration(req.Rule)
	if g.server.AuthManager != nil {
		if err := g.server.AuthManager.CheckPermission(ctx, rule.Collection, PermWrite); err != nil {
			return nil, status.Error(codes.PermissionDenied, err.Error())
		}
	}
	rule.ID = "" // force new ID — clients can't smuggle in an existing one
	if err := g.server.CurationManager.Set(rule); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	return curationToProto(rule), nil
}

// UpdateCurationRule implements the UpdateCurationRule RPC.
func (g *GRPCServer) UpdateCurationRule(ctx context.Context, req *proto.UpdateCurationRuleRequest) (*proto.CurationRuleProto, error) {
	if err := g.checkWritable(); err != nil {
		return nil, err
	}
	if g.server.CurationManager == nil {
		return nil, status.Error(codes.FailedPrecondition, "curation manager not initialized")
	}
	if req.Rule == nil || req.Rule.Id == "" {
		return nil, status.Error(codes.InvalidArgument, "missing rule or id")
	}
	prev, exists := g.server.CurationManager.Get(req.Rule.Id)
	if !exists {
		return nil, status.Error(codes.NotFound, "rule not found")
	}
	rule := protoToCuration(req.Rule)
	if rule.Collection == "" {
		rule.Collection = prev.Collection
	}
	rule.CreatedAt = prev.CreatedAt
	if g.server.AuthManager != nil {
		if err := g.server.AuthManager.CheckPermission(ctx, rule.Collection, PermWrite); err != nil {
			return nil, status.Error(codes.PermissionDenied, err.Error())
		}
	}
	if err := g.server.CurationManager.Set(rule); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	return curationToProto(rule), nil
}

// DeleteCurationRule implements the DeleteCurationRule RPC.
func (g *GRPCServer) DeleteCurationRule(ctx context.Context, req *proto.DeleteCurationRuleRequest) (*proto.DeleteCurationRuleResponse, error) {
	if err := g.checkWritable(); err != nil {
		return nil, err
	}
	if g.server.CurationManager == nil {
		return nil, status.Error(codes.FailedPrecondition, "curation manager not initialized")
	}
	if req.Id == "" {
		return nil, status.Error(codes.InvalidArgument, "missing id")
	}
	rule, exists := g.server.CurationManager.Get(req.Id)
	if !exists {
		return nil, status.Error(codes.NotFound, "rule not found")
	}
	if g.server.AuthManager != nil {
		if err := g.server.AuthManager.CheckPermission(ctx, rule.Collection, PermWrite); err != nil {
			return nil, status.Error(codes.PermissionDenied, err.Error())
		}
	}
	if err := g.server.CurationManager.Delete(req.Id); err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &proto.DeleteCurationRuleResponse{Status: "deleted", Id: req.Id}, nil
}
