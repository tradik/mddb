package main

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"mddb/internal/automationlog"
	"mddb/internal/binlog"
	"strings"
	"sync"
	"time"

	bolt "go.etcd.io/bbolt"
	json "mddb/internal/jsonx"
)

var bucketAutomation = []byte("automation")

// AutomationRule is a unified type for webhooks, triggers, and crons.
type AutomationRule struct {
	ID        string `json:"id"`
	Type      string `json:"type"` // "webhook" | "trigger" | "cron"
	Name      string `json:"name"`
	Enabled   bool   `json:"enabled"`
	CreatedAt int64  `json:"createdAt"`
	UpdatedAt int64  `json:"updatedAt"`

	// Webhook fields (type=webhook)
	URL     string            `json:"url,omitempty"`
	Method  string            `json:"method,omitempty"` // POST (default), GET, PUT
	Headers map[string]string `json:"headers,omitempty"`

	// Trigger fields (type=trigger)
	Collection       string                 `json:"collection,omitempty"`
	SearchType       string                 `json:"searchType,omitempty"` // "fts" | "vector" | "hybrid"
	Query            string                 `json:"query,omitempty"`
	Threshold        float64                `json:"threshold,omitempty"` // 0-100
	WebhookID        string                 `json:"webhookId,omitempty"`
	SearchParams     map[string]interface{} `json:"searchParams,omitempty"`     // extra: algorithm, fuzzy, etc.
	Events           []string               `json:"events,omitempty"`           // "insert", "update", "delete" (MySQL-style)
	SentimentEnabled bool                   `json:"sentimentEnabled,omitempty"` // enable sentiment condition
	SentimentMin     float64                `json:"sentimentMin,omitempty"`     // -1.0 to 1.0
	SentimentMax     float64                `json:"sentimentMax,omitempty"`     // -1.0 to 1.0
	ConditionLogic   string                 `json:"conditionLogic,omitempty"`   // "and" | "or"

	// Cron fields (type=cron)
	Schedule  string `json:"schedule,omitempty"` // cron expression "0 9 * * *"
	TriggerID string `json:"triggerId,omitempty"`
	LastRun   int64  `json:"lastRun,omitempty"`
	NextRun   int64  `json:"nextRun,omitempty"`
}

// AutomationManager manages automation rules (webhooks, triggers, crons).
type AutomationManager struct {
	db       *bolt.DB
	mu       sync.RWMutex
	rules    []AutomationRule
	binlog   *binlog.Binlog
	server   *Server
	logStore *automationlog.Store
}

// NewAutomationManager creates a new automation manager.
func NewAutomationManager(db *bolt.DB) *AutomationManager {
	return &AutomationManager{
		db: db,
	}
}

// SetBinlog sets the binlog for replication logging.
func (am *AutomationManager) SetBinlog(bl *binlog.Binlog) {
	am.binlog = bl
}

// SetServer sets the server reference for trigger evaluation.
func (am *AutomationManager) SetServer(s *Server) {
	am.server = s
}

// SetLogStore sets the automation log store for recording webhook executions.
func (am *AutomationManager) SetLogStore(ls *automationlog.Store) {
	am.logStore = ls
}

// EnsureBucket creates the automation bucket if it doesn't exist.
func (am *AutomationManager) EnsureBucket() error {
	return am.db.Update(func(tx *bolt.Tx) error {
		_, err := tx.CreateBucketIfNotExists(bucketAutomation)
		return err
	})
}

// LoadAll loads all automation rules from BoltDB into memory.
func (am *AutomationManager) LoadAll() error {
	var rules []AutomationRule
	err := am.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketAutomation)
		if b == nil {
			return nil
		}
		return b.ForEach(func(k, v []byte) error {
			var rule AutomationRule
			if err := json.Unmarshal(v, &rule); err != nil {
				return nil // skip corrupt entries
			}
			rules = append(rules, rule)
			return nil
		})
	})
	if err != nil {
		return err
	}
	am.mu.Lock()
	am.rules = rules
	am.mu.Unlock()
	return nil
}

// Create adds a new automation rule.
func (am *AutomationManager) Create(rule AutomationRule) (*AutomationRule, error) {
	if err := am.validateRule(&rule); err != nil {
		return nil, err
	}

	now := time.Now().Unix()
	rule.ID = generateAutoID()
	rule.CreatedAt = now
	rule.UpdatedAt = now

	if rule.Type == "webhook" && rule.Method == "" {
		rule.Method = "POST"
	}

	data, err := json.Marshal(rule)
	if err != nil {
		return nil, err
	}

	key := autoKey(rule.ID)
	if err := am.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketAutomation)
		return b.Put(key, data)
	}); err != nil {
		return nil, err
	}

	if am.binlog != nil {
		_ = am.binlog.Append(&binlog.BinlogEntry{Type: binlog.BinlogPut, BucketName: "automation", Key: CopyBytes(key), Value: CopyBytes(data)})
	}

	am.mu.Lock()
	am.rules = append(am.rules, rule)
	am.mu.Unlock()

	return &rule, nil
}

