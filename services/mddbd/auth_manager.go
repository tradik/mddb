package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	bolt "go.etcd.io/bbolt"
	"log/slog"
	json "mddb/internal/jsonx"
	"sync"
	"time"
)

var (
	bucketAuthUsers      = []byte("auth_users")
	bucketAuthAPIKeys    = []byte("auth_apikeys")
	bucketAuthPerms      = []byte("auth_permissions")
	bucketAuthGroups     = []byte("auth_groups")
	bucketAuthGroupPerms = []byte("auth_group_perms")
)

// AuthManager manages authentication and authorization
type AuthManager struct {
	db      *bolt.DB
	config  AuthConfig
	enabled bool
	server  *Server // set after construction; used only for audit log hooks

	// publicEndpoints is the auth-exempt path set, computed once from the
	// environment at construction (see buildPublicEndpoints / SEC-009).
	publicEndpoints map[string]bool

	// In-memory caches
	mu               sync.RWMutex
	users            map[string]*User
	apiKeys          map[string]*APIKey            // keyHash -> APIKey
	permissions      map[string][]*Permission      // username -> permissions
	groups           map[string]*Group             // groupName -> Group
	groupPermissions map[string][]*GroupPermission // groupName -> permissions
}

// SetServer wires the owning Server for audit log emission.
func (am *AuthManager) SetServer(s *Server) {
	if am == nil {
		return
	}
	am.server = s
}

// NewAuthManager creates a new authentication manager
func NewAuthManager(db *bolt.DB, config AuthConfig) *AuthManager {
	return &AuthManager{
		db:               db,
		config:           config,
		enabled:          true,
		publicEndpoints:  buildPublicEndpoints(),
		users:            make(map[string]*User),
		apiKeys:          make(map[string]*APIKey),
		permissions:      make(map[string][]*Permission),
		groups:           make(map[string]*Group),
		groupPermissions: make(map[string][]*GroupPermission),
	}
}

// IsEnabled returns whether auth is enabled
func (am *AuthManager) IsEnabled() bool {
	return am.enabled
}

// EnsureBuckets creates auth buckets if they don't exist
func (am *AuthManager) EnsureBuckets() error {
	return am.db.Update(func(tx *bolt.Tx) error {
		if _, err := tx.CreateBucketIfNotExists(bucketAuthUsers); err != nil {
			return err
		}
		if _, err := tx.CreateBucketIfNotExists(bucketAuthAPIKeys); err != nil {
			return err
		}
		if _, err := tx.CreateBucketIfNotExists(bucketAuthPerms); err != nil {
			return err
		}
		if _, err := tx.CreateBucketIfNotExists(bucketAuthGroups); err != nil {
			return err
		}
		if _, err := tx.CreateBucketIfNotExists(bucketAuthGroupPerms); err != nil {
			return err
		}
		return nil
	})
}

