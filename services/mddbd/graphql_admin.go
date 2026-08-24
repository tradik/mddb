package main

import (
	"context"
	gql "mddb/graphql"
	"time"
)

func (a *GraphQLAdapter) GetStats(ctx context.Context) (*gql.Stats, error) {
	if _, err := a.requireAuthenticated(ctx); err != nil {
		return nil, err
	}
	stats, err := a.mcp.Stats(ctx)
	if err != nil {
		return nil, err
	}
	cols := make([]*gql.CollectionStats, 0, len(stats.Collections))
	for i := range stats.Collections {
		c := stats.Collections[i]
		cols = append(cols, &gql.CollectionStats{
			Name:           c.Name,
			DocumentCount:  c.DocumentCount,
			RevisionCount:  c.RevisionCount,
			MetaIndexCount: c.MetaIndexCount,
		})
	}
	return &gql.Stats{
		DatabasePath:     stats.DatabasePath,
		DatabaseSize:     int(stats.DatabaseSize),
		Mode:             stats.Mode,
		Collections:      cols,
		TotalDocuments:   stats.TotalDocuments,
		TotalRevisions:   stats.TotalRevisions,
		TotalMetaIndices: stats.TotalMetaIndices,
	}, nil
}

// =============================================================================
// Schema
// =============================================================================

func (a *GraphQLAdapter) GetSchema(ctx context.Context, collection string) (*gql.Schema, error) {
	if _, err := a.requireAuthenticated(ctx); err != nil {
		return nil, err
	}
	resp, err := a.mcp.GetSchema(ctx, collection)
	if err != nil {
		return nil, err
	}
	return &gql.Schema{Collection: resp.Collection, Schema: resp.Schema, Enabled: resp.Enabled}, nil
}

func (a *GraphQLAdapter) ListSchemas(ctx context.Context) ([]*gql.Schema, error) {
	if _, err := a.requireAuthenticated(ctx); err != nil {
		return nil, err
	}
	resp, err := a.mcp.ListSchemas(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]*gql.Schema, 0, len(resp.Schemas))
	for i := range resp.Schemas {
		s := resp.Schemas[i]
		out = append(out, &gql.Schema{Collection: s.Collection, Schema: s.Schema, Enabled: true})
	}
	return out, nil
}

func (a *GraphQLAdapter) SetSchema(ctx context.Context, input gql.SetSchemaInput) error {
	if err := a.requireAdmin(ctx); err != nil {
		return err
	}
	return a.mcp.SetSchema(ctx, &MCPSetSchemaRequest{Collection: input.Collection, Schema: input.Schema})
}

func (a *GraphQLAdapter) DeleteSchema(ctx context.Context, collection string) error {
	if err := a.requireAdmin(ctx); err != nil {
		return err
	}
	return a.mcp.DeleteSchema(ctx, collection)
}

func (a *GraphQLAdapter) ValidateDocument(ctx context.Context, collection string, meta []*gql.MetaInput) (*gql.ValidationResult, error) {
	if _, err := a.requireAuthenticated(ctx); err != nil {
		return nil, err
	}
	if err := a.CheckPermission(ctx, collection, int(PermRead)); err != nil {
		return nil, err
	}
	resp, err := a.mcp.ValidateDocument(ctx, &MCPValidateRequest{
		Collection: collection,
		Meta:       gql.MapMetaInputToInternal(meta),
	})
	if err != nil {
		return nil, err
	}
	return &gql.ValidationResult{Valid: resp.Valid, Errors: resp.Errors, Warnings: resp.Warnings}, nil
}

// =============================================================================
// Webhooks
// =============================================================================

func (a *GraphQLAdapter) ListWebhooks(ctx context.Context, collection *string) ([]*gql.Webhook, error) {
	if err := a.requireAdmin(ctx); err != nil {
		return nil, err
	}
	all, err := a.mcp.ListWebhooks(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]*gql.Webhook, 0, len(all))
	for i := range all {
		w := all[i]
		if collection != nil && *collection != "" && w.Collection != *collection {
			continue
		}
		out = append(out, &gql.Webhook{
			ID:         w.ID,
			URL:        w.URL,
			Events:     w.Events,
			Collection: w.Collection,
			CreatedAt:  w.CreatedAt,
		})
	}
	return out, nil
}

