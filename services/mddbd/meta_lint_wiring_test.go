package main

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	gql "mddb/graphql"
	proto "mddb/proto"
)

// DOC-012 / issue #187: the warning has to reach the caller through every
// validation surface, and must never turn a valid document invalid.

const stringifiedFAQ = `[map[answer:Yes, it is free. question:Is it free?]]`

type validateBody struct {
	Valid    bool     `json:"valid"`
	Errors   []string `json:"errors"`
	Warnings []string `json:"warnings"`
}

func postValidate(t *testing.T, s *Server, body string) validateBody {
	t.Helper()
	req := httptest.NewRequest("POST", "/v1/validate", strings.NewReader(body))
	w := httptest.NewRecorder()
	s.handleValidate(w, req)

	var out validateBody
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("response is not a validation result: %v (%s)", err, w.Body.String())
	}
	return out
}

func TestValidateRESTWarnsOnStringifiedStructure(t *testing.T) {
	s, cleanup := schemaExtraTestServer(t)
	defer cleanup()

	got := postValidate(t, s, `{"collection":"site","meta":{"faq":["`+stringifiedFAQ+`"]}}`)

	if len(got.Warnings) != 1 {
		t.Fatalf("warnings = %v, want one", got.Warnings)
	}
	if !strings.Contains(got.Warnings[0], "meta.faq") {
		t.Errorf("the warning does not name the key: %s", got.Warnings[0])
	}
	// A stringified structure is a valid string; rejecting it would break
	// callers legitimately storing text that looks like this.
	if !got.Valid {
		t.Error("a warning made the document invalid")
	}
}

func TestValidateRESTStaysQuietOnOrdinaryMeta(t *testing.T) {
	s, cleanup := schemaExtraTestServer(t)
	defer cleanup()

	got := postValidate(t, s, `{"collection":"site","meta":{"tag":["go","rust"],"title":["About map[string]int"]}}`)
	if len(got.Warnings) != 0 {
		t.Errorf("ordinary metadata warned: %v", got.Warnings)
	}
}

// A schema failure and a lint warning are independent: a caller fixing the
// error should still see the warning.
func TestValidateRESTReportsWarningsAlongsideErrors(t *testing.T) {
	s, cleanup := schemaExtraTestServer(t)
	defer cleanup()
	_ = s.SchemaManager.Set("site", `{"required":["title"]}`)

	got := postValidate(t, s, `{"collection":"site","meta":{"faq":["`+stringifiedFAQ+`"]}}`)
	if got.Valid {
		t.Error("a document missing a required field passed")
	}
	if len(got.Warnings) != 1 {
		t.Errorf("the warning was dropped when validation failed: %v", got.Warnings)
	}
}

func TestValidateMCPWarnsOnStringifiedStructure(t *testing.T) {
	s, cleanup := schemaExtraTestServer(t)
	defer cleanup()

	resp, err := NewDirectClient(s).ValidateDocument(context.Background(), &MCPValidateRequest{
		Collection: "site",
		Meta:       map[string][]string{"faq": {stringifiedFAQ}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Warnings) != 1 {
		t.Errorf("MCP dropped the warning: %v", resp.Warnings)
	}
	if !resp.Valid {
		t.Error("a warning made the document invalid")
	}
}

// The lint is independent of the schema, so it must run where none is
// configured — which is exactly the case an unstructured import lands in.
func TestValidateMCPWarnsWithoutASchemaManager(t *testing.T) {
	s, cleanup := schemaExtraTestServer(t)
	defer cleanup()
	s.SchemaManager = nil

	resp, err := NewDirectClient(s).ValidateDocument(context.Background(), &MCPValidateRequest{
		Collection: "site",
		Meta:       map[string][]string{"faq": {stringifiedFAQ}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Warnings) != 1 {
		t.Errorf("no schema means no lint: %v", resp.Warnings)
	}
}

func TestValidateGRPCWarnsOnStringifiedStructure(t *testing.T) {
	s, cleanup := schemaExtraTestServer(t)
	defer cleanup()

	g := &GRPCServer{server: s}
	resp, err := g.ValidateDocument(context.Background(), &proto.ValidateDocumentRequest{
		Collection: "site",
		Meta:       map[string]*proto.MetaValues{"faq": {Values: []string{stringifiedFAQ}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Warnings) != 1 {
		t.Errorf("gRPC dropped the warning: %v", resp.Warnings)
	}
	if !resp.Valid {
		t.Error("a warning made the document invalid")
	}
}

func TestValidateGraphQLWarnsOnStringifiedStructure(t *testing.T) {
	s, cleanup := schemaExtraTestServer(t)
	defer cleanup()

	adapter := &GraphQLAdapter{server: s, mcp: NewDirectClient(s)}
	res, err := adapter.ValidateDocument(context.Background(), "site", []*gql.MetaInput{
		{Key: "faq", Values: []string{stringifiedFAQ}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Warnings) != 1 {
		t.Errorf("GraphQL dropped the warning: %v", res.Warnings)
	}
	if !res.Valid {
		t.Error("a warning made the document invalid")
	}
}

// All four surfaces must say the same thing about the same document.
func TestValidateSurfacesAgreeOnWarnings(t *testing.T) {
	s, cleanup := schemaExtraTestServer(t)
	defer cleanup()

	meta := map[string][]string{"faq": {stringifiedFAQ}}

	rest := postValidate(t, s, `{"collection":"site","meta":{"faq":["`+stringifiedFAQ+`"]}}`)

	mcp, err := NewDirectClient(s).ValidateDocument(context.Background(), &MCPValidateRequest{
		Collection: "site", Meta: meta,
	})
	if err != nil {
		t.Fatal(err)
	}

	g := &GRPCServer{server: s}
	grpcRes, err := g.ValidateDocument(context.Background(), &proto.ValidateDocumentRequest{
		Collection: "site",
		Meta:       map[string]*proto.MetaValues{"faq": {Values: []string{stringifiedFAQ}}},
	})
	if err != nil {
		t.Fatal(err)
	}

	adapter := &GraphQLAdapter{server: s, mcp: NewDirectClient(s)}
	gqlRes, err := adapter.ValidateDocument(context.Background(), "site", []*gql.MetaInput{
		{Key: "faq", Values: []string{stringifiedFAQ}},
	})
	if err != nil {
		t.Fatal(err)
	}

	all := [][]string{rest.Warnings, mcp.Warnings, grpcRes.Warnings, gqlRes.Warnings}
	for i, w := range all {
		if len(w) != 1 || w[0] != all[0][0] {
			t.Errorf("surface %d disagrees: %v vs %v", i, w, all[0])
		}
	}
}
