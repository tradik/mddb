package main

import (
	"net/http"
	"strings"
	"testing"
)

// OPS-023: `backup schedule` is a thin layer over /v1/automation. These check
// the rule it sends, what it lists, and that remove touches backup schedules
// only.

func TestBackupScheduleAddLocal(t *testing.T) {
	fs := newFakeServer(t, map[string]interface{}{
		"POST /v1/automation": map[string]interface{}{"id": "r1", "schedule": "0 3 * * *",
			"backup": map[string]interface{}{"target": "local", "path": "nightly", "prefix": "mddb-"}},
	})
	out, err := runCLI(t, fs.URL, "backup", "schedule", "add", "--name", "nightly", "--cron", "0 3 * * *",
		"--keep", "7", "--path", "nightly", "--collections", "blog,wiki")
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, out, "Backup schedule created: r1")
	mustContain(t, out, "local:nightly/mddb-")
	body := fs.lastCall(t).Body
	assertBodyField(t, body, "type", "cron")
	assertBodyField(t, body, "schedule", "0 3 * * *")
	assertBodyField(t, body, "enabled", true)
	assertBodyField(t, body, "backup", map[string]interface{}{"target": "local", "keep": 7, "path": "nightly",
		"collections": []string{"blog", "wiki"}})
}

func TestBackupScheduleAddS3TakesTheSecretFromTheEnvironment(t *testing.T) {
	t.Setenv(secretKeyEnv, "from-env")
	fs := newFakeServer(t, map[string]interface{}{"POST /v1/automation": `{"id":"r2"}`})
	if _, err := runCLI(t, fs.URL, "backup", "schedule", "add", "--name", "offsite", "--cron", "0 4 * * *",
		"--target", "s3", "--endpoint", "s3.example.com", "--bucket", "b", "--prefix", "prod/", "--tls",
		"--access-key", "AK", "--disabled"); err != nil {
		t.Fatal(err)
	}
	body := fs.lastCall(t).Body
	assertBodyField(t, body, "enabled", false)
	backup, _ := body["backup"].(map[string]interface{})
	assertBodyField(t, backup, "storageConfig", map[string]interface{}{"endpoint": "s3.example.com",
		"region": "", "accessKey": "AK", "secretKey": "from-env", "useTLS": true})
	assertBodyField(t, backup, "bucket", "b")

	// The flag wins over the environment; --json prints the server's answer.
	out, err := runCLI(t, fs.URL, "--json", "backup", "schedule", "add", "--name", "o", "--cron", "0 4 * * *",
		"--target", "s3", "--secret-key", "from-flag")
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, out, `{"id":"r2"}`)
	sc, _ := fs.lastCall(t).Body["backup"].(map[string]interface{})["storageConfig"].(map[string]interface{})
	if sc["secretKey"] != "from-flag" {
		t.Errorf("secretKey = %v", sc["secretKey"])
	}
}

func TestBackupScheduleAddFailures(t *testing.T) {
	fs := newFakeServer(t, map[string]interface{}{
		"POST /v1/automation": failure{http.StatusBadRequest, `{"error":"invalid schedule"}`},
	})
	if _, err := runCLI(t, fs.URL, "backup", "schedule", "add", "--name", "x"); err == nil ||
		!strings.Contains(err.Error(), "required") {
		t.Errorf("missing --cron: %v", err)
	}
	if _, err := runCLI(t, fs.URL, "backup", "schedule", "add", "--name", "x", "--cron", "bad"); err == nil {
		t.Error("a refused rule was reported as created")
	}
	bad := newFakeServer(t, map[string]interface{}{"POST /v1/automation": "not json"})
	if _, err := runCLI(t, bad.URL, "backup", "schedule", "add", "--name", "x", "--cron", "0 3 * * *"); err == nil {
		t.Error("an unreadable answer was accepted")
	}
}

