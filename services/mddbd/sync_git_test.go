package main

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"mddb/internal/automationlog"
)

// The fixture repository at commit A: Markdown with frontmatter, code, a
// binary file and test data the rule leaves out.
var gitFilesA = map[string]string{
	"docs/API.md":    "---\ntitle: The API\ntags: rest, grpc\n---\n# API\n\nEndpoints.",
	"docs/guide.md":  "# Guide\n\nStart here.",
	"README.md":      "# Project\n",
	"main.go":        "---\npackage main\n",
	"bin.dat":        "\x00\x01\x02",
	"testdata/x.md":  "# fixture",
	"docs/table.csv": "a,b\n1,2\n",
}

// gitCounts drops what a git run adds to the counts, for comparing them.
func gitCounts(c automationlog.SyncCounts) automationlog.SyncCounts {
	c.Ref, c.Commit, c.Fingerprint, c.UpToDate = "", "", "", false
	return c
}

// #421's acceptance: a branch indexes the included files under their paths;
// the same commit again downloads nothing and writes nothing; a new commit
// writes only what changed, adds what is new and prunes what is gone.
func TestGitSyncBranchIncrementalAndPrune(t *testing.T) {
	s := newBackupTestServer(t)
	f := newFakeForge(t, forgeGitea)
	f.commit("refs/heads/main", commitA, gitFilesA)
	a := gitSyncAction(f, "main")
	a.Include, a.Exclude = []string{"*.md", "*.go", "*.csv"}, []string{"testdata/**"}
	rule := syncRule(t, s, a)

	out, err := runSync(t, s, rule)
	if err != nil || gitCounts(out.counts) != (automationlog.SyncCounts{Added: 5}) ||
		out.counts.Ref != "main" || out.counts.Commit != commitA {
		t.Fatalf("first run: %+v, %v", out, err)
	}
	gitCheckDocs(t, s, rule)
	res := s.lastRunResult(&rule)
	if res.Ref != "main" || res.Commit != commitA || res.CommitURL != f.URL+"/acme/docs/commit/"+commitA || res.UpToDate {
		t.Errorf("lastResult: %+v", res)
	}

	time.Sleep(1100 * time.Millisecond) // a write now would move UpdatedAt
	before, _ := s.loadDocByRef("repo", "docs/API.md", "en")
	out, err = runSync(t, s, rule)
	if err != nil || !out.counts.UpToDate || gitCounts(out.counts) != (automationlog.SyncCounts{}) || f.archiveCount() != 1 {
		t.Fatalf("the same commit: %+v, %v, %d archives", out, err, f.archiveCount())
	}
	if d, _ := s.loadDocByRef("repo", "docs/API.md", "en"); d.UpdatedAt != before.UpdatedAt {
		t.Error("an up-to-date run wrote")
	}
	if res := s.lastRunResult(&rule); !res.UpToDate || res.Commit != commitA {
		t.Errorf("lastResult of an up-to-date run: %+v", res)
	}

	filesB := map[string]string{"docs/API.md": gitFilesA["docs/API.md"], "docs/guide.md": "# Guide\n\nRevised.",
		"main.go": gitFilesA["main.go"], "docs/table.csv": gitFilesA["docs/table.csv"], "docs/new.md": "# New"}
	f.commit("refs/heads/main", commitB, filesB)
	out, err = runSync(t, s, rule)
	if err != nil || gitCounts(out.counts) != (automationlog.SyncCounts{Added: 1, Updated: 1, Unchanged: 3, Deleted: 1}) {
		t.Fatalf("a new commit: %+v, %v", out, err)
	}
	if d, _ := s.loadDocByRef("repo", "docs/API.md", "en"); d.UpdatedAt != before.UpdatedAt || metaFirst(d.Meta, syncMetaCommit) != commitA {
		t.Error("an unchanged file was written")
	}
	if d, _ := s.loadDocByRef("repo", "docs/guide.md", "en"); !strings.Contains(d.ContentMD, "Revised") || metaFirst(d.Meta, syncMetaCommit) != commitB {
		t.Errorf("the changed file: %q %v", d.ContentMD, d.Meta)
	}
	if _, err := s.loadDocByRef("repo", "README.md", "en"); !errors.Is(err, errDocNotFound) {
		t.Errorf("the deleted file's document: %v", err)
	}

	// A changed rule reads the archive again, though the commit is the same.
	rule.Sync.Exclude = []string{"testdata/**", "*.csv"}
	if out, err := runSync(t, s, rule); err != nil || out.counts.UpToDate || out.counts.Deleted != 1 || f.archiveCount() != 3 {
		t.Errorf("a changed rule: %+v, %v", out, err)
	}
}

