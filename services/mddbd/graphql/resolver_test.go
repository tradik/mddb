package graphql

import (
	"context"
	"errors"
	"testing"
	"time"
)

// stubServer is a no-op implementation of ServerInterface used by resolver
// unit tests. Each test overrides only the function fields it needs; all
// other methods return errors so an unexpected call surfaces immediately.
type stubServer struct {
	authenticate    func(username, password string) (UserInfo, error)
	generateJWT     func(username string, isAdmin bool) (string, int64, error)
	getClaims       func(ctx context.Context) (Claims, bool)
	checkPermission func(ctx context.Context, collection string, perm int) error
	deleteDocument  func(ctx context.Context, collection, key, lang string) error
}

func (s *stubServer) Authenticate(username, password string) (UserInfo, error) {
	if s.authenticate != nil {
		return s.authenticate(username, password)
	}
	return UserInfo{}, errors.New("not implemented")
}
func (s *stubServer) GenerateJWT(username string, isAdmin bool) (string, int64, error) {
	if s.generateJWT != nil {
		return s.generateJWT(username, isAdmin)
	}
	return "", 0, errors.New("not implemented")
}
func (s *stubServer) GetClaimsFromContext(ctx context.Context) (Claims, bool) {
	if s.getClaims != nil {
		return s.getClaims(ctx)
	}
	return Claims{}, false
}
func (s *stubServer) CheckPermission(ctx context.Context, collection string, perm int) error {
	if s.checkPermission != nil {
		return s.checkPermission(ctx, collection, perm)
	}
	return nil
}
func (s *stubServer) IsAuthEnabled() bool { return false }

func (s *stubServer) GetDocument(_ context.Context, _, _, _ string, _ map[string]string) (*Document, error) {
	return nil, errors.New("not implemented")
}
func (s *stubServer) SearchDocuments(_ context.Context, _ SearchInput) (*DocumentConnection, error) {
	return nil, errors.New("not implemented")
}
func (s *stubServer) AddDocument(_ context.Context, _ AddDocumentInput) (*Document, error) {
	return nil, errors.New("not implemented")
}
func (s *stubServer) UpdateDocument(_ context.Context, _ UpdateDocumentInput) (*Document, error) {
	return nil, errors.New("not implemented")
}
func (s *stubServer) DeleteDocument(ctx context.Context, collection, key, lang string) error {
	if s.deleteDocument != nil {
		return s.deleteDocument(ctx, collection, key, lang)
	}
	return errors.New("not implemented")
}
func (s *stubServer) DeleteCollection(_ context.Context, _ string) (int, error) {
	return 0, errors.New("not implemented")
}
func (s *stubServer) AddBatch(_ context.Context, _ string, _ []*AddBatchDocumentInput) (*BatchAddResult, error) {
	return nil, errors.New("not implemented")
}
func (s *stubServer) IngestDocuments(_ context.Context, _ string, _ []*IngestDocumentInput, _ *IngestOptionsInput) (*IngestResult, error) {
	return nil, errors.New("not implemented")
}
func (s *stubServer) SetTTL(_ context.Context, _, _, _ string, _ int) (*Document, error) {
	return nil, errors.New("not implemented")
}
func (s *stubServer) ImportURL(_ context.Context, _, _ string, _ *string, _ string, _ []*MetaInput, _ *int) (*Document, error) {
	return nil, errors.New("not implemented")
}
func (s *stubServer) VectorSearch(_ context.Context, _ VectorSearchInput) (*VectorSearchResponse, error) {
	return nil, errors.New("not implemented")
}
func (s *stubServer) VectorStats(_ context.Context) (*VectorStats, error) {
	return nil, errors.New("not implemented")
}
func (s *stubServer) VectorReindex(_ context.Context, _ string, _ *bool) (*VectorStats, error) {
	return nil, errors.New("not implemented")
}
func (s *stubServer) FullTextSearch(_ context.Context, _ FTSInput) (*FTSResponse, error) {
	return nil, errors.New("not implemented")
}
func (s *stubServer) CodeGraph(_ context.Context, _ CodeGraphInput) (*CodeGraph, error) {
	return nil, errors.New("not implemented")
}
func (s *stubServer) GetStats(_ context.Context) (*Stats, error) {
	return nil, errors.New("not implemented")
}
func (s *stubServer) GetSchema(_ context.Context, _ string) (*Schema, error) {
	return nil, errors.New("not implemented")
}
func (s *stubServer) ListSchemas(_ context.Context) ([]*Schema, error) {
	return nil, errors.New("not implemented")
}
func (s *stubServer) SetSchema(_ context.Context, _ SetSchemaInput) error {
	return errors.New("not implemented")
}
func (s *stubServer) DeleteSchema(_ context.Context, _ string) error {
	return errors.New("not implemented")
}
func (s *stubServer) ValidateDocument(_ context.Context, _ string, _ []*MetaInput) (*ValidationResult, error) {
	return nil, errors.New("not implemented")
}
func (s *stubServer) ListWebhooks(_ context.Context, _ *string) ([]*Webhook, error) {
	return nil, errors.New("not implemented")
}
func (s *stubServer) RegisterWebhook(_ context.Context, _ RegisterWebhookInput) (*Webhook, error) {
	return nil, errors.New("not implemented")
}
func (s *stubServer) DeleteWebhook(_ context.Context, _ string) error {
	return errors.New("not implemented")
}
func (s *stubServer) Me(_ context.Context) (*User, error) {
	return nil, errors.New("not implemented")
}
func (s *stubServer) ListUsers(_ context.Context) ([]*User, error) {
	return nil, errors.New("not implemented")
}
func (s *stubServer) Register(_ context.Context, _, _ string) (*User, error) {
	return nil, errors.New("not implemented")
}
func (s *stubServer) CreateAPIKey(_ context.Context, _ CreateAPIKeyInput) (*APIKey, error) {
	return nil, errors.New("not implemented")
}
func (s *stubServer) SetPermission(_ context.Context, _ SetPermissionInput) error {
	return errors.New("not implemented")
}
func (s *stubServer) UserPermissionsList(_ context.Context, _ string) ([]*UserPermission, error) {
	return nil, errors.New("not implemented")
}
func (s *stubServer) ListGroups(_ context.Context) ([]*Group, error) {
	return nil, errors.New("not implemented")
}
func (s *stubServer) GroupPermissionsList(_ context.Context, _ string) ([]*GroupPermission, error) {
	return nil, errors.New("not implemented")
}
func (s *stubServer) CreateGroup(_ context.Context, _ CreateGroupInput) (*Group, error) {
	return nil, errors.New("not implemented")
}
func (s *stubServer) UpdateGroup(_ context.Context, _, _ string, _ []string) (*Group, error) {
	return nil, errors.New("not implemented")
}
func (s *stubServer) DeleteGroup(_ context.Context, _ string) error {
	return errors.New("not implemented")
}
func (s *stubServer) SetGroupPermission(_ context.Context, _ SetGroupPermissionInput) error {
	return errors.New("not implemented")
}

