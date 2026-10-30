package main

import (
	"strings"
	"testing"
	"time"
)

// What a sync action may say, and the defaults it gets.
func TestSyncActionNormalize(t *testing.T) {
	ok := func() *SyncAction {
		return &SyncAction{Source: SyncSourceURLs, URLs: []string{"https://example.com/a"}, Collection: " c ", Lang: "en"}
	}
	a := ok()
	if err := a.normalize(); err != nil || a.Collection != "c" || a.MaxDocuments != defaultSyncMaxDocuments || a.MaxBytes != defaultSyncMaxBytes {
		t.Errorf("defaults: %+v, %v", a, err)
	}
	for want, change := range map[string]func(*SyncAction){
		"collection and lang":     func(a *SyncAction) { a.Lang = "" },
		"maxDocuments":            func(a *SyncAction) { a.MaxDocuments = maxSyncMaxDocuments + 1 },
		"maxBytes":                func(a *SyncAction) { a.MaxBytes = -1 },
		"needs urls":              func(a *SyncAction) { a.URLs = nil },
		"absolute http":           func(a *SyncAction) { a.URLs = []string{"/relative"} },
		"invalid sync source":     func(a *SyncAction) { a.Source = "git" },
		"storageConfig.endpoint":  func(a *SyncAction) { a.Source = SyncSourceS3 },
		"absolute http or https ": func(a *SyncAction) { a.Source, a.URL = SyncSourceSitemap, "file:///etc/passwd" },
	} {
		a := ok()
		change(a)
		if err := a.normalize(); err == nil || !strings.Contains(err.Error(), strings.TrimSpace(want)) {
			t.Errorf("%s: %v", want, err)
		}
	}

	s3 := &SyncAction{Source: SyncSourceS3, Collection: "c", Lang: "en",
		StorageConfig: &StorageConfigDef{Endpoint: "e", Bucket: "b", Prefix: "p/", SecretKeySet: true}}
	if err := s3.normalize(); err != nil || s3.Bucket != "b" || s3.Prefix != "p/" || s3.StorageConfig.SecretKeySet {
		t.Errorf("s3 defaults: %+v, %v", s3, err)
	}
}

