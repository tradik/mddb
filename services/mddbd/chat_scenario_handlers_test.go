package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	json "mddb/internal/jsonx"
)

// #403: the REST surface of chat scenarios, through the registered routes
// and the access gate.

// chatServer is accessServer with the scenario store, aliases and three
// collections: "docs" (alice reads it), "private" and "t1/kb" (tenant t1).
func chatServer(t *testing.T) *Server {
	t.Helper()
	s := accessServer(t)
	s.Aliases = NewAliasManager(s.DB)
	s.ChatScenarios = NewChatScenarioManager(s.DB)
	s.CollectionManager = NewCollectionManager(s.DB)
	for _, err := range []error{s.Aliases.LoadAll(), s.ChatScenarios.LoadAll(), s.CollectionManager.EnsureBucket()} {
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, coll := range []string{"docs", "private", "t1/kb"} {
		addTestDoc(t, s, coll, "a", "en", "hello", nil)
	}
	return s
}

// chatREST sends one request as c through the registered routes.
func chatREST(t *testing.T, s *Server, c *JWTClaims, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	mux := http.NewServeMux()
	s.registerRoutes(mux, true)
	rec := httptest.NewRecorder()
	asUser(c, mux).ServeHTTP(rec, httptest.NewRequest(method, target, strings.NewReader(body)))
	return rec
}

const chatSupportBody = `{"name":"ignored","description":"Support desk","enabled":true,
 "operatorPrompt":"You are Ada.","guardrails":"No legal advice.",
 "offTopic":{"mode":"redirect","redirectTemplate":"Ask me about MDDB."},
 "allowedCollections":["docs"],"skills":["triage"],
 "style":{"emoji":"sparingly","verbosity":"short","markdown":true,"citationTemplate":"[{{key}}]"},
 "llm":{"model":"gpt-x","temperature":0,"maxTokens":800,"extraBody":{"reasoning_effort":"low"}},
 "maxTurns":12,"tokenBudgetPerSession":50000,"rateLimit":{"messagesPerMinute":20}}`

func TestChatScenarioRESTLifecycle(t *testing.T) {
	s := chatServer(t)
	if rec := chatREST(t, s, root, http.MethodGet, "/v1/chat/scenarios", ""); rec.Body.String() != `{"scenarios":[],"version":0}` {
		t.Fatalf("empty list: %s", rec.Body.String())
	}
	rec := chatREST(t, s, root, http.MethodPut, "/v1/chat/scenarios/support", chatSupportBody)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body.String())
	}
	var got ChatScenario
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Name != "support" || got.LLM.Temperature == nil || *got.LLM.Temperature != 0 ||
		got.LLM.ExtraBody["reasoning_effort"] != "low" || got.RateLimit.MessagesPerMinute != 20 ||
		got.CreatedAt.IsZero() || got.Skills[0] != "triage" || got.TokenBudgetPerSession != 50000 {
		t.Errorf("stored: %+v", got)
	}
	for _, field := range []string{`"operatorPrompt"`, `"allowedCollections"`, `"offTopic"`, `"redirectTemplate"`,
		`"citationTemplate"`, `"maxTokens"`, `"extraBody"`, `"maxTurns"`, `"tokenBudgetPerSession"`, `"messagesPerMinute"`, `"createdAt"`, `"updatedAt"`} {
		if !strings.Contains(rec.Body.String(), field) {
			t.Errorf("the body lacks %s: %s", field, rec.Body.String())
		}
	}
	if rec := chatREST(t, s, root, http.MethodPut, "/v1/chat/scenarios/support", `{"allowedCollections":["docs"]}`); rec.Code != http.StatusOK {
		t.Fatalf("replace: %d %s", rec.Code, rec.Body.String())
	}
	if rec := chatREST(t, s, root, http.MethodGet, "/v1/chat/scenarios/version", ""); rec.Body.String() != `{"version":2}` {
		t.Errorf("version: %s", rec.Body.String())
	}
	if rec := chatREST(t, s, root, http.MethodGet, "/v1/chat/scenarios/support", ""); rec.Code != 200 ||
		!strings.Contains(rec.Body.String(), `"mode":"refuse"`) {
		t.Errorf("get: %d %s", rec.Code, rec.Body.String())
	}
	for body, why := range map[string]string{`{`: "bad json", `{"allowedCollections":["ghost"]}`: "unknown collection",
		`{"style":{"emoji":"lots"}}`: "bad emoji"} {
		if rec := chatREST(t, s, root, http.MethodPut, "/v1/chat/scenarios/other", body); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: %d", why, rec.Code)
		}
	}
	if rec := chatREST(t, s, root, http.MethodDelete, "/v1/chat/scenarios/support", ""); rec.Body.String() != `{"deleted":true,"version":3}` {
		t.Errorf("delete: %d %s", rec.Code, rec.Body.String())
	}
	for _, target := range []string{"/v1/chat/scenarios/support", "/v1/chat/scenarios/support/prompt"} {
		if rec := chatREST(t, s, root, http.MethodGet, target, ""); rec.Code != http.StatusNotFound {
			t.Errorf("GET %s after delete: %d", target, rec.Code)
		}
	}
	if rec := chatREST(t, s, root, http.MethodDelete, "/v1/chat/scenarios/support", ""); rec.Code != http.StatusNotFound {
		t.Errorf("delete twice: %d", rec.Code)
	}
	for method, target := range map[string]string{http.MethodPost: "/v1/chat/scenarios", http.MethodPut: "/v1/chat/scenarios/a/prompt",
		http.MethodDelete: "/v1/chat/scenarios", http.MethodPatch: "/v1/chat/scenarios/a"} {
		if rec := chatREST(t, s, root, method, target, "{}"); rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s %s: %d", method, target, rec.Code)
		}
	}
}

