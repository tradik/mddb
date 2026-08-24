package webhooks

import (
	"encoding/json"
	"io"
	"mddb/internal/storage"
	"mddb/internal/testsync"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"sync/atomic"
	"testing"

	bolt "go.etcd.io/bbolt"
)

func newTestWebhookManager(t *testing.T) (*WebhookManager, func()) {
	t.Helper()
	f, err := os.CreateTemp("", "webhook_test_*.db")
	if err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	db, err := bolt.Open(f.Name(), 0600, nil)
	if err != nil {
		_ = os.Remove(f.Name())
		t.Fatal(err)
	}
	wm := NewWebhookManager(db)
	if err := wm.EnsureBucket(); err != nil {
		_ = db.Close()
		_ = os.Remove(f.Name())
		t.Fatal(err)
	}
	cleanup := func() {
		_ = db.Close()
		_ = os.Remove(f.Name())
	}
	return wm, cleanup
}

func TestNewWebhookManager(t *testing.T) {
	wm, cleanup := newTestWebhookManager(t)
	defer cleanup()

	if wm.db == nil {
		t.Error("expected non-nil db")
	}
}

func TestWebhookEnsureBucket(t *testing.T) {
	f, err := os.CreateTemp("", "wh_bucket_*.db")
	if err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	defer func() { _ = os.Remove(f.Name()) }()

	db, err := bolt.Open(f.Name(), 0600, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	wm := NewWebhookManager(db)

	if err := wm.EnsureBucket(); err != nil {
		t.Fatalf("EnsureBucket failed: %v", err)
	}

	// Idempotent
	if err := wm.EnsureBucket(); err != nil {
		t.Fatalf("second EnsureBucket failed: %v", err)
	}

	err = db.View(func(tx *bolt.Tx) error {
		if tx.Bucket(bucketWebhooks) == nil {
			t.Error("webhooks bucket not created")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestWebhookRegister(t *testing.T) {
	wm, cleanup := newTestWebhookManager(t)
	defer cleanup()

	wh, err := wm.Register("http://example.com/hook", []string{"doc.added"}, "blog")
	if err != nil {
		t.Fatalf("Register failed: %v", err)
	}

	if wh.ID == "" {
		t.Error("expected non-empty webhook ID")
	}
	if wh.URL != "http://example.com/hook" {
		t.Errorf("expected URL http://example.com/hook, got %q", wh.URL)
	}
	if len(wh.Events) != 1 || wh.Events[0] != "doc.added" {
		t.Error("expected events [doc.added]")
	}
	if wh.Collection != "blog" {
		t.Errorf("expected collection 'blog', got %q", wh.Collection)
	}
	if wh.CreatedAt == 0 {
		t.Error("expected non-zero CreatedAt")
	}
}

func TestWebhookRegisterEmptyURL(t *testing.T) {
	wm, cleanup := newTestWebhookManager(t)
	defer cleanup()

	_, err := wm.Register("", []string{"doc.added"}, "")
	if err == nil {
		t.Error("expected error for empty URL")
	}
}

func TestWebhookRegisterNoEvents(t *testing.T) {
	wm, cleanup := newTestWebhookManager(t)
	defer cleanup()

	_, err := wm.Register("http://example.com/hook", nil, "")
	if err == nil {
		t.Error("expected error for nil events")
	}

	_, err = wm.Register("http://example.com/hook", []string{}, "")
	if err == nil {
		t.Error("expected error for empty events")
	}
}

func TestWebhookRegisterInvalidEvent(t *testing.T) {
	wm, cleanup := newTestWebhookManager(t)
	defer cleanup()

	_, err := wm.Register("http://example.com/hook", []string{"invalid.event"}, "")
	if err == nil {
		t.Error("expected error for invalid event type")
	}
}

func TestWebhookRegisterAllValidEvents(t *testing.T) {
	wm, cleanup := newTestWebhookManager(t)
	defer cleanup()

	wh, err := wm.Register("http://example.com/hook", []string{"doc.added", "doc.updated", "doc.deleted"}, "")
	if err != nil {
		t.Fatalf("Register failed: %v", err)
	}
	if len(wh.Events) != 3 {
		t.Errorf("expected 3 events, got %d", len(wh.Events))
	}
}

func TestWebhookRegisterNoCollection(t *testing.T) {
	wm, cleanup := newTestWebhookManager(t)
	defer cleanup()

	wh, err := wm.Register("http://example.com/hook", []string{"doc.added"}, "")
	if err != nil {
		t.Fatalf("Register failed: %v", err)
	}
	if wh.Collection != "" {
		t.Errorf("expected empty collection, got %q", wh.Collection)
	}
}

func TestWebhookList(t *testing.T) {
	wm, cleanup := newTestWebhookManager(t)
	defer cleanup()

	// Empty list initially
	hooks := wm.List()
	if len(hooks) != 0 {
		t.Errorf("expected empty list, got %d", len(hooks))
	}

	// Register some webhooks
	_, _ = wm.Register("http://example.com/hook1", []string{"doc.added"}, "")
	_, _ = wm.Register("http://example.com/hook2", []string{"doc.updated"}, "blog")

	hooks = wm.List()
	if len(hooks) != 2 {
		t.Errorf("expected 2 webhooks, got %d", len(hooks))
	}
}

func TestWebhookListReturnsCopy(t *testing.T) {
	wm, cleanup := newTestWebhookManager(t)
	defer cleanup()

	_, _ = wm.Register("http://example.com/hook", []string{"doc.added"}, "")

	hooks := wm.List()
	hooks[0].URL = "modified"

	// Original should not be affected
	original := wm.List()
	if original[0].URL == "modified" {
		t.Error("List should return a copy, not the original slice")
	}
}

func TestWebhookDelete(t *testing.T) {
	wm, cleanup := newTestWebhookManager(t)
	defer cleanup()

	wh, _ := wm.Register("http://example.com/hook", []string{"doc.added"}, "")

	if err := wm.Delete(wh.ID); err != nil {
		t.Fatalf("Delete failed: %v", err)
	}

	hooks := wm.List()
	if len(hooks) != 0 {
		t.Errorf("expected 0 webhooks after delete, got %d", len(hooks))
	}
}

func TestWebhookDeleteNonexistent(t *testing.T) {
	wm, cleanup := newTestWebhookManager(t)
	defer cleanup()

	// Deleting a non-existent webhook should not error
	if err := wm.Delete("nonexistent"); err != nil {
		t.Fatalf("Delete of nonexistent webhook failed: %v", err)
	}
}

func TestWebhookDeletePreservesOthers(t *testing.T) {
	wm, cleanup := newTestWebhookManager(t)
	defer cleanup()

	wh1, _ := wm.Register("http://example.com/hook1", []string{"doc.added"}, "")
	_, _ = wm.Register("http://example.com/hook2", []string{"doc.updated"}, "")

	if err := wm.Delete(wh1.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	hooks := wm.List()
	if len(hooks) != 1 {
		t.Errorf("expected 1 webhook remaining, got %d", len(hooks))
	}
	if hooks[0].URL != "http://example.com/hook2" {
		t.Errorf("expected hook2 to remain, got %q", hooks[0].URL)
	}
}

func TestWebhookLoadAll(t *testing.T) {
	f, err := os.CreateTemp("", "wh_load_*.db")
	if err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	defer func() { _ = os.Remove(f.Name()) }()

	db, err := bolt.Open(f.Name(), 0600, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	wm1 := NewWebhookManager(db)
	if err := wm1.EnsureBucket(); err != nil {
		t.Fatalf("EnsureBucket: %v", err)
	}

	_, _ = wm1.Register("http://example.com/hook1", []string{"doc.added"}, "")
	_, _ = wm1.Register("http://example.com/hook2", []string{"doc.deleted"}, "blog")

	// Create a new manager and load from DB
	wm2 := NewWebhookManager(db)
	if err := wm2.LoadAll(); err != nil {
		t.Fatalf("LoadAll failed: %v", err)
	}

	hooks := wm2.List()
	if len(hooks) != 2 {
		t.Errorf("expected 2 webhooks loaded, got %d", len(hooks))
	}
}

func TestWebhookLoadAllEmptyDB(t *testing.T) {
	wm, cleanup := newTestWebhookManager(t)
	defer cleanup()

	if err := wm.LoadAll(); err != nil {
		t.Fatalf("LoadAll on empty DB failed: %v", err)
	}

	hooks := wm.List()
	if len(hooks) != 0 {
		t.Errorf("expected 0 webhooks, got %d", len(hooks))
	}
}

func TestHookMatchesEventMatch(t *testing.T) {
	hook := Webhook{
		Events:     []string{"doc.added", "doc.deleted"},
		Collection: "",
	}

	if !hookMatches(hook, "doc.added", "any") {
		t.Error("expected hook to match doc.added event")
	}
	if !hookMatches(hook, "doc.deleted", "any") {
		t.Error("expected hook to match doc.deleted event")
	}
	if hookMatches(hook, "doc.updated", "any") {
		t.Error("expected hook NOT to match doc.updated event")
	}
}

func TestHookMatchesCollectionFilter(t *testing.T) {
	hook := Webhook{
		Events:     []string{"doc.added"},
		Collection: "blog",
	}

	if !hookMatches(hook, "doc.added", "blog") {
		t.Error("expected hook to match collection 'blog'")
	}
	if hookMatches(hook, "doc.added", "docs") {
		t.Error("expected hook NOT to match collection 'docs'")
	}
}

func TestHookMatchesEmptyCollection(t *testing.T) {
	hook := Webhook{
		Events:     []string{"doc.added"},
		Collection: "", // matches all collections
	}

	if !hookMatches(hook, "doc.added", "blog") {
		t.Error("expected hook with empty collection to match any collection")
	}
	if !hookMatches(hook, "doc.added", "docs") {
		t.Error("expected hook with empty collection to match any collection")
	}
}

func TestHookMatchesNoEventMatch(t *testing.T) {
	hook := Webhook{
		Events:     []string{"doc.deleted"},
		Collection: "",
	}

	if hookMatches(hook, "doc.added", "blog") {
		t.Error("expected no match when event does not match")
	}
}

func TestWebhookFire(t *testing.T) {
	wm, cleanup := newTestWebhookManager(t)
	defer cleanup()

	var received int32
	var receivedPayload WebhookPayload
	var mu sync.Mutex

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		_ = json.Unmarshal(body, &receivedPayload)
		mu.Unlock()
		// Incremented last, after the payload is stored: the counter is what
		// the test waits on, so it must not become non-zero before the thing
		// the test then reads is there.
		atomic.AddInt32(&received, 1)
		w.WriteHeader(200)
	}))
	defer ts.Close()

	_, _ = wm.Register(ts.URL, []string{"doc.added"}, "")

	doc := &storage.Doc{
		ID:        "test-id",
		Key:       "test-key",
		Lang:      "en",
		ContentMD: "hello",
	}

	wm.Fire("doc.added", "blog", "test-key", "en", doc)

	testsync.WaitForCount(t, "the webhook to be delivered", 1,
		func() int { return int(atomic.LoadInt32(&received)) })

	if atomic.LoadInt32(&received) != 1 {
		t.Errorf("expected 1 webhook delivery, got %d", atomic.LoadInt32(&received))
	}

	mu.Lock()
	if receivedPayload.Event != "doc.added" {
		t.Errorf("expected event 'doc.added', got %q", receivedPayload.Event)
	}
	if receivedPayload.Collection != "blog" {
		t.Errorf("expected collection 'blog', got %q", receivedPayload.Collection)
	}
	if receivedPayload.Key != "test-key" {
		t.Errorf("expected key 'test-key', got %q", receivedPayload.Key)
	}
	if receivedPayload.Lang != "en" {
		t.Errorf("expected lang 'en', got %q", receivedPayload.Lang)
	}
	mu.Unlock()
}

func TestWebhookFireNoMatch(t *testing.T) {
	wm, cleanup := newTestWebhookManager(t)
	defer cleanup()

	var received int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&received, 1)
		w.WriteHeader(200)
	}))
	defer ts.Close()

	// A second endpoint that DOES match, so the test can wait for something
	// instead of a duration. "Nothing happened" cannot be polled for — you can
	// only ever say "not yet" — so the delivery that should happen becomes the
	// signal. Fire evaluates every registered webhook, so once this one has
	// arrived the non-matching one has necessarily been considered and
	// rejected. That is a fact about the dispatcher, not about the clock.
	var matched int32
	matchedTS := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&matched, 1)
		w.WriteHeader(200)
	}))
	defer matchedTS.Close()

	// Register for doc.deleted only
	_, _ = wm.Register(ts.URL, []string{"doc.deleted"}, "")
	_, _ = wm.Register(matchedTS.URL, []string{"doc.added"}, "")

	// Fire doc.added - should not match the first
	wm.Fire("doc.added", "blog", "key", "en", nil)

	testsync.WaitForCount(t, "the matching webhook to be delivered", 1,
		func() int { return int(atomic.LoadInt32(&matched)) })

	if atomic.LoadInt32(&received) != 0 {
		t.Errorf("expected 0 deliveries for non-matching event, got %d", atomic.LoadInt32(&received))
	}
}

