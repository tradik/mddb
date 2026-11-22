package main

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"mddb/proto"
)

// The edges of an issues source: what it cannot connect to, what it leaves
// out, and the answers it cannot read.
func TestIssuesSyncEdges(t *testing.T) {
	s := newBackupTestServer(t)
	f := newFakeIssues(t, forgeGitHub)
	seedIssues(f)

	bad := AutomationRule{Sync: &SyncAction{Source: SyncSourceIssues, URL: "https://unknown.example/a/b"}}
	if _, err := s.newIssuesSyncSource(bad, nil); err == nil {
		t.Error("an unknown forge")
	}
	tok := AutomationRule{Sync: issuesAction(f)}
	tok.Sync.TokenSecretRef = "missing"
	if _, err := s.newIssuesSyncSource(tok, nil); err == nil {
		t.Error("a secret that cannot be opened")
	}

	// Issues only: GitHub lists the pull request too, and it is left out; a
	// refresh of it reads nothing.
	a := issuesAction(f)
	a.Include = []string{includeIssues}
	only := syncRule(t, s, a)
	if out, err := runSync(t, s, only); err != nil || out.counts.Added != 1 {
		t.Fatalf("issues only: %+v, %v", out, err)
	}
	if out, err := s.runSync(only, time.Now(), &issueTarget{number: 2}); err != nil || out.counts.Added+out.counts.Updated != 0 {
		t.Errorf("a refresh of an excluded pull: %+v, %v", out, err)
	}
	if out, err := s.runSync(only, time.Now(), &issueTarget{kind: issueKindPull, number: 2}); err != nil || issueCounts(out.counts) != (syncOutcome{}).counts {
		t.Errorf("a refresh of an excluded kind: %+v, %v", out, err)
	}

	// The since of a rule that lists everything.
	g := &issuesSyncSource{action: &SyncAction{Prune: true, IssuesSyncFields: IssuesSyncFields{Since: "2026-02-01"}}}
	if got := g.since(); got != "2026-02-01T00:00:00Z" {
		t.Errorf("since: %s", got)
	}

	// Answers that are not JSON, a listing that fails, a page too large.
	for path, want := range map[string]string{"/issues/1/comments": "reading the comments", "/issues/1": "reading item 1"} {
		f.set(func(f *fakeIssues) { f.garbagePath = path })
		out, err := s.runSync(only, time.Now(), &issueTarget{number: 1})
		if err == nil || !strings.Contains(err.Error()+strings.Join(out.errors, " "), want) {
			t.Errorf("garbage on %s: %v %v", path, err, out.errors)
		}
	}
	f.set(func(f *fakeIssues) { f.garbagePath = "/issues" })
	if _, err := runSync(t, s, only); err == nil || !strings.Contains(err.Error(), "listing") {
		t.Errorf("a listing that is not JSON: %v", err)
	}
	f.set(func(f *fakeIssues) { f.garbagePath, f.status = "", 500 })
	if _, err := runSync(t, s, only); err == nil || !strings.Contains(err.Error(), "listing") {
		t.Errorf("a listing that fails: %v", err)
	}
	f.set(func(f *fakeIssues) { f.status = 0 })
	maxIssuesPageBytes = 10
	t.Cleanup(func() { maxIssuesPageBytes = 32 << 20 })
	repo, _ := parseGitRepo(f.repoURL(), forgeGitHub)
	if _, err := (&issuesSyncSource{repo: repo}).get(t.Context(), f.URL+"/api/v3/repos/acme/docs/issues/1", ""); err == nil ||
		!strings.Contains(err.Error(), "larger than") {
		t.Errorf("a page too large: %v", err)
	}
}

// The rate limit met on an item's comments fails the item, asks nothing
// more, and a prune is withheld.
func TestIssuesSyncRateLimitedComments(t *testing.T) {
	s := newBackupTestServer(t)
	f := newFakeIssues(t, forgeGitHub)
	seedIssues(f)
	a := issuesAction(f)
	a.Prune = true
	rule := syncRule(t, s, a)
	f.set(func(f *fakeIssues) { f.limitPath = "/comments" })
	_, err := runSync(t, s, rule)
	if err == nil || !strings.Contains(err.Error(), "nothing pruned") {
		t.Errorf("a prune after the rate limit: %v", err)
	}
	if _, err := s.loadDocByRef("repo-issues", "pull-2", "en"); err != nil {
		t.Errorf("the item without comments was not written: %v", err)
	}
}

// A second Gitea run lists from the cursor.
func TestIssuesSyncGiteaIncremental(t *testing.T) {
	s := newBackupTestServer(t)
	f := newFakeIssues(t, forgeGitea)
	seedIssues(f)
	rule := syncRule(t, s, issuesAction(f))
	for range 2 {
		if _, err := runSync(t, s, rule); err != nil {
			t.Fatal(err)
		}
	}
	if reqs, _ := f.seen(); !strings.Contains(reqs[len(reqs)-1], "since=") {
		t.Errorf("the second Gitea run: %v", reqs)
	}
}

// A rule refreshed but never run is throttled with when it was refreshed.
func TestSyncRefreshThrottledWithoutRuns(t *testing.T) {
	resetRefreshes(t)
	s := newBackupTestServer(t)
	f := newFakeIssues(t, forgeGitHub)
	rule := syncRule(t, s, issuesAction(f))
	at := time.Now().Add(-time.Second)
	syncRefreshes.Store(rule.ID, at)
	res, err := s.syncRefresh("repo-issues", 1)
	if !errors.Is(err, errSyncRefreshThrottled) || res.Runs[0].LastRunAt != at.Unix() || res.Runs[0].RetryAfter != 29 {
		t.Errorf("throttled: %+v, %v", res, err)
	}
}

// Writes that fail, and a redirect policy of a client without one.
func TestForgeTokensAndClientEdges(t *testing.T) {
	s := newBackupTestServer(t)
	unloaded := NewChatScenarioManager(s.DB) // no buckets yet: writes fail
	if err := unloaded.SetForgeTokenDefaults(map[string]string{}); err == nil {
		t.Error("a write without the bucket")
	}
	prev := gitSyncClient
	gitSyncClient = func() *http.Client { return &http.Client{} }
	t.Cleanup(func() { gitSyncClient = prev })
	req, _ := http.NewRequest(http.MethodGet, "https://a.example/x", nil)
	via, _ := http.NewRequest(http.MethodGet, "https://a.example/y", nil)
	if err := forgeClient().CheckRedirect(req, []*http.Request{via}); err != nil {
		t.Errorf("one redirect: %v", err)
	}
}

// A repository refused over gRPC.
func TestGRPCSetCollectionConfigRepository(t *testing.T) {
	s := newBackupTestServer(t)
	g := &GRPCServer{server: s}
	_, err := g.SetCollectionConfig(context.Background(), &proto.SetCollectionConfigRequest{Collection: "code",
		Repository: &proto.CollectionRepositoryProto{Url: "ftp://x/a/b"}})
	if err == nil {
		t.Error("a bad repository over gRPC")
	}
	if _, err := g.SetCollectionConfig(context.Background(), &proto.SetCollectionConfigRequest{Collection: "code",
		Repository: &proto.CollectionRepositoryProto{Url: "https://github.com/a/b"}}); err != nil {
		t.Fatal(err)
	}
	resp, err := g.GetCollectionConfig(context.Background(), &proto.GetCollectionConfigRequest{Collection: "code"})
	if err != nil || resp.GetConfig().GetRepository().GetUrl() != "https://github.com/a/b" {
		t.Errorf("the repository over gRPC: %v %v", resp, err)
	}
}
