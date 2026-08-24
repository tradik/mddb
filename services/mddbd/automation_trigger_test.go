package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"mddb/internal/storage"
)

// TEST-002. Automation triggers fire webhooks at third parties when a document
// matches. Two things must hold and neither is visible from a status code: what
// actually goes out on the wire, and that a webhook nobody can reach does not
// take the write path down with it.

// captureWebhook stands in for the third party, recording what it received.
type captureWebhook struct {
	mu       sync.Mutex
	requests []capturedRequest
	status   int
	server   *httptest.Server
}

type capturedRequest struct {
	method  string
	path    string
	headers http.Header
	body    map[string]any
}

func newCaptureWebhook(t *testing.T) *captureWebhook {
	t.Helper()
	c := &captureWebhook{status: http.StatusOK}
	c.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)

		c.mu.Lock()
		c.requests = append(c.requests, capturedRequest{
			method: r.Method, path: r.URL.Path,
			headers: r.Header.Clone(), body: body,
		})
		status := c.status
		c.mu.Unlock()

		w.WriteHeader(status)
	}))
	t.Cleanup(c.server.Close)
	return c
}

func (c *captureWebhook) received() []capturedRequest {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]capturedRequest(nil), c.requests...)
}

func (c *captureWebhook) setStatus(code int) {
	c.mu.Lock()
	c.status = code
	c.mu.Unlock()
}

// waitForRequests gives the webhook goroutine a bounded moment to arrive.
// Webhooks fire asynchronously, so asserting immediately tests the scheduler
// rather than the behaviour.
func (c *captureWebhook) waitForRequests(t *testing.T, want int) []capturedRequest {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if got := c.received(); len(got) >= want {
			return got
		}
		time.Sleep(10 * time.Millisecond)
	}
	return c.received()
}

func TestCronWebhookSendsTheCronIdentity(t *testing.T) {
	hook := newCaptureWebhook(t)

	fireCronWebhook(&AutomationRule{
		ID: "wh-1", Type: "webhook", URL: hook.server.URL + "/hook",
		Headers: map[string]string{"X-Token": "secret-value"},
	}, "cron-7", "nightly-reindex", nil)

	got := hook.waitForRequests(t, 1)
	if len(got) == 0 {
		t.Fatal("the webhook was never called")
	}
	req := got[0]

	if req.method != "POST" {
		t.Errorf("method = %q, want POST by default", req.method)
	}
	if req.headers.Get("X-Token") != "secret-value" {
		t.Errorf("configured header was not sent: %v", req.headers)
	}
	if req.body["event"] != "cron.fired" {
		t.Errorf("event = %v, want cron.fired", req.body["event"])
	}

	cron, ok := req.body["cron"].(map[string]any)
	if !ok {
		t.Fatalf("payload carries no cron block: %v", req.body)
	}
	if cron["id"] != "cron-7" || cron["name"] != "nightly-reindex" {
		t.Errorf("the cron identity is wrong: %v", cron)
	}
}

// A template in the URL is how a caller routes one webhook to several
// destinations; leaving it unexpanded silently posts to a literal "{{...}}".
func TestCronWebhookExpandsTemplates(t *testing.T) {
	hook := newCaptureWebhook(t)

	fireCronWebhook(&AutomationRule{
		ID: "wh-1", URL: hook.server.URL + "/hook/{{cron.id}}",
		Headers: map[string]string{"X-Cron-Name": "{{cron.name}}"},
	}, "cron-7", "nightly", nil)

	got := hook.waitForRequests(t, 1)
	if len(got) == 0 {
		t.Fatal("the webhook was never called")
	}
	if got[0].path != "/hook/cron-7" {
		t.Errorf("URL template was not expanded: %q", got[0].path)
	}
	if h := got[0].headers.Get("X-Cron-Name"); h != "nightly" {
		t.Errorf("header template was not expanded: %q", h)
	}
}

// A third party that is down must not be a data-loss event on our side.
func TestCronWebhookRetriesThenGivesUp(t *testing.T) {
	hook := newCaptureWebhook(t)
	hook.setStatus(http.StatusInternalServerError)

	done := make(chan struct{})
	go func() {
		fireCronWebhook(&AutomationRule{ID: "wh-1", URL: hook.server.URL}, "c", "n", nil)
		close(done)
	}()

	// Backoff is 0+1+5+15s with a 10s timeout per attempt, so the worst case
	// is around a minute. The assertion is that it *terminates*, not that it
	// is quick.
	select {
	case <-done:
	case <-time.After(90 * time.Second):
		t.Fatal("a failing webhook never gave up — it would hold the caller forever")
	}

	if n := len(hook.received()); n < 2 {
		t.Errorf("a failing webhook was attempted %d times; a retry is the point of the backoff", n)
	}
}

// An unreachable host is the ordinary case for a stale webhook someone
// configured months ago and forgot.
func TestCronWebhookSurvivesAnUnreachableHost(t *testing.T) {
	done := make(chan struct{})
	go func() {
		// Port 0 is never listening.
		fireCronWebhook(&AutomationRule{ID: "wh-1", URL: "http://127.0.0.1:0/hook"}, "c", "n", nil)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(90 * time.Second):
		t.Fatal("an unreachable webhook hung instead of failing")
	}
}

func TestCronWebhookHonoursTheConfiguredMethod(t *testing.T) {
	hook := newCaptureWebhook(t)

	fireCronWebhook(&AutomationRule{ID: "wh-1", URL: hook.server.URL, Method: "PUT"}, "c", "n", nil)

	got := hook.waitForRequests(t, 1)
	if len(got) == 0 {
		t.Fatal("the webhook was never called")
	}
	if got[0].method != "PUT" {
		t.Errorf("method = %q, want the configured PUT", got[0].method)
	}
}

