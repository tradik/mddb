package main

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	bolt "go.etcd.io/bbolt"
)

// Access decisions of AuthManager that the other suites leave out: the
// CheckPermission gates, credentials of a disabled user, tenants, bootstrap
// without credentials and the followed database handle.

// TestCheckPermissionGates proves the decisions CheckPermission makes before
// and around group grants: auth off allows, tenants are confined to their
// namespace and to reads on "*", groups the user is not in grant nothing, and
// a group's "*" grant applies to every collection.
func TestCheckPermissionGates(t *testing.T) {
	am, _, cleanup := setupTestAuthManager(t)
	defer cleanup()
	for _, name := range []string{"member", "outsider"} {
		if _, err := am.CreateUser(name, "pw"); err != nil {
			t.Fatalf("CreateUser: %v", err)
		}
	}
	if _, err := am.CreateGroup("others", "", []string{"outsider"}); err != nil {
		t.Fatalf("CreateGroup: %v", err)
	}
	if _, err := am.CreateGroup("readers", "", []string{"outsider", "member"}); err != nil {
		t.Fatalf("CreateGroup: %v", err)
	}
	if err := am.SetGroupPermission(&GroupPermission{GroupName: "others", Collection: "*", Read: true, Write: true}); err != nil {
		t.Fatalf("SetGroupPermission: %v", err)
	}
	if err := am.SetGroupPermission(&GroupPermission{GroupName: "readers", Collection: "*", Read: true}); err != nil {
		t.Fatalf("SetGroupPermission: %v", err)
	}
	ctxFor := func(c *JWTClaims) context.Context {
		return context.WithValue(context.Background(), authContextKey, c)
	}
	member := ctxFor(&JWTClaims{Username: "member"})
	tenant := ctxFor(&JWTClaims{Username: "member", Tenant: "acme"})

	checks := []struct {
		name string
		ctx  context.Context
		coll string
		op   PermissionType
		want error
	}{
		{"group wildcard read", member, "any", PermRead, nil},
		{"other group's write does not leak", member, "any", PermWrite, ErrForbidden},
		{"tenant write on *", tenant, "*", PermWrite, ErrForbidden},
		{"tenant read on *", tenant, "*", PermRead, nil},
		{"tenant outside namespace", tenant, "other/c", PermRead, ErrForbidden},
		{"tenant inside namespace", tenant, "acme/c", PermRead, nil},
	}
	for _, c := range checks {
		if err := am.CheckPermission(c.ctx, c.coll, c.op); !errors.Is(err, c.want) {
			t.Errorf("%s: err = %v, want %v", c.name, err, c.want)
		}
	}

	am.enabled = false
	if err := am.CheckPermission(context.Background(), "any", PermAdmin); err != nil {
		t.Errorf("auth disabled: err = %v, want nil", err)
	}
}

// TestUserLookupEdges proves the answers about a single user: a disabled user
// cannot log in with the right password, a tenant user reports its tenant, an
// unknown one reports none, and deleting an unknown user is ErrUserNotFound.
func TestUserLookupEdges(t *testing.T) {
	am, _, cleanup := setupTestAuthManager(t)
	defer cleanup()

	u, err := am.CreateTenantUser("t", "pw", "acme")
	if err != nil {
		t.Fatalf("CreateTenantUser: %v", err)
	}
	if got := am.UserTenant("t"); got != "acme" {
		t.Errorf("UserTenant(t) = %q, want acme", got)
	}
	if got := am.UserTenant("ghost"); got != "" {
		t.Errorf("UserTenant(unknown) = %q, want empty", got)
	}
	if err := am.DeleteUser("ghost"); !errors.Is(err, ErrUserNotFound) {
		t.Errorf("DeleteUser(unknown) = %v, want ErrUserNotFound", err)
	}

	am.mu.Lock()
	u.Disabled = true
	am.mu.Unlock()
	if _, err := am.Authenticate("t", "pw"); !errors.Is(err, ErrInvalidCredentials) {
		t.Errorf("Authenticate(disabled) = %v, want ErrInvalidCredentials", err)
	}
}

// TestBootstrapAdminWithoutCredentialsIsNoop proves bootstrap creates nothing
// when no admin username or password is configured.
func TestBootstrapAdminWithoutCredentialsIsNoop(t *testing.T) {
	am, _, cleanup := setupTestAuthManager(t)
	defer cleanup()
	am.config.AdminPassword = ""

	if err := am.BootstrapAdmin(); err != nil {
		t.Fatalf("BootstrapAdmin: %v", err)
	}
	if n := len(am.ListAllUsers()); n != 0 {
		t.Errorf("users = %d, want 0", n)
	}
}

// TestFollowDBWritesToCurrentHandle proves that after FollowDB the manager
// writes to the handle the source returns, not the one it was built with.
func TestFollowDBWritesToCurrentHandle(t *testing.T) {
	am, original, cleanup := setupTestAuthManager(t)
	defer cleanup()

	current, err := bolt.Open(filepath.Join(t.TempDir(), "current.db"), 0600, &bolt.Options{Timeout: time.Second})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = current.Close() }()
	am.FollowDB(func() *bolt.DB { return current })
	if err := am.EnsureBuckets(); err != nil {
		t.Fatalf("EnsureBuckets: %v", err)
	}
	if _, err := am.CreateUser("moved", "pw"); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	has := func(db *bolt.DB) bool {
		found := false
		_ = db.View(func(tx *bolt.Tx) error {
			b := tx.Bucket(bucketAuthUsers)
			found = b != nil && b.Get([]byte("user|moved")) != nil
			return nil
		})
		return found
	}
	if !has(current) || has(original) {
		t.Errorf("user stored in current=%v original=%v, want current only", has(current), has(original))
	}
}
