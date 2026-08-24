package automationlog

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	bolt "go.etcd.io/bbolt"
	"log/slog"
	json "mddb/internal/jsonx"
	"strconv"
	"strings"
	"time"
)

var bucketAutomationLog = []byte("automation_log")

// Entry represents a single automation execution log record.
type Entry struct {
	ID         string `json:"id"`
	Timestamp  int64  `json:"timestamp"`
	RuleID     string `json:"ruleId"`
	RuleName   string `json:"ruleName"`
	RuleType   string `json:"ruleType"` // "trigger" | "cron"
	WebhookID  string `json:"webhookId"`
	WebhookURL string `json:"webhookUrl"`
	Status     string `json:"status"`     // "success" | "error" | "skipped"
	HTTPStatus int    `json:"httpStatus"` // 0 if skipped/no response
	DurationMs int64  `json:"durationMs"`
	Error      string `json:"error,omitempty"`
	Attempt    int    `json:"attempt"` // 1-based retry attempt
}

// Store persists and queries automation execution logs in BoltDB.
type Store struct {
	db     *bolt.DB
	ttl    time.Duration
	stopCh chan struct{}
}

// NewStore creates a new log store with the given TTL retention.
func NewStore(db *bolt.DB, ttl time.Duration) *Store {
	return &Store{
		db:     db,
		ttl:    ttl,
		stopCh: make(chan struct{}),
	}
}

// EnsureBucket creates the automation_log bucket if it doesn't exist.
func (ls *Store) EnsureBucket() error {
	return ls.db.Update(func(tx *bolt.Tx) error {
		_, err := tx.CreateBucketIfNotExists(bucketAutomationLog)
		return err
	})
}

// Log writes a single log entry. Called inline after webhook fire.
func (ls *Store) Log(entry Entry) error {
	if entry.ID == "" {
		entry.ID = automationLogID()
	}
	if entry.Timestamp == 0 {
		entry.Timestamp = time.Now().Unix()
	}

	data, err := json.Marshal(entry)
	if err != nil {
		return err
	}

	key := []byte(entry.ID)
	return ls.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketAutomationLog)
		return b.Put(key, data)
	})
}

// List returns log entries ordered newest-first with cursor-based pagination.
// Pass cursor="" for the first page. Returns entries, nextCursor, error.
func (ls *Store) List(limit int, cursor string, ruleID string, status string) ([]Entry, string, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 500 {
		limit = 500
	}

	var entries []Entry
	var nextCursor string

	err := ls.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketAutomationLog)
		if b == nil {
			return nil
		}
		c := b.Cursor()

		var k []byte
		var v []byte
		if cursor != "" {
			// Seek to cursor and move to the previous entry (cursor is exclusive)
			k, _ = c.Seek([]byte(cursor))
			if k != nil && string(k) == cursor {
				k, v = c.Prev()
			} else {
				// Cursor key not found exactly — Seek landed past it, go back
				if k == nil {
					k, v = c.Last()
				} else {
					k, v = c.Prev()
				}
			}
		} else {
			k, v = c.Last()
		}

		for ; k != nil; k, v = c.Prev() {
			var entry Entry
			if err := json.Unmarshal(v, &entry); err != nil {
				continue
			}

			// Apply filters
			if ruleID != "" && entry.RuleID != ruleID {
				continue
			}
			if status != "" && entry.Status != status {
				continue
			}

			entries = append(entries, entry)
			if len(entries) >= limit+1 {
				break
			}
		}

		return nil
	})
	if err != nil {
		return nil, "", err
	}

	// If we got more than limit, there are more pages
	if len(entries) > limit {
		entries = entries[:limit]
		nextCursor = entries[limit-1].ID
	}

	if entries == nil {
		entries = []Entry{}
	}

	return entries, nextCursor, nil
}

// Count returns the total number of log entries, optionally filtered.
func (ls *Store) Count(ruleID string, status string) (int, error) {
	var count int
	err := ls.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketAutomationLog)
		if b == nil {
			return nil
		}

		// Fast path: no filters
		if ruleID == "" && status == "" {
			count = b.Stats().KeyN
			return nil
		}

		// Slow path: iterate and filter
		return b.ForEach(func(k, v []byte) error {
			var entry Entry
			if err := json.Unmarshal(v, &entry); err != nil {
				return nil
			}
			if ruleID != "" && entry.RuleID != ruleID {
				return nil
			}
			if status != "" && entry.Status != status {
				return nil
			}
			count++
			return nil
		})
	})
	return count, err
}

// StartCleanup starts a background goroutine that removes expired log entries.
func (ls *Store) StartCleanup(interval time.Duration) {
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		// Run once immediately
		ls.cleanup()

		for {
			select {
			case <-ticker.C:
				ls.cleanup()
			case <-ls.stopCh:
				return
			}
		}
	}()
}

// Stop signals the cleanup goroutine to stop.
func (ls *Store) Stop() {
	select {
	case <-ls.stopCh:
		// already closed
	default:
		close(ls.stopCh)
	}
}

// cleanup removes all log entries older than ls.ttl.
func (ls *Store) cleanup() {
	cutoffNano := time.Now().Add(-ls.ttl).UnixNano()
	cutoffKey := fmt.Sprintf("%020d|", cutoffNano)

	var deleted int
	_ = ls.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketAutomationLog)
		if b == nil {
			return nil
		}
		c := b.Cursor()
		for k, _ := c.First(); k != nil; k, _ = c.Next() {
			if string(k) >= cutoffKey {
				break
			}
			if err := c.Delete(); err == nil {
				deleted++
			}
		}
		return nil
	})
	if deleted > 0 {
		slog.Info("Automation logs cleaned up expired entries", "deleted", deleted)
	}
}

// automationLogID generates a time-ordered unique ID for log entries.
func automationLogID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return fmt.Sprintf("%020d|%s", time.Now().UnixNano(), hex.EncodeToString(b))
}

// ParseDurationString parses duration strings like "7d", "12h", "30d", "1h30m".
// Supports 'd' suffix for days in addition to standard Go duration suffixes.
func ParseDurationString(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("empty duration string")
	}
	if strings.HasSuffix(s, "d") {
		numStr := strings.TrimSuffix(s, "d")
		days, err := strconv.Atoi(numStr)
		if err != nil {
			return 0, fmt.Errorf("invalid day duration: %s", s)
		}
		return time.Duration(days) * 24 * time.Hour, nil
	}
	return time.ParseDuration(s)
}
