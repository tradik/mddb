package main

import (
	"context"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"mddb/internal/httpclient"
)

// Fetching a skill from a URL (#404). The rules are the corpus packs' —
// https only, a redirect may not leave TLS, the server's SSRF guard on every
// request (readOutbound) — with a cap just above MDDB_CHAT_SKILL_MAX_BYTES.
//
// A GitHub URL (a blob or raw page, or raw.githubusercontent.com) is pinned:
// its branch or tag is resolved to a commit first, and the file is read at
// that commit, so the commit stored with the skill is the commit of the text.

const (
	chatSkillFetchTimeout = 15 * time.Second
	// chatSkillFrontMatterAllowance is what a SKILL.md may add to its
	// instructions as front matter before the fetch is refused as too large.
	chatSkillFrontMatterAllowance = 8192
)

// Where GitHub is reached, and the client: the pooled, SSRF-guarded one with
// redirects held to https. A test points them at its own TLS server.
var (
	chatSkillGitHubAPI   = "https://api.github.com"
	chatSkillGitHubRaw   = "https://raw.githubusercontent.com"
	chatSkillFetchClient = func() *http.Client {
		c := httpclient.NewPooledClientWithTimeout(chatSkillFetchTimeout)
		c.CheckRedirect = httpsOnlyRedirect(c.CheckRedirect)
		return c
	}
)

// gitHubFile is the file a GitHub URL names. rest is everything after the
// repository, as written; ref and path split it at the first slash, which
// is right unless a branch name holds one.
type gitHubFile struct{ owner, repo, ref, path, rest string }

// parseGitHubFileURL reads github.com/{owner}/{repo}/blob|raw/{ref}/{path}
// and raw.githubusercontent.com/{owner}/{repo}/{ref}/{path}.
func parseGitHubFileURL(u *url.URL) (gitHubFile, bool) {
	seg := strings.Split(strings.Trim(u.Path, "/"), "/")
	switch {
	case u.Host == "github.com" && len(seg) >= 5 && (seg[2] == "blob" || seg[2] == "raw"):
		seg = append(seg[:2:2], seg[3:]...)
	case u.Host == "raw.githubusercontent.com" && len(seg) >= 4:
	default:
		return gitHubFile{}, false
	}
	rest := seg[2:]
	f := gitHubFile{owner: seg[0], repo: seg[1], rest: strings.Join(rest, "/")}
	if len(rest) > 3 && rest[0] == "refs" && (rest[1] == "heads" || rest[1] == "tags") {
		rest = rest[2:] // refs/heads/main/SKILL.md
	}
	f.ref, f.path = rest[0], strings.Join(rest[1:], "/")
	return f, true
}

// isCommitSHA reports whether s is a full Git commit id.
func isCommitSHA(s string) bool {
	_, err := hex.DecodeString(s)
	return err == nil && len(s) == 40
}

// chatSkillGet reads at most limit bytes of a 200 answer from target.
func chatSkillGet(ctx context.Context, target string, limit int64, accept string) ([]byte, error) {
	header := http.Header{"User-Agent": {"mddbd/" + VERSION}}
	if accept != "" {
		header.Set("Accept", accept)
	}
	res, err := readOutbound(ctx, chatSkillFetchClient(), target, limit, header)
	switch {
	case err != nil:
		return nil, fmt.Errorf("fetching the skill: %w", err)
	case res.status != http.StatusOK:
		return nil, fmt.Errorf("fetching the skill: %s answered %s", target, res.statusText)
	case res.overLimit:
		return nil, fmt.Errorf("the skill at %s is larger than %d bytes", target, limit)
	}
	return res.body, nil
}

// resolveGitHubCommit asks the GitHub API which commit a branch or tag is
// at; "" when it cannot say (an unknown ref, a rate limit, a private
// repository), and the import then goes on without a commit.
func resolveGitHubCommit(ctx context.Context, f gitHubFile) string {
	if isCommitSHA(f.ref) {
		return f.ref
	}
	target, _ := url.JoinPath(chatSkillGitHubAPI, "repos", f.owner, f.repo, "commits", f.ref)
	body, err := chatSkillGet(ctx, target, 64, "application/vnd.github.sha")
	if sha := strings.TrimSpace(string(body)); err == nil && isCommitSHA(sha) {
		return sha
	}
	return ""
}

// fetchChatSkill downloads the SKILL.md at rawURL and says where it came
// from. Nothing is stored.
func fetchChatSkill(ctx context.Context, rawURL string) (text string, src *ChatSkillSource, err error) {
	u, err := url.Parse(rawURL)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return "", nil, fmt.Errorf("%q: skills are imported over https only", rawURL)
	}
	target, commit := rawURL, ""
	if f, isGitHub := parseGitHubFileURL(u); isGitHub {
		at := f.rest
		if commit = resolveGitHubCommit(ctx, f); commit != "" {
			at = commit + "/" + f.path
		}
		target, _ = url.JoinPath(chatSkillGitHubRaw, f.owner, f.repo, at)
	}
	body, err := chatSkillGet(ctx, target, int64(chatSkillMaxBytes())+chatSkillFrontMatterAllowance, "")
	if err != nil {
		return "", nil, err
	}
	if !utf8.Valid(body) {
		return "", nil, fmt.Errorf("the skill at %s is not UTF-8 text", target)
	}
	return string(body), &ChatSkillSource{URL: rawURL, Commit: commit, FetchedAt: time.Now().UTC().Truncate(time.Second)}, nil
}
