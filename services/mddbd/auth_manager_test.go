package main

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	bolt "go.etcd.io/bbolt"
)

func setupTestAuthManager(t *testing.T) (*AuthManager, *bolt.DB, func()) {
	// Create temp database
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
		t.Fatalf("Failed to open test database: %v", err)
	}

	config := AuthConfig{
		JWTSecret:     "test-secret-key-12345678901234567890",
		JWTExpiry:     24 * time.Hour,
		AdminUsername: "admin",
		AdminPassword: "changeme",
	}

	am := NewAuthManager(db, config)

	if err := am.EnsureBuckets(); err != nil {
		t.Fatalf("Failed to ensure buckets: %v", err)
	}

	cleanup := func() {
		_ = db.Close()
	}

	return am, db, cleanup
}

func TestNewAuthManager(t *testing.T) {
	am, _, cleanup := setupTestAuthManager(t)
	defer cleanup()

	if am == nil {
		t.Fatal("NewAuthManager returned nil")
	}

	if !am.IsEnabled() {
		t.Error("AuthManager should be enabled")
	}
}

func TestAuthManager_EnsureBuckets(t *testing.T) {
	_, db, cleanup := setupTestAuthManager(t)
	defer cleanup()

	// Verify buckets exist
	err := db.View(func(tx *bolt.Tx) error {
		if tx.Bucket([]byte("auth_users")) == nil {
			t.Error("auth_users bucket not created")
		}
		if tx.Bucket([]byte("auth_apikeys")) == nil {
			t.Error("auth_apikeys bucket not created")
		}
		if tx.Bucket([]byte("auth_permissions")) == nil {
			t.Error("auth_permissions bucket not created")
		}
		return nil
	})

	if err != nil {
		t.Fatalf("Failed to verify buckets: %v", err)
	}
}

func TestAuthManager_CreateUser(t *testing.T) {
	am, _, cleanup := setupTestAuthManager(t)
	defer cleanup()

	username := "testuser"
	password := "password123"

	// Create user
	user, err := am.CreateUser(username, password)
	if err != nil {
		t.Fatalf("CreateUser failed: %v", err)
	}

	if user.Username != username {
		t.Errorf("Username = %s, want %s", user.Username, username)
	}

	if user.PasswordHash == "" {
		t.Error("PasswordHash should not be empty")
	}

	if user.PasswordHash == password {
		t.Error("Password should be hashed")
	}

	if user.Disabled {
		t.Error("User should not be disabled by default")
	}

	// Try to create duplicate user
	_, err = am.CreateUser(username, password)
	if err != ErrUserExists {
		t.Fatalf("CreateUser should fail with ErrUserExists for duplicate, got: %v", err)
	}
}

func TestAuthManager_CreateUser_EmptyFields(t *testing.T) {
	am, _, cleanup := setupTestAuthManager(t)
	defer cleanup()

	tests := []struct {
		name     string
		username string
		password string
	}{
		{"empty username", "", "password123"},
		{"empty password", "testuser", ""},
		{"both empty", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := am.CreateUser(tt.username, tt.password)
			if err == nil {
				t.Error("CreateUser should fail with empty fields")
			}
		})
	}
}

func TestAuthManager_Authenticate(t *testing.T) {
	am, _, cleanup := setupTestAuthManager(t)
	defer cleanup()

	username := "testuser"
	password := "password123"

	// Create user
	_, err := am.CreateUser(username, password)
	if err != nil {
		t.Fatalf("CreateUser failed: %v", err)
	}

	// Test correct password
	user, err := am.Authenticate(username, password)
	if err != nil {
		t.Fatalf("Authenticate failed with correct password: %v", err)
	}

	if user.Username != username {
		t.Errorf("Authenticated user = %s, want %s", user.Username, username)
	}

	// Test wrong password
	_, err = am.Authenticate(username, "wrongpassword")
	if err != ErrInvalidCredentials {
		t.Fatalf("Authenticate should fail with ErrInvalidCredentials, got: %v", err)
	}

	// Test non-existent user
	_, err = am.Authenticate("nonexistent", password)
	if err != ErrInvalidCredentials {
		t.Fatalf("Authenticate should fail for non-existent user, got: %v", err)
	}
}

func TestAuthManager_Authenticate_DisabledUser(t *testing.T) {
	am, _, cleanup := setupTestAuthManager(t)
	defer cleanup()

	username := "testuser"
	password := "password123"

	// Create and disable user
	_, err := am.CreateUser(username, password)
	if err != nil {
		t.Fatalf("CreateUser failed: %v", err)
	}

	err = am.DeleteUser(username)
	if err != nil {
		t.Fatalf("DeleteUser failed: %v", err)
	}

	// Should fail authentication
	_, err = am.Authenticate(username, password)
	if err != ErrInvalidCredentials {
		t.Fatalf("Authenticate should fail for disabled user, got: %v", err)
	}
}

