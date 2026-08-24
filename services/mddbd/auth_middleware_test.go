package main

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	bolt "go.etcd.io/bbolt"
)

// authMwSetup creates an AuthManager with a test DB for middleware tests.
func authMwSetup(t *testing.T) (*AuthManager, func()) {
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
		JWTSecret:     "mw-test-secret-key-12345",
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

// authMwDummyHandler returns a simple handler that responds 200 OK.
func authMwDummyHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})
}

func TestAuthMiddleware_Disabled(t *testing.T) {
	am, cleanup := authMwSetup(t)
	defer cleanup()

	// Disable auth
	am.enabled = false

	handler := am.HTTPMiddleware(authMwDummyHandler())

	req := httptest.NewRequest("GET", "/v1/docs/test", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200 when auth disabled, got %d", w.Code)
	}
}

func TestAuthMiddleware_PublicEndpoints(t *testing.T) {
	am, cleanup := authMwSetup(t)
	defer cleanup()

	handler := am.HTTPMiddleware(authMwDummyHandler())

	// /metrics is intentionally NOT in this always-public list anymore — its
	// auth gating is opt-in via MDDB_METRICS_PUBLIC and is covered by
	// TestHTTPMiddleware_MetricsPrivateByDefault / _MetricsPublicOptIn (SEC-009).
	endpoints := []string{"/health", "/v1/health", "/v1/auth/login"}
	for _, ep := range endpoints {
		req := httptest.NewRequest("GET", ep, nil)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("endpoint %q: expected 200, got %d", ep, w.Code)
		}
	}
}

func TestAuthMiddleware_NoAuth(t *testing.T) {
	am, cleanup := authMwSetup(t)
	defer cleanup()

	handler := am.HTTPMiddleware(authMwDummyHandler())

	req := httptest.NewRequest("GET", "/v1/docs/test", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 with no auth, got %d: %s", w.Code, w.Body.String())
	}
}

func TestAuthMiddleware_ValidBearerToken(t *testing.T) {
	am, cleanup := authMwSetup(t)
	defer cleanup()

	token, err := GenerateJWT("admin", true, am.config.JWTSecret, am.config.JWTExpiry)
	if err != nil {
		t.Fatalf("GenerateJWT: %v", err)
	}

	handler := am.HTTPMiddleware(authMwDummyHandler())

	req := httptest.NewRequest("GET", "/v1/docs/test", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestAuthMiddleware_InvalidToken(t *testing.T) {
	am, cleanup := authMwSetup(t)
	defer cleanup()

	handler := am.HTTPMiddleware(authMwDummyHandler())

	req := httptest.NewRequest("GET", "/v1/docs/test", nil)
	req.Header.Set("Authorization", "Bearer invalid-token-value")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 for invalid token, got %d", w.Code)
	}
}

func TestAuthMiddleware_ExpiredToken(t *testing.T) {
	am, cleanup := authMwSetup(t)
	defer cleanup()

	// Generate an already-expired token
	token, err := GenerateJWT("admin", true, am.config.JWTSecret, -1*time.Hour)
	if err != nil {
		t.Fatalf("GenerateJWT: %v", err)
	}

	handler := am.HTTPMiddleware(authMwDummyHandler())

	req := httptest.NewRequest("GET", "/v1/docs/test", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 for expired token, got %d", w.Code)
	}
}

func TestAuthMiddleware_ValidAPIKey(t *testing.T) {
	am, cleanup := authMwSetup(t)
	defer cleanup()

	// Create an API key for admin
	apiKey, err := am.CreateAPIKey("admin", "test key", 0)
	if err != nil {
		t.Fatalf("CreateAPIKey: %v", err)
	}

	handler := am.HTTPMiddleware(authMwDummyHandler())

	req := httptest.NewRequest("GET", "/v1/docs/test", nil)
	req.Header.Set("X-API-Key", apiKey)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200 with valid API key, got %d: %s", w.Code, w.Body.String())
	}
}

