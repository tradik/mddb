package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	json "mddb/internal/jsonx"
)

// Resolving a git source's ref to a commit (#421) from the ref
// advertisement of git's smart HTTP protocol: GET
// <repo>.git/info/refs?service=git-upload-pack answers every branch and tag
// with its object id, an annotated tag followed by the commit it points to
// ("<tag>^{}"). "latest-release" asks the forge's API for the latest
// release's tag and falls back to the highest semver tag.

// Bounds of the reads that resolve a ref.
const (
	maxGitReleaseBytes = 1 << 20
	gitPeeledSuffix    = "^{}"
	gitHeadsPrefix     = "refs/heads/"
	gitTagsPrefix      = "refs/tags/"
)

// maxGitRefsBytes bounds the advertisement of a repository with very many
// tags. A test lowers it.
var maxGitRefsBytes int64 = 32 << 20

// gitObjectID is an object id: SHA-1, or SHA-256 in a repository of git's
// newer object format.
var gitObjectID = regexp.MustCompile(`^(?:[0-9a-f]{40}|[0-9a-f]{64})$`)

// gitSemverTag is a release tag: v1.2.3 or 1.2.3, no pre-release.
var gitSemverTag = regexp.MustCompile(`^v?(\d+)\.(\d+)\.(\d+)$`)

// gitRefs are a repository's refs: full name → the commit it points to (an
// annotated tag's peeled commit), and the branch HEAD names.
type gitRefs struct {
	byName map[string]string
	head   string // the default branch, e.g. refs/heads/main
}

// readGitRefs fetches and parses the ref advertisement.
func readGitRefs(ctx context.Context, repo gitRepo, token string) (gitRefs, error) {
	res, err := readOutbound(ctx, gitSyncClient(), repo.infoRefsURL(), maxGitRefsBytes, repo.header(token, true))
	switch {
	case err != nil:
		return gitRefs{}, err
	case res.status == http.StatusUnauthorized || res.status == http.StatusNotFound:
		return gitRefs{}, fmt.Errorf("the repository answered %s: it does not exist, or it is private and needs tokenSecretRef", res.statusText)
	case res.status != http.StatusOK:
		return gitRefs{}, fmt.Errorf("reading the refs: %s", res.statusText)
	case res.overLimit:
		return gitRefs{}, fmt.Errorf("the ref advertisement is larger than %d bytes", maxGitRefsBytes)
	}
	return parseGitRefs(res.body)
}

// parseGitRefs reads a smart HTTP advertisement: pkt-lines (four hex digits
// of length, the length included; "0000" a flush), a "# service=" line, then
// "<id> <name>" per ref, the first one followed by NUL and the capabilities —
// among them symref=HEAD:<branch>.
func parseGitRefs(body []byte) (gitRefs, error) {
	refs := gitRefs{byName: map[string]string{}}
	r := bufio.NewReader(bytes.NewReader(body))
	peeled := map[string]string{}
	for {
		line, err := readPktLine(r)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return gitRefs{}, err
		}
		refs.addLine(line, peeled)
	}
	if len(refs.byName) == 0 {
		return gitRefs{}, errors.New("the server sent no refs: is the URL a git repository served over smart HTTP?")
	}
	for name, id := range peeled {
		refs.byName[name] = id
	}
	return refs, nil
}

// addLine records one ref line; others (the service line, flushes) are
// skipped. An annotated tag's peeled commit goes to peeled.
func (g *gitRefs) addLine(line string, peeled map[string]string) {
	line, caps, _ := strings.Cut(strings.TrimSuffix(line, "\n"), "\x00")
	id, name, found := strings.Cut(line, " ")
	if !found || !gitObjectID.MatchString(id) {
		return
	}
	for _, c := range strings.Fields(caps) {
		if target, ok := strings.CutPrefix(c, "symref=HEAD:"); ok {
			g.head = target
		}
	}
	if tag, ok := strings.CutSuffix(name, gitPeeledSuffix); ok {
		peeled[tag] = id
		return
	}
	g.byName[name] = id
}

