package main

import (
	"context"
	"testing"
	"time"

	gql "mddb/graphql"
)

// TEST-002. The GraphQL adapter is where authorisation is decided for every
// GraphQL query, and where MCP types are translated into what a client sees.
// Both halves fail quietly when wrong: a permission check that returns nil
// grants access, and a conversion that drops a field looks like missing data.

func gqlAdapter(t *testing.T) (*GraphQLAdapter, *Server, func()) {
	t.Helper()
	srv, cleanup := newTestServerForBatch(t)
	srv.CollectionManager = NewCollectionManager(srv.DB)
	if err := srv.CollectionManager.EnsureBucket(); err != nil {
		cleanup()
		t.Fatal(err)
	}
	return &GraphQLAdapter{server: srv, mcp: NewDirectClient(srv)}, srv, cleanup
}

func withAuth(t *testing.T, srv *Server) {
	t.Helper()
	srv.AuthManager = NewAuthManager(srv.DB, AuthConfig{
		JWTSecret: "test-secret-do-not-ship", JWTExpiry: time.Hour,
		AdminUsername: "admin", AdminPassword: "secret",
	})
	for _, step := range []func() error{
		srv.AuthManager.EnsureBuckets,
		srv.AuthManager.LoadAll,
		srv.AuthManager.BootstrapAdmin,
	} {
		if err := step(); err != nil {
			t.Fatal(err)
		}
	}
}

// With auth off, every request is authorised — that is the documented single-user
// mode, and it must not accidentally depend on claims being present.
func TestPermissionChecksPassWhenAuthIsDisabled(t *testing.T) {
	adapter, _, cleanup := gqlAdapter(t)
	defer cleanup()

	if err := adapter.CheckPermission(context.Background(), "docs", int(PermWrite)); err != nil {
		t.Errorf("a write was refused with auth disabled: %v", err)
	}
	if err := adapter.requireAdmin(context.Background()); err != nil {
		t.Errorf("admin was refused with auth disabled: %v", err)
	}
	if _, err := adapter.requireAuthenticated(context.Background()); err != nil {
		t.Errorf("authentication was required with auth disabled: %v", err)
	}
}

// With auth on and no claims, the answer must be no. A permission check that
// returns nil here grants access to everything.
func TestUnauthenticatedRequestsAreRefusedWhenAuthIsEnabled(t *testing.T) {
	adapter, srv, cleanup := gqlAdapter(t)
	defer cleanup()
	withAuth(t, srv)

	if _, err := adapter.requireAuthenticated(context.Background()); err == nil {
		t.Error("an unauthenticated request was accepted")
	}
	if err := adapter.requireAdmin(context.Background()); err == nil {
		t.Error("an unauthenticated request passed the admin check")
	}
}

// A non-admin must fail the admin check even though they are authenticated —
// the two are separate questions.
func TestAdminCheckDistinguishesAuthenticationFromAuthority(t *testing.T) {
	adapter, srv, cleanup := gqlAdapter(t)
	defer cleanup()
	withAuth(t, srv)

	ctx := context.WithValue(context.Background(), authContextKey, &JWTClaims{Username: "reader", Admin: false})

	if _, err := adapter.requireAuthenticated(ctx); err != nil {
		t.Errorf("an authenticated non-admin was treated as unauthenticated: %v", err)
	}
	if err := adapter.requireAdmin(ctx); err == nil {
		t.Error("a non-admin passed the admin check")
	}

	adminCtx := context.WithValue(context.Background(), authContextKey, &JWTClaims{Username: "admin", Admin: true})
	if err := adapter.requireAdmin(adminCtx); err != nil {
		t.Errorf("an admin was refused: %v", err)
	}
}

func TestClaimsCrossIntoTheGraphQLLayer(t *testing.T) {
	adapter, _, cleanup := gqlAdapter(t)
	defer cleanup()

	if _, ok := adapter.GetClaimsFromContext(context.Background()); ok {
		t.Error("claims were reported present on a bare context")
	}

	ctx := context.WithValue(context.Background(), authContextKey, &JWTClaims{Username: "someone", Admin: true})
	got, ok := adapter.GetClaimsFromContext(ctx)
	if !ok {
		t.Fatal("claims did not cross into the GraphQL layer")
	}
	if got.Username != "someone" || !got.Admin {
		t.Errorf("claims were altered in translation: %+v", got)
	}
}

// --- conversion ---

