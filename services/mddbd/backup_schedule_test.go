package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"mddb/internal/automationlog"
	"mddb/internal/metrics"
	"mddb/internal/webhooks"

	json "mddb/internal/jsonx"

	bolt "go.etcd.io/bbolt"
)

// OPS-023 acceptance: scheduled backups in automations, kept to `keep` on a
// local directory or an S3-compatible bucket.

// newBackupTestServer is a handler test server with automations, their log,
// metrics, the SSE hub and a backup directory of its own. It follows
// MDDB_STORAGE_LAYOUT, so the suite runs these in both layouts.
func newBackupTestServer(t *testing.T) *Server {
	t.Helper()
	t.Setenv("MDDB_BACKUP_DIR", t.TempDir())
	s, cleanup := newHandlerTestServer(t)
	t.Cleanup(func() { s.closeCollectionFiles(); cleanup() })
	s.AutomationManager = NewAutomationManager(s.DB)
	s.AutomationManager.FollowDB(func() *bolt.DB { return s.DB })
	s.AutomationManager.SetServer(s)
	s.AutomationLogStore = automationlog.NewStore(s.DB, time.Hour)
	s.AutomationLogStore.FollowDB(func() *bolt.DB { return s.DB })
	for _, step := range []func() error{s.AutomationManager.EnsureBucket, s.AutomationLogStore.EnsureBucket} {
		if err := step(); err != nil {
			t.Fatal(err)
		}
	}
	s.Metrics = metrics.NewMetrics(true, &serverMetricsStats{s: s})
	s.SSEHub = NewSSEHub(true, 10, 10)
	return s
}

// backupsIn lists the backup main files in dir, oldest first.
func backupsIn(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		if e.Type().IsRegular() && strings.HasSuffix(e.Name(), ".db") {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out
}

// scrape returns the /metrics page.
func scrape(t *testing.T, s *Server) string {
	t.Helper()
	rec := httptest.NewRecorder()
	s.Metrics.HandleMetrics(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	return rec.Body.String()
}

// backupFailedReceiver registers an incident webhook for backup.failed and
// returns the payloads it receives.
func backupFailedReceiver(t *testing.T, s *Server) chan map[string]any {
	t.Helper()
	got := make(chan map[string]any, 4)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var p map[string]any
		_ = json.NewDecoder(r.Body).Decode(&p)
		got <- p
	}))
	t.Cleanup(ts.Close)
	if _, err := s.WebhookManager.Register(ts.URL, []string{webhooks.EventBackupFailed}, ""); err != nil {
		t.Fatal(err)
	}
	return got
}

