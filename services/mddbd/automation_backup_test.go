package main

import (
	"strings"
	"testing"
	"time"
)

// The backup action's rules (OPS-023): defaults, what is refused, and what a
// client sees of its credentials.

func TestBackupActionNormalizeDefaults(t *testing.T) {
	a := &BackupAction{}
	if err := a.normalize(); err != nil {
		t.Fatal(err)
	}
	if a.Target != BackupTargetLocal || a.Prefix != defaultBackupPrefix || !a.allCollections() || a.Keep != 0 {
		t.Errorf("defaults: %+v", a)
	}
	if got := a.name(time.Date(2026, 10, 30, 3, 4, 5, 0, time.FixedZone("CET", 3600))); got != "mddb-20261030T020405Z.db" {
		t.Errorf("name in UTC: %s", got)
	}

	// The bucket and prefix of storageConfig fill in for the action's; the
	// output-only flag never reaches storage.
	s3 := &BackupAction{Target: "s3", StorageConfig: &StorageConfigDef{
		Endpoint: "s3.example.com", Bucket: "b", Prefix: "eu/", SecretKeySet: true}}
	if err := s3.normalize(); err != nil {
		t.Fatal(err)
	}
	if s3.Bucket != "b" || s3.Prefix != "eu/" || s3.StorageConfig.SecretKeySet {
		t.Errorf("s3 fallbacks: %+v %+v", s3, s3.StorageConfig)
	}
}

func TestBackupActionNormalizeRefuses(t *testing.T) {
	for _, tc := range []struct {
		a    BackupAction
		want string
	}{
		{BackupAction{Prefix: "a b"}, "prefix"},
		{BackupAction{Prefix: "../x"}, "prefix"},
		{BackupAction{Keep: -1}, "keep"},
		{BackupAction{Collections: []string{"*", "blog"}}, "stands alone"},
		{BackupAction{Prefix: "dir/x-"}, "no '/'"},
		{BackupAction{Path: "../out"}, "relative path"},
		{BackupAction{Path: "/abs"}, "relative path"},
		{BackupAction{Target: "s3"}, "endpoint"},
		{BackupAction{Target: "s3", StorageConfig: &StorageConfigDef{Endpoint: "e"}}, "bucket"},
		{BackupAction{Target: "ftp"}, "invalid backup target"},
	} {
		a := tc.a
		if err := a.normalize(); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%+v: %v, want %q", tc.a, err, tc.want)
		}
	}
}

func TestBackupActionNamesItsBackupsOnly(t *testing.T) {
	a := &BackupAction{Prefix: "n-"}
	for name, want := range map[string]bool{
		"n-20261030T030000Z.db":        true,
		"n-20261030T030000Z.db.sha256": false,
		"m-20261030T030000Z.db":        false,
		"n-notes.db":                   false,
		"n-20261030T030000Z.txt":       false,
	} {
		if got := a.isBackupName(name); got != want {
			t.Errorf("isBackupName(%q) = %v", name, got)
		}
	}
}

func TestAutomationRuleRedactedHidesTheSecret(t *testing.T) {
	plain := AutomationRule{Type: "webhook"}
	if got := plain.redacted(); got.Backup != nil {
		t.Error("a rule without a backup grew one")
	}
	r := AutomationRule{Backup: &BackupAction{Collections: []string{"a"},
		StorageConfig: &StorageConfigDef{SecretKey: "s"}}}
	out := r.redacted()
	if out.Backup.StorageConfig.SecretKey != "" || !out.Backup.StorageConfig.SecretKeySet {
		t.Errorf("redacted: %+v", out.Backup.StorageConfig)
	}
	out.Backup.Collections[0] = "changed"
	if r.Backup.StorageConfig.SecretKey != "s" || r.Backup.Collections[0] != "a" {
		t.Error("redacting changed the stored rule")
	}
	local := AutomationRule{Backup: &BackupAction{}}
	if got := local.redacted(); got.Backup.StorageConfig != nil {
		t.Error("a local backup grew a storage config")
	}
}

