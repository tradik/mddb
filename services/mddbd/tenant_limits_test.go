package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	json "mddb/internal/jsonx"
	proto "mddb/proto"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// GO-054: per-tenant and per-credential request limits, refused the same way
// on every surface.

// limitServer is accessServer with a tenant store and a frozen clock, so a
// rate limit's bucket does not refill between two calls.
func limitServer(t *testing.T) *Server {
	t.Helper()
	s := accessServer(t)
	s.Tenants = NewTenantManager(s.DB)
	if err := s.Tenants.LoadAll(); err != nil {
		t.Fatal(err)
	}
	frozen := time.Unix(1_700_000_000, 0)
	s.tenantLimiter().now = func() time.Time { return frozen }
	return s
}

// setLimits stores a tenant's limits.
func setLimits(t *testing.T, s *Server, tenant string, l TenantLimits) {
	t.Helper()
	if err := s.Tenants.Set(tenant, TenantConfig{Limits: &l}); err != nil {
		t.Fatal(err)
	}
}

// restRec sends one JSON request through the registered routes as c.
func restRec(t *testing.T, s *Server, c *JWTClaims, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	mux := http.NewServeMux()
	s.registerRoutes(mux, true)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	asUser(c, mux).ServeHTTP(rec, req)
	return rec
}

// wantTenantLimit requires a REST 429 carrying the limit.
func wantTenantLimit(t *testing.T, rec *httptest.ResponseRecorder, kind string) {
	t.Helper()
	if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") != "1" {
		t.Fatalf("status %d, Retry-After %q: %s", rec.Code, rec.Header().Get("Retry-After"), rec.Body.String())
	}
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body["code"] != "TENANT_LIMIT" || body["limit"] != kind {
		t.Fatalf("body = %v, want limit %s", body, kind)
	}
}

func TestTenantWriteRateOnEverySurface(t *testing.T) {
	addBody := `{"collection":"t1/notes","key":"k","lang":"en","contentMd":"x"}`
	cases := map[string]func(t *testing.T, s *Server){
		"REST": func(t *testing.T, s *Server) {
			if rec := restRec(t, s, tina, http.MethodPost, "/v1/add", addBody); rec.Code != http.StatusOK {
				t.Fatalf("first write: %d %s", rec.Code, rec.Body.String())
			}
			wantTenantLimit(t, restRec(t, s, tina, http.MethodPost, "/v1/add", addBody), limitWriteRate)
		},
		"gRPC": func(t *testing.T, s *Server) {
			client := grpcClient(t, s)
			req := &proto.AddRequest{Collection: "t1/notes", Key: "k", Lang: "en", ContentMd: "x"}
			if _, err := client.Add(grpcAs(t, tina), req); err != nil {
				t.Fatal(err)
			}
			_, err := client.Add(grpcAs(t, tina), req)
			if status.Code(err) != codes.ResourceExhausted || !strings.Contains(err.Error(), "writeRate") {
				t.Fatalf("second write: %v", err)
			}
		},
		"MCP": func(t *testing.T, s *Server) {
			args := map[string]any{"collection": "t1/notes", "key": "k", "lang": "en", "content_md": "x"}
			mcpCallAs(s, tina, "add_document", args)
			res, _ := mcpCallAs(s, tina, "add_document", args)["result"].(map[string]any)
			detail, _ := res["structuredContent"].(map[string]any)
			if res["isError"] != true || detail["code"] != "TENANT_LIMIT" || detail["status"] != http.StatusTooManyRequests {
				t.Fatalf("MCP result = %v", res)
			}
		},
		"GraphQL": func(t *testing.T, s *Server) {
			q := `mutation { addDocument(input:{collection:"t1/notes", key:"k", lang:"en", contentMd:"x"}) { key } }`
			gqlCall(t, s, tina, q)
			out := gqlCall(t, s, tina, q)
			if !strings.Contains(out, `"code":"TENANT_LIMIT"`) || !strings.Contains(out, `"status":429`) {
				t.Fatalf("GraphQL = %s", out)
			}
		},
	}
	for name, run := range cases {
		t.Run(name, func(t *testing.T) {
			s := limitServer(t)
			setLimits(t, s, "t1", TenantLimits{WritePerSecond: 1})
			run(t, s)
			// Reads have their own allowance, and another tenant is untouched.
			if rec := restRec(t, s, tina, http.MethodPost, "/v1/fts", `{"collection":"t1/notes","query":"x"}`); rec.Code == http.StatusTooManyRequests {
				t.Fatalf("a read was refused by the write limit: %s", rec.Body.String())
			}
			if rec := restRec(t, s, nobby, http.MethodPost, "/v1/add", strings.ReplaceAll(addBody, "t1/", "t2/")); rec.Code != http.StatusOK {
				t.Fatalf("tenant t2 was limited by t1's limit: %d", rec.Code)
			}
			if got := s.tenantRefusals("t1")["tenant.writeRate"]; got != 1 {
				t.Fatalf("refusals = %v", s.tenantRefusals("t1"))
			}
		})
	}
}