func TestBackupScheduleList(t *testing.T) {
	fs := newFakeServer(t, map[string]interface{}{
		"GET /v1/automation": map[string]interface{}{"rules": []map[string]interface{}{
			{"id": "r1", "name": "nightly", "schedule": "0 3 * * *", "enabled": true, "lastRun": 1793336400,
				"backup": map[string]interface{}{"target": "s3", "bucket": "b", "prefix": "prod/", "keep": 7}},
			{"id": "w1", "name": "digest", "schedule": "0 9 * * *", "webhookId": "wh"},
		}},
	})
	out, err := runCLI(t, fs.URL, "backup", "schedule", "list")
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, out, "s3://b/prod/")
	mustContain(t, out, "nightly")
	if strings.Contains(out, "digest") {
		t.Error("a webhook cron was listed as a backup schedule")
	}
	if got := fs.lastCall(t).Path; got != "/v1/automation" {
		t.Errorf("listed from %s", got)
	}

	out, err = runCLI(t, fs.URL, "--json", "backup", "schedule", "list")
	if err != nil || !strings.HasPrefix(out, `[{`) || strings.Contains(out, "digest") {
		t.Errorf("json list: %q %v", out, err)
	}

	empty := newFakeServer(t, map[string]interface{}{"GET /v1/automation": `{"rules":[]}`})
	if out, _ := runCLI(t, empty.URL, "backup", "schedule", "list"); !strings.Contains(out, "No scheduled backups") {
		t.Errorf("empty list: %q", out)
	}
	bad := newFakeServer(t, map[string]interface{}{"GET /v1/automation": "not json"})
	if _, err := runCLI(t, bad.URL, "backup", "schedule", "list"); err == nil {
		t.Error("an unreadable list was accepted")
	}
	down := newFakeServer(t, map[string]interface{}{})
	if _, err := runCLI(t, down.URL, "backup", "schedule", "list"); err == nil {
		t.Error("a failed list was accepted")
	}
}

func TestBackupScheduleRemove(t *testing.T) {
	fs := newFakeServer(t, map[string]interface{}{
		"GET /v1/automation/r1":    map[string]interface{}{"id": "r1", "type": "cron", "backup": map[string]interface{}{}},
		"DELETE /v1/automation/r1": map[string]interface{}{"status": "deleted", "id": "r1"},
		"GET /v1/automation/w1":    map[string]interface{}{"id": "w1", "type": "webhook"},
		"GET /v1/automation/bad":   "not json",
		"GET /v1/automation/gone":  map[string]interface{}{"id": "gone", "type": "cron", "backup": map[string]interface{}{}},
	})
	out, err := runCLI(t, fs.URL, "backup", "schedule", "remove", "r1")
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, out, "Backup schedule removed: r1")
	if c := fs.lastCall(t); c.Method != http.MethodDelete || c.Path != "/v1/automation/r1" {
		t.Errorf("last call %s %s", c.Method, c.Path)
	}
	if out, err = runCLI(t, fs.URL, "--json", "backup", "schedule", "remove", "r1"); err != nil || !strings.Contains(out, "deleted") {
		t.Errorf("json remove: %q %v", out, err)
	}

	n := len(fs.calls())
	if _, err := runCLI(t, fs.URL, "backup", "schedule", "remove", "w1"); err == nil || !strings.Contains(err.Error(), "not a scheduled backup") {
		t.Errorf("removing a webhook: %v", err)
	}
	for _, c := range fs.calls()[n:] {
		if c.Method == http.MethodDelete {
			t.Error("a webhook rule was deleted")
		}
	}
	for _, id := range []string{"bad", "gone", "absent"} {
		if _, err := runCLI(t, fs.URL, "backup", "schedule", "remove", id); err == nil {
			t.Errorf("remove %s succeeded", id)
		}
	}
}

// `backup <file>` still takes a backup; only "schedule" is the subcommand.
func TestBackupWithAFileNameIsStillABackup(t *testing.T) {
	fs := newFakeServer(t, map[string]interface{}{"/v1/backup": map[string]string{"backup": "backups/x.db"}})
	out, err := runCLI(t, fs.URL, "backup", "x.db")
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, out, "Backup created")
}