func TestAuthManager_CreateAPIKey(t *testing.T) {
	am, _, cleanup := setupTestAuthManager(t)
	defer cleanup()

	username := "testuser"
	description := "Test API key"

	// Create user first
	_, err := am.CreateUser(username, "password123")
	if err != nil {
		t.Fatalf("CreateUser failed: %v", err)
	}

	// Create API key
	key, err := am.CreateAPIKey(username, description, 0)
	if err != nil {
		t.Fatalf("CreateAPIKey failed: %v", err)
	}

	if key == "" {
		t.Error("CreateAPIKey should return a key")
	}

	if key[:10] != "mddb_live_" {
		t.Errorf("API key has wrong prefix: got %s, want mddb_live_", key[:10])
	}

	// Verify key can be validated
	keyUsername, err := am.ValidateAPIKey(key)
	if err != nil {
		t.Fatalf("ValidateAPIKey failed: %v", err)
	}

	if keyUsername != username {
		t.Errorf("ValidateAPIKey username = %s, want %s", keyUsername, username)
	}
}

func TestAuthManager_CreateAPIKey_NonExistentUser(t *testing.T) {
	am, _, cleanup := setupTestAuthManager(t)
	defer cleanup()

	_, err := am.CreateAPIKey("nonexistent", "Test", 0)
	if err == nil {
		t.Fatal("CreateAPIKey should fail for non-existent user")
	}
}

func TestAuthManager_ValidateAPIKey(t *testing.T) {
	am, _, cleanup := setupTestAuthManager(t)
	defer cleanup()

	username := "testuser"

	// Create user
	_, err := am.CreateUser(username, "password123")
	if err != nil {
		t.Fatalf("CreateUser failed: %v", err)
	}

	// Create API key
	key, err := am.CreateAPIKey(username, "Test", 0)
	if err != nil {
		t.Fatalf("CreateAPIKey failed: %v", err)
	}

	// Test valid key
	resultUsername, err := am.ValidateAPIKey(key)
	if err != nil {
		t.Fatalf("ValidateAPIKey failed for valid key: %v", err)
	}

	if resultUsername != username {
		t.Errorf("ValidateAPIKey username = %s, want %s", resultUsername, username)
	}

	// Test invalid key
	_, err = am.ValidateAPIKey("mddb_live_invalid")
	if err != ErrAPIKeyNotFound {
		t.Fatalf("ValidateAPIKey should fail with ErrAPIKeyNotFound, got: %v", err)
	}

	// Test empty key
	_, err = am.ValidateAPIKey("")
	if err != ErrAPIKeyNotFound {
		t.Fatalf("ValidateAPIKey should fail for empty key, got: %v", err)
	}
}

func TestAuthManager_ValidateAPIKey_Expired(t *testing.T) {
	am, _, cleanup := setupTestAuthManager(t)
	defer cleanup()

	username := "testuser"

	// Create user
	_, err := am.CreateUser(username, "password123")
	if err != nil {
		t.Fatalf("CreateUser failed: %v", err)
	}

	// Create expired API key
	expiresAt := time.Now().Add(-1 * time.Second).Unix()
	key, err := am.CreateAPIKey(username, "Expired key", expiresAt)
	if err != nil {
		t.Fatalf("CreateAPIKey failed: %v", err)
	}

	// Should fail validation
	_, err = am.ValidateAPIKey(key)
	if err != ErrAPIKeyExpired {
		t.Fatalf("ValidateAPIKey should fail with ErrAPIKeyExpired, got: %v", err)
	}
}

func TestAuthManager_SetPermission(t *testing.T) {
	am, _, cleanup := setupTestAuthManager(t)
	defer cleanup()

	username := "testuser"
	collection := "blog"

	// Create user
	_, err := am.CreateUser(username, "password123")
	if err != nil {
		t.Fatalf("CreateUser failed: %v", err)
	}

	// Set permission
	perm := &Permission{
		Username:   username,
		Collection: collection,
		Read:       true,
		Write:      false,
		Admin:      false,
	}

	err = am.SetPermission(perm)
	if err != nil {
		t.Fatalf("SetPermission failed: %v", err)
	}

	// Verify permission
	perms := am.GetPermissions(username)
	if len(perms) != 1 {
		t.Fatalf("Expected 1 permission, got %d", len(perms))
	}

	if perms[0].Collection != collection {
		t.Errorf("Permission collection = %s, want %s", perms[0].Collection, collection)
	}

	if !perms[0].Read {
		t.Error("Permission should have read access")
	}

	if perms[0].Write {
		t.Error("Permission should not have write access")
	}
}

func TestAuthManager_SetPermission_Update(t *testing.T) {
	am, _, cleanup := setupTestAuthManager(t)
	defer cleanup()

	username := "testuser"
	collection := "blog"

	// Create user
	_, err := am.CreateUser(username, "password123")
	if err != nil {
		t.Fatalf("CreateUser failed: %v", err)
	}

	// Set initial permission
	perm := &Permission{
		Username:   username,
		Collection: collection,
		Read:       true,
		Write:      false,
		Admin:      false,
	}
	if err := am.SetPermission(perm); err != nil {
		t.Fatalf("SetPermission failed: %v", err)
	}

	// Update permission
	perm.Write = true
	err = am.SetPermission(perm)
	if err != nil {
		t.Fatalf("SetPermission (update) failed: %v", err)
	}

	perms := am.GetPermissions(username)
	if !perms[0].Write {
		t.Error("Permission should have write access after update")
	}
}

