package main

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"unicode/utf8"

	"mddb/internal/encryption"
	json "mddb/internal/jsonx"
)

// MCP-005: calling an external tool for a chat — every switch is checked
// again, and what fails upstream is a short text for the model.

// mcpCallServer stores a scenario with one server at f's URL.
func mcpCallServer(t *testing.T, f *mcpFixture, server map[string]any, collections ...string) *Server {
	t.Helper()
	s := chatServer(t)
	externalMcpOn(t, s)
	server["name"], server["url"] = "wiki", f.URL
	if rec := chatREST(t, s, root, http.MethodPut, "/v1/chat/scenarios/support", chatMcpBody(t, server, collections...)); rec.Code != http.StatusCreated {
		t.Fatalf("scenario: %d %s", rec.Code, rec.Body.String())
	}
	return s
}

// chatMcpCallAs calls tool as c over REST.
func chatMcpCallAs(t *testing.T, s *Server, c *JWTClaims, tool string) (int, chatMcpCallResult, string) {
	t.Helper()
	body, _ := json.Marshal(chatMcpCallRequest{Scenario: "support", Server: "wiki", Tool: tool, Arguments: map[string]any{"q": "mddb"}})
	rec := chatREST(t, s, c, http.MethodPost, "/v1/chat/mcp/call", string(body))
	var out chatMcpCallResult
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out, rec.Body.String()
}

func TestChatMcpCallREST(t *testing.T) {
	f := newMcpFixture(t)
	s := mcpCallServer(t, f, map[string]any{"enabled": true, "timeoutMs": 150, "resultMaxChars": 100,
		"allowedTools": []string{"search", "slow", "big", "boom", "failing"}}, "docs")
	if code, out, body := chatMcpCallAs(t, s, alice, "search"); code != http.StatusOK || out.Content != "found mddb" || out.IsError || out.Tool != "search" {
		t.Fatalf("search: %d %s", code, body)
	}
	if _, out, _ := chatMcpCallAs(t, s, alice, "big"); !out.Truncated || utf8.RuneCountInString(out.Content) != 100 {
		t.Errorf("big: %+v", out)
	}
	for tool, want := range map[string]string{
		"slow":    "the external tool slow of wiki did not answer within 150 ms",
		"boom":    "the external tool boom of wiki failed: tools/call: exploded with  (code -32000)",
		"failing": "no such page",
	} {
		if code, out, body := chatMcpCallAs(t, s, alice, tool); code != http.StatusOK || !out.IsError || out.Content != want {
			t.Errorf("%s: %d %s", tool, code, body)
		}
	}
	for tool, want := range map[string]int{"write_doc": http.StatusForbidden} {
		if code, _, body := chatMcpCallAs(t, s, alice, tool); code != want || !strings.Contains(body, "not in the allowed tools") {
			t.Errorf("%s: %d %s", tool, code, body)
		}
	}
	// The server now marks search as a writer: refused on the call too.
	f.searchRO.Store(false)
	if code, _, body := chatMcpCallAs(t, s, alice, "search"); code != http.StatusForbidden || !strings.Contains(body, "not marked readOnlyHint") {
		t.Errorf("search turned writer: %d %s", code, body)
	}
	f.searchRO.Store(true)
	f.failList.Store(true)
	if _, out, _ := chatMcpCallAs(t, s, alice, "search"); !out.IsError || !strings.Contains(out.Content, "index offline") {
		t.Errorf("a failing tools/list: %+v", out)
	}
	f.failList.Store(false)
	f.token = "tok"
	if _, out, _ := chatMcpCallAs(t, s, alice, "search"); !out.IsError || !strings.Contains(out.Content, "401") {
		t.Errorf("a refused connection: %+v", out)
	}
	if code, _, _ := chatMcpCallAs(t, s, nobby, "search"); code != http.StatusForbidden {
		t.Errorf("a caller who may not read the scenario: %d", code)
	}
	setFeature(t, s, FeatureChatExternalMCP, false)
	if code, _, body := chatMcpCallAs(t, s, alice, "search"); code != http.StatusForbidden || !strings.Contains(body, "feature chatExternalMcp is off") {
		t.Errorf("module off: %d %s", code, body)
	}
}