// LoadAll loads all auth data from database into memory
func (am *AuthManager) LoadAll() error {
	var users []*User
	var apiKeys []*APIKey
	var permissions []*Permission
	var groups []*Group
	var groupPermissions []*GroupPermission

	err := am.db.View(func(tx *bolt.Tx) error {
		// Load users
		b := tx.Bucket(bucketAuthUsers)
		if b != nil {
			if err := b.ForEach(func(k, v []byte) error {
				var u User
				if err := json.Unmarshal(v, &u); err != nil {
					return nil // skip corrupt entries
				}
				users = append(users, &u)
				return nil
			}); err != nil {
				return err
			}
		}

		// Load API keys
		b = tx.Bucket(bucketAuthAPIKeys)
		if b != nil {
			if err := b.ForEach(func(k, v []byte) error {
				var key APIKey
				if err := json.Unmarshal(v, &key); err != nil {
					return nil // skip corrupt entries
				}
				apiKeys = append(apiKeys, &key)
				return nil
			}); err != nil {
				return err
			}
		}

		// Load permissions
		b = tx.Bucket(bucketAuthPerms)
		if b != nil {
			if err := b.ForEach(func(k, v []byte) error {
				var perm Permission
				if err := json.Unmarshal(v, &perm); err != nil {
					return nil // skip corrupt entries
				}
				permissions = append(permissions, &perm)
				return nil
			}); err != nil {
				return err
			}
		}

		// Load groups
		b = tx.Bucket(bucketAuthGroups)
		if b != nil {
			if err := b.ForEach(func(k, v []byte) error {
				var g Group
				if err := json.Unmarshal(v, &g); err != nil {
					return nil // skip corrupt entries
				}
				groups = append(groups, &g)
				return nil
			}); err != nil {
				return err
			}
		}

		// Load group permissions
		b = tx.Bucket(bucketAuthGroupPerms)
		if b != nil {
			if err := b.ForEach(func(k, v []byte) error {
				var gp GroupPermission
				if err := json.Unmarshal(v, &gp); err != nil {
					return nil // skip corrupt entries
				}
				groupPermissions = append(groupPermissions, &gp)
				return nil
			}); err != nil {
				return err
			}
		}

		return nil
	})

	if err != nil {
		return err
	}

	// Update in-memory caches
	am.mu.Lock()
	am.users = make(map[string]*User)
	am.apiKeys = make(map[string]*APIKey)
	am.permissions = make(map[string][]*Permission)
	am.groups = make(map[string]*Group)
	am.groupPermissions = make(map[string][]*GroupPermission)

	for _, u := range users {
		am.users[u.Username] = u
	}

	for _, k := range apiKeys {
		am.apiKeys[k.KeyHash] = k
	}

	for _, p := range permissions {
		am.permissions[p.Username] = append(am.permissions[p.Username], p)
	}

	for _, g := range groups {
		am.groups[g.Name] = g
	}

	for _, gp := range groupPermissions {
		am.groupPermissions[gp.GroupName] = append(am.groupPermissions[gp.GroupName], gp)
	}

	am.mu.Unlock()
	return nil
}

// BootstrapAdmin creates initial admin user if it doesn't exist
func (am *AuthManager) BootstrapAdmin() error {
	if am.config.AdminUsername == "" || am.config.AdminPassword == "" {
		slog.Info("No admin credentials configured, skipping bootstrap")
		return nil
	}

	// Check if admin already exists
	am.mu.RLock()
	_, exists := am.users[am.config.AdminUsername]
	am.mu.RUnlock()

	if exists {
		slog.Info("Admin user already exists", "adminUsername", am.config.AdminUsername)
		return nil
	}

	// Create admin user
	user, err := am.CreateUser(am.config.AdminUsername, am.config.AdminPassword)
	if err != nil {
		return fmt.Errorf("bootstrap admin: %w", err)
	}

	// Grant admin permissions (wildcard collection)
	perm := &Permission{
		Username:   user.Username,
		Collection: "*",
		Read:       true,
		Write:      true,
		Admin:      true,
	}

	if err := am.SetPermission(perm); err != nil {
		return fmt.Errorf("bootstrap admin permissions: %w", err)
	}

	slog.Info("Admin user created successfully", "adminUsername", am.config.AdminUsername)
	return nil
}

// ---- User management ----

// CreateUser creates a new global (non-tenant) user
func (am *AuthManager) CreateUser(username, password string) (*User, error) {
	return am.CreateTenantUser(username, password, "")
}

// CreateTenantUser creates a user confined to a tenant namespace.
// An empty tenant creates a global user (identical to CreateUser).
func (am *AuthManager) CreateTenantUser(username, password, tenant string) (*User, error) {
	if username == "" || password == "" {
		return nil, errors.New("username and password required")
	}
	if !ValidTenantName(tenant) {
		return nil, ErrInvalidTenant
	}

	// Check if user exists
	am.mu.RLock()
	if _, exists := am.users[username]; exists {
		am.mu.RUnlock()
		return nil, ErrUserExists
	}
	am.mu.RUnlock()

	// Hash password
	passwordHash, err := HashPassword(password)
	if err != nil {
		return nil, fmt.Errorf("hash password: %w", err)
	}

	user := &User{
		Username:     username,
		PasswordHash: passwordHash,
		CreatedAt:    time.Now().Unix(),
		Disabled:     false,
		Tenant:       tenant,
	}

	// Save to database
	data, _ := json.Marshal(user)
	if err := am.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketAuthUsers)
		return b.Put([]byte("user|"+username), data)
	}); err != nil {
		return nil, err
	}

	// Update cache
	am.mu.Lock()
	am.users[username] = user
	am.mu.Unlock()

	return user, nil
}