// =============================================================================
// Tests
// =============================================================================

func TestLogin_Success(t *testing.T) {
	stub := &stubServer{
		authenticate: func(username, password string) (UserInfo, error) {
			if username == "admin" && password == "secret" {
				return UserInfo{Username: "admin", Admin: true, CreatedAt: time.Now().Unix()}, nil
			}
			return UserInfo{}, errors.New("invalid credentials")
		},
		generateJWT: func(username string, isAdmin bool) (string, int64, error) {
			return "mock-jwt-token", time.Now().Add(24 * time.Hour).Unix(), nil
		},
	}
	mut := &mutationResolver{&Resolver{server: stub}}

	result, err := mut.Login(context.Background(), "admin", "secret")
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}
	if result.Token != "mock-jwt-token" {
		t.Errorf("Expected token 'mock-jwt-token', got %s", result.Token)
	}
	if result.ExpiresAt <= time.Now().Unix() {
		t.Errorf("Expected future expiration time, got %d", result.ExpiresAt)
	}
}

func TestLogin_InvalidCredentials(t *testing.T) {
	stub := &stubServer{
		authenticate: func(username, password string) (UserInfo, error) {
			return UserInfo{}, errors.New("invalid credentials")
		},
	}
	mut := &mutationResolver{&Resolver{server: stub}}

	_, err := mut.Login(context.Background(), "admin", "wrongpass")
	if err == nil {
		t.Fatal("Expected error for invalid credentials")
	}
	if err.Error() != "invalid credentials" {
		t.Errorf("Expected 'invalid credentials', got %q", err.Error())
	}
}

func TestDeleteDocument_Success(t *testing.T) {
	stub := &stubServer{
		deleteDocument: func(_ context.Context, collection, key, lang string) error {
			if collection == "blog" && key == "post1" && lang == "en" {
				return nil
			}
			return errors.New("not found")
		},
	}
	mut := &mutationResolver{&Resolver{server: stub}}

	ok, err := mut.DeleteDocument(context.Background(), "blog", "post1", "en")
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}
	if !ok {
		t.Error("Expected true, got false")
	}
}

func TestDeleteDocument_PropagatesError(t *testing.T) {
	stub := &stubServer{
		deleteDocument: func(_ context.Context, _, _, _ string) error {
			return errors.New("permission denied")
		},
	}
	mut := &mutationResolver{&Resolver{server: stub}}

	ok, err := mut.DeleteDocument(context.Background(), "blog", "post1", "en")
	if err == nil {
		t.Fatal("Expected error")
	}
	if ok {
		t.Error("Expected false on error")
	}
}

func TestMapMetaInputToInternal(t *testing.T) {
	tests := []struct {
		name     string
		input    []*MetaInput
		expected map[string][]string
	}{
		{
			name:     "single meta",
			input:    []*MetaInput{{Key: "author", Values: []string{"John"}}},
			expected: map[string][]string{"author": {"John"}},
		},
		{
			name: "multiple meta",
			input: []*MetaInput{
				{Key: "author", Values: []string{"John"}},
				{Key: "tags", Values: []string{"tutorial", "graphql"}},
			},
			expected: map[string][]string{
				"author": {"John"},
				"tags":   {"tutorial", "graphql"},
			},
		},
		{
			name:     "empty input",
			input:    []*MetaInput{},
			expected: map[string][]string{},
		},
		{
			name: "nil entry",
			input: []*MetaInput{
				{Key: "author", Values: []string{"John"}},
				nil,
			},
			expected: map[string][]string{"author": {"John"}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := MapMetaInputToInternal(tt.input)
			if len(result) != len(tt.expected) {
				t.Errorf("Expected %d keys, got %d", len(tt.expected), len(result))
			}
			for key, expectedVals := range tt.expected {
				actualVals, ok := result[key]
				if !ok {
					t.Errorf("Missing key %s", key)
					continue
				}
				if len(actualVals) != len(expectedVals) {
					t.Errorf("For %s: expected %d values, got %d", key, len(expectedVals), len(actualVals))
					continue
				}
				for i, v := range expectedVals {
					if actualVals[i] != v {
						t.Errorf("For %s[%d]: expected %s, got %s", key, i, v, actualVals[i])
					}
				}
			}
		})
	}
}