// A document that loses a field in conversion looks to the client like a
// document that never had it.
func TestDocumentConversionKeepsEveryField(t *testing.T) {
	doc := &MCPDocument{
		ID: "docs|a|en", Key: "a", Lang: "en",
		ContentMD: "body text",
		Meta:      map[string][]string{"tag": {"one", "two"}},
		AddedAt:   time.Unix(111, 0), UpdatedAt: time.Unix(222, 0),
	}

	got := mcpDocToGQL(doc)
	if got == nil {
		t.Fatal("conversion produced nothing")
	}
	if got.ID != doc.ID || got.Key != doc.Key || got.Lang != doc.Lang {
		t.Errorf("identity changed: %+v", got)
	}
	if got.ContentMd != doc.ContentMD {
		t.Errorf("content changed: %q", got.ContentMd)
	}
	if tags, ok := got.Meta["tag"].([]string); !ok || len(tags) != 2 {
		t.Errorf("metadata was lost or reshaped: %#v", got.Meta)
	}
}

func TestDocumentConversionOfNil(t *testing.T) {
	if got := mcpDocToGQL(nil); got != nil {
		t.Errorf("converting nil produced %+v", got)
	}
}

// nil stats become a present-but-disabled object rather than null: the
// GraphQL field is non-null, so returning nil would fail the query rather than
// report that vectors are off.
func TestVectorStatsConversionOfNil(t *testing.T) {
	got := mcpVectorStatsToGQL(nil)
	if got == nil {
		t.Fatal("nil stats became a null non-null field")
	}
	if got.Enabled {
		t.Error("nil stats reported vectors as enabled")
	}
	if got.Collections == nil {
		t.Error("the collections list is null; a non-null list field must be an empty list")
	}
}

// Collections come from map iteration, so without sorting the same query
// answers in a different order every time.
func TestVectorStatsCollectionsAreOrdered(t *testing.T) {
	stats := &MCPVectorStatsResponse{
		Enabled: true,
		Collections: map[string]MCPVectorCollectionStats{
			"zeta": {TotalDocuments: 1}, "alpha": {TotalDocuments: 2},
			"mid": {TotalDocuments: 3}, "beta": {TotalDocuments: 4},
		},
	}

	first := mcpVectorStatsToGQL(stats)
	var order []string
	for _, c := range first.Collections {
		order = append(order, c.Collection)
	}
	if len(order) != 4 {
		t.Fatalf("got %d collections, want 4", len(order))
	}
	for i := 1; i < len(order); i++ {
		if order[i-1] >= order[i] {
			t.Fatalf("collections are not sorted: %v", order)
		}
	}

	// And stable across calls, which is the property a caller diffing two
	// responses depends on.
	for range 20 {
		again := mcpVectorStatsToGQL(stats)
		for i := range order {
			if again.Collections[i].Collection != order[i] {
				t.Fatalf("order varies between calls: %v", again.Collections)
			}
		}
	}
}

// --- optional-value helpers ---
//
// GraphQL inputs are pointers so a client can omit a field. Reading one wrong
// turns "not specified" into a value the caller never chose.

func TestOptionalValueHelpersFallBackWhenOmitted(t *testing.T) {
	if got := derefInt(nil, 5); got != 5 {
		t.Errorf("derefInt(nil) = %d, want the fallback 5", got)
	}
	if got := derefFloat64(nil, 0.5); got != 0.5 {
		t.Errorf("derefFloat64(nil) = %v, want the fallback 0.5", got)
	}
	if got := derefBool(nil, true); !got {
		t.Error("derefBool(nil) ignored the fallback")
	}
	if got := derefString(nil); got != "" {
		t.Errorf("derefString(nil) = %q", got)
	}
}

// A supplied zero must win over the fallback: "0 results" and "unspecified"
// are different requests.
func TestOptionalValueHelpersHonourAnExplicitZero(t *testing.T) {
	zero := 0
	if got := derefInt(&zero, 5); got != 0 {
		t.Errorf("an explicit 0 was replaced by the fallback: %d", got)
	}

	zeroF := 0.0
	if got := derefFloat64(&zeroF, 0.5); got != 0 {
		t.Errorf("an explicit 0.0 was replaced by the fallback: %v", got)
	}

	no := false
	if got := derefBool(&no, true); got {
		t.Error("an explicit false was replaced by the fallback")
	}

	empty := ""
	if got := derefString(&empty); got != "" {
		t.Errorf("an explicit empty string became %q", got)
	}
}

func TestOptionalValueHelpersPassThroughRealValues(t *testing.T) {
	n, f, b, s := 42, 1.5, true, "value"
	if got := derefInt(&n, 5); got != 42 {
		t.Errorf("derefInt = %d", got)
	}
	if got := derefFloat64(&f, 0.5); got != 1.5 {
		t.Errorf("derefFloat64 = %v", got)
	}
	if got := derefBool(&b, false); !got {
		t.Error("derefBool dropped a true")
	}
	if got := derefString(&s); got != "value" {
		t.Errorf("derefString = %q", got)
	}
}

var _ = gql.Claims{}
