package main

import (
	"errors"
	"testing"

	bolt "go.etcd.io/bbolt"
	bolterrors "go.etcd.io/bbolt/errors"
)

// Error and permission paths of AuthManager that involve DeleteUser, groups
// and CheckPermission. Helpers live in auth_manager_cov_test.go.

// TestDeleteUserAbortsOnStorageFailure proves DeleteUser is all-or-nothing:
// when any record it must remove or rewrite cannot be, the error is returned
// and the user stays in the cache.
func TestDeleteUserAbortsOnStorageFailure(t *testing.T) {
	cases := map[string]func(*testing.T, *AuthManager){
		"user record": func(t *testing.T, am *AuthManager) {
			blockAuthKey(t, am.db, bucketAuthUsers, "user|v")
		},
		"api key": func(t *testing.T, am *AuthManager) {
			key, err := am.CreateAPIKey("v", "", 0)
			if err != nil {
				t.Fatalf("CreateAPIKey: %v", err)
			}
			blockAuthKey(t, am.db, bucketAuthAPIKeys, "apikey|"+HashAPIKey(key))
		},
		"permission": func(t *testing.T, am *AuthManager) {
			blockAuthKey(t, am.db, bucketAuthPerms, "perm|v|c")
		},
	}
	for name, arrange := range cases {
		t.Run(name, func(t *testing.T) {
			am, _, cleanup := setupTestAuthManager(t)
			defer cleanup()
			if _, err := am.CreateUser("v", "pw"); err != nil {
				t.Fatalf("CreateUser: %v", err)
			}
			arrange(t, am)
			if err := am.DeleteUser("v"); !errors.Is(err, bolterrors.ErrIncompatibleValue) {
				t.Fatalf("DeleteUser = %v, want ErrIncompatibleValue", err)
			}
			if _, err := am.GetUser("v"); err != nil {
				t.Errorf("user dropped from cache after a failed delete: %v", err)
			}
		})
	}
}

// TestDeleteUserGroupRecords proves DeleteUser fails on a corrupt group record
// it must rewrite, and skips a cached group whose record is already gone.
func TestDeleteUserGroupRecords(t *testing.T) {
	am, db, cleanup := setupTestAuthManager(t)
	defer cleanup()
	if _, err := am.CreateUser("v", "pw"); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if _, err := am.CreateGroup("g", "", []string{"v"}); err != nil {
		t.Fatalf("CreateGroup: %v", err)
	}
	putAuthRaw(t, db, bucketAuthGroups, "g", "{not json")
	if err := am.DeleteUser("v"); err == nil {
		t.Fatal("DeleteUser succeeded over a corrupt group record")
	}

	if err := db.Update(func(tx *bolt.Tx) error { return tx.Bucket(bucketAuthGroups).Delete([]byte("g")) }); err != nil {
		t.Fatalf("delete group record: %v", err)
	}
	if err := am.DeleteUser("v"); err != nil {
		t.Fatalf("DeleteUser with the group record gone: %v", err)
	}
	if g, _ := am.GetGroup("g"); len(g.Members) != 0 {
		t.Errorf("cached group still lists %v", g.Members)
	}
}

// TestGroupStorageFailures proves every group mutation validates its input
// and reports a database that refuses the write.
func TestGroupStorageFailures(t *testing.T) {
	am, db, cleanup := setupTestAuthManager(t)
	defer cleanup()

	if _, err := am.CreateGroup("", "", nil); err == nil {
		t.Error("CreateGroup accepted an empty name")
	}
	if _, err := am.UpdateGroup("none", "", nil); err == nil {
		t.Error("UpdateGroup accepted an unknown group")
	}
	if err := am.DeleteGroup("none"); err == nil {
		t.Error("DeleteGroup accepted an unknown group")
	}
	if err := am.SetGroupPermission(&GroupPermission{GroupName: "g"}); err == nil {
		t.Error("SetGroupPermission accepted an empty collection")
	}
	if err := am.SetGroupPermission(&GroupPermission{GroupName: "none", Collection: "c"}); err == nil {
		t.Error("SetGroupPermission accepted an unknown group")
	}

	if _, err := am.CreateGroup("g", "", nil); err != nil {
		t.Fatalf("CreateGroup: %v", err)
	}
	_ = db.Close()
	if _, err := am.CreateGroup("h", "", nil); !errors.Is(err, bolterrors.ErrDatabaseNotOpen) {
		t.Errorf("CreateGroup = %v, want ErrDatabaseNotOpen", err)
	}
	if _, err := am.UpdateGroup("g", "d", nil); !errors.Is(err, bolterrors.ErrDatabaseNotOpen) {
		t.Errorf("UpdateGroup = %v, want ErrDatabaseNotOpen", err)
	}
	if err := am.DeleteGroup("g"); !errors.Is(err, bolterrors.ErrDatabaseNotOpen) {
		t.Errorf("DeleteGroup = %v, want ErrDatabaseNotOpen", err)
	}
	if err := am.SetGroupPermission(&GroupPermission{GroupName: "g", Collection: "c"}); !errors.Is(err, bolterrors.ErrDatabaseNotOpen) {
		t.Errorf("SetGroupPermission = %v, want ErrDatabaseNotOpen", err)
	}
	if _, err := am.GetGroup("g"); err != nil {
		t.Errorf("group dropped from cache after a failed delete: %v", err)
	}
	if n := len(am.GetGroupPermissions("g")); n != 0 {
		t.Errorf("group permissions = %d, want 0", n)
	}
}

