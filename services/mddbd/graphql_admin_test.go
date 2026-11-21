package main

// The admin side of the GraphQL adapter (graphql_admin.go): who may call it,
// what it answers with auth off, and how it reports a failing backend.

import (
	"context"
	"errors"
	"testing"

	gql "mddb/graphql"
)

// gqlAdminCall is one adapter method, called with fixed arguments.
type gqlAdminCall struct {
	name string
	run  func(ctx context.Context, a *GraphQLAdapter) error
}

// gqlAdminIgnore drops the value of a (value, error) pair.
func gqlAdminIgnore[T any](_ T, err error) error { return err }

// gqlAdminCalls lists every adapter method of graphql_admin.go.
func gqlAdminCalls() []gqlAdminCall {
	exp := int64(4102444800)
	return []gqlAdminCall{
		{"GetStats", func(ctx context.Context, a *GraphQLAdapter) error { return gqlAdminIgnore(a.GetStats(ctx)) }},
		{"GetSchema", func(ctx context.Context, a *GraphQLAdapter) error { return gqlAdminIgnore(a.GetSchema(ctx, "docs")) }},
		{"ListSchemas", func(ctx context.Context, a *GraphQLAdapter) error { return gqlAdminIgnore(a.ListSchemas(ctx)) }},
		{"SetSchema", func(ctx context.Context, a *GraphQLAdapter) error {
			return a.SetSchema(ctx, gql.SetSchemaInput{Collection: "docs", Schema: "{}"})
		}},
		{"DeleteSchema", func(ctx context.Context, a *GraphQLAdapter) error { return a.DeleteSchema(ctx, "docs") }},
		{"ValidateDocument", func(ctx context.Context, a *GraphQLAdapter) error {
			return gqlAdminIgnore(a.ValidateDocument(ctx, "docs", nil))
		}},
		{"ListWebhooks", func(ctx context.Context, a *GraphQLAdapter) error { return gqlAdminIgnore(a.ListWebhooks(ctx, nil)) }},
		{"RegisterWebhook", func(ctx context.Context, a *GraphQLAdapter) error {
			return gqlAdminIgnore(a.RegisterWebhook(ctx, gql.RegisterWebhookInput{URL: "https://hooks.invalid/x", Events: []string{"doc.added"}}))
		}},
		{"DeleteWebhook", func(ctx context.Context, a *GraphQLAdapter) error { return a.DeleteWebhook(ctx, "nope") }},
		{"Me", func(ctx context.Context, a *GraphQLAdapter) error { return gqlAdminIgnore(a.Me(ctx)) }},
		{"ListUsers", func(ctx context.Context, a *GraphQLAdapter) error { return gqlAdminIgnore(a.ListUsers(ctx)) }},
		{"Register", func(ctx context.Context, a *GraphQLAdapter) error {
			return gqlAdminIgnore(a.Register(ctx, "zed", "pw"))
		}},
		{"CreateAPIKey", func(ctx context.Context, a *GraphQLAdapter) error {
			return gqlAdminIgnore(a.CreateAPIKey(ctx, gql.CreateAPIKeyInput{Description: "ci", ExpiresAt: &exp}))
		}},
		{"SetPermission", func(ctx context.Context, a *GraphQLAdapter) error {
			return a.SetPermission(ctx, gql.SetPermissionInput{Username: "admin", Collection: "docs", Read: true})
		}},
		{"DeletePermission", func(ctx context.Context, a *GraphQLAdapter) error { return a.DeletePermission(ctx, "admin", "docs") }},
		{"UserPermissionsList", func(ctx context.Context, a *GraphQLAdapter) error {
			return gqlAdminIgnore(a.UserPermissionsList(ctx, "admin"))
		}},
		{"ListGroups", func(ctx context.Context, a *GraphQLAdapter) error { return gqlAdminIgnore(a.ListGroups(ctx)) }},
		{"GroupPermissionsList", func(ctx context.Context, a *GraphQLAdapter) error {
			return gqlAdminIgnore(a.GroupPermissionsList(ctx, "eds"))
		}},
		{"CreateGroup", func(ctx context.Context, a *GraphQLAdapter) error {
			return gqlAdminIgnore(a.CreateGroup(ctx, gql.CreateGroupInput{Name: "eds"}))
		}},
		{"UpdateGroup", func(ctx context.Context, a *GraphQLAdapter) error {
			return gqlAdminIgnore(a.UpdateGroup(ctx, "eds", "d", nil))
		}},
		{"DeleteGroup", func(ctx context.Context, a *GraphQLAdapter) error { return a.DeleteGroup(ctx, "eds") }},
		{"SetGroupPermission", func(ctx context.Context, a *GraphQLAdapter) error {
			return a.SetGroupPermission(ctx, gql.SetGroupPermissionInput{GroupName: "eds", Collection: "docs", Read: true})
		}},
		{"DeleteGroupPermission", func(ctx context.Context, a *GraphQLAdapter) error {
			return a.DeleteGroupPermission(ctx, "eds", "docs")
		}},
	}
}