func TestChatMcpCallRESTRefusals(t *testing.T) {
	f := newMcpFixture(t)
	s := mcpCallServer(t, f, map[string]any{"allowedTools": []string{"search"}})
	if code, _, body := chatMcpCallAs(t, s, root, "search"); code != http.StatusForbidden || !strings.Contains(body, "server is disabled") {
		t.Errorf("a disabled server: %d %s", code, body)
	}
	for body, want := range map[string]int{
		`{"scenario":"ghost","server":"wiki","tool":"search"}`:    http.StatusNotFound,
		`{"scenario":"support","server":"ghost","tool":"search"}`: http.StatusNotFound,
		`{"scenario":`: http.StatusBadRequest,
	} {
		if rec := chatREST(t, s, root, http.MethodPost, "/v1/chat/mcp/call", body); rec.Code != want {
			t.Errorf("%s: %d %s", body, rec.Code, rec.Body.String())
		}
	}
	if rec := chatREST(t, s, root, http.MethodGet, "/v1/chat/mcp/call", ""); rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET: %d", rec.Code)
	}

	// A tool allowed while the module was off, gone from the server since.
	s2 := chatServer(t)
	retired := map[string]any{"name": "wiki", "url": f.URL, "enabled": true, "allowedTools": []string{"retired"}}
	if rec := chatREST(t, s2, root, http.MethodPut, "/v1/chat/scenarios/support", chatMcpBody(t, retired)); rec.Code != http.StatusCreated {
		t.Fatalf("scenario: %d %s", rec.Code, rec.Body.String())
	}
	externalMcpOn(t, s2)
	if _, out, _ := chatMcpCallAs(t, s2, root, "retired"); !out.IsError || out.Content != "the external tool retired of wiki failed: wiki no longer offers the tool retired" {
		t.Errorf("a retired tool: %+v", out)
	}
}

// A secret sealed with a key the server no longer has: the call fails
// short, without the value.
func TestChatMcpCallWithAnUnopenableSecret(t *testing.T) {
	f := newMcpFixture(t)
	f.token = "sealed-tok"
	t.Setenv("MDDB_ENCRYPTION_KEY", genKey(t))
	e, err := encryption.NewEncryptor()
	if err != nil {
		t.Fatal(err)
	}
	SetGlobalEncryptor(e)
	defer SetGlobalEncryptor(nil)
	s := chatServer(t)
	externalMcpOn(t, s)
	if _, err := s.PutChatMcpSecret("wiki", "", "sealed-tok"); err != nil {
		t.Fatal(err)
	}
	server := map[string]any{"name": "wiki", "url": f.URL, "enabled": true, "secretRef": "wiki", "allowedTools": []string{"search"}}
	if rec := chatREST(t, s, root, http.MethodPut, "/v1/chat/scenarios/support", chatMcpBody(t, server)); rec.Code != http.StatusCreated {
		t.Fatalf("scenario: %d %s", rec.Code, rec.Body.String())
	}
	if _, out, _ := chatMcpCallAs(t, s, root, "search"); out.IsError || out.Content != "found mddb" {
		t.Fatalf("with the key: %+v", out)
	}
	SetGlobalEncryptor(nil)
	if _, out, _ := chatMcpCallAs(t, s, root, "search"); !out.IsError || !strings.Contains(out.Content, "cannot be opened") ||
		strings.Contains(out.Content, "sealed-tok") {
		t.Errorf("without the key: %+v", out)
	}
}

// The store's absence.
func TestChatMcpServiceEdges(t *testing.T) {
	s := chatServer(t)
	externalMcpOn(t, s)
	s.ChatScenarios = nil
	if len(s.ListChatMcpSecrets()) != 0 {
		t.Error("secrets without a store")
	}
	if _, err := s.PutChatMcpSecret("a", "", "v"); err != errChatScenariosUnavailable {
		t.Errorf("put: %v", err)
	}
	if _, err := s.DeleteChatMcpSecret("a"); err != errChatScenariosUnavailable {
		t.Errorf("delete: %v", err)
	}
	if _, err := s.InspectChatMcpServer(context.Background(), "https://a", ""); err != errChatScenariosUnavailable {
		t.Errorf("inspect: %v", err)
	}
	if truncated, cut := truncateRunes("żółw", 2); truncated != "żó" || !cut {
		t.Errorf("truncateRunes: %q %v", truncated, cut)
	}
}