// GetUser retrieves a user by username
func (am *AuthManager) GetUser(username string) (*User, error) {
	am.mu.RLock()
	user, exists := am.users[username]
	am.mu.RUnlock()

	if !exists {
		return nil, ErrUserNotFound
	}

	return user, nil
}

// DeleteUser soft-deletes a user
// DeleteUser removes a user and everything that grants them access.
//
// It used to set Disabled and keep the record, while answering
// {"status":"deleted"} and leaving the name permanently taken — a subsequent
// register for it returned 409, so delete-then-register, which is how a
// tenant's credentials are rotated, could not work for any name that had ever
// been used (#213).
//
// Re-enabling the disabled record on register would have been the smaller
// change and is the more dangerous one: the record carries the user's
// permissions and group memberships, so a name that looked free would hand
// whoever claimed it the privileges of whoever held it before. A hard delete
// gives register a genuinely new user with nothing attached.
//
// Four things reference a user, and leaving any of them is what would make the
// deletion look complete while remaining a way in:
//
//	auth_users        the record itself
//	auth_apikeys      credentials that authenticate as them
//	auth_permissions  per-collection grants, keyed by username
//	auth_groups       membership, which carries the group's grants
//
// The audit log is deliberately untouched: it is where the record of who
// existed and who removed them belongs, and it outlives the user by design.
func (am *AuthManager) DeleteUser(username string) error {
	am.mu.RLock()
	_, exists := am.users[username]
	var ownedKeyHashes []string
	for hash, key := range am.apiKeys {
		if key.Username == username {
			ownedKeyHashes = append(ownedKeyHashes, hash)
		}
	}
	memberOf := make([]string, 0, len(am.groups))
	for name, group := range am.groups {
		for _, member := range group.Members {
			if member == username {
				memberOf = append(memberOf, name)
				break
			}
		}
	}
	am.mu.RUnlock()

	if !exists {
		return ErrUserNotFound
	}

	// One transaction: a delete that half-succeeds leaves a user without a
	// record but with the keys and grants that let them in.
	if err := am.db.Update(func(tx *bolt.Tx) error {
		if err := tx.Bucket(bucketAuthUsers).Delete([]byte("user|" + username)); err != nil {
			return err
		}

		keys := tx.Bucket(bucketAuthAPIKeys)
		for _, hash := range ownedKeyHashes {
			if err := keys.Delete([]byte("apikey|" + hash)); err != nil {
				return err
			}
		}

		// Permissions are keyed perm|<username>|<collection>, so the user's
		// grants are a prefix range rather than a known set.
		perms := tx.Bucket(bucketAuthPerms)
		prefix := []byte("perm|" + username + "|")
		cur := perms.Cursor()
		var doomed [][]byte
		for k, _ := cur.Seek(prefix); k != nil && bytes.HasPrefix(k, prefix); k, _ = cur.Next() {
			doomed = append(doomed, append([]byte(nil), k...))
		}
		for _, k := range doomed {
			if err := perms.Delete(k); err != nil {
				return err
			}
		}

		groups := tx.Bucket(bucketAuthGroups)
		for _, name := range memberOf {
			// Groups are keyed by bare name, unlike users and permissions.
			// Assuming the "group|" prefix the other buckets use would have
			// found nothing here and left the membership — and the grants that
			// come with it — in place, silently.
			raw := groups.Get([]byte(name))
			if raw == nil {
				continue
			}
			var g Group
			if err := json.Unmarshal(raw, &g); err != nil {
				return err
			}
			members := g.Members[:0]
			for _, member := range g.Members {
				if member != username {
					members = append(members, member)
				}
			}
			g.Members = members
			data, err := json.Marshal(&g)
			if err != nil {
				return err
			}
			if err := groups.Put([]byte(name), data); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return err
	}

	am.mu.Lock()
	delete(am.users, username)
	delete(am.permissions, username)
	for _, hash := range ownedKeyHashes {
		delete(am.apiKeys, hash)
	}
	for _, name := range memberOf {
		if g, ok := am.groups[name]; ok {
			members := make([]string, 0, len(g.Members))
			for _, member := range g.Members {
				if member != username {
					members = append(members, member)
				}
			}
			g.Members = members
		}
	}
	am.mu.Unlock()

	return nil
}

// Authenticate validates username and password
func (am *AuthManager) Authenticate(username, password string) (*User, error) {
	user, err := am.GetUser(username)
	if err != nil {
		return nil, ErrInvalidCredentials
	}

	if user.Disabled {
		return nil, ErrInvalidCredentials
	}

	if err := VerifyPassword(password, user.PasswordHash); err != nil {
		return nil, ErrInvalidCredentials
	}

	return user, nil
}

// ---- API Key management ----

// CreateAPIKey creates a new API key for a user
func (am *AuthManager) CreateAPIKey(username, description string, expiresAt int64) (string, error) {
	// Verify user exists
	if _, err := am.GetUser(username); err != nil {
		return "", err
	}

	// Generate API key
	key, err := GenerateAPIKey()
	if err != nil {
		return "", err
	}

	keyHash := HashAPIKey(key)

	apiKey := &APIKey{
		KeyHash:     keyHash,
		Username:    username,
		CreatedAt:   time.Now().Unix(),
		ExpiresAt:   expiresAt,
		Description: description,
	}

	// Save to database
	data, _ := json.Marshal(apiKey)
	if err := am.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketAuthAPIKeys)
		return b.Put([]byte("apikey|"+keyHash), data)
	}); err != nil {
		return "", err
	}

	// Update cache
	am.mu.Lock()
	am.apiKeys[keyHash] = apiKey
	am.mu.Unlock()

	return key, nil // Return plaintext key (only shown once)
}