func TestAuthMiddleware_InvalidAPIKey(t *testing.T) {
	am, cleanup := authMwSetup(t)
	defer cleanup()

	handler := am.HTTPMiddleware(authMwDummyHandler())

	req := httptest.NewRequest("GET", "/v1/docs/test", nil)
	req.Header.Set("X-API-Key", "invalid-api-key")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 for invalid API key, got %d", w.Code)
	}
}

func TestAuthMiddleware_DisabledUser(t *testing.T) {
	am, cleanup := authMwSetup(t)
	defer cleanup()

	// Create and disable a user
	_, err := am.CreateUser("disabled-user", "pass123")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if err := am.DeleteUser("disabled-user"); err != nil {
		t.Fatalf("DeleteUser: %v", err)
	}

	token, err := GenerateJWT("disabled-user", false, am.config.JWTSecret, am.config.JWTExpiry)
	if err != nil {
		t.Fatalf("GenerateJWT: %v", err)
	}

	handler := am.HTTPMiddleware(authMwDummyHandler())

	req := httptest.NewRequest("GET", "/v1/docs/test", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 for disabled user, got %d", w.Code)
	}
}

func TestAuthMiddleware_NonexistentUser(t *testing.T) {
	am, cleanup := authMwSetup(t)
	defer cleanup()

	// Token for a user that doesn't exist in the database
	token, err := GenerateJWT("ghost", false, am.config.JWTSecret, am.config.JWTExpiry)
	if err != nil {
		t.Fatalf("GenerateJWT: %v", err)
	}

	handler := am.HTTPMiddleware(authMwDummyHandler())

	req := httptest.NewRequest("GET", "/v1/docs/test", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 for nonexistent user, got %d", w.Code)
	}
}

func TestAuthMiddleware_WrongSecret(t *testing.T) {
	am, cleanup := authMwSetup(t)
	defer cleanup()

	// Token signed with different secret
	token, err := GenerateJWT("admin", true, "different-secret-key", am.config.JWTExpiry)
	if err != nil {
		t.Fatalf("GenerateJWT: %v", err)
	}

	handler := am.HTTPMiddleware(authMwDummyHandler())

	req := httptest.NewRequest("GET", "/v1/docs/test", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 for wrong secret, got %d", w.Code)
	}
}

func TestAuthMiddleware_MalformedAuthHeader(t *testing.T) {
	am, cleanup := authMwSetup(t)
	defer cleanup()

	handler := am.HTTPMiddleware(authMwDummyHandler())

	tests := []struct {
		name string
		auth string
	}{
		{"just-token", "some-token-without-bearer"},
		{"empty-bearer", "Bearer "},
		{"basic-auth", "Basic dXNlcjpwYXNz"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/v1/docs/test", nil)
			req.Header.Set("Authorization", tt.auth)
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, req)

			if w.Code != http.StatusUnauthorized {
				t.Errorf("expected 401, got %d", w.Code)
			}
		})
	}
}

func TestAuthMiddleware_ContextContainsClaims(t *testing.T) {
	am, cleanup := authMwSetup(t)
	defer cleanup()

	token, err := GenerateJWT("admin", true, am.config.JWTSecret, am.config.JWTExpiry)
	if err != nil {
		t.Fatalf("GenerateJWT: %v", err)
	}

	var capturedClaims *JWTClaims
	handler := am.HTTPMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims, ok := GetClaimsFromContext(r.Context())
		if ok {
			capturedClaims = claims
		}
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/v1/docs/test", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

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

// ---- Helper function tests ----

func TestExtractTokenFromRequest_Bearer(t *testing.T) {
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Bearer my-token-123")
	token := extractTokenFromRequest(req)
	if token != "my-token-123" {
		t.Errorf("got %q, want my-token-123", token)
	}
}

func TestExtractTokenFromRequest_BearerCaseInsensitive(t *testing.T) {
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "bearer my-token-123")
	token := extractTokenFromRequest(req)
	if token != "my-token-123" {
		t.Errorf("got %q, want my-token-123", token)
	}
}