func TestWebhookFireCollectionFilter(t *testing.T) {
	wm, cleanup := newTestWebhookManager(t)
	defer cleanup()

	var received int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&received, 1)
		w.WriteHeader(200)
	}))
	defer ts.Close()

	// Same trick as TestWebhookFireNoMatch: an endpoint registered for the
	// collection being fired gives the test a delivery to wait for, so the
	// absence of the other one is established by the dispatcher having run
	// rather than by a timer having expired.
	var matched int32
	matchedTS := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&matched, 1)
		w.WriteHeader(200)
	}))
	defer matchedTS.Close()

	// Register for blog collection only
	_, _ = wm.Register(ts.URL, []string{"doc.added"}, "blog")
	_, _ = wm.Register(matchedTS.URL, []string{"doc.added"}, "docs")

	// Fire for docs collection - should not match the first
	wm.Fire("doc.added", "docs", "key", "en", nil)

	testsync.WaitForCount(t, "the matching webhook to be delivered", 1,
		func() int { return int(atomic.LoadInt32(&matched)) })

	if atomic.LoadInt32(&received) != 0 {
		t.Errorf("expected 0 deliveries for wrong collection, got %d", atomic.LoadInt32(&received))
	}
}

func TestWebhookFireMultipleHooks(t *testing.T) {
	wm, cleanup := newTestWebhookManager(t)
	defer cleanup()

	var received int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&received, 1)
		w.WriteHeader(200)
	}))
	defer ts.Close()

	// Register two hooks for the same event
	_, _ = wm.Register(ts.URL+"/hook1", []string{"doc.added"}, "")
	_, _ = wm.Register(ts.URL+"/hook2", []string{"doc.added"}, "")

	wm.Fire("doc.added", "blog", "key", "en", nil)

	testsync.WaitForCount(t, "both webhooks to be delivered", 2,
		func() int { return int(atomic.LoadInt32(&received)) })

	if atomic.LoadInt32(&received) != 2 {
		t.Errorf("expected 2 deliveries for multiple matching hooks, got %d", atomic.LoadInt32(&received))
	}
}

