package main

import (
	"fmt"
	"mddb/internal/audit"
	"net/http"
	"strings"
	"time"

	json "mddb/internal/jsonx"
)

// ---- Request/Response types ----

// LoginRequest is the request body for user login.
type LoginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// LoginResponse is the response body for a successful login.
type LoginResponse struct {
	Token     string `json:"token"`
	ExpiresAt int64  `json:"expiresAt"`
}

// RegisterRequest is the request body for user registration.
type RegisterRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
	Tenant   string `json:"tenant,omitempty"` // confine the new user to a namespace
}

// RegisterResponse is the response body for a successful registration.
type RegisterResponse struct {
	Username  string `json:"username"`
	CreatedAt int64  `json:"createdAt"`
	Tenant    string `json:"tenant,omitempty"`
}

// CreateAPIKeyRequest is the request body for creating an API key.
type CreateAPIKeyRequest struct {
	Description string `json:"description"`
	ExpiresAt   int64  `json:"expiresAt,omitempty"` // 0 = never
}

// CreateAPIKeyResponse is the response body after creating an API key.
type CreateAPIKeyResponse struct {
	Key         string `json:"key"` // shown only once!
	Description string `json:"description"`
	ExpiresAt   int64  `json:"expiresAt"`
	CreatedAt   int64  `json:"createdAt"`
}

// APIKeyListItem represents a single API key in a list response.
type APIKeyListItem struct {
	KeyHash     string `json:"keyHash"` // for deletion
	Description string `json:"description"`
	CreatedAt   int64  `json:"createdAt"`
	ExpiresAt   int64  `json:"expiresAt"`
}

// ListAPIKeysResponse is the response body listing a user's API keys.
type ListAPIKeysResponse struct {
	Keys []APIKeyListItem `json:"keys"`
}

// GetMeResponse is the response body for the current authenticated user.
type GetMeResponse struct {
	Username  string `json:"username"`
	Admin     bool   `json:"admin"`
	CreatedAt int64  `json:"createdAt"`
	Tenant    string `json:"tenant,omitempty"`
}

// SetPermissionRequest is the request body for setting user permissions.
type SetPermissionRequest struct {
	Username   string `json:"username"`
	Collection string `json:"collection"`
	Read       bool   `json:"read"`
	Write      bool   `json:"write"`
	Admin      bool   `json:"admin"`
}

// SetPermissionResponse is the response body after setting permissions.
type SetPermissionResponse struct {
	Status string `json:"status"`
}

// ---- Handlers ----

// handleAuthLogin handles POST /v1/auth/login
func (s *Server) handleAuthLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	var req LoginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid request"}`, http.StatusBadRequest)
		return
	}

	// Authenticate user
	user, err := s.AuthManager.Authenticate(req.Username, req.Password)
	if err != nil {
		ip := ClientIP(r)
		if s.AuditManager != nil {
			s.AuditManager.Record(audit.AuditEvent{
				Actor:     req.Username,
				Action:    "auth.login",
				Resource:  r.URL.Path,
				Result:    "fail",
				IP:        ip,
				UserAgent: r.UserAgent(),
			})
		}
		if s.AuthFailureTracker != nil {
			s.AuthFailureTracker.Record(req.Username, ip)
		}
		http.Error(w, `{"error":"invalid credentials"}`, http.StatusUnauthorized)
		return
	}

	// Check if user has admin privileges
	isAdmin := s.AuthManager.IsAdmin(user.Username)

	// Generate JWT carrying the user's tenant confinement (if any)
	token, err := GenerateTenantJWT(user.Username, user.Tenant, isAdmin, s.AuthManager.config.JWTSecret, s.AuthManager.config.JWTExpiry)
	if err != nil {
		http.Error(w, `{"error":"failed to generate token"}`, http.StatusInternalServerError)
		return
	}
	if s.AuditManager != nil {
		s.AuditManager.Record(audit.AuditEvent{
			Actor:     user.Username,
			Action:    "auth.login",
			Resource:  r.URL.Path,
			Result:    "ok",
			IP:        ClientIP(r),
			UserAgent: r.UserAgent(),
		})
	}

	// Calculate expiry
	expiresAt := time.Now().Add(s.AuthManager.config.JWTExpiry).Unix()

	resp := LoginResponse{
		Token:     token,
		ExpiresAt: expiresAt,
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		http.Error(w, `{"error":"failed to encode response"}`, http.StatusInternalServerError)
	}
}