// ValidateAPIKey validates an API key and returns the username
func (am *AuthManager) ValidateAPIKey(key string) (string, error) {
	keyHash := HashAPIKey(key)

	am.mu.RLock()
	apiKey, exists := am.apiKeys[keyHash]
	am.mu.RUnlock()

	if !exists {
		return "", ErrAPIKeyNotFound
	}

	if apiKey.IsExpired() {
		return "", ErrAPIKeyExpired
	}

	return apiKey.Username, nil
}

// ListAPIKeys lists all API keys for a user (without key values)
func (am *AuthManager) ListAPIKeys(username string) ([]*APIKey, error) {
	am.mu.RLock()
	defer am.mu.RUnlock()

	var keys []*APIKey
	for _, apiKey := range am.apiKeys {
		if apiKey.Username == username {
			keys = append(keys, apiKey)
		}
	}

	return keys, nil
}

// DeleteAPIKey deletes an API key by its hash
func (am *AuthManager) DeleteAPIKey(username, keyHash string) error {
	// Verify user owns this key
	am.mu.RLock()
	apiKey, exists := am.apiKeys[keyHash]
	am.mu.RUnlock()

	if !exists {
		return ErrAPIKeyNotFound
	}

	if apiKey.Username != username {
		return ErrForbidden
	}

	// Delete from database
	if err := am.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketAuthAPIKeys)
		return b.Delete([]byte("apikey|" + keyHash))
	}); err != nil {
		return err
	}

	// Remove from cache
	am.mu.Lock()
	delete(am.apiKeys, keyHash)
	am.mu.Unlock()

	return nil
}

// ---- Permission management ----

// SetPermission sets permissions for a user on a collection
func (am *AuthManager) SetPermission(perm *Permission) error {
	// Verify user exists
	if _, err := am.GetUser(perm.Username); err != nil {
		return err
	}

	// Save to database
	key := fmt.Sprintf("perm|%s|%s", perm.Username, perm.Collection)
	data, _ := json.Marshal(perm)
	if err := am.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketAuthPerms)
		return b.Put([]byte(key), data)
	}); err != nil {
		return err
	}

	// Update cache
	am.mu.Lock()
	// Remove existing permission for this collection
	filtered := []*Permission{}
	for _, p := range am.permissions[perm.Username] {
		if p.Collection != perm.Collection {
			filtered = append(filtered, p)
		}
	}
	filtered = append(filtered, perm)
	am.permissions[perm.Username] = filtered
	am.mu.Unlock()

	return nil
}

// GetPermissions returns all permissions for a user
func (am *AuthManager) GetPermissions(username string) []*Permission {
	am.mu.RLock()
	defer am.mu.RUnlock()

	perms := am.permissions[username]
	result := make([]*Permission, len(perms))
	copy(result, perms)
	return result
}