// gitCheckDocs checks the first run's documents: keys are paths, meta is
// what autoindex writes plus the sync's, frontmatter only from Markdown.
func gitCheckDocs(t *testing.T, s *Server, rule AutomationRule) {
	t.Helper()
	api, err := s.loadDocByRef("repo", "docs/API.md", "en")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"title": "The API", "path": "docs/API.md", "extension": ".md", "kind": "doc",
		"size": "57", "gitCommit": commitA, "gitBranch": "main", syncMetaRef: "main", syncMetaCommit: commitA,
		syncMetaRule: rule.ID, syncMetaSource: "docs/API.md", syncMetaHash: gitBlobID([]byte(gitFilesA["docs/API.md"])),
		"modified": "2026-11-01T12:00:00Z"}
	for k, v := range want {
		if got := metaFirst(api.Meta, k); got != v {
			t.Errorf("meta %s = %q, want %q", k, got, v)
		}
	}
	if len(api.Meta["tags"]) != 2 || strings.HasPrefix(api.ContentMD, "---") {
		t.Errorf("frontmatter: %v %q", api.Meta, api.ContentMD)
	}
	code, _ := s.loadDocByRef("repo", "main.go", "en")
	if metaFirst(code.Meta, "kind") != "code" || metaFirst(code.Meta, "language") != "go" || !strings.HasPrefix(code.ContentMD, "---") {
		t.Errorf("code: %v %q", code.Meta, code.ContentMD)
	}
	if csv, _ := s.loadDocByRef("repo", "docs/table.csv", "en"); !strings.Contains(csv.ContentMD, "|") || metaFirst(csv.Meta, "kind") != "doc" {
		t.Errorf("a converted file: %q", csv.ContentMD)
	}
	for _, key := range []string{"bin.dat", "testdata/x.md"} {
		if _, err := s.loadDocByRef("repo", key, "en"); !errors.Is(err, errDocNotFound) {
			t.Errorf("%s was synced", key)
		}
	}
}

// Every way to name a ref, on each forge's API.
func TestGitSyncResolvesRefs(t *testing.T) {
	for _, forge := range []string{forgeGitea, forgeGitLab, forgeGitHub} {
		t.Run(forge, func(t *testing.T) {
			f := newFakeForge(t, forge)
			f.commit("refs/heads/main", commitA, nil)
			f.commit("refs/heads/dev", commitB, nil)
			f.commit("refs/tags/v1.2.0", commitB, nil)
			f.commit("refs/tags/v1.10.0", tagObj, nil)
			f.peeled["refs/tags/v1.10.0"] = commitC
			f.commit("refs/tags/v2.0.0-rc1", commitA, nil)
			f.release = "v1.2.0"
			repo, err := parseGitRepo(f.repoURL(), forge)
			if err != nil {
				t.Fatal(err)
			}
			cases := map[string][2]string{
				"": {"main", commitA}, "dev": {"dev", commitB}, "v1.2.0": {"v1.2.0", commitB},
				"v1.10.0": {"v1.10.0", commitC}, "refs/heads/dev": {"dev", commitB}, commitC: {commitC, commitC},
				gitRefLatestRelease: {"v1.2.0", commitB},
			}
			for ref, want := range cases {
				name, commit, err := resolveGitRef(t.Context(), repo, ref, "")
				if err != nil || name != want[0] || commit != want[1] {
					t.Errorf("%q: %s %s %v", ref, name, commit, err)
				}
			}
			f.release = "" // no release: the highest semver tag, not a pre-release
			if name, commit, err := resolveGitRef(t.Context(), repo, gitRefLatestRelease, ""); name != "v1.10.0" || commit != commitC || err != nil {
				t.Errorf("semver fallback: %s %s %v", name, commit, err)
			}
			if _, _, err := resolveGitRef(t.Context(), repo, "nope", ""); err == nil || !strings.Contains(err.Error(), `no branch or tag "nope"`) {
				t.Errorf("an unknown ref: %v", err)
			}
		})
	}
}

