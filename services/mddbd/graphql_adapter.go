package main

// GraphQLServerAdapter implements the gql.ServerInterface contract by
// delegating to the in-process MCP DirectClient (which has full coverage of
// the REST/gRPC surface) plus AuthManager for user/group/permission ops.
//
// The adapter performs every authentication / authorization check at its
// boundary so resolvers in services/mddbd/graphql/schema.resolvers.go stay
// thin one-liners. Permission semantics:
//
//   - When AuthManager is nil or auth disabled → all operations allowed.
//   - Otherwise: require authenticated claims for every operation except
//     `Login` (handled by Authenticate/GenerateJWT pair).
//   - Per-collection ops additionally call CheckPermission for read or write.
//   - Admin-only ops verify IsAdmin(currentUser).

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	gql "mddb/graphql"
)

// Sentinel errors returned by the GraphQL adapter.
var (
	ErrAuthNotEnabled         = errors.New("authentication not enabled")
	ErrVectorSearchNotEnabled = errors.New("vector search not enabled")
	ErrInvalidRequest         = errors.New("invalid request")
	ErrUnauthenticated        = errors.New("unauthenticated: missing or invalid credentials")
	ErrAdminRequired          = errors.New("forbidden: admin privileges required")
)

// GraphQLAdapter bridges the gql.ServerInterface to the main package's
// Server, AuthManager and DirectClient.
type GraphQLAdapter struct {
	server *Server
	mcp    *DirectClient
}

// NewGraphQLServerAdapter constructs the adapter wired to a running Server.
func NewGraphQLServerAdapter(s *Server) gql.ServerInterface {
	return &GraphQLAdapter{
		server: s,
		mcp:    NewDirectClient(s),
	}
}

// =============================================================================
// Auth primitives (called by directives, resolvers, and adapter internals).
// =============================================================================

func (a *GraphQLAdapter) IsAuthEnabled() bool {
	return a.server.AuthManager != nil && a.server.AuthManager.IsEnabled()
}

func (a *GraphQLAdapter) GetClaimsFromContext(ctx context.Context) (gql.Claims, bool) {
	claims, ok := GetClaimsFromContext(ctx)
	if !ok {
		return gql.Claims{}, false
	}
	return gql.Claims{Username: claims.Username, Admin: claims.Admin}, true
}

func (a *GraphQLAdapter) CheckPermission(ctx context.Context, collection string, perm int) error {
	if !a.IsAuthEnabled() {
		return nil
	}
	return a.server.AuthManager.CheckPermission(ctx, collection, PermissionType(perm))
}

func (a *GraphQLAdapter) Authenticate(username, password string) (gql.UserInfo, error) {
	if !a.IsAuthEnabled() {
		return gql.UserInfo{}, ErrAuthNotEnabled
	}
	user, err := a.server.AuthManager.Authenticate(username, password)
	if err != nil {
		return gql.UserInfo{}, err
	}
	return gql.UserInfo{
		Username:  user.Username,
		Admin:     a.server.AuthManager.IsAdmin(username),
		CreatedAt: user.CreatedAt,
	}, nil
}

func (a *GraphQLAdapter) GenerateJWT(username string, isAdmin bool) (string, int64, error) {
	if !a.IsAuthEnabled() {
		return "", 0, ErrAuthNotEnabled
	}
	expiry := a.server.AuthManager.config.JWTExpiry
	tenant := a.server.AuthManager.UserTenant(username)
	token, err := GenerateTenantJWT(username, tenant, isAdmin, a.server.AuthManager.config.JWTSecret, expiry)
	if err != nil {
		return "", 0, err
	}
	return token, time.Now().Add(expiry).Unix(), nil
}

// requireAuthenticated returns the current claims, or an error if no user is
// authenticated. When auth is disabled it returns a synthetic admin claim so
// every code path can proceed uniformly.
func (a *GraphQLAdapter) requireAuthenticated(ctx context.Context) (*JWTClaims, error) {
	if !a.IsAuthEnabled() {
		return &JWTClaims{Username: "anonymous", Admin: true}, nil
	}
	claims, ok := GetClaimsFromContext(ctx)
	if !ok {
		return nil, ErrUnauthenticated
	}
	return claims, nil
}

// requireAdmin asserts the current user is an admin (or auth is disabled).
func (a *GraphQLAdapter) requireAdmin(ctx context.Context) error {
	claims, err := a.requireAuthenticated(ctx)
	if err != nil {
		return err
	}
	if !a.IsAuthEnabled() {
		return nil
	}
	if !claims.Admin {
		return ErrAdminRequired
	}
	return nil
}

// =============================================================================
// Conversion helpers
// =============================================================================

// mcpDocToGQL converts an MCP document to the GraphQL Document type.
func mcpDocToGQL(d *MCPDocument) *gql.Document {
	if d == nil {
		return nil
	}
	out := &gql.Document{
		ID:        d.ID,
		Key:       d.Key,
		Lang:      d.Lang,
		Meta:      gql.MapMetaToGraphQL(d.Meta),
		ContentMd: d.ContentMD,
		AddedAt:   d.AddedAt.Unix(),
		UpdatedAt: d.UpdatedAt.Unix(),
	}
	if out.ID == "" {
		out.ID = fmt.Sprintf("%s|%s", d.Key, d.Lang)
	}
	return out
}

// mcpVectorStatsToGQL converts MCP vector stats response to the GraphQL type.
func mcpVectorStatsToGQL(s *MCPVectorStatsResponse) *gql.VectorStats {
	if s == nil {
		return &gql.VectorStats{Enabled: false, Collections: []*gql.VectorCollectionStats{}}
	}
	cols := make([]*gql.VectorCollectionStats, 0, len(s.Collections))
	for name, c := range s.Collections {
		cols = append(cols, &gql.VectorCollectionStats{
			Collection:        name,
			TotalDocuments:    c.TotalDocuments,
			EmbeddedDocuments: c.EmbeddedDocuments,
		})
	}
	// Sorted, because these come from map iteration: the same query returned
	// its collections in a different order every time, which makes a UI list
	// jump on refresh and any diff of two responses meaningless (TEST-002).
	sort.Slice(cols, func(i, j int) bool { return cols[i].Collection < cols[j].Collection })
	provider := s.Provider
	model := s.Model
	dims := s.Dimensions
	return &gql.VectorStats{
		Enabled:     s.Enabled,
		Provider:    &provider,
		Model:       &model,
		Dimensions:  &dims,
		Collections: cols,
		IndexReady:  s.Enabled,
	}
}

// derefString returns *p or "" if nil.
func derefString(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// derefBool returns *p or fallback if nil.
func derefBool(p *bool, fallback bool) bool {
	if p == nil {
		return fallback
	}
	return *p
}

// derefInt returns *p or fallback if nil.
func derefInt(p *int, fallback int) int {
	if p == nil {
		return fallback
	}
	return *p
}

// derefFloat64 returns *p or fallback if nil.
func derefFloat64(p *float64, fallback float64) float64 {
	if p == nil {
		return fallback
	}
	return *p
}