// Update modifies an existing automation rule.
func (am *AutomationManager) Update(id string, update AutomationRule) (*AutomationRule, error) {
	am.mu.RLock()
	var existing *AutomationRule
	for i := range am.rules {
		if am.rules[i].ID == id {
			existing = &am.rules[i]
			break
		}
	}
	am.mu.RUnlock()

	if existing == nil {
		return nil, fmt.Errorf("rule not found: %s", id)
	}

	// Preserve immutable fields
	update.ID = id
	update.Type = existing.Type
	update.CreatedAt = existing.CreatedAt
	update.UpdatedAt = time.Now().Unix()

	// Preserve lastRun/nextRun for crons
	if update.Type == "cron" {
		if update.LastRun == 0 {
			update.LastRun = existing.LastRun
		}
		if update.NextRun == 0 {
			update.NextRun = existing.NextRun
		}
	}

	if err := am.validateRule(&update); err != nil {
		return nil, err
	}

	if update.Type == "webhook" && update.Method == "" {
		update.Method = "POST"
	}

	data, err := json.Marshal(update)
	if err != nil {
		return nil, err
	}

	key := autoKey(id)
	if err := am.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketAutomation)
		return b.Put(key, data)
	}); err != nil {
		return nil, err
	}

	if am.binlog != nil {
		_ = am.binlog.Append(&binlog.BinlogEntry{Type: binlog.BinlogPut, BucketName: "automation", Key: CopyBytes(key), Value: CopyBytes(data)})
	}

	am.mu.Lock()
	for i := range am.rules {
		if am.rules[i].ID == id {
			am.rules[i] = update
			break
		}
	}
	am.mu.Unlock()

	return &update, nil
}

// Delete removes an automation rule by ID.
func (am *AutomationManager) Delete(id string) error {
	key := autoKey(id)
	if err := am.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketAutomation)
		return b.Delete(key)
	}); err != nil {
		return err
	}

	if am.binlog != nil {
		_ = am.binlog.Append(&binlog.BinlogEntry{Type: binlog.BinlogDelete, BucketName: "automation", Key: CopyBytes(key)})
	}

	am.mu.Lock()
	for i, r := range am.rules {
		if r.ID == id {
			am.rules = append(am.rules[:i], am.rules[i+1:]...)
			break
		}
	}
	am.mu.Unlock()
	return nil
}

// Get returns a single rule by ID.
func (am *AutomationManager) Get(id string) *AutomationRule {
	am.mu.RLock()
	defer am.mu.RUnlock()
	for i := range am.rules {
		if am.rules[i].ID == id {
			cp := am.rules[i]
			return &cp
		}
	}
	return nil
}

// List returns all rules, optionally filtered by type.
func (am *AutomationManager) List(filterType string) []AutomationRule {
	am.mu.RLock()
	defer am.mu.RUnlock()

	result := make([]AutomationRule, 0, len(am.rules))
	for _, r := range am.rules {
		if filterType == "" || r.Type == filterType {
			result = append(result, r)
		}
	}
	return result
}

// GetWebhook returns a webhook rule by ID (for trigger→webhook resolution).
func (am *AutomationManager) GetWebhook(id string) *AutomationRule {
	am.mu.RLock()
	defer am.mu.RUnlock()
	for i := range am.rules {
		if am.rules[i].ID == id && am.rules[i].Type == "webhook" {
			cp := am.rules[i]
			return &cp
		}
	}
	return nil
}

// GetTrigger returns a trigger rule by ID (for cron→trigger resolution).
func (am *AutomationManager) GetTrigger(id string) *AutomationRule {
	am.mu.RLock()
	defer am.mu.RUnlock()
	for i := range am.rules {
		if am.rules[i].ID == id && am.rules[i].Type == "trigger" {
			cp := am.rules[i]
			return &cp
		}
	}
	return nil
}

// EnabledTriggersForEvent returns all enabled triggers watching a given collection and event.
func (am *AutomationManager) EnabledTriggersForEvent(collection, event string) []AutomationRule {
	am.mu.RLock()
	defer am.mu.RUnlock()

	var result []AutomationRule
	for _, r := range am.rules {
		if r.Type != "trigger" || !r.Enabled || r.Collection != collection {
			continue
		}
		// Check event match: empty events defaults to insert+update
		events := r.Events
		if len(events) == 0 {
			events = []string{"insert", "update"}
		}
		for _, ev := range events {
			if ev == event {
				result = append(result, r)
				break
			}
		}
	}
	return result
}

