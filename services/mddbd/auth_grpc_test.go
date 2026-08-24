package main

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	bolt "go.etcd.io/bbolt"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// authGrpcSetup creates an AuthManager for gRPC interceptor tests.
func authGrpcSetup(t *testing.T) (*AuthManager, func()) {
	t.Helper()
	// WIN-004: t.TempDir rather than a fixed /tmp path. That path does not
	// exist on Windows, and even on Unix it outlived the run — a second run
	// inherited the first one's database, and two tests with the same name in
	// different packages collided. The Close is registered here as well as in
	// the returned cleanup: Windows refuses to remove a directory holding an
	// open file, so a test that forgets `defer cleanup()` would fail the
	// temp-directory removal rather than merely leak.
	dbPath := filepath.Join(t.TempDir(), "auth.db")
	db, err := bolt.Open(dbPath, 0600, &bolt.Options{Timeout: 1 * time.Second})
	if err != nil {
		t.Fatalf("bolt.Open: %v", err)
	}

	config := AuthConfig{
		JWTSecret:     "grpc-test-secret-key-12345",
		JWTExpiry:     24 * time.Hour,
		AdminUsername: "admin",
		AdminPassword: "adminpass",
	}

	am := NewAuthManager(db, config)
	if err := am.EnsureBuckets(); err != nil {
		_ = db.Close()
		t.Fatalf("EnsureBuckets: %v", err)
	}
	if err := am.BootstrapAdmin(); err != nil {
		_ = db.Close()
		t.Fatalf("BootstrapAdmin: %v", err)
	}
	if err := am.LoadAll(); err != nil {
		_ = db.Close()
		t.Fatalf("LoadAll: %v", err)
	}

	cleanup := func() {
		_ = db.Close()
	}
	return am, cleanup
}

// authGrpcDummyHandler is a simple gRPC handler that returns a string response.
func authGrpcDummyHandler(ctx context.Context, req interface{}) (interface{}, error) {
	return "success", nil
}

func TestGRPCInterceptor_Disabled(t *testing.T) {
	am, cleanup := authGrpcSetup(t)
	defer cleanup()

	am.enabled = false

	interceptor := am.GRPCUnaryInterceptor()
	resp, err := interceptor(
		context.Background(),
		nil,
		&grpc.UnaryServerInfo{FullMethod: "/mddb.MDDB/GetDoc"},
		authGrpcDummyHandler,
	)
	if err != nil {
		t.Fatalf("expected no error when auth disabled, got: %v", err)
	}
	if resp != "success" {
		t.Errorf("resp = %v, want success", resp)
	}
}

func TestGRPCInterceptor_NoMetadata(t *testing.T) {
	am, cleanup := authGrpcSetup(t)
	defer cleanup()

	interceptor := am.GRPCUnaryInterceptor()
	_, err := interceptor(
		context.Background(), // no metadata
		nil,
		&grpc.UnaryServerInfo{FullMethod: "/mddb.MDDB/GetDoc"},
		authGrpcDummyHandler,
	)
	if err == nil {
		t.Fatal("expected error for missing metadata")
	}
	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("expected gRPC status error, got: %v", err)
	}
	if st.Code() != codes.Unauthenticated {
		t.Errorf("code = %v, want Unauthenticated", st.Code())
	}
}

func TestGRPCInterceptor_NoAuthHeader(t *testing.T) {
	am, cleanup := authGrpcSetup(t)
	defer cleanup()

	interceptor := am.GRPCUnaryInterceptor()
	ctx := metadata.NewIncomingContext(context.Background(), metadata.MD{})
	_, err := interceptor(
		ctx,
		nil,
		&grpc.UnaryServerInfo{FullMethod: "/mddb.MDDB/GetDoc"},
		authGrpcDummyHandler,
	)
	if err == nil {
		t.Fatal("expected error for missing auth")
	}
	st, _ := status.FromError(err)
	if st.Code() != codes.Unauthenticated {
		t.Errorf("code = %v, want Unauthenticated", st.Code())
	}
}

func TestGRPCInterceptor_ValidBearerToken(t *testing.T) {
	am, cleanup := authGrpcSetup(t)
	defer cleanup()

	token, err := GenerateJWT("admin", true, am.config.JWTSecret, am.config.JWTExpiry)
	if err != nil {
		t.Fatalf("GenerateJWT: %v", err)
	}

	interceptor := am.GRPCUnaryInterceptor()
	md := metadata.Pairs("authorization", "Bearer "+token)
	ctx := metadata.NewIncomingContext(context.Background(), md)

	resp, err := interceptor(
		ctx,
		nil,
		&grpc.UnaryServerInfo{FullMethod: "/mddb.MDDB/GetDoc"},
		authGrpcDummyHandler,
	)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if resp != "success" {
		t.Errorf("resp = %v, want success", resp)
	}
}