func TestWebhookFireNilDocument(t *testing.T) {
	wm, cleanup := newTestWebhookManager(t)
	defer cleanup()

	var received int32
	var receivedPayload WebhookPayload
	var mu sync.Mutex

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		_ = json.Unmarshal(body, &receivedPayload)
		mu.Unlock()
		// Incremented last, after the payload is stored: the counter is what
		// the test waits on, so it must not become non-zero before the thing
		// the test then reads is there.
		atomic.AddInt32(&received, 1)
		w.WriteHeader(200)
	}))
	defer ts.Close()

	_, _ = wm.Register(ts.URL, []string{"doc.deleted"}, "")

	wm.Fire("doc.deleted", "blog", "key", "en", nil)

	testsync.WaitForCount(t, "the delete webhook to be delivered", 1,
		func() int { return int(atomic.LoadInt32(&received)) })

	mu.Lock()
	if receivedPayload.Document != nil {
		t.Error("expected nil document in payload for delete event")
	}
	mu.Unlock()
}

func TestWebhookFireHeaders(t *testing.T) {
	wm, cleanup := newTestWebhookManager(t)
	defer cleanup()

	var received int32
	var gotContentType, gotEvent, gotWebhookID string
	var mu sync.Mutex

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		gotContentType = r.Header.Get("Content-Type")
		gotEvent = r.Header.Get("X-MDDB-Event")
		gotWebhookID = r.Header.Get("X-MDDB-Webhook-ID")
		mu.Unlock()
		// Last, after the headers are captured: see TestWebhookFireNilDocument.
		atomic.AddInt32(&received, 1)
		w.WriteHeader(200)
	}))
	defer ts.Close()

	wh, _ := wm.Register(ts.URL, []string{"doc.added"}, "")
	wm.Fire("doc.added", "blog", "key", "en", nil)

	testsync.WaitForCount(t, "the webhook to be delivered", 1,
		func() int { return int(atomic.LoadInt32(&received)) })

	mu.Lock()
	if gotContentType != "application/json" {
		t.Errorf("expected Content-Type 'application/json', got %q", gotContentType)
	}
	if gotEvent != "doc.added" {
		t.Errorf("expected X-MDDB-Event 'doc.added', got %q", gotEvent)
	}
	if gotWebhookID != wh.ID {
		t.Errorf("expected X-MDDB-Webhook-ID %q, got %q", wh.ID, gotWebhookID)
	}
	mu.Unlock()
}