// TestDeleteGroupAbortsOnStorageFailure proves DeleteGroup keeps the group
// when its record or one of its permission records cannot be removed.
func TestDeleteGroupAbortsOnStorageFailure(t *testing.T) {
	blocked := map[string]struct {
		bucket []byte
		key    string
	}{
		"group record":     {bucketAuthGroups, "g"},
		"group permission": {bucketAuthGroupPerms, "g|c"},
	}
	for name, b := range blocked {
		t.Run(name, func(t *testing.T) {
			am, db, cleanup := setupTestAuthManager(t)
			defer cleanup()
			if _, err := am.CreateGroup("g", "", nil); err != nil {
				t.Fatalf("CreateGroup: %v", err)
			}
			blockAuthKey(t, db, b.bucket, b.key)
			if err := am.DeleteGroup("g"); !errors.Is(err, bolterrors.ErrIncompatibleValue) {
				t.Fatalf("DeleteGroup = %v, want ErrIncompatibleValue", err)
			}
			if _, err := am.GetGroup("g"); err != nil {
				t.Errorf("group dropped from cache after a failed delete: %v", err)
			}
		})
	}
}

// TestSetGroupPermissionReplacesExisting proves a second grant on the same
// collection replaces the first instead of adding a duplicate.
func TestSetGroupPermissionReplacesExisting(t *testing.T) {
	am, _, cleanup := setupTestAuthManager(t)
	defer cleanup()
	if _, err := am.CreateGroup("g", "", nil); err != nil {
		t.Fatalf("CreateGroup: %v", err)
	}
	for _, write := range []bool{false, true} {
		if err := am.SetGroupPermission(&GroupPermission{GroupName: "g", Collection: "c", Read: true, Write: write}); err != nil {
			t.Fatalf("SetGroupPermission: %v", err)
		}
	}
	perms := am.GetGroupPermissions("g")
	if len(perms) != 1 || !perms[0].Write {
		t.Errorf("group permissions = %+v, want one replaced write grant", perms)
	}
}

// DeleteGroup sliced each key under the group's prefix before checking its
// length: deleting "longname" panicked when the next permission key ("z|c")
// was shorter than "longname|". Only the group's own permissions go, the
// other group keeps its grant.
func TestDeleteGroupWithShorterNeighbourKey(t *testing.T) {
	am, _, cleanup := setupTestAuthManager(t)
	defer cleanup()
	for _, g := range []string{"longname", "z"} {
		if _, err := am.CreateGroup(g, "", nil); err != nil {
			t.Fatalf("CreateGroup %s: %v", g, err)
		}
	}
	for _, gp := range []*GroupPermission{{GroupName: "longname", Collection: "a", Read: true}, {GroupName: "z", Collection: "c", Read: true}} {
		if err := am.SetGroupPermission(gp); err != nil {
			t.Fatalf("SetGroupPermission: %v", err)
		}
	}
	if err := am.DeleteGroup("longname"); err != nil {
		t.Fatalf("DeleteGroup: %v", err)
	}
	if err := am.LoadAll(); err != nil {
		t.Fatal(err)
	}
	if got := am.GetGroupPermissions("longname"); len(got) != 0 {
		t.Errorf("longname's permissions survived: %+v", got)
	}
	if got := am.GetGroupPermissions("z"); len(got) != 1 {
		t.Errorf("z's permission = %+v, want it kept", got)
	}
}