// handleAuthRegister handles POST /v1/auth/register (admin only)
func (s *Server) handleAuthRegister(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	// Check admin permission
	claims, ok := GetClaimsFromContext(r.Context())
	if !ok || !claims.Admin {
		http.Error(w, `{"error":"admin access required"}`, http.StatusForbidden)
		return
	}

	var req RegisterRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid request"}`, http.StatusBadRequest)
		return
	}

	// Create user (optionally confined to a tenant namespace)
	user, err := s.AuthManager.CreateTenantUser(req.Username, req.Password, req.Tenant)
	if err != nil {
		switch err {
		case ErrUserExists:
			http.Error(w, `{"error":"user already exists"}`, http.StatusConflict)
		case ErrInvalidTenant:
			http.Error(w, `{"error":"invalid tenant name"}`, http.StatusBadRequest)
		default:
			http.Error(w, `{"error":"failed to create user"}`, http.StatusInternalServerError)
		}
		return
	}

	resp := RegisterResponse{
		Username:  user.Username,
		CreatedAt: user.CreatedAt,
		Tenant:    user.Tenant,
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		http.Error(w, `{"error":"failed to encode response"}`, http.StatusInternalServerError)
	}
}

// handleAuthAPIKey handles POST /v1/auth/api-key
func (s *Server) handleAuthAPIKey(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	// Check authentication
	claims, ok := GetClaimsFromContext(r.Context())
	if !ok {
		http.Error(w, `{"error":"authentication required"}`, http.StatusUnauthorized)
		return
	}

	var req CreateAPIKeyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid request"}`, http.StatusBadRequest)
		return
	}

	// Create API key
	key, err := s.AuthManager.CreateAPIKey(claims.Username, req.Description, req.ExpiresAt)
	if err != nil {
		http.Error(w, `{"error":"failed to create api key"}`, http.StatusInternalServerError)
		return
	}

	resp := CreateAPIKeyResponse{
		Key:         key,
		Description: req.Description,
		ExpiresAt:   req.ExpiresAt,
		CreatedAt:   time.Now().Unix(),
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		http.Error(w, `{"error":"failed to encode response"}`, http.StatusInternalServerError)
	}
}

// handleAuthAPIKeysList handles GET /v1/auth/api-keys
func (s *Server) handleAuthAPIKeysList(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	// Check authentication
	claims, ok := GetClaimsFromContext(r.Context())
	if !ok {
		http.Error(w, `{"error":"authentication required"}`, http.StatusUnauthorized)
		return
	}

	// Get user's API keys
	apiKeys, err := s.AuthManager.ListAPIKeys(claims.Username)
	if err != nil {
		http.Error(w, `{"error":"failed to list api keys"}`, http.StatusInternalServerError)
		return
	}

	// Convert to response format (without actual key values)
	items := make([]APIKeyListItem, 0, len(apiKeys))
	for _, key := range apiKeys {
		items = append(items, APIKeyListItem{
			KeyHash:     key.KeyHash,
			Description: key.Description,
			CreatedAt:   key.CreatedAt,
			ExpiresAt:   key.ExpiresAt,
		})
	}

	resp := ListAPIKeysResponse{Keys: items}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		http.Error(w, `{"error":"failed to encode response"}`, http.StatusInternalServerError)
	}
}