func TestSyncKey(t *testing.T) {
	for in, want := range map[string]string{
		"/":                                   "index",
		"":                                    "index",
		"/blog/Post 1.html":                   "blog-post-1",
		"/docs/v1.2/":                         "docs-v1",
		"/a/b.c/d":                            "a-b.c-d",
		"/../..":                              "index",
		"/" + strings.Repeat("ab/", 100):      strings.Repeat("ab-", 67)[:200],
		"/" + strings.Repeat("a", 199) + "/x": strings.Repeat("a", 199),
	} {
		if got := syncKey(in); got != want {
			t.Errorf("syncKey(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSyncActionDescribe(t *testing.T) {
	for want, a := range map[string]*SyncAction{
		"https://x/sitemap.xml": {Source: SyncSourceSitemap, URL: "https://x/sitemap.xml"},
		"2 urls":                {Source: SyncSourceURLs, URLs: []string{"a", "b"}},
		"s3://b/p/":             {Source: SyncSourceS3, Bucket: "b", Prefix: "p/"},
	} {
		if got := a.describe(); got != want {
			t.Errorf("describe = %q, want %q", got, want)
		}
	}
}

// carryOverAction: an update naming no action keeps the stored one; naming
// one keeps the stored secret when it sends none.
func TestCarryOverActionSync(t *testing.T) {
	existing := &AutomationRule{Sync: &SyncAction{Source: SyncSourceS3, StorageConfig: &StorageConfigDef{SecretKey: "s"}}}
	u := &AutomationRule{}
	carryOverAction(u, existing)
	if u.Sync == nil || u.Sync.Source != SyncSourceS3 || u.Backup != nil {
		t.Errorf("not carried over: %+v", u)
	}
	u = &AutomationRule{Sync: &SyncAction{StorageConfig: &StorageConfigDef{}}}
	carryOverAction(u, existing)
	if u.Sync.StorageConfig.SecretKey != "s" {
		t.Error("the stored secret was lost")
	}
	// A switch from a backup to a sync does not bring the backup along.
	u = &AutomationRule{Sync: &SyncAction{}}
	carryOverAction(u, &AutomationRule{Backup: &BackupAction{}})
	if u.Backup != nil {
		t.Error("a switch to a sync kept the backup")
	}
	if redactedSync(nil) != nil || redactedStorageConfig(nil) != nil {
		t.Error("nil redacted to something")
	}
}

// A sync rule through the AutomationManager: only a cron takes it, alone;
// an update that leaves it out keeps it.
func TestAutomationManagerSyncRules(t *testing.T) {
	s := newBackupTestServer(t)
	am := s.AutomationManager
	sync := func() *SyncAction {
		return &SyncAction{Source: SyncSourceURLs, URLs: []string{"https://example.com/"}, Collection: "c", Lang: "en"}
	}
	if _, err := am.Create(AutomationRule{Type: "trigger", Name: "t", Sync: sync()}); err == nil ||
		!strings.Contains(err.Error(), "action of a cron rule") {
		t.Errorf("a trigger with a sync: %v", err)
	}
	if _, err := am.Create(AutomationRule{Type: "cron", Name: "c", Schedule: "0 3 * * *", Sync: sync(), Backup: &BackupAction{}}); err == nil {
		t.Error("a cron with a sync and a backup")
	}
	created, err := am.Create(AutomationRule{Type: "cron", Name: "c", Schedule: "0 3 * * *", Sync: sync()})
	if err != nil {
		t.Fatal(err)
	}
	updated, err := am.Update(created.ID, AutomationRule{Name: "renamed", Enabled: true, Schedule: "0 4 * * *"})
	if err != nil || updated.Sync == nil || updated.Sync.MaxDocuments != defaultSyncMaxDocuments {
		t.Errorf("an update without the action: %+v, %v", updated, err)
	}
}

// The scheduler runs a sync rule's sync, rereading the rule when it fires:
// a rule switched to a webhook since the reload takes no action.
func TestCronSchedulerRunsSync(t *testing.T) {
	s := newBackupTestServer(t)
	site := newFakeSite(t, map[string]string{"/p": page("P", "p")})
	rule := syncRule(t, s, &SyncAction{Source: SyncSourceURLs, URLs: []string{site.URL + "/p"}, Collection: "web", Lang: "en"})
	cs := NewCronScheduler(s)
	s.CronScheduler = cs
	cs.Reload()
	entry := cs.cron.Entry(cs.entryMap[rule.ID])
	if !entry.Valid() {
		t.Fatal("the sync rule has no cron entry")
	}
	clock := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	cs.now = func() time.Time { return clock }
	entry.Job.Run()
	if countDocs(t, s, "web") != 1 {
		t.Fatal("the scheduled sync wrote nothing")
	}
	if got := s.AutomationManager.Get(rule.ID).LastRun; got != clock.Unix() {
		t.Errorf("lastRun %d", got)
	}

	// Failing runs are logged, and still stamp lastRun.
	site.del("/p")
	entry.Job.Run()

	wh, err := s.AutomationManager.Create(AutomationRule{Type: "webhook", Name: "h", URL: "https://example.com"})
	if err != nil {
		t.Fatal(err)
	}
	s.AutomationManager.mu.Lock()
	for i := range s.AutomationManager.rules {
		if s.AutomationManager.rules[i].ID == rule.ID {
			s.AutomationManager.rules[i].Sync, s.AutomationManager.rules[i].WebhookID = nil, wh.ID
		}
	}
	s.AutomationManager.mu.Unlock()
	clock = clock.Add(time.Hour)
	entry.Job.Run()
	if s.AutomationManager.Get(rule.ID).LastRun == clock.Unix() {
		t.Error("a rule without an action of its own ran one")
	}
}