func TestChatScenarioPromptComposesCollectionPromptsThroughAliases(t *testing.T) {
	s := chatServer(t)
	if err := s.CollectionManager.Set("docs", &CollectionConfig{ResponsePrompt: "Use code blocks for {{collection}}."}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetAlias("kb", "docs"); err != nil {
		t.Fatal(err)
	}
	body := `{"operatorPrompt":"You are Ada.","allowedCollections":["kb","private"]}`
	if rec := chatREST(t, s, root, http.MethodPut, "/v1/chat/scenarios/support", body); rec.Code != http.StatusCreated {
		t.Fatalf("an alias as an allowed collection: %d %s", rec.Code, rec.Body.String())
	}
	rec := chatREST(t, s, root, http.MethodGet, "/v1/chat/scenarios/support/prompt", "")
	var out chatScenarioPrompt
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Name != "support" || out.Version != 1 || !strings.HasPrefix(out.Prompt, "You are Ada.\n\nIf a question is outside") ||
		!strings.HasSuffix(out.Prompt, "For answers drawn from the collection kb:\nUse code blocks for docs.") {
		t.Errorf("prompt: %+v", out)
	}
}

func TestChatScenarioPermissions(t *testing.T) {
	s := chatServer(t)
	for name, colls := range map[string]string{"open": `[]`, "docs-bot": `["docs"]`, "private-bot": `["docs","private"]`, "tenant-bot": `["t1/kb"]`} {
		if rec := chatREST(t, s, root, http.MethodPut, "/v1/chat/scenarios/"+name, `{"allowedCollections":`+colls+`}`); rec.Code != http.StatusCreated {
			t.Fatalf("%s: %d %s", name, rec.Code, rec.Body.String())
		}
	}
	names := func(c *JWTClaims) string {
		var list chatScenarioList
		if err := json.Unmarshal(chatREST(t, s, c, http.MethodGet, "/v1/chat/scenarios", "").Body.Bytes(), &list); err != nil {
			t.Fatal(err)
		}
		out := []string{}
		for _, sc := range list.Scenarios {
			out = append(out, sc.Name)
		}
		return strings.Join(out, ",")
	}
	for who, want := range map[*JWTClaims]string{root: "docs-bot,open,private-bot,tenant-bot", alice: "docs-bot,open", tina: "open,tenant-bot", nobby: "open"} {
		if got := names(who); got != want {
			t.Errorf("%s lists %q, want %q", who.Username, got, want)
		}
	}
	for target, want := range map[string]int{"/v1/chat/scenarios/docs-bot": 200, "/v1/chat/scenarios/docs-bot/prompt": 200,
		"/v1/chat/scenarios/private-bot": 403, "/v1/chat/scenarios/private-bot/prompt": 403, "/v1/chat/scenarios/version": 200} {
		if rec := chatREST(t, s, alice, http.MethodGet, target, ""); rec.Code != want {
			t.Errorf("alice GET %s: %d, want %d", target, rec.Code, want)
		}
	}
	// Writes are for a global administrator, on any name.
	for _, who := range []*JWTClaims{alice, tina} {
		if rec := chatREST(t, s, who, http.MethodPut, "/v1/chat/scenarios/mine", `{}`); rec.Code != http.StatusForbidden {
			t.Errorf("%s PUT: %d", who.Username, rec.Code)
		}
		if rec := chatREST(t, s, who, http.MethodDelete, "/v1/chat/scenarios/open", ""); rec.Code != http.StatusForbidden {
			t.Errorf("%s DELETE: %d", who.Username, rec.Code)
		}
	}
	// No credentials: the gate answers before the handler.
	mux := http.NewServeMux()
	s.registerRoutes(mux, true)
	for _, target := range []string{"/v1/chat/scenarios", "/v1/chat/scenarios/private-bot"} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, target, nil))
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("anonymous GET %s: %d", target, rec.Code)
		}
	}
	// A read-only server refuses the writes and still serves the reads.
	s.Mode = ModeRead
	if rec := chatREST(t, s, root, http.MethodPut, "/v1/chat/scenarios/open", `{}`); rec.Code != http.StatusForbidden {
		t.Errorf("PUT on a read-only server: %d", rec.Code)
	}
	if rec := chatREST(t, s, root, http.MethodDelete, "/v1/chat/scenarios/open", ""); rec.Code != http.StatusForbidden {
		t.Errorf("DELETE on a read-only server: %d", rec.Code)
	}
	if got := names(root); !strings.Contains(got, "open") {
		t.Errorf("read-only list: %q", got)
	}
}

