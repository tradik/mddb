package main

// Removing a user or group permission (#438): the AuthManager, the REST
// endpoints, the GraphQL adapter, and the tokens issued before the removal.

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	bolt "go.etcd.io/bbolt"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

// A group record that no longer decodes stops DeleteUser before anything is
// removed: the transaction is all or nothing.
func TestDeleteUser_CorruptGroupRecord(t *testing.T) {
	s, db, cleanup := setupTestServer(t)
	defer cleanup()
	am := s.AuthManager
	if _, err := am.CreateUser("alice", "pw"); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if _, err := am.CreateGroup("eds", "", []string{"alice"}); err != nil {
		t.Fatalf("CreateGroup: %v", err)
	}
	if err := db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket(bucketAuthGroups).Put([]byte("eds"), []byte("{not json"))
	}); err != nil {
		t.Fatalf("corrupting the group: %v", err)
	}
	if err := am.DeleteUser("alice"); err == nil {
		t.Fatal("DeleteUser over a corrupt group record: no error")
	}
	if _, err := am.GetUser("alice"); err != nil {
		t.Errorf("the failed delete still removed the user: %v", err)
	}
}

// permDelGrant gives username a grant on collection, failing the test on error.
func permDelGrant(t *testing.T, am *AuthManager, username, collection string, admin bool) {
	t.Helper()
	if err := am.SetPermission(&Permission{Username: username, Collection: collection, Read: true, Write: admin, Admin: admin}); err != nil {
		t.Fatalf("SetPermission %s/%s: %v", username, collection, err)
	}
}

// permDelGroup creates group with members and gives it a read grant on each
// collection.
func permDelGroup(t *testing.T, am *AuthManager, group string, members []string, collections ...string) {
	t.Helper()
	if _, err := am.CreateGroup(group, "", members); err != nil {
		t.Fatalf("CreateGroup: %v", err)
	}
	for _, c := range collections {
		if err := am.SetGroupPermission(&GroupPermission{GroupName: group, Collection: c, Read: true}); err != nil {
			t.Fatalf("SetGroupPermission %s/%s: %v", group, c, err)
		}
	}
}

// permDelCollections lists the collections of perms.
func permDelCollections[P interface{ *Permission | *GroupPermission }](perms []P) []string {
	out := make([]string, 0, len(perms))
	for _, p := range perms {
		switch v := any(p).(type) {
		case *Permission:
			out = append(out, v.Collection)
		case *GroupPermission:
			out = append(out, v.Collection)
		}
	}
	return out
}

// permDelReopen reads the auth data again from the same database, the way a
// restart does.
func permDelReopen(t *testing.T, s *Server) *AuthManager {
	t.Helper()
	am := NewAuthManager(s.DB, s.AuthManager.config)
	if err := am.LoadAll(); err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	return am
}

// permDelRequest runs handler for an admin's (or a non-admin's) request.
func permDelRequest(handler http.HandlerFunc, method, target, actor string, admin bool) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, nil)
	req = req.WithContext(authenticatedContext(actor, admin))
	w := httptest.NewRecorder()
	handler(w, req)
	return w
}

func TestDeletePermission_RemovesOnlyThatGrant(t *testing.T) {
	s, _, cleanup := setupTestServer(t)
	defer cleanup()
	am := s.AuthManager
	if _, err := am.CreateUser("alice", "pw"); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	permDelGrant(t, am, "alice", "blog", false)
	permDelGrant(t, am, "alice", "docs", false)

	if err := am.DeletePermission("alice", "blog"); err != nil {
		t.Fatalf("DeletePermission: %v", err)
	}
	if got := permDelCollections(am.GetPermissions("alice")); len(got) != 1 || got[0] != "docs" {
		t.Fatalf("grants after delete = %v, want [docs]", got)
	}
	// Reading the database again must not bring it back.
	if got := permDelCollections(permDelReopen(t, s).GetPermissions("alice")); len(got) != 1 || got[0] != "docs" {
		t.Fatalf("grants after reload = %v, want [docs]", got)
	}
	if err := am.DeletePermission("alice", "blog"); !errors.Is(err, ErrPermissionNotFound) {
		t.Fatalf("second delete = %v, want ErrPermissionNotFound", err)
	}
}

