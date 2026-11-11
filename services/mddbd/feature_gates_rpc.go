package main

import (
	"context"

	"github.com/99designs/gqlgen/graphql"
	"github.com/vektah/gqlparser/v2/gqlerror"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// The gates of a disabled module (GO-049) on gRPC, MCP and GraphQL; the
// tables they read are in feature_gates.go.

// rpcFeatureError is FAILED_PRECONDITION for a method of a disabled module.
func (s *Server) rpcFeatureError(fullMethod string) error {
	if f := s.disabledFeatureOf(rpcFeature(fullMethod)); f != "" {
		return status.Error(codes.FailedPrecondition, featureDisabledError{feature: f}.Error())
	}
	return nil
}

// featureUnaryInterceptor refuses unary calls into a disabled module.
func (s *Server) featureUnaryInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		if err := s.rpcFeatureError(info.FullMethod); err != nil {
			return nil, err
		}
		return handler(ctx, req)
	}
}

// featureStreamInterceptor refuses streaming calls into a disabled module.
func (s *Server) featureStreamInterceptor() grpc.StreamServerInterceptor {
	return func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		if err := s.rpcFeatureError(info.FullMethod); err != nil {
			return err
		}
		return handler(srv, ss)
	}
}

// toolFeatureError refuses a built-in MCP tool of a disabled module. A custom
// tool is refused when it calls its action, which passes this gate too.
func (s *Server) toolFeatureError(name string) error {
	if f := s.disabledFeatureOf(toolFeature(name)); f != "" {
		return featureDisabledError{feature: f}
	}
	return nil
}

// featureVisibleTools leaves out of tools/list the built-in tools of disabled
// modules, and the custom tools whose action is one. Without an in-process
// server there is nothing to ask, and the list is returned as it is.
func featureVisibleTools(client MCPClient, custom []MCPCustomToolConfig, tools []MCPTool) []MCPTool {
	dc, isDirect := client.(*DirectClient)
	if !isDirect || dc.server == nil {
		return tools
	}
	action := map[string]string{}
	for _, ct := range custom {
		action[ct.Name] = ct.Action
	}
	out := tools[:0:0]
	for _, t := range tools {
		name := t.Name
		if a, isCustom := action[name]; isCustom {
			name = a
		}
		if dc.server.toolFeatureError(name) == nil {
			out = append(out, t)
		}
	}
	return out
}

// featureFieldMiddleware fails the GraphQL root fields of a disabled module.
func (s *Server) featureFieldMiddleware(ctx context.Context, next graphql.Resolver) (any, error) {
	if fc := graphql.GetFieldContext(ctx); fc != nil {
		if f := s.disabledFeatureOf(graphqlFeatureFields[fc.Object+"."+fc.Field.Name]); f != "" {
			return nil, gqlerror.Errorf("%s", featureDisabledError{feature: f}.Error())
		}
	}
	return next(ctx)
}
