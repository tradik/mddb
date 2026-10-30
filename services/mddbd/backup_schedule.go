package main

import (
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"mddb/internal/automationlog"
	"mddb/internal/webhooks"
)

// The run of a scheduled backup (OPS-023): stage → write and trial-open
// (backupCollections, the code behind GET /v1/backup) → sha256 → store and
// verify on the target → prune to `keep`. Each run is logged in the automation
// log and announced over the jobs SSE stream (GO-030); a stored backup sets
// mddb_backup_last_success_timestamp, a failure fires backup.failed.

// backupSuccessGauge is the metric a stored scheduled backup sets, per rule.
const backupSuccessGauge = "mddb_backup_last_success_timestamp"

// backupRunSteps is how many job.progress steps a run reports: written,
// hashed, stored, pruned.
const backupRunSteps = 4

// errBackupRunning is why a run is skipped: the previous run of the same rule
// has not finished.
var errBackupRunning = errors.New("the previous run of this backup is still going")

// backupRuns holds the IDs of the rules whose backup is running now.
var backupRuns sync.Map

// backupOutcome is what a run did.
type backupOutcome struct {
	where   string   // where the backup is; empty until it is stored
	removed []string // what retention deleted
}

// runScheduledBackup takes one backup for rule at now. now names the backup
// and stamps the metric; the scheduler passes its clock.
func (s *Server) runScheduledBackup(rule AutomationRule, now time.Time) (backupOutcome, error) {
	start := time.Now()
	if _, running := backupRuns.LoadOrStore(rule.ID, true); running {
		s.logBackup(rule, start, backupOutcome{}, "skipped", errBackupRunning)
		return backupOutcome{}, errBackupRunning
	}
	defer backupRuns.Delete(rule.ID)

	job := &BulkIngestJob{
		ID:          actionJobID(actionBackup, rule.ID, now),
		Collection:  "*",
		Status:      BulkJobProcessing,
		Total:       backupRunSteps,
		SubmittedAt: start.Unix(),
		StartedAt:   start.Unix(),
	}
	s.announceJob(SSEJobStarted, job)

	out, err := s.takeBackup(rule.Backup, rule.Backup.name(now), job)
	if out.where != "" {
		s.Metrics.SetGauge(backupSuccessGauge,
			"Unix time of the last scheduled backup stored and verified, per automation rule.",
			"rule="+strconv.Quote(rule.ID), float64(now.Unix()))
	}
	job.CompletedAt = time.Now().Unix()
	if err != nil {
		job.Status, job.Failed, job.Errors = BulkJobFailed, 1, []string{err.Error()}
		s.announceJob(SSEJobFailed, job)
		s.logBackup(rule, start, out, "error", err)
		s.Metrics.IncOp("backup_failed", rule.ID)
		if s.WebhookManager != nil {
			s.WebhookManager.FireEvent(webhooks.EventBackupFailed, map[string]interface{}{
				"ruleId":   rule.ID,
				"ruleName": rule.Name,
				"target":   rule.Backup.Target,
				"backup":   out.where,
				"error":    err.Error(),
			})
		}
		return out, err
	}
	job.Status = BulkJobCompleted
	s.announceJob(SSEJobCompleted, job)
	s.logBackup(rule, start, out, "success", nil)
	return out, nil
}

// takeBackup does the work of a run. A failure before the backup is stored
// leaves nothing of it behind; retention runs only once it is stored.
func (s *Server) takeBackup(a *BackupAction, name string, job *BulkIngestJob) (out backupOutcome, err error) {
	var only []string
	if !a.allCollections() {
		if s.collectionStore() == nil {
			return out, errors.New(`a backup of some collections needs MDDB_STORAGE_LAYOUT=per-collection; use collections ["*"] in the single-file layout`)
		}
		only = a.Collections
	}
	target, err := newBackupTarget(a)
	if err != nil {
		return out, fmt.Errorf("connecting to the %s target: %w", a.Target, err)
	}
	dir, base, err := target.stage(name)
	if err != nil {
		return out, err
	}
	defer func() { target.discard(dir, base, out.where == "") }()

	if err := s.backupCollections(filepath.Join(dir, base), only); err != nil {
		return out, err
	}
	s.backupProgress(job)
	set, err := hashBackupSet(dir, base)
	if err != nil {
		return out, err
	}
	s.backupProgress(job)
	if out.where, err = target.store(set, name); err != nil {
		return out, err
	}
	s.backupProgress(job)
	if out.removed, err = pruneBackups(target, a, name); err != nil {
		return out, fmt.Errorf("the backup is stored, but retention failed: %w", err)
	}
	s.backupProgress(job)
	return out, nil
}

// backupProgress counts one finished step and announces it.
func (s *Server) backupProgress(job *BulkIngestJob) {
	job.Processed++
	s.announceJob(SSEJobProgress, job)
}

// announceJob puts a scheduled run's job event on the SSE stream. A backup
// covers the whole database, so its job's collection is "*": a client needs
// read permission on every collection to hear of it. A sync's job names its
// collection (INT-022).
func (s *Server) announceJob(event string, job *BulkIngestJob) {
	if s.SSEHub != nil {
		s.SSEHub.BroadcastJob(event, job, s.AuthManager)
	}
}

// logBackup records a run in the automation log.
func (s *Server) logBackup(rule AutomationRule, start time.Time, out backupOutcome, status string, err error) {
	if s.AutomationLogStore == nil {
		return
	}
	entry := automationlog.Entry{
		Timestamp:  start.Unix(),
		RuleID:     rule.ID,
		RuleName:   rule.Name,
		RuleType:   rule.Type,
		Status:     status,
		DurationMs: time.Since(start).Milliseconds(),
		Attempt:    1,
		Action:     "backup",
		Target:     out.where,
		Removed:    out.removed,
	}
	if err != nil {
		entry.Error = err.Error()
	}
	_ = s.AutomationLogStore.Log(entry)
}
