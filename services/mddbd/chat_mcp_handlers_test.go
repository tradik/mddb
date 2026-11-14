package main

import (
	"net/http"
	"strings"
	"testing"

	json "mddb/internal/jsonx"

	bolt "go.etcd.io/bbolt"
)

// MCP-005: the REST surface — secrets, inspect, and the checks a scenario's
// servers pass when it is saved.

// chatMcpBody is a scenario body with one server.
func chatMcpBody(t *testing.T, server map[string]any, collections ...string) string {
	t.Helper()
	if collections == nil {
		collections = []string{}
	}
	b, _ := json.Marshal(map[string]any{"enabled": true, "allowedCollections": collections, "mcpServers": []any{server}})
	return string(b)
}

// Without an at-rest key the listing says so and a put is refused with 409
// naming the fix; nothing is stored and the value is never echoed.
func TestChatMcpSecretsRESTWithoutKey(t *testing.T) {
	s := chatServer(t)
	if rec := chatREST(t, s, root, http.MethodGet, "/v1/chat/mcp-secrets", ""); rec.Body.String() != `{"secrets":[],"encryptionReady":false}` {
		t.Fatalf("list: %s", rec.Body.String())
	}
	rec := chatREST(t, s, root, http.MethodPut, "/v1/chat/mcp-secrets/wiki", `{"value":"tok-plain"}`)
	want := `{"error":"` + errChatMcpSecretsNeedKey.Error() + `","requires":"MDDB_ENCRYPTION_KEY"}`
	if rec.Code != http.StatusConflict || strings.TrimSpace(rec.Body.String()) != want {
		t.Fatalf("put: %d %s", rec.Code, rec.Body.String())
	}
	if s.ChatScenarios.HasMcpSecret("wiki") {
		t.Error("a refused put was stored")
	}
}

// A secret stored unsealed is refused on use: inspect answers 409, not 502.
func TestChatMcpInspectRESTUnsealedSecret(t *testing.T) {
	s := chatServer(t)
	f := newMcpFixture(t)
	externalMcpOn(t, s)
	_ = s.DB.Update(func(tx *bolt.Tx) error {
		return tx.Bucket(bucketChatMcpSecrets).Put([]byte("old"), []byte(`{"name":"old","value":"cGxhaW4tdG9r"}`))
	})
	if err := s.ChatScenarios.LoadAll(); err != nil {
		t.Fatal(err)
	}
	rec := chatREST(t, s, root, http.MethodPost, "/v1/chat/mcp/inspect", `{"url":"`+f.URL+`","secretRef":"old"}`)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "stored unsealed") || strings.Contains(rec.Body.String(), "plain-tok") {
		t.Errorf("inspect: %d %s", rec.Code, rec.Body.String())
	}
}

