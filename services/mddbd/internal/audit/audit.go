package audit

import (
	"encoding/binary"
	"errors"
	bolt "go.etcd.io/bbolt"
	"log/slog"
	json "mddb/internal/jsonx"
	"sync"
	"sync/atomic"
	"time"
)

var bucketAudit = []byte("audit")

// AuditEvent is one immutable record in the audit log.
// Field names are stable — auditors consume the JSON directly.
type AuditEvent struct {
	Timestamp  int64  `json:"ts"`
	Actor      string `json:"actor,omitempty"`
	Action     string `json:"action"`
	Resource   string `json:"resource,omitempty"`
	Collection string `json:"collection,omitempty"`
	Key        string `json:"key,omitempty"`
	Result     string `json:"result"`
	IP         string `json:"ip,omitempty"`
	UserAgent  string `json:"userAgent,omitempty"`
	Detail     string `json:"detail,omitempty"`
}

// AuditManager persists authentication, authorization, and mutation
// events to a dedicated BoltDB bucket for ISO 27001 A.8.15 / SOC 2
// CC7.2 compliance. Writes are buffered and flushed by a single
// background goroutine so hot-path handlers never block on disk I/O.
type AuditManager struct {
	db            *bolt.DB
	enabled       bool
	retentionDays int
	ch            chan AuditEvent
	stopCh        chan struct{}
	wg            sync.WaitGroup
	dropped       uint64
	exMu          sync.RWMutex
	exporters     []AuditExporter // optional fan-out to SIEM / syslog
}

// AddExporter registers an external sink. Safe to call before or
// after Start. nil receivers and nil exporters are silently ignored.
func (a *AuditManager) AddExporter(e AuditExporter) {
	if a == nil || e == nil {
		return
	}
	a.exMu.Lock()
	a.exporters = append(a.exporters, e)
	a.exMu.Unlock()
}

// Exporters returns a snapshot of the current exporter list.
func (a *AuditManager) Exporters() []AuditExporter {
	if a == nil {
		return nil
	}
	a.exMu.RLock()
	defer a.exMu.RUnlock()
	out := make([]AuditExporter, len(a.exporters))
	copy(out, a.exporters)
	return out
}

// fanOut delivers one event to every registered exporter. Each
// exporter's Export() is non-blocking by contract, so this stays
// off the hot path even with several SIEM destinations configured.
func (a *AuditManager) fanOut(ev AuditEvent) {
	a.exMu.RLock()
	exs := a.exporters
	a.exMu.RUnlock()
	for _, e := range exs {
		e.Export(ev)
	}
}

// NewAuditManager wires a manager. The caller must invoke Start
// after EnsureBuckets; Record is a no-op until Start runs.
// Enabled reports whether audit logging is active.
func (a *AuditManager) Enabled() bool { return a.enabled }

func NewAuditManager(db *bolt.DB, enabled bool, retentionDays int) *AuditManager {
	if retentionDays <= 0 {
		retentionDays = 90
	}
	return &AuditManager{
		db:            db,
		enabled:       enabled,
		retentionDays: retentionDays,
		ch:            make(chan AuditEvent, 1024),
		stopCh:        make(chan struct{}),
	}
}

// EnsureBuckets creates the audit bucket.
func (a *AuditManager) EnsureBuckets() error {
	if a == nil || !a.enabled {
		return nil
	}
	return a.db.Update(func(tx *bolt.Tx) error {
		_, err := tx.CreateBucketIfNotExists(bucketAudit)
		return err
	})
}

// Start launches the writer and the retention trimmer.
func (a *AuditManager) Start() {
	if a == nil || !a.enabled {
		return
	}
	a.wg.Add(2)
	go a.writer()
	go a.trimmer()
}

// Stop blocks until the buffer is drained and closes every registered
// exporter so syslog / webhook workers exit cleanly on shutdown.
func (a *AuditManager) Stop() {
	if a == nil || !a.enabled {
		return
	}
	close(a.stopCh)
	a.wg.Wait()
	a.exMu.Lock()
	exs := a.exporters
	a.exporters = nil
	a.exMu.Unlock()
	for _, e := range exs {
		e.Close()
	}
}

// Record queues an event. Never blocks — when the buffer is full
// the event is counted as dropped and surfaced via /v1/audit/stats.
func (a *AuditManager) Record(ev AuditEvent) {
	if a == nil || !a.enabled {
		return
	}
	if ev.Timestamp == 0 {
		ev.Timestamp = time.Now().UnixNano()
	}
	if ev.Result == "" {
		ev.Result = "ok"
	}
	select {
	case a.ch <- ev:
	default:
		atomic.AddUint64(&a.dropped, 1)
	}
}

// Dropped returns the count of events that could not be buffered.
func (a *AuditManager) Dropped() uint64 {
	if a == nil {
		return 0
	}
	return atomic.LoadUint64(&a.dropped)
}