// --- trigger evaluation ---

func TestEvaluateTriggersIgnoresDisabledAndForeignRules(t *testing.T) {
	srv, cleanup := newTestServerForAutomation(t)
	defer cleanup()

	hook := newCaptureWebhook(t)
	am := srv.AutomationManager

	// Create assigns its own id and refuses a trigger whose webhook does not
	// exist, so the webhook has to come first and be referenced by what it
	// hands back.
	wh, err := am.Create(AutomationRule{
		Name: "capture", Type: "webhook", Enabled: true, URL: hook.server.URL,
	})
	if err != nil {
		t.Fatal(err)
	}

	// A rule that would match, but is switched off; and one for another
	// collection. Neither may fire.
	for _, rule := range []AutomationRule{
		{Name: "disabled", Type: "trigger", Enabled: false, Collection: "docs",
			SearchType: "fts", Query: "match", Threshold: 0, WebhookID: wh.ID},
		{Name: "other-collection", Type: "trigger", Enabled: true, Collection: "elsewhere",
			SearchType: "fts", Query: "match", Threshold: 0, WebhookID: wh.ID},
	} {
		if _, err := am.Create(rule); err != nil {
			t.Fatal(err)
		}
	}

	am.EvaluateTriggers("docs", storage.Doc{
		ID: "docs|d1|en", Key: "d1", Lang: "en", ContentMD: "a document that would match",
	}, "document.added")

	time.Sleep(300 * time.Millisecond)
	if got := hook.received(); len(got) != 0 {
		t.Errorf("a disabled or foreign trigger fired: %+v", got)
	}
}

// A trigger pointing at a webhook that no longer exists must not panic the
// write path — rules are edited independently and this happens.
func TestEvaluateTriggersSurvivesAMissingWebhook(t *testing.T) {
	srv, cleanup := newTestServerForAutomation(t)
	defer cleanup()

	am := srv.AutomationManager

	// Create refuses this, which is correct — but a webhook deleted after the
	// trigger was made leaves exactly this state, and the write path must
	// survive meeting it.
	if _, err := am.Create(AutomationRule{
		Name: "orphan", Type: "trigger", Enabled: true, Collection: "docs",
		SearchType: "fts", Query: "anything", Threshold: 0, WebhookID: "does-not-exist",
	}); err == nil {
		t.Error("Create accepted a trigger pointing at a webhook that does not exist")
	}

	// Inject the orphaned state directly, since that is how it arises.
	am.mu.Lock()
	am.rules = append(am.rules, AutomationRule{
		ID: "t-orphan", Name: "orphan", Type: "trigger", Enabled: true, Collection: "docs",
		SearchType: "fts", Query: "anything", Threshold: 0, WebhookID: "does-not-exist",
	})
	am.mu.Unlock()

	// Must return rather than panic.
	am.EvaluateTriggers("docs", storage.Doc{
		ID: "docs|d1|en", Key: "d1", Lang: "en", ContentMD: "anything at all",
	}, "document.added")
}

func TestEvaluateTriggersOnAnEmptyRuleSet(t *testing.T) {
	srv, cleanup := newTestServerForAutomation(t)
	defer cleanup()

	srv.AutomationManager.EvaluateTriggers("docs", storage.Doc{ID: "d", Key: "d", Lang: "en"}, "document.added")
}

// An unknown search type is a configuration mistake; it must be inert rather
// than matching everything or crashing.
func TestTriggerWithAnUnknownSearchTypeDoesNotFire(t *testing.T) {
	srv, cleanup := newTestServerForAutomation(t)
	defer cleanup()

	hook := newCaptureWebhook(t)
	am := srv.AutomationManager

	wh, err := am.Create(AutomationRule{
		Name: "capture", Type: "webhook", Enabled: true, URL: hook.server.URL,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Create refuses an unknown search type — good. Injected directly, because
	// a rule stored by an older version, or edited outside the API, arrives
	// this way and evaluation must stay inert rather than match everything.
	if _, err := am.Create(AutomationRule{
		Name: "typo", Type: "trigger", Enabled: true, Collection: "docs",
		SearchType: "telepathy", Query: "x", WebhookID: wh.ID,
	}); err == nil {
		t.Error("Create accepted an unknown search type")
	}

	am.mu.Lock()
	am.rules = append(am.rules, AutomationRule{
		ID: "t-typo", Name: "typo", Type: "trigger", Enabled: true, Collection: "docs",
		SearchType: "telepathy", Query: "x", Threshold: 0, WebhookID: wh.ID,
	})
	am.mu.Unlock()

	am.EvaluateTriggers("docs", storage.Doc{
		ID: "docs|d1|en", Key: "d1", Lang: "en", ContentMD: "x",
	}, "document.added")

	time.Sleep(300 * time.Millisecond)
	if got := hook.received(); len(got) != 0 {
		t.Errorf("a trigger with an unknown search type fired: %+v", got)
	}
}

func TestRunTriggerRejectsAnUnknownSearchType(t *testing.T) {
	srv, cleanup := newTestServerForAutomation(t)
	defer cleanup()

	srv.AutomationManager.server = srv
	_, err := srv.AutomationManager.RunTrigger(&AutomationRule{
		ID: "t", Name: "typo", Type: "trigger", Collection: "docs", SearchType: "telepathy", Query: "x",
	})
	if err == nil {
		t.Error("an unknown search type was accepted")
	}
	if err != nil && !strings.Contains(err.Error(), "telepathy") {
		t.Errorf("the error does not name the offending type: %v", err)
	}
}