func TestChatMcpSecretsREST(t *testing.T) {
	chatMcpKey(t)
	s := chatServer(t)
	if rec := chatREST(t, s, root, http.MethodGet, "/v1/chat/mcp-secrets", ""); rec.Body.String() != `{"secrets":[],"encryptionReady":true}` {
		t.Fatalf("empty: %s", rec.Body.String())
	}
	rec := chatREST(t, s, root, http.MethodPut, "/v1/chat/mcp-secrets/wiki", `{"value":"tok-123"}`)
	if rec.Code != http.StatusCreated || strings.Contains(rec.Body.String(), "tok-123") || !strings.Contains(rec.Body.String(), `"set":true`) {
		t.Fatalf("create: %d %s", rec.Code, rec.Body.String())
	}
	if rec := chatREST(t, s, root, http.MethodPut, "/v1/chat/mcp-secrets/wiki", `{"value":"tok-456","header":"X-Api-Key"}`); rec.Code != http.StatusOK {
		t.Errorf("replace: %d %s", rec.Code, rec.Body.String())
	}
	for body, want := range map[string]string{`{"value":"tok-789`: "the body must be", `{"value":"v","header":"Host"}`: "not a header"} {
		rec := chatREST(t, s, root, http.MethodPut, "/v1/chat/mcp-secrets/wiki", body)
		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), want) || strings.Contains(rec.Body.String(), "tok-789") {
			t.Errorf("%s: %d %s", body, rec.Code, rec.Body.String())
		}
	}
	rec = chatREST(t, s, root, http.MethodGet, "/v1/chat/mcp-secrets", "")
	if !strings.Contains(rec.Body.String(), `"header":"X-Api-Key","set":true`) || strings.Contains(rec.Body.String(), "tok-") {
		t.Errorf("list: %s", rec.Body.String())
	}
	if rec := chatREST(t, s, alice, http.MethodGet, "/v1/chat/mcp-secrets", ""); rec.Code != http.StatusForbidden {
		t.Errorf("a non-admin listed the secrets: %d", rec.Code)
	}
	if rec := chatREST(t, s, root, http.MethodPost, "/v1/chat/mcp-secrets", ""); rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST: %d", rec.Code)
	}

	// A scenario naming it keeps it; deleting it then answers 409.
	body := chatMcpBody(t, map[string]any{"name": "w", "url": "https://wiki.example.com/mcp", "secretRef": "wiki"})
	if rec := chatREST(t, s, root, http.MethodPut, "/v1/chat/scenarios/support", body); rec.Code != http.StatusCreated {
		t.Fatalf("scenario: %d %s", rec.Code, rec.Body.String())
	}
	if rec := chatREST(t, s, root, http.MethodDelete, "/v1/chat/mcp-secrets/wiki", ""); rec.Code != http.StatusConflict ||
		!strings.Contains(rec.Body.String(), `"usedBy":["support"]`) {
		t.Errorf("delete in use: %d %s", rec.Code, rec.Body.String())
	}
	_ = s.DeleteChatScenario("support")
	if rec := chatREST(t, s, root, http.MethodDelete, "/v1/chat/mcp-secrets/wiki", ""); rec.Code != http.StatusOK {
		t.Errorf("delete: %d %s", rec.Code, rec.Body.String())
	}
	if rec := chatREST(t, s, root, http.MethodDelete, "/v1/chat/mcp-secrets/wiki", ""); rec.Code != http.StatusNotFound {
		t.Errorf("delete again: %d", rec.Code)
	}
}

func TestChatMcpInspectREST(t *testing.T) {
	chatMcpKey(t)
	s := chatServer(t)
	inspect := func(body string) (int, string) {
		rec := chatREST(t, s, root, http.MethodPost, "/v1/chat/mcp/inspect", body)
		return rec.Code, rec.Body.String()
	}
	if code, body := inspect(`{"url":"https://wiki.example.com/mcp"}`); code != http.StatusNotFound ||
		body != `{"error":"feature disabled","feature":"chatExternalMcp"}` {
		t.Fatalf("module off: %d %s", code, body)
	}
	f := newMcpFixture(t)
	externalMcpOn(t, s)
	code, body := inspect(`{"url":"` + f.URL + `"}`)
	var in chatMcpInspection
	_ = json.Unmarshal([]byte(body), &in)
	if code != http.StatusOK || in.ServerName != "wiki" || len(in.Tools) != 10 || !in.Tools[0].Allowable {
		t.Fatalf("inspect: %d %s", code, body)
	}
	if w := in.Tools[8]; w.Name != "write_doc" || w.Allowable || w.Reason != "it is not marked readOnlyHint: true" {
		t.Errorf("a writer: %+v", w)
	}
	f.setToken("tok")
	for body, want := range map[string]int{
		`{"url":"` + f.URL + `"}`:                     http.StatusBadGateway,
		`{"url":"` + f.URL + `","secretRef":"ghost"}`: http.StatusNotFound,
		`{"url":"http://wiki.example.com"}`:           http.StatusBadRequest,
		`{"url":`:                                     http.StatusBadRequest,
	} {
		if code, out := inspect(body); code != want || strings.Contains(out, "tok") {
			t.Errorf("%s: %d %s", body, code, out)
		}
	}
	if _, err := s.PutChatMcpSecret("wiki", "", "tok"); err != nil {
		t.Fatal(err)
	}
	if code, out := inspect(`{"url":"` + f.URL + `","secretRef":"wiki"}`); code != http.StatusOK {
		t.Errorf("with the secret: %d %s", code, out)
	}
	f.failList.Store(true)
	if code, out := inspect(`{"url":"` + f.URL + `","secretRef":"wiki"}`); code != http.StatusBadGateway || !strings.Contains(out, "index offline") {
		t.Errorf("a failing tools/list: %d %s", code, out)
	}
	if rec := chatREST(t, s, root, http.MethodGet, "/v1/chat/mcp/inspect", ""); rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET: %d", rec.Code)
	}
	if rec := chatREST(t, s, alice, http.MethodPost, "/v1/chat/mcp/inspect", `{}`); rec.Code != http.StatusForbidden {
		t.Errorf("a non-admin inspected: %d", rec.Code)
	}
}

