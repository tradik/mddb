package main

import (
	"log/slog"
	"mddb/internal/webhooks"
	"net/http"
	"os"
	"runtime/debug"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

// AuthFailureTracker counts authentication failures per IP/user over
// a sliding window and fires security.auth_failure_burst when a key
// crosses the configured threshold. The counter resets once the
// window elapses — the same actor can only trigger again after a
// cool-down period to avoid webhook storms.
type AuthFailureTracker struct {
	wm        *webhooks.WebhookManager
	threshold int
	window    time.Duration
	cooldown  time.Duration
	mu        sync.Mutex
	counts    map[string]*authFailureBucket
}

type authFailureBucket struct {
	count      int
	windowAt   time.Time
	lastFireAt time.Time
}

// NewAuthFailureTracker reads configuration from env and returns a
// tracker. threshold<=0 or no WebhookManager means the tracker is a
// no-op.
func NewAuthFailureTracker(wm *webhooks.WebhookManager) *AuthFailureTracker {
	t := &AuthFailureTracker{
		wm:     wm,
		counts: make(map[string]*authFailureBucket),
	}
	t.threshold, _ = strconv.Atoi(os.Getenv("MDDB_INCIDENT_AUTH_THRESHOLD"))
	if t.threshold <= 0 {
		t.threshold = 10
	}
	winSec, _ := strconv.Atoi(os.Getenv("MDDB_INCIDENT_AUTH_WINDOW_SEC"))
	if winSec <= 0 {
		winSec = 60
	}
	t.window = time.Duration(winSec) * time.Second
	cdSec, _ := strconv.Atoi(os.Getenv("MDDB_INCIDENT_AUTH_COOLDOWN_SEC"))
	if cdSec <= 0 {
		cdSec = 300
	}
	t.cooldown = time.Duration(cdSec) * time.Second
	return t
}

// Record notes one auth failure for the given actor/ip pair. Safe
// for concurrent use. Returns true if a burst event was just fired.
func (t *AuthFailureTracker) Record(actor, ip string) bool {
	if t == nil || t.wm == nil {
		return false
	}
	key := actor + "@" + ip
	now := time.Now()
	t.mu.Lock()
	b, ok := t.counts[key]
	if !ok || now.After(b.windowAt) {
		b = &authFailureBucket{windowAt: now.Add(t.window)}
		t.counts[key] = b
	}
	b.count++
	fire := b.count >= t.threshold && now.Sub(b.lastFireAt) >= t.cooldown
	if fire {
		b.lastFireAt = now
	}
	t.mu.Unlock()

	if fire {
		t.wm.FireEvent(webhooks.EventAuthFailureBurst, map[string]interface{}{
			"actor":     actor,
			"ip":        ip,
			"count":     t.threshold,
			"windowSec": int(t.window.Seconds()),
		})
		return true
	}
	return false
}

// PanicRecoveryMiddleware wraps an HTTP handler with a defer+recover
// so a crashed handler returns 500 instead of terminating the
// process, emits a structured log line, and fires ops.panic_recovered.
func PanicRecoveryMiddleware(wm *webhooks.WebhookManager, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			rec := recover()
			if rec == nil {
				return
			}
			stack := string(debug.Stack())
			slog.Error("panic recovered", "method", r.Method, "path", r.URL.Path, "rec", rec, "stack", stack) // #nosec G706 -- method/path are already validated by net/http router; safe to log
			if wm != nil {
				wm.FireEvent(webhooks.EventPanicRecovered, map[string]interface{}{
					"method": r.Method,
					"path":   r.URL.Path,
					"panic":  asString(rec),
					"ip":     ClientIP(r),
				})
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"error":"internal server error"}`))
		}()
		next.ServeHTTP(w, r)
	})
}

func asString(v interface{}) string {
	switch x := v.(type) {
	case string:
		return x
	case error:
		return x.Error()
	default:
		return "(unprintable panic)"
	}
}

// lagSource is the minimal surface ReplicationLagMonitor needs —
// kept narrow so tests can supply a stub without building a full
// replication client.
type lagSource interface {
	LagMs() int64
}

// ReplicationLagMonitor polls the replication client's LagMs and
// fires ops.replication_lag_high when the lag exceeds a threshold.
// A single firing per breach period (cool-down) prevents event
// storms on sustained lag.
type ReplicationLagMonitor struct {
	wm          *webhooks.WebhookManager
	rc          lagSource
	thresholdMs int64
	interval    time.Duration
	cooldown    time.Duration
	lastFire    atomic.Int64
	stopCh      chan struct{}
	wg          sync.WaitGroup
}

// NewReplicationLagMonitor builds a monitor from env. Returns nil
// when replication is disabled or no webhook manager is wired.
func NewReplicationLagMonitor(wm *webhooks.WebhookManager, rc lagSource) *ReplicationLagMonitor {
	if wm == nil || rc == nil {
		return nil
	}
	m := &ReplicationLagMonitor{wm: wm, rc: rc, stopCh: make(chan struct{})}
	m.thresholdMs, _ = strconv.ParseInt(os.Getenv("MDDB_INCIDENT_LAG_THRESHOLD_MS"), 10, 64)
	if m.thresholdMs <= 0 {
		m.thresholdMs = 5000
	}
	intSec, _ := strconv.Atoi(os.Getenv("MDDB_INCIDENT_LAG_INTERVAL_SEC"))
	if intSec <= 0 {
		intSec = 30
	}
	m.interval = time.Duration(intSec) * time.Second
	cdSec, _ := strconv.Atoi(os.Getenv("MDDB_INCIDENT_LAG_COOLDOWN_SEC"))
	if cdSec <= 0 {
		cdSec = 300
	}
	m.cooldown = time.Duration(cdSec) * time.Second
	return m
}

// Start launches the polling loop. Safe to call on nil.
func (m *ReplicationLagMonitor) Start() {
	if m == nil {
		return
	}
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		ticker := time.NewTicker(m.interval)
		defer ticker.Stop()
		for {
			select {
			case <-m.stopCh:
				return
			case <-ticker.C:
				m.check()
			}
		}
	}()
}

// Stop blocks until the goroutine exits.
func (m *ReplicationLagMonitor) Stop() {
	if m == nil {
		return
	}
	select {
	case <-m.stopCh:
		// already closed
	default:
		close(m.stopCh)
	}
	m.wg.Wait()
}

func (m *ReplicationLagMonitor) check() {
	lag := m.rc.LagMs()
	if lag < m.thresholdMs {
		return
	}
	now := time.Now().UnixNano()
	last := m.lastFire.Load()
	if last != 0 && time.Duration(now-last) < m.cooldown {
		return
	}
	m.lastFire.Store(now)
	m.wm.FireEvent(webhooks.EventReplicationLagHigh, map[string]interface{}{
		"lagMs":       lag,
		"thresholdMs": m.thresholdMs,
	})
}

// DiskUsageMonitor polls filesystem usage at the DB path and fires
// ops.disk_usage_high when the used-percentage exceeds the threshold.
type DiskUsageMonitor struct {
	wm        *webhooks.WebhookManager
	path      string
	threshold float64 // 0..1
	interval  time.Duration
	cooldown  time.Duration
	lastFire  atomic.Int64
	stopCh    chan struct{}
	wg        sync.WaitGroup
}

// NewDiskUsageMonitor builds a monitor watching the given path.
// Returns nil when no webhook manager is wired or the path is empty.
func NewDiskUsageMonitor(wm *webhooks.WebhookManager, path string) *DiskUsageMonitor {
	if wm == nil || path == "" {
		return nil
	}
	m := &DiskUsageMonitor{wm: wm, path: path, stopCh: make(chan struct{})}
	pct, _ := strconv.Atoi(os.Getenv("MDDB_INCIDENT_DISK_THRESHOLD_PCT"))
	if pct <= 0 || pct > 100 {
		pct = 85
	}
	m.threshold = float64(pct) / 100.0
	intSec, _ := strconv.Atoi(os.Getenv("MDDB_INCIDENT_DISK_INTERVAL_SEC"))
	if intSec <= 0 {
		intSec = 300
	}
	m.interval = time.Duration(intSec) * time.Second
	cdSec, _ := strconv.Atoi(os.Getenv("MDDB_INCIDENT_DISK_COOLDOWN_SEC"))
	if cdSec <= 0 {
		cdSec = 3600
	}
	m.cooldown = time.Duration(cdSec) * time.Second
	return m
}

// Start launches the polling loop.
func (m *DiskUsageMonitor) Start() {
	if m == nil {
		return
	}
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		ticker := time.NewTicker(m.interval)
		defer ticker.Stop()
		for {
			select {
			case <-m.stopCh:
				return
			case <-ticker.C:
				m.check()
			}
		}
	}()
}

// Stop blocks until the goroutine exits.
func (m *DiskUsageMonitor) Stop() {
	if m == nil {
		return
	}
	select {
	case <-m.stopCh:
	default:
		close(m.stopCh)
	}
	m.wg.Wait()
}

func (m *DiskUsageMonitor) check() {
	used, total, ok := diskUsage(m.path)
	if !ok || total == 0 {
		return
	}
	ratio := float64(used) / float64(total)
	if ratio < m.threshold {
		return
	}
	now := time.Now().UnixNano()
	last := m.lastFire.Load()
	if last != 0 && time.Duration(now-last) < m.cooldown {
		return
	}
	m.lastFire.Store(now)
	m.wm.FireEvent(webhooks.EventDiskUsageHigh, map[string]interface{}{
		"path":         m.path,
		"usedBytes":    used,
		"totalBytes":   total,
		"usedPct":      int(ratio * 100),
		"thresholdPct": int(m.threshold * 100),
	})
}

// diskUsage returns used and total bytes for the filesystem that contains
// path. Falls back to (0, 0, false) when the platform cannot answer.
//
// "Used" is measured against what an unprivileged process can actually write,
// so it counts the blocks a Unix filesystem reserves for root as used. A
// monitor that reports the root reserve as available headroom fires its
// disk-full alert after the server has already failed to write.
func diskUsage(path string) (used, total uint64, ok bool) {
	avail, capacity, err := diskSpace(path)
	if err != nil || avail > capacity {
		return 0, 0, false
	}
	return capacity - avail, capacity, true
}