func TestAuthManager_CheckPermission(t *testing.T) {
	am, _, cleanup := setupTestAuthManager(t)
	defer cleanup()

	username := "testuser"
	password := "password123"
	collection := "blog"

	// Create user
	_, err := am.CreateUser(username, password)
	if err != nil {
		t.Fatalf("CreateUser failed: %v", err)
	}

	// Create context with claims
	claims := &JWTClaims{
		Username: username,
		Admin:    false,
	}
	ctx := context.WithValue(context.Background(), authContextKey, claims)

	// No permissions yet - should fail
	err = am.CheckPermission(ctx, collection, PermRead)
	if err != ErrForbidden {
		t.Fatalf("CheckPermission should fail with ErrForbidden, got: %v", err)
	}

	// Grant read permission
	perm := &Permission{
		Username:   username,
		Collection: collection,
		Read:       true,
		Write:      false,
		Admin:      false,
	}
	if err := am.SetPermission(perm); err != nil {
		t.Fatalf("SetPermission failed: %v", err)
	}

	// Load permissions into cache
	if err := am.LoadAll(); err != nil {
		t.Fatalf("LoadAll failed: %v", err)
	}

	// Should succeed for read
	err = am.CheckPermission(ctx, collection, PermRead)
	if err != nil {
		t.Errorf("CheckPermission should succeed for read: %v", err)
	}

	// Should fail for write
	err = am.CheckPermission(ctx, collection, PermWrite)
	if err != ErrForbidden {
		t.Fatalf("CheckPermission should fail for write, got: %v", err)
	}
}

func TestAuthManager_CheckPermission_Wildcard(t *testing.T) {
	am, _, cleanup := setupTestAuthManager(t)
	defer cleanup()

	username := "testuser"

	// Create user
	_, err := am.CreateUser(username, "password123")
	if err != nil {
		t.Fatalf("CreateUser failed: %v", err)
	}

	// Grant wildcard read permission
	perm := &Permission{
		Username:   username,
		Collection: "*",
		Read:       true,
		Write:      false,
		Admin:      false,
	}
	if err := am.SetPermission(perm); err != nil {
		t.Fatalf("SetPermission failed: %v", err)
	}
	if err := am.LoadAll(); err != nil {
		t.Fatalf("LoadAll failed: %v", err)
	}

	claims := &JWTClaims{
		Username: username,
		Admin:    false,
	}
	ctx := context.WithValue(context.Background(), authContextKey, claims)

	// Should succeed for any collection
	collections := []string{"blog", "docs", "products"}
	for _, col := range collections {
		err := am.CheckPermission(ctx, col, PermRead)
		if err != nil {
			t.Errorf("CheckPermission should succeed for collection %s with wildcard: %v", col, err)
		}
	}
}

func TestAuthManager_CheckPermission_Admin(t *testing.T) {
	am, _, cleanup := setupTestAuthManager(t)
	defer cleanup()

	username := "admin"

	// Create user
	_, err := am.CreateUser(username, "password123")
	if err != nil {
		t.Fatalf("CreateUser failed: %v", err)
	}

	// Grant admin permission
	perm := &Permission{
		Username:   username,
		Collection: "*",
		Read:       false,
		Write:      false,
		Admin:      true,
	}
	if err := am.SetPermission(perm); err != nil {
		t.Fatalf("SetPermission failed: %v", err)
	}
	if err := am.LoadAll(); err != nil {
		t.Fatalf("LoadAll failed: %v", err)
	}

	claims := &JWTClaims{
		Username: username,
		Admin:    true, // Admin flag in claims
	}
	ctx := context.WithValue(context.Background(), authContextKey, claims)

	// Admin should bypass all checks
	operations := []PermissionType{PermRead, PermWrite, PermAdmin}
	for _, op := range operations {
		err := am.CheckPermission(ctx, "blog", op)
		if err != nil {
			t.Errorf("CheckPermission should succeed for admin with operation %v: %v", op, err)
		}
	}
}

func TestAuthManager_IsAdmin(t *testing.T) {
	am, _, cleanup := setupTestAuthManager(t)
	defer cleanup()

	username := "testuser"

	// Create user
	_, err := am.CreateUser(username, "password123")
	if err != nil {
		t.Fatalf("CreateUser failed: %v", err)
	}

	// Initially not admin
	if am.IsAdmin(username) {
		t.Error("User should not be admin initially")
	}

	// Grant admin permission
	perm := &Permission{
		Username:   username,
		Collection: "*",
		Read:       false,
		Write:      false,
		Admin:      true,
	}
	if err := am.SetPermission(perm); err != nil {
		t.Fatalf("SetPermission failed: %v", err)
	}
	if err := am.LoadAll(); err != nil {
		t.Fatalf("LoadAll failed: %v", err)
	}

	// Should be admin now
	if !am.IsAdmin(username) {
		t.Error("User should be admin after granting admin permission")
	}
}

