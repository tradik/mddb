package graphql

import (
	"context"
)

//go:generate go run github.com/99designs/gqlgen generate

// This file will not be regenerated automatically.
//
// It serves as dependency injection for your app, add any dependencies you require here.

// ServerInterface defines the contract that the main package's adapter must
// implement so the graphql package can serve every operation declared in
// schema.graphql without importing the main package.
//
// Methods return *graphql package types directly so resolvers stay one-liners.
// All authentication / authorization is enforced inside the adapter or
// resolver bodies (the @auth and @hasRole directives are no-ops in this
// implementation — see directives.go).
type ServerInterface interface {
	// --- Auth primitives ---
	GetClaimsFromContext(ctx context.Context) (Claims, bool)
	CheckPermission(ctx context.Context, collection string, perm int) error
	Authenticate(username, password string) (UserInfo, error)
	GenerateJWT(username string, isAdmin bool) (string, int64, error)
	IsAuthEnabled() bool

	// --- Documents (Query) ---
	GetDocument(ctx context.Context, collection, key, lang string, env map[string]string) (*Document, error)
	SearchDocuments(ctx context.Context, input SearchInput) (*DocumentConnection, error)

	// --- Documents (Mutation) ---
	AddDocument(ctx context.Context, input AddDocumentInput) (*Document, error)
	UpdateDocument(ctx context.Context, input UpdateDocumentInput) (*Document, error)
	DeleteDocument(ctx context.Context, collection, key, lang string) error
	DeleteCollection(ctx context.Context, collection string) (int, error)
	AddBatch(ctx context.Context, collection string, docs []*AddBatchDocumentInput) (*BatchAddResult, error)
	IngestDocuments(ctx context.Context, collection string, docs []*IngestDocumentInput, opts *IngestOptionsInput) (*IngestResult, error)
	SetTTL(ctx context.Context, collection, key, lang string, ttl int) (*Document, error)
	ImportURL(ctx context.Context, collection, url string, key *string, lang string, meta []*MetaInput, ttl *int) (*Document, error)

	// --- Vector / FTS / Stats ---
	VectorSearch(ctx context.Context, input VectorSearchInput) (*VectorSearchResponse, error)
	VectorStats(ctx context.Context) (*VectorStats, error)
	VectorReindex(ctx context.Context, collection string, force *bool) (*VectorStats, error)
	FullTextSearch(ctx context.Context, input FTSInput) (*FTSResponse, error)
	CodeGraph(ctx context.Context, input CodeGraphInput) (*CodeGraph, error)
	GetStats(ctx context.Context) (*Stats, error)

	// --- Schema ---
	GetSchema(ctx context.Context, collection string) (*Schema, error)
	ListSchemas(ctx context.Context) ([]*Schema, error)
	SetSchema(ctx context.Context, input SetSchemaInput) error
	DeleteSchema(ctx context.Context, collection string) error
	ValidateDocument(ctx context.Context, collection string, meta []*MetaInput) (*ValidationResult, error)

	// --- Webhooks ---
	ListWebhooks(ctx context.Context, collection *string) ([]*Webhook, error)
	RegisterWebhook(ctx context.Context, input RegisterWebhookInput) (*Webhook, error)
	DeleteWebhook(ctx context.Context, id string) error

	// --- Auth / Users / Groups / Permissions ---
	Me(ctx context.Context) (*User, error)
	ListUsers(ctx context.Context) ([]*User, error)
	Register(ctx context.Context, username, password string) (*User, error)
	CreateAPIKey(ctx context.Context, input CreateAPIKeyInput) (*APIKey, error)
	SetPermission(ctx context.Context, input SetPermissionInput) error
	UserPermissionsList(ctx context.Context, username string) ([]*UserPermission, error)
	ListGroups(ctx context.Context) ([]*Group, error)
	GroupPermissionsList(ctx context.Context, groupName string) ([]*GroupPermission, error)
	CreateGroup(ctx context.Context, input CreateGroupInput) (*Group, error)
	UpdateGroup(ctx context.Context, name, description string, members []string) (*Group, error)
	DeleteGroup(ctx context.Context, name string) error
	SetGroupPermission(ctx context.Context, input SetGroupPermissionInput) error
}

// Claims represents JWT token claims passed across the package boundary.
type Claims struct {
	Username string
	Admin    bool
}

// UserInfo represents user information returned by Authenticate.
type UserInfo struct {
	Username  string
	Admin     bool
	CreatedAt int64
}

// Resolver is the root GraphQL resolver that delegates everything to the
// injected ServerInterface implementation.
type Resolver struct {
	server ServerInterface
}

// NewResolver creates a new root GraphQL resolver.
func NewResolver(server ServerInterface) *Resolver {
	return &Resolver{server: server}
}
