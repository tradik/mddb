package main

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	json "mddb/internal/jsonx"
	"mddb/proto"
)

// MCP-005: the chat MCP secret tools, and one secret store behind REST,
// gRPC and MCP.

func TestChatMcpSecretMCPLifecycle(t *testing.T) {
	s := chatServer(t)
	ts, admin := chatMCP(s), asClaims(root)
	out, err := ts.mcpCallTool(admin, "chat_mcp_secret_put", map[string]interface{}{"name": "wiki", "value": "tok-mcp", "header": "X-Api-Key"})
	if err != nil || out != `{"created":true,"header":"X-Api-Key","name":"wiki","set":true}` {
		t.Fatalf("put: %s %v", out, err)
	}
	out, err = ts.mcpCallTool(admin, "chat_mcp_secret_list", nil)
	if err != nil || !strings.Contains(out, `"name": "wiki"`) || !strings.Contains(out, `"set": true`) || strings.Contains(out, "tok-mcp") {
		t.Fatalf("list: %s %v", out, err)
	}
	for _, tool := range []string{"chat_mcp_secret_put", "chat_mcp_secret_delete"} {
		if _, err := ts.mcpCallTool(admin, tool, map[string]interface{}{"value": "x"}); err == nil || !strings.Contains(err.Error(), "name is required") {
			t.Errorf("%s without a name: %v", tool, err)
		}
	}
	if _, err := ts.mcpCallTool(admin, "chat_mcp_secret_put", map[string]interface{}{"name": "x"}); err == nil {
		t.Error("a put without a value was accepted")
	}
	if _, err := s.PutChatScenario(context.Background(), &ChatScenario{Name: "support",
		McpServers: []ChatMcpServer{{Name: "w", URL: "https://w.example.com", SecretRef: "wiki"}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := ts.mcpCallTool(admin, "chat_mcp_secret_delete", map[string]interface{}{"name": "wiki"}); !errors.Is(err, errChatMcpSecretInUse) ||
		!strings.Contains(err.Error(), "in use by: support") {
		t.Errorf("delete in use: %v", err)
	}
	out, err = ts.mcpCallTool(admin, "chat_scenario_get", map[string]interface{}{"name": "support"})
	if err != nil || !strings.Contains(out, `"reason": "feature chatExternalMcp is off"`) {
		t.Errorf("scenario get: %s %v", out, err)
	}
	_ = s.DeleteChatScenario("support")
	if out, err := ts.mcpCallTool(admin, "chat_mcp_secret_delete", map[string]interface{}{"name": "wiki"}); err != nil || out != `{"deleted":true,"name":"wiki"}` {
		t.Errorf("delete: %s %v", out, err)
	}
	for _, tool := range []string{"chat_mcp_secret_list", "chat_mcp_secret_put", "chat_mcp_secret_delete"} {
		if _, err := ts.mcpCallTool(asClaims(alice), tool, map[string]interface{}{"name": "wiki", "value": "v"}); !errors.Is(err, ErrForbidden) {
			t.Errorf("%s as alice: %v", tool, err)
		}
	}
	failing := &MCPToolServer{client: covBNewAllErr(), globalMode: ModeRW}
	if _, err := failing.toolChatMcpSecretList(context.Background(), nil); !errors.Is(err, errCovBClient) {
		t.Errorf("list through a failing client: %v", err)
	}
}

func (c *covBAllErrClient) ChatMcpSecretList(context.Context) ([]*chatMcpSecretView, error) {
	return nil, errCovBClient
}

// Deleting a secret is gated (MCP-004) and asks for its name; the tools sit
// in the admin toolset, and only the listing works read-only.
func TestChatMcpSecretToolsClassification(t *testing.T) {
	ts := &MCPToolServer{}
	for name, readOnly := range map[string]bool{"chat_mcp_secret_list": true, "chat_mcp_secret_put": false, "chat_mcp_secret_delete": false} {
		if mcpToolGroup[name] != ToolsetAdmin || ts.isToolReadOnly(name) != readOnly || mcpToolAccessRules[name].class != accessAdmin {
			t.Errorf("%s: toolset %q, read-only %v", name, mcpToolGroup[name], ts.isToolReadOnly(name))
		}
		if mcpNeedsConfirmation(name) != (name == "chat_mcp_secret_delete") {
			t.Errorf("%s: gated = %v", name, mcpNeedsConfirmation(name))
		}
	}
	args := map[string]interface{}{"name": "wiki"}
	if err := checkMCPConfirmation("chat_mcp_secret_delete", args, accept(map[string]interface{}{"confirm": true, "name": "other"})); err == nil {
		t.Error("a wrong secret name was accepted")
	}
}

// Parity: a secret set on each surface lists the same on all three, and a
// scenario's servers written over MCP read the same over REST and gRPC.
func TestChatMcpParityAcrossSurfaces(t *testing.T) {
	s := chatServer(t)
	g, ts, admin := NewGRPCServer(s), chatMCP(s), asClaims(root)
	if rec := chatREST(t, s, root, http.MethodPut, "/v1/chat/mcp-secrets/via-rest", `{"value":"v","header":"X-Key"}`); rec.Code != http.StatusCreated {
		t.Fatalf("REST: %d %s", rec.Code, rec.Body.String())
	}
	if _, err := g.PutChatMcpSecret(admin, &proto.PutChatMcpSecretRequest{Name: "via-grpc", Value: "v", Header: "X-Key"}); err != nil {
		t.Fatal(err)
	}
	if _, err := ts.mcpCallTool(admin, "chat_mcp_secret_put", map[string]interface{}{"name": "via-mcp", "value": "v", "header": "X-Key"}); err != nil {
		t.Fatal(err)
	}
	var rest struct{ Secrets []*chatMcpSecretView }
	_ = json.Unmarshal(chatREST(t, s, root, http.MethodGet, "/v1/chat/mcp-secrets", "").Body.Bytes(), &rest)
	grpcList, _ := g.ListChatMcpSecrets(admin, &proto.ListChatMcpSecretsRequest{})
	out, _ := ts.mcpCallTool(admin, "chat_mcp_secret_list", nil)
	var mcp struct{ Secrets []*chatMcpSecretView }
	_ = json.Unmarshal([]byte(out), &mcp)
	if len(rest.Secrets) != 3 || len(grpcList.GetSecrets()) != 3 || len(mcp.Secrets) != 3 {
		t.Fatalf("lists: %d %d %d", len(rest.Secrets), len(grpcList.GetSecrets()), len(mcp.Secrets))
	}
	for i, v := range rest.Secrets {
		gv, mv := grpcList.GetSecrets()[i], mcp.Secrets[i]
		if v.Header != "X-Key" || gv.GetName() != v.Name || gv.GetHeader() != v.Header || mv.Name != v.Name || mv.Header != v.Header || !mv.Set || !gv.GetSet() {
			t.Errorf("%d: REST %+v gRPC %+v MCP %+v", i, v, gv, mv)
		}
	}

	server := map[string]interface{}{"name": "w", "url": "https://w.example.com/mcp", "enabled": true, "secretRef": "via-mcp",
		"allowedTools": []interface{}{"search"}, "timeoutMs": float64(500)}
	if _, err := ts.mcpCallTool(admin, "chat_scenario_put", map[string]interface{}{"name": "support",
		"scenario": map[string]interface{}{"mcpServers": []interface{}{server}}}); err != nil {
		t.Fatal(err)
	}
	restBody := chatREST(t, s, root, http.MethodGet, "/v1/chat/scenarios/support", "").Body.String()
	want := `"mcpServers":[{"name":"w","url":"https://w.example.com/mcp","enabled":true,"secretRef":"via-mcp","allowedTools":["search"],"timeoutMs":500,"resultMaxChars":4000}]`
	if !strings.Contains(restBody, want) {
		t.Errorf("REST: %s", restBody)
	}
	got, err := g.GetChatScenario(admin, &proto.GetChatScenarioRequest{Name: "support"})
	if m := got.GetMcpServers(); err != nil || len(m) != 1 || m[0].GetTimeoutMs() != 500 || m[0].GetAllowedTools()[0] != "search" {
		t.Errorf("gRPC: %+v %v", got, err)
	}
}
