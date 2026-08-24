package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
)

// Authentication errors
var (
	ErrInvalidCredentials = errors.New("invalid credentials")
	ErrUserNotFound       = errors.New("user not found")
	ErrUserExists         = errors.New("user already exists")
	ErrAPIKeyNotFound     = errors.New("api key not found")
	ErrAPIKeyExpired      = errors.New("api key expired")
	ErrForbidden          = errors.New("forbidden")
	ErrUnauthorized       = errors.New("unauthorized")
)

// PermissionType represents a level of access control.
type PermissionType int

// Permission level constants.
const (
	PermRead PermissionType = iota
	PermWrite
	PermAdmin
)

// Auth context key
type contextKey string

const authContextKey contextKey = "auth_claims"

// User represents a user account
type User struct {
	Username     string `json:"username"`
	PasswordHash string `json:"passwordHash"`
	CreatedAt    int64  `json:"createdAt"`
	Disabled     bool   `json:"disabled"`
	Tenant       string `json:"tenant,omitempty"` // "" = global user
}

// APIKey represents an API key for authentication
type APIKey struct {
	KeyHash     string `json:"keyHash"`  // SHA256 of the API key
	Username    string `json:"username"` // owner
	CreatedAt   int64  `json:"createdAt"`
	ExpiresAt   int64  `json:"expiresAt"`   // 0 = never
	Description string `json:"description"` // optional label
}

// Permission represents a user permission for a collection
type Permission struct {
	Username   string `json:"username"`
	Collection string `json:"collection"` // "*" = all collections
	Read       bool   `json:"read"`
	Write      bool   `json:"write"`
	Admin      bool   `json:"admin"` // can manage users/permissions
}

// Group represents a user group
type Group struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Members     []string `json:"members"` // usernames
	CreatedAt   int64    `json:"createdAt"`
}

// GroupPermission represents a group permission for a collection
type GroupPermission struct {
	GroupName  string `json:"groupName"`
	Collection string `json:"collection"` // "*" = all collections
	Read       bool   `json:"read"`
	Write      bool   `json:"write"`
	Admin      bool   `json:"admin"` // can manage users/permissions
}

// JWTClaims represents JWT token claims
type JWTClaims struct {
	Username string `json:"username"`
	Admin    bool   `json:"admin"`            // cached admin flag; never true for tenant users
	Tenant   string `json:"tenant,omitempty"` // namespace the caller is confined to
	jwt.RegisteredClaims
}

// AuthConfig holds authentication configuration
type AuthConfig struct {
	JWTSecret     string
	JWTExpiry     time.Duration
	AdminUsername string
	AdminPassword string
}

// ---- Helper functions ----

// HashPassword hashes a password using bcrypt
func HashPassword(password string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	return string(hash), nil
}

// VerifyPassword verifies a password against a hash
func VerifyPassword(password, hash string) error {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password))
}

// APIKeyPrefix is what every generated API key starts with.
//
// It exists as a constant because two places need to agree on it: the generator
// below, and the HTTP middleware, which uses it to tell an API key sent as
// `Authorization: Bearer` apart from a JWT sent the same way (#212). A prefix
// makes that decision exact — no validating the credential twice to see which
// one it is, and no guessing from its shape.
// #nosec G101 -- a namespace, not a secret. Every generated key starts with
// these ten characters and the remaining 96 are the credential; publishing the
// prefix is how a reader tells one of our keys from a JWT, which is exactly
// what the middleware does with it.
const APIKeyPrefix = "mddb_live_"

// GenerateAPIKey generates a new API key with format: mddb_live_{48 hex chars}
func GenerateAPIKey() (string, error) {
	b := make([]byte, 24) // 24 bytes = 48 hex chars
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return APIKeyPrefix + hex.EncodeToString(b), nil
}

// HashAPIKey hashes an API key using SHA256
func HashAPIKey(key string) string {
	hash := sha256.Sum256([]byte(key))
	return hex.EncodeToString(hash[:])
}

// HasPermission checks if permission has the required operation
func (p *Permission) HasPermission(op PermissionType) bool {
	switch op {
	case PermRead:
		return p.Read
	case PermWrite:
		return p.Write
	case PermAdmin:
		return p.Admin
	default:
		return false
	}
}

// HasPermission checks if group permission has the required operation
func (gp *GroupPermission) HasPermission(op PermissionType) bool {
	switch op {
	case PermRead:
		return gp.Read
	case PermWrite:
		return gp.Write
	case PermAdmin:
		return gp.Admin
	default:
		return false
	}
}

// IsExpired checks if API key has expired
func (k *APIKey) IsExpired() bool {
	if k.ExpiresAt == 0 {
		return false // never expires
	}
	return time.Now().Unix() > k.ExpiresAt
}

// GetClaimsFromContext retrieves JWT claims from context
func GetClaimsFromContext(ctx context.Context) (*JWTClaims, bool) {
	claims, ok := ctx.Value(authContextKey).(*JWTClaims)
	return claims, ok
}

// ---- JWT generation and validation ----

// GenerateJWT generates a JWT token for a global (non-tenant) user.
//
// No production caller: every issuing path knows its tenant and calls
// GenerateTenantJWT directly, passing "" for global users. This alias stays
// because it is the only way to mint a token with Admin set — the tenant form
// strips it — which is what the auth tests need to exercise admin-only paths.
func GenerateJWT(username string, isAdmin bool, secret string, expiry time.Duration) (string, error) {
	return GenerateTenantJWT(username, "", isAdmin, secret, expiry)
}

// GenerateTenantJWT generates a JWT token carrying a tenant namespace.
// Tenant users are always issued Admin=false: the global admin flag would
// bypass CheckPermission and every admin-only endpoint, leaking cross-tenant
// control, so it is stripped here regardless of what the caller passed.
func GenerateTenantJWT(username, tenant string, isAdmin bool, secret string, expiry time.Duration) (string, error) {
	if tenant != "" {
		isAdmin = false
	}
	now := time.Now()
	claims := &JWTClaims{
		Username: username,
		Admin:    isAdmin,
		Tenant:   tenant,
		RegisteredClaims: jwt.RegisteredClaims{
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(expiry)),
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(secret))
}

// ValidateJWT validates a JWT token and returns the claims
func ValidateJWT(tokenString, secret string) (*JWTClaims, error) {
	token, err := jwt.ParseWithClaims(tokenString, &JWTClaims{}, func(token *jwt.Token) (interface{}, error) {
		// Validate signing method
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return []byte(secret), nil
	})

	if err != nil {
		return nil, err
	}

	if claims, ok := token.Claims.(*JWTClaims); ok && token.Valid {
		return claims, nil
	}

	return nil, errors.New("invalid token")
}
