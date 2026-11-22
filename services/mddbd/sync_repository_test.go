package main

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// setRepository stores a collection's repository.
func setRepository(t *testing.T, s *Server, collection string, r *CollectionRepository) {
	t.Helper()
	if err := s.CollectionManager.Set(collection, &CollectionConfig{Repository: r}); err != nil {
		t.Fatal(err)
	}
}

// INT-023: a rule without a url reads the repository of the collection
// repositoryFrom names, else its own collection's; with neither it is
// refused, saying where one is set.
func TestSyncRepositoryResolution(t *testing.T) {
	s := newBackupTestServer(t)
	f := newFakeIssues(t, forgeGitHub)
	seedIssues(f)
	a := issuesAction(f)
	a.URL, a.Forge = "", ""
	if _, err := s.AutomationManager.Create(AutomationRule{Type: "cron", Name: "x", Schedule: "0 * * * *", Sync: a}); err == nil ||
		!strings.Contains(err.Error(), "PUT /v1/collection-config") {
		t.Fatalf("no repository anywhere: %v", err)
	}
	a.RepositoryFrom = "code"
	if err := s.errNoRepository(a); !strings.Contains(err.Error(), `"code" (repositoryFrom)`) {
		t.Errorf("the message: %v", err)
	}
	setRepository(t, s, "code", &CollectionRepository{URL: f.repoURL(), Forge: forgeGitHub})
	rule := syncRule(t, s, a)
	if out, err := runSync(t, s, rule); err != nil || out.counts.Added != 2 {
		t.Fatalf("a rule reading the code collection's repository: %+v, %v", out, err)
	}
	if !strings.Contains(rule.Sync.describe(), "repository of code") {
		t.Errorf("describe: %s", rule.Sync.describe())
	}

	b := issuesAction(f)
	b.URL, b.Forge, b.Collection = "", "", "own"
	setRepository(t, s, "own", &CollectionRepository{URL: f.repoURL(), Forge: forgeGitHub})
	if got, err := s.resolvedSync(b); err != nil || got.URL != f.repoURL() {
		t.Errorf("its own collection's repository: %+v, %v", got, err)
	}
	setRepository(t, s, "code", &CollectionRepository{}) // {} removes it
	if cfg, _ := s.CollectionManager.Get("code"); cfg.Repository != nil {
		t.Error("an empty repository was kept")
	}
	if _, err := runSync(t, s, rule); err == nil || !strings.Contains(err.Error(), "needs a repository") {
		t.Errorf("a repository removed: %v", err)
	}
	if (&AutomationManager{}).checkSyncRepository(a) == nil {
		t.Error("a manager without a server took a rule without a url")
	}
	if got, _ := s.resolvedSync(&SyncAction{Source: SyncSourceS3}); got.Source != SyncSourceS3 {
		t.Error("another source")
	}
	bad := &SyncAction{Source: SyncSourceGit, URL: "https://unknown.example/a/b", Collection: "c"}
	if _, err := s.resolvedSync(bad); err == nil {
		t.Error("an unknown forge")
	}
}

// The token: the rule's, then the collection's — only with that
// collection's url — then the default of the url's host, exactly.
func TestSyncRepositoryTokens(t *testing.T) {
	s := newBackupTestServer(t)
	f := newFakeIssues(t, forgeGitHub)
	host := strings.TrimPrefix(f.URL, "https://")
	storeSecret(t, s, "rule-tok", "r")
	storeSecret(t, s, "coll-tok", "c")
	storeSecret(t, s, "default-tok", "d")
	if err := s.ChatScenarios.SetForgeTokenDefaults(map[string]string{host: "default-tok", "other.example": "rule-tok"}); err != nil {
		t.Fatal(err)
	}
	setRepository(t, s, "code", &CollectionRepository{URL: f.repoURL(), Forge: forgeGitHub, TokenSecretRef: "coll-tok"})
	cases := []struct {
		name string
		a    SyncAction
		want string
	}{
		{"the rule's", SyncAction{URL: f.repoURL(), GitSyncFields: GitSyncFields{TokenSecretRef: "rule-tok"}}, "rule-tok"},
		{"the collection's", SyncAction{GitSyncFields: GitSyncFields{RepositoryFrom: "code"}}, "coll-tok"},
		{"the rule's over the collection's", SyncAction{GitSyncFields: GitSyncFields{RepositoryFrom: "code", TokenSecretRef: "rule-tok"}}, "rule-tok"},
		{"own url: the host's default, not the collection's", SyncAction{URL: f.repoURL(), GitSyncFields: GitSyncFields{RepositoryFrom: "code"}}, "default-tok"},
		{"another host: no default", SyncAction{URL: "https://gitlab.com/a/b"}, ""},
	}
	for _, c := range cases {
		a := c.a
		a.Source, a.Collection, a.Forge = SyncSourceIssues, "repo-issues", cmpForge(a.URL, f)
		got, err := s.resolvedSync(&a)
		if err != nil || got.TokenSecretRef != c.want {
			t.Errorf("%s: %q, %v", c.name, got.TokenSecretRef, err)
		}
	}
}

