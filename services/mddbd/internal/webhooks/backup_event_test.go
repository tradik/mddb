package webhooks

import (
	"testing"
	"time"
)

// backup.failed (OPS-023) is an event a webhook can subscribe to, and an
// incident fired with it reaches only its subscribers.
func TestBackupFailedIsASubscribableEvent(t *testing.T) {
	wm, cleanup := newTestWebhookManager(t)
	defer cleanup()
	wh, err := wm.Register("http://127.0.0.1:1/hook", []string{EventBackupFailed}, "")
	if err != nil {
		t.Fatalf("backup.failed refused: %v", err)
	}
	if !hookMatches(*wh, EventBackupFailed, "") || hookMatches(*wh, EventDiskUsageHigh, "") {
		t.Error("the subscription matches the wrong events")
	}
}

// A webhook whose endpoint cannot be reached is retried and then given up.
func TestFireWebhookGivesUpOnAnUnreachableEndpoint(t *testing.T) {
	restore := retryBackoffs
	retryBackoffs = []time.Duration{0, time.Millisecond}
	defer func() { retryBackoffs = restore }()
	done := make(chan struct{})
	go func() {
		fireWebhook(Webhook{ID: "x", URL: "http://127.0.0.1:1/unreachable"}, WebhookPayload{Event: EventBackupFailed})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("fireWebhook did not give up")
	}
}

// A closed database is reported by every call that touches it.
func TestWebhookManagerOnAClosedDatabase(t *testing.T) {
	wm, cleanup := newTestWebhookManager(t)
	db := wm.db
	cleanup()
	if err := wm.Reload(db); err == nil {
		t.Error("reloading onto a closed database succeeded")
	}
	if err := wm.LoadAll(); err == nil {
		t.Error("loading from a closed database succeeded")
	}
	if _, err := wm.Register("http://127.0.0.1:1/hook", []string{"doc.added"}, ""); err == nil {
		t.Error("registering into a closed database succeeded")
	}
	if err := wm.Delete("x"); err == nil {
		t.Error("deleting from a closed database succeeded")
	}
}