func TestAuthManager_DeleteUser(t *testing.T) {
	am, _, cleanup := setupTestAuthManager(t)
	defer cleanup()

	username := "testuser"
	password := "password123"

	// Create user
	_, err := am.CreateUser(username, password)
	if err != nil {
		t.Fatalf("CreateUser failed: %v", err)
	}

	// Verify user exists and not disabled
	user, err := am.GetUser(username)
	if err != nil {
		t.Fatalf("GetUser failed: %v", err)
	}

	if user.Disabled {
		t.Error("User should not be disabled initially")
	}

	// Delete user
	err = am.DeleteUser(username)
	if err != nil {
		t.Fatalf("DeleteUser failed: %v", err)
	}

	// The user is gone, not disabled (#213). It used to stay, marked disabled,
	// which held the name against every later registration while the response
	// said "deleted".
	if _, err = am.GetUser(username); err == nil {
		t.Error("the user still resolves after deletion")
	}

	// Authentication should fail
	_, err = am.Authenticate(username, password)
	if err != ErrInvalidCredentials {
		t.Fatalf("Authenticate should fail for disabled user, got: %v", err)
	}
}

func TestAuthManager_BootstrapAdmin(t *testing.T) {
	am, _, cleanup := setupTestAuthManager(t)
	defer cleanup()

	adminUsername := am.config.AdminUsername
	adminPassword := am.config.AdminPassword

	// Bootstrap admin
	err := am.BootstrapAdmin()
	if err != nil {
		t.Fatalf("BootstrapAdmin failed: %v", err)
	}

	// Verify admin user exists
	user, err := am.GetUser(adminUsername)
	if err != nil {
		t.Fatalf("GetUser failed for admin: %v", err)
	}

	if user.Username != adminUsername {
		t.Errorf("Admin username = %s, want %s", user.Username, adminUsername)
	}

	// Verify admin can authenticate
	_, err = am.Authenticate(adminUsername, adminPassword)
	if err != nil {
		t.Fatalf("Admin authentication failed: %v", err)
	}

	// Load permissions
	if err := am.LoadAll(); err != nil {
		t.Fatalf("LoadAll failed: %v", err)
	}

	// Verify admin has admin permission
	if !am.IsAdmin(adminUsername) {
		t.Error("Bootstrap admin should have admin permission")
	}

	// Bootstrap again - should be idempotent
	err = am.BootstrapAdmin()
	if err != nil {
		t.Errorf("BootstrapAdmin should be idempotent: %v", err)
	}
}

func TestAuthManager_GetUser_NotFound(t *testing.T) {
	am, _, cleanup := setupTestAuthManager(t)
	defer cleanup()

	_, err := am.GetUser("nonexistent")
	if err != ErrUserNotFound {
		t.Fatalf("GetUser should return ErrUserNotFound, got: %v", err)
	}
}

func TestAuthManager_CheckPermission_NoContext(t *testing.T) {
	am, _, cleanup := setupTestAuthManager(t)
	defer cleanup()

	ctx := context.Background() // No auth claims

	err := am.CheckPermission(ctx, "blog", PermRead)
	if err != ErrUnauthorized {
		t.Fatalf("CheckPermission should fail with ErrUnauthorized without context, got: %v", err)
	}
}

// ---- Group Management Tests ----

func TestAuthManager_CreateGroup(t *testing.T) {
	am, _, cleanup := setupTestAuthManager(t)
	defer cleanup()

	members := []string{"alice", "bob"}
	group, err := am.CreateGroup("developers", "Development team", members)
	if err != nil {
		t.Fatalf("CreateGroup failed: %v", err)
	}

	if group.Name != "developers" {
		t.Errorf("Expected name 'developers', got '%s'", group.Name)
	}
	if group.Description != "Development team" {
		t.Errorf("Expected description 'Development team', got '%s'", group.Description)
	}
	if len(group.Members) != 2 {
		t.Errorf("Expected 2 members, got %d", len(group.Members))
	}
	if group.CreatedAt == 0 {
		t.Error("CreatedAt should be set")
	}
}

func TestAuthManager_CreateGroup_Duplicate(t *testing.T) {
	am, _, cleanup := setupTestAuthManager(t)
	defer cleanup()

	_, err := am.CreateGroup("developers", "Dev team", []string{"alice"})
	if err != nil {
		t.Fatalf("CreateGroup failed: %v", err)
	}

	_, err = am.CreateGroup("developers", "Duplicate", []string{"bob"})
	if err == nil {
		t.Fatal("CreateGroup should fail for duplicate group name")
	}
}

func TestAuthManager_GetGroup(t *testing.T) {
	am, _, cleanup := setupTestAuthManager(t)
	defer cleanup()

	members := []string{"alice", "bob"}
	_, err := am.CreateGroup("developers", "Dev team", members)
	if err != nil {
		t.Fatalf("CreateGroup failed: %v", err)
	}

	group, err := am.GetGroup("developers")
	if err != nil {
		t.Fatalf("GetGroup failed: %v", err)
	}

	if group.Name != "developers" {
		t.Errorf("Expected name 'developers', got '%s'", group.Name)
	}
	if len(group.Members) != 2 {
		t.Errorf("Expected 2 members, got %d", len(group.Members))
	}
}

