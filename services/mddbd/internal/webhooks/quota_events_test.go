package webhooks

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"mddb/internal/binlog"
)

// GO-043: quota.exceeded is a collection event, and the manager's failure
// paths (closed database, binlog mirroring, unreachable receiver).

func TestFireCollectionEventReachesCollectionHooks(t *testing.T) {
	wm, cleanup := newTestWebhookManager(t)
	defer cleanup()
	got := make(chan WebhookPayload, 2)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var p WebhookPayload
		_ = json.NewDecoder(r.Body).Decode(&p)
		got <- p
	}))
	defer srv.Close()
	if _, err := wm.Register(srv.URL, []string{EventQuotaExceeded}, "acme/notes"); err != nil {
		t.Fatalf("quota.exceeded refused as an event: %v", err)
	}
	if _, err := wm.Register(srv.URL, []string{EventQuotaExceeded}, "other"); err != nil {
		t.Fatal(err)
	}
	wm.FireCollectionEvent(EventQuotaExceeded, "acme/notes", map[string]interface{}{"resource": "documents"})
	select {
	case p := <-got:
		if p.Event != EventQuotaExceeded || p.Collection != "acme/notes" || p.Detail["resource"] != "documents" {
			t.Fatalf("payload = %+v", p)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no delivery")
	}
	select {
	case p := <-got:
		t.Fatalf("a hook for another collection received %+v", p)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestManagerFailsCleanlyOnClosedDatabase(t *testing.T) {
	wm, cleanup := newTestWebhookManager(t)
	cleanup() // closes the database
	if err := wm.Reload(wm.db); err == nil {
		t.Error("Reload on a closed database succeeded")
	}
	if err := wm.LoadAll(); err == nil {
		t.Error("LoadAll on a closed database succeeded")
	}
	if _, err := wm.Register("http://example.invalid", []string{"doc.added"}, ""); err == nil {
		t.Error("Register on a closed database succeeded")
	}
	if err := wm.Delete("x"); err == nil {
		t.Error("Delete on a closed database succeeded")
	}
}

func TestManagerMirrorsChangesToBinlog(t *testing.T) {
	wm, cleanup := newTestWebhookManager(t)
	defer cleanup()
	bl, err := binlog.NewBinlog("", binlog.BinlogConfig{Path: filepath.Join(t.TempDir(), "wh.binlog")})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = bl.Close() }()
	wm.SetBinlog(bl)
	before := bl.Stats().CurrentLSN
	hook, err := wm.Register("http://example.invalid", []string{"doc.added"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := wm.Delete(hook.ID); err != nil {
		t.Fatal(err)
	}
	if got := bl.Stats().CurrentLSN - before; got != 2 {
		t.Fatalf("binlog advanced by %d entries, want 2 (put + delete)", got)
	}
}

func TestFireWebhookGivesUpOnUnreachableReceiver(t *testing.T) {
	restore := retryBackoffs
	retryBackoffs = []time.Duration{0, time.Millisecond}
	defer func() { retryBackoffs = restore }()
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := srv.URL
	srv.Close()
	done := make(chan struct{})
	go func() {
		fireWebhook(Webhook{ID: "gone", URL: url}, WebhookPayload{Event: "doc.added"})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("fireWebhook did not give up")
	}
}