// With auth on, a caller without credentials gets nothing from any of them.
func TestGraphQLAdmin_RefusesAnonymousCaller(t *testing.T) {
	a, _, cleanup := gqlAdapterWithAuth(t)
	defer cleanup()
	for _, c := range gqlAdminCalls() {
		if err := c.run(context.Background(), a); !errors.Is(err, ErrUnauthenticated) {
			t.Errorf("%s: err = %v, want ErrUnauthenticated", c.name, err)
		}
	}
}

// With auth off there is nobody to manage: the auth methods say so, or
// answer with nothing, and Me is the synthetic admin.
func TestGraphQLAdmin_WithoutAuth(t *testing.T) {
	a, cleanup := gqlAdapterFromServer(t)
	defer cleanup()
	ctx := context.Background()
	wantDisabled := map[string]bool{
		"Register": true, "CreateAPIKey": true, "SetPermission": true, "DeletePermission": true,
		"CreateGroup": true, "UpdateGroup": true, "DeleteGroup": true, "SetGroupPermission": true,
		"DeleteGroupPermission": true,
	}
	for _, c := range gqlAdminCalls() {
		if !wantDisabled[c.name] {
			continue
		}
		if err := c.run(ctx, a); !errors.Is(err, ErrAuthNotEnabled) {
			t.Errorf("%s: err = %v, want ErrAuthNotEnabled", c.name, err)
		}
	}
	me, err := a.Me(ctx)
	if err != nil || me.Username != "anonymous" || !me.Admin {
		t.Errorf("Me = %+v, %v; want the anonymous admin", me, err)
	}
	if up, err := a.UserPermissionsList(ctx, "x"); err != nil || len(up) != 0 {
		t.Errorf("UserPermissionsList = %v, %v; want empty", up, err)
	}
	if gp, err := a.GroupPermissionsList(ctx, "x"); err != nil || len(gp) != 0 {
		t.Errorf("GroupPermissionsList = %v, %v; want empty", gp, err)
	}
}

// Without the managers behind them, the schema and webhook methods report it.
func TestGraphQLAdmin_MissingManagers(t *testing.T) {
	bare := &Server{}
	a := &GraphQLAdapter{server: bare, mcp: NewDirectClient(bare)}
	ctx := context.Background()
	for _, name := range []string{"GetSchema", "ListSchemas", "ListWebhooks", "RegisterWebhook"} {
		for _, c := range gqlAdminCalls() {
			if c.name == name {
				if err := c.run(ctx, a); err == nil {
					t.Errorf("%s without its manager: no error", name)
				}
			}
		}
	}
}

