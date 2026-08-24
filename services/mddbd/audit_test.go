package main

import (
	"context"
	"fmt"
	"mddb/internal/audit"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"mddb/internal/testsync"
	"time"

	bolt "go.etcd.io/bbolt"
)

func newAuditTestManager(t *testing.T, retention int) (*audit.AuditManager, func()) {
	t.Helper()
	dir := t.TempDir()
	db, err := bolt.Open(filepath.Join(dir, "audit.db"), 0o600, nil)
	if err != nil {
		t.Fatalf("open bolt: %v", err)
	}
	am := audit.NewAuditManager(db, true, retention)
	if err := am.EnsureBuckets(); err != nil {
		t.Fatalf("buckets: %v", err)
	}
	am.Start()
	cleanup := func() {
		am.Stop()
		_ = db.Close()
	}
	return am, cleanup
}

// waitFlush blocks until the audit manager has flushed at least want events.
//
// It was a fixed 700 ms sleep, which is a guess about how long a batch takes to
// reach the database and was wrong on a loaded runner: the first Windows run
// slower than usual reported "want 3 events, got 0" and "want 200, got 64".
// The batch is what the tests assert on, so the batch is what to wait for —
// and where it has already landed this returns at once rather than sleeping
// out the rest of the guess.
func waitFlush(t *testing.T, am *audit.AuditManager, want int) {
	t.Helper()
	testsync.Wait(t, fmt.Sprintf("%d audit events to be flushed", want), func() bool {
		events, err := am.Query(audit.QueryFilter{Limit: want + 100})
		return err == nil && len(events) >= want
	})
}

func TestAuditRecordAndQuery(t *testing.T) {
	am, cleanup := newAuditTestManager(t, 90)
	defer cleanup()

	am.Record(audit.AuditEvent{Actor: "alice", Action: "auth.login", Result: "ok"})
	am.Record(audit.AuditEvent{Actor: "bob", Action: "auth.login", Result: "fail"})
	am.Record(audit.AuditEvent{Actor: "alice", Action: "write./v1/add", Result: "ok"})
	waitFlush(t, am, 3)

	events, err := am.Query(audit.QueryFilter{})
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(events) != 3 {
		t.Fatalf("want 3 events, got %d", len(events))
	}

	// Newest first
	if events[0].Action != "write./v1/add" {
		t.Errorf("want newest first, got %q", events[0].Action)
	}

	// Filter by actor
	ev, _ := am.Query(audit.QueryFilter{Actor: "alice"})
	if len(ev) != 2 {
		t.Fatalf("alice events: want 2, got %d", len(ev))
	}

	// Filter by result=fail
	ev, _ = am.Query(audit.QueryFilter{Result: "fail"})
	if len(ev) != 1 || ev[0].Actor != "bob" {
		t.Fatalf("fail events: unexpected %+v", ev)
	}

	// Filter by action
	ev, _ = am.Query(audit.QueryFilter{Action: "auth.login"})
	if len(ev) != 2 {
		t.Fatalf("auth.login events: want 2, got %d", len(ev))
	}

	// Limit
	ev, _ = am.Query(audit.QueryFilter{Limit: 1})
	if len(ev) != 1 {
		t.Fatalf("limit 1: got %d", len(ev))
	}
}

func TestAuditPurgeOlderThan(t *testing.T) {
	am, cleanup := newAuditTestManager(t, 90)
	defer cleanup()

	past := time.Now().Add(-10 * time.Hour).UnixNano()
	now := time.Now().UnixNano()

	am.Record(audit.AuditEvent{Timestamp: past, Actor: "old", Action: "auth.login", Result: "ok"})
	am.Record(audit.AuditEvent{Timestamp: now, Actor: "new", Action: "auth.login", Result: "ok"})
	waitFlush(t, am, 2)

	cutoff := time.Now().Add(-5 * time.Hour).UnixNano()
	if err := am.PurgeOlderThan(cutoff); err != nil {
		t.Fatalf("purge: %v", err)
	}

	events, _ := am.Query(audit.QueryFilter{})
	if len(events) != 1 || events[0].Actor != "new" {
		t.Fatalf("after purge want only 'new', got %+v", events)
	}
}

