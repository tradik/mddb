package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	bolt "go.etcd.io/bbolt"
)

// callForgeTokens calls /v1/forge-tokens.
func callForgeTokens(s *Server, method, body string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	s.handleForgeTokens(w, httptest.NewRequest(method, forgeTokensPath, strings.NewReader(body)))
	return w
}

// INT-023: the default forge tokens are a map of hosts to stored secrets,
// replaced whole, kept with the secrets across a reload.
func TestForgeTokensREST(t *testing.T) {
	s := newBackupTestServer(t)
	if w := callForgeTokens(s, http.MethodGet, ""); w.Code != http.StatusServiceUnavailable {
		t.Errorf("no secret store: %d", w.Code)
	}
	storeSecret(t, s, "gh", "x")
	for body, want := range map[string]string{
		`{`: "the body must be", `{"defaults":{"https://github.com":"gh"}}`: "lower-case host",
		`{"defaults":{"github.com":"none"}}`: "not found",
	} {
		if w := callForgeTokens(s, http.MethodPut, body); w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), want) {
			t.Errorf("%s: %d %s", body, w.Code, w.Body)
		}
	}
	if w := callForgeTokens(s, http.MethodPut, `{"defaults":{" GitHub.com ":" gh ","git.example:8443":"gh"}}`); w.Code != 200 ||
		!strings.Contains(w.Body.String(), `"github.com":"gh"`) {
		t.Fatalf("a put: %d %s", w.Code, w.Body)
	}
	if err := s.ChatScenarios.LoadAll(); err != nil {
		t.Fatal(err)
	}
	if w := callForgeTokens(s, http.MethodGet, ""); !strings.Contains(w.Body.String(), `"git.example:8443":"gh"`) {
		t.Errorf("after a reload: %s", w.Body)
	}
	for _, sec := range s.ChatScenarios.ListMcpSecrets() {
		if sec.Name == "" {
			t.Error("the map was read as a secret")
		}
	}
	if w := callForgeTokens(s, http.MethodDelete, ""); w.Code != http.StatusMethodNotAllowed {
		t.Errorf("DELETE: %d", w.Code)
	}
	many := map[string]string{}
	for i := range maxForgeTokenDefaults + 1 {
		many[strings.Repeat("h", i+1)+".example"] = "gh"
	}
	if s.ChatScenarios.SetForgeTokenDefaults(many) == nil {
		t.Error("too many defaults")
	}
	if (*ChatScenarioManager)(nil).ForgeTokenDefault("x") != "" {
		t.Error("a nil manager")
	}
}

// A corrupt stored map is none.
func TestForgeTokensCorrupt(t *testing.T) {
	s := newBackupTestServer(t)
	storeSecret(t, s, "gh", "x")
	if err := s.ChatScenarios.SetForgeTokenDefaults(map[string]string{"github.com": "gh"}); err != nil {
		t.Fatal(err)
	}
	if err := s.DB.Update(func(tx *bolt.Tx) error { return tx.Bucket(bucketChatMcpSecrets).Put(forgeTokensKey, []byte("{")) }); err != nil {
		t.Fatal(err)
	}
	if err := s.ChatScenarios.LoadAll(); err != nil || s.ChatScenarios.ForgeTokenDefault("github.com") != "" {
		t.Errorf("a corrupt map: %v", err)
	}
}

// The repository of a collection over REST: set, kept by an update that
// does not name it, removed by an empty one, refused when wrong.
func TestCollectionConfigRepositoryREST(t *testing.T) {
	s := newBackupTestServer(t)
	put := func(body string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		s.handleCollectionConfig(w, httptest.NewRequest(http.MethodPut, "/v1/collection-config", strings.NewReader(body)))
		return w
	}
	get := func() string {
		w := httptest.NewRecorder()
		s.handleCollectionConfig(w, httptest.NewRequest(http.MethodGet, "/v1/collection-config?collection=code", nil))
		return w.Body.String()
	}
	if w := put(`{"collection":"code","repository":{"url":"https://github.com/acme/shop"}}`); w.Code != 200 {
		t.Fatalf("set: %d %s", w.Code, w.Body)
	}
	if w := put(`{"collection":"code","description":"the code"}`); w.Code != 200 || !strings.Contains(get(), `"url":"https://github.com/acme/shop"`) {
		t.Errorf("an update without it dropped it: %s", get())
	}
	for _, body := range []string{`{"collection":"code","repository":{"url":"ftp://x/a/b"}}`,
		`{"collection":"code","repository":{"url":"https://github.com/a/b","tokenSecretRef":"missing"}}`} {
		if w := put(body); w.Code != http.StatusBadRequest {
			t.Errorf("%s: %d", body, w.Code)
		}
	}
	if w := put(`{"collection":"code","repository":{}}`); w.Code != 200 || strings.Contains(get(), "repository") {
		t.Errorf("an empty one did not remove it: %s", get())
	}
}