func TestDeletePermission_RefusesEmptyArguments(t *testing.T) {
	s, _, cleanup := setupTestServer(t)
	defer cleanup()
	for _, args := range [][2]string{{"", "blog"}, {"alice", ""}} {
		if err := s.AuthManager.DeletePermission(args[0], args[1]); err == nil || errors.Is(err, ErrPermissionNotFound) {
			t.Errorf("DeletePermission(%q, %q) = %v, want an argument error", args[0], args[1], err)
		}
		if err := s.AuthManager.DeleteGroupPermission(args[0], args[1]); err == nil || errors.Is(err, ErrPermissionNotFound) {
			t.Errorf("DeleteGroupPermission(%q, %q) = %v, want an argument error", args[0], args[1], err)
		}
	}
}

func TestDeleteGroupPermission_RemovesOnlyThatGrant(t *testing.T) {
	s, _, cleanup := setupTestServer(t)
	defer cleanup()
	am := s.AuthManager
	permDelGroup(t, am, "editors", nil, "blog", "docs")

	if err := am.DeleteGroupPermission("editors", "blog"); err != nil {
		t.Fatalf("DeleteGroupPermission: %v", err)
	}
	if got := permDelCollections(am.GetGroupPermissions("editors")); len(got) != 1 || got[0] != "docs" {
		t.Fatalf("group grants after delete = %v, want [docs]", got)
	}
	if got := permDelCollections(permDelReopen(t, s).GetGroupPermissions("editors")); len(got) != 1 || got[0] != "docs" {
		t.Fatalf("group grants after reload = %v, want [docs]", got)
	}
	if _, err := am.GetGroup("editors"); err != nil {
		t.Fatalf("the group itself went: %v", err)
	}
	if err := am.DeleteGroupPermission("editors", "blog"); !errors.Is(err, ErrPermissionNotFound) {
		t.Fatalf("second delete = %v, want ErrPermissionNotFound", err)
	}
}

func TestDeletePermission_StorageFailure(t *testing.T) {
	s, _, cleanup := setupTestServer(t)
	am := s.AuthManager
	cleanup() // the database is closed: every transaction fails
	if err := am.DeletePermission("alice", "blog"); err == nil || errors.Is(err, ErrPermissionNotFound) {
		t.Errorf("DeletePermission on a closed database = %v, want a storage error", err)
	}
	if err := am.DeleteGroupPermission("editors", "blog"); err == nil || errors.Is(err, ErrPermissionNotFound) {
		t.Errorf("DeleteGroupPermission on a closed database = %v, want a storage error", err)
	}
}

func TestGuardPermissionRemoval(t *testing.T) {
	s, _, cleanup := setupTestServer(t)
	defer cleanup()
	_ = createAdminAndGetToken(t, s)
	am := s.AuthManager
	if _, err := am.CreateUser("bob", "pw"); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	permDelGrant(t, am, "bob", "*", true)
	permDelGrant(t, am, "admin", "blog", true)

	cases := []struct {
		actor, username, collection string
		want                        error
	}{
		{"admin", "admin", "*", ErrOwnGlobalAdmin}, // own global admin
		{"admin", "admin", "blog", nil},            // own grant on one collection
		{"admin", "bob", "*", nil},                 // another admin's global admin
		{"bob", "bob", "blog", nil},                // bob holds nothing on blog
	}
	for _, tc := range cases {
		if got := am.GuardPermissionRemoval(tc.actor, tc.username, tc.collection); !errors.Is(got, tc.want) {
			t.Errorf("Guard(%s, %s, %s) = %v, want %v", tc.actor, tc.username, tc.collection, got, tc.want)
		}
	}
	// Once read on "*" only, it is no longer what makes them an admin.
	if err := am.SetPermission(&Permission{Username: "admin", Collection: "*", Read: true}); err != nil {
		t.Fatalf("SetPermission: %v", err)
	}
	if err := am.GuardPermissionRemoval("admin", "admin", "*"); err != nil {
		t.Errorf("Guard on a non-admin grant = %v, want nil", err)
	}
}