func TestAuthManager_GetGroup_NotFound(t *testing.T) {
	am, _, cleanup := setupTestAuthManager(t)
	defer cleanup()

	_, err := am.GetGroup("nonexistent")
	if err == nil {
		t.Fatal("GetGroup should fail for nonexistent group")
	}
}

func TestAuthManager_ListGroups(t *testing.T) {
	am, _, cleanup := setupTestAuthManager(t)
	defer cleanup()

	_, err := am.CreateGroup("developers", "Dev team", []string{"alice"})
	if err != nil {
		t.Fatalf("CreateGroup failed: %v", err)
	}

	_, err = am.CreateGroup("managers", "Management", []string{"bob"})
	if err != nil {
		t.Fatalf("CreateGroup failed: %v", err)
	}

	groups := am.ListGroups()
	if len(groups) != 2 {
		t.Fatalf("Expected 2 groups, got %d", len(groups))
	}
}

func TestAuthManager_UpdateGroup(t *testing.T) {
	am, _, cleanup := setupTestAuthManager(t)
	defer cleanup()

	_, err := am.CreateGroup("developers", "Dev team", []string{"alice"})
	if err != nil {
		t.Fatalf("CreateGroup failed: %v", err)
	}

	newMembers := []string{"alice", "bob", "charlie"}
	group, err := am.UpdateGroup("developers", "Updated description", newMembers)
	if err != nil {
		t.Fatalf("UpdateGroup failed: %v", err)
	}

	if group.Description != "Updated description" {
		t.Errorf("Expected updated description, got '%s'", group.Description)
	}
	if len(group.Members) != 3 {
		t.Errorf("Expected 3 members, got %d", len(group.Members))
	}
}

func TestAuthManager_DeleteGroup(t *testing.T) {
	am, _, cleanup := setupTestAuthManager(t)
	defer cleanup()

	_, err := am.CreateGroup("developers", "Dev team", []string{"alice"})
	if err != nil {
		t.Fatalf("CreateGroup failed: %v", err)
	}

	err = am.DeleteGroup("developers")
	if err != nil {
		t.Fatalf("DeleteGroup failed: %v", err)
	}

	_, err = am.GetGroup("developers")
	if err == nil {
		t.Fatal("Group should not exist after deletion")
	}
}

func TestAuthManager_SetGroupPermission(t *testing.T) {
	am, _, cleanup := setupTestAuthManager(t)
	defer cleanup()

	_, err := am.CreateGroup("developers", "Dev team", []string{"alice"})
	if err != nil {
		t.Fatalf("CreateGroup failed: %v", err)
	}

	perm := &GroupPermission{
		GroupName:  "developers",
		Collection: "blog",
		Read:       true,
		Write:      true,
		Admin:      false,
	}

	err = am.SetGroupPermission(perm)
	if err != nil {
		t.Fatalf("SetGroupPermission failed: %v", err)
	}

	perms := am.GetGroupPermissions("developers")
	if len(perms) != 1 {
		t.Fatalf("Expected 1 permission, got %d", len(perms))
	}

	if perms[0].Collection != "blog" {
		t.Errorf("Expected collection 'blog', got '%s'", perms[0].Collection)
	}
	if !perms[0].Read || !perms[0].Write || perms[0].Admin {
		t.Error("Permission flags not set correctly")
	}
}

func TestAuthManager_CheckPermission_GroupPermission(t *testing.T) {
	am, _, cleanup := setupTestAuthManager(t)
	defer cleanup()

	// Create user
	user, err := am.CreateUser("alice", "password123")
	if err != nil {
		t.Fatalf("CreateUser failed: %v", err)
	}

	// Create group with alice as member
	_, err = am.CreateGroup("developers", "Dev team", []string{"alice"})
	if err != nil {
		t.Fatalf("CreateGroup failed: %v", err)
	}

	// Set group permission
	perm := &GroupPermission{
		GroupName:  "developers",
		Collection: "blog",
		Read:       true,
		Write:      false,
		Admin:      false,
	}
	err = am.SetGroupPermission(perm)
	if err != nil {
		t.Fatalf("SetGroupPermission failed: %v", err)
	}

	// Generate JWT for alice
	token, err := GenerateJWT(user.Username, false, am.config.JWTSecret, am.config.JWTExpiry)
	if err != nil {
		t.Fatalf("GenerateJWT failed: %v", err)
	}

	claims, err := ValidateJWT(token, am.config.JWTSecret)
	if err != nil {
		t.Fatalf("ValidateJWT failed: %v", err)
	}

	ctx := context.WithValue(context.Background(), authContextKey, claims)

	// Test read permission (should succeed via group)
	err = am.CheckPermission(ctx, "blog", PermRead)
	if err != nil {
		t.Errorf("CheckPermission should succeed for group read permission: %v", err)
	}

	// Test write permission (should fail)
	err = am.CheckPermission(ctx, "blog", PermWrite)
	if err != ErrForbidden {
		t.Errorf("CheckPermission should fail for write, got: %v", err)
	}
}

