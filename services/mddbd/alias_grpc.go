package main

import (
	"context"

	"google.golang.org/grpc"
	protov2 "google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// gRPC side of alias resolution (GO-044): one interceptor rewrites the
// collection fields of every request message, whatever the RPC.

var (
	aliasProtoStringFields = map[protoreflect.Name]bool{"collection": true, "source_collection": true}
	aliasProtoListFields   = map[protoreflect.Name]bool{"collections": true, "target_collections": true}
)

// resolveAliasMessage rewrites a message's top-level collection fields.
func (a *AliasManager) resolveAliasMessage(msg any) {
	m, ok := msg.(protov2.Message)
	if !ok || !a.active() {
		return
	}
	r := m.ProtoReflect()
	fields := r.Descriptor().Fields()
	for i := 0; i < fields.Len(); i++ {
		fd := fields.Get(i)
		if fd.Kind() != protoreflect.StringKind {
			continue
		}
		switch {
		case aliasProtoStringFields[fd.Name()] && !fd.IsList():
			if v := r.Get(fd).String(); v != "" {
				if t := a.Resolve(v); t != v {
					r.Set(fd, protoreflect.ValueOfString(t))
				}
			}
		case aliasProtoListFields[fd.Name()] && fd.IsList():
			list := r.Mutable(fd).List()
			for j := 0; j < list.Len(); j++ {
				if v := list.Get(j).String(); a.Resolve(v) != v {
					list.Set(j, protoreflect.ValueOfString(a.Resolve(v)))
				}
			}
		}
	}
}

// aliasUnaryInterceptor resolves aliases in unary requests.
func (s *Server) aliasUnaryInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		s.Aliases.resolveAliasMessage(req)
		return handler(ctx, req)
	}
}

// aliasStream resolves aliases in each message a stream receives (Export).
type aliasStream struct {
	grpc.ServerStream
	aliases *AliasManager
}

func (a aliasStream) RecvMsg(m any) error {
	if err := a.ServerStream.RecvMsg(m); err != nil {
		return err
	}
	a.aliases.resolveAliasMessage(m)
	return nil
}

// aliasStreamInterceptor resolves aliases in streaming requests.
func (s *Server) aliasStreamInterceptor() grpc.StreamServerInterceptor {
	return func(srv any, ss grpc.ServerStream, _ *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		return handler(srv, aliasStream{ServerStream: ss, aliases: s.Aliases})
	}
}
