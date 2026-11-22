package main

import (
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	json "mddb/internal/jsonx"
	"mddb/proto"
)

// The validation of an issues sync's fields.
func TestIssuesSyncCheck(t *testing.T) {
	ok := &SyncAction{Source: SyncSourceIssues, URL: "https://github.com/a/b", Collection: "c", Lang: "en",
		GitSyncFields: GitSyncFields{Include: []string{" Pulls ", "issues", "pulls"}}, IssuesSyncFields: IssuesSyncFields{Since: "2026-01-02"}}
	if err := ok.normalize(); err != nil || ok.State != issueStateAll || !slices.Equal(ok.Include, []string{"pulls", "issues"}) {
		t.Fatalf("a good sync: %+v %v", ok, err)
	}
	def := &SyncAction{Source: SyncSourceIssues, URL: "https://github.com/a/b", Collection: "c", Lang: "en"}
	if err := def.normalize(); err != nil || !slices.Equal(def.Include, []string{includeIssues}) || !def.withComments() {
		t.Errorf("defaults: %+v %v", def, err)
	}
	off := false
	def.Comments = &off
	if def.withComments() || redactedSync(def).Comments == def.Comments {
		t.Error("comments off, or a redacted copy sharing the flag")
	}
	for name, a := range map[string]*SyncAction{
		"state":   {IssuesSyncFields: IssuesSyncFields{State: "merged"}},
		"since":   {IssuesSyncFields: IssuesSyncFields{Since: "yesterday"}},
		"include": {GitSyncFields: GitSyncFields{Include: []string{"discussions"}}},
		"url":     {URL: "http://github.com/a/b"},
		"secret":  {GitSyncFields: GitSyncFields{HookSecretRef: "a b"}},
	} {
		a.Source, a.Collection, a.Lang = SyncSourceIssues, "c", "en"
		if a.URL == "" {
			a.URL = "https://github.com/a/b"
		}
		if a.normalize() == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if s, _ := parseIssuesSince("2026-01-02T03:04:05+02:00"); !s.Equal(time.Date(2026, 1, 2, 1, 4, 5, 0, time.UTC)) {
		t.Errorf("an RFC 3339 since: %v", s)
	}
	if got := (&SyncAction{Source: SyncSourceIssues, URL: "u"}).describe(); got != "u issues" {
		t.Errorf("describe: %s", got)
	}
}

// What the forges' payloads say about an item.
func TestIssueTargetFromHook(t *testing.T) {
	cases := map[string]issueTarget{
		`{"action":"opened","issue":{"number":3,"pull_request":null}}`:            {issueKindIssue, 3},
		`{"action":"created","issue":{"number":4,"pull_request":{"url":"x"}}}`:    {issueKindPull, 4},
		`{"action":"created","is_pull":true,"issue":{"number":5}}`:                {issueKindPull, 5},
		`{"action":"edited","pull_request":{"number":6},"number":6}`:              {issueKindPull, 6},
		`{"object_kind":"issue","object_attributes":{"iid":7}}`:                   {issueKindIssue, 7},
		`{"object_kind":"merge_request","object_attributes":{"iid":8}}`:           {issueKindPull, 8},
		`{"object_kind":"note","merge_request":{"iid":9},"object_attributes":{}}`: {issueKindPull, 9},
		`{"object_kind":"note","issue":{"iid":10},"object_attributes":{}}`:        {issueKindIssue, 10},
	}
	for body, want := range cases {
		if got, ok := issueTargetFromHook([]byte(body)); !ok || got != want {
			t.Errorf("%s: %+v %v", body, got, ok)
		}
	}
	for _, body := range []string{`{"ref":"refs/heads/main"}`, `not json`, `{"issue":{"number":0}}`} {
		if _, ok := issueTargetFromHook([]byte(body)); ok {
			t.Errorf("%s: an item", body)
		}
	}
}

// The webhook of an issues sync: a bad signature is refused, an event about
// an issue refreshes that one document, a push runs a pass.
func TestIssuesSyncHook(t *testing.T) {
	s, _ := runNowServer(t)
	storeSecret(t, s, "hook-key", "hook-s3cret")
	f := newFakeIssues(t, forgeGitHub)
	seedIssues(f)
	a := issuesAction(f)
	a.HookSecretRef = "hook-key"
	rule := syncRule(t, s, a)
	body := `{"action":"created","issue":{"number":1}}`
	if w := postHook(s, rule.ID, body, map[string]string{"X-Hub-Signature-256": "sha256=" + hubSignature("nope", body)}); w.Code != http.StatusUnauthorized {
		t.Errorf("a bad signature: %d", w.Code)
	}
	if reqs, _ := f.seen(); len(reqs) != 0 {
		t.Fatalf("a refused hook asked the forge: %v", reqs)
	}
	sig := map[string]string{"X-Hub-Signature-256": "sha256=" + hubSignature("hook-s3cret", body), "X-GitHub-Event": "issue_comment"}
	w := postHook(s, rule.ID, body, sig)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"key":"issue-1"`) || !strings.Contains(w.Body.String(), `"added":1`) {
		t.Fatalf("an issue event: %d %s", w.Code, w.Body)
	}
	if reqs, _ := f.seen(); len(reqs) != 3 || !strings.HasSuffix(reqs[0], "/issues/1") {
		t.Errorf("the event read more than its item: %v", reqs)
	}
	if _, err := s.loadDocByRef("repo-issues", "pull-2", "en"); err == nil {
		t.Error("the event wrote another item")
	}

	syncRuns.Store(rule.ID, true)
	if w := postHook(s, rule.ID, body, sig); w.Code != http.StatusConflict {
		t.Errorf("a run going: %d", w.Code)
	}
	syncRuns.Delete(rule.ID)
	f.set(func(f *fakeIssues) { f.status = 500 })
	if w := postHook(s, rule.ID, body, sig); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"status":"error"`) {
		t.Errorf("a forge error: %d %s", w.Code, w.Body)
	}
	push := `{"ref":"refs/heads/main"}`
	if w := postHook(s, rule.ID, push, map[string]string{"X-Hub-Signature-256": "sha256=" + hubSignature("hook-s3cret", push)}); w.Code != http.StatusAccepted {
		t.Errorf("an event without an item: %d %s", w.Code, w.Body)
	}
	s.CronScheduler.runs.Wait()
	gl := `{"object_kind":"issue","object_attributes":{"iid":1}}`
	f.set(func(f *fakeIssues) { f.status = 0 })
	if w := postHook(s, rule.ID, gl, map[string]string{"X-Gitlab-Token": "hook-s3cret"}); w.Code != http.StatusOK {
		t.Errorf("a GitLab-signed event: %d %s", w.Code, w.Body)
	}
}

