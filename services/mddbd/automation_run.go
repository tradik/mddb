package main

import (
	"errors"
	"sync"
	"time"
)

// Running a cron rule's action now, and what its last run did (#377).
//
// A sync (INT-022) or backup (OPS-023) rule runs on its schedule. RunNow runs
// it at once through the same code the schedule fires (runAction): a run of
// the same rule still going skips the new one, and the run is logged in the
// automation log, announced over the jobs SSE stream and fires its webhook.
// REST (POST /v1/automation/{id}/run), gRPC (RunAutomation) and MCP
// (run_automation) all call RunNow.

// Cron rule actions that can be run now.
const (
	actionSync   = "sync"
	actionBackup = "backup"
)

// automationRunStarted is the status of a run RunNow started.
const automationRunStarted = "started"

var (
	// errRuleNotFound: no rule has the id.
	errRuleNotFound = errors.New("automation rule not found")
	// errRuleNotRunnable: the rule has no action that can run now.
	errRuleNotRunnable = errors.New("only a cron rule with a sync or backup can be run now")
	// errActionRunning: the previous run of the rule has not finished.
	errActionRunning = errors.New("the previous run of this rule is still going")
	// errSchedulerOff: the server runs no cron scheduler.
	errSchedulerOff = errors.New("the cron scheduler is not running")
)

// AutomationRun is a run RunNow started.
type AutomationRun struct {
	RuleID string `json:"ruleId"`
	// Action is "sync" or "backup".
	Action string `json:"action"`
	// Status is "started".
	Status string `json:"status"`
	// JobID is the run's id on the jobs SSE stream (GET /v1/jobs/stream).
	JobID     string `json:"jobId"`
	StartedAt int64  `json:"startedAt"`
}

// AutomationRunResult is what the latest run of a cron rule's sync or backup
// did, read from the automation log. Listings set it; it is never stored.
type AutomationRunResult struct {
	// At is when the run started (unix seconds).
	At int64 `json:"at"`
	// Status is "success", "error" or "skipped".
	Status     string `json:"status"`
	DurationMs int64  `json:"durationMs"`
	Error      string `json:"error,omitempty"`
	// Added … Failed are a sync's counts; zero for a backup.
	Added     int `json:"added"`
	Updated   int `json:"updated"`
	Unchanged int `json:"unchanged"`
	Deleted   int `json:"deleted"`
	Failed    int `json:"failed"`
}

// cronAction names a rule's runnable action and the set of its runs in
// flight; "" when it has none.
func (r *AutomationRule) cronAction() (string, *sync.Map) {
	switch {
	case r.Type != "cron":
		return "", nil
	case r.Sync != nil:
		return actionSync, &syncRuns
	case r.Backup != nil:
		return actionBackup, &backupRuns
	}
	return "", nil
}

// actionJobID is the jobs SSE id of a rule's run at now.
func actionJobID(action, ruleID string, now time.Time) string {
	return action + "-" + ruleID + "-" + now.UTC().Format(backupStampLayout)
}

// RunNow starts rule ruleID's sync or backup now and returns the run. A rule
// whose previous run is still going gets errActionRunning, and the skip is
// logged by the run path as a scheduled skip is. A disabled rule runs: a
// manual run is how a rule is tried before it is enabled.
func (cs *CronScheduler) RunNow(ruleID string) (*AutomationRun, error) {
	rule := cs.server.AutomationManager.stored(ruleID)
	if rule == nil {
		return nil, errRuleNotFound
	}
	action, inFlight := rule.cronAction()
	if action == "" {
		return nil, errRuleNotRunnable
	}
	now := cs.now()
	_, busy := inFlight.Load(rule.ID)
	// Even a run that will skip goes through the run path, which logs the
	// skip; it never blocks the caller.
	cs.runs.Add(1)
	go func() {
		defer cs.runs.Done()
		cs.runAction(*rule, now)
	}()
	if busy {
		return nil, errActionRunning
	}
	return &AutomationRun{
		RuleID:    rule.ID,
		Action:    action,
		Status:    automationRunStarted,
		JobID:     actionJobID(action, rule.ID, now),
		StartedAt: now.Unix(),
	}, nil
}

// runAutomationNow is RunNow behind the server's own check that a scheduler
// runs, for the REST, gRPC and MCP entry points.
func (s *Server) runAutomationNow(ruleID string) (*AutomationRun, error) {
	if s.CronScheduler == nil {
		return nil, errSchedulerOff
	}
	return s.CronScheduler.RunNow(ruleID)
}

// automationRules lists the rules of filterType ("" for every type) that act
// on collection ("" for any): the triggers watching it and the syncs writing
// to it. Each sync and backup carries its last result.
func (s *Server) automationRules(filterType, collection string) []AutomationRule {
	rules := s.AutomationManager.List(filterType)
	out := rules[:0]
	for _, r := range rules {
		if collection != "" && !r.actsOn(collection) {
			continue
		}
		r.LastResult = s.lastRunResult(&r)
		out = append(out, r)
	}
	return out
}

// actsOn reports whether a rule watches or writes collection.
func (r *AutomationRule) actsOn(collection string) bool {
	return r.Collection == collection || (r.Sync != nil && r.Sync.Collection == collection)
}

// lastRunResult reads the newest automation log entry of a sync or backup
// rule; nil for other rules and for one that has not run.
func (s *Server) lastRunResult(r *AutomationRule) *AutomationRunResult {
	if action, _ := r.cronAction(); action == "" || s.AutomationLogStore == nil {
		return nil
	}
	logs, _, err := s.AutomationLogStore.List(1, "", r.ID, "")
	if err != nil || len(logs) == 0 {
		return nil
	}
	e := logs[0]
	res := &AutomationRunResult{At: e.Timestamp, Status: e.Status, DurationMs: e.DurationMs, Error: e.Error}
	if c := e.Sync; c != nil {
		res.Added, res.Updated, res.Unchanged, res.Deleted, res.Failed =
			c.Added, c.Updated, c.Unchanged, c.Deleted, c.Failed
	}
	return res
}