// CheckPermission checks if user has permission for operation on collection
func (am *AuthManager) CheckPermission(ctx context.Context, collection string, operation PermissionType) error {
	if !am.enabled {
		return nil // Auth disabled = allow all
	}

	claims, ok := GetClaimsFromContext(ctx)
	if !ok {
		return ErrUnauthorized
	}

	// Tenant isolation gate: a tenant user may only touch collections inside
	// their namespace. "*" is allowed through only as a read scope request
	// (listing endpoints, which filter results via TenantFromContext) —
	// write/admin operations on "*" are global and always denied to tenants.
	if claims.Tenant != "" {
		if collection == "*" {
			if operation != PermRead {
				return ErrForbidden
			}
		} else if !CollectionInTenant(claims.Tenant, collection) {
			return ErrForbidden
		}
	}

	// Global admins bypass all checks (GenerateTenantJWT guarantees the
	// Admin flag is never set on tenant claims).
	if claims.Admin {
		return nil
	}

	am.mu.RLock()
	defer am.mu.RUnlock()

	username := claims.Username
	perms := am.permissions[username]

	// Check user's direct collection-specific permission
	for _, p := range perms {
		if p.Collection == collection && p.HasPermission(operation) {
			return nil
		}
	}

	// Check user's direct wildcard permission
	for _, p := range perms {
		if p.Collection == "*" && p.HasPermission(operation) {
			return nil
		}
	}

	// Check group permissions
	for _, g := range am.groups {
		// Check if user is a member of this group
		isMember := false
		for _, member := range g.Members {
			if member == username {
				isMember = true
				break
			}
		}

		if !isMember {
			continue
		}

		// Check group's collection-specific permission
		groupPerms := am.groupPermissions[g.Name]
		for _, gp := range groupPerms {
			if gp.Collection == collection && gp.HasPermission(operation) {
				return nil
			}
		}

		// Check group's wildcard permission
		for _, gp := range groupPerms {
			if gp.Collection == "*" && gp.HasPermission(operation) {
				return nil
			}
		}
	}

	return ErrForbidden
}

// UserTenant returns the tenant of a user, or "" for unknown/global users.
func (am *AuthManager) UserTenant(username string) string {
	am.mu.RLock()
	defer am.mu.RUnlock()
	if u, ok := am.users[username]; ok {
		return u.Tenant
	}
	return ""
}

// IsAdmin checks if user has admin privileges
func (am *AuthManager) IsAdmin(username string) bool {
	am.mu.RLock()
	defer am.mu.RUnlock()

	perms := am.permissions[username]
	for _, p := range perms {
		if p.Admin {
			return true
		}
	}

	return false
}

// ---- Group Management ----

// CreateGroup creates a new group
func (am *AuthManager) CreateGroup(name, description string, members []string) (*Group, error) {
	if name == "" {
		return nil, errors.New("group name cannot be empty")
	}

	// Check if group already exists
	am.mu.RLock()
	if _, exists := am.groups[name]; exists {
		am.mu.RUnlock()
		return nil, errors.New("group already exists")
	}
	am.mu.RUnlock()

	group := &Group{
		Name:        name,
		Description: description,
		Members:     members,
		CreatedAt:   time.Now().Unix(),
	}

	// Save to database
	err := am.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketAuthGroups)
		data, err := json.Marshal(group)
		if err != nil {
			return err
		}
		return b.Put([]byte(name), data)
	})

	if err != nil {
		return nil, err
	}

	// Update cache
	am.mu.Lock()
	am.groups[name] = group
	am.mu.Unlock()

	slog.Info("group created", "name", name, "membersCount", len(members))
	return group, nil
}

// GetGroup retrieves a group by name
func (am *AuthManager) GetGroup(name string) (*Group, error) {
	am.mu.RLock()
	defer am.mu.RUnlock()

	group, exists := am.groups[name]
	if !exists {
		return nil, errors.New("group not found")
	}
	return group, nil
}

// ListGroups returns all groups
func (am *AuthManager) ListGroups() []*Group {
	am.mu.RLock()
	defer am.mu.RUnlock()

	groups := make([]*Group, 0, len(am.groups))
	for _, g := range am.groups {
		groups = append(groups, g)
	}
	return groups
}