// The document: heading, body, a section per comment; links and paths.
func TestRenderIssue(t *testing.T) {
	repo, _ := parseGitRepo("https://gitlab.com/g/r", "")
	it := issueItem{kind: issueKindPull, number: 3, title: "T", body: "", state: "open", url: "u",
		createdAt: "2026-01-01T00:00:00Z", updatedAt: "2026-01-02T00:00:00Z"}
	text := "Fixes #12 and #2, not &#3 or a/#4; see !3 (self) and !5, " + repo.web + "/-/issues/8 and " + repo.web +
		"/-/merge_requests/9 and https://other.example/g/r/-/issues/1. Files: ./cmd/main.go, docs/x.md, CHANGELOG.md, e.g. v2.19, Makefile."
	body, meta := renderIssue(it, []issueComment{{author: "a", createdAt: "bad", body: text}, {author: "b", createdAt: "2026-01-03T00:00:00Z"}}, repo)
	if !strings.HasPrefix(body, "# T\n\n## Comment by a on bad\n") || !strings.Contains(body, "## Comment by b on 2026-01-03\n") {
		t.Errorf("body:\n%s", body)
	}
	if !slices.Equal(meta["linkedIssues"], []string{"2", "8", "12"}) || !slices.Equal(meta["linkedPulls"], []string{"5", "9"}) {
		t.Errorf("links: %v %v", meta["linkedIssues"], meta["linkedPulls"])
	}
	if !slices.Equal(meta["paths"], []string{"CHANGELOG.md", "cmd/main.go", "docs/x.md"}) {
		t.Errorf("paths: %v", meta["paths"])
	}
	if _, ok := meta["labels"]; ok {
		t.Error("an empty list was kept")
	}
	many := ""
	for i := range maxIssuePaths + 5 {
		many += " d/f" + string(rune('a'+i%26)) + strings.Repeat("x", i) + ".go"
	}
	if got := mentionedPaths(many); len(got) != maxIssuePaths {
		t.Errorf("paths are bounded: %d", len(got))
	}
}