// Without a release or a semver tag, latest-release has nothing to follow;
// a repository that is not there says so.
func TestGitSyncRefFailures(t *testing.T) {
	f := newFakeForge(t, forgeGitea)
	f.commit("refs/heads/main", commitA, nil)
	repo, _ := parseGitRepo(f.repoURL(), forgeGitea)
	if _, _, err := resolveGitRef(t.Context(), repo, gitRefLatestRelease, ""); err == nil || !strings.Contains(err.Error(), "no release and no semver tag") {
		t.Errorf("nothing to follow: %v", err)
	}
	f.token = "s3cret"
	if _, _, err := resolveGitRef(t.Context(), repo, "main", ""); err == nil || !strings.Contains(err.Error(), "tokenSecretRef") {
		t.Errorf("a private repository without a token: %v", err)
	}
	other, _ := parseGitRepo(f.URL+"/acme/missing", forgeGitea)
	if _, _, err := resolveGitRef(t.Context(), other, "main", ""); err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Errorf("a missing repository: %v", err)
	}
}

// A collection seeded by autoindex: without adopt a sync leaves its
// documents alone; with adopt it takes them over, under the same keys.
func TestGitSyncAdopt(t *testing.T) {
	s := newBackupTestServer(t)
	f := newFakeForge(t, forgeGitea)
	f.commit("refs/heads/main", commitA, map[string]string{"docs/API.md": "# API", "docs/new.md": "# New"})
	addTestDoc(t, s, "repo", "docs/API.md", "en", "# API (autoindex)", map[string][]string{"path": {"docs/API.md"}})

	a := gitSyncAction(f, "main")
	rule := syncRule(t, s, a)
	out, err := runSync(t, s, rule)
	if err == nil || out.counts.Failed != 1 || out.counts.Added != 1 || !strings.Contains(out.errors[0], "not written by this sync") {
		t.Fatalf("without adopt: %+v, %v", out, err)
	}

	rule.Sync.Adopt = true
	out, err = runSync(t, s, rule)
	if err != nil || out.counts.Updated != 1 || out.counts.Unchanged != 1 {
		t.Fatalf("with adopt: %+v, %v", out, err)
	}
	d, _ := s.loadDocByRef("repo", "docs/API.md", "en")
	if d.ContentMD != "# API" || metaFirst(d.Meta, syncMetaRule) != rule.ID || countDocs(t, s, "repo") != 2 {
		t.Errorf("the adopted document: %q %v, %d documents", d.ContentMD, d.Meta, countDocs(t, s, "repo"))
	}

	// A document another sync wrote is never adopted.
	other := syncRule(t, s, gitSyncAction(f, "main"))
	other.Sync.Adopt = true
	if out, _ := runSync(t, s, other); out.counts.Failed != 2 {
		t.Errorf("another sync's documents: %+v", out)
	}
}

// A private repository: the token is a stored secret, sent per forge
// convention, and the secret cannot be deleted while a rule names it.
func TestGitSyncPrivateRepository(t *testing.T) {
	s := newBackupTestServer(t)
	chatMcpKey(t)
	s.ChatScenarios = NewChatScenarioManager(s.DB)
	if err := s.ChatScenarios.LoadAll(); err != nil {
		t.Fatal(err)
	}
	f := newFakeForge(t, forgeGitea)
	f.token = "s3cret"
	f.commit("refs/heads/main", commitA, map[string]string{"a.md": "# A"})
	a := gitSyncAction(f, "main")
	a.TokenSecretRef = "forge-token"
	if _, err := s.AutomationManager.Create(AutomationRule{Type: "cron", Name: "sync", Schedule: "0 * * * *", Sync: a}); err == nil ||
		!strings.Contains(err.Error(), `"forge-token" not found`) {
		t.Fatalf("a missing secret: %v", err)
	}
	if _, err := s.PutChatMcpSecret("forge-token", "", "s3cret"); err != nil {
		t.Fatal(err)
	}
	rule := syncRule(t, s, a)
	if out, err := runSync(t, s, rule); err != nil || out.counts.Added != 1 {
		t.Fatalf("a private repository: %+v, %v", out, err)
	}
	seen := strings.Join(f.authSeen(), "|")
	if !strings.Contains(seen, "Basic ") || !strings.Contains(seen, "token s3cret") {
		t.Errorf("credentials sent: %s", seen)
	}
	if used, err := s.DeleteChatMcpSecret("forge-token"); !errors.Is(err, errChatMcpSecretInUse) || used[0] != "automation:sync" {
		t.Errorf("deleting a secret a sync uses: %v %v", used, err)
	}
	if got := s.AutomationManager.Get(rule.ID); got.Sync.TokenSecretRef != "forge-token" || strings.Contains(fmt.Sprintf("%+v", *got.Sync), "s3cret") {
		t.Errorf("a read: %+v", got.Sync)
	}

	// A secret that cannot be opened fails the run before anything is read.
	SetGlobalEncryptor(nil)
	if _, err := runSync(t, s, rule); err == nil || !strings.Contains(err.Error(), "connecting to the source") {
		t.Errorf("an unopenable secret: %v", err)
	}
	s.ChatScenarios = nil
	if _, err := s.syncSecret("forge-token"); !errors.Is(err, errChatScenariosUnavailable) {
		t.Errorf("no secret store: %v", err)
	}
}

