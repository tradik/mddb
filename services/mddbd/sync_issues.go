package main

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	"mddb/internal/automationlog"
	json "mddb/internal/jsonx"
)

// A forge's issues and pull requests as a synced source (INT-023). A run
// lists the items updated since the newest one the last successful run read
// (its cursor, a second earlier so an item changed in that second is not
// missed), sending each listing page back with the ETag the last run got for
// it: an unchanged page answers 304 and costs the forge's rate limit
// nothing. An item whose updatedAt is the one its document was written at is
// not fetched again; any other is written with its comments. mddbd never
// writes to the forge.
//
// A rule with prune lists everything on every run — a deletion only shows as
// an item no longer listed — and conditional requests are not sent, since a
// page answered 304 lists nothing. A refresh of one item (a webhook,
// sync_refresh) reads that item alone and leaves the cursor as it was.

// issueTarget is the one item a refresh reads; kind "" is whichever of the
// included kinds the number is.
type issueTarget struct {
	kind   string
	number int
}

// issuesSyncSource reads a repository's issues and pull requests.
type issuesSyncSource struct {
	action      *SyncAction
	repo        gitRepo
	token       string
	fingerprint string
	only        *issueTarget
	// last is what the last successful run left: its cursor and pages.
	last automationlog.SyncCounts
	// cursorAt and pages are this run's, for the next.
	cursorAt string
	pages    automationlog.SyncPages
	items    map[string]issueItem // by ref
	// limited is set when the forge refused a request for its rate limit:
	// the run asks nothing more.
	limited atomic.Bool
}

// newIssuesSyncSource connects a rule's issues source, its action resolved
// (sync_repository.go); only, when set, is the one item to refresh.
func (s *Server) newIssuesSyncSource(rule AutomationRule, only *issueTarget) (*issuesSyncSource, error) {
	a := rule.Sync
	repo, err := parseGitRepo(a.URL, a.Forge)
	if err != nil {
		return nil, err
	}
	g := &issuesSyncSource{action: a, repo: repo, fingerprint: a.fingerprint(), only: only,
		last: s.lastSyncCounts(rule), items: map[string]issueItem{}}
	if a.TokenSecretRef != "" {
		if g.token, err = s.syncSecret(a.TokenSecretRef); err != nil {
			return nil, err
		}
	}
	return g, nil
}

// cursor is where the next run starts.
func (g *issuesSyncSource) cursor() (string, *automationlog.SyncPages) {
	if len(g.pages) == 0 {
		return g.cursorAt, nil
	}
	return g.cursorAt, &g.pages
}

// lastPages are the pages the last run read.
func (g *issuesSyncSource) lastPages() automationlog.SyncPages {
	if g.last.Pages == nil {
		return nil
	}
	return *g.last.Pages
}

// partial reports a refresh of one item: what it does not list still exists.
func (g *issuesSyncSource) partial() bool { return g.only != nil }

// incremental reports whether the run lists only what changed since the
// last: never with prune.
func (g *issuesSyncSource) incremental() bool { return !g.action.Prune }

// list lists the items to sync.
func (g *issuesSyncSource) list(ctx context.Context) ([]syncItem, bool, error) {
	if g.only != nil {
		g.cursorAt, g.pages = g.last.Cursor, g.lastPages()
		return g.listOne(ctx)
	}
	g.cursorAt, g.pages = g.last.Cursor, automationlog.SyncPages{}
	if !g.incremental() {
		g.cursorAt = ""
	}
	since := g.since()
	var items []syncItem
	for _, l := range g.repo.issueListings(g.action, since) {
		more, truncated, err := g.readListing(ctx, l, len(items))
		items = append(items, more...)
		if err != nil || truncated {
			return items, g.reportTruncation(truncated), err
		}
	}
	return items, false, nil
}

// reportTruncation is whether a listing cut at maxDocuments is reported as
// cut. GitHub and GitLab list the oldest change first, so the cursor stops
// where the cut is and the next run goes on from there; Gitea lists in an
// order of its own, so a cut there is reported, as a full listing's is.
func (g *issuesSyncSource) reportTruncation(truncated bool) bool {
	return truncated && (!g.incremental() || g.repo.forge == forgeGitea)
}

// since is the time a listing starts at: the rule's since, or a second
// before the cursor when that is later.
func (g *issuesSyncSource) since() string {
	ruleSince, _ := parseIssuesSince(g.action.Since) // checked by normalize
	if c, err := time.Parse(time.RFC3339, g.cursorAt); err == nil && c.Add(-time.Second).After(ruleSince) {
		return c.Add(-time.Second).UTC().Format(time.RFC3339)
	}
	if ruleSince.IsZero() {
		return ""
	}
	return ruleSince.Format(time.RFC3339)
}

// readListing reads a listing's pages; have is how many items the run
// already has.
func (g *issuesSyncSource) readListing(ctx context.Context, l issueListing, have int) ([]syncItem, bool, error) {
	var items []syncItem
	for next, n := l.url, 0; next != "" && n < maxIssuesPages; n++ {
		listed, pageNext, err := g.readListingPage(ctx, next)
		if err != nil {
			return items, false, err
		}
		var truncated bool
		if items, truncated = g.keepListed(listed, l.kind, have, items); truncated {
			return items, true, nil
		}
		next = pageNext
	}
	return items, false, nil
}