func TestAuthManager_GetUserGroups(t *testing.T) {
	am, _, cleanup := setupTestAuthManager(t)
	defer cleanup()

	_, err := am.CreateGroup("developers", "Dev team", []string{"alice", "bob"})
	if err != nil {
		t.Fatalf("CreateGroup failed: %v", err)
	}

	_, err = am.CreateGroup("managers", "Management", []string{"alice"})
	if err != nil {
		t.Fatalf("CreateGroup failed: %v", err)
	}

	aliceGroups := am.GetUserGroups("alice")
	if len(aliceGroups) != 2 {
		t.Fatalf("Expected alice to be in 2 groups, got %d", len(aliceGroups))
	}

	bobGroups := am.GetUserGroups("bob")
	if len(bobGroups) != 1 {
		t.Fatalf("Expected bob to be in 1 group, got %d", len(bobGroups))
	}

	charlieGroups := am.GetUserGroups("charlie")
	if len(charlieGroups) != 0 {
		t.Fatalf("Expected charlie to be in 0 groups, got %d", len(charlieGroups))
	}
}

func TestAuthManager_ListAllUsers(t *testing.T) {
	am, _, cleanup := setupTestAuthManager(t)
	defer cleanup()

	_, err := am.CreateUser("alice", "password123")
	if err != nil {
		t.Fatalf("CreateUser failed: %v", err)
	}

	_, err = am.CreateUser("bob", "password456")
	if err != nil {
		t.Fatalf("CreateUser failed: %v", err)
	}

	users := am.ListAllUsers()
	if len(users) != 2 {
		t.Fatalf("Expected 2 users, got %d", len(users))
	}
}

// ---- API Key Management Tests ----

func TestAuthManager_ListAPIKeys(t *testing.T) {
	am, _, cleanup := setupTestAuthManager(t)
	defer cleanup()

	username := "testuser"

	// Create user
	_, err := am.CreateUser(username, "password123")
	if err != nil {
		t.Fatalf("CreateUser failed: %v", err)
	}

	// Initially no API keys
	keys, err := am.ListAPIKeys(username)
	if err != nil {
		t.Fatalf("ListAPIKeys failed: %v", err)
	}
	if len(keys) != 0 {
		t.Errorf("Expected 0 API keys initially, got %d", len(keys))
	}

	// Create multiple API keys
	key1, err := am.CreateAPIKey(username, "Development key", 0)
	if err != nil {
		t.Fatalf("CreateAPIKey failed: %v", err)
	}

	key2, err := am.CreateAPIKey(username, "Production key", 0)
	if err != nil {
		t.Fatalf("CreateAPIKey failed: %v", err)
	}

	// List API keys
	keys, err = am.ListAPIKeys(username)
	if err != nil {
		t.Fatalf("ListAPIKeys failed: %v", err)
	}
	if len(keys) != 2 {
		t.Fatalf("Expected 2 API keys, got %d", len(keys))
	}

	// Verify keys are correct
	descriptions := make(map[string]bool)
	for _, key := range keys {
		if key.Username != username {
			t.Errorf("API key username = %s, want %s", key.Username, username)
		}
		descriptions[key.Description] = true
	}

	if !descriptions["Development key"] {
		t.Error("Expected 'Development key' in API keys")
	}
	if !descriptions["Production key"] {
		t.Error("Expected 'Production key' in API keys")
	}

	// Verify keys are not leaked
	_ = key1
	_ = key2
}

func TestAuthManager_ListAPIKeys_EmptyForOtherUser(t *testing.T) {
	am, _, cleanup := setupTestAuthManager(t)
	defer cleanup()

	user1 := "alice"
	user2 := "bob"

	// Create both users
	_, err := am.CreateUser(user1, "password123")
	if err != nil {
		t.Fatalf("CreateUser failed: %v", err)
	}
	_, err = am.CreateUser(user2, "password456")
	if err != nil {
		t.Fatalf("CreateUser failed: %v", err)
	}

	// Create API key for user1
	_, err = am.CreateAPIKey(user1, "Alice's key", 0)
	if err != nil {
		t.Fatalf("CreateAPIKey failed: %v", err)
	}

	// User2 should see no keys
	keys, err := am.ListAPIKeys(user2)
	if err != nil {
		t.Fatalf("ListAPIKeys failed: %v", err)
	}
	if len(keys) != 0 {
		t.Errorf("Expected 0 API keys for user2, got %d", len(keys))
	}

	// User1 should see 1 key
	keys, err = am.ListAPIKeys(user1)
	if err != nil {
		t.Fatalf("ListAPIKeys failed: %v", err)
	}
	if len(keys) != 1 {
		t.Errorf("Expected 1 API key for user1, got %d", len(keys))
	}
}

