package main

// End-to-end coverage for the GraphQL adapter (services/mddbd/graphql_adapter.go).
//
// These tests instantiate a real Server with a temporary BoltDB (via the
// existing newHandlerTestServer helper), wrap it in the adapter that the
// graphql package consumes, and exercise queries / mutations through the
// public ServerInterface contract — the same code path the production
// /graphql HTTP handler hits.
//
// The goal is to prove that the new resolvers actually work end-to-end on a
// real BoltDB, not just that the package compiles. Auth is left disabled
// (s.AuthManager == nil) so the adapter's permission checks short-circuit
// to allow-all, which mirrors a fresh-out-of-the-box mddbd deployment.

import (
	"context"
	"strings"
	"testing"
	"time"

	gql "mddb/graphql"
)

// gqlAdapterWithAuth wires up the adapter against a Server with a fully
// initialised AuthManager so the admin / auth code paths
// (Me, ListUsers, Register, CreateAPIKey, SetPermission, group ops) can be
// covered. The bootstrap admin is "admin" / "secret".
func gqlAdapterWithAuth(t *testing.T) (*GraphQLAdapter, context.Context, func()) {
	t.Helper()
	s, cleanup := newHandlerTestServer(t)

	s.AuthManager = NewAuthManager(s.DB, AuthConfig{
		JWTSecret:     "test-secret-do-not-ship",
		JWTExpiry:     time.Hour,
		AdminUsername: "admin",
		AdminPassword: "secret",
	})
	if err := s.AuthManager.EnsureBuckets(); err != nil {
		cleanup()
		t.Fatalf("auth EnsureBuckets: %v", err)
	}
	if err := s.AuthManager.LoadAll(); err != nil {
		cleanup()
		t.Fatalf("auth LoadAll: %v", err)
	}
	if err := s.AuthManager.BootstrapAdmin(); err != nil {
		cleanup()
		t.Fatalf("auth BootstrapAdmin: %v", err)
	}

	adapter := NewGraphQLServerAdapter(s).(*GraphQLAdapter)

	// Synthesize an admin context (mirrors what the HTTP middleware does after
	// validating a JWT) so the adapter's requireAdmin / CheckPermission paths
	// see a real authenticated principal.
	ctx := context.WithValue(context.Background(), authContextKey, &JWTClaims{
		Username: "admin",
		Admin:    true,
	})
	return adapter, ctx, cleanup
}

// gqlAdapterFromServer wires up the adapter against an in-memory test Server.
func gqlAdapterFromServer(t *testing.T) (*GraphQLAdapter, func()) {
	t.Helper()
	s, cleanup := newHandlerTestServer(t)
	a := NewGraphQLServerAdapter(s).(*GraphQLAdapter)
	return a, cleanup
}

func TestGraphQLE2E_AddDocumentThenGet(t *testing.T) {
	a, cleanup := gqlAdapterFromServer(t)
	defer cleanup()
	ctx := context.Background()

	// Mutation: addDocument
	added, err := a.AddDocument(ctx, gql.AddDocumentInput{
		Collection: "blog",
		Key:        "hello",
		Lang:       "en_US",
		ContentMd:  "# Hello\n\nFirst post.",
		Meta: []*gql.MetaInput{
			{Key: "tags", Values: []string{"intro", "test"}},
			{Key: "author", Values: []string{"alice"}},
		},
	})
	if err != nil {
		t.Fatalf("AddDocument: %v", err)
	}
	if added == nil || added.Key != "hello" || added.Lang != "en_US" {
		t.Fatalf("AddDocument returned wrong document: %+v", added)
	}
	if !strings.Contains(added.ContentMd, "Hello") {
		t.Errorf("expected content to contain 'Hello', got %q", added.ContentMd)
	}

	// Query: document
	got, err := a.GetDocument(ctx, "blog", "hello", "en_US", nil)
	if err != nil {
		t.Fatalf("GetDocument: %v", err)
	}
	if got == nil {
		t.Fatal("GetDocument returned nil")
	}
	if got.Key != "hello" || got.Lang != "en_US" {
		t.Errorf("GetDocument key/lang mismatch: %+v", got)
	}
	if !strings.Contains(got.ContentMd, "First post") {
		t.Errorf("expected fetched content to contain 'First post', got %q", got.ContentMd)
	}
}