// cmpForge is the fake's forge for its own url.
func cmpForge(url string, f *fakeIssues) string {
	if strings.HasPrefix(url, f.URL) {
		return forgeGitHub
	}
	return ""
}

// A token reaches only the forge's hosts: a request to another host, and a
// redirect off the host, carry no Authorization.
func TestForgeTokenStaysOnItsHost(t *testing.T) {
	var seen []string
	other := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Header.Get("Authorization"))
		_, _ = w.Write([]byte("[]"))
	}))
	defer other.Close()
	f := newFakeIssues(t, forgeGitHub)
	redirect := httptest.NewTLSServer(http.RedirectHandler(other.URL+"/x", http.StatusFound))
	defer redirect.Close()
	repo, _ := parseGitRepo(redirect.URL+"/acme/docs", forgeGitHub)
	g := &issuesSyncSource{repo: repo, token: "tok"}
	if h := repo.header(other.URL+"/x", "tok", false); h.Get("Authorization") != "" {
		t.Error("a header for another host carries the token")
	}
	if h := repo.header(redirect.URL+"/x", "tok", false); h.Get("Authorization") != "Bearer tok" {
		t.Error("the forge's own host lost the token")
	}
	if _, err := g.get(t.Context(), redirect.URL+"/api/v3/repos/acme/docs/issues", ""); err != nil {
		t.Fatal(err)
	}
	if len(seen) != 1 || seen[0] != "" {
		t.Errorf("a redirect off the host carried %q", seen)
	}
	_ = f
	gh, _ := parseGitRepo("https://github.com/a/b", "")
	for u, want := range map[string]bool{"https://api.github.com/x": true, "https://codeload.github.com/x": true,
		"https://github.com.evil.example/x": false, "https://evil.example/x": false, "%zz": false} {
		if gh.tokenHost(u) != want {
			t.Errorf("tokenHost(%s) != %v", u, want)
		}
	}
	if err := defaultRedirectPolicy(make([]*http.Request, maxForgeRedirects)); err == nil {
		t.Error("too many redirects")
	}
	if defaultRedirectPolicy(nil) != nil {
		t.Error("one redirect refused")
	}
}

// A secret a collection's repository or a default names cannot be deleted.
func TestSyncRepositorySecretsInUse(t *testing.T) {
	s := newBackupTestServer(t)
	storeSecret(t, s, "coll-tok", "c")
	storeSecret(t, s, "default-tok", "d")
	setRepository(t, s, "code", &CollectionRepository{URL: "https://github.com/a/b", TokenSecretRef: "coll-tok"})
	if err := s.ChatScenarios.SetForgeTokenDefaults(map[string]string{"github.com": "default-tok"}); err != nil {
		t.Fatal(err)
	}
	if used, err := s.DeleteChatMcpSecret("coll-tok"); !errors.Is(err, errChatMcpSecretInUse) || used[0] != "collection:code" {
		t.Errorf("a collection's secret: %v %v", used, err)
	}
	if used, err := s.DeleteChatMcpSecret("default-tok"); !errors.Is(err, errChatMcpSecretInUse) || used[0] != "default:github.com" {
		t.Errorf("a default's secret: %v %v", used, err)
	}
	if (&Server{}).collectionsUsingSecret("x") != nil {
		t.Error("no collection manager")
	}
	if err := s.checkRepositorySecret(&CollectionRepository{URL: "https://github.com/a/b", TokenSecretRef: "nope"}); err == nil {
		t.Error("an unknown secret")
	}
}

// A repository block is validated: an https forge url, a secret name, and
// nothing but a url can be removed.
func TestCollectionRepositoryNormalize(t *testing.T) {
	for name, r := range map[string]*CollectionRepository{
		"http":            {URL: "http://github.com/a/b"},
		"unknown host":    {URL: "https://example.com/a/b"},
		"token, no url":   {TokenSecretRef: "x"},
		"bad secret name": {URL: "https://github.com/a/b", TokenSecretRef: "a b"},
	} {
		if r.normalize() == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	ok := &CollectionRepository{URL: " https://GitHub.com/a/b/ ", Forge: " ", TokenSecretRef: " t "}
	if err := ok.normalize(); err != nil || ok.URL != "https://GitHub.com/a/b" || ok.TokenSecretRef != "t" {
		t.Errorf("a good block: %+v %v", ok, err)
	}
	if (*CollectionRepository)(nil).normalize() != nil || (&CollectionRepository{}).normalize() != nil {
		t.Error("nil and empty blocks")
	}
}