func TestAuthManager_DeleteAPIKey(t *testing.T) {
	am, _, cleanup := setupTestAuthManager(t)
	defer cleanup()

	username := "testuser"

	// Create user
	_, err := am.CreateUser(username, "password123")
	if err != nil {
		t.Fatalf("CreateUser failed: %v", err)
	}

	// Create API key
	key, err := am.CreateAPIKey(username, "Test key", 0)
	if err != nil {
		t.Fatalf("CreateAPIKey failed: %v", err)
	}

	// Verify key exists
	keys, err := am.ListAPIKeys(username)
	if err != nil {
		t.Fatalf("ListAPIKeys failed: %v", err)
	}
	if len(keys) != 1 {
		t.Fatalf("Expected 1 API key, got %d", len(keys))
	}
	keyHash := keys[0].KeyHash

	// Delete the key
	err = am.DeleteAPIKey(username, keyHash)
	if err != nil {
		t.Fatalf("DeleteAPIKey failed: %v", err)
	}

	// Verify key no longer exists
	keys, err = am.ListAPIKeys(username)
	if err != nil {
		t.Fatalf("ListAPIKeys failed after delete: %v", err)
	}
	if len(keys) != 0 {
		t.Errorf("Expected 0 API keys after delete, got %d", len(keys))
	}

	// Verify key can't be validated
	_, err = am.ValidateAPIKey(key)
	if err != ErrAPIKeyNotFound {
		t.Errorf("ValidateAPIKey should fail with ErrAPIKeyNotFound after delete, got: %v", err)
	}
}

func TestAuthManager_DeleteAPIKey_NotFound(t *testing.T) {
	am, _, cleanup := setupTestAuthManager(t)
	defer cleanup()

	username := "testuser"

	// Create user
	_, err := am.CreateUser(username, "password123")
	if err != nil {
		t.Fatalf("CreateUser failed: %v", err)
	}

	// Try to delete non-existent key
	err = am.DeleteAPIKey(username, "nonexistent_hash")
	if err != ErrAPIKeyNotFound {
		t.Errorf("DeleteAPIKey should fail with ErrAPIKeyNotFound, got: %v", err)
	}
}

func TestAuthManager_DeleteAPIKey_Forbidden(t *testing.T) {
	am, _, cleanup := setupTestAuthManager(t)
	defer cleanup()

	user1 := "alice"
	user2 := "bob"

	// Create both users
	_, err := am.CreateUser(user1, "password123")
	if err != nil {
		t.Fatalf("CreateUser failed: %v", err)
	}
	_, err = am.CreateUser(user2, "password456")
	if err != nil {
		t.Fatalf("CreateUser failed: %v", err)
	}

	// Create API key for user1
	_, err = am.CreateAPIKey(user1, "Alice's key", 0)
	if err != nil {
		t.Fatalf("CreateAPIKey failed: %v", err)
	}

	// Get user1's key hash
	keys, err := am.ListAPIKeys(user1)
	if err != nil {
		t.Fatalf("ListAPIKeys failed: %v", err)
	}
	if len(keys) != 1 {
		t.Fatalf("Expected 1 API key, got %d", len(keys))
	}
	keyHash := keys[0].KeyHash

	// user2 should not be able to delete user1's key
	err = am.DeleteAPIKey(user2, keyHash)
	if err != ErrForbidden {
		t.Errorf("DeleteAPIKey should fail with ErrForbidden, got: %v", err)
	}

	// Verify key still exists for user1
	keys, err = am.ListAPIKeys(user1)
	if err != nil {
		t.Fatalf("ListAPIKeys failed after forbidden delete: %v", err)
	}
	if len(keys) != 1 {
		t.Errorf("Expected 1 API key to still exist after forbidden delete, got %d", len(keys))
	}
}

func TestAuthManager_DeleteAPIKey_MultiplKeys(t *testing.T) {
	am, _, cleanup := setupTestAuthManager(t)
	defer cleanup()

	username := "testuser"

	// Create user
	_, err := am.CreateUser(username, "password123")
	if err != nil {
		t.Fatalf("CreateUser failed: %v", err)
	}

	// Create multiple API keys
	_, err = am.CreateAPIKey(username, "Key 1", 0)
	if err != nil {
		t.Fatalf("CreateAPIKey failed: %v", err)
	}
	_, err = am.CreateAPIKey(username, "Key 2", 0)
	if err != nil {
		t.Fatalf("CreateAPIKey failed: %v", err)
	}
	_, err = am.CreateAPIKey(username, "Key 3", 0)
	if err != nil {
		t.Fatalf("CreateAPIKey failed: %v", err)
	}

	// Get all keys
	keys, err := am.ListAPIKeys(username)
	if err != nil {
		t.Fatalf("ListAPIKeys failed: %v", err)
	}
	if len(keys) != 3 {
		t.Fatalf("Expected 3 API keys, got %d", len(keys))
	}

	// Delete middle key
	keyToDelete := keys[1].KeyHash
	err = am.DeleteAPIKey(username, keyToDelete)
	if err != nil {
		t.Fatalf("DeleteAPIKey failed: %v", err)
	}

	// Verify 2 keys remain
	keys, err = am.ListAPIKeys(username)
	if err != nil {
		t.Fatalf("ListAPIKeys failed after delete: %v", err)
	}
	if len(keys) != 2 {
		t.Errorf("Expected 2 API keys after delete, got %d", len(keys))
	}

	// Verify deleted key hash is not in list
	for _, key := range keys {
		if key.KeyHash == keyToDelete {
			t.Error("Deleted key still in list")
		}
	}
}

