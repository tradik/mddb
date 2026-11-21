package main

// Error answers of the auth handlers: the audit trail of a login, refused
// input, and storage that fails under a handler.

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"mddb/internal/audit"
)

// authErrDo runs handler for one request, authenticated as actor (an admin
// when admin is set) unless actor is empty.
func authErrDo(handler http.HandlerFunc, method, target, body, actor string, admin bool) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	if actor != "" {
		req = req.WithContext(authenticatedContext(actor, admin))
	}
	w := httptest.NewRecorder()
	handler(w, req)
	return w
}

// A login is recorded in the audit log, failed or not, and a failure is
// counted towards the incident detector.
func TestHandleAuthLogin_RecordsTheAttempt(t *testing.T) {
	s, _, cleanup := setupTestServer(t)
	defer cleanup()
	_ = createAdminAndGetToken(t, s)
	am, stop := newAuditTestManager(t, 90)
	defer stop()
	s.AuditManager = am
	s.AuthFailureTracker = NewAuthFailureTracker(nil)

	if w := authErrDo(s.handleAuthLogin, http.MethodPost, "/v1/auth/login", `{"username":"admin","password":"wrong"}`, "", false); w.Code != http.StatusUnauthorized {
		t.Fatalf("bad password: status %d, want 401", w.Code)
	}
	if w := authErrDo(s.handleAuthLogin, http.MethodPost, "/v1/auth/login", `{"username":"admin","password":"adminpass"}`, "", false); w.Code != http.StatusOK {
		t.Fatalf("good password: status %d, want 200: %s", w.Code, w.Body.String())
	}
	waitFlush(t, am, 2)
	for _, result := range []string{"fail", "ok"} {
		events, err := am.Query(audit.QueryFilter{Action: "auth.login", Result: result, Limit: 10})
		if err != nil || len(events) != 1 {
			t.Errorf("auth.login %s events = %d (%v), want 1", result, len(events), err)
		}
	}
}

func TestAuthHandlers_RefusedInput(t *testing.T) {
	s, _, cleanup := setupTestServer(t)
	defer cleanup()
	_ = createAdminAndGetToken(t, s)
	if _, err := s.AuthManager.CreateGroup("editors", "", nil); err != nil {
		t.Fatalf("CreateGroup: %v", err)
	}

	cases := []struct {
		name    string
		handler http.HandlerFunc
		method  string
		target  string
		body    string
		want    int
	}{
		{"register into a bad tenant", s.handleAuthRegister, http.MethodPost, "/v1/auth/register", `{"username":"t1","password":"pw","tenant":"Not A Tenant!"}`, http.StatusBadRequest},
		{"api key delete by GET", s.handleAuthAPIKeyDelete, http.MethodGet, "/v1/auth/api-keys/abc", "", http.StatusMethodNotAllowed},
		{"api key delete without a hash", s.handleAuthAPIKeyDelete, http.MethodDelete, "/v1/auth/api-keys/", "", http.StatusBadRequest},
		{"group that exists", s.handleAuthGroups, http.MethodPost, "/v1/auth/groups", `{"name":"editors"}`, http.StatusBadRequest},
		{"grant to a missing group", s.handleAuthGroupPermissions, http.MethodPost, "/v1/auth/group-permissions", `{"groupName":"nobody","collection":"blog","read":true}`, http.StatusBadRequest},
	}
	for _, tc := range cases {
		if w := authErrDo(tc.handler, tc.method, tc.target, tc.body, "admin", true); w.Code != tc.want {
			t.Errorf("%s: status %d, want %d: %s", tc.name, w.Code, tc.want, w.Body.String())
		}
	}
}

// Every write the auth handlers make fails cleanly when the database does.
func TestAuthHandlers_StorageFailure(t *testing.T) {
	s, _, cleanup := setupTestServer(t)
	_ = createAdminAndGetToken(t, s)
	am := s.AuthManager
	if _, err := am.CreateUser("alice", "pw"); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	key, err := am.CreateAPIKey("admin", "ci", 0)
	if err != nil {
		t.Fatalf("CreateAPIKey: %v", err)
	}
	if _, err := am.CreateGroup("editors", "", nil); err != nil {
		t.Fatalf("CreateGroup: %v", err)
	}
	cleanup() // from here on every transaction fails

	cases := []struct {
		name    string
		handler http.HandlerFunc
		method  string
		target  string
		body    string
		want    int
	}{
		{"register", s.handleAuthRegister, http.MethodPost, "/v1/auth/register", `{"username":"bob","password":"pw"}`, http.StatusInternalServerError},
		{"create api key", s.handleAuthAPIKey, http.MethodPost, "/v1/auth/api-key", `{"description":"x"}`, http.StatusInternalServerError},
		{"delete api key", s.handleAuthAPIKeyDelete, http.MethodDelete, "/v1/auth/api-keys/" + HashAPIKey(key), "", http.StatusInternalServerError},
		{"set permission", s.handleAuthPermissions, http.MethodPost, "/v1/auth/permissions", `{"username":"alice","collection":"blog","read":true}`, http.StatusInternalServerError},
		{"delete user", s.handleAuthDeleteUser, http.MethodDelete, "/v1/auth/users/alice", "", http.StatusInternalServerError},
		{"update group", s.handleAuthGroupDetail, http.MethodPut, "/v1/auth/groups/editors", `{"description":"d"}`, http.StatusBadRequest},
		{"delete group", s.handleAuthGroupDetail, http.MethodDelete, "/v1/auth/groups/editors", "", http.StatusInternalServerError},
	}
	for _, tc := range cases {
		if w := authErrDo(tc.handler, tc.method, tc.target, tc.body, "admin", true); w.Code != tc.want {
			t.Errorf("%s: status %d, want %d: %s", tc.name, w.Code, tc.want, w.Body.String())
		}
	}
}
