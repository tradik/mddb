package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"

	json "mddb/internal/jsonx"
)

// fakeIssues is a forge's issue API for the issues sync tests (INT-023):
// one repository, acme/docs, on the paths and in the shapes of the forge it
// plays — GitHub (Enterprise Server, so /api/v3), GitLab or Gitea —
// paginated with Link headers, conditional on ETag, and able to answer for
// its rate limit.
type fakeIssues struct {
	*httptest.Server
	forge string
	mu    sync.Mutex
	items map[string]*fakeItem // by key, issue-<n> / pull-<n>
	// perPage, when set, overrides the page size the client asks for.
	perPage int
	// limited answers every request with the rate limit's refusal.
	limited bool
	// status, when set, answers every request with it.
	status int
	// limitPath and garbagePath answer the requests whose path holds them
	// with the rate limit's 429, or with a body that is not JSON.
	limitPath, garbagePath string
	requests               []string // paths of the requests, with their query
	auth                   []string // the Authorization headers seen
	notMod                 int      // 304s answered
}

// fakeItem is one issue or pull request.
type fakeItem struct {
	kind, title, body, state, updated string
	number                            int
	labels                            []string
	merged                            bool
	comments                          []fakeComment
}

// fakeComment is one comment; system is a GitLab system note.
type fakeComment struct {
	author, body, created string
	system                bool
}

func newFakeIssues(t *testing.T, forge string) *fakeIssues {
	t.Helper()
	f := &fakeIssues{forge: forge, items: map[string]*fakeItem{}}
	f.Server = httptest.NewTLSServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.Close)
	prev := gitSyncClient
	gitSyncClient = func() *http.Client {
		c := *f.Client()
		c.CheckRedirect = httpsOnlyRedirect(func(*http.Request, []*http.Request) error { return nil })
		return &c
	}
	t.Cleanup(func() { gitSyncClient = prev })
	return f
}

// repoURL is the repository's URL on the fake forge.
func (f *fakeIssues) repoURL() string { return f.URL + "/acme/docs" }

// put adds or replaces an item.
func (f *fakeIssues) put(it *fakeItem) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.items[issueKey(it.kind, it.number)] = it
}

// remove deletes an item.
func (f *fakeIssues) remove(key string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.items, key)
}

// set changes the fake under its lock.
func (f *fakeIssues) set(change func(*fakeIssues)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	change(f)
}

// seen returns the requests seen since the last call, and the 304s.
func (f *fakeIssues) seen() ([]string, int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out, n := f.requests, f.notMod
	f.requests, f.notMod = nil, 0
	return out, n
}

func (f *fakeIssues) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests = append(f.requests, r.URL.RequestURI())
	f.auth = append(f.auth, r.Header.Get("Authorization"))
	switch {
	case f.limited:
		w.Header().Set("X-RateLimit-Remaining", "0")
		w.Header().Set("X-RateLimit-Reset", "1795000000")
		http.Error(w, "rate limited", http.StatusForbidden)
		return
	case f.status != 0:
		w.WriteHeader(f.status)
		return
	case f.limitPath != "" && strings.Contains(r.URL.Path, f.limitPath):
		w.Header().Set("Retry-After", "30")
		w.WriteHeader(http.StatusTooManyRequests)
		return
	case f.garbagePath != "" && strings.Contains(r.URL.Path, f.garbagePath):
		_, _ = w.Write([]byte("not json"))
		return
	}
	kind, number, rest, ok := f.route(r.URL.EscapedPath())
	switch {
	case !ok:
		http.NotFound(w, r)
	case number == 0:
		f.serveList(w, r, kind)
	case rest == "":
		f.serveItem(w, kind, number)
	default:
		f.serveComments(w, r, kind, number)
	}
}

// route reads a path of the forge's API: the kind of listing ("" for
// GitHub's and Gitea's mixed one), the number of an item, and what follows
// it ("comments", "notes").
func (f *fakeIssues) route(p string) (kind string, number int, rest string, ok bool) {
	prefix := map[string]string{forgeGitHub: "/api/v3/repos/acme/docs/issues", forgeGitea: "/api/v1/repos/acme/docs/issues"}[f.forge]
	if f.forge == forgeGitLab {
		for k, what := range map[string]string{issueKindIssue: "issues", issueKindPull: "merge_requests"} {
			if strings.HasPrefix(p, "/api/v4/projects/acme%2Fdocs/"+what) {
				kind, prefix = k, "/api/v4/projects/acme%2Fdocs/"+what
			}
		}
	}
	tail, found := strings.CutPrefix(p, prefix)
	if prefix == "" || !found {
		return "", 0, "", false
	}
	tail = strings.Trim(tail, "/")
	if tail == "" {
		return kind, 0, "", true
	}
	num, rest, _ := strings.Cut(tail, "/")
	n, err := strconv.Atoi(num)
	return kind, n, rest, err == nil
}

// lookup is an item of a kind ("" for either, GitHub's shared numbers).
func (f *fakeIssues) lookup(kind string, number int) *fakeItem {
	for _, k := range []string{issueKindIssue, issueKindPull} {
		if it := f.items[issueKey(k, number)]; it != nil && (kind == "" || kind == k) {
			return it
		}
	}
	return nil
}