func TestExtractTokenFromRequest_Empty(t *testing.T) {
	req := httptest.NewRequest("GET", "/", nil)
	token := extractTokenFromRequest(req)
	if token != "" {
		t.Errorf("got %q, want empty", token)
	}
}

func TestExtractTokenFromRequest_NoBearerPrefix(t *testing.T) {
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "just-a-token")
	token := extractTokenFromRequest(req)
	if token != "" {
		t.Errorf("got %q, want empty (no bearer prefix)", token)
	}
}

func TestExtractTokenFromRequest_BasicAuth(t *testing.T) {
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Basic dXNlcjpwYXNz")
	token := extractTokenFromRequest(req)
	if token != "" {
		t.Errorf("got %q, want empty (Basic auth)", token)
	}
}

// TestBuildPublicEndpoints covers SEC-009: /metrics is only auth-exempt when
// MDDB_METRICS_PUBLIC=true; the liveness/login endpoints are always exempt.
func TestBuildPublicEndpoints(t *testing.T) {
	t.Setenv("MDDB_METRICS_PUBLIC", "") // default: metrics private
	eps := buildPublicEndpoints()
	for _, p := range []string{"/health", "/v1/health", "/v1/auth/login"} {
		if !eps[p] {
			t.Errorf("expected %q to be public by default", p)
		}
	}
	if eps["/metrics"] {
		t.Error("/metrics must NOT be public when MDDB_METRICS_PUBLIC is unset (SEC-009)")
	}

	t.Setenv("MDDB_METRICS_PUBLIC", "true")
	if !buildPublicEndpoints()["/metrics"] {
		t.Error("/metrics must be public when MDDB_METRICS_PUBLIC=true (opt-in)")
	}

	t.Setenv("MDDB_METRICS_PUBLIC", "1")
	if buildPublicEndpoints()["/metrics"] {
		t.Error(`only the exact value "true" should make /metrics public`)
	}
}

func TestIsPublicEndpoint(t *testing.T) {
	t.Setenv("MDDB_METRICS_PUBLIC", "") // default: metrics private
	am := &AuthManager{publicEndpoints: buildPublicEndpoints()}
	tests := []struct {
		path   string
		public bool
	}{
		{"/health", true},
		{"/v1/health", true},
		{"/v1/auth/login", true},
		{"/metrics", false}, // SEC-009: gated unless explicitly opted in
		{"/v1/docs/test", false},
		{"/v1/collections", false},
		{"/v1/auth/register", false},
		{"/unknown", false},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			got := am.isPublicEndpoint(tt.path)
			if got != tt.public {
				t.Errorf("isPublicEndpoint(%q) = %v, want %v", tt.path, got, tt.public)
			}
		})
	}

	// Opt-in flips /metrics public.
	t.Setenv("MDDB_METRICS_PUBLIC", "true")
	amPub := &AuthManager{publicEndpoints: buildPublicEndpoints()}
	if !amPub.isPublicEndpoint("/metrics") {
		t.Error("/metrics should be public with MDDB_METRICS_PUBLIC=true")
	}

	// Fail closed: a manager built without buildPublicEndpoints (nil set)
	// must treat every path as private.
	var amNil AuthManager
	if amNil.isPublicEndpoint("/health") {
		t.Error("nil publicEndpoints must fail closed (nothing public)")
	}
}

// metricsProbe builds the middleware around a sentinel that records whether a
// request reached the backend, then issues an unauthenticated GET /metrics.
func metricsProbe(t *testing.T) (code int, reached bool) {
	t.Helper()
	am, cleanup := authMwSetup(t)
	defer cleanup()

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
		w.WriteHeader(http.StatusOK)
	})
	h := am.HTTPMiddleware(next)

	req := httptest.NewRequest("GET", "/metrics", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Code, reached
}