// The auth methods hand back what the AuthManager refuses, and the admin's
// own API key keeps the expiry it was given.
func TestGraphQLAdmin_AuthManagerRefusals(t *testing.T) {
	a, ctx, cleanup := gqlAdapterWithAuth(t)
	defer cleanup()

	exp := int64(4102444800)
	key, err := a.CreateAPIKey(ctx, gql.CreateAPIKeyInput{Description: "ci", ExpiresAt: &exp})
	if err != nil || key.ExpiresAt == nil || *key.ExpiresAt != 4102444800 {
		t.Fatalf("CreateAPIKey = %+v, %v; want the expiry kept", key, err)
	}
	if _, err := a.Register(ctx, "admin", "pw"); !errors.Is(err, ErrUserExists) {
		t.Errorf("Register of an existing user = %v, want ErrUserExists", err)
	}
	if _, err := a.CreateGroup(ctx, gql.CreateGroupInput{Name: "eds"}); err != nil {
		t.Fatalf("CreateGroup: %v", err)
	}
	if _, err := a.CreateGroup(ctx, gql.CreateGroupInput{Name: "eds"}); err == nil {
		t.Error("CreateGroup of an existing group: no error")
	}
	if _, err := a.UpdateGroup(ctx, "nobody", "d", nil); err == nil {
		t.Error("UpdateGroup of a missing group: no error")
	}

	ghost := context.WithValue(context.Background(), authContextKey, &JWTClaims{Username: "ghost", Admin: true})
	if _, err := a.Me(ghost); !errors.Is(err, ErrUserNotFound) {
		t.Errorf("Me of a deleted user = %v, want ErrUserNotFound", err)
	}
	if _, err := a.CreateAPIKey(ghost, gql.CreateAPIKeyInput{Description: "x"}); !errors.Is(err, ErrUserNotFound) {
		t.Errorf("CreateAPIKey of a deleted user = %v, want ErrUserNotFound", err)
	}

	// A user without a grant on the collection cannot validate against it.
	if _, err := a.server.AuthManager.CreateUser("alice", "pw"); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	alice := context.WithValue(context.Background(), authContextKey, &JWTClaims{Username: "alice"})
	if _, err := a.ValidateDocument(alice, "docs", nil); !errors.Is(err, ErrForbidden) {
		t.Errorf("ValidateDocument without a grant = %v, want ErrForbidden", err)
	}
}

// ListWebhooks keeps to the collection asked for.
func TestGraphQLAdmin_ListWebhooksFiltersByCollection(t *testing.T) {
	a, cleanup := gqlAdapterFromServer(t)
	defer cleanup()
	ctx := context.Background()
	for _, coll := range []string{"blog", "docs"} {
		if _, err := a.RegisterWebhook(ctx, gql.RegisterWebhookInput{URL: "https://hooks.invalid/" + coll, Events: []string{"doc.added"}, Collection: coll}); err != nil {
			t.Fatalf("RegisterWebhook %s: %v", coll, err)
		}
	}
	want := "docs"
	hooks, err := a.ListWebhooks(ctx, &want)
	if err != nil || len(hooks) != 1 || hooks[0].Collection != "docs" {
		t.Errorf("ListWebhooks(docs) = %+v, %v; want the docs hook only", hooks, err)
	}
}

// GetStats reports storage it cannot read rather than empty statistics.
func TestGraphQLAdmin_GetStatsOnFailingStorage(t *testing.T) {
	a, cleanup := gqlAdapterFromServer(t)
	defer cleanup()
	// A document first: in the per-collection layout a server without
	// collections has no file the walk would read.
	if _, err := a.AddDocument(context.Background(), gql.AddDocumentInput{Collection: "docs", Key: "k", Lang: "en", ContentMd: "x"}); err != nil {
		t.Fatalf("AddDocument: %v", err)
	}
	closeStorage(t, a.server, "docs")
	if stats, err := a.GetStats(context.Background()); err == nil {
		t.Errorf("GetStats on closed storage = %+v, want an error", stats)
	}
}