func (a *GraphQLAdapter) RegisterWebhook(ctx context.Context, input gql.RegisterWebhookInput) (*gql.Webhook, error) {
	if err := a.requireAdmin(ctx); err != nil {
		return nil, err
	}
	w, err := a.mcp.RegisterWebhook(ctx, &MCPRegisterWebhookRequest{
		URL:        input.URL,
		Events:     input.Events,
		Collection: input.Collection,
	})
	if err != nil {
		return nil, err
	}
	return &gql.Webhook{
		ID:         w.ID,
		URL:        w.URL,
		Events:     w.Events,
		Collection: w.Collection,
		CreatedAt:  w.CreatedAt,
	}, nil
}

func (a *GraphQLAdapter) DeleteWebhook(ctx context.Context, id string) error {
	if err := a.requireAdmin(ctx); err != nil {
		return err
	}
	return a.mcp.DeleteWebhook(ctx, &MCPDeleteWebhookRequest{ID: id})
}

// =============================================================================
// Auth / Users / Groups / Permissions
// =============================================================================

func (a *GraphQLAdapter) Me(ctx context.Context) (*gql.User, error) {
	claims, err := a.requireAuthenticated(ctx)
	if err != nil {
		return nil, err
	}
	if !a.IsAuthEnabled() {
		return &gql.User{Username: claims.Username, Admin: claims.Admin, CreatedAt: 0}, nil
	}
	user, err := a.server.AuthManager.GetUser(claims.Username)
	if err != nil {
		return nil, err
	}
	return &gql.User{Username: user.Username, Admin: claims.Admin, CreatedAt: user.CreatedAt}, nil
}

func (a *GraphQLAdapter) ListUsers(ctx context.Context) ([]*gql.User, error) {
	if err := a.requireAdmin(ctx); err != nil {
		return nil, err
	}
	if !a.IsAuthEnabled() {
		return []*gql.User{}, nil
	}
	users := a.server.AuthManager.ListAllUsers()
	out := make([]*gql.User, 0, len(users))
	for _, u := range users {
		out = append(out, &gql.User{
			Username:  u.Username,
			Admin:     a.server.AuthManager.IsAdmin(u.Username),
			CreatedAt: u.CreatedAt,
		})
	}
	return out, nil
}

func (a *GraphQLAdapter) Register(ctx context.Context, username, password string) (*gql.User, error) {
	if err := a.requireAdmin(ctx); err != nil {
		return nil, err
	}
	if !a.IsAuthEnabled() {
		return nil, ErrAuthNotEnabled
	}
	user, err := a.server.AuthManager.CreateUser(username, password)
	if err != nil {
		return nil, err
	}
	return &gql.User{Username: user.Username, Admin: false, CreatedAt: user.CreatedAt}, nil
}

func (a *GraphQLAdapter) CreateAPIKey(ctx context.Context, input gql.CreateAPIKeyInput) (*gql.APIKey, error) {
	claims, err := a.requireAuthenticated(ctx)
	if err != nil {
		return nil, err
	}
	if !a.IsAuthEnabled() {
		return nil, ErrAuthNotEnabled
	}
	expiresAt := int64(0)
	if input.ExpiresAt != nil {
		expiresAt = *input.ExpiresAt
	}
	key, err := a.server.AuthManager.CreateAPIKey(claims.Username, input.Description, expiresAt)
	if err != nil {
		return nil, err
	}
	out := &gql.APIKey{
		Key:         key,
		Description: input.Description,
		CreatedAt:   time.Now().Unix(),
	}
	if expiresAt > 0 {
		out.ExpiresAt = &expiresAt
	}
	return out, nil
}

func (a *GraphQLAdapter) SetPermission(ctx context.Context, input gql.SetPermissionInput) error {
	if err := a.requireAdmin(ctx); err != nil {
		return err
	}
	if !a.IsAuthEnabled() {
		return ErrAuthNotEnabled
	}
	return a.server.AuthManager.SetPermission(&Permission{
		Username:   input.Username,
		Collection: input.Collection,
		Read:       input.Read,
		Write:      input.Write,
		Admin:      input.Admin,
	})
}