// What an archive may not do: climb out of the repository, smuggle a link,
// hold a file over maxBytes or more files than maxDocuments.
func TestGitSyncArchiveSafety(t *testing.T) {
	s := newBackupTestServer(t)
	f := newFakeForge(t, forgeGitea)
	f.commit("refs/heads/main", commitA, nil)
	f.raw[commitA] = tarGz(tarEntry{name: "docs-x/ok.md", body: "# ok"}, tarEntry{name: "docs-x/../../etc/passwd", body: "root"})
	rule := syncRule(t, s, gitSyncAction(f, "main"))
	if _, err := runSync(t, s, rule); err == nil || !strings.Contains(err.Error(), "outside the repository") || countDocs(t, s, "repo") != 0 {
		t.Errorf("a path with ..: %v", err)
	}

	f.commit("refs/heads/main", commitB, nil)
	f.raw[commitB] = tarGz(tarEntry{name: "docs-x/ok.md", body: "# ok"},
		tarEntry{name: "docs-x/link.md", typeflag: '2', linkname: "/etc/passwd"},
		tarEntry{name: "docs-x/big.md", body: strings.Repeat("x", 2048)}, tarEntry{name: "docs-x/sub/", typeflag: '5'})
	rule.Sync.MaxBytes = 1024
	out, err := runSync(t, s, rule)
	if err == nil || out.counts.Added != 1 || out.counts.Failed != 1 || !strings.Contains(out.errors[0], "maxBytes") {
		t.Fatalf("a link and a large file: %+v, %v", out, err)
	}
	if _, err := s.loadDocByRef("repo", "link.md", "en"); !errors.Is(err, errDocNotFound) {
		t.Error("a symlink was synced")
	}

	rule.Sync.MaxDocuments = 1
	if out, err := runSync(t, s, rule); err == nil || !strings.Contains(err.Error(), "nothing pruned") || out.counts.Deleted != 0 {
		t.Errorf("more files than maxDocuments: %+v, %v", out, err)
	}

	f.commit("refs/heads/main", commitC, nil)
	f.raw[commitC] = []byte("not an archive")
	if _, err := runSync(t, s, rule); err == nil || !strings.Contains(err.Error(), "not gzip") {
		t.Errorf("not an archive: %v", err)
	}
	f.commit("refs/heads/main", strings.Repeat("e", 40), nil)
	delete(f.commits, strings.Repeat("e", 40))
	if _, err := runSync(t, s, rule); err == nil || !strings.Contains(err.Error(), "downloading the archive") {
		t.Errorf("no archive: %v", err)
	}
}

// The SSRF guard applies to every request of a git source.
func TestGitSyncRefusesInternalAddresses(t *testing.T) {
	s := newBackupTestServer(t)
	f := newFakeForge(t, forgeGitea)
	f.commit("refs/heads/main", commitA, map[string]string{"a.md": "# A"})
	rule := syncRule(t, s, gitSyncAction(f, "main"))
	t.Setenv("MDDB_OUTBOUND_ALLOW_PRIVATE", "false")
	if _, err := runSync(t, s, rule); err == nil || !strings.Contains(err.Error(), "not allowed") {
		t.Errorf("an internal forge: %v", err)
	}
	if len(f.authSeen()) != 0 {
		t.Error("the internal address was reached")
	}
}
