package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"mddb/internal/automationlog"
	"mddb/internal/webhooks"
	pb "mddb/proto"

	json "mddb/internal/jsonx"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// #377: a sync or backup rule runs now through the scheduler's path, and the
// automation listing, filtered by collection, shows each source's last run.

// runNowServer is a backup test server with a scheduler whose clock is fixed.
func runNowServer(t *testing.T) (*Server, time.Time) {
	t.Helper()
	s := newBackupTestServer(t)
	now := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	s.CronScheduler = NewCronScheduler(s)
	s.CronScheduler.now = func() time.Time { return now }
	t.Cleanup(s.CronScheduler.Stop)
	return s, now
}

// postRun calls POST /v1/automation/{id}/run.
func postRun(s *Server, id string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	s.handleAutomationDetail(w, httptest.NewRequest(http.MethodPost, "/v1/automation/"+id+"/run", nil))
	return w
}

// listByCollection calls GET /v1/automation?collection=c.
func listByCollection(t *testing.T, s *Server, c string) []AutomationRule {
	t.Helper()
	w := httptest.NewRecorder()
	s.handleAutomation(w, httptest.NewRequest(http.MethodGet, "/v1/automation?collection="+c, nil))
	var body struct{ Rules []AutomationRule }
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	return body.Rules
}

func TestRunNowSyncThroughTheScheduledPath(t *testing.T) {
	s, now := runNowServer(t)
	site := newFakeSite(t, map[string]string{"/a.html": page("A", "alpha"), "/b": page("B", "beta")})
	synced := sourceSyncedReceiver(t, s)
	rule := syncRule(t, s, &SyncAction{Source: SyncSourceSitemap, URL: site.URL + "/sitemap.xml", Collection: "web", Lang: "en"})
	// A manual run is how a rule is tried before it is enabled.
	off := *s.AutomationManager.Get(rule.ID)
	off.Enabled = false
	if _, err := s.AutomationManager.Update(rule.ID, off); err != nil {
		t.Fatal(err)
	}
	_, events := collectSSE(s.SSEHub, "", "")

	w := postRun(s, rule.ID)
	var run AutomationRun
	if err := json.Unmarshal(w.Body.Bytes(), &run); err != nil || w.Code != http.StatusAccepted {
		t.Fatalf("run: %d %s", w.Code, w.Body)
	}
	wantJob := "sync-" + rule.ID + "-20261030T120000Z"
	if run.JobID != wantJob || run.Action != actionSync || run.Status != automationRunStarted || run.StartedAt != now.Unix() {
		t.Errorf("run: %+v", run)
	}
	s.CronScheduler.runs.Wait()

	if countDocs(t, s, "web") != 2 {
		t.Errorf("documents synced: %d", countDocs(t, s, "web"))
	}
	if got := events(); len(got) == 0 || got[len(got)-1].Job.ID != wantJob || got[len(got)-1].Event != SSEJobCompleted {
		t.Errorf("SSE: %+v", got)
	}
	if p := <-synced; p["event"] != webhooks.EventSourceSynced || detailOf(p)["added"] != 2.0 {
		t.Errorf("source.synced: %v", p)
	}
	if got := s.AutomationManager.Get(rule.ID).LastRun; got != now.Unix() {
		t.Errorf("lastRun: %d", got)
	}

	rules := listByCollection(t, s, "web")
	if len(rules) != 1 || rules[0].ID != rule.ID || rules[0].LastResult == nil {
		t.Fatalf("the sources of web: %+v", rules)
	}
	if lr := rules[0].LastResult; lr.Status != "success" || lr.Added != 2 || lr.Error != "" {
		t.Errorf("last result: %+v", lr)
	}
	if got := listByCollection(t, s, "other"); len(got) != 0 {
		t.Errorf("the sources of another collection: %+v", got)
	}
}

func TestRunNowWhileRunningIsSkippedAndLogged(t *testing.T) {
	s, _ := runNowServer(t)
	rule := syncRule(t, s, &SyncAction{Source: SyncSourceURLs, URLs: []string{"http://127.0.0.1:1/x"}, Collection: "web", Lang: "en"})
	syncRuns.Store(rule.ID, true)
	defer syncRuns.Delete(rule.ID)

	if w := postRun(s, rule.ID); w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "still going") {
		t.Errorf("a run while one is going: %d %s", w.Code, w.Body)
	}
	s.CronScheduler.runs.Wait()
	logs, _, _ := s.AutomationLogStore.List(10, "", rule.ID, "skipped")
	if len(logs) != 1 {
		t.Fatalf("the skip was not logged: %+v", logs)
	}
	if lr := listByCollection(t, s, "web")[0].LastResult; lr == nil || lr.Status != "skipped" {
		t.Errorf("last result: %+v", lr)
	}
}

func TestRunNowBackupAndRefusals(t *testing.T) {
	s, _ := runNowServer(t)
	backup, err := s.AutomationManager.Create(AutomationRule{Type: "cron", Name: "nightly", Enabled: true,
		Schedule: "0 3 * * *", Backup: &BackupAction{}})
	if err != nil {
		t.Fatal(err)
	}
	run, err := s.runAutomationNow(backup.ID)
	if err != nil || run.Action != actionBackup || !strings.HasPrefix(run.JobID, "backup-"+backup.ID+"-") {
		t.Fatalf("backup run: %+v, %v", run, err)
	}
	s.CronScheduler.runs.Wait()
	if lr := s.lastRunResult(backup); lr == nil || lr.Status != "success" || lr.Added != 0 {
		t.Errorf("backup last result: %+v", lr)
	}

	hook, _ := s.AutomationManager.Create(AutomationRule{Type: "webhook", Name: "h", URL: "https://example.com"})
	for id, want := range map[string]int{
		"missing": http.StatusNotFound,
		hook.ID:   http.StatusBadRequest,
	} {
		if w := postRun(s, id); w.Code != want {
			t.Errorf("run %s: %d %s", id, w.Code, w.Body)
		}
	}
	w := httptest.NewRecorder()
	s.handleAutomationDetail(w, httptest.NewRequest(http.MethodGet, "/v1/automation/"+backup.ID+"/run", nil))
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET run: %d", w.Code)
	}
	s.CronScheduler = nil
	if w := postRun(s, backup.ID); w.Code != http.StatusServiceUnavailable {
		t.Errorf("run without a scheduler: %d", w.Code)
	}
}

