package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	bolt "go.etcd.io/bbolt"
)

// The webhook job a cron rule registers, run the way the scheduler runs it,
// and the manager's handling of a database it cannot use.

func TestCronWebhookJobFiresAndSkips(t *testing.T) {
	s := newBackupTestServer(t)
	var hits atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-MDDB-Event") == "cron.fired" {
			hits.Add(1)
		}
	}))
	defer ts.Close()
	wh, err := s.AutomationManager.Create(AutomationRule{Type: "webhook", Name: "hook", URL: ts.URL, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	rule, err := s.AutomationManager.Create(AutomationRule{Type: "cron", Name: "digest", Enabled: true,
		Schedule: "0 9 * * *", WebhookID: wh.ID})
	if err != nil {
		t.Fatal(err)
	}
	cs := NewCronScheduler(s)
	cs.Reload()
	entry := cs.cron.Entry(cs.entryMap[rule.ID])
	if !entry.Valid() {
		t.Fatal("a five-field webhook cron got no entry")
	}

	entry.Job.Run()
	if hits.Load() != 1 {
		t.Errorf("the webhook was called %d times", hits.Load())
	}

	// Disabled since the reload: skipped and logged.
	upd := *s.AutomationManager.Get(wh.ID)
	upd.Enabled = false
	if _, err := s.AutomationManager.Update(wh.ID, upd); err != nil {
		t.Fatal(err)
	}
	entry.Job.Run()
	if hits.Load() != 1 {
		t.Error("a disabled webhook was called")
	}
	logs, _, _ := s.AutomationLogStore.List(10, "", rule.ID, "skipped")
	if len(logs) != 1 || !strings.Contains(logs[0].Error, "disabled") {
		t.Errorf("skipped run log: %+v", logs)
	}

	// No automation manager: nothing to load.
	s.AutomationManager = nil
	cs.Reload()
}

func TestAutomationManagerStorageFailures(t *testing.T) {
	s, cleanup := newTestServerForAutomation(t)
	am := s.AutomationManager
	wh, err := am.Create(AutomationRule{Type: "webhook", Name: "Hook", URL: "https://example.com"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := am.Create(AutomationRule{Type: "trigger", Name: "t", Collection: "c", WebhookID: wh.ID,
		Events: []string{"insert", "renamed"}}); err == nil || !strings.Contains(err.Error(), "invalid event") {
		t.Errorf("an invalid trigger event: %v", err)
	}

	// An entry that cannot be decoded is skipped on load.
	if err := s.DB.Update(func(tx *bolt.Tx) error {
		return tx.Bucket(bucketAutomation).Put([]byte("auto|bad"), []byte("{not json"))
	}); err != nil {
		t.Fatal(err)
	}
	if err := am.LoadAll(); err != nil || len(am.List("")) != 1 {
		t.Errorf("load with a corrupt entry: %v, %d rules", err, len(am.List("")))
	}
	// No bucket: nothing to load.
	if err := s.DB.Update(func(tx *bolt.Tx) error { return tx.DeleteBucket(bucketAutomation) }); err != nil {
		t.Fatal(err)
	}
	if err := am.LoadAll(); err != nil || len(am.List("")) != 0 {
		t.Errorf("load without a bucket: %v", err)
	}
	if err := am.EnsureBucket(); err != nil {
		t.Fatal(err)
	}
	wh, _ = am.Create(AutomationRule{Type: "webhook", Name: "Hook", URL: "https://example.com"})

	// A closed database fails every write and the load.
	cleanup()
	if _, err := am.Create(AutomationRule{Type: "webhook", Name: "x", URL: "https://example.com"}); err == nil {
		t.Error("a create on a closed database succeeded")
	}
	if _, err := am.Update(wh.ID, AutomationRule{Name: "y", URL: "https://example.com"}); err == nil {
		t.Error("an update on a closed database succeeded")
	}
	if err := am.LoadAll(); err == nil {
		t.Error("a load from a closed database succeeded")
	}
}
