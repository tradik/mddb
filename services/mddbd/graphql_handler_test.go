package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	gql "mddb/graphql"
)

// Mock adapter for testing
//
//nolint:unused // Mock is reserved for future GraphQL adapter tests
type mockGraphQLAdapter struct {
	authenticateFn func(username, password string) (gql.UserInfo, error)
	generateJWTFn  func(username string, isAdmin bool) (string, int64, error)
}

//nolint:unused // Mock method reserved for future GraphQL adapter tests
func (m *mockGraphQLAdapter) Authenticate(username, password string) (gql.UserInfo, error) {
	if m.authenticateFn != nil {
		return m.authenticateFn(username, password)
	}
	return gql.UserInfo{}, ErrAuthNotEnabled
}

//nolint:unused // Mock method reserved for future GraphQL adapter tests
func (m *mockGraphQLAdapter) GenerateJWT(username string, isAdmin bool) (string, int64, error) {
	if m.generateJWTFn != nil {
		return m.generateJWTFn(username, isAdmin)
	}
	return "", 0, ErrAuthNotEnabled
}

//nolint:unused // Mock method reserved for future GraphQL adapter tests
func (m *mockGraphQLAdapter) GetClaimsFromContext(ctx context.Context) (gql.Claims, bool) {
	return gql.Claims{}, false
}

//nolint:unused // Mock method reserved for future GraphQL adapter tests
func (m *mockGraphQLAdapter) CheckPermission(ctx context.Context, collection string, perm int) error {
	return nil
}

//nolint:unused // Mock method reserved for future GraphQL adapter tests
func (m *mockGraphQLAdapter) GetDocument(ctx context.Context, collection, key, lang string, env map[string]string) (interface{}, error) {
	return nil, ErrInvalidRequest
}

//nolint:unused // Mock method reserved for future GraphQL adapter tests
func (m *mockGraphQLAdapter) SearchDocuments(ctx context.Context, collection string, filterMeta map[string][]string, sort string, asc bool, limit, offset int) (interface{}, error) {
	return nil, ErrInvalidRequest
}

//nolint:unused // Mock method reserved for future GraphQL adapter tests
func (m *mockGraphQLAdapter) AddDocument(ctx context.Context, collection, key, lang string, meta map[string][]string, contentMd string, ttl int64) (interface{}, bool, error) {
	return nil, false, ErrInvalidRequest
}

//nolint:unused // Mock method reserved for future GraphQL adapter tests
func (m *mockGraphQLAdapter) DeleteDocument(ctx context.Context, collection, key, lang string, _ *string) error {
	return ErrInvalidRequest
}

//nolint:unused // Mock method reserved for future GraphQL adapter tests
func (m *mockGraphQLAdapter) GetStats(ctx context.Context) (interface{}, error) {
	return nil, ErrInvalidRequest
}

//nolint:unused // Mock method reserved for future GraphQL adapter tests
func (m *mockGraphQLAdapter) VectorSearch(ctx context.Context, collection, query string, queryVector []float32, topK int, threshold float64, filterMeta map[string][]string, includeContent bool) (interface{}, error) {
	return nil, ErrVectorSearchNotEnabled
}

func TestGraphQLHandler_Login(t *testing.T) {
	// Create mock server
	s := &Server{
		AuthManager: nil, // Will use mock adapter
	}

	handler := s.newGraphQLHandler()

	// Test login mutation
	query := map[string]interface{}{
		"query": `mutation { login(username: "admin", password: "secret") { token expiresAt } }`,
	}
	body, _ := json.Marshal(query)

	req := httptest.NewRequest("POST", "/graphql", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}

	var response map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	// Should have errors since auth is not enabled
	if _, ok := response["errors"]; !ok {
		t.Error("Expected errors in response when auth not enabled")
	}
}

func TestGraphQLAuthMiddleware_NoAuth(t *testing.T) {
	// Test middleware when auth is not enabled
	s := &Server{
		AuthManager: nil,
	}

	// Create a simple handler
	testHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok")) // nolint:errcheck // Test response writer
	})

	wrappedHandler := s.GraphQLAuthMiddleware(testHandler)

	// Test without token (should pass through when auth disabled)
	req := httptest.NewRequest("POST", "/graphql", nil)
	w := httptest.NewRecorder()

	wrappedHandler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200 when auth disabled, got %d", w.Code)
	}
}

func TestGraphQLPlaygroundHandler(t *testing.T) {
	handler := newGraphQLPlaygroundHandler("/graphql")

	req := httptest.NewRequest("GET", "/playground", nil)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}

	contentType := w.Header().Get("Content-Type")
	if contentType != "text/html; charset=UTF-8" && contentType != "text/html; charset=utf-8" {
		t.Errorf("Expected Content-Type text/html with charset, got %s", contentType)
	}

	body := w.Body.String()
	if !bytes.Contains([]byte(body), []byte("GraphQL Playground")) {
		t.Error("Expected response to contain 'GraphQL Playground'")
	}

	if !bytes.Contains([]byte(body), []byte("/graphql")) {
		t.Error("Expected response to contain endpoint URL '/graphql'")
	}
}
