package webhooks

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// Test: fireWebhook retry behavior
// ---------------------------------------------------------------------------

func TestFireWebhook_RetryOnFailure(t *testing.T) {
	// The real schedule is 0/1s/5s/15s, so this test slept for six seconds to
	// observe three attempts. What it is asserting is that failures are
	// retried, not how long the gaps are — and six seconds of that, on every
	// run, is most of this package's test time.
	restore := retryBackoffs
	retryBackoffs = []time.Duration{0, time.Millisecond, 2 * time.Millisecond, 3 * time.Millisecond}
	defer func() { retryBackoffs = restore }()

	attempts := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts < 3 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	hook := Webhook{
		ID:     "retry-test",
		URL:    ts.URL,
		Events: []string{"doc.added"},
	}
	payload := WebhookPayload{
		Event:      "doc.added",
		Collection: "blog",
		Key:        "test",
		Lang:       "en",
	}

	// fireWebhook is synchronous - call it directly
	fireWebhook(hook, payload)

	if attempts < 3 {
		t.Errorf("expected at least 3 attempts (retries), got %d", attempts)
	}
}

// ---------------------------------------------------------------------------
// Test: hookMatches edge cases
// ---------------------------------------------------------------------------

func TestHookMatches_MultipleEvents(t *testing.T) {
	hook := Webhook{
		Events:     []string{"doc.added", "doc.updated", "doc.deleted"},
		Collection: "",
	}

	for _, event := range []string{"doc.added", "doc.updated", "doc.deleted"} {
		if !hookMatches(hook, event, "any") {
			t.Errorf("expected match for event %s", event)
		}
	}
}

func TestHookMatches_CollectionAndEventMismatch(t *testing.T) {
	hook := Webhook{
		Events:     []string{"doc.added"},
		Collection: "blog",
	}

	// Wrong event, right collection
	if hookMatches(hook, "doc.deleted", "blog") {
		t.Error("expected no match for wrong event")
	}

	// Right event, wrong collection
	if hookMatches(hook, "doc.added", "pages") {
		t.Error("expected no match for wrong collection")
	}
}