// serveList answers a listing: filtered by state, kind and since, oldest
// change first, one page at a time.
func (f *fakeIssues) serveList(w http.ResponseWriter, r *http.Request, kind string) {
	q := r.URL.Query()
	since := q.Get("since") + q.Get("updated_after")
	state := q.Get("state")
	if kind == "" && q.Get("type") != "" {
		kind = strings.TrimSuffix(q.Get("type"), "s")
	}
	var list []*fakeItem
	for _, it := range f.items {
		open := it.state == issueStateOpen
		switch {
		case kind != "" && it.kind != kind, since != "" && it.updated < since,
			(state == issueStateOpen || state == "opened") && !open, state == issueStateClosed && open:
			continue
		}
		list = append(list, it)
	}
	sort.Slice(list, func(i, j int) bool {
		return list[i].updated < list[j].updated || (list[i].updated == list[j].updated && list[i].number < list[j].number)
	})
	size, _ := strconv.Atoi(q.Get("per_page") + q.Get("limit"))
	if f.perPage > 0 {
		size = f.perPage
	}
	page, _ := strconv.Atoi(q.Get("page"))
	page = max(page, 1)
	start, end := min((page-1)*size, len(list)), min(page*size, len(list))
	out := make([]map[string]any, 0, end-start)
	for _, it := range list[start:end] {
		out = append(out, f.shape(it))
	}
	if end < len(list) {
		q.Set("page", strconv.Itoa(page+1))
		w.Header().Set("Link", fmt.Sprintf(`<%s%s?%s>; rel="next", <%s/last>; rel="last"`, f.URL, r.URL.EscapedPath(), q.Encode(), f.URL))
	}
	f.writeJSON(w, r, out)
}

// serveItem answers one item.
func (f *fakeIssues) serveItem(w http.ResponseWriter, kind string, number int) {
	it := f.lookup(kind, number)
	if it == nil {
		http.NotFound(w, nil)
		return
	}
	f.writeJSON(w, nil, f.shape(it))
}

// serveComments answers an item's comments, two to a page.
func (f *fakeIssues) serveComments(w http.ResponseWriter, r *http.Request, kind string, number int) {
	it := f.lookup(kind, number)
	if it == nil {
		http.NotFound(w, r)
		return
	}
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	page = max(page, 1)
	start, end := min((page-1)*2, len(it.comments)), min(page*2, len(it.comments))
	out := []map[string]any{}
	for _, c := range it.comments[start:end] {
		user := map[string]any{"login": c.author}
		if f.forge == forgeGitLab {
			out = append(out, map[string]any{"author": map[string]any{"username": c.author}, "body": c.body,
				"created_at": c.created, "system": c.system})
			continue
		}
		out = append(out, map[string]any{"user": user, "body": c.body, "created_at": c.created})
	}
	if end < len(it.comments) {
		w.Header().Set("Link", fmt.Sprintf(`<%s%s?page=%d>; rel="next"`, f.URL, r.URL.EscapedPath(), page+1))
	}
	f.writeJSON(w, nil, out)
}

// shape is an item as the forge sends it.
func (f *fakeIssues) shape(it *fakeItem) map[string]any {
	closed := any(nil)
	if it.state == issueStateClosed {
		closed = it.updated
	}
	if f.forge == forgeGitLab {
		web := f.URL + "/acme/docs/-/issues/"
		state := map[string]string{issueStateOpen: "opened", issueStateClosed: "closed"}[it.state]
		m := map[string]any{"iid": it.number, "id": 1000 + it.number, "title": it.title, "description": it.body,
			"labels": it.labels, "author": map[string]any{"username": "alice"}, "assignees": []any{map[string]any{"username": "bob"}},
			"created_at": "2026-01-01T10:00:00.000Z", "updated_at": it.updated, "closed_at": closed,
			"user_notes_count": len(it.comments), "milestone": map[string]any{"title": "v1"}}
		if it.kind == issueKindPull {
			web = f.URL + "/acme/docs/-/merge_requests/"
			if it.merged {
				state, m["merged_at"] = "merged", it.updated
			}
		}
		m["state"], m["web_url"] = state, web+strconv.Itoa(it.number)
		return m
	}
	labels := []any{}
	for _, l := range it.labels {
		labels = append(labels, map[string]any{"name": l})
	}
	m := map[string]any{"number": it.number, "title": it.title, "body": it.body, "state": it.state,
		"labels": labels, "user": map[string]any{"login": "alice"}, "assignees": []any{map[string]any{"login": "bob"}},
		"created_at": "2026-01-01T10:00:00Z", "updated_at": it.updated, "closed_at": closed, "comments": len(it.comments),
		"html_url": f.URL + "/acme/docs/issues/" + strconv.Itoa(it.number), "milestone": nil}
	if it.kind == issueKindPull {
		m["html_url"] = f.URL + "/acme/docs/pull/" + strconv.Itoa(it.number)
		merged := any(nil)
		if it.merged {
			merged = it.updated
		}
		m["pull_request"] = map[string]any{"merged_at": merged, "merged": it.merged}
	}
	return m
}

// writeJSON writes v with an ETag, or 304 when the request names it.
func (f *fakeIssues) writeJSON(w http.ResponseWriter, r *http.Request, v any) {
	data, _ := json.Marshal(v)
	sum := sha256.Sum256(data)
	etag := `"` + hex.EncodeToString(sum[:8]) + `"`
	if r != nil && r.Header.Get("If-None-Match") == etag {
		f.notMod++
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("ETag", etag)
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(data)
}

// issuesAction is an issues sync of the fake forge's repository.
func issuesAction(f *fakeIssues) *SyncAction {
	return &SyncAction{Source: SyncSourceIssues, URL: f.repoURL(), Collection: "repo-issues", Lang: "en",
		GitSyncFields: GitSyncFields{Forge: f.forge, Include: []string{includeIssues, includePulls}}}
}