func TestCarryOverAction(t *testing.T) {
	existing := &AutomationRule{Backup: &BackupAction{Target: "s3", StorageConfig: &StorageConfigDef{SecretKey: "s"}}}

	// No backup and no webhook: a client that does not know the field.
	u := &AutomationRule{}
	carryOverAction(u, existing)
	if u.Backup == nil || u.Backup.Target != "s3" {
		t.Errorf("not carried over: %+v", u.Backup)
	}
	// A switch to a webhook drops the backup.
	u = &AutomationRule{WebhookID: "wh"}
	carryOverAction(u, existing)
	if u.Backup != nil {
		t.Error("a switch to a webhook kept the backup")
	}
	// A blank secret keeps the stored one; a new one replaces it.
	u = &AutomationRule{Backup: &BackupAction{StorageConfig: &StorageConfigDef{}}}
	carryOverAction(u, existing)
	if u.Backup.StorageConfig.SecretKey != "s" {
		t.Error("the stored secret was lost")
	}
	u = &AutomationRule{Backup: &BackupAction{StorageConfig: &StorageConfigDef{SecretKey: "new"}}}
	carryOverAction(u, existing)
	if u.Backup.StorageConfig.SecretKey != "new" {
		t.Error("a new secret was overwritten")
	}
	// Nothing stored: nothing to carry.
	u = &AutomationRule{}
	carryOverAction(u, &AutomationRule{})
	if u.Backup != nil {
		t.Error("carried a backup from a rule without one")
	}
}

func TestAutomationManagerBackupRules(t *testing.T) {
	s, cleanup := newTestServerForAutomation(t)
	defer cleanup()
	am := s.AutomationManager
	wh, err := am.Create(AutomationRule{Type: "webhook", Name: "Hook", URL: "https://example.com"})
	if err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		rule AutomationRule
		want string
	}{
		{AutomationRule{Type: "trigger", Name: "t", Backup: &BackupAction{}}, "action of a cron rule"},
		{AutomationRule{Type: "cron", Name: "c", Schedule: "0 3 * * *", WebhookID: wh.ID, Backup: &BackupAction{}}, "one of webhookId, backup and sync"},
		{AutomationRule{Type: "cron", Name: "c", Schedule: "0 3 * * *"}, "webhookId, backup or sync"},
		{AutomationRule{Type: "cron", Name: "c", Schedule: "nightly"}, "invalid schedule"},
		{AutomationRule{Type: "cron", Name: "c", Schedule: "0 3 * * *", WebhookID: "absent"}, "webhook not found"},
		{AutomationRule{Type: "cron", Name: "c", Schedule: "0 3 * * *", Backup: &BackupAction{Keep: -2}}, "keep"},
	} {
		if _, err := am.Create(tc.rule); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%+v: %v, want %q", tc.rule, err, tc.want)
		}
	}

	// A six-field schedule still works next to the five-field one.
	if _, err := am.Create(AutomationRule{Type: "cron", Name: "six", Schedule: "0 0 3 * * *", WebhookID: wh.ID}); err != nil {
		t.Errorf("a six-field schedule: %v", err)
	}

	created, err := am.Create(AutomationRule{Type: "cron", Name: "offsite", Schedule: "0 3 * * *",
		Backup: &BackupAction{Target: "s3", Bucket: "b", StorageConfig: &StorageConfigDef{Endpoint: "e", SecretKey: "s"}}})
	if err != nil {
		t.Fatal(err)
	}
	for what, r := range map[string]*AutomationRule{"create": created, "get": am.Get(created.ID)} {
		if r.Backup.StorageConfig.SecretKey != "" || !r.Backup.StorageConfig.SecretKeySet {
			t.Errorf("%s shows the secret", what)
		}
	}
	for _, r := range am.List("cron") {
		if r.Backup != nil && r.Backup.StorageConfig.SecretKey != "" {
			t.Error("list shows the secret")
		}
	}

	// An update that sends the rule back as read keeps the secret; one that
	// knows nothing of backups (gRPC) keeps the whole action.
	readBack := *am.Get(created.ID)
	readBack.Name = "renamed"
	updated, err := am.Update(created.ID, readBack)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Backup.StorageConfig.SecretKey != "" || am.stored(created.ID).Backup.StorageConfig.SecretKey != "s" {
		t.Error("the update lost or showed the secret")
	}
	if _, err := am.Update(created.ID, AutomationRule{Name: "toggled", Schedule: "0 4 * * *", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if st := am.stored(created.ID); st.Backup == nil || st.Backup.Bucket != "b" || !st.Enabled {
		t.Errorf("a gRPC-style toggle lost the backup: %+v", st)
	}
	if am.stored("absent") != nil || am.Get("absent") != nil {
		t.Error("an absent rule was found")
	}
}
