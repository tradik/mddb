package main

import (
	"errors"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"sync"
	"time"

	json "mddb/internal/jsonx"
)

// Refreshing a synced collection on demand (INT-023): POST /v1/sync/refresh
// and the sync_refresh MCP tool, {collection, number?}. An agent that has
// just commented on an issue refreshes the mirror before it searches it.
//
// It is gated as a write of the collection, not as administration: a key
// limited to the collection may call it, and it runs only the sync rules
// that write to that collection — it never opens run_automation, which
// runs any rule. With number it refreshes that one item of each issues
// rule (a request or two to the forge, synchronously); without, it starts
// an incremental run of each rule, as run_automation would. Each rule is
// refreshed at most once per syncRefreshInterval, and the answer says when
// it last ran.

// syncRefreshInterval is how often one rule may be refreshed on demand.
const syncRefreshInterval = 30 * time.Second

// syncRefreshPath is the route.
const syncRefreshPath = "/v1/sync/refresh"

var (
	// errNoSyncRule: no sync rule writes to the collection.
	errNoSyncRule = errors.New("no sync rule writes to this collection")
	// errSyncRefreshThrottled: every rule was refreshed too recently.
	errSyncRefreshThrottled = errors.New("refreshed less than 30 s ago")
)

// syncRefreshes holds when each rule was last refreshed on demand.
var syncRefreshes sync.Map

// syncRefreshNow is the refresh's clock. A test replaces it.
var syncRefreshNow = time.Now

// SyncRefreshRequest is the body of POST /v1/sync/refresh.
type SyncRefreshRequest struct {
	Collection string `json:"collection"`
	// Number is the issue or pull request to refresh; 0 runs a pass.
	Number int `json:"number,omitempty"`
}

// SyncRefreshResult is what a refresh did, per rule.
type SyncRefreshResult struct {
	Collection string           `json:"collection"`
	Number     int              `json:"number,omitempty"`
	Runs       []SyncRefreshRun `json:"runs"`
}

// SyncRefreshRun is one rule's refresh.
type SyncRefreshRun struct {
	RuleID string `json:"ruleId"`
	// Status is "done" (one item refreshed), "started" (a pass is running,
	// JobID on the jobs stream), "throttled", "running" (a run of the rule
	// is going) or "error".
	Status string `json:"status"`
	// LastRunAt is when the rule last ran before this refresh (unix
	// seconds); 0 when it never has.
	LastRunAt int64 `json:"lastRunAt,omitempty"`
	// RetryAfter is the seconds until a throttled rule may be refreshed.
	RetryAfter int    `json:"retryAfter,omitempty"`
	JobID      string `json:"jobId,omitempty"`
	Error      string `json:"error,omitempty"`
	Added      int    `json:"added"`
	Updated    int    `json:"updated"`
	Unchanged  int    `json:"unchanged"`
	Deleted    int    `json:"deleted"`
	Failed     int    `json:"failed"`
}

// syncRefresh refreshes the sync rules writing to collection.
func (s *Server) syncRefresh(collection string, number int) (*SyncRefreshResult, error) {
	if s.AutomationManager == nil {
		return nil, errors.New(automationNotInitialized)
	}
	if number < 0 {
		return nil, errors.New("number must be a positive issue or pull request number")
	}
	collection = s.Aliases.Resolve(collection)
	rules := s.refreshRules(collection, number > 0)
	if len(rules) == 0 {
		return nil, fmt.Errorf("%w: %s", errNoSyncRule, collection)
	}
	res := &SyncRefreshResult{Collection: collection, Number: number}
	var throttled error
	for _, rule := range rules {
		run := s.refreshRule(rule, number)
		if run.Status == "throttled" && throttled == nil {
			throttled = fmt.Errorf("%w: the last run of rule %s was at %s; try again in %d s", errSyncRefreshThrottled,
				rule.ID, time.Unix(run.LastRunAt, 0).UTC().Format(time.RFC3339), run.RetryAfter)
		}
		res.Runs = append(res.Runs, run)
	}
	for _, run := range res.Runs {
		if run.Status != "throttled" {
			return res, nil
		}
	}
	return res, throttled
}

// refreshRules are the sync rules writing to collection, as stored; only
// the issues rules when one item is asked for.
func (s *Server) refreshRules(collection string, oneItem bool) []AutomationRule {
	var out []AutomationRule
	for _, r := range s.AutomationManager.List("cron") {
		if r.Sync == nil || r.Sync.Collection != collection || (oneItem && r.Sync.Source != SyncSourceIssues) {
			continue
		}
		if stored := s.AutomationManager.stored(r.ID); stored != nil {
			out = append(out, *stored)
		}
	}
	return out
}

// refreshRule refreshes one rule unless it was refreshed under
// syncRefreshInterval ago.
func (s *Server) refreshRule(rule AutomationRule, number int) SyncRefreshRun {
	run := SyncRefreshRun{RuleID: rule.ID}
	if last := s.lastRunResult(&rule); last != nil {
		run.LastRunAt = last.At
	}
	now := syncRefreshNow()
	if prev, ok := syncRefreshes.Load(rule.ID); ok {
		if wait := syncRefreshInterval - now.Sub(prev.(time.Time)); wait > 0 {
			run.Status, run.RetryAfter = "throttled", int(math.Ceil(wait.Seconds()))
			if run.LastRunAt == 0 {
				run.LastRunAt = prev.(time.Time).Unix()
			}
			return run
		}
	}
	syncRefreshes.Store(rule.ID, now)
	if number > 0 {
		out, err := s.runSync(rule, now, &issueTarget{number: number})
		c := out.counts
		run.Added, run.Updated, run.Unchanged, run.Deleted, run.Failed = c.Added, c.Updated, c.Unchanged, c.Deleted, c.Failed
		run.Status = refreshStatus(err, "done")
		if err != nil && run.Status == "error" {
			run.Error = err.Error()
		}
		return run
	}
	started, err := s.runAutomationNow(rule.ID)
	run.Status = refreshStatus(err, "started")
	switch {
	case err == nil:
		run.JobID = started.JobID
	case run.Status == "error":
		run.Error = err.Error()
	}
	return run
}

// refreshStatus is a refresh's status: ok, "running" while a run of the
// rule is going, else "error".
func refreshStatus(err error, ok string) string {
	switch {
	case err == nil:
		return ok
	case errors.Is(err, errSyncRunning), errors.Is(err, errActionRunning):
		return "running"
	}
	return "error"
}

// handleSyncRefresh serves POST /v1/sync/refresh: 200 with the runs, 404
// when no sync rule writes to the collection, 429 (Retry-After) when every
// rule was refreshed too recently.
func (s *Server) handleSyncRefresh(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	var req SyncRefreshRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Collection == "" {
		bad(w, errors.New(`the body must be {"collection": ..., "number": ...}`))
		return
	}
	s.Metrics.IncOp("sync_refresh", req.Collection)
	res, err := s.syncRefresh(req.Collection, req.Number)
	switch {
	case errors.Is(err, errNoSyncRule):
		writeJSONError(w, http.StatusNotFound, map[string]any{"error": err.Error()})
	case errors.Is(err, errSyncRefreshThrottled):
		w.Header().Set("Retry-After", strconv.Itoa(res.Runs[0].RetryAfter))
		writeJSONError(w, http.StatusTooManyRequests, map[string]any{"error": err.Error(), "runs": res.Runs})
	case err != nil:
		bad(w, err)
	default:
		ok(w, res)
	}
}
