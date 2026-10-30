package main

import (
	"errors"
	"strings"
	"testing"

	bolt "go.etcd.io/bbolt"
	bolterrors "go.etcd.io/bbolt/errors"
)

// Error paths of AuthManager: corrupt records on load, a closed database, and
// keys the manager expects to be values but finds as nested buckets. Each test
// checks that the failure is reported and that the in-memory cache does not
// claim a change the database refused.

// putAuthRaw writes value under key in bucket, bypassing AuthManager.
func putAuthRaw(t *testing.T, db *bolt.DB, bucket []byte, key, value string) {
	t.Helper()
	if err := db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket(bucket).Put([]byte(key), []byte(value))
	}); err != nil {
		t.Fatalf("put %s/%s: %v", bucket, key, err)
	}
}

// blockAuthKey replaces key in bucket with a nested bucket, so a Put or Delete
// of that key fails with ErrIncompatibleValue inside the manager's transaction.
func blockAuthKey(t *testing.T, db *bolt.DB, bucket []byte, key string) {
	t.Helper()
	if err := db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucket)
		if err := b.Delete([]byte(key)); err != nil {
			return err
		}
		_, err := b.CreateBucket([]byte(key))
		return err
	}); err != nil {
		t.Fatalf("nested bucket %s/%s: %v", bucket, key, err)
	}
}

// TestSetServerOnNilManagerIsNoop proves SetServer tolerates a nil receiver
// (servers built without auth) and wires the server on a real one.
func TestSetServerOnNilManagerIsNoop(t *testing.T) {
	var none *AuthManager
	none.SetServer(&Server{}) // must not panic

	am, _, cleanup := setupTestAuthManager(t)
	defer cleanup()
	s := &Server{}
	am.SetServer(s)
	if am.server != s {
		t.Error("SetServer did not wire the server")
	}
}

// TestLoadAllSkipsCorruptRecords proves one unreadable record in any auth
// bucket is skipped rather than failing the load or dropping its neighbours.
func TestLoadAllSkipsCorruptRecords(t *testing.T) {
	am, db, cleanup := setupTestAuthManager(t)
	defer cleanup()

	putAuthRaw(t, db, bucketAuthUsers, "user|ok", `{"username":"ok"}`)
	putAuthRaw(t, db, bucketAuthAPIKeys, "apikey|h", `{"keyHash":"h","username":"ok"}`)
	putAuthRaw(t, db, bucketAuthPerms, "perm|ok|c", `{"username":"ok","collection":"c","read":true}`)
	putAuthRaw(t, db, bucketAuthGroups, "g", `{"name":"g","members":["ok"]}`)
	putAuthRaw(t, db, bucketAuthGroupPerms, "g|c", `{"groupName":"g","collection":"c","read":true}`)
	for _, b := range [][]byte{bucketAuthUsers, bucketAuthAPIKeys, bucketAuthPerms, bucketAuthGroups, bucketAuthGroupPerms} {
		putAuthRaw(t, db, b, "zz-corrupt", "{not json")
	}

	if err := am.LoadAll(); err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	if n := len(am.ListAllUsers()); n != 1 {
		t.Errorf("users = %d, want 1", n)
	}
	if keys, _ := am.ListAPIKeys("ok"); len(keys) != 1 {
		t.Errorf("api keys = %d, want 1", len(keys))
	}
	if n := len(am.GetPermissions("ok")); n != 1 {
		t.Errorf("permissions = %d, want 1", n)
	}
	if n := len(am.ListGroups()); n != 1 {
		t.Errorf("groups = %d, want 1", n)
	}
	if n := len(am.GetGroupPermissions("g")); n != 1 {
		t.Errorf("group permissions = %d, want 1", n)
	}
}

// TestLoadAllOnClosedDatabaseKeepsCache proves a failed load reports the error
// and leaves the cache as it was instead of emptying it.
func TestLoadAllOnClosedDatabaseKeepsCache(t *testing.T) {
	am, db, cleanup := setupTestAuthManager(t)
	defer cleanup()
	if _, err := am.CreateUser("kept", "pw"); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	_ = db.Close()

	if err := am.LoadAll(); !errors.Is(err, bolterrors.ErrDatabaseNotOpen) {
		t.Fatalf("LoadAll = %v, want ErrDatabaseNotOpen", err)
	}
	if _, err := am.GetUser("kept"); err != nil {
		t.Errorf("cache lost a user after a failed load: %v", err)
	}
}