func (a *AuditManager) writer() {
	defer a.wg.Done()
	batch := make([]AuditEvent, 0, 64)
	flush := func() {
		if len(batch) == 0 {
			return
		}
		if err := a.flushBatch(batch); err != nil {
			slog.Warn("audit flush failed", "err", err)
		}
		batch = batch[:0]
	}
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-a.stopCh:
			// Drain remaining events before exit
			for {
				select {
				case ev := <-a.ch:
					batch = append(batch, ev)
				default:
					flush()
					return
				}
			}
		case ev := <-a.ch:
			batch = append(batch, ev)
			if len(batch) >= 64 {
				flush()
			}
		case <-ticker.C:
			flush()
		}
	}
}

func (a *AuditManager) flushBatch(batch []AuditEvent) error {
	if err := a.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketAudit)
		if b == nil {
			return errors.New("audit bucket missing")
		}
		seq, _ := b.NextSequence()
		for i, ev := range batch {
			payload, err := json.Marshal(ev)
			if err != nil {
				continue
			}
			key := auditKey(ev.Timestamp, seq+uint64(i))
			if err := b.Put(key, payload); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return err
	}
	// Fan-out to external sinks AFTER the durable write. Each sink is
	// best-effort; failures never roll back the BoltDB record.
	for _, ev := range batch {
		a.fanOut(ev)
	}
	return nil
}

func (a *AuditManager) trimmer() {
	defer a.wg.Done()
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-a.stopCh:
			return
		case <-ticker.C:
			a.trimOnce()
		}
	}
}

// trimOnce purges events older than the retention window. Extracted from the
// hourly trimmer loop so it can be exercised directly in tests.
func (a *AuditManager) trimOnce() {
	cutoff := time.Now().Add(-time.Duration(a.retentionDays) * 24 * time.Hour).UnixNano()
	if err := a.PurgeOlderThan(cutoff); err != nil {
		slog.Warn("audit trim failed", "err", err)
	}
}

// PurgeOlderThan removes events whose timestamp is earlier than the
// given nanosecond cutoff.
func (a *AuditManager) PurgeOlderThan(cutoffNanos int64) error {
	if a == nil || !a.enabled {
		return nil
	}
	return a.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketAudit)
		if b == nil {
			return nil
		}
		c := b.Cursor()
		var toDelete [][]byte
		for k, _ := c.First(); k != nil; k, _ = c.Next() {
			if len(k) < 8 {
				continue
			}
			ts := int64(binary.BigEndian.Uint64(k[:8])) // #nosec G115 -- audit timestamp fits int64
			if ts >= cutoffNanos {
				break
			}
			toDelete = append(toDelete, append([]byte(nil), k...))
		}
		for _, k := range toDelete {
			if err := b.Delete(k); err != nil {
				return err
			}
		}
		return nil
	})
}

// QueryFilter narrows a Query.
type QueryFilter struct {
	FromNanos int64
	ToNanos   int64
	Actor     string
	Action    string
	Result    string
	Limit     int
}

// Query returns events matching the filter, newest first.
func (a *AuditManager) Query(f QueryFilter) ([]AuditEvent, error) {
	if a == nil || !a.enabled {
		return nil, nil
	}
	if f.Limit <= 0 {
		f.Limit = 100
	}
	var out []AuditEvent
	err := a.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketAudit)
		if b == nil {
			return nil
		}
		c := b.Cursor()
		k, v := c.Last()
		for ; k != nil; k, v = c.Prev() {
			if len(k) < 8 {
				continue
			}
			ts := int64(binary.BigEndian.Uint64(k[:8])) // #nosec G115 -- audit timestamp fits int64
			if f.FromNanos > 0 && ts < f.FromNanos {
				break
			}
			if f.ToNanos > 0 && ts > f.ToNanos {
				continue
			}
			var ev AuditEvent
			if err := json.Unmarshal(v, &ev); err != nil {
				continue
			}
			if f.Actor != "" && ev.Actor != f.Actor {
				continue
			}
			if f.Action != "" && ev.Action != f.Action {
				continue
			}
			if f.Result != "" && ev.Result != f.Result {
				continue
			}
			out = append(out, ev)
			if len(out) >= f.Limit {
				break
			}
		}
		return nil
	})
	return out, err
}

func auditKey(tsNanos int64, seq uint64) []byte {
	var k [16]byte
	binary.BigEndian.PutUint64(k[:8], uint64(tsNanos)) // #nosec G115 -- monotonic nanosecond timestamp
	binary.BigEndian.PutUint64(k[8:], seq)
	return k[:]
}

// ClientIP returns the best-effort originating IP for the request,
// honouring X-Forwarded-For only when the direct remote is a trusted
