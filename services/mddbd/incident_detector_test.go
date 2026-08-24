package main

import (
	"io"
	"mddb/internal/webhooks"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	bolt "go.etcd.io/bbolt"
	json "mddb/internal/jsonx"
)

func newIncidentWebhookManager(t *testing.T) *webhooks.WebhookManager {
	t.Helper()
	dir := t.TempDir()
	db, err := bolt.Open(filepath.Join(dir, "wh.db"), 0o600, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	wm := webhooks.NewWebhookManager(db)
	if err := wm.EnsureBucket(); err != nil {
		t.Fatal(err)
	}
	return wm
}

// captureEvents wires a single webhook that records every payload
// it receives into a channel. Returns the channel and a cleanup func.
func captureEvents(t *testing.T, wm *webhooks.WebhookManager, events ...string) <-chan webhooks.WebhookPayload {
	t.Helper()
	ch := make(chan webhooks.WebhookPayload, 16)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var p webhooks.WebhookPayload
		if err := decodeJSONBody(r, &p); err != nil {
			t.Logf("decode: %v", err)
			w.WriteHeader(500)
			return
		}
		ch <- p
		w.WriteHeader(200)
	}))
	t.Cleanup(srv.Close)
	if _, err := wm.Register(srv.URL, events, ""); err != nil {
		t.Fatalf("register: %v", err)
	}
	return ch
}

func decodeJSONBody(r *http.Request, v interface{}) error {
	defer func() { _ = r.Body.Close() }()
	b, err := io.ReadAll(r.Body)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

// ----- AuthFailureTracker -----

func TestAuthFailureTrackerBelowThresholdSilent(t *testing.T) {
	wm := newIncidentWebhookManager(t)
	ch := captureEvents(t, wm, webhooks.EventAuthFailureBurst)
	t.Setenv("MDDB_INCIDENT_AUTH_THRESHOLD", "5")
	t.Setenv("MDDB_INCIDENT_AUTH_WINDOW_SEC", "60")
	t.Setenv("MDDB_INCIDENT_AUTH_COOLDOWN_SEC", "300")
	tr := NewAuthFailureTracker(wm)
	for i := 0; i < 4; i++ {
		if tr.Record("alice", "1.1.1.1") {
			t.Fatalf("burst fired too early at %d", i)
		}
	}
	select {
	case p := <-ch:
		t.Fatalf("unexpected burst: %+v", p)
	case <-time.After(150 * time.Millisecond):
	}
}

func TestAuthFailureTrackerFiresAtThreshold(t *testing.T) {
	wm := newIncidentWebhookManager(t)
	ch := captureEvents(t, wm, webhooks.EventAuthFailureBurst)
	t.Setenv("MDDB_INCIDENT_AUTH_THRESHOLD", "3")
	t.Setenv("MDDB_INCIDENT_AUTH_COOLDOWN_SEC", "1")
	tr := NewAuthFailureTracker(wm)
	fired := 0
	for i := 0; i < 3; i++ {
		if tr.Record("bob", "2.2.2.2") {
			fired++
		}
	}
	if fired != 1 {
		t.Fatalf("want 1 fire, got %d", fired)
	}
	select {
	case p := <-ch:
		if p.Event != webhooks.EventAuthFailureBurst {
			t.Fatalf("wrong event %q", p.Event)
		}
		if p.Detail["ip"] != "2.2.2.2" || p.Detail["actor"] != "bob" {
			t.Fatalf("bad detail: %+v", p.Detail)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("burst not delivered")
	}
}

func TestAuthFailureTrackerCooldown(t *testing.T) {
	wm := newIncidentWebhookManager(t)
	t.Setenv("MDDB_INCIDENT_AUTH_THRESHOLD", "2")
	t.Setenv("MDDB_INCIDENT_AUTH_COOLDOWN_SEC", "3600")
	tr := NewAuthFailureTracker(wm)
	_ = tr.Record("carol", "3.3.3.3")
	if !tr.Record("carol", "3.3.3.3") {
		t.Fatal("threshold hit should have fired")
	}
	// Further failures in the same window+cooldown must not refire.
	for i := 0; i < 5; i++ {
		if tr.Record("carol", "3.3.3.3") {
			t.Fatalf("refired during cooldown at %d", i)
		}
	}
}

func TestAuthFailureTrackerNilSafe(t *testing.T) {
	var tr *AuthFailureTracker
	if tr.Record("x", "y") {
		t.Fatal("nil must be no-op")
	}
	tr2 := &AuthFailureTracker{wm: nil}
	if tr2.Record("x", "y") {
		t.Fatal("nil wm must be no-op")
	}
}

// ----- Panic recovery -----

func TestPanicRecoveryMiddleware(t *testing.T) {
	wm := newIncidentWebhookManager(t)
	ch := captureEvents(t, wm, webhooks.EventPanicRecovered)
	h := PanicRecoveryMiddleware(wm, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("boom")
	}))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/crash", nil)
	req.RemoteAddr = "9.9.9.9:1"
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("want 500, got %d", rec.Code)
	}
	select {
	case p := <-ch:
		if p.Detail["path"] != "/crash" || p.Detail["panic"] != "boom" {
			t.Fatalf("bad detail: %+v", p.Detail)
		}
	case <-time.After(time.Second):
		t.Fatal("panic event not delivered")
	}
}