func TestHandleDeletePermission(t *testing.T) {
	s, _, cleanup := setupTestServer(t)
	defer cleanup()
	_ = createAdminAndGetToken(t, s)
	if _, err := s.AuthManager.CreateUser("alice", "pw"); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	permDelGrant(t, s.AuthManager, "alice", "blog", false)
	permDelGrant(t, s.AuthManager, "alice", "docs", false)

	cases := []struct {
		name, target, actor string
		admin               bool
		want                int
	}{
		{"non-admin", "/v1/auth/permissions?username=alice&collection=blog", "alice", false, http.StatusForbidden},
		{"no username", "/v1/auth/permissions?collection=blog", "admin", true, http.StatusBadRequest},
		{"no collection", "/v1/auth/permissions?username=alice", "admin", true, http.StatusBadRequest},
		{"own global admin", "/v1/auth/permissions?username=admin&collection=%2A", "admin", true, http.StatusConflict},
		{"removed", "/v1/auth/permissions?username=alice&collection=blog", "admin", true, http.StatusOK},
		{"already gone", "/v1/auth/permissions?username=alice&collection=blog", "admin", true, http.StatusNotFound},
		{"unknown user", "/v1/auth/permissions?username=nobody&collection=blog", "admin", true, http.StatusNotFound},
	}
	for _, tc := range cases {
		w := permDelRequest(s.handleAuthPermissions, http.MethodDelete, tc.target, tc.actor, tc.admin)
		if w.Code != tc.want {
			t.Errorf("%s: status %d, want %d: %s", tc.name, w.Code, tc.want, w.Body.String())
		}
	}
	if got := permDelCollections(s.AuthManager.GetPermissions("alice")); len(got) != 1 || got[0] != "docs" {
		t.Errorf("alice's grants = %v, want [docs]", got)
	}
	if !s.AuthManager.IsAdmin("admin") {
		t.Error("the 409 still removed the admin's global admin")
	}
}

func TestHandleDeleteGroupPermission(t *testing.T) {
	s, _, cleanup := setupTestServer(t)
	defer cleanup()
	permDelGroup(t, s.AuthManager, "editors", nil, "blog", "docs")

	cases := []struct {
		name, target string
		admin        bool
		want         int
	}{
		{"non-admin", "/v1/auth/group-permissions?group=editors&collection=blog", false, http.StatusForbidden},
		{"no group", "/v1/auth/group-permissions?collection=blog", true, http.StatusBadRequest},
		{"no collection", "/v1/auth/group-permissions?group=editors", true, http.StatusBadRequest},
		{"removed", "/v1/auth/group-permissions?group=editors&collection=blog", true, http.StatusOK},
		{"already gone", "/v1/auth/group-permissions?group=editors&collection=blog", true, http.StatusNotFound},
		{"unknown group", "/v1/auth/group-permissions?group=nobody&collection=blog", true, http.StatusNotFound},
	}
	for _, tc := range cases {
		w := permDelRequest(s.handleAuthGroupPermissions, http.MethodDelete, tc.target, "admin", tc.admin)
		if w.Code != tc.want {
			t.Errorf("%s: status %d, want %d: %s", tc.name, w.Code, tc.want, w.Body.String())
		}
	}
	if got := permDelCollections(s.AuthManager.GetGroupPermissions("editors")); len(got) != 1 || got[0] != "docs" {
		t.Errorf("editors' grants = %v, want [docs]", got)
	}
}

