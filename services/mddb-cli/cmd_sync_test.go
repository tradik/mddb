package main

import (
	"net/http"
	"strings"
	"testing"
)

// #377: `sync` is a thin layer over /v1/automation: the rule create sends,
// what list shows, run's call and that delete touches synced sources only.

func TestSyncCreateSitemap(t *testing.T) {
	fs := newFakeServer(t, map[string]interface{}{
		"POST /v1/automation": map[string]interface{}{"id": "s1", "schedule": "0 * * * *",
			"sync": map[string]interface{}{"source": "sitemap", "url": "https://d.example/sitemap.xml", "collection": "docs"}},
	})
	out, err := runCLI(t, fs.URL, "sync", "create", "--name", "docs", "--cron", "0 * * * *", "--source", "sitemap",
		"--url", "https://d.example/sitemap.xml", "--collection", "docs", "--prune", "--max-documents", "50")
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, out, "Synced source created: s1")
	mustContain(t, out, "https://d.example/sitemap.xml → docs")
	body := fs.lastCall(t).Body
	assertBodyField(t, body, "type", "cron")
	assertBodyField(t, body, "enabled", true)
	assertBodyField(t, body, "sync", map[string]interface{}{"source": "sitemap", "collection": "docs", "lang": "en",
		"prune": true, "maxDocuments": 50, "maxBytes": 0, "url": "https://d.example/sitemap.xml"})
}

func TestSyncCreateS3AndURLs(t *testing.T) {
	t.Setenv(syncSecretKeyEnv, "from-env")
	fs := newFakeServer(t, map[string]interface{}{"POST /v1/automation": `{"id":"s2"}`})
	if _, err := runCLI(t, fs.URL, "sync", "create", "--name", "kb", "--cron", "0 * * * *", "--source", "s3",
		"--collection", "kb", "--endpoint", "s3.example.com", "--bucket", "b", "--prefix", "p/", "--access-key", "AK",
		"--tls", "--disabled"); err != nil {
		t.Fatal(err)
	}
	body := fs.lastCall(t).Body
	assertBodyField(t, body, "enabled", false)
	sync, _ := body["sync"].(map[string]interface{})
	assertBodyField(t, sync, "storageConfig", map[string]interface{}{"endpoint": "s3.example.com",
		"region": "", "accessKey": "AK", "secretKey": "from-env", "useTLS": true})

	out, err := runCLI(t, fs.URL, "--json", "sync", "create", "--name", "u", "--cron", "0 * * * *", "--source", "urls",
		"--collection", "c", "--urls", "https://a.example/1,https://a.example/2")
	if err != nil || !strings.Contains(out, `{"id":"s2"}`) {
		t.Fatalf("json create: %q %v", out, err)
	}
	sync, _ = fs.lastCall(t).Body["sync"].(map[string]interface{})
	assertBodyField(t, sync, "urls", []string{"https://a.example/1", "https://a.example/2"})
}

func TestSyncCreateFailures(t *testing.T) {
	fs := newFakeServer(t, map[string]interface{}{
		"POST /v1/automation": failure{http.StatusBadRequest, `{"error":"invalid sync source"}`},
	})
	if _, err := runCLI(t, fs.URL, "sync", "create", "--name", "x", "--cron", "0 * * * *"); err == nil ||
		!strings.Contains(err.Error(), "required") {
		t.Errorf("missing --source: %v", err)
	}
	if _, err := runCLI(t, fs.URL, "sync", "create", "--name", "x", "--cron", "0 * * * *", "--source", "ftp", "--collection", "c"); err == nil {
		t.Error("a refused rule was reported as created")
	}
	bad := newFakeServer(t, map[string]interface{}{"POST /v1/automation": "not json"})
	if _, err := runCLI(t, bad.URL, "sync", "create", "--name", "x", "--cron", "0 * * * *", "--source", "sitemap", "--collection", "c"); err == nil {
		t.Error("an unreadable answer was accepted")
	}
}

