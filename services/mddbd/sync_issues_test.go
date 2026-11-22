package main

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"mddb/internal/automationlog"
)

// issueCounts are the counts of a run without what an issues run adds.
func issueCounts(c automationlog.SyncCounts) automationlog.SyncCounts {
	c.Cursor, c.Pages, c.Fingerprint = "", nil, ""
	return c
}

// issueDoc is a mirrored document; the test fails when it is missing.
func issueDoc(t *testing.T, s *Server, key string) (string, map[string][]string) {
	t.Helper()
	d, err := s.loadDocByRef("repo-issues", key, "en")
	if err != nil {
		t.Fatalf("%s: %v", key, err)
	}
	return d.ContentMD, d.Meta
}

// seedIssues puts an open bug with three comments and a merged pull
// request that closes it.
func seedIssues(f *fakeIssues) {
	f.put(&fakeItem{kind: issueKindIssue, number: 1, title: "Discount applied twice", state: issueStateOpen,
		body:   "See `internal/cart/discount.go` and README.md, fixed by #2 maybe; https://example.com/a.go is a link.",
		labels: []string{"bug"}, updated: "2026-03-01T10:00:00Z",
		comments: []fakeComment{{author: "carol", body: "Same here.", created: "2026-03-01T09:00:00Z"},
			{author: "dave", body: "Bisected.", created: "2026-03-01T09:30:00Z"},
			{author: "erin", body: "Ok.", created: "2026-03-01T09:45:00Z"}}})
	f.put(&fakeItem{kind: issueKindPull, number: 2, title: "Fix the discount", state: issueStateClosed, merged: true,
		body: "Closes #1. Also " + f.repoURL() + "/issues/7 and " + f.repoURL() + "/pull/9.", updated: "2026-03-02T10:00:00Z"})
}

// INT-023's acceptance on GitHub's shapes: the first run writes one document
// per item with its comments and meta; a second run without changes writes
// nothing with one conditional request; a change is read incrementally.
func TestIssuesSyncGitHubFirstIncrementalNotModified(t *testing.T) {
	s := newBackupTestServer(t)
	f := newFakeIssues(t, forgeGitHub)
	seedIssues(f)
	rule := syncRule(t, s, issuesAction(f))

	out, err := runSync(t, s, rule)
	if err != nil || issueCounts(out.counts) != (automationlog.SyncCounts{Added: 2}) {
		t.Fatalf("first run: %+v, %v", out, err)
	}
	body, meta := issueDoc(t, s, "issue-1")
	for _, want := range []string{"# Discount applied twice\n", "## Comment by carol on 2026-03-01", "## Comment by erin on 2026-03-01", "Bisected."} {
		if !strings.Contains(body, want) {
			t.Errorf("issue-1 lacks %q:\n%s", want, body)
		}
	}
	for k, want := range map[string]string{"state": "open", metaKind: "issue", "number": "1", "author": "alice",
		"comments": "3", "labels": "bug", "assignees": "bob", "url": f.repoURL() + "/issues/1", syncMetaRule: rule.ID} {
		if got := metaFirst(meta, k); got != want {
			t.Errorf("issue-1 meta %s = %q, want %q", k, got, want)
		}
	}
	if !slices.Equal(meta["paths"], []string{"README.md", "internal/cart/discount.go"}) || !slices.Equal(meta["linkedIssues"], []string{"2"}) {
		t.Errorf("issue-1 paths and links: %v %v", meta["paths"], meta["linkedIssues"])
	}
	_, pull := issueDoc(t, s, "pull-2")
	if metaFirst(pull, "state") != "closed" || metaFirst(pull, "stateReason") != "merged" || metaFirst(pull, metaKind) != "pull" ||
		!slices.Equal(pull["linkedIssues"], []string{"1", "7"}) || !slices.Equal(pull["linkedPulls"], []string{"9"}) ||
		metaFirst(pull, "closedAt") == "" {
		t.Errorf("pull-2 meta: %v", pull)
	}
	if ids := s.getDocIDsByMeta("repo-issues", map[string][]string{"state": {"open"}, "labels": {"bug"}}); len(ids) != 1 {
		t.Errorf("filter state open, label bug: %v", ids)
	}

	// The second run lists from the cursor: the newest item again, written
	// from the same updatedAt, so nothing is written; the third sends the
	// page's ETag back and is answered 304.
	f.seen()
	out, err = runSync(t, s, rule)
	reqs, _ := f.seen()
	if err != nil || issueCounts(out.counts) != (automationlog.SyncCounts{Unchanged: 1}) || len(reqs) != 1 ||
		!strings.Contains(reqs[0], "since=2026-03-02T09%3A59%3A59Z") {
		t.Fatalf("an unchanged run: %+v, %v, %v", out, err, reqs)
	}
	out, err = runSync(t, s, rule)
	reqs, notMod := f.seen()
	if err != nil || issueCounts(out.counts) != (automationlog.SyncCounts{}) || len(reqs) != 1 || notMod != 1 {
		t.Fatalf("a run answered 304: %+v, %v, %v, %d 304s", out, err, reqs, notMod)
	}
	if res := s.lastRunResult(&rule); res.Status != "success" {
		t.Errorf("a 304 run: %+v", res)
	}

	f.set(func(f *fakeIssues) {
		it := f.items["issue-1"]
		it.updated, it.labels = "2026-03-05T10:00:00Z", []string{"bug", "p1"}
		it.comments = append(it.comments, fakeComment{author: "frank", body: "Fixed in 2.", created: "2026-03-05T09:00:00Z"})
	})
	out, err = runSync(t, s, rule)
	if err != nil || issueCounts(out.counts) != (automationlog.SyncCounts{Updated: 1, Unchanged: 1}) {
		t.Fatalf("a changed issue: %+v, %v", out, err)
	}
	if body, meta := issueDoc(t, s, "issue-1"); !strings.Contains(body, "## Comment by frank") || !slices.Equal(meta["labels"], []string{"bug", "p1"}) {
		t.Errorf("the change was not written: %s %v", body, meta)
	}
}