func TestLastRunResultEdges(t *testing.T) {
	s, _ := runNowServer(t)
	trigger := &AutomationRule{Type: "trigger", ID: "t"}
	if s.lastRunResult(trigger) != nil {
		t.Error("a trigger has a last result")
	}
	rule := syncRule(t, s, &SyncAction{Source: SyncSourceURLs, URLs: []string{"http://127.0.0.1:1/x"}, Collection: "web", Lang: "en"})
	if s.lastRunResult(&rule) != nil {
		t.Error("a rule that never ran has a last result")
	}
	_ = s.AutomationLogStore.Log(automationlog.Entry{RuleID: rule.ID, Status: "error", Error: "boom",
		Sync: &automationlog.SyncCounts{Failed: 1}})
	if lr := s.lastRunResult(&rule); lr == nil || lr.Failed != 1 || lr.Error != "boom" {
		t.Errorf("last result: %+v", lr)
	}
	s.AutomationLogStore = nil
	if s.lastRunResult(&rule) != nil {
		t.Error("a last result without a log")
	}
	// A client cannot store a last result: REST drops one in a create or an
	// update body (gRPC and MCP have no way to send one).
	w := callAutomation(s, http.MethodPost, "/v1/automation",
		`{"type":"webhook","name":"h","url":"https://example.com","lastResult":{"status":"x"}}`)
	var created AutomationRule
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil || s.AutomationManager.stored(created.ID).LastResult != nil {
		t.Errorf("a stored last result: %s", w.Body)
	}
	callAutomation(s, http.MethodPut, "/v1/automation/"+created.ID, `{"name":"h","url":"https://example.com","lastResult":{"status":"x"}}`)
	if s.AutomationManager.stored(created.ID).LastResult != nil {
		t.Error("an update stored a last result")
	}
	if (&AutomationRule{Type: "cron"}).actsOn("web") {
		t.Error("a webhook cron acts on a collection")
	}
	if action, _ := (&AutomationRule{Type: "cron", WebhookID: "w"}).cronAction(); action != "" {
		t.Errorf("a webhook cron has action %q", action)
	}
}

func TestGRPCRunAutomationAndSources(t *testing.T) {
	s, _ := runNowServer(t)
	g := &GRPCServer{server: s}
	ctx := context.Background()
	rule := syncRule(t, s, &SyncAction{Source: SyncSourceURLs, URLs: []string{"http://127.0.0.1:1/x"}, Collection: "web", Lang: "en"})

	resp, err := g.RunAutomation(ctx, &pb.RunAutomationRequest{Id: rule.ID})
	if err != nil || resp.GetAction() != actionSync || resp.GetJobId() == "" || resp.GetStatus() != automationRunStarted {
		t.Fatalf("RunAutomation: %+v, %v", resp, err)
	}
	s.CronScheduler.runs.Wait()
	list, err := g.ListAutomation(ctx, &pb.ListAutomationRequest{Collection: "web"})
	if err != nil || len(list.GetRules()) != 1 {
		t.Fatalf("ListAutomation(web): %+v, %v", list, err)
	}
	if lr := list.GetRules()[0].GetLastResult(); lr.GetStatus() != "error" || lr.GetFailed() != 1 || lr.GetError() == "" {
		t.Errorf("last result: %+v", lr)
	}

	for req, want := range map[string]codes.Code{"": codes.InvalidArgument, "missing": codes.NotFound} {
		if _, err := g.RunAutomation(ctx, &pb.RunAutomationRequest{Id: req}); status.Code(err) != want {
			t.Errorf("RunAutomation(%q): %v", req, err)
		}
	}
	for err, want := range map[error]codes.Code{
		errActionRunning:   codes.Aborted,
		errSchedulerOff:    codes.Unavailable,
		errRuleNotRunnable: codes.InvalidArgument,
	} {
		if got := automationRunCode(err); got != want {
			t.Errorf("%v: %v", err, got)
		}
	}
	if automationRunResultToProto(nil) != nil {
		t.Error("a nil result converted")
	}
}

func TestMCPRunAutomationAndSources(t *testing.T) {
	s, _ := runNowServer(t)
	c := &DirectClient{server: s}
	ctx := context.Background()
	rule := syncRule(t, s, &SyncAction{Source: SyncSourceURLs, URLs: []string{"http://127.0.0.1:1/x"}, Collection: "web", Lang: "en"})
	run, err := c.RunAutomation(ctx, rule.ID)
	if err != nil || run.RuleID != rule.ID {
		t.Fatalf("RunAutomation: %+v, %v", run, err)
	}
	s.CronScheduler.runs.Wait()
	if list, err := c.ListAutomation(ctx, "", "web"); err != nil || list.Total != 1 || list.Rules[0].LastResult == nil {
		t.Errorf("ListAutomation(web): %+v, %v", list, err)
	}
	s.AutomationManager = nil
	if _, err := c.RunAutomation(ctx, rule.ID); err == nil || !strings.Contains(err.Error(), "not initialized") {
		t.Errorf("RunAutomation without a manager: %v", err)
	}
}