// UpdateLastRun updates the lastRun timestamp for a cron rule.
func (am *AutomationManager) UpdateLastRun(id string, ts int64) {
	am.mu.Lock()
	for i := range am.rules {
		if am.rules[i].ID == id {
			am.rules[i].LastRun = ts
			am.rules[i].UpdatedAt = ts
			// Persist async (best-effort)
			go func(rule AutomationRule) {
				data, _ := json.Marshal(rule)
				key := autoKey(rule.ID)
				_ = am.db.Update(func(tx *bolt.Tx) error {
					b := tx.Bucket(bucketAutomation)
					return b.Put(key, data)
				})
			}(am.rules[i])
			break
		}
	}
	am.mu.Unlock()
}

// validateRule validates an automation rule based on its type.
func (am *AutomationManager) validateRule(rule *AutomationRule) error {
	rule.Name = strings.TrimSpace(rule.Name)
	if rule.Name == "" {
		return fmt.Errorf("name is required")
	}

	switch rule.Type {
	case "webhook":
		rule.URL = strings.TrimSpace(rule.URL)
		if rule.URL == "" {
			return fmt.Errorf("url is required for webhook")
		}
	case "trigger":
		rule.Collection = strings.TrimSpace(rule.Collection)
		rule.Query = strings.TrimSpace(rule.Query)
		rule.WebhookID = strings.TrimSpace(rule.WebhookID)
		if rule.Collection == "" {
			return fmt.Errorf("collection is required for trigger")
		}
		// searchType is only required when a query is specified
		if rule.Query != "" {
			if rule.SearchType == "" {
				return fmt.Errorf("searchType is required when query is set (fts, vector, hybrid)")
			}
			validSearch := map[string]bool{"fts": true, "vector": true, "hybrid": true}
			if !validSearch[rule.SearchType] {
				return fmt.Errorf("invalid searchType: %s (valid: fts, vector, hybrid)", rule.SearchType)
			}
		}
		if rule.Threshold < 0 || rule.Threshold > 100 {
			return fmt.Errorf("threshold must be between 0 and 100")
		}
		if rule.WebhookID == "" {
			return fmt.Errorf("webhookId is required for trigger")
		}
		// Verify webhook exists
		if am.GetWebhook(rule.WebhookID) == nil {
			return fmt.Errorf("webhook not found: %s", rule.WebhookID)
		}
		// Validate events; default to insert+update if empty
		if len(rule.Events) == 0 {
			rule.Events = []string{"insert", "update"}
		}
		validEvents := map[string]bool{"insert": true, "update": true, "delete": true}
		for _, ev := range rule.Events {
			if !validEvents[ev] {
				return fmt.Errorf("invalid event: %s (valid: insert, update, delete)", ev)
			}
		}
		// Validate sentiment fields
		if rule.SentimentEnabled {
			if rule.SentimentMin < -1.0 || rule.SentimentMin > 1.0 {
				return fmt.Errorf("sentimentMin must be between -1.0 and 1.0")
			}
			if rule.SentimentMax < -1.0 || rule.SentimentMax > 1.0 {
				return fmt.Errorf("sentimentMax must be between -1.0 and 1.0")
			}
			if rule.SentimentMin > rule.SentimentMax {
				return fmt.Errorf("sentimentMin must be <= sentimentMax")
			}
		}
		// Validate condition logic
		if rule.ConditionLogic == "" {
			rule.ConditionLogic = "and"
		} else if rule.ConditionLogic != "and" && rule.ConditionLogic != "or" {
			return fmt.Errorf("conditionLogic must be 'and' or 'or'")
		}
	case "cron":
		rule.Schedule = strings.TrimSpace(rule.Schedule)
		rule.WebhookID = strings.TrimSpace(rule.WebhookID)
		if rule.Schedule == "" {
			return fmt.Errorf("schedule is required for cron")
		}
		if rule.WebhookID == "" {
			return fmt.Errorf("webhookId is required for cron")
		}
		// Verify webhook exists
		if am.GetWebhook(rule.WebhookID) == nil {
			return fmt.Errorf("webhook not found: %s", rule.WebhookID)
		}
	default:
		return fmt.Errorf("invalid type: %s (valid: webhook, trigger, cron)", rule.Type)
	}
	return nil
}

func autoKey(id string) []byte {
	return []byte("auto|" + id)
}

func generateAutoID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