func TestHandleDeletePermission_StorageFailureIs500(t *testing.T) {
	s, _, cleanup := setupTestServer(t)
	cleanup()
	for _, tc := range []struct {
		handler http.HandlerFunc
		target  string
	}{
		{s.handleAuthPermissions, "/v1/auth/permissions?username=alice&collection=blog"},
		{s.handleAuthGroupPermissions, "/v1/auth/group-permissions?group=editors&collection=blog"},
	} {
		if w := permDelRequest(tc.handler, http.MethodDelete, tc.target, "admin", true); w.Code != http.StatusInternalServerError {
			t.Errorf("%s: status %d, want 500: %s", tc.target, w.Code, w.Body.String())
		}
	}
}

// permDelGate is a route behind the auth middleware that serves a read of
// "blog" only to a caller allowed it, and an admin-only route.
func permDelGate(s *Server) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/read", func(w http.ResponseWriter, r *http.Request) {
		if err := s.AuthManager.CheckPermission(r.Context(), "blog", PermRead); err != nil {
			http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/v1/auth/users", s.handleAuthUsersList)
	return s.AuthManager.HTTPMiddleware(mux)
}

// permDelDo sends one request with the given credential header.
func permDelDo(h http.Handler, path, header, value string) int {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Header.Set(header, value)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w.Code
}

// A grant removed after a token was issued no longer applies to that token:
// the next request with it is refused (#438). Covers a login JWT and an API
// key, on a user grant, a group grant and a global admin grant.
func TestDeletedGrantStopsApplyingToIssuedTokens(t *testing.T) {
	s, _, cleanup := setupTestServer(t)
	defer cleanup()
	_ = createAdminAndGetToken(t, s)
	am := s.AuthManager
	for _, u := range []string{"alice", "carol", "bob"} {
		if _, err := am.CreateUser(u, "pw"); err != nil {
			t.Fatalf("CreateUser %s: %v", u, err)
		}
	}
	permDelGrant(t, am, "alice", "blog", false)
	permDelGroup(t, am, "readers", []string{"carol"}, "blog")
	permDelGrant(t, am, "bob", "*", true)

	jwtOf := func(user string) string {
		tok, err := GenerateTenantJWT(user, "", am.IsAdmin(user), am.config.JWTSecret, am.config.JWTExpiry)
		if err != nil {
			t.Fatalf("GenerateTenantJWT: %v", err)
		}
		return "Bearer " + tok
	}
	aliceJWT, carolJWT, bobJWT := jwtOf("alice"), jwtOf("carol"), jwtOf("bob")
	aliceKey, err := am.CreateAPIKey("alice", "ci", 0)
	if err != nil {
		t.Fatalf("CreateAPIKey: %v", err)
	}
	h := permDelGate(s)

	before := map[string]int{
		"alice jwt": permDelDo(h, "/read", "Authorization", aliceJWT),
		"alice key": permDelDo(h, "/read", "X-API-Key", aliceKey),
		"carol jwt": permDelDo(h, "/read", "Authorization", carolJWT),
		"bob admin": permDelDo(h, "/v1/auth/users", "Authorization", bobJWT),
	}
	for name, code := range before {
		if code != http.StatusOK {
			t.Fatalf("%s before the removal: status %d, want 200", name, code)
		}
	}

	if err := am.DeletePermission("alice", "blog"); err != nil {
		t.Fatalf("DeletePermission alice: %v", err)
	}
	if err := am.DeleteGroupPermission("readers", "blog"); err != nil {
		t.Fatalf("DeleteGroupPermission: %v", err)
	}
	if err := am.DeletePermission("bob", "*"); err != nil {
		t.Fatalf("DeletePermission bob: %v", err)
	}

	after := map[string]int{
		"alice jwt": permDelDo(h, "/read", "Authorization", aliceJWT),
		"alice key": permDelDo(h, "/read", "X-API-Key", aliceKey),
		"carol jwt": permDelDo(h, "/read", "Authorization", carolJWT),
		"bob admin": permDelDo(h, "/v1/auth/users", "Authorization", bobJWT),
		"bob read":  permDelDo(h, "/read", "Authorization", bobJWT),
	}
	for name, code := range after {
		if code != http.StatusForbidden {
			t.Errorf("%s after the removal: status %d, want 403", name, code)
		}
	}
}