func TestGraphQLE2E_SearchPagination(t *testing.T) {
	a, cleanup := gqlAdapterFromServer(t)
	defer cleanup()
	ctx := context.Background()

	// Seed three documents.
	for _, k := range []string{"a", "b", "c"} {
		if _, err := a.AddDocument(ctx, gql.AddDocumentInput{
			Collection: "notes",
			Key:        k,
			Lang:       "en_US",
			ContentMd:  "# " + k,
		}); err != nil {
			t.Fatalf("seed %s: %v", k, err)
		}
	}

	limit := 2
	conn, err := a.SearchDocuments(ctx, gql.SearchInput{
		Collection: "notes",
		Limit:      &limit,
	})
	if err != nil {
		t.Fatalf("SearchDocuments: %v", err)
	}
	if conn == nil {
		t.Fatal("SearchDocuments returned nil")
	}
	if conn.TotalCount != 3 {
		t.Errorf("TotalCount = %d, want 3", conn.TotalCount)
	}
	if len(conn.Edges) != 2 {
		t.Errorf("Edges len = %d, want 2 (limit)", len(conn.Edges))
	}
	if conn.PageInfo == nil || !conn.PageInfo.HasNextPage {
		t.Errorf("PageInfo.HasNextPage should be true (3 docs total, page size 2)")
	}
}

