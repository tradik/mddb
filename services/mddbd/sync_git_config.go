package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"path"
	"strings"

	json "mddb/internal/jsonx"
)

// The settings of a git source (#421) and their validation. A git source
// reads a repository's files at a branch, a tag or its latest release from
// the forge that hosts it — the ref advertisement of git's smart HTTP
// protocol and the forge's archive of the commit — so mddbd needs no git
// binary (its image has none). sync_git.go runs it.

// GitSyncFields are the fields only a git source reads.
type GitSyncFields struct {
	// Ref is a branch, a tag, or "latest-release": the forge's latest
	// release, else the highest semver tag. Empty is the default branch.
	Ref string `json:"ref,omitempty"`
	// Include and Exclude are globs over repository-relative paths, matched
	// as `mddb-cli autoindex --include/--exclude` match them.
	Include []string `json:"include,omitempty"`
	Exclude []string `json:"exclude,omitempty"`
	// Forge is "github", "gitlab", "gitea" or "forgejo": how a host other
	// than github.com, gitlab.com and codeberg.org serves releases and
	// archives. Empty for those three, which are known by their host.
	Forge string `json:"forge,omitempty"`
	// TokenSecretRef names a stored secret (the write-only store
	// /v1/chat/mcp-secrets keeps, sealed with MDDB_ENCRYPTION_KEY) whose
	// value is the token a private repository is read with.
	TokenSecretRef string `json:"tokenSecretRef,omitempty"`
	// HookSecretRef names the stored secret POST /v1/automation/{id}/hook
	// verifies a push or release webhook with; empty disables the hook.
	HookSecretRef string `json:"hookSecretRef,omitempty"`
	// Adopt lets the rule take over documents of the same key that no sync
	// wrote — a collection seeded by `mddb-cli autoindex`. Off by default:
	// a sync never overwrites a document it did not write.
	Adopt bool `json:"adopt,omitempty"`
}

// The ref that follows the forge's releases.
const gitRefLatestRelease = "latest-release"

// Forges a git source reads from.
const (
	forgeGitHub  = "github"
	forgeGitLab  = "gitlab"
	forgeGitea   = "gitea"
	forgeForgejo = "forgejo" // Gitea's API: same releases and archives
)

// knownForgeHosts are the hosts a forge is known by without `forge`.
var knownForgeHosts = map[string]string{
	"github.com":   forgeGitHub,
	"gitlab.com":   forgeGitLab,
	"codeberg.org": forgeForgejo,
}

// validForges are the values `forge` takes.
var validForges = map[string]bool{forgeGitHub: true, forgeGitLab: true, forgeGitea: true, forgeForgejo: true}

// errGitForgeUnknown names what a git source can read from.
var errGitForgeUnknown = errors.New("a git sync reads from github.com, gitlab.com and codeberg.org, " +
	"or from a self-hosted GitHub, GitLab, Gitea or Forgejo named in forge (github, gitlab, gitea, forgejo)")

// checkGit validates a git source: an https repository URL a forge serves,
// a ref, globs that parse and secret names that may name a secret.
func (a *SyncAction) checkGit() error {
	g := &a.GitSyncFields
	g.Ref = strings.TrimSpace(g.Ref)
	g.Forge = strings.ToLower(strings.TrimSpace(g.Forge))
	a.URL = strings.TrimRight(strings.TrimSpace(a.URL), "/")
	if _, err := parseGitRepo(a.URL, g.Forge); err != nil {
		return err
	}
	if err := checkGitRef(g.Ref); err != nil {
		return err
	}
	for _, glob := range append(append([]string(nil), g.Include...), g.Exclude...) {
		if _, err := path.Match(glob, ""); err != nil {
			return fmt.Errorf("sync glob %q: %w", glob, err)
		}
	}
	for _, ref := range []string{g.TokenSecretRef, g.HookSecretRef} {
		if ref == "" {
			continue
		}
		if err := validChatKeyName("sync secret", ref); err != nil {
			return err
		}
	}
	return nil
}

// checkSyncSecrets refuses a sync that names a stored secret the server
// does not have: the rule would fail on every run, or never verify a hook.
func (am *AutomationManager) checkSyncSecrets(a *SyncAction) error {
	if am.server == nil {
		return nil
	}
	for _, ref := range []string{a.TokenSecretRef, a.HookSecretRef} {
		if ref != "" && !am.server.ChatScenarios.HasMcpSecret(ref) {
			return fmt.Errorf("sync secret %q not found: store it first (PUT /v1/chat/mcp-secrets/%s)", ref, ref)
		}
	}
	return nil
}

// syncRulesUsingSecret names the sync rules whose token or hook is a stored
// secret, which keeps it from being deleted.
func (s *Server) syncRulesUsingSecret(name string) []string {
	if s.AutomationManager == nil {
		return nil
	}
	var out []string
	for _, r := range s.AutomationManager.List("cron") {
		if r.Sync != nil && (r.Sync.TokenSecretRef == name || r.Sync.HookSecretRef == name) {
			out = append(out, "automation:"+r.Name)
		}
	}
	return out
}

// fingerprint identifies the settings a run reads with: a commit the last
// run read is skipped only when they have not changed since.
func (a *SyncAction) fingerprint() string {
	data, _ := json.Marshal(a) // strings, numbers and slices: it encodes
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:8])
}

// commitURL is a commit's page on the forge of a git source; "" for
// another source, or no commit.
func (a *SyncAction) commitURL(commit string) string {
	if a == nil || a.Source != SyncSourceGit || commit == "" {
		return ""
	}
	repo, err := parseGitRepo(a.URL, a.Forge)
	if err != nil {
		return ""
	}
	return repo.commitURL(commit)
}

// checkGitRef refuses a ref git would refuse, or that could not be a path
// segment of an archive URL: control characters, spaces, "..", a leading
// "-" or "/".
func checkGitRef(ref string) error {
	bad := strings.ContainsAny(ref, " ~^:?*[\\") || strings.Contains(ref, "..") ||
		strings.HasPrefix(ref, "-") || strings.HasPrefix(ref, "/") || strings.HasSuffix(ref, "/")
	for _, r := range ref {
		bad = bad || r < 0x20 || r == 0x7f
	}
	if bad {
		return fmt.Errorf("sync ref %q is not a branch, a tag or %q", ref, gitRefLatestRelease)
	}
	return nil
}

// gitRepo is a repository on a forge.
type gitRepo struct {
	forge string
	// web is https://host/path without ".git": the repository's page.
	web  string
	host string
	// project is the repository's path on the host: owner/repo, or a GitLab
	// group path group/sub/repo.
	project string
}

// parseGitRepo reads a repository URL: https, no credentials, no query, a
// path of at least owner/repo, and a forge known by its host or named.
func parseGitRepo(raw, forge string) (gitRepo, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return gitRepo{}, fmt.Errorf("sync url %q: a git repository is read over https, without credentials or a query", raw)
	}
	project := strings.TrimSuffix(strings.Trim(u.Path, "/"), ".git")
	if strings.Count(project, "/") < 1 || strings.Contains(project, "//") {
		return gitRepo{}, fmt.Errorf("sync url %q: the repository's path (owner/repo) is missing", raw)
	}
	host := strings.ToLower(u.Host)
	if forge == "" {
		forge = knownForgeHosts[host]
	}
	if !validForges[forge] {
		return gitRepo{}, fmt.Errorf("sync url %q: %w", raw, errGitForgeUnknown)
	}
	if forge == forgeForgejo {
		forge = forgeGitea
	}
	return gitRepo{forge: forge, web: "https://" + u.Host + "/" + project, host: host, project: project}, nil
}