func TestGRPCInterceptor_RawToken(t *testing.T) {
	am, cleanup := authGrpcSetup(t)
	defer cleanup()

	token, err := GenerateJWT("admin", true, am.config.JWTSecret, am.config.JWTExpiry)
	if err != nil {
		t.Fatalf("GenerateJWT: %v", err)
	}

	interceptor := am.GRPCUnaryInterceptor()
	// Raw token without "Bearer " prefix
	md := metadata.Pairs("authorization", token)
	ctx := metadata.NewIncomingContext(context.Background(), md)

	resp, err := interceptor(
		ctx,
		nil,
		&grpc.UnaryServerInfo{FullMethod: "/mddb.MDDB/GetDoc"},
		authGrpcDummyHandler,
	)
	if err != nil {
		t.Fatalf("expected no error for raw token, got: %v", err)
	}
	if resp != "success" {
		t.Errorf("resp = %v, want success", resp)
	}
}

func TestGRPCInterceptor_InvalidToken(t *testing.T) {
	am, cleanup := authGrpcSetup(t)
	defer cleanup()

	interceptor := am.GRPCUnaryInterceptor()
	md := metadata.Pairs("authorization", "Bearer invalid-token")
	ctx := metadata.NewIncomingContext(context.Background(), md)

	_, err := interceptor(
		ctx,
		nil,
		&grpc.UnaryServerInfo{FullMethod: "/mddb.MDDB/GetDoc"},
		authGrpcDummyHandler,
	)
	if err == nil {
		t.Fatal("expected error for invalid token")
	}
	st, _ := status.FromError(err)
	if st.Code() != codes.Unauthenticated {
		t.Errorf("code = %v, want Unauthenticated", st.Code())
	}
}

func TestGRPCInterceptor_ExpiredToken(t *testing.T) {
	am, cleanup := authGrpcSetup(t)
	defer cleanup()

	token, err := GenerateJWT("admin", true, am.config.JWTSecret, -1*time.Hour)
	if err != nil {
		t.Fatalf("GenerateJWT: %v", err)
	}

	interceptor := am.GRPCUnaryInterceptor()
	md := metadata.Pairs("authorization", "Bearer "+token)
	ctx := metadata.NewIncomingContext(context.Background(), md)

	_, err = interceptor(
		ctx,
		nil,
		&grpc.UnaryServerInfo{FullMethod: "/mddb.MDDB/GetDoc"},
		authGrpcDummyHandler,
	)
	if err == nil {
		t.Fatal("expected error for expired token")
	}
	st, _ := status.FromError(err)
	if st.Code() != codes.Unauthenticated {
		t.Errorf("code = %v, want Unauthenticated", st.Code())
	}
}

func TestGRPCInterceptor_ValidAPIKey(t *testing.T) {
	am, cleanup := authGrpcSetup(t)
	defer cleanup()

	apiKey, err := am.CreateAPIKey("admin", "test key", 0)
	if err != nil {
		t.Fatalf("CreateAPIKey: %v", err)
	}

	interceptor := am.GRPCUnaryInterceptor()
	md := metadata.Pairs("x-api-key", apiKey)
	ctx := metadata.NewIncomingContext(context.Background(), md)

	resp, err := interceptor(
		ctx,
		nil,
		&grpc.UnaryServerInfo{FullMethod: "/mddb.MDDB/GetDoc"},
		authGrpcDummyHandler,
	)
	if err != nil {
		t.Fatalf("expected no error for valid API key, got: %v", err)
	}
	if resp != "success" {
		t.Errorf("resp = %v, want success", resp)
	}
}

func TestGRPCInterceptor_InvalidAPIKey(t *testing.T) {
	am, cleanup := authGrpcSetup(t)
	defer cleanup()

	interceptor := am.GRPCUnaryInterceptor()
	md := metadata.Pairs("x-api-key", "invalid-api-key")
	ctx := metadata.NewIncomingContext(context.Background(), md)

	_, err := interceptor(
		ctx,
		nil,
		&grpc.UnaryServerInfo{FullMethod: "/mddb.MDDB/GetDoc"},
		authGrpcDummyHandler,
	)
	if err == nil {
		t.Fatal("expected error for invalid API key")
	}
	st, _ := status.FromError(err)
	if st.Code() != codes.Unauthenticated {
		t.Errorf("code = %v, want Unauthenticated", st.Code())
	}
}

