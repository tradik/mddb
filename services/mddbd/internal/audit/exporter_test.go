package audit

import (
	"errors"
	"mddb/internal/testsync"
	"sync"
	"sync/atomic"
	"testing"

	bolt "go.etcd.io/bbolt"
)

// openTestBolt opens a fresh BoltDB at the given path and registers a
// cleanup. Helper shared by audit-exporter tests.
func openTestBolt(t *testing.T, path string) *bolt.DB {
	t.Helper()
	db, err := bolt.Open(path, 0600, nil)
	if err != nil {
		t.Fatalf("bolt.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// stubExporter is a minimal AuditExporter for AuditManager wiring tests.
type stubExporter struct {
	*exporterCore
	delivered atomic.Int64
}

func newStubExporter() *stubExporter {
	core := newExporterCore("stub", "memory", 16)
	s := &stubExporter{exporterCore: core}
	core.wg.Add(1)
	go core.run(s.deliver)
	return s
}

func (s *stubExporter) deliver(ev AuditEvent) error {
	s.delivered.Add(1)
	return nil
}

func (s *stubExporter) Export(ev AuditEvent) { s.pushOrDrop(ev) }

// TestExporterCore_StatsRoundtrip exercises every counter field.
func TestExporterCore_StatsRoundtrip(t *testing.T) {
	c := newExporterCore("x", "tgt", 16)
	c.wg.Add(1)
	calls := atomic.Int64{}
	go c.run(func(ev AuditEvent) error {
		n := calls.Add(1)
		if n%3 == 0 {
			return errors.New("synthetic")
		}
		return nil
	})

	for i := 0; i < 6; i++ {
		c.pushOrDrop(AuditEvent{Action: "a"})
	}
	// A 2 s hand-rolled deadline that fell through to a confusing assertion
	// failure; the helper waits longer and says "timed out waiting for …"
	// instead (TEST-004).
	testsync.WaitForCount(t, "all six events to be delivered or failed", 6, func() int {
		st := c.Stats()
		return testsync.CountOf(st.Delivered + st.Failed)
	})
	st := c.Stats()
	if st.Delivered+st.Failed != 6 {
		t.Errorf("delivered+failed=%d, want 6", st.Delivered+st.Failed)
	}
	if st.Failed == 0 {
		t.Error("expected at least one synthetic failure")
	}
	if st.LastError != "synthetic" {
		t.Errorf("lastError=%q", st.LastError)
	}
	if st.Name != "x" || st.Target != "tgt" {
		t.Errorf("ident: %+v", st)
	}
	c.Close()
}

// TestExporterCore_DropOnFull bumps the dropped counter when the buffer is full.
func TestExporterCore_DropOnFull(t *testing.T) {
	c := newExporterCore("x", "", 1)
	// Stall the worker by NEVER consuming from the channel — we don't
	// even start one, so the channel fills after one push.
	c.pushOrDrop(AuditEvent{Action: "ok"}) // queued
	for i := 0; i < 10; i++ {
		c.pushOrDrop(AuditEvent{Action: "drop"})
	}
	st := c.Stats()
	if st.Dropped != 10 {
		t.Errorf("dropped=%d, want 10", st.Dropped)
	}
	if st.Queued != 1 {
		t.Errorf("queued=%d, want 1", st.Queued)
	}
}

// TestExporterCore_CloseIdempotent — calling Close twice must not panic.
func TestExporterCore_CloseIdempotent(t *testing.T) {
	c := newExporterCore("x", "", 4)
	c.wg.Add(1)
	go c.run(func(ev AuditEvent) error { return nil })
	c.Close()
	c.Close() // must not panic on the closed channel
}

// TestExporterCore_CloseConcurrent — GO-017: many goroutines calling Close at
// once must not race into a double close(stopCh). Run with -race.
func TestExporterCore_CloseConcurrent(t *testing.T) {
	c := newExporterCore("x", "", 4)
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c.Close()
		}()
	}
	wg.Wait() // completes without "close of closed channel" panic
}

// TestAuditManagerFanOut wires a stub exporter and confirms every
// flushed event is mirrored.
func TestAuditManagerFanOut(t *testing.T) {
	tmp := t.TempDir() + "/db"
	db := openTestBolt(t, tmp)
	defer func() { _ = db.Close() }()

	am := NewAuditManager(db, true, 1)
	if err := am.EnsureBuckets(); err != nil {
		t.Fatal(err)
	}
	stub := newStubExporter()
	am.AddExporter(stub)
	am.Start()
	defer am.Stop()

	for i := 0; i < 5; i++ {
		am.Record(AuditEvent{Action: "fan-out", Result: "ok"})
	}
	testsync.WaitForCount(t, "all five events to fan out", 5,
		func() int { return int(stub.delivered.Load()) })

	if got := stub.delivered.Load(); got != 5 {
		t.Errorf("stub delivered=%d, want 5", got)
	}
}

// TestAuditManagerExporters_ListAndStop checks the slot.
func TestAuditManagerExporters_ListAndStop(t *testing.T) {
	tmp := t.TempDir() + "/db"
	db := openTestBolt(t, tmp)
	defer func() { _ = db.Close() }()
	am := NewAuditManager(db, true, 1)
	if err := am.EnsureBuckets(); err != nil {
		t.Fatal(err)
	}
	am.Start()
	stub := newStubExporter()
	am.AddExporter(stub)
	if got := am.Exporters(); len(got) != 1 || got[0].Name() != "stub" {
		t.Errorf("exporters: %+v", got)
	}
	am.Stop()
	// After Stop, exporter list is cleared.
	if got := am.Exporters(); len(got) != 0 {
		t.Errorf("after stop: %+v", got)
	}
}

// TestAddExporterNilSafe — nil receiver and nil exporter must not panic.
func TestAddExporterNilSafe(t *testing.T) {
	var am *AuditManager
	am.AddExporter(nil)
	am2 := &AuditManager{}
	am2.AddExporter(nil) // nil exporter is dropped silently
	if got := am2.Exporters(); len(got) != 0 {
		t.Errorf("expected empty: %+v", got)
	}
}
