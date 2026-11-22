package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// timeNow is the run's clock for a test.
func timeNow() time.Time { return time.Now() }

// storeSecret loads the chat's secret store with an at-rest key and stores
// one secret.
func storeSecret(t *testing.T, s *Server, name, value string) {
	t.Helper()
	if s.ChatScenarios == nil {
		chatMcpKey(t)
		s.ChatScenarios = NewChatScenarioManager(s.DB)
		if err := s.ChatScenarios.LoadAll(); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.PutChatMcpSecret(name, "", value); err != nil {
		t.Fatal(err)
	}
}

// getAutomationRule is the body of GET /v1/automation/{id}.
func getAutomationRule(s *Server, id string) string {
	w := httptest.NewRecorder()
	s.handleAutomationDetail(w, httptest.NewRequest(http.MethodGet, "/v1/automation/"+id, nil))
	return w.Body.String()
}

// postRefresh calls POST /v1/sync/refresh.
func postRefresh(s *Server, body string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	s.handleSyncRefresh(w, httptest.NewRequest(http.MethodPost, syncRefreshPath, strings.NewReader(body)))
	return w
}

// resetRefreshes forgets the refreshes of earlier tests.
func resetRefreshes(t *testing.T) {
	t.Helper()
	syncRefreshes.Clear()
	t.Cleanup(syncRefreshes.Clear)
}

// INT-023: a refresh names the collection; one item refreshes at once, at
// most once every 30 s per rule, and the answer says when the rule last ran.
func TestSyncRefreshREST(t *testing.T) {
	resetRefreshes(t)
	s, _ := runNowServer(t)
	f := newFakeIssues(t, forgeGitHub)
	seedIssues(f)
	rule := syncRule(t, s, issuesAction(f))

	for body, want := range map[string]int{`{`: 400, `{"collection":""}`: 400, `{"collection":"nothing"}`: 404,
		`{"collection":"repo-issues","number":-1}`: 400} {
		if w := postRefresh(s, body); w.Code != want {
			t.Errorf("%s: %d %s", body, w.Code, w.Body)
		}
	}
	if w := httptest.NewRecorder(); func() int {
		s.handleSyncRefresh(w, httptest.NewRequest(http.MethodGet, syncRefreshPath, nil))
		return w.Code
	}() != http.StatusMethodNotAllowed {
		t.Error("GET was served")
	}

	w := postRefresh(s, `{"collection":"repo-issues","number":1}`)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"status":"done"`) || !strings.Contains(w.Body.String(), `"added":1`) {
		t.Fatalf("refresh one: %d %s", w.Code, w.Body)
	}
	w = postRefresh(s, `{"collection":"repo-issues","number":1}`)
	if w.Code != http.StatusTooManyRequests || w.Header().Get("Retry-After") == "" || !strings.Contains(w.Body.String(), "last run of rule") {
		t.Errorf("a second refresh: %d %s", w.Code, w.Body)
	}

	syncRefreshNow = func() time.Time { return time.Now().Add(time.Minute) }
	t.Cleanup(func() { syncRefreshNow = time.Now })
	w = postRefresh(s, `{"collection":"repo-issues"}`)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"status":"started"`) || !strings.Contains(w.Body.String(), `"lastRunAt"`) {
		t.Fatalf("a pass: %d %s", w.Code, w.Body)
	}
	s.CronScheduler.runs.Wait()
	if res := s.lastRunResult(&rule); res == nil || res.Status != "success" {
		t.Errorf("the pass: %+v", res)
	}
}