func TestChatScenariosWithoutAStoreOrAuth(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	if _, err := s.PutChatScenario(context.Background(), &ChatScenario{Name: "s"}); err != errChatScenariosUnavailable {
		t.Errorf("put: %v", err)
	}
	if err := s.DeleteChatScenario("s"); err != errChatScenariosUnavailable {
		t.Errorf("delete: %v", err)
	}
	if _, err := s.GetChatScenario(t.Context(), "s"); err != errChatScenariosUnavailable {
		t.Errorf("get: %v", err)
	}
	if len(s.ListChatScenarios(t.Context())) != 0 || s.ChatScenariosVersion() != 0 {
		t.Error("a server without a store lists scenarios")
	}
	rec := httptest.NewRecorder()
	s.handleChatScenarios(rec, httptest.NewRequest(http.MethodGet, "/v1/chat/scenarios/s", nil))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("GET without a store: %d", rec.Code)
	}
	// Auth off: everything is readable.
	s.ChatScenarios = NewChatScenarioManager(s.DB)
	if err := s.ChatScenarios.LoadAll(); err != nil {
		t.Fatal(err)
	}
	addTestDoc(t, s, "docs", "a", "en", "hello", nil)
	if _, err := s.PutChatScenario(context.Background(), &ChatScenario{Name: "s", AllowedCollections: []string{"docs"}}); err != nil {
		t.Fatal(err)
	}
	if sc, err := s.GetChatScenario(t.Context(), "s"); err != nil || sc.Name != "s" {
		t.Errorf("get with auth off: %v", err)
	}
}