// Pages are followed by their Link header, and the token goes to the forge.
func TestIssuesSyncPaginationAndToken(t *testing.T) {
	s, _ := runNowServer(t)
	storeSecret(t, s, "forge-tok", "s3cret")
	f := newFakeIssues(t, forgeGitea)
	seedIssues(f)
	f.put(&fakeItem{kind: issueKindIssue, number: 3, title: "Third", state: issueStateOpen, updated: "2026-03-03T10:00:00Z"})
	f.perPage = 1
	a := issuesAction(f)
	a.TokenSecretRef = "forge-tok"
	rule := syncRule(t, s, a)
	out, err := runSync(t, s, rule)
	if err != nil || out.counts.Added != 3 {
		t.Fatalf("paginated run: %+v, %v", out, err)
	}
	for _, h := range f.auth {
		if h != "token s3cret" {
			t.Fatalf("an Authorization header: %q", h)
		}
	}
	if w := getAutomationRule(s, rule.ID); strings.Contains(w, "s3cret") {
		t.Error("the rule shows the token")
	}
}

// A rule limited to one kind and one state mirrors only those; Gitea asks
// for the kind, and a cut listing is reported.
func TestIssuesSyncIncludeStateAndGiteaCut(t *testing.T) {
	s := newBackupTestServer(t)
	f := newFakeIssues(t, forgeGitea)
	seedIssues(f)
	a := issuesAction(f)
	a.Include, a.State = []string{includeIssues}, issueStateOpen
	rule := syncRule(t, s, a)
	if out, err := runSync(t, s, rule); err != nil || out.counts.Added != 1 {
		t.Fatalf("issues only: %+v, %v", out, err)
	}
	if reqs, _ := f.seen(); !strings.Contains(reqs[0], "type=issues") {
		t.Errorf("gitea listing: %v", reqs)
	}

	f.put(&fakeItem{kind: issueKindIssue, number: 5, title: "Five", state: issueStateOpen, updated: "2026-03-06T10:00:00Z"})
	f.put(&fakeItem{kind: issueKindIssue, number: 6, title: "Six", state: issueStateOpen, updated: "2026-03-07T10:00:00Z"})
	a2 := issuesAction(f)
	a2.Collection, a2.MaxDocuments = "repo-issues-2", 2
	cut := syncRule(t, s, a2)
	if _, err := runSync(t, s, cut); err == nil || !strings.Contains(err.Error(), "maxDocuments") {
		t.Errorf("a cut Gitea listing: %v", err)
	}
}