func TestKeyRateIsPerCredential(t *testing.T) {
	t.Setenv("MDDB_KEY_READ_RPS", "1")
	s := limitServer(t)
	body := `{"collection":"t1/notes","query":"x"}`
	if rec := restRec(t, s, tina, http.MethodPost, "/v1/fts", body); rec.Code != http.StatusOK {
		t.Fatalf("first read: %d %s", rec.Code, rec.Body.String())
	}
	rec := restRec(t, s, tina, http.MethodPost, "/v1/fts", body)
	wantTenantLimit(t, rec, limitReadRate)
	if !strings.Contains(rec.Body.String(), `"scope":"key"`) {
		t.Fatalf("not a credential limit: %s", rec.Body.String())
	}
	// A global user's credential is limited too, once the operator sets it.
	if rec := restRec(t, s, alice, http.MethodPost, "/v1/fts", `{"collection":"docs","query":"x"}`); rec.Code != http.StatusOK {
		t.Fatalf("alice's first read: %d", rec.Code)
	}
	// An API key is its own credential: a fresh key of tina's has its own allowance.
	ctx := withAPIKeyID(context.WithValue(context.Background(), authContextKey, tina), "mddb_live_other")
	if _, release, err := s.admitRequest(ctx, PermRead); err != nil {
		t.Fatalf("a second key shared the first one's allowance: %v", err)
	} else {
		release()
	}
	// A tenant override of -1 lifts the default.
	setLimits(t, s, "t1", TenantLimits{KeyReadPerSecond: -1})
	if rec := restRec(t, s, tina, http.MethodPost, "/v1/fts", body); rec.Code != http.StatusOK {
		t.Fatalf("-1 did not lift the default: %d", rec.Code)
	}
}

func TestTenantReadRateAndAdmittedOnce(t *testing.T) {
	s := limitServer(t)
	setLimits(t, s, "t1", TenantLimits{ReadPerSecond: 1})
	ctx := context.WithValue(context.Background(), authContextKey, tina)
	ctx, release, err := s.admitRequest(ctx, PermRead)
	if err != nil {
		t.Fatal(err)
	}
	release()
	// Admitted already: a second gate on the same request does not count.
	if _, _, err := s.admitRequest(ctx, PermRead); err != nil {
		t.Fatalf("the same request was counted twice: %v", err)
	}
	_, _, err = s.admitRequest(context.WithValue(context.Background(), authContextKey, tina), PermRead)
	if te := asTenantLimitError(err); te == nil || te.Kind != limitReadRate || te.Scope != "tenant" {
		t.Fatalf("second request: %v", err)
	}
	if !strings.Contains(err.Error(), `tenant "t1"`) {
		t.Fatalf("message = %v", err)
	}
	// No claims (auth off, or stdio MCP): nothing to limit.
	if _, _, err := s.admitRequest(context.Background(), PermWrite); err != nil {
		t.Fatal(err)
	}
}

func TestTenantWriteConcurrency(t *testing.T) {
	t.Setenv("MDDB_TENANT_QUEUE_TIMEOUT_MS", "20")
	s := limitServer(t)
	setLimits(t, s, "t1", TenantLimits{MaxConcurrentWrites: 1})
	ctx := context.WithValue(context.Background(), authContextKey, tina)
	_, release, err := s.admitRequest(ctx, PermWrite)
	if err != nil {
		t.Fatal(err)
	}
	if n := s.tenantUsageReport("t1").InFlight["writes"]; n != 1 {
		t.Fatalf("in flight = %d", n)
	}
	rec := restRec(t, s, tina, http.MethodPost, "/v1/add", `{"collection":"t1/n","key":"k","lang":"en","contentMd":"x"}`)
	wantTenantLimit(t, rec, limitWriteConcurrency)
	// The client giving up is not a refusal.
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, _, err := s.admitRequest(cancelled, PermWrite); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled: %v", err)
	}
	release()
	if _, release, err := s.admitRequest(ctx, PermWrite); err != nil {
		t.Fatalf("after release: %v", err)
	} else {
		release()
	}
}

