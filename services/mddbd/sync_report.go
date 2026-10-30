package main

import (
	"errors"
	"time"

	"mddb/internal/automationlog"
	"mddb/internal/webhooks"
)

// How a sync run (INT-022) reports what it did: an automation log entry and
// the source.synced webhook event. The SSE job is announced by the run.

// logSync records a run in the automation log.
func (s *Server) logSync(rule AutomationRule, start time.Time, out syncOutcome, err error) {
	if s.AutomationLogStore == nil {
		return
	}
	counts := out.counts
	entry := automationlog.Entry{
		Timestamp:  start.Unix(),
		RuleID:     rule.ID,
		RuleName:   rule.Name,
		RuleType:   rule.Type,
		Status:     "success",
		DurationMs: time.Since(start).Milliseconds(),
		Attempt:    1,
		Action:     "sync",
		Target:     rule.Sync.describe(),
		Sync:       &counts,
	}
	switch {
	case errors.Is(err, errSyncRunning):
		entry.Status, entry.Error = "skipped", err.Error()
	case err != nil:
		entry.Status, entry.Error = "error", err.Error()
	}
	_ = s.AutomationLogStore.Log(entry)
}

// fireSourceSynced fires source.synced with what the run did.
func (s *Server) fireSourceSynced(rule AutomationRule, out syncOutcome, err error) {
	if s.WebhookManager == nil {
		return
	}
	payload := map[string]interface{}{
		"ruleId":     rule.ID,
		"ruleName":   rule.Name,
		"collection": rule.Sync.Collection,
		"source":     rule.Sync.describe(),
		"added":      out.counts.Added,
		"updated":    out.counts.Updated,
		"unchanged":  out.counts.Unchanged,
		"deleted":    out.counts.Deleted,
		"failed":     out.counts.Failed,
	}
	if err != nil {
		payload["error"] = err.Error()
		payload["errors"] = out.errors
	}
	s.WebhookManager.FireEvent(webhooks.EventSourceSynced, payload)
}
