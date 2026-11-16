package main

import (
	"context"
	"strconv"
	"strings"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	protov2 "google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
)

// Routing gRPC calls across groups (GO-052). A unary call for collections
// of another group is made again on that group's leader, with the caller's
// metadata, and its answer returned; with MDDB_PLACEMENT_ROUTE=redirect, and
// for a streaming call, the caller is told where to send it instead:
// FailedPrecondition with an ErrorInfo of reason COLLECTION_ELSEWHERE and
// the leader's addresses. A call for collections of several groups is
// refused: cross-collection search over groups is answered on REST.

// mdPlacementHops counts how often a call was sent on.
const mdPlacementHops = "x-mddb-placement-hops"

// placementUnaryInterceptor routes unary calls.
func (s *Server) placementUnaryInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		p := s.placement
		if p == nil {
			return handler(ctx, req)
		}
		group, write, err := p.rpcGroup(ctx, info.FullMethod, req)
		switch {
		case err != nil:
			return nil, err
		case group == "":
			return handler(ctx, req)
		case group != p.cfg.self:
			return p.forwardRPC(ctx, info.FullMethod, req, group)
		case !write || !p.leads():
			return handler(ctx, req) // a follower answers a write with its leader
		}
		ends, err := p.enterRPCWrites(ctx, req)
		defer ends()
		if err != nil {
			return nil, err
		}
		return handler(ctx, req)
	}
}

// placementStreamInterceptor refuses stream messages for collections of
// another group, naming where to send them.
func (s *Server) placementStreamInterceptor() grpc.StreamServerInterceptor {
	return func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		if s.placement == nil {
			return handler(srv, ss)
		}
		return handler(srv, placementStream{ServerStream: ss, p: s.placement, method: info.FullMethod})
	}
}

// placementStream checks every message a stream receives.
type placementStream struct {
	grpc.ServerStream
	p      *placementRuntime
	method string
}

func (ps placementStream) RecvMsg(m any) error {
	if err := ps.ServerStream.RecvMsg(m); err != nil {
		return err
	}
	group, _, err := ps.p.rpcGroup(ps.Context(), ps.method, m)
	if err == nil && group != "" && group != ps.p.cfg.self {
		err = ps.p.elsewhere(group)
	}
	return err
}

// rpcGroup is the group a call's collections belong to: "" when the call
// names none, or is not a collection call.
func (p *placementRuntime) rpcGroup(ctx context.Context, method string, req any) (string, bool, error) {
	rule := grpcAccessRules[method]
	if rule.class != accessCollection || rule.all || strings.HasPrefix(method, "/mddb.MDDBReplication/") {
		return "", false, nil
	}
	if hops(ctx) >= maxPlacementHops {
		return "", false, status.Error(codes.Unavailable, "the call went round the node groups; the placement is changing")
	}
	groups, known := p.groupsOf(p.resolveNames(messageCollections(req)))
	switch {
	case !known:
		return "", false, status.Error(codes.Unavailable, "this node has not received the placement map yet")
	case len(groups) > 1:
		return "", false, status.Error(codes.FailedPrecondition, "the call names collections held by different node groups")
	case len(groups) == 0:
		return "", false, nil
	}
	return groupName(groups), rule.op == PermWrite, nil
}

// enterRPCWrites waits while a written collection is fenced; ends ends the
// writes it counted.
func (p *placementRuntime) enterRPCWrites(ctx context.Context, req any) (ends func(), err error) {
	var done []func()
	ends = func() {
		for _, end := range done {
			end()
		}
	}
	for _, c := range p.resolveNames(messageCollections(req)) {
		end, again, werr := p.enterWrite(ctx, c)
		if again || werr != nil {
			return ends, status.Error(codes.Unavailable, errFenceHeld.Error())
		}
		done = append(done, end)
	}
	return ends, nil
}

// forwardRPC makes the call on group's leader.
func (p *placementRuntime) forwardRPC(ctx context.Context, method string, req any, group string) (any, error) {
	n, ok := p.leaderNode(group)
	if p.cfg.redirect || !ok {
		return nil, p.elsewhere(group)
	}
	out, err := responseFor(method)
	if err != nil {
		return nil, err
	}
	conn, err := p.s.cluster.tr.conn(n.GRPC)
	if err != nil {
		return nil, status.Error(codes.Unavailable, err.Error())
	}
	md, _ := metadata.FromIncomingContext(ctx)
	md = md.Copy()
	md.Set(mdPlacementHops, strconv.Itoa(hops(ctx)+1))
	if err := conn.Invoke(metadata.NewOutgoingContext(ctx, md), method, req, out); err != nil {
		p.leaders.forget(group)
		return nil, err
	}
	return out, nil
}

// responseFor is an empty response message of a method.
func responseFor(method string) (protov2.Message, error) {
	name := protoreflect.FullName(strings.ReplaceAll(strings.TrimPrefix(method, "/"), "/", "."))
	d, err := protoregistry.GlobalFiles.FindDescriptorByName(name)
	if err != nil {
		return nil, status.Error(codes.Unimplemented, "unknown method "+method)
	}
	md, ok := d.(protoreflect.MethodDescriptor)
	if !ok {
		return nil, status.Error(codes.Unimplemented, "not a method: "+method)
	}
	mt, err := protoregistry.GlobalTypes.FindMessageByName(md.Output().FullName())
	if err != nil {
		return nil, status.Error(codes.Unimplemented, err.Error())
	}
	return mt.New().Interface(), nil
}

// hops is how often the call was sent on before it came here.
func hops(ctx context.Context) int {
	md, _ := metadata.FromIncomingContext(ctx)
	if v := md.Get(mdPlacementHops); len(v) > 0 {
		n, _ := strconv.Atoi(v[0])
		return n
	}
	return 0
}

// elsewhere is the error naming where group's leader is.
func (p *placementRuntime) elsewhere(group string) error {
	n, _ := p.leaderNode(group)
	st := status.New(codes.FailedPrecondition, (&collectionElsewhereError{Group: group, LeaderHTTP: n.HTTP}).Error())
	if withInfo, err := st.WithDetails(&errdetails.ErrorInfo{Reason: "COLLECTION_ELSEWHERE", Domain: "mddb",
		Metadata: map[string]string{"group": group, "leader_id": n.ID, "leader_addr": n.HTTP, "leader_grpc": n.GRPC}}); err == nil {
		st = withInfo
	}
	return st.Err()
}