func TestTenantSearchConcurrency(t *testing.T) {
	t.Setenv("MDDB_TENANT_QUEUE_TIMEOUT_MS", "20")
	s := limitServer(t)
	setLimits(t, s, "t1", TenantLimits{MaxConcurrentSearches: 1})
	ctx := context.WithValue(context.Background(), authContextKey, tina)

	held, release, err := s.searchSlot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// The same request passing the gate again holds one slot, not two.
	if _, again, err := s.searchSlot(held); err != nil {
		t.Fatal(err)
	} else {
		again()
	}
	_, _, err = s.searchSlot(ctx)
	if te := asTenantLimitError(err); te == nil || te.Kind != limitSearchConcurrency {
		t.Fatalf("second search: %v", err)
	}
	// Over REST the refusal is a 429, not the server's 503.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/search", nil).WithContext(ctx)
	s.withSearchLimit(func(http.ResponseWriter, *http.Request) { t.Fatal("ran past the limit") })(rec, req)
	wantTenantLimit(t, rec, limitSearchConcurrency)
	// Another tenant searches while t1 is at its limit.
	if _, rel, err := s.searchSlot(context.WithValue(context.Background(), authContextKey, nobby)); err != nil {
		t.Fatalf("t2 refused: %v", err)
	} else {
		rel()
	}
	release()

	// Without the server limiter the tenant's slot alone is held.
	s.SearchLimiter = nil
	held, release, err = s.searchSlot(ctx)
	if err != nil || held.Value(searchSlotKey{}) == nil {
		t.Fatalf("tenant slot without a server limiter: %v", err)
	}
	release()
}

func TestTenantSearchSlotReleasedWhenServerIsBusy(t *testing.T) {
	t.Setenv("MDDB_SEARCH_MAX_CONCURRENT", "1")
	t.Setenv("MDDB_SEARCH_QUEUE_TIMEOUT_MS", "10")
	s := limitServer(t)
	s.SearchLimiter = newSearchLimiter()
	setLimits(t, s, "t1", TenantLimits{MaxConcurrentSearches: 1})
	_, hold, err := s.searchSlot(context.Background()) // a global user takes the only server slot
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.WithValue(context.Background(), authContextKey, tina)
	if _, _, err := s.searchSlot(ctx); !errors.Is(err, ErrSearchBusy) {
		t.Fatalf("want the server's refusal, got %v", err)
	}
	if n := s.tenantLimiter().inUse("s|t1"); n != 0 {
		t.Fatalf("the tenant slot leaked: %d in use", n)
	}
	hold()
}

func TestTenantLimitErrorShapes(t *testing.T) {
	te := &TenantLimitError{Scope: "key", Kind: limitReadRate, Limit: 5}
	if !strings.Contains(te.Error(), "this credential") || te.retryAfterSeconds() != 1 {
		t.Fatalf("error = %q", te.Error())
	}
	if tenantLimitDetail(errors.New("x")) != nil || writeTenantLimit(httptest.NewRecorder(), errors.New("x")) {
		t.Fatal("an unrelated error was taken for a tenant limit")
	}
	if mcpQuotaContent(te)["code"] != "TENANT_LIMIT" {
		t.Fatal("MCP detail missing")
	}
	if status.Code(grpcError(codes.Internal, te)) != codes.ResourceExhausted {
		t.Fatal("gRPC code")
	}
	rec := httptest.NewRecorder()
	bad(rec, te)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("bad() = %d", rec.Code)
	}
}

func TestAPIKeyIdentityReachesTheContext(t *testing.T) {
	s := limitServer(t)
	key, err := s.AuthManager.CreateAPIKey("tina", "k", 0)
	if err != nil {
		t.Fatal(err)
	}
	var seen string
	h := s.AuthManager.HTTPMiddleware(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		claims, _ := GetClaimsFromContext(r.Context())
		seen = credentialOf(r.Context(), claims)
	}))
	req := httptest.NewRequest(http.MethodGet, "/v1/stats", nil)
	req.Header.Set("X-API-Key", key)
	h.ServeHTTP(httptest.NewRecorder(), req)
	if !strings.HasPrefix(seen, "key:") {
		t.Fatalf("credential = %q, want the API key", seen)
	}
	md := metadata.New(map[string]string{"x-api-key": key})
	ctx, err := s.AuthManager.authenticateContext(metadata.NewIncomingContext(context.Background(), md))
	if err != nil {
		t.Fatal(err)
	}
	claims, _ := GetClaimsFromContext(ctx)
	if got := credentialOf(ctx, claims); !strings.HasPrefix(got, "key:") {
		t.Fatalf("gRPC credential = %q", got)
	}
}
