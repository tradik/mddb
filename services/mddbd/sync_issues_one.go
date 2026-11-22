package main

import (
	"cmp"
	"context"
	"errors"
	"fmt"

	json "mddb/internal/jsonx"
)

// A refresh of one item of an issues source (INT-023): a webhook's event or
// sync_refresh with a number. It reads that item alone — on GitLab, which
// numbers issues and merge requests apart, either — and lists an item the
// forge answers 404 for under its keys, so fetch can tell its document is
// gone. sync_issues.go holds the rest of the source.

// listOne reads the one item a refresh names. An item the forge no longer
// has is listed under its key, so fetch can tell its document is gone.
func (g *issuesSyncSource) listOne(ctx context.Context) ([]syncItem, bool, error) {
	kinds := []string{g.only.kind}
	if g.only.kind == "" && g.repo.forge == forgeGitLab { // numbered apart: either
		kinds = []string{issueKindIssue, issueKindPull}
	}
	var items []syncItem
	for _, k := range kinds {
		if k != "" && !g.action.includes(k) {
			continue
		}
		page, err := g.get(ctx, g.repo.issueURL(cmp.Or(k, issueKindIssue), g.only.number), "")
		switch {
		case errors.Is(err, errSourceGone):
			items = append(items, g.goneItems(k)...)
			continue
		case err != nil:
			return nil, false, err
		}
		var f forgeIssue
		if err := json.Unmarshal(page.body, &f); err != nil {
			return nil, false, fmt.Errorf("reading item %d: %w", g.only.number, err)
		}
		if it, ok := g.keepOne(f.item(gitLabKind(g.repo, k))); ok {
			items = append(items, it)
		}
	}
	return items, false, nil
}

// gitLabKind is the kind a GitLab item is read as: its endpoint's. The
// other forges' items say it themselves.
func gitLabKind(repo gitRepo, kind string) string {
	if repo.forge == forgeGitLab {
		return kind
	}
	return ""
}

// keepOne records a refreshed item of an included kind; its state is not
// filtered — a refresh is asked for that item.
func (g *issuesSyncSource) keepOne(it issueItem) (syncItem, bool) {
	if !g.action.includes(it.kind) {
		return syncItem{}, false
	}
	g.items[it.url] = it
	return syncItem{ref: it.url, key: issueKey(it.kind, it.number)}, true
}

// goneItems are the keys an item the forge answered 404 for may have had.
func (g *issuesSyncSource) goneItems(kind string) []syncItem {
	var out []syncItem
	for _, k := range []string{issueKindIssue, issueKindPull} {
		if (kind == "" || kind == k) && g.action.includes(k) {
			key := issueKey(k, g.only.number)
			out = append(out, syncItem{ref: key, key: key})
		}
	}
	return out
}
