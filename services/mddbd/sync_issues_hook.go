package main

import (
	"errors"
	"net/http"
	"time"

	json "mddb/internal/jsonx"
)

// A forge's webhook for an issues sync (INT-023): an event about an issue,
// a comment or a pull request refreshes the one document of that item,
// verified like a git sync's hook (automation_hook.go). The events are
// GitHub's, Gitea's and Forgejo's issues, issue_comment and pull_request
// (and their pull_request_comment), and GitLab's Issue, Note and Merge
// Request Hook; an event that names no item runs an incremental pass.

// hookPayload is what the forges' payloads say about the item.
type hookPayload struct {
	ObjectKind string `json:"object_kind"` // GitLab
	Issue      *struct {
		Number      int             `json:"number"`
		IID         int             `json:"iid"`
		PullRequest json.RawMessage `json:"pull_request"`
	} `json:"issue"`
	IsPull      bool `json:"is_pull"` // Gitea's comment on a pull request
	PullRequest *struct {
		Number int `json:"number"`
	} `json:"pull_request"`
	ObjectAttributes *struct {
		IID int `json:"iid"`
	} `json:"object_attributes"`
	MergeRequest *struct {
		IID int `json:"iid"`
	} `json:"merge_request"`
}

// issueTargetFromHook reads the item an event is about; false when it names
// none.
func issueTargetFromHook(body []byte) (issueTarget, bool) {
	var p hookPayload
	if json.Unmarshal(body, &p) != nil {
		return issueTarget{}, false
	}
	var t issueTarget
	switch {
	case p.ObjectKind == "issue" && p.ObjectAttributes != nil:
		t = issueTarget{issueKindIssue, p.ObjectAttributes.IID}
	case p.ObjectKind == "merge_request" && p.ObjectAttributes != nil:
		t = issueTarget{issueKindPull, p.ObjectAttributes.IID}
	case p.ObjectKind == "note" && p.MergeRequest != nil:
		t = issueTarget{issueKindPull, p.MergeRequest.IID}
	case p.ObjectKind == "note" && p.Issue != nil:
		t = issueTarget{issueKindIssue, p.Issue.IID}
	case p.PullRequest != nil:
		t = issueTarget{issueKindPull, p.PullRequest.Number}
	case p.Issue != nil:
		t = issueTarget{issueKindIssue, p.Issue.Number}
		if p.IsPull || (len(p.Issue.PullRequest) > 0 && string(p.Issue.PullRequest) != "null") {
			t.kind = issueKindPull
		}
	}
	return t, t.number > 0
}

// issueHookResult is the hook's answer for one refreshed item.
type issueHookResult struct {
	RuleID    string `json:"ruleId"`
	Key       string `json:"key"`
	Status    string `json:"status"` // "success" or "error"
	Error     string `json:"error,omitempty"`
	Added     int    `json:"added"`
	Updated   int    `json:"updated"`
	Unchanged int    `json:"unchanged"`
	Deleted   int    `json:"deleted"`
}

// issueHook refreshes the item an event names: 200 with what the refresh
// did, 409 while a run of the rule is going.
func (s *Server) issueHook(rule AutomationRule, t issueTarget) (int, any) {
	out, err := s.runSync(rule, time.Now(), &t)
	if errors.Is(err, errSyncRunning) {
		return http.StatusConflict, hookError(err.Error())
	}
	c := out.counts
	res := issueHookResult{RuleID: rule.ID, Key: issueKey(t.kind, t.number), Status: "success",
		Added: c.Added, Updated: c.Updated, Unchanged: c.Unchanged, Deleted: c.Deleted}
	if err != nil {
		res.Status, res.Error = "error", err.Error()
	}
	return http.StatusOK, res
}