// Saving a scenario checks every allowed tool against the server's own list.
func TestChatScenarioSaveChecksMcpTools(t *testing.T) {
	s := chatServer(t)
	put := func(server map[string]any) (int, string) {
		rec := chatREST(t, s, root, http.MethodPut, "/v1/chat/scenarios/support", chatMcpBody(t, server))
		return rec.Code, rec.Body.String()
	}
	// With the module off nothing is contacted: any tool is stored, inactive.
	off := map[string]any{"name": "w", "url": "https://unreachable.invalid/mcp", "enabled": true, "allowedTools": []string{"write_doc"}}
	if code, body := put(off); code != http.StatusCreated {
		t.Fatalf("module off: %d %s", code, body)
	}
	rec := chatREST(t, s, root, http.MethodGet, "/v1/chat/scenarios/support/prompt", "")
	if !strings.Contains(rec.Body.String(), `"active":false,"reason":"feature chatExternalMcp is off"`) {
		t.Errorf("prompt: %s", rec.Body.String())
	}
	if code, body := put(map[string]any{"name": "w", "url": "https://a.example.com", "secretRef": "ghost"}); code != http.StatusBadRequest ||
		!strings.Contains(body, "secret ghost does not exist") {
		t.Errorf("a missing secret: %d %s", code, body)
	}

	f := newMcpFixture(t)
	externalMcpOn(t, s)
	for tool, want := range map[string]string{
		"write_doc": "tool write_doc of mcp server w is refused: it is not marked readOnlyHint: true",
		"wipe":      "tool wipe of mcp server w is refused: it is marked destructiveHint: true",
		"plain":     "tool plain of mcp server w is refused",
		"ghost":     "mcp server w does not offer the tool ghost",
	} {
		code, body := put(map[string]any{"name": "w", "url": f.URL, "enabled": true, "allowedTools": []string{"search", tool}})
		if code != http.StatusBadRequest || !strings.Contains(body, want) {
			t.Errorf("%s: %d %s", tool, code, body)
		}
	}
	if code, body := put(map[string]any{"name": "w", "url": f.URL, "enabled": true, "allowedTools": []string{"search", "slow"}}); code != http.StatusOK {
		t.Errorf("read-only tools: %d %s", code, body)
	}
	f.failList.Store(true)
	if code, body := put(map[string]any{"name": "w", "url": f.URL, "enabled": true}); code != http.StatusBadRequest ||
		!strings.Contains(body, "cannot be reached") {
		t.Errorf("an unreachable enabled server: %d %s", code, body)
	}
	f.setToken("tok") // initialize itself is refused
	if code, body := put(map[string]any{"name": "w", "url": f.URL, "enabled": true}); code != http.StatusBadRequest ||
		!strings.Contains(body, "401") {
		t.Errorf("a server refusing the session: %d %s", code, body)
	}
	if code, body := put(map[string]any{"name": "w", "url": f.URL, "allowedTools": []string{"anything"}}); code != http.StatusOK {
		t.Errorf("an unreachable disabled server: %d %s", code, body)
	}
}
