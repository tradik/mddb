package main

import (
	"log/slog"
	"mddb/internal/automationlog"
	"sync"
	"time"

	"github.com/robfig/cron/v3"
)

// CronScheduler manages scheduled automation triggers.
type CronScheduler struct {
	cron     *cron.Cron
	server   *Server
	mu       sync.Mutex
	entryMap map[string]cron.EntryID // rule ID → cron entry ID
}

// NewCronScheduler creates a new cron scheduler.
func NewCronScheduler(server *Server) *CronScheduler {
	return &CronScheduler{
		cron:     cron.New(cron.WithSeconds()),
		server:   server,
		entryMap: make(map[string]cron.EntryID),
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

// addEntry adds a single cron entry to the scheduler.
func (cs *CronScheduler) addEntry(cronRule AutomationRule) {
	am := cs.server.AutomationManager

	// Resolve the webhook
	webhook := am.GetWebhook(cronRule.WebhookID)
	if webhook == nil {
		slog.Info("cron webhook not found, skipping", "iD", cronRule.ID, "webhookID", cronRule.WebhookID)
		return
	}

	ruleID := cronRule.ID
	webhookID := cronRule.WebhookID

	entryID, err := cs.cron.AddFunc(cronRule.Schedule, func() {
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
	})

	if err != nil {
		slog.Warn("cron invalid schedule", "iD", cronRule.ID, "schedule", cronRule.Schedule, "err", err)
		return
	}

	cs.entryMap[cronRule.ID] = entryID
}