func TestWebhookSetBinlog(t *testing.T) {
	wm, cleanup := newTestWebhookManager(t)
	defer cleanup()

	wm.SetBinlog(nil)
	if wm.binlog != nil {
		t.Error("expected nil binlog")
	}
}

func TestGenerateWebhookID(t *testing.T) {
	id1 := generateWebhookID()
	id2 := generateWebhookID()

	if len(id1) != 16 {
		t.Errorf("expected 16-char hex ID, got %d chars: %q", len(id1), id1)
	}
	if id1 == id2 {
		t.Error("expected unique IDs")
	}
}

func TestWebhookPersistedInDB(t *testing.T) {
	wm, cleanup := newTestWebhookManager(t)
	defer cleanup()

	wh, _ := wm.Register("http://example.com/hook", []string{"doc.added"}, "blog")

	// Verify it's in the DB
	err := wm.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketWebhooks)
		v := b.Get([]byte("wh|" + wh.ID))
		if v == nil {
			t.Error("webhook not found in database")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestWebhookDeleteFromDB(t *testing.T) {
	wm, cleanup := newTestWebhookManager(t)
	defer cleanup()

	wh, _ := wm.Register("http://example.com/hook", []string{"doc.added"}, "")
	if err := wm.Delete(wh.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	// Verify it's removed from DB
	err := wm.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketWebhooks)
		v := b.Get([]byte("wh|" + wh.ID))
		if v != nil {
			t.Error("webhook should be deleted from database")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestWebhookConcurrentRegisterAndList(t *testing.T) {
	wm, cleanup := newTestWebhookManager(t)
	defer cleanup()

	var wg sync.WaitGroup

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = wm.Register("http://example.com/hook", []string{"doc.added"}, "")
		}()
	}

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			wm.List()
		}()
	}

	wg.Wait()

	hooks := wm.List()
	if len(hooks) != 10 {
		t.Errorf("expected 10 webhooks after concurrent registration, got %d", len(hooks))
	}
}