// GitLab's shapes: issues and merge requests listed apart, notes without the
// system ones, a merged request closed as merged, !n a merge request.
func TestIssuesSyncGitLab(t *testing.T) {
	s := newBackupTestServer(t)
	f := newFakeIssues(t, forgeGitLab)
	f.put(&fakeItem{kind: issueKindIssue, number: 1, title: "Crash", state: issueStateOpen, body: "Fixed by !1",
		labels: []string{"bug"}, updated: "2026-03-01T10:00:00.000Z",
		comments: []fakeComment{{author: "gitlab", body: "added label", created: "2026-03-01T09:00:00Z", system: true},
			{author: "carol", body: "Seen.", created: "2026-03-01T09:30:00Z"}}})
	f.put(&fakeItem{kind: issueKindPull, number: 1, title: "Fix crash", state: issueStateClosed, merged: true, updated: "2026-03-02T10:00:00Z"})
	rule := syncRule(t, s, issuesAction(f))
	if out, err := runSync(t, s, rule); err != nil || out.counts.Added != 2 {
		t.Fatalf("gitlab run: %+v, %v", out, err)
	}
	body, meta := issueDoc(t, s, "issue-1")
	if strings.Contains(body, "added label") || !strings.Contains(body, "## Comment by carol") ||
		!slices.Equal(meta["linkedPulls"], []string{"1"}) || metaFirst(meta, "milestone") != "v1" || metaFirst(meta, "updatedAt") != "2026-03-01T10:00:00Z" {
		t.Errorf("gitlab issue: %s %v", body, meta)
	}
	if _, mr := issueDoc(t, s, "pull-1"); metaFirst(mr, "state") != "closed" || metaFirst(mr, "stateReason") != "merged" {
		t.Errorf("gitlab merge request: %v", mr)
	}
}

// A deleted item goes only with prune, which lists everything; the rate
// limit ends a run with its reset time and deletes nothing.
func TestIssuesSyncPruneAndRateLimit(t *testing.T) {
	s := newBackupTestServer(t)
	f := newFakeIssues(t, forgeGitHub)
	seedIssues(f)
	keep := syncRule(t, s, issuesAction(f))
	a := issuesAction(f)
	a.Collection, a.Prune = "repo-pruned", true
	pruned := syncRule(t, s, a)
	for _, r := range []AutomationRule{keep, pruned} {
		if _, err := runSync(t, s, r); err != nil {
			t.Fatal(err)
		}
	}

	f.set(func(f *fakeIssues) { f.limited = true })
	_, err := runSync(t, s, pruned)
	if !errors.Is(err, errSyncRateLimited) || !strings.Contains(err.Error(), "resets at 2026-11-18T") {
		t.Errorf("a rate-limited run: %v", err)
	}
	f.set(func(f *fakeIssues) { f.limited = false })

	f.remove("pull-2")
	out, err := runSync(t, s, pruned)
	if err != nil || out.counts.Deleted != 1 {
		t.Errorf("prune: %+v, %v", out, err)
	}
	if reqs, notMod := f.seen(); notMod != 0 || strings.Contains(strings.Join(reqs, " "), "since=") {
		t.Errorf("a prune run listed incrementally: %v", reqs)
	}
	if out, err := runSync(t, s, keep); err != nil || out.counts.Deleted != 0 {
		t.Errorf("without prune: %+v, %v", out, err)
	}
	if _, err := s.loadDocByRef("repo-issues", "pull-2", "en"); err != nil {
		t.Errorf("without prune the document went: %v", err)
	}
}

