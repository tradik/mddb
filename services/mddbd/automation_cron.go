package main

import (
	"log/slog"
	"mddb/internal/automationlog"
	"sync"
	"time"

	"github.com/robfig/cron/v3"
)

// cronParser reads a rule's schedule: the standard five fields, or six with
// seconds first. The scheduler used to demand six, so "0 3 * * *" — the
// form docs/AUTOMATIONS.md shows — was rejected and the rule silently never
// ran. Validation and the scheduler share this parser, so a schedule that is
// accepted is one that runs.
var cronParser = cron.NewParser(cron.SecondOptional | cron.Minute | cron.Hour |
	cron.Dom | cron.Month | cron.Dow | cron.Descriptor)

// CronScheduler manages scheduled automation triggers.
type CronScheduler struct {
	cron     *cron.Cron
	server   *Server
	mu       sync.Mutex
	entryMap map[string]cron.EntryID // rule ID → cron entry ID
	// now is the clock a job reads when it fires; a test replaces it to run
	// a schedule's days in a moment.
	now func() time.Time
}

// NewCronScheduler creates a new cron scheduler.
func NewCronScheduler(server *Server) *CronScheduler {
	return &CronScheduler{
		cron:     cron.New(cron.WithParser(cronParser)),
		server:   server,
		entryMap: make(map[string]cron.EntryID),
		now:      time.Now,
	}
}

// Start starts the cron scheduler.
func (cs *CronScheduler) Start() {
	cs.cron.Start()
}

// Stop gracefully stops the cron scheduler.
func (cs *CronScheduler) Stop() {
	cs.cron.Stop()
}

// Reload reloads all cron entries from the automation manager.
// Called after automation CRUD operations.
func (cs *CronScheduler) Reload() {
	cs.mu.Lock()
	defer cs.mu.Unlock()

	if cs.server.AutomationManager == nil {
		return
	}

	// Remove all existing entries
	for ruleID, entryID := range cs.entryMap {
		cs.cron.Remove(entryID)
		delete(cs.entryMap, ruleID)
	}

	// Add all enabled crons
	crons := cs.server.AutomationManager.List("cron")
	for _, cronRule := range crons {
		if !cronRule.Enabled {
			continue
		}
		cs.addEntry(cronRule)
	}

	slog.Info("Cron scheduler reloaded active crons", "entryMapCount", len(cs.entryMap))
}

// addEntry adds a single cron entry to the scheduler: a backup when the rule
// has one, otherwise its webhook.
func (cs *CronScheduler) addEntry(cronRule AutomationRule) {
	var job func()
	if cronRule.Backup != nil {
		job = cs.backupJob(cronRule)
	} else {
		job = cs.webhookJob(cronRule)
	}
	if job == nil {
		return
	}
	entryID, err := cs.cron.AddFunc(cronRule.Schedule, job)
	if err != nil {
		slog.Warn("cron invalid schedule", "iD", cronRule.ID, "schedule", cronRule.Schedule, "err", err)
		return
	}
	cs.entryMap[cronRule.ID] = entryID
}

// backupJob runs a scheduled backup (OPS-023). The rule is read again when it
// fires — with its credentials, which List leaves out — so an edit made since
// the last reload applies.
func (cs *CronScheduler) backupJob(cronRule AutomationRule) func() {
	ruleID := cronRule.ID
	return func() {
		am := cs.server.AutomationManager
		cs.server.Metrics.IncOp("automation_cron", ruleID)
		rule := am.stored(ruleID)
		if rule == nil || !rule.Enabled || rule.Backup == nil {
			return // deleted or changed since the reload; the next one drops it
		}
		now := cs.now()
		if _, err := cs.server.runScheduledBackup(*rule, now); err != nil {
			slog.Warn("scheduled backup failed", "ruleID", ruleID, "err", err)
		}
		am.UpdateLastRun(ruleID, now.Unix())
	}
}

// webhookJob fires the rule's webhook.
func (cs *CronScheduler) webhookJob(cronRule AutomationRule) func() {
	am := cs.server.AutomationManager

	// Resolve the webhook
	webhook := am.GetWebhook(cronRule.WebhookID)
	if webhook == nil {
		slog.Info("cron webhook not found, skipping", "iD", cronRule.ID, "webhookID", cronRule.WebhookID)
		return nil
	}

	ruleID := cronRule.ID
	webhookID := cronRule.WebhookID

	return func() {
		slog.Info("cron firing webhook", "ruleID", ruleID, "webhookID", webhookID)

		// Track cron execution
		if cs.server.Metrics != nil {
			cs.server.Metrics.IncOp("automation_cron", ruleID)
		}

		// Re-fetch webhook in case it was updated
		currentWebhook := am.GetWebhook(webhookID)
		if currentWebhook == nil || !currentWebhook.Enabled {
			slog.Info("cron webhook disabled or deleted, skipping", "ruleID", ruleID, "webhookID", webhookID)
			if cs.server.AutomationLogStore != nil {
				_ = cs.server.AutomationLogStore.Log(automationlog.Entry{
					Timestamp: time.Now().Unix(),
					RuleID:    ruleID,
					RuleName:  cronRule.Name,
					RuleType:  "cron",
					WebhookID: webhookID,
					Status:    "skipped",
					Error:     "webhook disabled or deleted",
				})
			}
			return
		}

		fireCronWebhook(currentWebhook, ruleID, cronRule.Name, cs.server.AutomationLogStore)
		am.UpdateLastRun(ruleID, time.Now().Unix())
	}
}
