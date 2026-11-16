package main

import (
	"context"
	"errors"
	"testing"
	"time"

	proto "mddb/proto"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// GO-052: the gRPC router, branch by branch.

// unaryHere is a handler that answers "here".
func unaryHere(context.Context, any) (any, error) { return "here", nil }

func callUnary(s *Server, ctx context.Context, method string, req any) (any, error) {
	return s.placementUnaryInterceptor()(ctx, req, &grpc.UnaryServerInfo{FullMethod: method}, unaryHere)
}

func TestPlacementUnaryInterceptorBranches(t *testing.T) {
	plain, cleanup := newHandlerTestServer(t)
	defer cleanup()
	if got, err := callUnary(plain, context.Background(), "/mddb.MDDB/Add", &proto.AddRequest{Collection: "c"}); got != "here" || err != nil {
		t.Fatalf("without placement: %v %v", got, err)
	}
	tp := startTestPlacement(t, map[string]int{"meta": 1, "g2": 1}, "meta")
	meta := tp.leader("meta")
	p := meta.s.placement
	mine := nameIn(t, p, "meta")
	hopped := metadata.NewIncomingContext(context.Background(), metadata.Pairs(mdPlacementHops, "2"))
	if _, err := callUnary(meta.s, hopped, "/mddb.MDDB/Add", &proto.AddRequest{Collection: mine}); status.Code(err) != codes.Unavailable {
		t.Fatalf("too many hops: %v", err)
	}
	if got, err := callUnary(meta.s, context.Background(), "/mddb.MDDB/Get", &proto.GetRequest{Collection: mine}); got != "here" || err != nil {
		t.Fatalf("a read here: %v %v", got, err)
	}
	// A write to a fenced collection waits, then is refused.
	timeout := p.cfg.fenceTimeout
	p.cfg.fenceTimeout = 20 * time.Millisecond
	defer func() { p.cfg.fenceTimeout = timeout }()
	if err := p.local.fence(mine, time.Second); err != nil {
		t.Fatal(err)
	}
	defer p.local.lift(mine)
	if _, err := callUnary(meta.s, context.Background(), "/mddb.MDDB/Add", &proto.AddRequest{Collection: mine}); status.Code(err) != codes.Unavailable {
		t.Fatalf("a fenced write: %v", err)
	}
}

func TestPlacementRPCGroupBranches(t *testing.T) {
	tp := startTestPlacement(t, map[string]int{"meta": 1, "g2": 1}, "meta")
	p := unitRuntime(tp.leader("meta"), nil, nil)
	mine, theirs := nameIn(t, p, "meta"), nameIn(t, p, "g2")
	ctx := context.Background()
	if _, _, err := p.rpcGroup(ctx, "/mddb.MDDB/CrossSearch", &proto.CrossSearchRequest{TargetCollections: []string{mine, theirs}}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("two groups: %v", err)
	}
	if g, _, err := p.rpcGroup(ctx, "/mddb.MDDB/Add", &proto.AddRequest{}); g != "" || err != nil {
		t.Fatalf("no collection: %q %v", g, err)
	}
	p.table.Store(nil)
	if _, _, err := p.rpcGroup(ctx, "/mddb.MDDB/Add", &proto.AddRequest{Collection: mine}); status.Code(err) != codes.Unavailable {
		t.Fatalf("no map: %v", err)
	}
}

func TestPlacementForwardRPCFailures(t *testing.T) {
	tp := startTestPlacement(t, map[string]int{"meta": 1}, "meta")
	node := tp.leader("meta")
	p := unitRuntime(node, fakeGroup(t, closedURL(t)), map[string]string{"far": "fake"})
	ctx := context.Background()
	req := &proto.GetRequest{Collection: "far"}
	if _, err := p.forwardRPC(ctx, "/mddb.MDDB/Nope", req, "fake"); status.Code(err) != codes.Unimplemented {
		t.Fatalf("unknown method: %v", err)
	}
	// The fake node serves no documents: the call fails there.
	if _, err := p.forwardRPC(ctx, "/mddb.MDDB/Get", req, "fake"); err == nil {
		t.Fatal("a call the leader cannot answer")
	}
	p.cfg.redirect = true
	if _, err := p.forwardRPC(ctx, "/mddb.MDDB/Get", req, "fake"); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("redirect: %v", err)
	}
	if _, err := responseFor("/mddb/AddRequest"); status.Code(err) != codes.Unimplemented {
		t.Fatalf("not a method: %v", err)
	}
	bad := unitRuntime(node, map[string][]placementNode{"bad": {{ID: "bad-1", GRPC: startFakeLeader(t, "bad-1"), HTTP: closedURL(t)}}}, map[string]string{"x": "bad"})
	bad.leaders.set("bad", "bad-1")
	bad.cfg.groups["bad"][0].GRPC = "dns://a:b:c/%zz"
	if _, err := bad.forwardRPC(ctx, "/mddb.MDDB/Get", req, "bad"); status.Code(err) != codes.Unavailable {
		t.Fatalf("an address no connection can be made to: %v", err)
	}
}

// recvStream is a server stream that receives one message, or fails.
type recvStream struct {
	grpc.ServerStream
	msg any
	err error
}

func (r recvStream) Context() context.Context { return context.Background() }

func (r recvStream) RecvMsg(m any) error {
	if r.err != nil {
		return r.err
	}
	*m.(*proto.GetRequest) = proto.GetRequest{Collection: r.msg.(string)}
	return nil
}

func TestPlacementStreamInterceptorBranches(t *testing.T) {
	plain, cleanup := newHandlerTestServer(t)
	defer cleanup()
	ran := false
	handler := func(any, grpc.ServerStream) error { ran = true; return nil }
	info := &grpc.StreamServerInfo{FullMethod: "/mddb.MDDB/Get"}
	if err := plain.placementStreamInterceptor()(nil, recvStream{}, info, handler); err != nil || !ran {
		t.Fatalf("without placement: %v %v", err, ran)
	}
	tp := startTestPlacement(t, map[string]int{"meta": 1, "g2": 1}, "meta")
	meta := tp.leader("meta")
	theirs := nameIn(t, meta.s.placement, "g2")
	var seen error
	recv := func(_ any, ss grpc.ServerStream) error {
		seen = ss.RecvMsg(&proto.GetRequest{})
		return nil
	}
	_ = meta.s.placementStreamInterceptor()(nil, recvStream{msg: theirs}, info, recv)
	if status.Code(seen) != codes.FailedPrecondition {
		t.Fatalf("another group's collection: %v", seen)
	}
	broken := errors.New("broken stream")
	_ = meta.s.placementStreamInterceptor()(nil, recvStream{err: broken}, info, recv)
	if !errors.Is(seen, broken) {
		t.Fatalf("a failed receive: %v", seen)
	}
}