// The gRPC interceptor narrows a token's Admin flag the same way.
func TestGRPCInterceptor_DeletedGlobalAdminStopsApplying(t *testing.T) {
	am, cleanup := authGrpcSetup(t)
	defer cleanup()
	token, err := GenerateJWT("admin", true, am.config.JWTSecret, am.config.JWTExpiry)
	if err != nil {
		t.Fatalf("GenerateJWT: %v", err)
	}
	if err := am.DeletePermission("admin", "*"); err != nil {
		t.Fatalf("DeletePermission: %v", err)
	}
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "Bearer "+token))
	var admin bool
	_, err = am.GRPCUnaryInterceptor()(ctx, nil, &grpc.UnaryServerInfo{FullMethod: "/mddb.MDDB/Stats"},
		func(ctx context.Context, _ interface{}) (interface{}, error) {
			claims, _ := GetClaimsFromContext(ctx)
			admin = claims.Admin
			return nil, nil
		})
	if err != nil {
		t.Fatalf("interceptor: %v", err)
	}
	if admin {
		t.Error("a token issued before the removal still carries Admin")
	}
}

func TestGraphQLDeletePermissions(t *testing.T) {
	a, ctx, cleanup := gqlAdapterWithAuth(t)
	defer cleanup()
	am := a.server.AuthManager
	if _, err := am.CreateUser("alice", "pw"); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	permDelGrant(t, am, "alice", "blog", false)
	permDelGroup(t, am, "editors", nil, "blog")

	if err := a.DeletePermission(ctx, "alice", "blog"); err != nil {
		t.Fatalf("DeletePermission: %v", err)
	}
	if len(am.GetPermissions("alice")) != 0 {
		t.Error("alice's grant is still there")
	}
	if err := a.DeletePermission(ctx, "admin", "*"); !errors.Is(err, ErrOwnGlobalAdmin) {
		t.Errorf("removing own global admin = %v, want ErrOwnGlobalAdmin", err)
	}
	if err := a.DeleteGroupPermission(ctx, "editors", "blog"); err != nil {
		t.Fatalf("DeleteGroupPermission: %v", err)
	}
	if err := a.DeleteGroupPermission(ctx, "editors", "blog"); !errors.Is(err, ErrPermissionNotFound) {
		t.Errorf("second group delete = %v, want ErrPermissionNotFound", err)
	}

	user := context.WithValue(context.Background(), authContextKey, &JWTClaims{Username: "alice"})
	if err := a.DeletePermission(user, "alice", "docs"); !errors.Is(err, ErrAdminRequired) {
		t.Errorf("non-admin DeletePermission = %v, want ErrAdminRequired", err)
	}
	if err := a.DeleteGroupPermission(user, "editors", "docs"); !errors.Is(err, ErrAdminRequired) {
		t.Errorf("non-admin DeleteGroupPermission = %v, want ErrAdminRequired", err)
	}
}

func TestGraphQLDeletePermissions_AuthDisabled(t *testing.T) {
	a, cleanup := gqlAdapterFromServer(t)
	defer cleanup()
	ctx := context.Background()
	if err := a.DeletePermission(ctx, "alice", "blog"); !errors.Is(err, ErrAuthNotEnabled) {
		t.Errorf("DeletePermission = %v, want ErrAuthNotEnabled", err)
	}
	if err := a.DeleteGroupPermission(ctx, "editors", "blog"); !errors.Is(err, ErrAuthNotEnabled) {
		t.Errorf("DeleteGroupPermission = %v, want ErrAuthNotEnabled", err)
	}
}