// The states a refresh answers besides done and started.
func TestSyncRefreshStates(t *testing.T) {
	resetRefreshes(t)
	s := newBackupTestServer(t) // no scheduler: a pass cannot start
	f := newFakeIssues(t, forgeGitHub)
	rule := syncRule(t, s, issuesAction(f))
	res, err := s.syncRefresh("repo-issues", 0)
	if err != nil || res.Runs[0].Status != "error" || res.Runs[0].Error == "" {
		t.Errorf("no scheduler: %+v, %v", res, err)
	}
	syncRuns.Store(rule.ID, true)
	defer syncRuns.Delete(rule.ID)
	syncRefreshes.Clear()
	if res, _ := s.syncRefresh("repo-issues", 3); res.Runs[0].Status != "running" {
		t.Errorf("a run going: %+v", res)
	}
	f.set(func(f *fakeIssues) { f.status = 500 })
	syncRuns.Delete(rule.ID)
	syncRefreshes.Clear()
	if res, _ := s.syncRefresh("repo-issues", 3); res.Runs[0].Status != "error" {
		t.Errorf("a forge error: %+v", res)
	}
	if refreshStatus(errActionRunning, "x") != "running" {
		t.Error("errActionRunning")
	}
	if _, err := (&Server{}).syncRefresh("c", 0); err == nil {
		t.Error("no automation manager")
	}
	// A git rule is no issues rule: a number finds none.
	syncRule(t, s, gitSyncAction(newFakeForge(t, forgeGitea), "main"))
	if _, err := s.syncRefresh("repo", 1); err == nil {
		t.Error("a number for a git sync")
	}
}

// A key limited to the collection may refresh it, through the MCP tool; a
// key limited to another may not; neither may call run_automation.
func TestSyncRefreshScopedKey(t *testing.T) {
	resetRefreshes(t)
	s := newBackupTestServer(t)
	s.Aliases = NewAliasManager(s.DB)
	if err := s.Aliases.LoadAll(); err != nil {
		t.Fatal(err)
	}
	f := newFakeIssues(t, forgeGitHub)
	seedIssues(f)
	rule := syncRule(t, s, issuesAction(f))
	ts := &MCPToolServer{client: &DirectClient{server: s}, globalMode: ModeRW}
	in := withKeyScope(context.Background(), mcpKeyScope{collections: []string{"repo-issues"}})
	out := withKeyScope(context.Background(), mcpKeyScope{collections: []string{"other"}})
	readOnly := withKeyScope(context.Background(), mcpKeyScope{collections: []string{"repo-issues"}, readOnly: true})

	res, err := ts.mcpCallTool(in, "sync_refresh", map[string]any{"collection": "repo-issues", "number": float64(1)})
	if err != nil || !strings.Contains(res, `"status": "done"`) {
		t.Fatalf("in scope: %s, %v", res, err)
	}
	if _, err := ts.mcpCallTool(in, "sync_refresh", map[string]any{"collection": "repo-issues", "number": float64(1)}); err == nil ||
		!strings.Contains(err.Error(), "try again in") {
		t.Errorf("throttled over MCP: %v", err)
	}
	if _, err := ts.mcpCallTool(out, "sync_refresh", map[string]any{"collection": "repo-issues"}); err == nil ||
		!strings.Contains(err.Error(), "forbidden") {
		t.Errorf("out of scope: %v", err)
	}
	// A read-only key, and one holding the collection read-only, may refresh
	// a mirror in their scope: they pass the scope check and meet the same
	// 30 s limit as any caller.
	partly := withKeyScope(context.Background(), mcpKeyScope{collections: []string{"repo-issues", "work"}, readOnlyCollections: []string{"repo-issues"}})
	for name, ctx := range map[string]context.Context{"read-only": readOnly, "read-only collection": partly} {
		if _, err := ts.mcpCallTool(ctx, "sync_refresh", map[string]any{"collection": "repo-issues"}); err == nil ||
			!strings.Contains(err.Error(), "try again in") {
			t.Errorf("%s: %v", name, err)
		}
		if _, err := ts.mcpCallTool(ctx, "add_document", map[string]any{"collection": "repo-issues", "key": "x", "lang": "en", "content": "x"}); err == nil ||
			!strings.Contains(err.Error(), "forbidden") {
			t.Errorf("%s may write a document: %v", name, err)
		}
	}
	if _, err := ts.mcpCallTool(in, "run_automation", map[string]any{"id": rule.ID}); err == nil || !strings.Contains(err.Error(), "forbidden") {
		t.Errorf("run_automation for a scoped key: %v", err)
	}
	if !keyScopeMayList(in, "sync_refresh") || keyScopeMayList(in, "run_automation") {
		t.Error("tools/list for a scoped key")
	}
	if _, err := ts.mcpCallTool(in, "sync_refresh", map[string]any{}); err == nil {
		t.Error("no collection")
	}
	if _, err := ts.mcpCallTool(context.Background(), "sync_refresh", map[string]any{"collection": "none"}); err == nil {
		t.Error("no rule")
	}
}
