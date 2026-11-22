package main

import (
	"encoding/base64"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"mddb/internal/httpclient"
)

// Where a forge (#421) serves what a git source reads: the ref advertisement
// of git's smart HTTP protocol (every forge, at <repo>.git/info/refs), the
// latest release (each forge's API) and the tar.gz archive of a commit (each
// forge's API or archive host), and how a token is sent to each.

// gitSyncTimeout bounds one request of a git source; an archive of a large
// repository takes its time.
const gitSyncTimeout = 10 * time.Minute

// Where github.com's API and archives are served. A test points them at its
// own TLS server.
var (
	gitHubAPI      = "https://api.github.com"
	gitHubCodeload = "https://codeload.github.com"
)

// gitSyncClient is the client a git source reads with: the pooled,
// SSRF-guarded one, with redirects held to https (an archive redirects to
// its download host). A test replaces it to trust its own TLS server.
var gitSyncClient = func() *http.Client {
	c := httpclient.NewPooledClientWithTimeout(gitSyncTimeout)
	c.CheckRedirect = httpsOnlyRedirect(c.CheckRedirect)
	return c
}

// isGitHubCom reports whether the repository is on github.com, whose API and
// archives have hosts of their own; another GitHub is GitHub Enterprise
// Server, which serves its API under /api/v3.
func (r gitRepo) isGitHubCom() bool { return r.host == "github.com" }

// apiBase is the root of the forge's REST API.
func (r gitRepo) apiBase() string {
	if r.forge == forgeGitHub && r.isGitHubCom() {
		return gitHubAPI
	}
	return "https://" + r.host + apiVersionPath[r.forge]
}

// apiVersionPath is where a self-hosted forge serves its REST API.
var apiVersionPath = map[string]string{forgeGitHub: "/api/v3", forgeGitLab: "/api/v4", forgeGitea: "/api/v1"}

// repoAPI is the repository in the GitHub and Gitea APIs.
func (r gitRepo) repoAPI() string { return r.apiBase() + "/repos/" + r.project }

// gitLabProject is the project's id in GitLab's API: its path, escaped.
func (r gitRepo) gitLabProject() string { return url.PathEscape(r.project) }

// infoRefsURL is the smart HTTP ref advertisement.
func (r gitRepo) infoRefsURL() string {
	return r.web + ".git/info/refs?service=git-upload-pack"
}

// latestReleaseURL is the API call that answers the latest release's tag.
func (r gitRepo) latestReleaseURL() string {
	if r.forge == forgeGitLab {
		return r.apiBase() + "/projects/" + r.gitLabProject() + "/releases?order_by=released_at&sort=desc&per_page=1"
	}
	return r.repoAPI() + "/releases/latest"
}

// archiveURL is the tar.gz of a commit. github.com's archive host serves a
// public repository without spending the API's rate limit; a private one
// goes through the API, which redirects to a signed download.
func (r gitRepo) archiveURL(commit string, private bool) string {
	switch {
	case r.forge == forgeGitHub && r.isGitHubCom() && !private:
		return gitHubCodeload + "/" + r.project + "/tar.gz/" + commit
	case r.forge == forgeGitHub:
		return r.repoAPI() + "/tarball/" + commit
	case r.forge == forgeGitLab:
		return r.apiBase() + "/projects/" + r.gitLabProject() + "/repository/archive.tar.gz?sha=" + commit
	}
	return r.repoAPI() + "/archive/" + commit + ".tar.gz"
}

// commitURL is the forge's page of a commit, for a run's result.
func (r gitRepo) commitURL(commit string) string {
	if r.forge == forgeGitLab {
		return r.web + "/-/commit/" + commit
	}
	return r.web + "/commit/" + commit
}

// gitBasicUser is the user name a forge takes with a token as the password
// of git's smart HTTP; Gitea and Forgejo read the token whatever the name.
var gitBasicUser = map[string]string{forgeGitHub: "x-access-token", forgeGitLab: "oauth2", forgeGitea: "oauth2"}

// header is the header of a request to rawURL: the sync's User-Agent and,
// with a token, the forge's credentials — basic auth for git's smart HTTP,
// the API's Authorization scheme (Gitea's is "token") for the rest. A URL
// on a host that is not the forge's gets no credentials (INT-023).
func (r gitRepo) header(rawURL, token string, smartHTTP bool) http.Header {
	h := syncHeader()
	var auth string
	switch {
	case token == "" || !r.tokenHost(rawURL):
		return h
	case smartHTTP:
		auth = "Basic " + base64.StdEncoding.EncodeToString([]byte(gitBasicUser[r.forge]+":"+token))
	case r.forge == forgeGitea:
		auth = "token " + token
	default:
		auth = "Bearer " + token
	}
	h.Set("Authorization", auth)
	return h
}

// tokenHost reports whether a request to rawURL may carry the repository's
// token: it goes to the repository's own host, exactly, or — for
// github.com, whose API and archives have hosts of their own — to those.
func (r gitRepo) tokenHost(rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	host := strings.ToLower(u.Host)
	if host == r.host {
		return true
	}
	if !r.isGitHubCom() {
		return false
	}
	for _, own := range []string{gitHubAPI, gitHubCodeload} {
		if o, err := url.Parse(own); err == nil && strings.EqualFold(o.Host, host) {
			return true
		}
	}
	return false
}

// forgeClient is the client every request of a git or issues source is sent
// with: gitSyncClient, whose redirects drop the Authorization header when
// they leave the host the request was sent to (INT-023). Go's client keeps
// it on a redirect to a subdomain; a token is never meant for one.
func forgeClient() *http.Client {
	c := *gitSyncClient()
	next := c.CheckRedirect
	c.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if !strings.EqualFold(req.URL.Host, via[0].URL.Host) {
			req.Header.Del("Authorization")
		}
		if next == nil {
			return defaultRedirectPolicy(via)
		}
		return next(req, via)
	}
	return &c
}

// maxForgeRedirects is the redirects a forge request follows, as Go's own
// client does.
const maxForgeRedirects = 10

// defaultRedirectPolicy is Go's: at most maxForgeRedirects.
func defaultRedirectPolicy(via []*http.Request) error {
	if len(via) >= maxForgeRedirects {
		return errors.New("stopped after 10 redirects")
	}
	return nil
}