// #213: DELETE answered {"status":"deleted"} while the user stayed, marked
// disabled, and the name could never be registered again — so delete-then-
// register, which is how a tenant's credentials get rotated, could not work for
// any name that had ever been used.
//
// The deletion is now real, and these tests check all four places a user is
// referenced. Leaving any one of them would make the deletion look complete
// while remaining a way in, and the group and permission entries are the
// dangerous ones: they are what a re-registered name would otherwise inherit.
func TestDeleteUserRemovesEverythingThatGrantsAccess(t *testing.T) {
	am, db, cleanup := setupTestAuthManager(t)
	defer cleanup()

	const victim = "tenant-admin"
	if _, err := am.CreateUser(victim, "correct-horse-battery"); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	key, err := am.CreateAPIKey(victim, "rotation test", 0)
	if err != nil {
		t.Fatalf("CreateAPIKey: %v", err)
	}
	if err := am.SetPermission(&Permission{
		Username: victim, Collection: "secrets", Read: true, Write: true, Admin: true,
	}); err != nil {
		t.Fatalf("SetPermission: %v", err)
	}
	if _, err := am.CreateGroup("editors", "", []string{victim, "admin"}); err != nil {
		t.Fatalf("CreateGroup: %v", err)
	}

	if err := am.DeleteUser(victim); err != nil {
		t.Fatalf("DeleteUser: %v", err)
	}

	t.Run("the record is gone", func(t *testing.T) {
		if _, err := am.GetUser(victim); err == nil {
			t.Error("the user still resolves after deletion")
		}
	})

	t.Run("the API key no longer authenticates", func(t *testing.T) {
		if _, err := am.ValidateAPIKey(key); err == nil {
			t.Error("an API key belonging to a deleted user still validates")
		}
	})

	t.Run("permissions are gone", func(t *testing.T) {
		if perms := am.GetPermissions(victim); len(perms) != 0 {
			t.Errorf("deleted user still holds %d permissions: %+v", len(perms), perms)
		}
	})

	t.Run("group membership is gone", func(t *testing.T) {
		g, err := am.GetGroup("editors")
		if err != nil {
			t.Fatalf("GetGroup: %v", err)
		}
		for _, m := range g.Members {
			if m == victim {
				t.Fatal("deleted user is still a member of editors — a name " +
					"registered again would inherit the group's grants")
			}
		}
		if len(g.Members) != 1 || g.Members[0] != "admin" {
			t.Errorf("removing one member disturbed the rest: %v", g.Members)
		}
	})

	// The caches are rebuilt from the database on every start, so an entry left
	// on disk comes back after a restart even when the in-memory state looks
	// clean. Checking both is the only way to know which one was cleared.
	t.Run("nothing survives on disk", func(t *testing.T) {
		fresh := NewAuthManager(db, am.config)
		if err := fresh.LoadAll(); err != nil {
			t.Fatalf("LoadAll: %v", err)
		}
		if _, err := fresh.GetUser(victim); err == nil {
			t.Error("the user came back after reloading from the database")
		}
		if _, err := fresh.ValidateAPIKey(key); err == nil {
			t.Error("the API key came back after reloading from the database")
		}
		if perms := fresh.GetPermissions(victim); len(perms) != 0 {
			t.Errorf("permissions came back after reloading: %+v", perms)
		}
		g, err := fresh.GetGroup("editors")
		if err != nil {
			t.Fatalf("GetGroup after reload: %v", err)
		}
		for _, m := range g.Members {
			if m == victim {
				t.Error("group membership came back after reloading")
			}
		}
	})
}

// The sequence the issue is about: delete then register the same name. It must
// work, and the new user must start with nothing the old one had.
func TestDeletedUsernameCanBeRegisteredAgainWithoutInheriting(t *testing.T) {
	am, _, cleanup := setupTestAuthManager(t)
	defer cleanup()

	const name = "rotated"
	if _, err := am.CreateUser(name, "first-password"); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if err := am.SetPermission(&Permission{
		Username: name, Collection: "secrets", Read: true, Write: true, Admin: true,
	}); err != nil {
		t.Fatalf("SetPermission: %v", err)
	}

	if err := am.DeleteUser(name); err != nil {
		t.Fatalf("DeleteUser: %v", err)
	}

	if _, err := am.CreateUser(name, "second-password"); err != nil {
		t.Fatalf("registering a deleted name failed: %v — this is the 409 in #213", err)
	}

	if perms := am.GetPermissions(name); len(perms) != 0 {
		t.Errorf("the re-registered user inherited %d permissions from the one "+
			"deleted before it: %+v", len(perms), perms)
	}
	if _, err := am.Authenticate(name, "first-password"); err == nil {
		t.Error("the old password still authenticates the new user")
	}
	if _, err := am.Authenticate(name, "second-password"); err != nil {
		t.Errorf("the new password does not authenticate: %v", err)
	}
}