// A rate limit met while reading comments fails the item, and with prune
// nothing is pruned.
func TestIssuesSyncRateLimitOnComments(t *testing.T) {
	s := newBackupTestServer(t)
	f := newFakeIssues(t, forgeGitHub)
	seedIssues(f)
	a := issuesAction(f)
	a.Prune = true
	rule := syncRule(t, s, a)
	g, err := s.newIssuesSyncSource(rule, nil)
	if err != nil {
		t.Fatal(err)
	}
	g.limited.Store(true)
	if _, err := g.readComments(t.Context(), issueItem{kind: issueKindIssue, number: 1}); !errors.Is(err, errSyncRateLimited) {
		t.Errorf("a limited source asked again: %v", err)
	}
	r := &syncRun{server: s, rule: rule, action: rule.Sync, job: &BulkIngestJob{}}
	r.failed("x", errSyncRateLimited)
	if !r.limited {
		t.Error("the run did not note the limit")
	}
}

// A refresh of one item reads that item alone; one the forge no longer has
// is deleted with prune and left without.
func TestIssuesSyncRefreshOne(t *testing.T) {
	s := newBackupTestServer(t)
	f := newFakeIssues(t, forgeGitHub)
	seedIssues(f)
	a := issuesAction(f)
	a.Prune = true
	rule := syncRule(t, s, a)
	if _, err := runSync(t, s, rule); err != nil {
		t.Fatal(err)
	}
	f.seen()
	f.set(func(f *fakeIssues) { f.items["issue-1"].comments[0].body = "Edited." }) // updatedAt unmoved
	out, err := s.runSync(rule, timeNow(), &issueTarget{number: 1})
	reqs, _ := f.seen()
	if err != nil || out.counts.Updated != 1 || len(reqs) != 3 || !strings.HasSuffix(reqs[0], "/issues/1") {
		t.Fatalf("refresh one: %+v, %v, %v", out, err, reqs)
	}
	if body, _ := issueDoc(t, s, "issue-1"); !strings.Contains(body, "Edited.") {
		t.Error("the edited comment was not written")
	}
	f.remove("pull-2")
	if out, err := s.runSync(rule, timeNow(), &issueTarget{kind: issueKindPull, number: 2}); err != nil || out.counts.Deleted != 1 {
		t.Errorf("a gone item with prune: %+v, %v", out, err)
	}
	if out, err := s.runSync(rule, timeNow(), &issueTarget{number: 99}); err != nil || out.counts.Unchanged != 2 {
		t.Errorf("an item never mirrored: %+v, %v", out, err)
	}
	if out, err := runSync(t, s, rule); err != nil || out.counts.Deleted != 0 {
		t.Errorf("the run after refreshes: %+v, %v", out, err)
	}
}

// GitLab numbers issues and merge requests apart: a refresh by number reads
// both.
func TestIssuesSyncRefreshGitLab(t *testing.T) {
	s := newBackupTestServer(t)
	f := newFakeIssues(t, forgeGitLab)
	f.put(&fakeItem{kind: issueKindIssue, number: 4, title: "Four", state: issueStateOpen, updated: "2026-03-01T10:00:00Z"})
	a := issuesAction(f)
	rule := syncRule(t, s, a)
	out, err := s.runSync(rule, timeNow(), &issueTarget{number: 4})
	if err != nil || out.counts.Added != 1 || out.counts.Unchanged != 1 {
		t.Fatalf("gitlab refresh: %+v, %v", out, err)
	}
	f.set(func(f *fakeIssues) { f.status = 500 })
	if _, err := s.runSync(rule, timeNow(), &issueTarget{number: 4}); err == nil {
		t.Error("a forge error was not reported")
	}
}
