package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// hookServer is a run-now server with a git sync whose hook is verified
// with the stored secret "hook-key".
func hookServer(t *testing.T) (*Server, *fakeForge, AutomationRule) {
	t.Helper()
	s, _ := runNowServer(t)
	chatMcpKey(t)
	s.ChatScenarios = NewChatScenarioManager(s.DB)
	if err := s.ChatScenarios.LoadAll(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PutChatMcpSecret("hook-key", "", "hook-s3cret"); err != nil {
		t.Fatal(err)
	}
	f := newFakeForge(t, forgeGitea)
	f.commit("refs/heads/main", commitA, map[string]string{"a.md": "# A"})
	a := gitSyncAction(f, "main")
	a.HookSecretRef = "hook-key"
	return s, f, syncRule(t, s, a)
}

// postHook calls POST /v1/automation/{id}/hook with the body and headers.
func postHook(s *Server, id, body string, header map[string]string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodPost, "/v1/automation/"+id+"/hook", strings.NewReader(body))
	for k, v := range header {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	s.handleAutomationDetail(w, r)
	return w
}

// hubSignature is GitHub's X-Hub-Signature-256 of body.
func hubSignature(secret, body string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(body))
	return hex.EncodeToString(mac.Sum(nil))
}

// #421: a signed push webhook runs the sync; an unsigned or wrongly signed
// one runs nothing.
func TestAutomationHookRunsTheSync(t *testing.T) {
	s, f, rule := hookServer(t)
	body := `{"ref":"refs/heads/main"}`
	for name, h := range map[string]map[string]string{
		"no signature":    {},
		"wrong signature": {"X-Hub-Signature-256": "sha256=" + hubSignature("other", body)},
		"not hex":         {"X-Hub-Signature-256": "sha256=zz"},
		"wrong token":     {"X-Gitlab-Token": "nope"},
	} {
		if w := postHook(s, rule.ID, body, h); w.Code != http.StatusUnauthorized {
			t.Errorf("%s: %d %s", name, w.Code, w.Body)
		}
	}
	if f.archiveCount() != 0 {
		t.Fatal("a refused hook ran the sync")
	}

	if w := postHook(s, rule.ID, body, map[string]string{"X-Hub-Signature-256": "sha256=" + hubSignature("hook-s3cret", body),
		"X-GitHub-Event": "ping"}); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "pong") {
		t.Errorf("ping: %d %s", w.Code, w.Body)
	}
	w := postHook(s, rule.ID, body, map[string]string{"X-Hub-Signature-256": "sha256=" + hubSignature("hook-s3cret", body)})
	if w.Code != http.StatusAccepted || !strings.Contains(w.Body.String(), `"action":"sync"`) {
		t.Fatalf("a signed push: %d %s", w.Code, w.Body)
	}
	s.CronScheduler.runs.Wait()
	if f.archiveCount() != 1 {
		t.Errorf("the sync did not run: %d archives", f.archiveCount())
	}
	for name, h := range map[string]map[string]string{
		"gitea":   {"X-Gitea-Signature": hubSignature("hook-s3cret", body)},
		"forgejo": {"X-Forgejo-Signature": hubSignature("hook-s3cret", body)},
		"gitlab":  {"X-Gitlab-Token": "hook-s3cret"},
	} {
		if w := postHook(s, rule.ID, body, h); w.Code != http.StatusAccepted && w.Code != http.StatusConflict {
			t.Errorf("%s: %d %s", name, w.Code, w.Body)
		}
		s.CronScheduler.runs.Wait()
	}
}

func TestAutomationHookRefusals(t *testing.T) {
	s, _, rule := hookServer(t)
	plain := syncRule(t, s, &SyncAction{Source: SyncSourceURLs, URLs: []string{"https://example.com/"}, Collection: "w", Lang: "en"})
	for name, id := range map[string]string{"unknown": "nope", "no hook": plain.ID} {
		if w := postHook(s, id, "{}", nil); w.Code != http.StatusNotFound {
			t.Errorf("%s: %d", name, w.Code)
		}
	}
	w := httptest.NewRecorder()
	s.handleAutomationDetail(w, httptest.NewRequest(http.MethodGet, "/v1/automation/"+rule.ID+"/hook", nil))
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET: %d", w.Code)
	}
	r := httptest.NewRequest(http.MethodPost, "/v1/automation/"+rule.ID+"/hook", strings.NewReader(strings.Repeat("x", 64)))
	r.Body = http.MaxBytesReader(httptest.NewRecorder(), r.Body, 16)
	w = httptest.NewRecorder()
	s.handleAutomationDetail(w, r)
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("a body over the cap: %d", w.Code)
	}
	if err := hashBody(sha256.New(), strings.NewReader(strings.Repeat("x", maxHookBodyBytes+1))); err == nil {
		t.Error("a body over maxHookBodyBytes")
	}

	// A secret that cannot be opened: 503, nothing said about it.
	SetGlobalEncryptor(nil)
	if w := postHook(s, rule.ID, "{}", nil); w.Code != http.StatusServiceUnavailable || strings.Contains(w.Body.String(), "hook-key") {
		t.Errorf("an unopenable secret: %d %s", w.Code, w.Body)
	}
	s.CronScheduler = nil // the run itself refused: no scheduler
	chatMcpKey(t)
	s.ChatScenarios = NewChatScenarioManager(s.DB)
	_ = s.ChatScenarios.LoadAll()
	_, _ = s.PutChatMcpSecret("hook-key", "", "k")
	if w := postHook(s, rule.ID, "{}", map[string]string{"X-Gitlab-Token": "k"}); w.Code != http.StatusServiceUnavailable {
		t.Errorf("no scheduler: %d %s", w.Code, w.Body)
	}
}

// The hook is the one automation route open without credentials: its
// request is verified by signature instead.
func TestAutomationHookPathIsPublic(t *testing.T) {
	am := &AuthManager{publicEndpoints: buildPublicEndpoints()}
	for p, want := range map[string]bool{
		"/v1/automation/abc/hook": true, "/v1/automation/abc/run": false, "/v1/automation//hook": false,
		"/v1/automation/a/b/hook": false, "/v1/automation/abc": false, "/v1/other/abc/hook": false,
	} {
		if got := am.isPublicEndpoint(p); got != want {
			t.Errorf("%s: %v", p, got)
		}
	}
}