// A cron "0 3 * * *" with keep 7, run for eight nights on a fake clock: seven
// backups remain, and the oldest goes only once the eighth has succeeded — a
// failed eighth run removes nothing.
func TestScheduledBackupKeepsSevenOfEightNightlyRuns(t *testing.T) {
	s := newBackupTestServer(t)
	addTestDoc(t, s, "blog", "a", "en", "kept", nil)
	failed := backupFailedReceiver(t, s)

	rule, err := s.AutomationManager.Create(AutomationRule{Type: "cron", Name: "nightly", Enabled: true,
		Schedule: "0 3 * * *", Backup: &BackupAction{Path: "nightly", Keep: 7}})
	if err != nil {
		t.Fatal(err)
	}
	cs := NewCronScheduler(s)
	s.CronScheduler = cs
	cs.Reload()
	entry := cs.cron.Entry(cs.entryMap[rule.ID])
	if !entry.Valid() {
		t.Fatal("the backup rule has no cron entry")
	}
	clock := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	cs.now = func() time.Time { return clock }
	_, events := collectSSE(s.SSEHub, "", "")
	dir := filepath.Join(backupDir(), "nightly")

	var first string
	for run := 1; run <= 7; run++ {
		clock = entry.Schedule.Next(clock)
		if clock.Hour() != 3 || clock.Minute() != 0 {
			t.Fatalf("the schedule fired at %s", clock)
		}
		entry.Job.Run()
		got := backupsIn(t, dir)
		if len(got) != run {
			t.Fatalf("after run %d: %d backups %v", run, len(got), got)
		}
		first = got[0]
	}
	if first != "mddb-20261030T030000Z.db" {
		t.Errorf("the first backup is %q", first)
	}

	// The eighth run fails: nothing is removed, the failure is announced.
	clock = entry.Schedule.Next(clock)
	t.Setenv("MDDB_VERIFY_TIMEOUT", "1ns")
	entry.Job.Run()
	t.Setenv("MDDB_VERIFY_TIMEOUT", "")
	if got := backupsIn(t, dir); len(got) != 7 || got[0] != first {
		t.Fatalf("a failed run changed the backups: %v", got)
	}
	select {
	case p := <-failed:
		if p["event"] != webhooks.EventBackupFailed || detailOf(p)["ruleId"] != rule.ID {
			t.Errorf("backup.failed payload: %v", p)
		}
	case <-time.After(5 * time.Second):
		t.Error("no backup.failed webhook")
	}

	// It succeeds: the oldest is removed, seven remain.
	entry.Job.Run()
	got := backupsIn(t, dir)
	want := "mddb-" + clock.Format(backupStampLayout) + ".db"
	if len(got) != 7 || got[0] == first || got[6] != want {
		t.Fatalf("after the eighth success: %v", got)
	}
	if _, err := os.Stat(filepath.Join(dir, sumFileName(first))); !os.IsNotExist(err) {
		t.Error("the oldest backup's checksum file is left")
	}
	assertChecksumFile(t, dir, want)

	// The automation log has every run; the last names what it removed.
	logs, _, err := s.AutomationLogStore.List(100, "", rule.ID, "")
	if err != nil || len(logs) != 9 {
		t.Fatalf("log: %d entries, %v", len(logs), err)
	}
	statuses := map[string]int{}
	removed := false
	for _, e := range logs {
		statuses[e.Status]++
		if e.Action != "backup" {
			t.Errorf("log entry action %q", e.Action)
		}
		if len(e.Removed) == 1 && e.Removed[0] == first {
			removed = true
		}
	}
	if statuses["success"] != 8 || statuses["error"] != 1 || !removed {
		t.Errorf("log statuses %v, removal logged %v", statuses, removed)
	}

	// The metric holds the time of the last success.
	if page := scrape(t, s); !strings.Contains(page,
		fmt.Sprintf("%s{rule=%q} %d\n", backupSuccessGauge, rule.ID, clock.Unix())) {
		t.Errorf("metric missing:\n%s", grepLines(page, "mddb_backup"))
	}

	// SSE: each run started and ended; the failed one as job.failed.
	kinds := map[string]int{}
	for _, e := range events() {
		kinds[e.Event]++
	}
	if kinds[SSEJobStarted] != 9 || kinds[SSEJobCompleted] != 8 || kinds[SSEJobFailed] != 1 {
		t.Errorf("SSE events: %v", kinds)
	}
}

// detailOf is a webhook payload's detail object.
func detailOf(p map[string]any) map[string]any {
	d, _ := p["detail"].(map[string]any)
	return d
}

// grepLines keeps the lines of page containing s.
func grepLines(page, s string) string {
	var out []string
	for _, l := range strings.Split(page, "\n") {
		if strings.Contains(l, s) {
			out = append(out, l)
		}
	}
	return strings.Join(out, "\n")
}

// assertChecksumFile checks dir/name's checksum file against the files.
func assertChecksumFile(t *testing.T, dir, name string) {
	t.Helper()
	root, err := os.OpenRoot(dir) // what the checksum file names is read inside dir only
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	list, err := root.ReadFile(sumFileName(name))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(list)), "\n")
	if !strings.HasSuffix(lines[0], "  "+name) {
		t.Errorf("the checksum file does not start with the main file: %q", lines[0])
	}
	for _, l := range lines {
		sum, rel, _ := strings.Cut(l, "  ")
		data, err := root.ReadFile(rel)
		if err != nil {
			t.Fatal(err)
		}
		h := sha256.Sum256(data)
		if hex.EncodeToString(h[:]) != sum {
			t.Errorf("%s: checksum file says %s", rel, sum)
		}
	}
	if perCollectionLayout() && len(lines) < 2 {
		t.Error("the per-collection backup's checksum file lists no collection file")
	}
}
