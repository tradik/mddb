package main

import (
	"net/url"
	"strconv"
	"time"

	json "mddb/internal/jsonx"
)

// What an issues source (INT-023) asks each forge, and how it reads the
// answers into one shape. GitHub and Gitea (Forgejo) list issues and pull
// requests together, numbered alike, and say which is which; GitLab keeps
// issues and merge requests apart, each numbered on its own (iid). Comments
// are GitHub's and Gitea's comments, GitLab's notes without the system ones.

// Page sizes of the listings: each forge's maximum.
const (
	gitHubPerPage = 100
	giteaPerPage  = 50
)

// issueListing is one listing an issues source reads: its first page, and
// the kind of every item on it ("" when the items say it themselves).
type issueListing struct {
	url  string
	kind string
}

// issueListings are the listings of the items updated since since ("" for
// all of them).
func (r gitRepo) issueListings(a *SyncAction, since string) []issueListing {
	switch r.forge {
	case forgeGitLab:
		var out []issueListing
		for _, k := range []string{issueKindIssue, issueKindPull} {
			if a.includes(k) {
				out = append(out, issueListing{url: r.gitLabItems(k) + "?" + gitLabListQuery(a.State, k, since), kind: k})
			}
		}
		return out
	case forgeGitea:
		q := url.Values{"state": {a.State}, "limit": {strconv.Itoa(giteaPerPage)}}
		if len(a.Include) == 1 {
			q.Set("type", a.Include[0])
		}
		if since != "" {
			q.Set("since", since)
		}
		return []issueListing{{url: r.repoAPI() + "/issues?" + q.Encode()}}
	}
	q := url.Values{"state": {a.State}, "sort": {"updated"}, "direction": {"asc"}, "per_page": {strconv.Itoa(gitHubPerPage)}}
	if since != "" {
		q.Set("since", since)
	}
	return []issueListing{{url: r.repoAPI() + "/issues?" + q.Encode()}}
}

// gitLabListQuery is the query of a GitLab listing, oldest change first. A
// merge request closed by merging is "merged", not "closed": closed ones are
// listed with all and kept by state after reading.
func gitLabListQuery(state, kind, since string) string {
	switch {
	case state == issueStateOpen:
		state = "opened"
	case kind == issueKindPull:
		state = issueStateAll
	}
	q := url.Values{"state": {state}, "order_by": {"updated_at"}, "sort": {"asc"}, "per_page": {strconv.Itoa(gitHubPerPage)}}
	if since != "" {
		q.Set("updated_after", since)
	}
	return q.Encode()
}

// gitLabItems is the collection of a kind in GitLab's API.
func (r gitRepo) gitLabItems(kind string) string {
	what := "/issues"
	if kind == issueKindPull {
		what = "/merge_requests"
	}
	return r.apiBase() + "/projects/" + r.gitLabProject() + what
}

// issueURL is one item in the forge's API.
func (r gitRepo) issueURL(kind string, number int) string {
	if r.forge == forgeGitLab {
		return r.gitLabItems(kind) + "/" + strconv.Itoa(number)
	}
	return r.repoAPI() + "/issues/" + strconv.Itoa(number)
}

// commentsURL is the first page of an item's comments.
func (r gitRepo) commentsURL(kind string, number int) string {
	switch r.forge {
	case forgeGitLab:
		return r.issueURL(kind, number) + "/notes?sort=asc&order_by=created_at&per_page=" + strconv.Itoa(gitHubPerPage)
	case forgeGitea:
		return r.issueURL(kind, number) + "/comments"
	}
	return r.issueURL(kind, number) + "/comments?per_page=" + strconv.Itoa(gitHubPerPage)
}

// issueItem is one issue or pull request, as every forge's answer is read.
type issueItem struct {
	kind, title, body, state, stateReason string
	author, milestone, url                string
	createdAt, updatedAt, closedAt        string
	number, comments                      int
	labels, assignees                     []string
}

// issueComment is one comment.
type issueComment struct{ author, createdAt, body string }

// forgeUser is a user: GitHub's and Gitea's login, GitLab's username.
type forgeUser struct {
	Login    string `json:"login"`
	Username string `json:"username"`
}