func (a *GraphQLAdapter) UserPermissionsList(ctx context.Context, username string) ([]*gql.UserPermission, error) {
	if err := a.requireAdmin(ctx); err != nil {
		return nil, err
	}
	if !a.IsAuthEnabled() {
		return []*gql.UserPermission{}, nil
	}
	perms := a.server.AuthManager.GetPermissions(username)
	out := make([]*gql.UserPermission, 0, len(perms))
	for _, p := range perms {
		out = append(out, &gql.UserPermission{
			Username:   p.Username,
			Collection: p.Collection,
			Read:       p.Read,
			Write:      p.Write,
			Admin:      p.Admin,
		})
	}
	return out, nil
}

func (a *GraphQLAdapter) ListGroups(ctx context.Context) ([]*gql.Group, error) {
	if err := a.requireAdmin(ctx); err != nil {
		return nil, err
	}
	if !a.IsAuthEnabled() {
		return []*gql.Group{}, nil
	}
	groups := a.server.AuthManager.ListGroups()
	out := make([]*gql.Group, 0, len(groups))
	for _, g := range groups {
		out = append(out, &gql.Group{
			Name:        g.Name,
			Description: g.Description,
			Members:     g.Members,
			CreatedAt:   g.CreatedAt,
		})
	}
	return out, nil
}

func (a *GraphQLAdapter) GroupPermissionsList(ctx context.Context, groupName string) ([]*gql.GroupPermission, error) {
	if err := a.requireAdmin(ctx); err != nil {
		return nil, err
	}
	if !a.IsAuthEnabled() {
		return []*gql.GroupPermission{}, nil
	}
	perms := a.server.AuthManager.GetGroupPermissions(groupName)
	out := make([]*gql.GroupPermission, 0, len(perms))
	for _, p := range perms {
		out = append(out, &gql.GroupPermission{
			GroupName:  p.GroupName,
			Collection: p.Collection,
			Read:       p.Read,
			Write:      p.Write,
			Admin:      p.Admin,
		})
	}
	return out, nil
}

func (a *GraphQLAdapter) CreateGroup(ctx context.Context, input gql.CreateGroupInput) (*gql.Group, error) {
	if err := a.requireAdmin(ctx); err != nil {
		return nil, err
	}
	if !a.IsAuthEnabled() {
		return nil, ErrAuthNotEnabled
	}
	g, err := a.server.AuthManager.CreateGroup(input.Name, input.Description, input.Members)
	if err != nil {
		return nil, err
	}
	return &gql.Group{Name: g.Name, Description: g.Description, Members: g.Members, CreatedAt: g.CreatedAt}, nil
}

func (a *GraphQLAdapter) UpdateGroup(ctx context.Context, name, description string, members []string) (*gql.Group, error) {
	if err := a.requireAdmin(ctx); err != nil {
		return nil, err
	}
	if !a.IsAuthEnabled() {
		return nil, ErrAuthNotEnabled
	}
	g, err := a.server.AuthManager.UpdateGroup(name, description, members)
	if err != nil {
		return nil, err
	}
	return &gql.Group{Name: g.Name, Description: g.Description, Members: g.Members, CreatedAt: g.CreatedAt}, nil
}

func (a *GraphQLAdapter) DeleteGroup(ctx context.Context, name string) error {
	if err := a.requireAdmin(ctx); err != nil {
		return err
	}
	if !a.IsAuthEnabled() {
		return ErrAuthNotEnabled
	}
	return a.server.AuthManager.DeleteGroup(name)
}

func (a *GraphQLAdapter) SetGroupPermission(ctx context.Context, input gql.SetGroupPermissionInput) error {
	if err := a.requireAdmin(ctx); err != nil {
		return err
	}
	if !a.IsAuthEnabled() {
		return ErrAuthNotEnabled
	}
	return a.server.AuthManager.SetGroupPermission(&GroupPermission{
		GroupName:  input.GroupName,
		Collection: input.Collection,
		Read:       input.Read,
		Write:      input.Write,
		Admin:      input.Admin,
	})
}
