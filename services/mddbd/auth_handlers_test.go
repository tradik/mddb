package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	bolt "go.etcd.io/bbolt"
)

// setupTestServer creates a test Server with BoltDB and AuthManager initialized.
// Returns the server, the database, and a cleanup function.
func setupTestServer(t *testing.T) (*Server, *bolt.DB, func()) {
	t.Helper()
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

	// Create all required buckets
	buckets := []string{
		"docs", "idxmeta", "rev", "bykey", "vectors",
		"fts_tokens", "webhooks", "schemas",
		"auth_users", "auth_groups", "ttl",
	}
	err = db.Update(func(tx *bolt.Tx) error {
		for _, name := range buckets {
			if _, err := tx.CreateBucketIfNotExists([]byte(name)); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		_ = db.Close()
		t.Fatalf("Failed to create buckets: %v", err)
	}

	config := AuthConfig{
		JWTSecret:     "test-jwt-secret-key-for-handlers",
		JWTExpiry:     24 * time.Hour,
		AdminUsername: "admin",
		AdminPassword: "adminpass",
	}

	am := NewAuthManager(db, config)
	if err := am.EnsureBuckets(); err != nil {
		_ = db.Close()
		t.Fatalf("Failed to ensure auth buckets: %v", err)
	}

	s := &Server{
		DB:          db,
		AuthManager: am,
	}

	cleanup := func() {
		_ = db.Close()
	}

	return s, db, cleanup
}

// createAdminAndGetToken bootstraps the admin user and returns a JWT token string.
func createAdminAndGetToken(t *testing.T, s *Server) string {
	t.Helper()
	if err := s.AuthManager.BootstrapAdmin(); err != nil {
		t.Fatalf("BootstrapAdmin failed: %v", err)
	}
	if err := s.AuthManager.LoadAll(); err != nil {
		t.Fatalf("LoadAll failed: %v", err)
	}
	token, err := GenerateJWT(
		s.AuthManager.config.AdminUsername,
		true,
		s.AuthManager.config.JWTSecret,
		s.AuthManager.config.JWTExpiry,
	)
	if err != nil {
		t.Fatalf("GenerateJWT failed: %v", err)
	}
	return token
}

// authenticatedContext returns a context with JWT claims injected, suitable for
// passing to handlers that call GetClaimsFromContext.
func authenticatedContext(username string, admin bool) context.Context {
	claims := &JWTClaims{
		Username: username,
		Admin:    admin,
	}
	return context.WithValue(context.Background(), authContextKey, claims)
}

// ---- Login Handler Tests ----

func TestHandleAuthLogin_Success(t *testing.T) {
	s, _, cleanup := setupTestServer(t)
	defer cleanup()

	adminToken := createAdminAndGetToken(t, s)
	_ = adminToken

	body := `{"username":"admin","password":"adminpass"}`
	req := httptest.NewRequest("POST", "/v1/auth/login", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	s.handleAuthLogin(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Expected status 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp LoginResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if resp.Token == "" {
		t.Error("Expected non-empty token")
	}
	if resp.ExpiresAt == 0 {
		t.Error("Expected non-zero expiresAt")
	}
}

func TestHandleAuthLogin_InvalidCredentials(t *testing.T) {
	s, _, cleanup := setupTestServer(t)
	defer cleanup()

	_ = createAdminAndGetToken(t, s)

	body := `{"username":"admin","password":"wrongpassword"}`
	req := httptest.NewRequest("POST", "/v1/auth/login", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	s.handleAuthLogin(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("Expected status 401, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleAuthLogin_InvalidJSON(t *testing.T) {
	s, _, cleanup := setupTestServer(t)
	defer cleanup()

	req := httptest.NewRequest("POST", "/v1/auth/login", strings.NewReader("not json"))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	s.handleAuthLogin(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("Expected status 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleAuthLogin_MethodNotAllowed(t *testing.T) {
	s, _, cleanup := setupTestServer(t)
	defer cleanup()

	req := httptest.NewRequest("GET", "/v1/auth/login", nil)
	w := httptest.NewRecorder()

	s.handleAuthLogin(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("Expected status 405, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleAuthLogin_NonexistentUser(t *testing.T) {
	s, _, cleanup := setupTestServer(t)
	defer cleanup()

	body := `{"username":"ghost","password":"password"}`
	req := httptest.NewRequest("POST", "/v1/auth/login", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	s.handleAuthLogin(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("Expected status 401, got %d: %s", w.Code, w.Body.String())
	}
}

// ---- Register Handler Tests ----

func TestHandleAuthRegister_Success(t *testing.T) {
	s, _, cleanup := setupTestServer(t)
	defer cleanup()

	_ = createAdminAndGetToken(t, s)

	body := `{"username":"newuser","password":"newpass123"}`
	req := httptest.NewRequest("POST", "/v1/auth/register", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(authenticatedContext("admin", true))
	w := httptest.NewRecorder()

	s.handleAuthRegister(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Expected status 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp RegisterResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if resp.Username != "newuser" {
		t.Errorf("Expected username 'newuser', got '%s'", resp.Username)
	}
	if resp.CreatedAt == 0 {
		t.Error("Expected non-zero createdAt")
	}
}

func TestHandleAuthRegister_NonAdminForbidden(t *testing.T) {
	s, _, cleanup := setupTestServer(t)
	defer cleanup()

	body := `{"username":"newuser","password":"newpass123"}`
	req := httptest.NewRequest("POST", "/v1/auth/register", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(authenticatedContext("regularuser", false))
	w := httptest.NewRecorder()

	s.handleAuthRegister(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("Expected status 403, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleAuthRegister_NoAuth(t *testing.T) {
	s, _, cleanup := setupTestServer(t)
	defer cleanup()

	body := `{"username":"newuser","password":"newpass123"}`
	req := httptest.NewRequest("POST", "/v1/auth/register", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	s.handleAuthRegister(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("Expected status 403, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleAuthRegister_DuplicateUser(t *testing.T) {
	s, _, cleanup := setupTestServer(t)
	defer cleanup()

	_ = createAdminAndGetToken(t, s)

	// Create user the first time
	_, err := s.AuthManager.CreateUser("existinguser", "password123")
	if err != nil {
		t.Fatalf("CreateUser failed: %v", err)
	}

	// Try to register same user via handler
	body := `{"username":"existinguser","password":"newpass"}`
	req := httptest.NewRequest("POST", "/v1/auth/register", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(authenticatedContext("admin", true))
	w := httptest.NewRecorder()

	s.handleAuthRegister(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("Expected status 409, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleAuthRegister_InvalidJSON(t *testing.T) {
	s, _, cleanup := setupTestServer(t)
	defer cleanup()

	req := httptest.NewRequest("POST", "/v1/auth/register", strings.NewReader("{bad"))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(authenticatedContext("admin", true))
	w := httptest.NewRecorder()

	s.handleAuthRegister(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("Expected status 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleAuthRegister_MethodNotAllowed(t *testing.T) {
	s, _, cleanup := setupTestServer(t)
	defer cleanup()

	req := httptest.NewRequest("GET", "/v1/auth/register", nil)
	req = req.WithContext(authenticatedContext("admin", true))
	w := httptest.NewRecorder()

	s.handleAuthRegister(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("Expected status 405, got %d: %s", w.Code, w.Body.String())
	}
}

// ---- Me Handler Tests ----

func TestHandleAuthMe_Success(t *testing.T) {
	s, _, cleanup := setupTestServer(t)
	defer cleanup()

	_ = createAdminAndGetToken(t, s)

	req := httptest.NewRequest("GET", "/v1/auth/me", nil)
	req = req.WithContext(authenticatedContext("admin", true))
	w := httptest.NewRecorder()

	s.handleAuthMe(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Expected status 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp GetMeResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if resp.Username != "admin" {
		t.Errorf("Expected username 'admin', got '%s'", resp.Username)
	}
	if !resp.Admin {
		t.Error("Expected admin to be true")
	}
}

func TestHandleAuthMe_NoAuth(t *testing.T) {
	s, _, cleanup := setupTestServer(t)
	defer cleanup()

	req := httptest.NewRequest("GET", "/v1/auth/me", nil)
	w := httptest.NewRecorder()

	s.handleAuthMe(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("Expected status 401, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleAuthMe_MethodNotAllowed(t *testing.T) {
	s, _, cleanup := setupTestServer(t)
	defer cleanup()

	req := httptest.NewRequest("DELETE", "/v1/auth/me", nil)
	req = req.WithContext(authenticatedContext("admin", true))
	w := httptest.NewRecorder()

	s.handleAuthMe(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("Expected status 405, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleAuthMe_UserNotFound(t *testing.T) {
	s, _, cleanup := setupTestServer(t)
	defer cleanup()

	// Use a context for a user that does not exist in the database
	req := httptest.NewRequest("GET", "/v1/auth/me", nil)
	req = req.WithContext(authenticatedContext("ghost", false))
	w := httptest.NewRecorder()

	s.handleAuthMe(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("Expected status 404, got %d: %s", w.Code, w.Body.String())
	}
}

// ---- Users List Handler Tests ----

func TestHandleAuthUsersList_Success(t *testing.T) {
	s, _, cleanup := setupTestServer(t)
	defer cleanup()

	_ = createAdminAndGetToken(t, s)

	// Create a few extra users
	_, _ = s.AuthManager.CreateUser("alice", "password1")
	_, _ = s.AuthManager.CreateUser("bob", "password2")

	req := httptest.NewRequest("GET", "/v1/auth/users", nil)
	req = req.WithContext(authenticatedContext("admin", true))
	w := httptest.NewRecorder()

	s.handleAuthUsersList(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Expected status 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp UsersListResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	// admin + alice + bob = 3 users
	if len(resp.Users) != 3 {
		t.Fatalf("Expected 3 users, got %d", len(resp.Users))
	}

	// Verify at least one user is admin
	foundAdmin := false
	for _, u := range resp.Users {
		if u.Username == "admin" && u.Admin {
			foundAdmin = true
		}
	}
	if !foundAdmin {
		t.Error("Expected to find admin user with admin=true in users list")
	}
}

func TestHandleAuthUsersList_NonAdminForbidden(t *testing.T) {
	s, _, cleanup := setupTestServer(t)
	defer cleanup()

	req := httptest.NewRequest("GET", "/v1/auth/users", nil)
	req = req.WithContext(authenticatedContext("regularuser", false))
	w := httptest.NewRecorder()

	s.handleAuthUsersList(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("Expected status 403, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleAuthUsersList_MethodNotAllowed(t *testing.T) {
	s, _, cleanup := setupTestServer(t)
	defer cleanup()

	req := httptest.NewRequest("POST", "/v1/auth/users", nil)
	req = req.WithContext(authenticatedContext("admin", true))
	w := httptest.NewRecorder()

	s.handleAuthUsersList(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("Expected status 405, got %d: %s", w.Code, w.Body.String())
	}
}

// ---- Delete User Handler Tests ----

func TestHandleAuthDeleteUser_Success(t *testing.T) {
	s, _, cleanup := setupTestServer(t)
	defer cleanup()

	_ = createAdminAndGetToken(t, s)

	// Create a user to delete
	_, err := s.AuthManager.CreateUser("todelete", "password")
	if err != nil {
		t.Fatalf("CreateUser failed: %v", err)
	}

	req := httptest.NewRequest("DELETE", "/v1/auth/users/todelete", nil)
	req = req.WithContext(authenticatedContext("admin", true))
	w := httptest.NewRecorder()

	s.handleAuthDeleteUser(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Expected status 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]string
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}
	if resp["status"] != "deleted" {
		t.Errorf("Expected status 'deleted', got '%s'", resp["status"])
	}

	// The response says "deleted" and now means it (#213): the record is gone
	// and the name is free, rather than held by a disabled user that every
	// later registration collided with.
	if _, err := s.AuthManager.GetUser("todelete"); err == nil {
		t.Error("the user still resolves after a delete that reported success")
	}
	if _, err := s.AuthManager.CreateUser("todelete", "a-new-password"); err != nil {
		t.Errorf("the freed name could not be registered again: %v", err)
	}
}

func TestHandleAuthDeleteUser_NotFound(t *testing.T) {
	s, _, cleanup := setupTestServer(t)
	defer cleanup()

	req := httptest.NewRequest("DELETE", "/v1/auth/users/nonexistent", nil)
	req = req.WithContext(authenticatedContext("admin", true))
	w := httptest.NewRecorder()

	s.handleAuthDeleteUser(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("Expected status 404, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleAuthDeleteUser_NonAdminForbidden(t *testing.T) {
	s, _, cleanup := setupTestServer(t)
	defer cleanup()

	req := httptest.NewRequest("DELETE", "/v1/auth/users/someuser", nil)
	req = req.WithContext(authenticatedContext("regularuser", false))
	w := httptest.NewRecorder()

	s.handleAuthDeleteUser(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("Expected status 403, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleAuthDeleteUser_MethodNotAllowed(t *testing.T) {
	s, _, cleanup := setupTestServer(t)
	defer cleanup()

	req := httptest.NewRequest("GET", "/v1/auth/users/someuser", nil)
	req = req.WithContext(authenticatedContext("admin", true))
	w := httptest.NewRecorder()

	s.handleAuthDeleteUser(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("Expected status 405, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleAuthDeleteUser_EmptyUsername(t *testing.T) {
	s, _, cleanup := setupTestServer(t)
	defer cleanup()

	// URL path /v1/auth/users/ with nothing after - username is empty
	req := httptest.NewRequest("DELETE", "/v1/auth/users/", nil)
	req = req.WithContext(authenticatedContext("admin", true))
	w := httptest.NewRecorder()

	s.handleAuthDeleteUser(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("Expected status 400, got %d: %s", w.Code, w.Body.String())
	}
}

// ---- Permissions Handler Tests ----

func TestHandleAuthPermissions_SetPermission(t *testing.T) {
	s, _, cleanup := setupTestServer(t)
	defer cleanup()

	_ = createAdminAndGetToken(t, s)

	// Create a user to set permissions for
	_, err := s.AuthManager.CreateUser("alice", "password123")
	if err != nil {
		t.Fatalf("CreateUser failed: %v", err)
	}

	body := `{"username":"alice","collection":"blog","read":true,"write":true,"admin":false}`
	req := httptest.NewRequest("POST", "/v1/auth/permissions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(authenticatedContext("admin", true))
	w := httptest.NewRecorder()

	s.handleAuthPermissions(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Expected status 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp SetPermissionResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}
	if resp.Status != "ok" {
		t.Errorf("Expected status 'ok', got '%s'", resp.Status)
	}

	// Verify the permission was actually set
	perms := s.AuthManager.GetPermissions("alice")
	if len(perms) != 1 {
		t.Fatalf("Expected 1 permission, got %d", len(perms))
	}
	if perms[0].Collection != "blog" {
		t.Errorf("Expected collection 'blog', got '%s'", perms[0].Collection)
	}
	if !perms[0].Read || !perms[0].Write {
		t.Error("Expected read and write to be true")
	}
}

func TestHandleAuthPermissions_GetPermissions(t *testing.T) {
	s, _, cleanup := setupTestServer(t)
	defer cleanup()

	_ = createAdminAndGetToken(t, s)

	// Create user and set permission
	_, err := s.AuthManager.CreateUser("bob", "password")
	if err != nil {
		t.Fatalf("CreateUser failed: %v", err)
	}
	err = s.AuthManager.SetPermission(&Permission{
		Username:   "bob",
		Collection: "docs",
		Read:       true,
		Write:      false,
		Admin:      false,
	})
	if err != nil {
		t.Fatalf("SetPermission failed: %v", err)
	}

	req := httptest.NewRequest("GET", "/v1/auth/permissions?username=bob", nil)
	req = req.WithContext(authenticatedContext("admin", true))
	w := httptest.NewRecorder()

	s.handleAuthPermissions(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Expected status 200, got %d: %s", w.Code, w.Body.String())
	}

	var perms []*Permission
	if err := json.NewDecoder(w.Body).Decode(&perms); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}
	if len(perms) != 1 {
		t.Fatalf("Expected 1 permission, got %d", len(perms))
	}
	if perms[0].Collection != "docs" {
		t.Errorf("Expected collection 'docs', got '%s'", perms[0].Collection)
	}
}

func TestHandleAuthPermissions_GetMissingUsername(t *testing.T) {
	s, _, cleanup := setupTestServer(t)
	defer cleanup()

	req := httptest.NewRequest("GET", "/v1/auth/permissions", nil)
	req = req.WithContext(authenticatedContext("admin", true))
	w := httptest.NewRecorder()

	s.handleAuthPermissions(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("Expected status 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleAuthPermissions_NonAdminForbidden(t *testing.T) {
	s, _, cleanup := setupTestServer(t)
	defer cleanup()

	req := httptest.NewRequest("GET", "/v1/auth/permissions?username=alice", nil)
	req = req.WithContext(authenticatedContext("regularuser", false))
	w := httptest.NewRecorder()

	s.handleAuthPermissions(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("Expected status 403, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleAuthPermissions_MethodNotAllowed(t *testing.T) {
	s, _, cleanup := setupTestServer(t)
	defer cleanup()

	req := httptest.NewRequest("DELETE", "/v1/auth/permissions", nil)
	req = req.WithContext(authenticatedContext("admin", true))
	w := httptest.NewRecorder()

	s.handleAuthPermissions(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("Expected status 405, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleAuthPermissions_SetInvalidJSON(t *testing.T) {
	s, _, cleanup := setupTestServer(t)
	defer cleanup()

	req := httptest.NewRequest("POST", "/v1/auth/permissions", strings.NewReader("{{bad"))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(authenticatedContext("admin", true))
	w := httptest.NewRecorder()

	s.handleAuthPermissions(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("Expected status 400, got %d: %s", w.Code, w.Body.String())
	}
}

// ---- API Key Handler Tests ----

func TestHandleAuthAPIKey_CreateSuccess(t *testing.T) {
	s, _, cleanup := setupTestServer(t)
	defer cleanup()

	_ = createAdminAndGetToken(t, s)

	body := `{"description":"test key"}`
	req := httptest.NewRequest("POST", "/v1/auth/api-key", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(authenticatedContext("admin", false))
	w := httptest.NewRecorder()

	s.handleAuthAPIKey(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Expected status 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp CreateAPIKeyResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}
	if resp.Key == "" {
		t.Error("Expected non-empty key")
	}
	if !strings.HasPrefix(resp.Key, "mddb_live_") {
		t.Errorf("Expected key prefix 'mddb_live_', got '%s'", resp.Key[:10])
	}
	if resp.Description != "test key" {
		t.Errorf("Expected description 'test key', got '%s'", resp.Description)
	}
}

func TestHandleAuthAPIKey_NoAuth(t *testing.T) {
	s, _, cleanup := setupTestServer(t)
	defer cleanup()

	body := `{"description":"test key"}`
	req := httptest.NewRequest("POST", "/v1/auth/api-key", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	s.handleAuthAPIKey(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("Expected status 401, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleAuthAPIKey_MethodNotAllowed(t *testing.T) {
	s, _, cleanup := setupTestServer(t)
	defer cleanup()

	req := httptest.NewRequest("GET", "/v1/auth/api-key", nil)
	w := httptest.NewRecorder()

	s.handleAuthAPIKey(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("Expected status 405, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleAuthAPIKey_InvalidJSON(t *testing.T) {
	s, _, cleanup := setupTestServer(t)
	defer cleanup()

	_ = createAdminAndGetToken(t, s)

	req := httptest.NewRequest("POST", "/v1/auth/api-key", strings.NewReader("not json"))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(authenticatedContext("admin", false))
	w := httptest.NewRecorder()

	s.handleAuthAPIKey(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("Expected status 400, got %d: %s", w.Code, w.Body.String())
	}
}

// ---- API Keys List Handler Tests ----

func TestHandleAuthAPIKeysList_Success(t *testing.T) {
	s, _, cleanup := setupTestServer(t)
	defer cleanup()

	_ = createAdminAndGetToken(t, s)

	// Create a couple of API keys for the admin user
	_, _ = s.AuthManager.CreateAPIKey("admin", "Key 1", 0)
	_, _ = s.AuthManager.CreateAPIKey("admin", "Key 2", 0)

	req := httptest.NewRequest("GET", "/v1/auth/api-keys", nil)
	req = req.WithContext(authenticatedContext("admin", true))
	w := httptest.NewRecorder()

	s.handleAuthAPIKeysList(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Expected status 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp ListAPIKeysResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}
	if len(resp.Keys) != 2 {
		t.Fatalf("Expected 2 keys, got %d", len(resp.Keys))
	}
}

func TestHandleAuthAPIKeysList_NoAuth(t *testing.T) {
	s, _, cleanup := setupTestServer(t)
	defer cleanup()

	req := httptest.NewRequest("GET", "/v1/auth/api-keys", nil)
	w := httptest.NewRecorder()

	s.handleAuthAPIKeysList(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("Expected status 401, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleAuthAPIKeysList_MethodNotAllowed(t *testing.T) {
	s, _, cleanup := setupTestServer(t)
	defer cleanup()

	req := httptest.NewRequest("POST", "/v1/auth/api-keys", nil)
	req = req.WithContext(authenticatedContext("admin", true))
	w := httptest.NewRecorder()

	s.handleAuthAPIKeysList(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("Expected status 405, got %d: %s", w.Code, w.Body.String())
	}
}

// ---- API Key Delete Handler Tests ----

func TestHandleAuthAPIKeyDelete_Success(t *testing.T) {
	s, _, cleanup := setupTestServer(t)
	defer cleanup()

	_ = createAdminAndGetToken(t, s)

	// Create an API key
	_, err := s.AuthManager.CreateAPIKey("admin", "To delete", 0)
	if err != nil {
		t.Fatalf("CreateAPIKey failed: %v", err)
	}

	// Get the key hash
	keys, err := s.AuthManager.ListAPIKeys("admin")
	if err != nil {
		t.Fatalf("ListAPIKeys failed: %v", err)
	}
	if len(keys) == 0 {
		t.Fatal("Expected at least 1 API key")
	}
	keyHash := keys[0].KeyHash

	req := httptest.NewRequest("DELETE", "/v1/auth/api-keys/"+keyHash, nil)
	req = req.WithContext(authenticatedContext("admin", true))
	w := httptest.NewRecorder()

	s.handleAuthAPIKeyDelete(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Expected status 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]string
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}
	if resp["status"] != "deleted" {
		t.Errorf("Expected status 'deleted', got '%s'", resp["status"])
	}

	// Verify key is gone
	keys, _ = s.AuthManager.ListAPIKeys("admin")
	if len(keys) != 0 {
		t.Errorf("Expected 0 API keys after deletion, got %d", len(keys))
	}
}

func TestHandleAuthAPIKeyDelete_NotFound(t *testing.T) {
	s, _, cleanup := setupTestServer(t)
	defer cleanup()

	_ = createAdminAndGetToken(t, s)

	req := httptest.NewRequest("DELETE", "/v1/auth/api-keys/nonexistenthash", nil)
	req = req.WithContext(authenticatedContext("admin", true))
	w := httptest.NewRecorder()

	s.handleAuthAPIKeyDelete(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("Expected status 404, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleAuthAPIKeyDelete_ForbiddenOtherUsersKey(t *testing.T) {
	s, _, cleanup := setupTestServer(t)
	defer cleanup()

	_ = createAdminAndGetToken(t, s)

	// Create another user and an API key for them
	_, err := s.AuthManager.CreateUser("alice", "password123")
	if err != nil {
		t.Fatalf("CreateUser failed: %v", err)
	}
	_, err = s.AuthManager.CreateAPIKey("alice", "Alice's key", 0)
	if err != nil {
		t.Fatalf("CreateAPIKey failed: %v", err)
	}

	// Get Alice's key hash
	keys, _ := s.AuthManager.ListAPIKeys("alice")
	keyHash := keys[0].KeyHash

	// Try to delete Alice's key as admin (but the handler checks username match, not admin)
	req := httptest.NewRequest("DELETE", "/v1/auth/api-keys/"+keyHash, nil)
	req = req.WithContext(authenticatedContext("admin", true))
	w := httptest.NewRecorder()

	s.handleAuthAPIKeyDelete(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("Expected status 403, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleAuthAPIKeyDelete_NoAuth(t *testing.T) {
	s, _, cleanup := setupTestServer(t)
	defer cleanup()

	req := httptest.NewRequest("DELETE", "/v1/auth/api-keys/somehash", nil)
	w := httptest.NewRecorder()

	s.handleAuthAPIKeyDelete(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("Expected status 401, got %d: %s", w.Code, w.Body.String())
	}
}

// ---- Groups Handler Tests ----

func TestHandleAuthGroups_CreateGroup(t *testing.T) {
	s, _, cleanup := setupTestServer(t)
	defer cleanup()

	body := `{"name":"developers","description":"Dev team","members":["alice","bob"]}`
	req := httptest.NewRequest("POST", "/v1/auth/groups", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(authenticatedContext("admin", true))
	w := httptest.NewRecorder()

	s.handleAuthGroups(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("Expected status 201, got %d: %s", w.Code, w.Body.String())
	}

	var group Group
	if err := json.NewDecoder(w.Body).Decode(&group); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}
	if group.Name != "developers" {
		t.Errorf("Expected name 'developers', got '%s'", group.Name)
	}
	if group.Description != "Dev team" {
		t.Errorf("Expected description 'Dev team', got '%s'", group.Description)
	}
	if len(group.Members) != 2 {
		t.Errorf("Expected 2 members, got %d", len(group.Members))
	}
}

func TestHandleAuthGroups_CreateGroupEmptyName(t *testing.T) {
	s, _, cleanup := setupTestServer(t)
	defer cleanup()

	body := `{"name":"","description":"No name"}`
	req := httptest.NewRequest("POST", "/v1/auth/groups", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(authenticatedContext("admin", true))
	w := httptest.NewRecorder()

	s.handleAuthGroups(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("Expected status 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleAuthGroups_ListGroups(t *testing.T) {
	s, _, cleanup := setupTestServer(t)
	defer cleanup()

	// Create some groups directly
	_, _ = s.AuthManager.CreateGroup("team-a", "Team A", []string{"alice"})
	_, _ = s.AuthManager.CreateGroup("team-b", "Team B", []string{"bob"})

	req := httptest.NewRequest("GET", "/v1/auth/groups", nil)
	req = req.WithContext(authenticatedContext("admin", true))
	w := httptest.NewRecorder()

	s.handleAuthGroups(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Expected status 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp GroupsListResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}
	if len(resp.Groups) != 2 {
		t.Fatalf("Expected 2 groups, got %d", len(resp.Groups))
	}
}

func TestHandleAuthGroups_NonAdminForbidden(t *testing.T) {
	s, _, cleanup := setupTestServer(t)
	defer cleanup()

	req := httptest.NewRequest("GET", "/v1/auth/groups", nil)
	req = req.WithContext(authenticatedContext("regularuser", false))
	w := httptest.NewRecorder()

	s.handleAuthGroups(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("Expected status 403, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleAuthGroups_MethodNotAllowed(t *testing.T) {
	s, _, cleanup := setupTestServer(t)
	defer cleanup()

	req := httptest.NewRequest("DELETE", "/v1/auth/groups", nil)
	req = req.WithContext(authenticatedContext("admin", true))
	w := httptest.NewRecorder()

	s.handleAuthGroups(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("Expected status 405, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleAuthGroups_CreateInvalidJSON(t *testing.T) {
	s, _, cleanup := setupTestServer(t)
	defer cleanup()

	req := httptest.NewRequest("POST", "/v1/auth/groups", strings.NewReader("{bad"))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(authenticatedContext("admin", true))
	w := httptest.NewRecorder()

	s.handleAuthGroups(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("Expected status 400, got %d: %s", w.Code, w.Body.String())
	}
}

// ---- Group Detail Handler Tests ----

func TestHandleAuthGroupDetail_Get(t *testing.T) {
	s, _, cleanup := setupTestServer(t)
	defer cleanup()

	_, _ = s.AuthManager.CreateGroup("devs", "Developers", []string{"alice", "bob"})

	req := httptest.NewRequest("GET", "/v1/auth/groups/devs", nil)
	req = req.WithContext(authenticatedContext("admin", true))
	w := httptest.NewRecorder()

	s.handleAuthGroupDetail(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Expected status 200, got %d: %s", w.Code, w.Body.String())
	}

	var group Group
	if err := json.NewDecoder(w.Body).Decode(&group); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}
	if group.Name != "devs" {
		t.Errorf("Expected name 'devs', got '%s'", group.Name)
	}
}

func TestHandleAuthGroupDetail_GetNotFound(t *testing.T) {
	s, _, cleanup := setupTestServer(t)
	defer cleanup()

	req := httptest.NewRequest("GET", "/v1/auth/groups/nonexistent", nil)
	req = req.WithContext(authenticatedContext("admin", true))
	w := httptest.NewRecorder()

	s.handleAuthGroupDetail(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("Expected status 404, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleAuthGroupDetail_Update(t *testing.T) {
	s, _, cleanup := setupTestServer(t)
	defer cleanup()

	_, _ = s.AuthManager.CreateGroup("devs", "Developers", []string{"alice"})

	body := `{"description":"Updated devs","members":["alice","bob","charlie"]}`
	req := httptest.NewRequest("PUT", "/v1/auth/groups/devs", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(authenticatedContext("admin", true))
	w := httptest.NewRecorder()

	s.handleAuthGroupDetail(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Expected status 200, got %d: %s", w.Code, w.Body.String())
	}

	var group Group
	if err := json.NewDecoder(w.Body).Decode(&group); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}
	if group.Description != "Updated devs" {
		t.Errorf("Expected description 'Updated devs', got '%s'", group.Description)
	}
	if len(group.Members) != 3 {
		t.Errorf("Expected 3 members, got %d", len(group.Members))
	}
}

func TestHandleAuthGroupDetail_UpdateNotFound(t *testing.T) {
	s, _, cleanup := setupTestServer(t)
	defer cleanup()

	body := `{"description":"Updated","members":[]}`
	req := httptest.NewRequest("PUT", "/v1/auth/groups/nonexistent", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(authenticatedContext("admin", true))
	w := httptest.NewRecorder()

	s.handleAuthGroupDetail(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("Expected status 404, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleAuthGroupDetail_Delete(t *testing.T) {
	s, _, cleanup := setupTestServer(t)
	defer cleanup()

	_, _ = s.AuthManager.CreateGroup("todelete", "Will be deleted", []string{})

	req := httptest.NewRequest("DELETE", "/v1/auth/groups/todelete", nil)
	req = req.WithContext(authenticatedContext("admin", true))
	w := httptest.NewRecorder()

	s.handleAuthGroupDetail(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Expected status 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]string
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}
	if resp["status"] != "deleted" {
		t.Errorf("Expected status 'deleted', got '%s'", resp["status"])
	}

	// Verify group is gone
	_, err := s.AuthManager.GetGroup("todelete")
	if err == nil {
		t.Error("Expected GetGroup to fail after deletion")
	}
}

func TestHandleAuthGroupDetail_DeleteNotFound(t *testing.T) {
	s, _, cleanup := setupTestServer(t)
	defer cleanup()

	req := httptest.NewRequest("DELETE", "/v1/auth/groups/ghost", nil)
	req = req.WithContext(authenticatedContext("admin", true))
	w := httptest.NewRecorder()

	s.handleAuthGroupDetail(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("Expected status 404, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleAuthGroupDetail_NonAdminForbidden(t *testing.T) {
	s, _, cleanup := setupTestServer(t)
	defer cleanup()

	req := httptest.NewRequest("GET", "/v1/auth/groups/devs", nil)
	req = req.WithContext(authenticatedContext("regularuser", false))
	w := httptest.NewRecorder()

	s.handleAuthGroupDetail(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("Expected status 403, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleAuthGroupDetail_MethodNotAllowed(t *testing.T) {
	s, _, cleanup := setupTestServer(t)
	defer cleanup()

	req := httptest.NewRequest("PATCH", "/v1/auth/groups/devs", nil)
	req = req.WithContext(authenticatedContext("admin", true))
	w := httptest.NewRecorder()

	s.handleAuthGroupDetail(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("Expected status 405, got %d: %s", w.Code, w.Body.String())
	}
}

// ---- Group Permissions Handler Tests ----

func TestHandleAuthGroupPermissions_Set(t *testing.T) {
	s, _, cleanup := setupTestServer(t)
	defer cleanup()

	// Create a group first
	_, err := s.AuthManager.CreateGroup("devs", "Developers", []string{"alice"})
	if err != nil {
		t.Fatalf("CreateGroup failed: %v", err)
	}

	body := `{"groupName":"devs","collection":"blog","read":true,"write":true,"admin":false}`
	req := httptest.NewRequest("POST", "/v1/auth/group-permissions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(authenticatedContext("admin", true))
	w := httptest.NewRecorder()

	s.handleAuthGroupPermissions(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Expected status 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]string
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}
	if resp["status"] != "permission set" {
		t.Errorf("Expected status 'permission set', got '%s'", resp["status"])
	}
}

func TestHandleAuthGroupPermissions_Get(t *testing.T) {
	s, _, cleanup := setupTestServer(t)
	defer cleanup()

	// Create group and set permission
	_, err := s.AuthManager.CreateGroup("devs", "Developers", []string{"alice"})
	if err != nil {
		t.Fatalf("CreateGroup failed: %v", err)
	}
	err = s.AuthManager.SetGroupPermission(&GroupPermission{
		GroupName:  "devs",
		Collection: "blog",
		Read:       true,
		Write:      false,
		Admin:      false,
	})
	if err != nil {
		t.Fatalf("SetGroupPermission failed: %v", err)
	}

	req := httptest.NewRequest("GET", "/v1/auth/group-permissions?group=devs", nil)
	req = req.WithContext(authenticatedContext("admin", true))
	w := httptest.NewRecorder()

	s.handleAuthGroupPermissions(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Expected status 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}
	perms, ok := resp["permissions"].([]interface{})
	if !ok {
		t.Fatalf("Expected 'permissions' array in response")
	}
	if len(perms) != 1 {
		t.Fatalf("Expected 1 permission, got %d", len(perms))
	}
}

func TestHandleAuthGroupPermissions_GetMissingGroup(t *testing.T) {
	s, _, cleanup := setupTestServer(t)
	defer cleanup()

	req := httptest.NewRequest("GET", "/v1/auth/group-permissions", nil)
	req = req.WithContext(authenticatedContext("admin", true))
	w := httptest.NewRecorder()

	s.handleAuthGroupPermissions(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("Expected status 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleAuthGroupPermissions_NonAdminForbidden(t *testing.T) {
	s, _, cleanup := setupTestServer(t)
	defer cleanup()

	req := httptest.NewRequest("GET", "/v1/auth/group-permissions?group=devs", nil)
	req = req.WithContext(authenticatedContext("regularuser", false))
	w := httptest.NewRecorder()

	s.handleAuthGroupPermissions(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("Expected status 403, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleAuthGroupPermissions_MethodNotAllowed(t *testing.T) {
	s, _, cleanup := setupTestServer(t)
	defer cleanup()

	req := httptest.NewRequest("DELETE", "/v1/auth/group-permissions", nil)
	req = req.WithContext(authenticatedContext("admin", true))
	w := httptest.NewRecorder()

	s.handleAuthGroupPermissions(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("Expected status 405, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleAuthGroupPermissions_SetInvalidJSON(t *testing.T) {
	s, _, cleanup := setupTestServer(t)
	defer cleanup()

	req := httptest.NewRequest("POST", "/v1/auth/group-permissions", strings.NewReader("bad json"))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(authenticatedContext("admin", true))
	w := httptest.NewRecorder()

	s.handleAuthGroupPermissions(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("Expected status 400, got %d: %s", w.Code, w.Body.String())
	}
}

// ---- Integration / End-to-End Style Tests ----

func TestLoginThenAccessMe(t *testing.T) {
	s, _, cleanup := setupTestServer(t)
	defer cleanup()

	_ = createAdminAndGetToken(t, s)

	// Step 1: Login via handler to get a token
	loginBody := `{"username":"admin","password":"adminpass"}`
	loginReq := httptest.NewRequest("POST", "/v1/auth/login", strings.NewReader(loginBody))
	loginReq.Header.Set("Content-Type", "application/json")
	loginW := httptest.NewRecorder()

	s.handleAuthLogin(loginW, loginReq)

	if loginW.Code != http.StatusOK {
		t.Fatalf("Login failed with status %d: %s", loginW.Code, loginW.Body.String())
	}

	var loginResp LoginResponse
	if err := json.NewDecoder(loginW.Body).Decode(&loginResp); err != nil {
		t.Fatalf("Failed to decode login response: %v", err)
	}

	// Step 2: Validate the token and use it to access /me
	claims, err := ValidateJWT(loginResp.Token, s.AuthManager.config.JWTSecret)
	if err != nil {
		t.Fatalf("ValidateJWT failed: %v", err)
	}

	meReq := httptest.NewRequest("GET", "/v1/auth/me", nil)
	meReq = meReq.WithContext(context.WithValue(context.Background(), authContextKey, claims))
	meW := httptest.NewRecorder()

	s.handleAuthMe(meW, meReq)

	if meW.Code != http.StatusOK {
		t.Fatalf("Me endpoint failed with status %d: %s", meW.Code, meW.Body.String())
	}

	var meResp GetMeResponse
	if err := json.NewDecoder(meW.Body).Decode(&meResp); err != nil {
		t.Fatalf("Failed to decode me response: %v", err)
	}

	if meResp.Username != "admin" {
		t.Errorf("Expected username 'admin', got '%s'", meResp.Username)
	}
	if !meResp.Admin {
		t.Error("Expected admin flag to be true")
	}
}

func TestRegisterThenLogin(t *testing.T) {
	s, _, cleanup := setupTestServer(t)
	defer cleanup()

	_ = createAdminAndGetToken(t, s)

	// Step 1: Register a new user as admin
	regBody := `{"username":"newuser","password":"newuserpass"}`
	regReq := httptest.NewRequest("POST", "/v1/auth/register", strings.NewReader(regBody))
	regReq.Header.Set("Content-Type", "application/json")
	regReq = regReq.WithContext(authenticatedContext("admin", true))
	regW := httptest.NewRecorder()

	s.handleAuthRegister(regW, regReq)

	if regW.Code != http.StatusOK {
		t.Fatalf("Register failed with status %d: %s", regW.Code, regW.Body.String())
	}

	// Step 2: Login as the newly registered user
	loginBody := `{"username":"newuser","password":"newuserpass"}`
	loginReq := httptest.NewRequest("POST", "/v1/auth/login", strings.NewReader(loginBody))
	loginReq.Header.Set("Content-Type", "application/json")
	loginW := httptest.NewRecorder()

	s.handleAuthLogin(loginW, loginReq)

	if loginW.Code != http.StatusOK {
		t.Fatalf("Login as new user failed with status %d: %s", loginW.Code, loginW.Body.String())
	}

	var loginResp LoginResponse
	if err := json.NewDecoder(loginW.Body).Decode(&loginResp); err != nil {
		t.Fatalf("Failed to decode login response: %v", err)
	}
	if loginResp.Token == "" {
		t.Error("Expected non-empty token for new user")
	}
}

func TestCreateGroupSetPermissionsAndVerify(t *testing.T) {
	s, _, cleanup := setupTestServer(t)
	defer cleanup()

	adminCtx := authenticatedContext("admin", true)

	// Step 1: Create a group
	createBody := `{"name":"editors","description":"Content editors","members":["alice","bob"]}`
	createReq := httptest.NewRequest("POST", "/v1/auth/groups", strings.NewReader(createBody))
	createReq.Header.Set("Content-Type", "application/json")
	createReq = createReq.WithContext(adminCtx)
	createW := httptest.NewRecorder()

	s.handleAuthGroups(createW, createReq)

	if createW.Code != http.StatusCreated {
		t.Fatalf("Create group failed with status %d: %s", createW.Code, createW.Body.String())
	}

	// Step 2: Set group permissions
	permBody := `{"groupName":"editors","collection":"articles","read":true,"write":true,"admin":false}`
	permReq := httptest.NewRequest("POST", "/v1/auth/group-permissions", strings.NewReader(permBody))
	permReq.Header.Set("Content-Type", "application/json")
	permReq = permReq.WithContext(adminCtx)
	permW := httptest.NewRecorder()

	s.handleAuthGroupPermissions(permW, permReq)

	if permW.Code != http.StatusOK {
		t.Fatalf("Set group permission failed with status %d: %s", permW.Code, permW.Body.String())
	}

	// Step 3: Retrieve group permissions and verify
	getReq := httptest.NewRequest("GET", "/v1/auth/group-permissions?group=editors", nil)
	getReq = getReq.WithContext(adminCtx)
	getW := httptest.NewRecorder()

	s.handleAuthGroupPermissions(getW, getReq)

	if getW.Code != http.StatusOK {
		t.Fatalf("Get group permissions failed with status %d: %s", getW.Code, getW.Body.String())
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(getW.Body).Decode(&resp); err != nil {
		t.Fatalf("Failed to decode group permissions response: %v", err)
	}
	perms, ok := resp["permissions"].([]interface{})
	if !ok {
		t.Fatal("Expected permissions array in response")
	}
	if len(perms) != 1 {
		t.Fatalf("Expected 1 group permission, got %d", len(perms))
	}
}

func TestHandleAuthGroupDetail_UpdateInvalidJSON(t *testing.T) {
	s, _, cleanup := setupTestServer(t)
	defer cleanup()

	_, _ = s.AuthManager.CreateGroup("devs", "Developers", []string{"alice"})

	req := httptest.NewRequest("PUT", "/v1/auth/groups/devs", strings.NewReader("{bad json"))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(authenticatedContext("admin", true))
	w := httptest.NewRecorder()

	s.handleAuthGroupDetail(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("Expected status 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleAuthGroupDetail_EmptyGroupName(t *testing.T) {
	s, _, cleanup := setupTestServer(t)
	defer cleanup()

	// URL path /v1/auth/groups/ with nothing after
	req := httptest.NewRequest("GET", "/v1/auth/groups/", nil)
	req = req.WithContext(authenticatedContext("admin", true))
	w := httptest.NewRecorder()

	s.handleAuthGroupDetail(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("Expected status 400, got %d: %s", w.Code, w.Body.String())
	}
}

// Ensure the bytes import is used by having at least one test reference it
func TestHandleAuthAPIKey_WithExpiresAt(t *testing.T) {
	s, _, cleanup := setupTestServer(t)
	defer cleanup()

	_ = createAdminAndGetToken(t, s)

	expiresAt := time.Now().Add(24 * time.Hour).Unix()
	payload := CreateAPIKeyRequest{
		Description: "expiring key",
		ExpiresAt:   expiresAt,
	}
	bodyBytes, _ := json.Marshal(payload)

	req := httptest.NewRequest("POST", "/v1/auth/api-key", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(authenticatedContext("admin", false))
	w := httptest.NewRecorder()

	s.handleAuthAPIKey(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Expected status 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp CreateAPIKeyResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}
	if resp.ExpiresAt != expiresAt {
		t.Errorf("Expected expiresAt %d, got %d", expiresAt, resp.ExpiresAt)
	}
	if resp.Description != "expiring key" {
		t.Errorf("Expected description 'expiring key', got '%s'", resp.Description)
	}
}

func TestHandleAuthMe_PostMethod(t *testing.T) {
	s, _, cleanup := setupTestServer(t)
	defer cleanup()

	_ = createAdminAndGetToken(t, s)

	// The handleAuthMe handler also accepts POST
	req := httptest.NewRequest("POST", "/v1/auth/me", nil)
	req = req.WithContext(authenticatedContext("admin", true))
	w := httptest.NewRecorder()

	s.handleAuthMe(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Expected status 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp GetMeResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}
	if resp.Username != "admin" {
		t.Errorf("Expected username 'admin', got '%s'", resp.Username)
	}
}