func (u *forgeUser) name() string {
	if u == nil {
		return ""
	}
	if u.Login != "" {
		return u.Login
	}
	return u.Username
}

// forgeLabels are labels: objects with a name (GitHub, Gitea) or names
// (GitLab).
type forgeLabels []string

// UnmarshalJSON reads either shape; anything else is no label.
func (l *forgeLabels) UnmarshalJSON(b []byte) error {
	var raw []json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil {
		return nil // null, or a shape no forge sends
	}
	for _, r := range raw {
		var name string
		if json.Unmarshal(r, &name) != nil {
			var obj struct {
				Name string `json:"name"`
			}
			_ = json.Unmarshal(r, &obj) // an object without a name is skipped below
			name = obj.Name
		}
		if name != "" {
			*l = append(*l, name)
		}
	}
	return nil
}

// forgeIssue is an item as any of the forges sends it.
type forgeIssue struct {
	Number      int         `json:"number"`
	IID         int         `json:"iid"`
	Title       string      `json:"title"`
	Body        string      `json:"body"`
	Description string      `json:"description"`
	State       string      `json:"state"`
	StateReason string      `json:"state_reason"`
	Labels      forgeLabels `json:"labels"`
	Assignees   []forgeUser `json:"assignees"`
	User        *forgeUser  `json:"user"`
	Author      *forgeUser  `json:"author"`
	Milestone   *struct {
		Title string `json:"title"`
	} `json:"milestone"`
	CreatedAt      string  `json:"created_at"`
	UpdatedAt      string  `json:"updated_at"`
	ClosedAt       *string `json:"closed_at"`
	MergedAt       *string `json:"merged_at"`
	Comments       int     `json:"comments"`
	UserNotesCount int     `json:"user_notes_count"`
	HTMLURL        string  `json:"html_url"`
	WebURL         string  `json:"web_url"`
	PullRequest    *struct {
		MergedAt *string `json:"merged_at"`
		Merged   bool    `json:"merged"`
	} `json:"pull_request"`
}

// item reads a forge's item; kind is the listing's, "" when the item says.
func (f *forgeIssue) item(kind string) issueItem {
	it := issueItem{kind: kind, number: f.Number, title: f.Title, body: f.Body, state: f.State,
		stateReason: f.StateReason, author: f.User.name(), url: f.HTMLURL, labels: f.Labels,
		createdAt: forgeTime(f.CreatedAt), updatedAt: forgeTime(f.UpdatedAt), comments: f.Comments}
	merged := f.MergedAt != nil && *f.MergedAt != ""
	if it.kind == "" {
		it.kind = issueKindIssue
		if f.PullRequest != nil {
			it.kind = issueKindPull
			merged = f.PullRequest.Merged || (f.PullRequest.MergedAt != nil && *f.PullRequest.MergedAt != "")
		}
	}
	if f.IID != 0 { // GitLab
		it.number, it.body, it.author, it.url, it.comments = f.IID, f.Description, f.Author.name(), f.WebURL, f.UserNotesCount
	}
	switch it.state {
	case "opened":
		it.state = issueStateOpen
	case "merged", "locked":
		it.state = issueStateClosed
	}
	if merged {
		it.stateReason = "merged"
	}
	for _, u := range f.Assignees {
		it.assignees = append(it.assignees, u.name())
	}
	if f.Milestone != nil {
		it.milestone = f.Milestone.Title
	}
	if f.ClosedAt != nil {
		it.closedAt = forgeTime(*f.ClosedAt)
	}
	return it
}

// forgeComment is a comment as any of the forges sends it.
type forgeComment struct {
	User      *forgeUser `json:"user"`
	Author    *forgeUser `json:"author"`
	Body      string     `json:"body"`
	CreatedAt string     `json:"created_at"`
	System    bool       `json:"system"` // GitLab's notes of events: label added, merged, ...
}

// forgeTime is a forge's timestamp in RFC 3339 seconds, UTC; one that does
// not parse is kept as it came.
func forgeTime(raw string) string {
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return raw
	}
	return t.UTC().Format(time.RFC3339)
}
