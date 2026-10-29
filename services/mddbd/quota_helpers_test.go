package main

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	json "mddb/internal/jsonx"
	"mddb/internal/storage"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Helpers shared by the GO-043 quota tests.

// quotaTestServer is a server with every surface wired, plus a collection
// holding two documents.
func quotaTestServer(t *testing.T) (*GRPCServer, *Server) {
	t.Helper()
	gs, s, cleanup := newTestGRPCServerFull(t)
	t.Cleanup(cleanup)
	addTestDoc(t, s, "q", "a", "en", "first document", nil)
	addTestDoc(t, s, "q", "b", "en", "second document", nil)
	return gs, s
}

// setTestQuota stores a quota for a collection.
func setTestQuota(t *testing.T, s *Server, collection string, q CollectionQuota) {
	t.Helper()
	if err := s.CollectionManager.Set(collection, &CollectionConfig{Quota: &q}); err != nil {
		t.Fatal(err)
	}
}

// quotaDocCount counts a collection's stored documents independently of the
// quota code.
func quotaDocCount(t *testing.T, s *Server, collection string) int {
	t.Helper()
	n := 0
	if err := s.DBScanC(collection, []byte("docs"), storage.DocKey(collection, ""), func(_, _ []byte) bool {
		n++
		return true
	}); err != nil {
		t.Fatal(err)
	}
	return n
}

// wantHTTPQuota requires a REST refusal with the given status and resource.
func wantHTTPQuota(t *testing.T, rec *httptest.ResponseRecorder, code int, resource string) {
	t.Helper()
	if rec.Code != code {
		t.Fatalf("status = %d, want %d: %s", rec.Code, code, rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("body is not JSON: %s", rec.Body.String())
	}
	if body["code"] != "QUOTA_EXCEEDED" || body["resource"] != resource {
		t.Fatalf("body = %v, want resource %s", body, resource)
	}
}

// wantGRPCQuota requires RESOURCE_EXHAUSTED.
func wantGRPCQuota(t *testing.T, err error) {
	t.Helper()
	if status.Code(err) != codes.ResourceExhausted {
		t.Fatalf("code = %v (%v), want ResourceExhausted", status.Code(err), err)
	}
}

// mcpCall runs one tool through the MCP handler, as a client's tools/call would.
func mcpCall(s *Server, tool string, args map[string]any) map[string]any {
	h := NewMCPHandlerWithConfig(NewDirectClient(s), nil, MCPServerInfo{}, "", ModeRW, "")
	return h.handleToolsCall(context.Background(), map[string]any{
		"params": map[string]any{"name": tool, "arguments": args},
	})
}

// wantMCPQuota requires a tool error carrying the quota as structured content.
func wantMCPQuota(t *testing.T, res map[string]any, httpStatus int) {
	t.Helper()
	detail, _ := res["structuredContent"].(map[string]any)
	if res["isError"] != true || detail == nil || detail["status"] != httpStatus {
		t.Fatalf("MCP result = %v, want a quota error with status %d", res, httpStatus)
	}
}

// graphQLPost runs a query through the production GraphQL handler.
func graphQLPost(t *testing.T, s *Server, query string) map[string]any {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"query": query})
	req := httptest.NewRequest(http.MethodPost, "/graphql", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.newGraphQLHandler().ServeHTTP(rec, req)
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("GraphQL answer is not JSON: %s", rec.Body.String())
	}
	return out
}

// wantGraphQLQuota requires an error whose extensions carry the quota.
func wantGraphQLQuota(t *testing.T, out map[string]any, httpStatus int) {
	t.Helper()
	errs, _ := out["errors"].([]any)
	if len(errs) == 0 {
		t.Fatalf("no GraphQL error: %v", out)
	}
	ext, _ := errs[0].(map[string]any)["extensions"].(map[string]any)
	if ext["code"] != "QUOTA_EXCEEDED" || ext["status"] != float64(httpStatus) {
		t.Fatalf("extensions = %v", ext)
	}
}
