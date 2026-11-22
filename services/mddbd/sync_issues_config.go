package main

import (
	"fmt"
	"slices"
	"strings"
	"time"
)

// The settings of an issues source (INT-023) and their validation. An
// issues source mirrors a forge repository's issues and pull requests into a
// collection, one document per item, and never writes to the forge.
// sync_issues.go runs it. It shares url, forge, tokenSecretRef,
// hookSecretRef and repositoryFrom with a git source (GitSyncFields), and
// reads include as the kinds of item to mirror.

// IssuesSyncFields are the fields only an issues source reads.
type IssuesSyncFields struct {
	// State is "open", "closed" or "all" (the default).
	State string `json:"state,omitempty"`
	// Since keeps items last updated before it out: a date (2026-01-02) or
	// an RFC 3339 time.
	Since string `json:"since,omitempty"`
	// Comments appends each comment to its item's document; nil is true.
	Comments *bool `json:"comments,omitempty"`
}

// Kinds of item, which are also the prefixes of their keys.
const (
	issueKindIssue = "issue"
	issueKindPull  = "pull"
)

// The values of include.
const (
	includeIssues = "issues"
	includePulls  = "pulls"
)

// The values of state.
const (
	issueStateOpen   = "open"
	issueStateClosed = "closed"
	issueStateAll    = "all"
)

// checkIssues validates an issues source: the forge fields, a state, a
// since that parses, and include naming kinds of item.
func (a *SyncAction) checkIssues() error {
	if err := a.checkForge(); err != nil {
		return err
	}
	i := &a.IssuesSyncFields
	i.State = strings.ToLower(strings.TrimSpace(i.State))
	switch i.State {
	case "":
		i.State = issueStateAll
	case issueStateOpen, issueStateClosed, issueStateAll:
	default:
		return fmt.Errorf("sync state %q: open, closed or all", i.State)
	}
	i.Since = strings.TrimSpace(i.Since)
	if _, err := parseIssuesSince(i.Since); err != nil {
		return err
	}
	if len(a.Include) == 0 {
		a.Include = []string{includeIssues}
	}
	var kinds []string
	for _, k := range a.Include {
		k = strings.ToLower(strings.TrimSpace(k))
		if k != includeIssues && k != includePulls {
			return fmt.Errorf("sync include %q: an issues sync includes issues, pulls or both", k)
		}
		if !slices.Contains(kinds, k) {
			kinds = append(kinds, k)
		}
	}
	a.Include = kinds
	return nil
}

// parseIssuesSince reads since: zero for none.
func parseIssuesSince(since string) (time.Time, error) {
	if since == "" {
		return time.Time{}, nil
	}
	if t, err := time.Parse(time.DateOnly, since); err == nil {
		return t, nil
	}
	t, err := time.Parse(time.RFC3339, since)
	if err != nil {
		return time.Time{}, fmt.Errorf("sync since %q: a date (2026-01-02) or an RFC 3339 time", since)
	}
	return t.UTC(), nil
}

// includes reports whether an issues source mirrors kind.
func (a *SyncAction) includes(kind string) bool {
	want := includeIssues
	if kind == issueKindPull {
		want = includePulls
	}
	return slices.Contains(a.Include, want)
}

// withComments reports whether comments are mirrored.
func (a *SyncAction) withComments() bool { return a.Comments == nil || *a.Comments }