func TestAuditDisabledNoOp(t *testing.T) {
	dir := t.TempDir()
	db, err := bolt.Open(filepath.Join(dir, "audit.db"), 0o600, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	am := audit.NewAuditManager(db, false, 90)
	_ = am.EnsureBuckets()
	am.Start()
	defer am.Stop()

	am.Record(audit.AuditEvent{Actor: "alice", Action: "x", Result: "ok"})
	events, _ := am.Query(audit.QueryFilter{})
	if len(events) != 0 {
		t.Fatalf("disabled manager stored %d events", len(events))
	}
}

func TestAuditDroppedWhenBufferFull(t *testing.T) {
	dir := t.TempDir()
	db, err := bolt.Open(filepath.Join(dir, "audit.db"), 0o600, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	// Do NOT Start(): writer goroutine is absent, channel cannot drain.
	am := audit.NewAuditManager(db, true, 90)
	_ = am.EnsureBuckets()

	// Buffer cap is 1024; overshoot to force drops.
	for i := 0; i < 1100; i++ {
		am.Record(audit.AuditEvent{Actor: "spam", Action: "x", Result: "ok"})
	}
	if am.Dropped() == 0 {
		t.Fatalf("expected drops, got 0")
	}
}

func TestAuditHandlerRequiresAdmin(t *testing.T) {
	am, cleanup := newAuditTestManager(t, 90)
	defer cleanup()
	am.Record(audit.AuditEvent{Actor: "alice", Action: "auth.login", Result: "ok"})
	waitFlush(t, am, 1)

	s := &Server{AuditManager: am, AuthManager: &AuthManager{enabled: true}}

	// No claims => 401
	req := httptest.NewRequest("GET", "/v1/audit", nil)
	rec := httptest.NewRecorder()
	s.handleAudit(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", rec.Code)
	}

	// Non-admin claims => 403
	req = httptest.NewRequest("GET", "/v1/audit", nil)
	req = req.WithContext(context.WithValue(req.Context(), authContextKey, &JWTClaims{Username: "alice", Admin: false}))
	rec = httptest.NewRecorder()
	s.handleAudit(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("want 403, got %d", rec.Code)
	}

	// Admin claims => 200
	req = httptest.NewRequest("GET", "/v1/audit?limit=10", nil)
	req = req.WithContext(context.WithValue(req.Context(), authContextKey, &JWTClaims{Username: "root", Admin: true}))
	rec = httptest.NewRecorder()
	s.handleAudit(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestAuditHandlerDisabled(t *testing.T) {
	s := &Server{AuditManager: audit.NewAuditManager(nil, false, 0)}
	req := httptest.NewRequest("GET", "/v1/audit", nil)
	rec := httptest.NewRecorder()
	s.handleAudit(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d", rec.Code)
	}
}

func TestAuditHandlerTimeRangeParsing(t *testing.T) {
	am, cleanup := newAuditTestManager(t, 90)
	defer cleanup()
	am.Record(audit.AuditEvent{Actor: "alice", Action: "auth.login", Result: "ok"})
	waitFlush(t, am, 1)

	s := &Server{AuditManager: am}
	from := time.Now().Add(-time.Hour).Format(time.RFC3339)
	to := time.Now().Add(time.Hour).Format(time.RFC3339)
	req := httptest.NewRequest("GET", "/v1/audit?from="+from+"&to="+to, nil)
	rec := httptest.NewRecorder()
	s.handleAudit(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestClientIPExtraction(t *testing.T) {
	cases := map[string]struct {
		remote string
		xff    string
		want   string
	}{
		"no xff":       {remote: "203.0.113.5:5555", want: "203.0.113.5"},
		"single xff":   {remote: "10.0.0.1:80", xff: "198.51.100.4", want: "198.51.100.4"},
		"chained xff":  {remote: "10.0.0.1:80", xff: "198.51.100.4, 10.0.0.1", want: "198.51.100.4"},
		"ipv6 remote":  {remote: "[::1]:80", want: "[::1]"},
		"empty remote": {remote: "", want: ""},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/", nil)
			r.RemoteAddr = tc.remote
			if tc.xff != "" {
				r.Header.Set("X-Forwarded-For", tc.xff)
			}
			got := ClientIP(r)
			if got != tc.want {
				t.Fatalf("want %q, got %q", tc.want, got)
			}
		})
	}
}

func TestAuditPurgeEmptyBucket(t *testing.T) {
	dir := t.TempDir()
	db, err := bolt.Open(filepath.Join(dir, "a.db"), 0o600, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	am := audit.NewAuditManager(db, true, 90)
	// No EnsureBuckets — PurgeOlderThan must no-op rather than panic.
	if err := am.PurgeOlderThan(time.Now().UnixNano()); err != nil {
		t.Fatalf("purge empty: %v", err)
	}
}

func TestAuditQueryTimeWindow(t *testing.T) {
	am, cleanup := newAuditTestManager(t, 90)
	defer cleanup()
	base := time.Now().UnixNano()
	am.Record(audit.AuditEvent{Timestamp: base - int64(time.Hour), Actor: "a", Action: "x", Result: "ok"})
	am.Record(audit.AuditEvent{Timestamp: base, Actor: "b", Action: "x", Result: "ok"})
	am.Record(audit.AuditEvent{Timestamp: base + int64(time.Hour), Actor: "c", Action: "x", Result: "ok"})
	waitFlush(t, am, 3)
	ev, _ := am.Query(audit.QueryFilter{FromNanos: base - int64(30*time.Minute), ToNanos: base + int64(30*time.Minute)})
	if len(ev) != 1 || ev[0].Actor != "b" {
		t.Fatalf("window filter: %+v", ev)
	}
}

func TestAuditBatchFlushLarge(t *testing.T) {
	am, cleanup := newAuditTestManager(t, 90)
	defer cleanup()
	for i := 0; i < 200; i++ {
		am.Record(audit.AuditEvent{Actor: "u", Action: "x", Result: "ok"})
	}
	waitFlush(t, am, 200)
	ev, _ := am.Query(audit.QueryFilter{Limit: 300})
	if len(ev) != 200 {
		t.Fatalf("want 200, got %d", len(ev))
	}
}

func TestAuditDisabledStopNoOp(t *testing.T) {
	am := audit.NewAuditManager(nil, false, 0)
	am.Start()
	am.Stop()
}

func TestAuditNilManagerSafe(t *testing.T) {
	var am *audit.AuditManager
	am.Record(audit.AuditEvent{Action: "x"})
	if am.Dropped() != 0 {
		t.Fatal("nil Dropped != 0")
	}
	if err := am.EnsureBuckets(); err != nil {
		t.Fatal(err)
	}
	_, err := am.Query(audit.QueryFilter{})
	if err != nil {
		t.Fatal(err)
	}
}

func TestAuditHandlerRawNanosAndInvalidParams(t *testing.T) {
	am, cleanup := newAuditTestManager(t, 90)
	defer cleanup()
	am.Record(audit.AuditEvent{Actor: "x", Action: "y", Result: "ok"})
	waitFlush(t, am, 1)
	s := &Server{AuditManager: am}

	req := httptest.NewRequest("GET", "/v1/audit?fromNanos=1&toNanos=9999999999999999999&limit=abc", nil)
	rec := httptest.NewRecorder()
	s.handleAudit(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rec.Code)
	}
}