// readPktLine reads one pkt-line's payload; a flush is "".
func readPktLine(r *bufio.Reader) (string, error) {
	var size [4]byte
	if _, err := io.ReadFull(r, size[:]); err != nil {
		if errors.Is(err, io.ErrUnexpectedEOF) {
			return "", errors.New("a truncated pkt-line")
		}
		return "", err
	}
	n, err := strconv.ParseUint(string(size[:]), 16, 16)
	switch {
	case err != nil:
		return "", fmt.Errorf("not a pkt-line: %q (is the URL a git repository?)", size[:])
	case n < 4:
		return "", nil // flush (0000) and the v2 delimiters
	}
	payload := make([]byte, n-4)
	if _, err := io.ReadFull(r, payload); err != nil {
		return "", errors.New("a truncated pkt-line")
	}
	return string(payload), nil
}

// resolve turns a ref into the name a run reports and the commit it reads:
// "" is the default branch, a branch or tag by its short or full name, a
// commit id as itself.
func (g gitRefs) resolve(ref string) (name, commit string, err error) {
	if ref == "" {
		ref = g.head
		if ref == "" {
			ref = "HEAD"
		}
	}
	for _, full := range []string{ref, gitHeadsPrefix + ref, gitTagsPrefix + ref} {
		if id := g.byName[full]; id != "" {
			return shortRefName(full), id, nil
		}
	}
	if gitObjectID.MatchString(ref) {
		return ref, ref, nil
	}
	return "", "", fmt.Errorf("the repository has no branch or tag %q", ref)
}

// shortRefName drops refs/heads/ or refs/tags/.
func shortRefName(full string) string {
	for _, p := range []string{gitHeadsPrefix, gitTagsPrefix} {
		if s, ok := strings.CutPrefix(full, p); ok {
			return s
		}
	}
	return full
}

// highestSemverTag is the tag with the highest version among the v1.2.3 /
// 1.2.3 tags; "" when there is none.
func (g gitRefs) highestSemverTag() string {
	best, bestV := "", [3]int{-1, -1, -1}
	for full := range g.byName {
		tag, ok := strings.CutPrefix(full, gitTagsPrefix)
		m := gitSemverTag.FindStringSubmatch(tag)
		if !ok || m == nil {
			continue
		}
		var v [3]int
		for i := range v {
			v[i], _ = strconv.Atoi(m[i+1]) // \d+ parses unless it overflows, and then 0 loses
		}
		if newer(v, bestV) || (v == bestV && tag > best) {
			best, bestV = tag, v
		}
	}
	return best
}

// newer reports whether version a is above b.
func newer(a, b [3]int) bool {
	for i := range a {
		if a[i] != b[i] {
			return a[i] > b[i]
		}
	}
	return false
}

// latestReleaseTag asks the forge for its latest release's tag; "" when the
// repository has no release (or the forge does not say), so the caller
// falls back to the highest semver tag.
func latestReleaseTag(ctx context.Context, repo gitRepo, token string) (string, error) {
	res, err := readOutbound(ctx, gitSyncClient(), repo.latestReleaseURL(), maxGitReleaseBytes, repo.header(token, false))
	switch {
	case err != nil:
		return "", err
	case res.status == http.StatusNotFound:
		return "", nil
	case res.status != http.StatusOK || res.overLimit:
		return "", fmt.Errorf("reading the latest release: %s", res.statusText)
	}
	var release struct {
		TagName string `json:"tag_name"`
	}
	body := bytes.TrimSpace(res.body)
	if repo.forge == forgeGitLab { // GitLab lists releases: the newest first
		var list []json.RawMessage
		if err := json.Unmarshal(body, &list); err != nil || len(list) == 0 {
			return "", nil
		}
		body = list[0]
	}
	if err := json.Unmarshal(body, &release); err != nil {
		return "", fmt.Errorf("reading the latest release: %w", err)
	}
	return release.TagName, nil
}

// resolveGitRef resolves the action's ref against the repository.
func resolveGitRef(ctx context.Context, repo gitRepo, ref, token string) (name, commit string, err error) {
	refs, err := readGitRefs(ctx, repo, token)
	if err != nil {
		return "", "", err
	}
	if ref != gitRefLatestRelease {
		return refs.resolve(ref)
	}
	tag, err := latestReleaseTag(ctx, repo, token)
	if err != nil {
		return "", "", err
	}
	if tag == "" {
		if tag = refs.highestSemverTag(); tag == "" {
			return "", "", errors.New("the repository has no release and no semver tag (v1.2.3) to follow")
		}
	}
	return refs.resolve(gitTagsPrefix + tag)
}
