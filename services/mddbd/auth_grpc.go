package main

import (
	"context"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// authenticateContext validates the JWT / API key carried in the incoming gRPC
// metadata and returns a context with the resolved claims injected. It is the
// single source of truth shared by the unary and stream interceptors (SEC-003,
// DRY) so streaming RPCs cannot bypass the auth that unary RPCs enforce.
func (am *AuthManager) authenticateContext(ctx context.Context) (context.Context, error) {
	// Extract metadata from context
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "missing metadata")
	}

	token := metadataBearer(md)

	// If no bearer token, try x-api-key
	if values := md.Get("x-api-key"); token == "" && len(values) > 0 {
		var err error
		if ctx, token, err = am.grpcAPIKeySession(ctx, values[0]); err != nil {
			return nil, err
		}
	}

	// No token found
	if token == "" {
		return nil, status.Error(codes.Unauthenticated, "missing authentication")
	}

	// Validate JWT
	claims, err := ValidateJWT(token, am.config.JWTSecret)
	if err != nil {
		return nil, status.Error(codes.Unauthenticated, "invalid token")
	}

	// Check if user still exists and is not disabled
	user, err := am.GetUser(claims.Username)
	if err != nil || user.Disabled {
		return nil, status.Error(codes.Unauthenticated, "user disabled or not found")
	}
	am.refreshClaims(claims)

	// Inject claims into context
	return context.WithValue(ctx, authContextKey, claims), nil
}

// metadataBearer is the token of the authorization metadata: "Bearer <token>"
// or the raw token; "" when there is none.
func metadataBearer(md metadata.MD) string {
	values := md.Get("authorization")
	if len(values) == 0 {
		return ""
	}
	auth := values[0]
	if strings.HasPrefix(strings.ToLower(auth), "bearer ") {
		return strings.TrimPrefix(strings.TrimPrefix(auth, "Bearer "), "bearer ")
	}
	return auth
}

// grpcAPIKeySession turns a valid API key into a short-lived session token,
// as the HTTP middleware does — the tenant confinement travels with it — and
// records the key in the context (GO-054: per-key limits).
func (am *AuthManager) grpcAPIKeySession(ctx context.Context, apiKey string) (context.Context, string, error) {
	username, err := am.ValidateAPIKey(apiKey)
	if err != nil {
		return ctx, "", status.Error(codes.Unauthenticated, "invalid api key")
	}
	token, err := apiKeySessionToken(username, am.UserTenant(username), am.IsAdmin(username), am.config.JWTSecret, 1*time.Hour)
	if err != nil {
		return ctx, "", status.Error(codes.Internal, "failed to generate token")
	}
	return withAPIKeyID(ctx, apiKey), token, nil
}

// GRPCUnaryInterceptor returns a gRPC unary interceptor for authentication.
func (am *AuthManager) GRPCUnaryInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		if !am.enabled {
			return handler(ctx, req)
		}
		authCtx, err := am.authenticateContext(ctx)
		if err != nil {
			return nil, err
		}
		return handler(authCtx, req)
	}
}

// authServerStream wraps a grpc.ServerStream so the authenticated context
// (with injected claims) is what handlers and CheckPermission see via
// stream.Context().
type authServerStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (w *authServerStream) Context() context.Context { return w.ctx }

// GRPCStreamInterceptor returns a gRPC stream interceptor for authentication.
//
// SEC-003: without this, every server-streaming RPC (MDDB.Export, the whole
// MDDBReplication service) bypassed authentication entirely — and Export, which
// calls CheckPermission(stream.Context(), ...), always failed because no claims
// were ever injected into the stream context. This authenticates the stream and
// forwards a context carrying the claims, fixing both the security hole and the
// fail-closed Export bug.
func (am *AuthManager) GRPCStreamInterceptor() grpc.StreamServerInterceptor {
	return func(srv interface{}, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		if !am.enabled {
			return handler(srv, ss)
		}
		authCtx, err := am.authenticateContext(ss.Context())
		if err != nil {
			return err
		}
		return handler(srv, &authServerStream{ServerStream: ss, ctx: authCtx})
	}
}
