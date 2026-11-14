package main

import (
	"bytes"
	"math/rand/v2"
	"strings"
	"testing"
)

// The answers a forge gives that are neither refs nor a release.
func TestGitRefsAndReleaseAnswers(t *testing.T) {
	f := newFakeForge(t, forgeGitea)
	f.commit("refs/heads/main", commitA, nil)
	repo, _ := parseGitRepo(f.repoURL(), forgeGitea)

	f.refsStatus = 500
	if _, err := readGitRefs(t.Context(), repo, ""); err == nil || !strings.Contains(err.Error(), "reading the refs: 500") {
		t.Errorf("a failing forge: %v", err)
	}
	f.refsStatus = 0
	prev := maxGitRefsBytes
	maxGitRefsBytes = 16
	if _, err := readGitRefs(t.Context(), repo, ""); err == nil || !strings.Contains(err.Error(), "larger than 16 bytes") {
		t.Errorf("an advertisement over the cap: %v", err)
	}
	maxGitRefsBytes = prev

	f.releaseBody = "not json"
	if _, _, err := resolveGitRef(t.Context(), repo, gitRefLatestRelease, ""); err == nil || !strings.Contains(err.Error(), "reading the latest release") {
		t.Errorf("a release that is not JSON: %v", err)
	}
	f.forge = forgeGitLab
	gitlab, _ := parseGitRepo(f.repoURL(), forgeGitLab)
	f.releaseBody = "[]"
	if tag, err := latestReleaseTag(t.Context(), gitlab, ""); tag != "" || err != nil {
		t.Errorf("GitLab without releases: %q %v", tag, err)
	}

	closed, _ := parseGitRepo("https://127.0.0.1:1/acme/docs", forgeGitea)
	if _, err := latestReleaseTag(t.Context(), closed, ""); err == nil {
		t.Error("a forge that does not answer")
	}
}

// HEAD without a symref names itself; equal versions keep the higher name.
func TestGitRefsHeadAndSemverTies(t *testing.T) {
	body := pktLine(commitA+" HEAD\n") + pktLine(commitB+" refs/tags/1.0.0\n") + pktLine(commitC+" refs/tags/v1.0.0\n")
	refs, err := parseGitRefs([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	if name, commit, err := refs.resolve(""); name != "HEAD" || commit != commitA || err != nil {
		t.Errorf("HEAD: %s %s %v", name, commit, err)
	}
	if tag := refs.highestSemverTag(); tag != "v1.0.0" {
		t.Errorf("a tie: %s", tag)
	}
}

// What only a stored rule out of step with validation, or a broken stream,
// could reach.
func TestGitSyncSourceEdges(t *testing.T) {
	s := newBackupTestServer(t)
	rule := AutomationRule{ID: "r", Sync: &SyncAction{Source: SyncSourceGit, URL: "https://unknown.example/a/b"}}
	if _, err := s.newGitSyncSource(rule); err == nil {
		t.Error("an unknown forge connected")
	}
	s.AutomationLogStore = nil
	if s.lastGitCommit(rule) != "" {
		t.Error("a last commit without a log")
	}
	if (&AutomationManager{}).checkSyncSecrets(&SyncAction{GitSyncFields: GitSyncFields{TokenSecretRef: "x"}}) != nil {
		t.Error("secrets checked without a server")
	}
	if (&Server{}).syncRulesUsingSecret("x") != nil {
		t.Error("rules without automations")
	}
	if _, err := s.AutomationManager.Create(AutomationRule{Type: "cron", Name: "x", Schedule: "0 * * * *",
		Sync: &SyncAction{Source: SyncSourceGit}}); err == nil || !strings.Contains(err.Error(), "collection and lang") {
		t.Errorf("an invalid sync: %v", err)
	}

	t.Setenv("MDDB_OUTBOUND_ALLOW_PRIVATE", "false")
	repo, _ := parseGitRepo("https://127.0.0.1:1/acme/docs", forgeGitea)
	g := &gitSyncSource{action: &SyncAction{MaxDocuments: 1, MaxBytes: 1}, repo: repo, commit: commitA}
	if _, _, err := g.readArchive(t.Context()); err == nil || !strings.Contains(err.Error(), "not allowed") {
		t.Errorf("an internal archive host: %v", err)
	}
	if c := gitSyncClient(); c.CheckRedirect == nil || c.Timeout != gitSyncTimeout {
		t.Error("the production client")
	}
}

// A binary file is left out without a word; a stream cut inside a file
// fails the read.
func TestReadGitArchiveBinaryAndCut(t *testing.T) {
	lim := gitArchiveLimits{maxFiles: 10, maxBytes: 1 << 20}
	files, _, err := readGitArchive(bytes.NewReader(tarGz(tarEntry{name: "r/bin.dat", body: "\x00\x01"},
		tarEntry{name: "r/doc.pdf", body: "%PDF-\x00"})), lim)
	if err != nil || len(files) != 1 || files[0].path != "doc.pdf" {
		t.Errorf("binary files: %+v %v", files, err)
	}
	rng := rand.New(rand.NewPCG(1, 2)) // #nosec G404 -- incompressible test data
	noise := make([]byte, 64<<10)
	for i := range noise {
		noise[i] = "abcdefghijklmnopqrstuvwxyz"[rng.IntN(26)]
	}
	full := tarGz(tarEntry{name: "r/a.md", body: string(noise)})
	if _, _, err := readGitArchive(bytes.NewReader(full[:len(full)/2]), lim); err == nil || !strings.Contains(err.Error(), "a.md") {
		t.Errorf("a stream cut inside a file: %v", err)
	}
}
