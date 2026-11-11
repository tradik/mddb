package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"mddb/proto"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// GO-049: the one implementation of the switches on REST, gRPC and MCP, and
// the answers each surface gives.

// featureServer is accessServer (auth on: root, alice) with the switch store.
func featureServer(t *testing.T) *Server {
	t.Helper()
	t.Setenv(featuresForceOffEnv, "temporal")
	s := accessServer(t)
	s.Features = NewFeatureManager(s.DB)
	if err := s.Features.LoadAll(); err != nil {
		t.Fatal(err)
	}
	return s
}

// featureREST sends one request as c through the registered routes.
func featureREST(t *testing.T, s *Server, c *JWTClaims, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	mux := http.NewServeMux()
	s.registerRoutes(mux, true)
	rec := httptest.NewRecorder()
	h := http.Handler(mux)
	if c != nil {
		h = asUser(c, mux)
	}
	h.ServeHTTP(rec, httptest.NewRequest(method, target, strings.NewReader(body)))
	return rec
}

func TestFeaturesREST(t *testing.T) {
	s := featureServer(t)
	rec := featureREST(t, s, alice, http.MethodGet, "/v1/features", "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"name":"chatExternalMcp"`) ||
		!strings.Contains(rec.Body.String(), `"lockedBy":"MDDB_FEATURES_FORCE_OFF"`) || !strings.HasSuffix(rec.Body.String(), `"version":0}`) {
		t.Fatalf("list as any user: %d %s", rec.Code, rec.Body.String())
	}
	for _, c := range []struct {
		who          *JWTClaims
		method, path string
		body         string
		code         int
		want         string
	}{
		{alice, http.MethodPut, "/v1/features/geo", `{"enabled":false}`, http.StatusForbidden, "admin access required"},
		{nil, http.MethodGet, "/v1/features", "", http.StatusUnauthorized, "unauthorized"},
		{root, http.MethodPut, "/v1/features/geo", `{}`, http.StatusBadRequest, "enabled"},
		{root, http.MethodPut, "/v1/features/geo", `nope`, http.StatusBadRequest, "enabled"},
		{root, http.MethodPut, "/v1/features/nope", `{"enabled":true}`, http.StatusNotFound, `{"error":"unknown feature","feature":"nope"}`},
		{root, http.MethodPut, "/v1/features/temporal", `{"enabled":true}`, http.StatusConflict, `"lockedBy":"MDDB_FEATURES_FORCE_OFF"`},
		{root, http.MethodDelete, "/v1/features/geo", "", http.StatusMethodNotAllowed, "method not allowed"},
		{root, http.MethodGet, "/v1/features/geo", "", http.StatusMethodNotAllowed, "method not allowed"},
		{root, http.MethodPut, "/v1/features/geo", `{"enabled":false}`, http.StatusOK, `"updatedBy":"root"`},
		{root, http.MethodPost, "/v1/geo-encode", `{"lat":1,"lng":1}`, http.StatusNotFound, `{"error":"feature disabled","feature":"geo"}`},
		{nil, http.MethodPost, "/v1/geo-encode", `{"lat":1,"lng":1}`, http.StatusUnauthorized, "unauthorized"}, // auth before the switch
		{root, http.MethodPut, "/v1/features/geo", `{"enabled":true}`, http.StatusOK, `"version":2`},
		{root, http.MethodPost, "/v1/geo-encode", `{"lat":1,"lng":1}`, http.StatusOK, "geohash"},
	} {
		rec := featureREST(t, s, c.who, c.method, c.path, c.body)
		if rec.Code != c.code || !strings.Contains(rec.Body.String(), c.want) {
			t.Errorf("%s %s: %d %s, want %d %q", c.method, c.path, rec.Code, rec.Body.String(), c.code, c.want)
		}
	}
	// A read-only server takes no switch.
	s.Mode = ModeRead
	if rec := featureREST(t, s, root, http.MethodPut, "/v1/features/geo", `{"enabled":false}`); rec.Code != http.StatusForbidden {
		t.Errorf("read-only: %d", rec.Code)
	}
}

func TestFeatureErrorsAndUnloadedStore(t *testing.T) {
	s := &Server{}
	if _, err := s.SetFeature(context.Background(), FeatureGeo, false); err != errFeatureUnavailable {
		t.Errorf("no store: %v", err)
	}
	if got := s.FeaturesList(); len(got.Features) != len(featureRegistry) || got.Version != 0 {
		t.Errorf("no store lists the defaults: %+v", got)
	}
	for err, code := range map[error]int{errFeatureUnavailable: 503, context.Canceled: 500} {
		rec := httptest.NewRecorder()
		featureError(rec, "geo", err)
		if rec.Code != code {
			t.Errorf("%v: %d", err, rec.Code)
		}
	}
	for err, code := range map[error]codes.Code{errFeatureUnknown: codes.NotFound, errFeatureLocked: codes.FailedPrecondition,
		errFeatureUnavailable: codes.Unavailable, context.Canceled: codes.Internal} {
		if got := status.Code(featureGRPCError(err)); got != code {
			t.Errorf("%v: %v", err, got)
		}
	}
}

func TestFeaturesGRPC(t *testing.T) {
	s := featureServer(t)
	g := NewGRPCServer(s)
	list, err := g.ListFeatures(asClaims(alice), &proto.ListFeaturesRequest{})
	if err != nil || len(list.GetFeatures()) != len(featureRegistry) || list.GetFeatures()[1].GetDefaultEnabled() || list.GetFeatures()[1].GetRequires()[0] != FeatureChat {
		t.Fatalf("list: %+v, %v", list, err)
	}
	if _, err := g.SetFeature(asClaims(alice), &proto.SetFeatureRequest{Name: "geo"}); status.Code(err) != codes.PermissionDenied {
		t.Errorf("non-admin: %v", err)
	}
	if _, err := g.SetFeature(asClaims(root), &proto.SetFeatureRequest{Name: "temporal", Enabled: true}); status.Code(err) != codes.FailedPrecondition {
		t.Errorf("locked: %v", err)
	}
	set, err := g.SetFeature(asClaims(root), &proto.SetFeatureRequest{Name: "geo", Enabled: false})
	if err != nil || set.GetFeature().GetEnabled() || set.GetVersion() != 1 || set.GetFeature().GetUpdatedAt() == 0 || set.GetFeature().GetUpdatedBy() != "root" {
		t.Fatalf("set: %+v, %v", set, err)
	}
}

func TestFeaturesMCP(t *testing.T) {
	s := featureServer(t)
	ts := &MCPToolServer{client: NewDirectClient(s), globalMode: ModeRW}
	out, err := ts.mcpCallTool(asClaims(alice), "feature_list", nil)
	if err != nil || !strings.Contains(out, `"name": "images"`) {
		t.Fatalf("list: %s, %v", out, err)
	}
	if _, err := ts.mcpCallTool(asClaims(alice), "feature_set", map[string]interface{}{"name": "geo", "enabled": false}); err == nil {
		t.Error("non-admin set")
	}
	for _, args := range []map[string]interface{}{{"name": "geo"}, {"enabled": true}, {"name": "geo", "enabled": "no"}} {
		if _, err := ts.mcpCallTool(asClaims(root), "feature_set", args); err == nil || !strings.Contains(err.Error(), "required") {
			t.Errorf("%v: %v", args, err)
		}
	}
	if _, err := ts.mcpCallTool(asClaims(root), "feature_set", map[string]interface{}{"name": "nope", "enabled": true}); err != errFeatureUnknown {
		t.Errorf("unknown: %v", err)
	}
	out, err = ts.mcpCallTool(asClaims(root), "feature_set", map[string]interface{}{"name": "eval", "enabled": false})
	if err != nil || !strings.Contains(out, `"version": 1`) {
		t.Fatalf("set: %s, %v", out, err)
	}
	remote := &MCPToolServer{client: covBNewAllErr(), globalMode: ModeRW}
	for _, tool := range []string{"feature_list", "feature_set"} {
		if _, err := remote.mcpCallTool(context.Background(), tool, map[string]interface{}{"name": "geo", "enabled": true}); err != errFeatureToolsDirect {
			t.Errorf("%s without a server: %v", tool, err)
		}
	}
	if _, err := (&MCPToolServer{client: &DirectClient{}}).featureServer(); err != errFeatureToolsDirect {
		t.Errorf("a direct client without a server: %v", err)
	}
}

// grpcCall runs one method through the feature interceptors.
func grpcCall(s *Server, method string) (error, error) {
	unary := func(context.Context, any) (any, error) { return "ok", nil }
	_, uerr := s.featureUnaryInterceptor()(context.Background(), nil, &grpc.UnaryServerInfo{FullMethod: method}, unary)
	stream := func(any, grpc.ServerStream) error { return nil }
	serr := s.featureStreamInterceptor()(nil, nil, &grpc.StreamServerInfo{FullMethod: method}, stream)
	return uerr, serr
}