// Reading the forges' items.
func TestForgeIssueShapes(t *testing.T) {
	var f forgeIssue
	_ = json.Unmarshal([]byte(`{"number":1,"state":"closed","labels":"weird","closed_at":"2026-01-01T00:00:00+01:00",
		"pull_request":{"merged":true},"milestone":{"title":"m"},"assignees":[{"login":"x"},{"username":"y"}]}`), &f)
	it := f.item("")
	if it.kind != issueKindPull || it.stateReason != "merged" || it.closedAt != "2025-12-31T23:00:00Z" || it.milestone != "m" ||
		len(it.labels) != 0 || !slices.Equal(it.assignees, []string{"x", "y"}) || it.author != "" {
		t.Errorf("an item: %+v", it)
	}
	var l forgeLabels
	_ = json.Unmarshal([]byte(`["a",{"name":"b"},{"color":"red"},3]`), &l)
	if !slices.Equal(l, []string{"a", "b"}) {
		t.Errorf("labels: %v", l)
	}
	if forgeTime("soon") != "soon" {
		t.Error("an unparsable time")
	}
	gl, _ := parseGitRepo("https://gitlab.com/g/r", "")
	if u := gl.commentsURL(issueKindPull, 2); !strings.HasSuffix(u, "/merge_requests/2/notes?sort=asc&order_by=created_at&per_page=100") {
		t.Errorf("gitlab notes: %s", u)
	}
	a := &SyncAction{IssuesSyncFields: IssuesSyncFields{State: issueStateOpen}, GitSyncFields: GitSyncFields{Include: []string{includePulls}}}
	if l := gl.issueListings(a, "2026-01-01T00:00:00Z"); len(l) != 1 || !strings.Contains(l[0].url, "state=opened") || !strings.Contains(l[0].url, "updated_after=") {
		t.Errorf("gitlab listing: %+v", l)
	}
	if q := gitLabListQuery(issueStateClosed, issueKindIssue, ""); !strings.Contains(q, "state=closed") {
		t.Errorf("gitlab closed issues: %s", q)
	}
}

// The forge's answers: rate limits, refusals, Link headers.
func TestIssuesHTTPAnswers(t *testing.T) {
	issuesNow = func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) }
	t.Cleanup(func() { issuesNow = time.Now })
	for h, want := range map[string]string{"Retry-After": "2026-01-01T00:01:00Z", "RateLimit-Reset": "1970-01-01T00:01:00Z", "X-Other": "does not say"} {
		hdr := http.Header{}
		hdr.Set(h, "60")
		if got := rateLimitReset(hdr); !strings.Contains(got, want) {
			t.Errorf("%s: %s", h, got)
		}
	}
	if !rateLimited(http.Header{"Ratelimit-Remaining": {"0"}}) || rateLimited(http.Header{}) {
		t.Error("rateLimited")
	}
	if nextLink(`<https://x/2>; rel="prev", <https://x/3>; rel="next"`) != "https://x/3" || nextLink("") != "" {
		t.Error("nextLink")
	}
	f := newFakeIssues(t, forgeGitHub)
	repo, _ := parseGitRepo(f.repoURL(), forgeGitHub)
	g := &issuesSyncSource{repo: repo}
	for status, want := range map[int]string{401: "needs a token", 403: "needs a token", 429: "rate limit", 502: "502", 404: "gone"} {
		f.set(func(f *fakeIssues) { f.status = status })
		if _, err := g.get(t.Context(), f.URL+"/api/v3/repos/acme/docs/issues", ""); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%d: %v", status, err)
		}
	}
	if _, err := g.get(t.Context(), "https://localhost:1/x", ""); err == nil {
		t.Error("an unreachable forge")
	}
}

// The gRPC shapes of the new fields.
func TestIssuesSyncProto(t *testing.T) {
	on := true
	a := &SyncAction{Source: SyncSourceIssues, GitSyncFields: GitSyncFields{RepositoryFrom: "code"},
		IssuesSyncFields: IssuesSyncFields{State: "open", Since: "2026-01-01", Comments: &on}}
	back := syncActionFromProto(syncActionToProto(a))
	if back.State != "open" || back.Since != "2026-01-01" || back.Comments == nil || !*back.Comments || back.RepositoryFrom != "code" {
		t.Errorf("round trip: %+v", back)
	}
	if repositoryToProto(nil) != nil || repositoryToProto(&CollectionRepository{URL: "u"}).GetUrl() != "u" {
		t.Error("repositoryToProto")
	}
	s := newBackupTestServer(t)
	cfg := &CollectionConfig{}
	if err := s.applyRepositoryProto(cfg, nil); err != nil || cfg.Repository != nil {
		t.Error("nil leaves it")
	}
	if err := s.applyRepositoryProto(cfg, &proto.CollectionRepositoryProto{Url: "http://x"}); err == nil {
		t.Error("a bad url")
	}
	if err := s.applyRepositoryProto(cfg, &proto.CollectionRepositoryProto{Url: "https://github.com/a/b", TokenSecretRef: "none"}); err == nil {
		t.Error("an unknown secret")
	}
	if err := s.applyRepositoryProto(cfg, &proto.CollectionRepositoryProto{Url: "https://github.com/a/b"}); err != nil || cfg.Repository.URL == "" {
		t.Errorf("a repository: %v", err)
	}
}