// UpdateGroup updates group description and members
func (am *AuthManager) UpdateGroup(name, description string, members []string) (*Group, error) {
	am.mu.Lock()
	defer am.mu.Unlock()

	group, exists := am.groups[name]
	if !exists {
		return nil, errors.New("group not found")
	}

	// Update fields
	group.Description = description
	group.Members = members

	// Save to database
	err := am.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketAuthGroups)
		data, err := json.Marshal(group)
		if err != nil {
			return err
		}
		return b.Put([]byte(name), data)
	})

	if err != nil {
		return nil, err
	}

	slog.Info("group updated", "name", name, "membersCount", len(members)) // #nosec G706 -- internal operational log
	return group, nil
}

// DeleteGroup deletes a group and its permissions
func (am *AuthManager) DeleteGroup(name string) error {
	am.mu.Lock()
	defer am.mu.Unlock()

	if _, exists := am.groups[name]; !exists {
		return errors.New("group not found")
	}

	// Delete from database
	err := am.db.Update(func(tx *bolt.Tx) error {
		// Delete group
		b := tx.Bucket(bucketAuthGroups)
		if err := b.Delete([]byte(name)); err != nil {
			return err
		}

		// Delete group permissions
		b = tx.Bucket(bucketAuthGroupPerms)
		cursor := b.Cursor()
		prefix := []byte(name + "|")
		for k, _ := cursor.Seek(prefix); k != nil && string(k[:len(prefix)]) == string(prefix); k, _ = cursor.Next() {
			if err := b.Delete(k); err != nil {
				return err
			}
		}
		return nil
	})

	if err != nil {
		return err
	}

	// Update cache
	delete(am.groups, name)
	delete(am.groupPermissions, name)

	slog.Info("Deleted group", "name", name) // #nosec G706 -- internal operational log
	return nil
}

// SetGroupPermission sets a group permission for a collection
func (am *AuthManager) SetGroupPermission(gp *GroupPermission) error {
	if gp.GroupName == "" || gp.Collection == "" {
		return errors.New("group name and collection are required")
	}

	// Check if group exists
	am.mu.RLock()
	if _, exists := am.groups[gp.GroupName]; !exists {
		am.mu.RUnlock()
		return errors.New("group not found")
	}
	am.mu.RUnlock()

	key := fmt.Sprintf("%s|%s", gp.GroupName, gp.Collection)

	// Save to database
	err := am.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketAuthGroupPerms)
		data, err := json.Marshal(gp)
		if err != nil {
			return err
		}
		return b.Put([]byte(key), data)
	})

	if err != nil {
		return err
	}

	// Update cache - replace existing permission for this collection
	am.mu.Lock()
	perms := am.groupPermissions[gp.GroupName]
	found := false
	for i, p := range perms {
		if p.Collection == gp.Collection {
			perms[i] = gp
			found = true
			break
		}
	}
	if !found {
		am.groupPermissions[gp.GroupName] = append(perms, gp)
	}
	am.mu.Unlock()

	slog.Info("group permission set",
		"group", gp.GroupName, "collection", gp.Collection,
		"read", gp.Read, "write", gp.Write, "admin", gp.Admin)
	return nil
}

// GetGroupPermissions retrieves all permissions for a group
func (am *AuthManager) GetGroupPermissions(groupName string) []*GroupPermission {
	am.mu.RLock()
	defer am.mu.RUnlock()

	return am.groupPermissions[groupName]
}

// ListAllUsers returns all users (for admin panel)
func (am *AuthManager) ListAllUsers() []*User {
	am.mu.RLock()
	defer am.mu.RUnlock()

	users := make([]*User, 0, len(am.users))
	for _, u := range am.users {
		users = append(users, u)
	}
	return users
}

// GetUserGroups returns all groups a user belongs to
func (am *AuthManager) GetUserGroups(username string) []string {
	am.mu.RLock()
	defer am.mu.RUnlock()

	var userGroups []string
	for _, g := range am.groups {
		for _, member := range g.Members {
			if member == username {
				userGroups = append(userGroups, g.Name)
				break
			}
		}
	}
	return userGroups
}