func TestGRPCInterceptor_DisabledUser(t *testing.T) {
	am, cleanup := authGrpcSetup(t)
	defer cleanup()

	// Create and disable a user
	_, err := am.CreateUser("disabled-grpc", "pass123")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if err := am.DeleteUser("disabled-grpc"); err != nil {
		t.Fatalf("DeleteUser: %v", err)
	}

	token, err := GenerateJWT("disabled-grpc", false, am.config.JWTSecret, am.config.JWTExpiry)
	if err != nil {
		t.Fatalf("GenerateJWT: %v", err)
	}

	interceptor := am.GRPCUnaryInterceptor()
	md := metadata.Pairs("authorization", "Bearer "+token)
	ctx := metadata.NewIncomingContext(context.Background(), md)

	_, err = interceptor(
		ctx,
		nil,
		&grpc.UnaryServerInfo{FullMethod: "/mddb.MDDB/GetDoc"},
		authGrpcDummyHandler,
	)
	if err == nil {
		t.Fatal("expected error for disabled user")
	}
	st, _ := status.FromError(err)
	if st.Code() != codes.Unauthenticated {
		t.Errorf("code = %v, want Unauthenticated", st.Code())
	}
}

func TestGRPCInterceptor_NonexistentUser(t *testing.T) {
	am, cleanup := authGrpcSetup(t)
	defer cleanup()

	token, err := GenerateJWT("ghost", false, am.config.JWTSecret, am.config.JWTExpiry)
	if err != nil {
		t.Fatalf("GenerateJWT: %v", err)
	}

	interceptor := am.GRPCUnaryInterceptor()
	md := metadata.Pairs("authorization", "Bearer "+token)
	ctx := metadata.NewIncomingContext(context.Background(), md)

	_, err = interceptor(
		ctx,
		nil,
		&grpc.UnaryServerInfo{FullMethod: "/mddb.MDDB/GetDoc"},
		authGrpcDummyHandler,
	)
	if err == nil {
		t.Fatal("expected error for nonexistent user")
	}
	st, _ := status.FromError(err)
	if st.Code() != codes.Unauthenticated {
		t.Errorf("code = %v, want Unauthenticated", st.Code())
	}
}

func TestGRPCInterceptor_WrongSecret(t *testing.T) {
	am, cleanup := authGrpcSetup(t)
	defer cleanup()

	token, err := GenerateJWT("admin", true, "different-secret", am.config.JWTExpiry)
	if err != nil {
		t.Fatalf("GenerateJWT: %v", err)
	}

	interceptor := am.GRPCUnaryInterceptor()
	md := metadata.Pairs("authorization", "Bearer "+token)
	ctx := metadata.NewIncomingContext(context.Background(), md)

	_, err = interceptor(
		ctx,
		nil,
		&grpc.UnaryServerInfo{FullMethod: "/mddb.MDDB/GetDoc"},
		authGrpcDummyHandler,
	)
	if err == nil {
		t.Fatal("expected error for wrong secret")
	}
	st, _ := status.FromError(err)
	if st.Code() != codes.Unauthenticated {
		t.Errorf("code = %v, want Unauthenticated", st.Code())
	}
}

func TestGRPCInterceptor_ContextContainsClaims(t *testing.T) {
	am, cleanup := authGrpcSetup(t)
	defer cleanup()

	token, err := GenerateJWT("admin", true, am.config.JWTSecret, am.config.JWTExpiry)
	if err != nil {
		t.Fatalf("GenerateJWT: %v", err)
	}

	interceptor := am.GRPCUnaryInterceptor()
	md := metadata.Pairs("authorization", "Bearer "+token)
	ctx := metadata.NewIncomingContext(context.Background(), md)

	var capturedClaims *JWTClaims
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		claims, ok := GetClaimsFromContext(ctx)
		if ok {
			capturedClaims = claims
		}
		return "ok", nil
	}

	_, err = interceptor(
		ctx,
		nil,
		&grpc.UnaryServerInfo{FullMethod: "/mddb.MDDB/GetDoc"},
		handler,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if capturedClaims == nil {
		t.Fatal("expected claims in context")
	}
	if capturedClaims.Username != "admin" {
		t.Errorf("Username = %q, want admin", capturedClaims.Username)
	}
	if !capturedClaims.Admin {
		t.Error("expected Admin to be true")
	}
}

func TestGRPCInterceptor_BearerCaseVariations(t *testing.T) {
	am, cleanup := authGrpcSetup(t)
	defer cleanup()

	token, err := GenerateJWT("admin", true, am.config.JWTSecret, am.config.JWTExpiry)
	if err != nil {
		t.Fatalf("GenerateJWT: %v", err)
	}

	interceptor := am.GRPCUnaryInterceptor()

	// Test lowercase "bearer"
	md := metadata.Pairs("authorization", "bearer "+token)
	ctx := metadata.NewIncomingContext(context.Background(), md)

	resp, err := interceptor(
		ctx,
		nil,
		&grpc.UnaryServerInfo{FullMethod: "/mddb.MDDB/GetDoc"},
		authGrpcDummyHandler,
	)
	if err != nil {
		t.Fatalf("expected no error for lowercase bearer, got: %v", err)
	}
	if resp != "success" {
		t.Errorf("resp = %v, want success", resp)
	}
}