// TestHTTPMiddleware_MetricsPrivateByDefault — SEC-009: with auth on and no
// opt-in, anonymous /metrics is rejected before reaching the handler.
func TestHTTPMiddleware_MetricsPrivateByDefault(t *testing.T) {
	t.Setenv("MDDB_METRICS_PUBLIC", "")
	code, reached := metricsProbe(t)
	if code != http.StatusUnauthorized {
		t.Errorf("GET /metrics without creds: got %d, want 401", code)
	}
	if reached {
		t.Error("request reached backend — /metrics must be gated when auth is enabled")
	}
}

// TestHTTPMiddleware_MetricsPublicOptIn — MDDB_METRICS_PUBLIC=true restores the
// previous behaviour: anonymous /metrics passes straight through.
func TestHTTPMiddleware_MetricsPublicOptIn(t *testing.T) {
	t.Setenv("MDDB_METRICS_PUBLIC", "true")
	code, reached := metricsProbe(t)
	if code != http.StatusOK || !reached {
		t.Errorf("GET /metrics with opt-in: got code=%d reached=%v, want 200 true", code, reached)
	}
}

// #212: an API key sent as `Authorization: Bearer` used to be parsed as a JWT
// and refused with "invalid token" — a message about the credential when the
// problem was the header. The MCP middleware next door has always accepted the
// key in either place, so two surfaces of one server disagreed and clients met
// the stricter one first.
func TestAuthMiddleware_APIKeyInEitherHeader(t *testing.T) {
	am, cleanup := authMwSetup(t)
	defer cleanup()

	key, err := am.CreateAPIKey("admin", "test key", 0)
	if err != nil {
		t.Fatalf("CreateAPIKey: %v", err)
	}
	if !strings.HasPrefix(key, APIKeyPrefix) {
		t.Fatalf("generated key %q does not carry the prefix the middleware keys on", key)
	}

	for _, tc := range []struct {
		name   string
		header string
		value  string
	}{
		{"X-API-Key", "X-API-Key", key},
		{"Authorization: Bearer", "Authorization", "Bearer " + key},
	} {
		t.Run(tc.name, func(t *testing.T) {
			handler := am.HTTPMiddleware(authMwDummyHandler())
			req := httptest.NewRequest("GET", "/v1/docs/test", nil)
			req.Header.Set(tc.header, tc.value)
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, req)

			if w.Code != http.StatusOK {
				t.Errorf("expected 200, got %d: %s", w.Code, w.Body.String())
			}
		})
	}
}

// The prefix decides between a key and a JWT, so neither can be mistaken for
// the other. A bearer value that is not a key must still take the JWT path and
// be refused on its own terms.
func TestAuthMiddleware_BearerWithoutKeyPrefixIsStillAJWT(t *testing.T) {
	am, cleanup := authMwSetup(t)
	defer cleanup()

	handler := am.HTTPMiddleware(authMwDummyHandler())
	req := httptest.NewRequest("GET", "/v1/docs/test", nil)
	req.Header.Set("Authorization", "Bearer not-a-jwt-and-not-a-key")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "invalid token") {
		t.Errorf("a non-key bearer value should be refused as a token, got %s", w.Body.String())
	}
}

// An invalid API key must be refused as a key, whichever header carried it —
// the routing decision is the prefix, and it must not change what happens next.
func TestAuthMiddleware_InvalidAPIKeyInAuthorizationHeader(t *testing.T) {
	am, cleanup := authMwSetup(t)
	defer cleanup()

	handler := am.HTTPMiddleware(authMwDummyHandler())
	req := httptest.NewRequest("GET", "/v1/docs/test", nil)
	req.Header.Set("Authorization", "Bearer "+APIKeyPrefix+"0000000000000000")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "invalid api key") {
		t.Errorf("expected the key error, not the token error, got %s", w.Body.String())
	}
}