// readListingPage reads one page of a listing, sending the ETag the last
// run kept, and returns its issues and the next page's URL. A page nothing
// on changed since the last run gives no issues and the next page that run
// saw.
func (g *issuesSyncSource) readListingPage(ctx context.Context, pageURL string) ([]forgeIssue, string, error) {
	prev, cached := g.lastPages()[pageURL]
	etag := ""
	if cached && g.incremental() {
		etag = prev.ETag
	}
	page, err := g.get(ctx, pageURL, etag)
	if err != nil {
		return nil, "", fmt.Errorf("listing %s: %w", pageURL, err)
	}
	if page.notModified {
		g.pages[pageURL] = prev
		return nil, prev.Next, nil
	}
	if page.etag != "" && g.incremental() {
		g.pages[pageURL] = automationlog.SyncPage{ETag: page.etag, Next: page.next}
	}
	var listed []forgeIssue
	if err := json.Unmarshal(page.body, &listed); err != nil {
		return nil, "", fmt.Errorf("listing %s: %w", pageURL, err)
	}
	return listed, page.next, nil
}

// keepListed adds the listed issues the rule keeps to items; true when the
// run reached maxDocuments (have is how many it had before this listing)
// with issues left.
func (g *issuesSyncSource) keepListed(listed []forgeIssue, kind string, have int, items []syncItem) ([]syncItem, bool) {
	for i := range listed {
		if have+len(items) == g.action.MaxDocuments {
			return items, true
		}
		if it, ok := g.keep(listed[i].item(kind)); ok {
			items = append(items, it)
		}
	}
	return items, false
}

// keep records an item of an included kind and state, and moves the cursor.
func (g *issuesSyncSource) keep(it issueItem) (syncItem, bool) {
	if !g.action.includes(it.kind) || (g.action.State != issueStateAll && it.state != g.action.State) {
		return syncItem{}, false
	}
	if it.updatedAt > g.cursorAt { // RFC 3339 in UTC sorts as text
		g.cursorAt = it.updatedAt
	}
	g.items[it.url] = it
	return syncItem{ref: it.url, key: issueKey(it.kind, it.number)}, true
}

// issueKey is the document key of an item: issue-<n> or pull-<n>.
func issueKey(kind string, number int) string { return fmt.Sprintf("%s-%d", kind, number) }

// fetch builds an item's document unless the item has not changed since its
// document was written: the same updatedAt under the same rule. A refresh
// rebuilds it whatever: a comment edited does not always move updatedAt.
func (g *issuesSyncSource) fetch(ctx context.Context, it syncItem, prev syncState) (syncFetched, error) {
	item, listed := g.items[it.ref]
	switch {
	case !listed && g.action.Prune && prev.hash != "":
		return syncFetched{}, errSourceGone
	case !listed: // gone, and kept (no prune) or never written
		return syncFetched{notModified: true}, nil
	}
	version := g.version(item)
	if g.only == nil && version == prev.etag {
		return syncFetched{notModified: true}, nil
	}
	var comments []issueComment
	if g.action.withComments() && item.comments > 0 {
		var err error
		if comments, err = g.readComments(ctx, item); err != nil {
			return syncFetched{}, err
		}
	}
	body, meta := renderIssue(item, comments, g.repo)
	return syncFetched{body: []byte(body), format: "md", noFrontmatter: true, etag: version,
		hash: issueHash(body, meta), meta: meta}, nil
}

// version identifies what an item's document was built from: the item's
// updatedAt under the rule's settings. It is kept as the document's
// syncEtag.
func (g *issuesSyncSource) version(it issueItem) string {
	sum := sha256.Sum256([]byte(it.kind + "|" + fmt.Sprint(it.number) + "|" + it.updatedAt + "|" + g.fingerprint))
	return hex.EncodeToString(sum[:8])
}

// issueHash is the hash a document is compared by: its body and meta, so a
// label changed is written even when the text is not.
func issueHash(body string, meta map[string][]string) string {
	data, _ := json.Marshal(meta) // a map of strings: it encodes, keys sorted
	sum := sha256.Sum256(append([]byte(body+"\x00"), data...))
	return hex.EncodeToString(sum[:])
}

// readComments reads an item's comments, oldest first; GitLab's system
// notes are left out.
func (g *issuesSyncSource) readComments(ctx context.Context, it issueItem) ([]issueComment, error) {
	if g.limited.Load() {
		return nil, errSyncRateLimited
	}
	var out []issueComment
	for next, n := g.repo.commentsURL(it.kind, it.number), 0; next != "" && n < maxIssuesPages; n++ {
		page, err := g.get(ctx, next, "")
		if err != nil {
			if errors.Is(err, errSyncRateLimited) {
				g.limited.Store(true)
			}
			return nil, fmt.Errorf("reading the comments: %w", err)
		}
		var list []forgeComment
		if err := json.Unmarshal(page.body, &list); err != nil {
			return nil, fmt.Errorf("reading the comments: %w", err)
		}
		for _, c := range list {
			if !c.System {
				out = append(out, issueComment{author: cmp.Or(c.User.name(), c.Author.name()),
					createdAt: forgeTime(c.CreatedAt), body: c.Body})
			}
		}
		next = page.next
	}
	return out, nil
}
