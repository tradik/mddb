package main

import (
	"context"
	"errors"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	protov2 "google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// The gRPC side of the access gate (SEC-019): interceptors that apply the
// method's rule from grpcAccessRules to the request message. They run after
// authentication, which puts the caller's claims in the context.

// accessUnaryInterceptor gates unary calls.
func (s *Server) accessUnaryInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		if err := s.authorizeRPC(ctx, info.FullMethod, req); err != nil {
			return nil, err
		}
		return handler(ctx, req)
	}
}

// accessStreamInterceptor gates streaming calls: a call whose rule needs no
// collection before it starts, and each message the client sends after.
func (s *Server) accessStreamInterceptor() grpc.StreamServerInterceptor {
	return func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		rule := grpcAccessRules[info.FullMethod]
		if rule.class != accessCollection {
			if err := s.authorizeRPC(ss.Context(), info.FullMethod, nil); err != nil {
				return err
			}
			return handler(srv, ss)
		}
		return handler(srv, accessStream{ServerStream: ss, s: s, method: info.FullMethod})
	}
}

// accessStream checks every request message a stream receives (Export).
type accessStream struct {
	grpc.ServerStream
	s      *Server
	method string
}

// RecvMsg receives a message and refuses it when its collections are not
// permitted.
func (a accessStream) RecvMsg(m any) error {
	if err := a.ServerStream.RecvMsg(m); err != nil {
		return err
	}
	return a.s.authorizeRPC(a.Context(), a.method, m)
}

// authorizeRPC applies the method's rule to a request message, as a gRPC
// status error.
func (s *Server) authorizeRPC(ctx context.Context, method string, req any) error {
	err := s.authorize(ctx, grpcAccessRules[method], messageCollections(req))
	switch {
	case err == nil:
		return nil
	case errors.Is(err, ErrUnauthorized):
		return status.Error(codes.Unauthenticated, "unauthorized")
	default:
		return status.Error(codes.PermissionDenied, "forbidden: "+method)
	}
}

// messageCollections reads the collection fields of a request message, and
// of the messages it holds directly (a curation rule, a nested options
// message), so a collection one level down is checked too.
func messageCollections(msg any) []string {
	m, ok := msg.(protov2.Message)
	if !ok {
		return nil
	}
	var out []string
	r := m.ProtoReflect()
	r.Range(func(fd protoreflect.FieldDescriptor, v protoreflect.Value) bool {
		switch {
		case fd.Kind() == protoreflect.StringKind && !fd.IsList() && aliasProtoStringFields[fd.Name()]:
			out = append(out, v.String())
		case fd.Kind() == protoreflect.StringKind && fd.IsList() && aliasProtoListFields[fd.Name()]:
			for i := 0; i < v.List().Len(); i++ {
				out = append(out, v.List().Get(i).String())
			}
		case fd.Kind() == protoreflect.MessageKind && !fd.IsList() && !fd.IsMap():
			out = append(out, messageCollections(v.Message().Interface())...)
		}
		return true
	})
	return out
}