func TestSyncList(t *testing.T) {
	rules := map[string]interface{}{"rules": []map[string]interface{}{
		{"id": "s1", "name": "docs", "schedule": "0 * * * *", "lastRun": 1793336400,
			"sync":       map[string]interface{}{"source": "sitemap", "url": "https://d.example/sitemap.xml", "collection": "docs"},
			"lastResult": map[string]interface{}{"status": "error", "added": 2, "failed": 1, "error": "1 of 3 sources failed"}},
		{"id": "s2", "name": "pages", "schedule": "0 * * * *",
			"sync": map[string]interface{}{"source": "urls", "urls": []string{"a", "b"}, "collection": "docs"}},
		{"id": "s3", "name": "kb", "schedule": "0 * * * *",
			"sync": map[string]interface{}{"source": "s3", "bucket": "b", "prefix": "p/", "collection": "docs"}},
		{"id": "b1", "name": "nightly", "schedule": "0 3 * * *", "backup": map[string]interface{}{}},
	}}
	fs := newFakeServer(t, map[string]interface{}{"GET /v1/automation": rules})
	out, err := runCLI(t, fs.URL, "sync", "list", "--collection", "docs")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"https://d.example/sitemap.xml", "2 urls", "s3://b/p/", "error +2 ~0 =0 -0 !1: 1 of 3 sources failed"} {
		mustContain(t, out, want)
	}
	if strings.Contains(out, "nightly") {
		t.Error("a backup was listed as a synced source")
	}
	out, err = runCLI(t, fs.URL, "--json", "sync", "list")
	if err != nil || !strings.HasPrefix(out, `[{`) || strings.Contains(out, "nightly") {
		t.Errorf("json list: %q %v", out, err)
	}
	empty := newFakeServer(t, map[string]interface{}{"GET /v1/automation": `{"rules":[]}`})
	if out, _ := runCLI(t, empty.URL, "sync", "list"); !strings.Contains(out, "No synced sources") {
		t.Errorf("empty list: %q", out)
	}
	bad := newFakeServer(t, map[string]interface{}{"GET /v1/automation": "not json"})
	if _, err := runCLI(t, bad.URL, "sync", "list"); err == nil {
		t.Error("an unreadable list was accepted")
	}
	down := newFakeServer(t, map[string]interface{}{})
	if _, err := runCLI(t, down.URL, "sync", "list"); err == nil {
		t.Error("a failed list was accepted")
	}
}

func TestSyncRun(t *testing.T) {
	fs := newFakeServer(t, map[string]interface{}{
		"POST /v1/automation/s1/run":   map[string]interface{}{"ruleId": "s1", "jobId": "sync-s1-20261030T120000Z", "status": "started"},
		"POST /v1/automation/busy/run": failure{http.StatusConflict, `{"error":"the previous run of this rule is still going"}`},
		"POST /v1/automation/bad/run":  "not json",
	})
	out, err := runCLI(t, fs.URL, "sync", "run", "s1")
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, out, "Run started: s1 (job sync-s1-20261030T120000Z)")
	if out, err = runCLI(t, fs.URL, "--json", "sync", "run", "s1"); err != nil || !strings.Contains(out, `"jobId"`) {
		t.Errorf("json run: %q %v", out, err)
	}
	if _, err := runCLI(t, fs.URL, "sync", "run", "busy"); err == nil {
		t.Error("a refused run was reported as started")
	}
	if _, err := runCLI(t, fs.URL, "sync", "run", "bad"); err == nil {
		t.Error("an unreadable answer was accepted")
	}
}

func TestSyncDelete(t *testing.T) {
	fs := newFakeServer(t, map[string]interface{}{
		"GET /v1/automation/s1":    map[string]interface{}{"id": "s1", "type": "cron", "sync": map[string]interface{}{}},
		"DELETE /v1/automation/s1": map[string]interface{}{"status": "deleted", "id": "s1"},
		"GET /v1/automation/b1":    map[string]interface{}{"id": "b1", "type": "cron", "backup": map[string]interface{}{}},
	})
	out, err := runCLI(t, fs.URL, "sync", "delete", "s1")
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, out, "Synced source deleted: s1")
	if c := fs.lastCall(t); c.Method != http.MethodDelete || c.Path != "/v1/automation/s1" {
		t.Errorf("last call %s %s", c.Method, c.Path)
	}
	if _, err := runCLI(t, fs.URL, "sync", "delete", "b1"); err == nil || !strings.Contains(err.Error(), "not a synced source") {
		t.Errorf("deleting a backup: %v", err)
	}
}