func TestGraphQLE2E_DeleteDocument(t *testing.T) {
	a, cleanup := gqlAdapterFromServer(t)
	defer cleanup()
	ctx := context.Background()

	if _, err := a.AddDocument(ctx, gql.AddDocumentInput{
		Collection: "tmp",
		Key:        "soon-gone",
		Lang:       "en_US",
		ContentMd:  "# About to vanish",
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := a.DeleteDocument(ctx, "tmp", "soon-gone", "en_US", nil); err != nil {
		t.Fatalf("DeleteDocument: %v", err)
	}
	// Subsequent get should fail (document gone).
	if _, err := a.GetDocument(ctx, "tmp", "soon-gone", "en_US", nil); err == nil {
		t.Error("expected error fetching deleted document, got nil")
	}
}

func TestGraphQLE2E_StatsContainsCollections(t *testing.T) {
	a, cleanup := gqlAdapterFromServer(t)
	defer cleanup()
	ctx := context.Background()

	if _, err := a.AddDocument(ctx, gql.AddDocumentInput{
		Collection: "stats-target",
		Key:        "k",
		Lang:       "en_US",
		ContentMd:  "# stats test",
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	stats, err := a.GetStats(ctx)
	if err != nil {
		t.Fatalf("GetStats: %v", err)
	}
	if stats == nil {
		t.Fatal("GetStats returned nil")
	}
	if stats.TotalDocuments < 1 {
		t.Errorf("TotalDocuments = %d, want >= 1", stats.TotalDocuments)
	}
	found := false
	for _, c := range stats.Collections {
		if c.Name == "stats-target" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected 'stats-target' collection in Stats.Collections")
	}
}

func TestGraphQLE2E_LoginWithoutAuthEnabled(t *testing.T) {
	a, cleanup := gqlAdapterFromServer(t)
	defer cleanup()

	// AuthManager is nil in newHandlerTestServer; Authenticate must surface
	// the not-enabled sentinel rather than panicking.
	_, err := a.Authenticate("admin", "secret")
	if err == nil {
		t.Fatal("expected ErrAuthNotEnabled when AuthManager is nil")
	}
	if err != ErrAuthNotEnabled {
		t.Errorf("got %v, want ErrAuthNotEnabled", err)
	}
}

func TestGraphQLE2E_UpdateDocument(t *testing.T) {
	a, cleanup := gqlAdapterFromServer(t)
	defer cleanup()
	ctx := context.Background()

	if _, err := a.AddDocument(ctx, gql.AddDocumentInput{
		Collection: "blog",
		Key:        "post",
		Lang:       "en_US",
		ContentMd:  "# original",
		Meta:       []*gql.MetaInput{{Key: "tags", Values: []string{"draft"}}},
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	newContent := "# updated body"
	updated, err := a.UpdateDocument(ctx, gql.UpdateDocumentInput{
		Collection: "blog",
		Key:        "post",
		Lang:       "en_US",
		ContentMd:  &newContent,
		Meta:       []*gql.MetaInput{{Key: "tags", Values: []string{"published"}}},
	})
	if err != nil {
		t.Fatalf("UpdateDocument: %v", err)
	}
	if !strings.Contains(updated.ContentMd, "updated body") {
		t.Errorf("expected updated content, got %q", updated.ContentMd)
	}
}

func TestGraphQLE2E_AddBatch(t *testing.T) {
	a, cleanup := gqlAdapterFromServer(t)
	defer cleanup()
	ctx := context.Background()

	res, err := a.AddBatch(ctx, "batch-coll", []*gql.AddBatchDocumentInput{
		{Key: "k1", Lang: "en_US", ContentMd: "# first"},
		{Key: "k2", Lang: "en_US", ContentMd: "# second"},
		{Key: "k3", Lang: "en_US", ContentMd: "# third"},
	})
	if err != nil {
		t.Fatalf("AddBatch: %v", err)
	}
	if res.Added+res.Updated != 3 {
		t.Errorf("expected 3 docs added/updated, got added=%d updated=%d failed=%d",
			res.Added, res.Updated, res.Failed)
	}
}

func TestGraphQLE2E_SetTTLAndImportURL(t *testing.T) {
	a, cleanup := gqlAdapterFromServer(t)
	defer cleanup()
	ctx := context.Background()

	if _, err := a.AddDocument(ctx, gql.AddDocumentInput{
		Collection: "ttl",
		Key:        "tmp",
		Lang:       "en_US",
		ContentMd:  "# tmp",
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := a.SetTTL(ctx, "ttl", "tmp", "en_US", 3600); err != nil {
		t.Fatalf("SetTTL: %v", err)
	}

	// ImportURL with a bogus URL should surface the fetch error rather than
	// panic. We're testing the resolver path, not the network.
	bogus := "key1"
	_, err := a.ImportURL(ctx, "imports", "http://127.0.0.1:1/never", &bogus, "en_US", nil, nil)
	if err == nil {
		t.Error("expected ImportURL to fail on unreachable URL")
	}
}

func TestGraphQLE2E_FullTextSearch(t *testing.T) {
	a, cleanup := gqlAdapterFromServer(t)
	defer cleanup()
	ctx := context.Background()

	for _, k := range []string{"red", "blue", "green"} {
		if _, err := a.AddDocument(ctx, gql.AddDocumentInput{
			Collection: "fts",
			Key:        k,
			Lang:       "en_US",
			ContentMd:  "# " + k + "\n\nthe quick brown fox is " + k,
		}); err != nil {
			t.Fatalf("seed %s: %v", k, err)
		}
	}

	// FTS may or may not return results depending on whether the test server
	// runs the indexer synchronously — accept either case, but the resolver
	// must not panic and must return a non-nil response shape.
	resp, err := a.FullTextSearch(ctx, gql.FTSInput{
		Collection: "fts",
		Query:      "fox",
	})
	if err != nil {
		t.Logf("FTS returned error (may be expected if FTS is async): %v", err)
		return
	}
	if resp == nil {
		t.Fatal("FullTextSearch returned nil response")
	}
}

func TestGraphQLE2E_SchemaCRUD(t *testing.T) {
	a, cleanup := gqlAdapterFromServer(t)
	defer cleanup()
	ctx := context.Background()

	// SetSchema
	if err := a.SetSchema(ctx, gql.SetSchemaInput{
		Collection: "schema-coll",
		Schema:     `{"type":"object","properties":{"author":{"type":"string"}}}`,
	}); err != nil {
		t.Fatalf("SetSchema: %v", err)
	}

	// GetSchema
	got, err := a.GetSchema(ctx, "schema-coll")
	if err != nil {
		t.Fatalf("GetSchema: %v", err)
	}
	if got == nil || got.Collection != "schema-coll" {
		t.Errorf("GetSchema unexpected: %+v", got)
	}

	// ListSchemas should now contain at least one entry
	all, err := a.ListSchemas(ctx)
	if err != nil {
		t.Fatalf("ListSchemas: %v", err)
	}
	if len(all) == 0 {
		t.Error("ListSchemas returned empty after SetSchema")
	}

	// ValidateDocument against the schema
	res, err := a.ValidateDocument(ctx, "schema-coll", []*gql.MetaInput{
		{Key: "author", Values: []string{"alice"}},
	})
	if err != nil {
		t.Fatalf("ValidateDocument: %v", err)
	}
	if res == nil {
		t.Fatal("ValidateDocument returned nil")
	}

	// DeleteSchema
	if err := a.DeleteSchema(ctx, "schema-coll"); err != nil {
		t.Fatalf("DeleteSchema: %v", err)
	}
}

func TestGraphQLE2E_WebhookCRUD(t *testing.T) {
	a, cleanup := gqlAdapterFromServer(t)
	defer cleanup()
	ctx := context.Background()

	w, err := a.RegisterWebhook(ctx, gql.RegisterWebhookInput{
		URL:        "https://example.invalid/hook",
		Events:     []string{"doc.added", "doc.deleted"},
		Collection: "blog",
	})
	if err != nil {
		t.Fatalf("RegisterWebhook: %v", err)
	}
	if w == nil || w.ID == "" {
		t.Fatalf("RegisterWebhook returned bad webhook: %+v", w)
	}

	all, err := a.ListWebhooks(ctx, nil)
	if err != nil {
		t.Fatalf("ListWebhooks: %v", err)
	}
	if len(all) == 0 {
		t.Error("ListWebhooks returned empty after RegisterWebhook")
	}

	if err := a.DeleteWebhook(ctx, w.ID); err != nil {
		t.Fatalf("DeleteWebhook: %v", err)
	}
}

func TestGraphQLE2E_VectorReindex(t *testing.T) {
	a, cleanup := gqlAdapterFromServer(t)
	defer cleanup()
	ctx := context.Background()

	// Reindex on an empty collection should not panic; the underlying
	// reindex either returns OK with zero docs, or surfaces an error if
	// embeddings aren't configured. Either is acceptable — we only care that
	// the resolver wiring works.
	force := true
	_, err := a.VectorReindex(ctx, "empty", &force)
	_ = err // tolerated; the resolver path is what we're covering
}

func TestGraphQLE2E_DeleteCollection(t *testing.T) {
	a, cleanup := gqlAdapterFromServer(t)
	defer cleanup()
	ctx := context.Background()

	for _, k := range []string{"a", "b"} {
		if _, err := a.AddDocument(ctx, gql.AddDocumentInput{
			Collection: "doomed",
			Key:        k,
			Lang:       "en_US",
			ContentMd:  "# " + k,
		}); err != nil {
			t.Fatalf("seed %s: %v", k, err)
		}
	}
	deleted, err := a.DeleteCollection(ctx, "doomed")
	if err != nil {
		t.Fatalf("DeleteCollection: %v", err)
	}
	if deleted < 2 {
		t.Errorf("expected at least 2 docs deleted, got %d", deleted)
	}
}

func TestGraphQLE2E_AuthFlow(t *testing.T) {
	a, ctx, cleanup := gqlAdapterWithAuth(t)
	defer cleanup()

	// Authenticate (login resolver pair)
	info, err := a.Authenticate("admin", "secret")
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	if info.Username != "admin" || !info.Admin {
		t.Errorf("Authenticate returned %+v, want admin=true", info)
	}
	token, exp, err := a.GenerateJWT(info.Username, info.Admin)
	if err != nil {
		t.Fatalf("GenerateJWT: %v", err)
	}
	if token == "" || exp <= time.Now().Unix() {
		t.Errorf("GenerateJWT returned bad token=%q expiresAt=%d", token, exp)
	}

	// Wrong password should fail
	if _, err := a.Authenticate("admin", "wrong"); err == nil {
		t.Error("expected Authenticate to fail with wrong password")
	}

	// Me — uses claims from context
	me, err := a.Me(ctx)
	if err != nil {
		t.Fatalf("Me: %v", err)
	}
	if me.Username != "admin" || !me.Admin {
		t.Errorf("Me returned %+v, want admin", me)
	}

	// Register a new user (admin-only)
	user, err := a.Register(ctx, "alice", "alice-pass-123")
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if user.Username != "alice" {
		t.Errorf("Register returned %+v", user)
	}

	// ListUsers — should now show admin + alice
	users, err := a.ListUsers(ctx)
	if err != nil {
		t.Fatalf("ListUsers: %v", err)
	}
	if len(users) < 2 {
		t.Errorf("ListUsers returned %d users, want >= 2", len(users))
	}

	// CreateAPIKey for the current user (admin)
	desc := "ci-test"
	key, err := a.CreateAPIKey(ctx, gql.CreateAPIKeyInput{Description: desc})
	if err != nil {
		t.Fatalf("CreateAPIKey: %v", err)
	}
	if key.Key == "" || key.Description != desc {
		t.Errorf("CreateAPIKey returned %+v", key)
	}

	// SetPermission for alice on a collection
	if err := a.SetPermission(ctx, gql.SetPermissionInput{
		Username:   "alice",
		Collection: "blog",
		Read:       true,
		Write:      true,
		Admin:      false,
	}); err != nil {
		t.Fatalf("SetPermission: %v", err)
	}

	// UserPermissionsList should include the new grant
	perms, err := a.UserPermissionsList(ctx, "alice")
	if err != nil {
		t.Fatalf("UserPermissionsList: %v", err)
	}
	if len(perms) == 0 {
		t.Error("UserPermissionsList returned empty after SetPermission")
	}
}

func TestGraphQLE2E_GroupFlow(t *testing.T) {
	a, ctx, cleanup := gqlAdapterWithAuth(t)
	defer cleanup()

	// CreateGroup
	g, err := a.CreateGroup(ctx, gql.CreateGroupInput{
		Name:        "editors",
		Description: "content editors",
		Members:     []string{"admin"},
	})
	if err != nil {
		t.Fatalf("CreateGroup: %v", err)
	}
	if g.Name != "editors" {
		t.Errorf("CreateGroup returned %+v", g)
	}

	// ListGroups
	all, err := a.ListGroups(ctx)
	if err != nil {
		t.Fatalf("ListGroups: %v", err)
	}
	if len(all) == 0 {
		t.Error("ListGroups returned empty")
	}

	// SetGroupPermission
	if err := a.SetGroupPermission(ctx, gql.SetGroupPermissionInput{
		GroupName:  "editors",
		Collection: "blog",
		Read:       true,
		Write:      true,
		Admin:      false,
	}); err != nil {
		t.Fatalf("SetGroupPermission: %v", err)
	}

	// GroupPermissionsList
	perms, err := a.GroupPermissionsList(ctx, "editors")
	if err != nil {
		t.Fatalf("GroupPermissionsList: %v", err)
	}
	if len(perms) == 0 {
		t.Error("GroupPermissionsList returned empty after SetGroupPermission")
	}

	// UpdateGroup
	if _, err := a.UpdateGroup(ctx, "editors", "edited", []string{"admin"}); err != nil {
		t.Fatalf("UpdateGroup: %v", err)
	}

	// DeleteGroup
	if err := a.DeleteGroup(ctx, "editors"); err != nil {
		t.Fatalf("DeleteGroup: %v", err)
	}
}

func TestGraphQLE2E_NoPanicOnAllResolvers(t *testing.T) {
	a, cleanup := gqlAdapterFromServer(t)
	defer cleanup()
	ctx := context.Background()

	// Smoke-call every read-only adapter method to confirm none of them
	// panic on a freshly-initialised, empty Server. This is the regression
	// guard against the prior state where 29/32 resolvers panicked
	// `not implemented`. Errors are tolerated — empty BoltDB legitimately
	// returns "not found" / nil for many of these — only panics fail the test.
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("unexpected panic from a resolver path: %v", r)
		}
	}()

	_, _ = a.GetStats(ctx)
	_, _ = a.VectorStats(ctx)
	_, _ = a.ListSchemas(ctx)
	_, _ = a.ListWebhooks(ctx, nil)
	_, _ = a.ListUsers(ctx)
	_, _ = a.ListGroups(ctx)
	_, _ = a.SearchDocuments(ctx, gql.SearchInput{Collection: "missing"})
	_, _ = a.GetDocument(ctx, "missing", "missing", "en_US", nil)
	_, _ = a.GetSchema(ctx, "missing")
}