// handleAuthAPIKeyDelete handles DELETE /v1/auth/api-keys/:keyHash
func (s *Server) handleAuthAPIKeyDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != "DELETE" {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	// Check authentication
	claims, ok := GetClaimsFromContext(r.Context())
	if !ok {
		http.Error(w, `{"error":"authentication required"}`, http.StatusUnauthorized)
		return
	}

	// Extract keyHash from path: /v1/auth/api-keys/abc123...
	keyHash := strings.TrimPrefix(r.URL.Path, "/v1/auth/api-keys/")
	if keyHash == "" {
		http.Error(w, `{"error":"key hash required"}`, http.StatusBadRequest)
		return
	}

	// Delete API key
	switch err := s.AuthManager.DeleteAPIKey(claims.Username, keyHash); err {
	case nil:
		// Success, continue
	case ErrAPIKeyNotFound:
		http.Error(w, `{"error":"api key not found"}`, http.StatusNotFound)
		return
	case ErrForbidden:
		http.Error(w, `{"error":"forbidden: you can only delete your own api keys"}`, http.StatusForbidden)
		return
	default:
		http.Error(w, `{"error":"failed to delete api key"}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]string{"status": "deleted"}); err != nil {
		http.Error(w, `{"error":"failed to encode response"}`, http.StatusInternalServerError)
	}
}

// handleAuthMe handles GET /v1/auth/me
func (s *Server) handleAuthMe(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" && r.Method != "POST" {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	claims, ok := GetClaimsFromContext(r.Context())
	if !ok {
		http.Error(w, `{"error":"authentication required"}`, http.StatusUnauthorized)
		return
	}

	user, err := s.AuthManager.GetUser(claims.Username)
	if err != nil {
		http.Error(w, `{"error":"user not found"}`, http.StatusNotFound)
		return
	}

	resp := GetMeResponse{
		Username:  user.Username,
		Admin:     claims.Admin,
		CreatedAt: user.CreatedAt,
		Tenant:    user.Tenant,
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		http.Error(w, `{"error":"failed to encode response"}`, http.StatusInternalServerError)
	}
}

// handleAuthPermissions handles POST /v1/auth/permissions and GET /v1/auth/permissions
func (s *Server) handleAuthPermissions(w http.ResponseWriter, r *http.Request) {
	// Check admin permission
	claims, ok := GetClaimsFromContext(r.Context())
	if !ok || !claims.Admin {
		http.Error(w, `{"error":"admin access required"}`, http.StatusForbidden)
		return
	}

	switch r.Method {
	case "POST":
		// Set permissions
		var req SetPermissionRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, `{"error":"invalid request"}`, http.StatusBadRequest)
			return
		}

		perm := &Permission{
			Username:   req.Username,
			Collection: req.Collection,
			Read:       req.Read,
			Write:      req.Write,
			Admin:      req.Admin,
		}

		if err := s.AuthManager.SetPermission(perm); err != nil {
			http.Error(w, `{"error":"failed to set permission"}`, http.StatusInternalServerError)
			return
		}

		resp := SetPermissionResponse{Status: "ok"}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			http.Error(w, `{"error":"failed to encode response"}`, http.StatusInternalServerError)
		}

	case "GET":
		// Get permissions
		username := r.URL.Query().Get("username")
		if username == "" {
			http.Error(w, `{"error":"username parameter required"}`, http.StatusBadRequest)
			return
		}

		perms := s.AuthManager.GetPermissions(username)
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(perms); err != nil {
			http.Error(w, `{"error":"failed to encode response"}`, http.StatusInternalServerError)
		}

	default:
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
	}
}

// handleAuthDeleteUser handles DELETE /v1/auth/users/:username
func (s *Server) handleAuthDeleteUser(w http.ResponseWriter, r *http.Request) {
	if r.Method != "DELETE" {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	// Check admin permission
	claims, ok := GetClaimsFromContext(r.Context())
	if !ok || !claims.Admin {
		http.Error(w, `{"error":"admin access required"}`, http.StatusForbidden)
		return
	}

	// Extract username from path: /v1/auth/users/alice
	path := strings.TrimPrefix(r.URL.Path, "/v1/auth/users/")
	if path == "" {
		http.Error(w, `{"error":"username required"}`, http.StatusBadRequest)
		return
	}

	if err := s.AuthManager.DeleteUser(path); err != nil {
		if err == ErrUserNotFound {
			http.Error(w, `{"error":"user not found"}`, http.StatusNotFound)
		} else {
			http.Error(w, `{"error":"failed to delete user"}`, http.StatusInternalServerError)
		}
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]string{"status": "deleted"}); err != nil {
		http.Error(w, `{"error":"failed to encode response"}`, http.StatusInternalServerError)
	}
}

// ---- Users List Handler ----

// UserInfoResponse represents a single user in the users list.
type UserInfoResponse struct {
	Username  string   `json:"username"`
	CreatedAt int64    `json:"createdAt"`
	Disabled  bool     `json:"disabled"`
	Admin     bool     `json:"admin"`
	Groups    []string `json:"groups"`
}

// UsersListResponse is the response body listing all users.
type UsersListResponse struct {
	Users []UserInfoResponse `json:"users"`
}

// handleAuthUsersList lists all users (admin only)
func (s *Server) handleAuthUsersList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	// Check admin permission
	claims, ok := GetClaimsFromContext(r.Context())
	if !ok || !claims.Admin {
		http.Error(w, `{"error":"admin access required"}`, http.StatusForbidden)
		return
	}

	users := s.AuthManager.ListAllUsers()
	userInfos := make([]UserInfoResponse, 0, len(users))

	for _, u := range users {
		userInfos = append(userInfos, UserInfoResponse{
			Username:  u.Username,
			CreatedAt: u.CreatedAt,
			Disabled:  u.Disabled,
			Admin:     s.AuthManager.IsAdmin(u.Username),
			Groups:    s.AuthManager.GetUserGroups(u.Username),
		})
	}

	response := UsersListResponse{Users: userInfos}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(response); err != nil {
		http.Error(w, `{"error":"failed to encode response"}`, http.StatusInternalServerError)
	}
}

// ---- Group Handlers ----

// CreateGroupRequest is the request body for creating a user group.
type CreateGroupRequest struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Members     []string `json:"members"`
}

// UpdateGroupRequest is the request body for updating a user group.
type UpdateGroupRequest struct {
	Description string   `json:"description"`
	Members     []string `json:"members"`
}

// GroupsListResponse is the response body listing all groups.
type GroupsListResponse struct {
	Groups []*Group `json:"groups"`
}

// handleAuthGroups handles GET (list) and POST (create) for groups
func (s *Server) handleAuthGroups(w http.ResponseWriter, r *http.Request) {
	// Check admin permission
	claims, ok := GetClaimsFromContext(r.Context())
	if !ok || !claims.Admin {
		http.Error(w, `{"error":"admin access required"}`, http.StatusForbidden)
		return
	}

	switch r.Method {
	case http.MethodGet:
		// List all groups
		groups := s.AuthManager.ListGroups()
		response := GroupsListResponse{Groups: groups}

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(response); err != nil {
			http.Error(w, `{"error":"failed to encode response"}`, http.StatusInternalServerError)
		}

	case http.MethodPost:
		// Create new group
		var req CreateGroupRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
			return
		}

		if req.Name == "" {
			http.Error(w, `{"error":"group name is required"}`, http.StatusBadRequest)
			return
		}

		group, err := s.AuthManager.CreateGroup(req.Name, req.Description, req.Members)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), http.StatusBadRequest)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		if err := json.NewEncoder(w).Encode(group); err != nil {
			http.Error(w, `{"error":"failed to encode response"}`, http.StatusInternalServerError)
		}

	default:
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
	}
}

// handleAuthGroupDetail handles GET, PUT, DELETE for specific group
func (s *Server) handleAuthGroupDetail(w http.ResponseWriter, r *http.Request) {
	// Check admin permission
	claims, ok := GetClaimsFromContext(r.Context())
	if !ok || !claims.Admin {
		http.Error(w, `{"error":"admin access required"}`, http.StatusForbidden)
		return
	}

	// Extract group name from path: /v1/auth/groups/developers
	groupName := strings.TrimPrefix(r.URL.Path, "/v1/auth/groups/")
	if groupName == "" {
		http.Error(w, `{"error":"group name required"}`, http.StatusBadRequest)
		return
	}

	switch r.Method {
	case http.MethodGet:
		// Get group details
		group, err := s.AuthManager.GetGroup(groupName)
		if err != nil {
			http.Error(w, `{"error":"group not found"}`, http.StatusNotFound)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(group); err != nil {
			http.Error(w, `{"error":"failed to encode response"}`, http.StatusInternalServerError)
		}

	case http.MethodPut:
		// Update group
		var req UpdateGroupRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
			return
		}

		group, err := s.AuthManager.UpdateGroup(groupName, req.Description, req.Members)
		if err != nil {
			if err.Error() == "group not found" {
				http.Error(w, `{"error":"group not found"}`, http.StatusNotFound)
			} else {
				http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), http.StatusBadRequest)
			}
			return
		}

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(group); err != nil {
			http.Error(w, `{"error":"failed to encode response"}`, http.StatusInternalServerError)
		}

	case http.MethodDelete:
		// Delete group
		if err := s.AuthManager.DeleteGroup(groupName); err != nil {
			if err.Error() == "group not found" {
				http.Error(w, `{"error":"group not found"}`, http.StatusNotFound)
			} else {
				http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), http.StatusInternalServerError)
			}
			return
		}

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]string{"status": "deleted"}); err != nil {
			http.Error(w, `{"error":"failed to encode response"}`, http.StatusInternalServerError)
		}

	default:
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
	}
}

// handleAuthGroupPermissions handles GET and POST for group permissions
func (s *Server) handleAuthGroupPermissions(w http.ResponseWriter, r *http.Request) {
	// Check admin permission
	claims, ok := GetClaimsFromContext(r.Context())
	if !ok || !claims.Admin {
		http.Error(w, `{"error":"admin access required"}`, http.StatusForbidden)
		return
	}

	switch r.Method {
	case http.MethodGet:
		// Get group permissions
		groupName := r.URL.Query().Get("group")
		if groupName == "" {
			http.Error(w, `{"error":"group parameter required"}`, http.StatusBadRequest)
			return
		}

		perms := s.AuthManager.GetGroupPermissions(groupName)

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]interface{}{"permissions": perms}); err != nil {
			http.Error(w, `{"error":"failed to encode response"}`, http.StatusInternalServerError)
		}

	case http.MethodPost:
		// Set group permission
		var gp GroupPermission
		if err := json.NewDecoder(r.Body).Decode(&gp); err != nil {
			http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
			return
		}

		if err := s.AuthManager.SetGroupPermission(&gp); err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), http.StatusBadRequest)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]string{"status": "permission set"}); err != nil {
			http.Error(w, `{"error":"failed to encode response"}`, http.StatusInternalServerError)
		}

	default:
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
	}
}