func TestPanicRecoveryMiddlewareHappyPath(t *testing.T) {
	wm := newIncidentWebhookManager(t)
	called := atomic.Int32{}
	h := PanicRecoveryMiddleware(wm, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called.Add(1)
		w.WriteHeader(200)
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/ok", nil))
	if rec.Code != 200 || called.Load() != 1 {
		t.Fatalf("happy path broken: code=%d called=%d", rec.Code, called.Load())
	}
}

func TestPanicRecoveryMiddlewareErrorPanic(t *testing.T) {
	wm := newIncidentWebhookManager(t)
	ch := captureEvents(t, wm, webhooks.EventPanicRecovered)
	h := PanicRecoveryMiddleware(wm, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic(&errPanic{"db exploded"})
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/x", nil))
	if rec.Code != 500 {
		t.Fatalf("want 500, got %d", rec.Code)
	}
	select {
	case p := <-ch:
		if p.Detail["panic"] != "db exploded" {
			t.Fatalf("error panic message lost: %+v", p.Detail)
		}
	case <-time.After(time.Second):
		t.Fatal("no event")
	}
}

type errPanic struct{ s string }

func (e *errPanic) Error() string { return e.s }

func TestAsStringFallback(t *testing.T) {
	if got := asString(42); got != "(unprintable panic)" {
		t.Errorf("want fallback, got %q", got)
	}
	if got := asString("hi"); got != "hi" {
		t.Errorf("want pass-through, got %q", got)
	}
}

// ----- Lag + disk monitors (start/stop lifecycle) -----

func TestLagMonitorNilSafe(t *testing.T) {
	var m *ReplicationLagMonitor
	m.Start()
	m.Stop()
}

func TestDiskMonitorNilSafe(t *testing.T) {
	var m *DiskUsageMonitor
	m.Start()
	m.Stop()
}

func TestDiskMonitorZeroWhenNoWM(t *testing.T) {
	if NewDiskUsageMonitor(nil, "/tmp") != nil {
		t.Fatal("nil wm should produce nil monitor")
	}
	if NewDiskUsageMonitor(&webhooks.WebhookManager{}, "") != nil {
		t.Fatal("empty path should produce nil monitor")
	}
}

func TestDiskMonitorFiresAtThreshold(t *testing.T) {
	wm := newIncidentWebhookManager(t)
	ch := captureEvents(t, wm, webhooks.EventDiskUsageHigh)
	// 0%% threshold ⇒ always over.
	t.Setenv("MDDB_INCIDENT_DISK_THRESHOLD_PCT", "1")
	t.Setenv("MDDB_INCIDENT_DISK_INTERVAL_SEC", "1")
	t.Setenv("MDDB_INCIDENT_DISK_COOLDOWN_SEC", "1")
	m := NewDiskUsageMonitor(wm, os.TempDir())
	if m == nil {
		t.Fatal("nil monitor")
	}
	// Call check directly to avoid waiting for the ticker.
	m.check()
	select {
	case p := <-ch:
		if p.Event != webhooks.EventDiskUsageHigh {
			t.Fatalf("bad event %q", p.Event)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no disk event")
	}
}

func TestDiskUsageUnknownPath(t *testing.T) {
	_, _, ok := diskUsage("/this/path/does/not/exist/ever")
	if ok {
		t.Fatal("expected failure for bogus path")
	}
}

// stubLag lets us feed arbitrary LagMs values to the monitor.
type stubLag struct{ v atomic.Int64 }

func (s *stubLag) LagMs() int64 { return s.v.Load() }

func TestLagMonitorFiresAboveThreshold(t *testing.T) {
	wm := newIncidentWebhookManager(t)
	ch := captureEvents(t, wm, webhooks.EventReplicationLagHigh)
	t.Setenv("MDDB_INCIDENT_LAG_THRESHOLD_MS", "1000")
	t.Setenv("MDDB_INCIDENT_LAG_INTERVAL_SEC", "1")
	t.Setenv("MDDB_INCIDENT_LAG_COOLDOWN_SEC", "1")
	src := &stubLag{}
	src.v.Store(5000)
	m := NewReplicationLagMonitor(wm, src)
	m.check()
	select {
	case p := <-ch:
		if p.Detail["lagMs"].(float64) != 5000 {
			t.Fatalf("bad lagMs: %+v", p.Detail)
		}
	case <-time.After(time.Second):
		t.Fatal("no lag event")
	}
}

func TestLagMonitorBelowThresholdSilent(t *testing.T) {
	wm := newIncidentWebhookManager(t)
	ch := captureEvents(t, wm, webhooks.EventReplicationLagHigh)
	t.Setenv("MDDB_INCIDENT_LAG_THRESHOLD_MS", "5000")
	src := &stubLag{}
	src.v.Store(200)
	m := NewReplicationLagMonitor(wm, src)
	m.check()
	select {
	case <-ch:
		t.Fatal("should be silent")
	case <-time.After(150 * time.Millisecond):
	}
}

func TestLagMonitorCooldown(t *testing.T) {
	wm := newIncidentWebhookManager(t)
	ch := captureEvents(t, wm, webhooks.EventReplicationLagHigh)
	t.Setenv("MDDB_INCIDENT_LAG_THRESHOLD_MS", "100")
	t.Setenv("MDDB_INCIDENT_LAG_COOLDOWN_SEC", "3600")
	src := &stubLag{}
	src.v.Store(500)
	m := NewReplicationLagMonitor(wm, src)
	m.check()
	m.check() // second call must be suppressed
	var fires int
	for {
		select {
		case <-ch:
			fires++
			if fires > 1 {
				t.Fatal("cooldown not honoured")
			}
		case <-time.After(200 * time.Millisecond):
			if fires == 0 {
				t.Fatal("first event missing")
			}
			return
		}
	}
}

func TestLagMonitorStartStop(t *testing.T) {
	wm := newIncidentWebhookManager(t)
	t.Setenv("MDDB_INCIDENT_LAG_INTERVAL_SEC", "1")
	m := NewReplicationLagMonitor(wm, &stubLag{})
	m.Start()
	time.Sleep(50 * time.Millisecond)
	m.Stop()
	m.Stop() // idempotent
}

func TestDiskMonitorStartStop(t *testing.T) {
	wm := newIncidentWebhookManager(t)
	t.Setenv("MDDB_INCIDENT_DISK_INTERVAL_SEC", "1")
	m := NewDiskUsageMonitor(wm, os.TempDir())
	m.Start()
	time.Sleep(50 * time.Millisecond)
	m.Stop()
	m.Stop() // idempotent
}

func TestNewReplicationLagMonitorNilInputs(t *testing.T) {
	if NewReplicationLagMonitor(nil, &stubLag{}) != nil {
		t.Fatal("nil wm must yield nil monitor")
	}
	wm := newIncidentWebhookManager(t)
	if NewReplicationLagMonitor(wm, nil) != nil {
		t.Fatal("nil source must yield nil monitor")
	}
}