// TestBootstrapAdminReportsUserFailure proves a database that refuses the
// admin record fails bootstrap with a wrapped error and caches no admin.
func TestBootstrapAdminReportsUserFailure(t *testing.T) {
	am, db, cleanup := setupTestAuthManager(t)
	defer cleanup()
	_ = db.Close()

	err := am.BootstrapAdmin()
	if !errors.Is(err, bolterrors.ErrDatabaseNotOpen) || !strings.HasPrefix(err.Error(), "bootstrap admin:") {
		t.Fatalf("BootstrapAdmin = %v, want wrapped ErrDatabaseNotOpen", err)
	}
	if _, err := am.GetUser("admin"); err == nil {
		t.Error("admin cached although it was never stored")
	}
}

// TestBootstrapAdminReportsPermissionFailure proves bootstrap fails, rather
// than leaving a powerless admin unnoticed, when the "*" grant cannot be stored.
func TestBootstrapAdminReportsPermissionFailure(t *testing.T) {
	am, db, cleanup := setupTestAuthManager(t)
	defer cleanup()
	blockAuthKey(t, db, bucketAuthPerms, "perm|admin|*")

	err := am.BootstrapAdmin()
	if !errors.Is(err, bolterrors.ErrIncompatibleValue) || !strings.HasPrefix(err.Error(), "bootstrap admin permissions:") {
		t.Fatalf("BootstrapAdmin = %v, want wrapped ErrIncompatibleValue", err)
	}
	if am.IsAdmin("admin") {
		t.Error("admin is global admin although the grant was refused")
	}
}

// TestCreateTenantUserRejects proves the input and storage checks of
// CreateTenantUser: a bad tenant name, an unhashable password and a closed
// database all fail and none of them caches a user.
func TestCreateTenantUserRejects(t *testing.T) {
	am, db, cleanup := setupTestAuthManager(t)
	defer cleanup()

	if _, err := am.CreateTenantUser("u", "pw", "Bad Tenant!"); !errors.Is(err, ErrInvalidTenant) {
		t.Errorf("invalid tenant: err = %v, want ErrInvalidTenant", err)
	}
	// bcrypt refuses passwords longer than 72 bytes.
	if _, err := am.CreateUser("u", strings.Repeat("p", 73)); err == nil || !strings.HasPrefix(err.Error(), "hash password:") {
		t.Errorf("long password: err = %v, want hash password error", err)
	}
	_ = db.Close()
	if _, err := am.CreateUser("u", "pw"); !errors.Is(err, bolterrors.ErrDatabaseNotOpen) {
		t.Errorf("closed db: err = %v, want ErrDatabaseNotOpen", err)
	}
	if _, err := am.GetUser("u"); err == nil {
		t.Error("a refused user was cached")
	}
}

// TestAPIKeyStorageFailures proves CreateAPIKey and DeleteAPIKey report a
// closed database and leave the cache matching what was stored.
func TestAPIKeyStorageFailures(t *testing.T) {
	am, db, cleanup := setupTestAuthManager(t)
	defer cleanup()
	if _, err := am.CreateUser("k", "pw"); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	key, err := am.CreateAPIKey("k", "", 0)
	if err != nil {
		t.Fatalf("CreateAPIKey: %v", err)
	}
	_ = db.Close()

	if _, err := am.CreateAPIKey("k", "", 0); !errors.Is(err, bolterrors.ErrDatabaseNotOpen) {
		t.Errorf("CreateAPIKey = %v, want ErrDatabaseNotOpen", err)
	}
	if keys, _ := am.ListAPIKeys("k"); len(keys) != 1 {
		t.Errorf("api keys = %d, want 1 (refused key cached)", len(keys))
	}
	if err := am.DeleteAPIKey("k", HashAPIKey(key)); !errors.Is(err, bolterrors.ErrDatabaseNotOpen) {
		t.Errorf("DeleteAPIKey = %v, want ErrDatabaseNotOpen", err)
	}
	if _, err := am.ValidateAPIKey(key); err != nil {
		t.Errorf("key dropped from cache although the delete failed: %v", err)
	}
}

// TestSetPermissionFailures proves SetPermission refuses unknown users and
// reports a closed database without caching the grant.
func TestSetPermissionFailures(t *testing.T) {
	am, db, cleanup := setupTestAuthManager(t)
	defer cleanup()

	if err := am.SetPermission(&Permission{Username: "ghost", Collection: "c"}); !errors.Is(err, ErrUserNotFound) {
		t.Errorf("unknown user: err = %v, want ErrUserNotFound", err)
	}
	if _, err := am.CreateUser("p", "pw"); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	_ = db.Close()
	if err := am.SetPermission(&Permission{Username: "p", Collection: "c", Read: true}); !errors.Is(err, bolterrors.ErrDatabaseNotOpen) {
		t.Errorf("closed db: err = %v, want ErrDatabaseNotOpen", err)
	}
	if n := len(am.GetPermissions("p")); n != 0 {
		t.Errorf("permissions = %d, want 0", n)
	}
}
