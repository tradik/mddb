package main

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"time"
)

// The requests of an issues source (INT-023): every one goes through
// readOutbound — the SSRF guard and the pooled client — with forgeClient's
// https-only redirects that drop the token off the forge's host, a timeout
// of syncFetchTimeout, the sync's User-Agent, and the token only on the
// forge's own hosts (gitRepo.header). A refusal for the rate limit names
// when it resets.

// maxIssuesPages bounds the pages of one listing or of one item's comments.
const maxIssuesPages = 200

// maxIssuesPageBytes bounds one page of the forge's answer. A test lowers
// it.
var maxIssuesPageBytes int64 = 32 << 20

// issuesNow is the clock a rate limit's reset is read against. A test
// replaces it.
var issuesNow = time.Now

// forgePage is one answer of the forge's API: the body of a 200, or
// notModified for a 304, and the page after it.
type forgePage struct {
	notModified bool
	body        []byte
	etag        string
	next        string
}

// get reads one page, conditionally on etag when set.
func (g *issuesSyncSource) get(ctx context.Context, rawURL, etag string) (forgePage, error) {
	ctx, cancel := context.WithTimeout(ctx, syncFetchTimeout)
	defer cancel()
	h := g.repo.header(rawURL, g.token, false)
	h.Set("Accept", "application/json")
	if etag != "" {
		h.Set("If-None-Match", etag)
	}
	res, err := readOutbound(ctx, forgeClient(), rawURL, maxIssuesPageBytes, h)
	switch {
	case err != nil:
		return forgePage{}, err
	case res.status == http.StatusNotModified:
		return forgePage{notModified: true}, nil
	case res.status == http.StatusTooManyRequests,
		res.status == http.StatusForbidden && rateLimited(res.header):
		return forgePage{}, fmt.Errorf("%w: %s", errSyncRateLimited, rateLimitReset(res.header))
	case res.status == http.StatusNotFound || res.status == http.StatusGone:
		return forgePage{}, errSourceGone
	case res.status == http.StatusUnauthorized || res.status == http.StatusForbidden:
		return forgePage{}, fmt.Errorf("the forge answered %s: the repository is private and needs a token that can read its issues "+
			"(tokenSecretRef, the collection's repository, or a default forge token)", res.statusText)
	case res.status != http.StatusOK:
		return forgePage{}, fmt.Errorf("the forge answered %s", res.statusText)
	case res.overLimit:
		return forgePage{}, fmt.Errorf("a page of the forge's answer is larger than %d bytes", maxIssuesPageBytes)
	}
	return forgePage{body: res.body, etag: res.header.Get("ETag"), next: nextLink(res.header.Get("Link"))}, nil
}

// rateLimited reports whether a 403 is the rate limit rather than a token
// without access: no requests remain, or the forge says when to retry.
func rateLimited(h http.Header) bool {
	return h.Get("X-RateLimit-Remaining") == "0" || h.Get("RateLimit-Remaining") == "0" || h.Get("Retry-After") != ""
}

// rateLimitReset says when the forge takes requests again: Retry-After
// (seconds), or the reset time GitHub and Gitea send as X-RateLimit-Reset
// and GitLab as RateLimit-Reset (Unix seconds).
func rateLimitReset(h http.Header) string {
	if s, err := strconv.Atoi(h.Get("Retry-After")); err == nil {
		return "it resets at " + issuesNow().Add(time.Duration(s)*time.Second).UTC().Format(time.RFC3339)
	}
	for _, k := range []string{"X-RateLimit-Reset", "RateLimit-Reset"} {
		if s, err := strconv.ParseInt(h.Get(k), 10, 64); err == nil {
			return "it resets at " + time.Unix(s, 0).UTC().Format(time.RFC3339)
		}
	}
	return "the forge does not say when it resets"
}

// linkNext is the next page in a Link header (RFC 8288), which GitHub,
// GitLab and Gitea all send.
var linkNext = regexp.MustCompile(`<([^>]+)>\s*;\s*rel="?next"?`)

// nextLink is the URL of the next page; "" on the last.
func nextLink(link string) string {
	if m := linkNext.FindStringSubmatch(link); m != nil {
		return m[1]
	}
	return ""
}
