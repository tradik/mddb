package main

import (
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	"mddb/internal/cache"
	json "mddb/internal/jsonx"
	"mddb/internal/storage"
	"mddb/internal/temporal"

	bolt "go.etcd.io/bbolt"
)

// covHRevisionKeyOverflow returns a key whose document key is exactly as long
// as bbolt allows, so the document is stored and its revision key — 21 bytes
// longer — is refused.
func covHRevisionKeyOverflow(collection, lang string) string {
	return strings.Repeat("k", bolt.MaxKeySize-len(storage.DocKey(collection, genID(collection, "", lang))))
}

// covHDocCount counts the documents stored for a collection.
func covHDocCount(t *testing.T, s *Server, collection string) int {
	t.Helper()
	n := 0
	_ = s.DB.View(func(tx *bolt.Tx) error {
		return tx.Bucket([]byte("docs")).ForEach(func(k, _ []byte) error {
			if strings.HasPrefix(string(k), "doc|"+collection+"|") {
				n++
			}
			return nil
		})
	})
	return n
}

// covHRevCount counts the revisions stored for a document.
func covHRevCount(t *testing.T, s *Server, collection, docID string) int {
	t.Helper()
	n := 0
	prefix := string(storage.RevPrefix(collection, docID))
	_ = s.DB.View(func(tx *bolt.Tx) error {
		return tx.Bucket([]byte("rev")).ForEach(func(k, _ []byte) error {
			if strings.HasPrefix(string(k), prefix) {
				n++
			}
			return nil
		})
	})
	return n
}

// covHFailingBackend is a memory backend whose deletes fail.
type covHFailingBackend struct{ StorageBackend }

func (covHFailingBackend) DeleteDoc(string, string) error { return errors.New("covH: delete refused") }
func (covHFailingBackend) Name() string                   { return "covh" }

func TestCovHAddDocumentRefusals(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	// A stored document that no longer decodes cannot be overwritten blind.
	_ = s.DB.Update(func(tx *bolt.Tx) error {
		return tx.Bucket([]byte("docs")).Put(storage.DocKey("c", genID("c", "bad", "en")), []byte{0x01})
	})
	if _, _, err := s.addDocument("c", "bad", "en", nil, "x", 0, true); err == nil {
		t.Fatal("a corrupt existing document must fail the write")
	}

	// A key protobuf cannot encode fails at marshal time.
	if _, _, err := s.addDocument("c", "bad\xffkey", "en", nil, "x", 0, true); err == nil {
		t.Fatal("an invalid UTF-8 key must fail")
	}

	// A key too long for bbolt fails the document write.
	if _, _, err := s.addDocument("c", strings.Repeat("k", bolt.MaxKeySize), "en", nil, "x", 0, true); err == nil {
		t.Fatal("an oversized key must fail")
	}
	// A metadata value too long for the index fails the index write.
	if _, _, err := s.addDocument("c", "m", "en", map[string][]string{"k": {strings.Repeat("v", bolt.MaxKeySize)}}, "x", 0, true); err == nil {
		t.Fatal("an oversized metadata value must fail")
	}
	// A key that fits the document bucket but not the revision history.
	long := covHRevisionKeyOverflow("c", "en")
	if _, _, err := s.addDocument("c", long, "en", nil, "x", 0, true); err == nil {
		t.Fatal("a revision key over the limit must fail")
	}
	// Every refused write rolled back: only the corrupt seed is stored.
	if n := covHDocCount(t, s, "c"); n != 1 {
		t.Fatalf("refused writes left %d documents, want 1", n)
	}
	// Without a revision the same key is stored.
	if _, _, err := s.addDocument("c", long, "en", nil, "x", 0, false); err != nil {
		t.Fatalf("no revision asked: %v", err)
	}

	// An external backend that refuses the payload refuses the write.
	mem, err := CreateBackend("memory", nil)
	if err != nil {
		t.Fatal(err)
	}
	s.Backends = NewBackendRegistry(nil)
	s.Backends.Register("ext", mem)
	s.Backends.MarkFailed("ext", errors.New("covH: unreachable"))
	if _, _, err := s.addDocument("ext", "a", "en", nil, "x", 0, true); err == nil || !strings.Contains(err.Error(), "unavailable storage backend") {
		t.Fatalf("failed backend: %v", err)
	}
	if n := covHDocCount(t, s, "ext"); n != 0 {
		t.Fatal("a refused payload left a document")
	}
}

func TestCovHAddDocumentMetaReindexAndRetention(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	if err := s.CollectionManager.Set("c", &CollectionConfig{MaxRevisions: 2}); err != nil {
		t.Fatal(err)
	}
	var doc storage.Doc
	for i := range 4 {
		doc = addTestDoc(t, s, "c", "a", "en", "v"+itoa(i), map[string][]string{"tag": {"t" + itoa(i)}})
	}
	if n := covHRevCount(t, s, "c", doc.ID); n != 2 {
		t.Fatalf("revisions kept: %d, want 2", n)
	}
	// Only the current tag is indexed; the earlier ones were removed.
	_ = s.DB.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte("idxmeta"))
		if b.Get(append(storage.MetaKeyPrefix("c", "tag", "t3"), doc.ID...)) == nil {
			t.Error("current tag not indexed")
		}
		if b.Get(append(storage.MetaKeyPrefix("c", "tag", "t0"), doc.ID...)) != nil {
			t.Error("stale tag still indexed")
		}
		return nil
	})
}

// covHSideEffectServer wires every optional subsystem the write pipeline
// feeds.
func covHSideEffectServer(t *testing.T) (*Server, *sseClient, func()) {
	t.Helper()
	s, cleanup := newHandlerTestServer(t)
	enableGeoOnTestServer(t, s)
	s.EmbeddingWorker = NewEmbeddingWorker(nil, s.VectorStore, s.VectorIndex, 16)
	s.TemporalManager = temporal.NewTemporalManager(s.DB)
	if err := s.TemporalManager.EnsureBuckets(); err != nil {
		t.Fatal(err)
	}
	s.TemporalManager.Start()
	s.SSEHub = NewSSEHub(true, 0, 0)
	client := &sseClient{ch: make(chan []byte, 64)}
	if !s.SSEHub.addClient(client) {
		t.Fatal("sse client refused")
	}
	s.AutomationManager = NewAutomationManager(s.DB)
	s.UseExtreme = true
	s.LockFreeCache = cache.NewLockFreeCache(100, 60)
	t.Setenv("MDDB_TRIGGERS", "true")
	return s, client, func() {
		s.TemporalManager.Stop()
		s.LockFreeCache.Close()
		cleanup()
	}
}

// covHNextSSE returns the next SSE event's name.
func covHNextSSE(t *testing.T, c *sseClient) string {
	t.Helper()
	select {
	case raw := <-c.ch:
		s := string(raw)
		if i := strings.Index(s, "{"); i >= 0 {
			s = s[i:]
		}
		if j := strings.LastIndex(s, "}"); j >= 0 {
			s = s[:j+1]
		}
		var ev SSEEvent
		if err := json.Unmarshal([]byte(s), &ev); err != nil {
			t.Fatalf("sse payload %q: %v", raw, err)
		}
		return ev.Event
	case <-time.After(5 * time.Second):
		t.Fatal("no sse event")
		return ""
	}
}

// covHNextJob returns the next queued embedding job.
func covHNextJob(t *testing.T, s *Server) EmbeddingJob {
	t.Helper()
	select {
	case j := <-s.EmbeddingWorker.jobs:
		return j
	case <-time.After(5 * time.Second):
		t.Fatal("no embedding job")
		return EmbeddingJob{}
	}
}

func TestCovHWritePipelineSideEffects(t *testing.T) {
	s, client, cleanup := covHSideEffectServer(t)
	defer cleanup()
	geoMeta := map[string][]string{"geo_lat": {"52.52"}, "geo_lng": {"13.40"}}

	// Add: embedding queued, geo indexed, lock-free cache filled, SSE fired.
	doc, isNew, err := s.addDocument("places", "berlin", "en", geoMeta, "Berlin", 0, true)
	if err != nil || !isNew {
		t.Fatalf("add: %v %v", err, isNew)
	}
	if j := covHNextJob(t, s); j.DocID != doc.ID || j.ContentMD != "Berlin" {
		t.Fatalf("embedding job: %+v", j)
	}
	if _, _, ok, _ := s.GeoStore.Get("places", doc.ID); !ok || s.GeoHashIndex.Len("places") != 1 {
		t.Fatal("geo point not stored")
	}
	if _, hit := s.LockFreeCache.Get(cache.BuildCacheKey("places", "berlin", "en")); !hit {
		t.Fatal("lock-free cache not refreshed")
	}
	if ev := covHNextSSE(t, client); ev != "doc.added" {
		t.Fatalf("sse: %s", ev)
	}

	// Re-add without coordinates: the stale point goes.
	if _, isNew, err := s.addDocument("places", "berlin", "en", nil, "Berlin again", 0, true); err != nil || isNew {
		t.Fatalf("re-add: %v %v", err, isNew)
	}
	covHNextJob(t, s)
	if _, _, ok, _ := s.GeoStore.Get("places", doc.ID); ok || s.GeoHashIndex.Len("places") != 0 {
		t.Fatal("stale geo point kept")
	}
	if ev := covHNextSSE(t, client); ev != "doc.updated" {
		t.Fatalf("sse: %s", ev)
	}

	// Partial update with coordinates and content: point back, job queued.
	content := "Berlin, updated"
	if _, err := s.updateDocument("places", "berlin", "en", DocPatch{HasMeta: true, Meta: geoMeta, Content: &content}); err != nil {
		t.Fatal(err)
	}
	if j := covHNextJob(t, s); j.ContentMD != content {
		t.Fatalf("update job: %+v", j)
	}
	if _, _, ok, _ := s.GeoStore.Get("places", doc.ID); !ok || s.GeoHashIndex.Len("places") != 1 {
		t.Fatal("update did not index the point")
	}
	// Partial update dropping the coordinates removes the point.
	if _, err := s.updateDocument("places", "berlin", "en", DocPatch{HasMeta: true, Meta: map[string][]string{"k": {"v"}}}); err != nil {
		t.Fatal(err)
	}
	if _, _, ok, _ := s.GeoStore.Get("places", doc.ID); ok || s.GeoHashIndex.Len("places") != 0 {
		t.Fatal("update kept a stale point")
	}

	// Temporal tracking recorded the create and the update.
	deadline := time.Now().Add(5 * time.Second)
	for {
		evs, _ := s.TemporalManager.QueryRange("places", doc.ID, 0, math.MaxInt64, "", 10)
		kinds := map[temporal.TemporalEventType]bool{}
		for _, e := range evs {
			kinds[e.EventType] = true
		}
		if kinds[temporal.EventCreate] && kinds[temporal.EventUpdate] {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("temporal events: %+v", evs)
		}
		time.Sleep(20 * time.Millisecond)
	}

	// Delete: vectors (legacy key and chunks), geo point and caches go.
	if _, err := s.updateDocument("places", "berlin", "en", DocPatch{HasMeta: true, Meta: geoMeta}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{doc.ID, doc.ID + "#0", doc.ID + "#1"} {
		if err := s.VectorIndex.Add("places", id, []float32{1, 0}); err != nil {
			t.Fatal(err)
		}
	}
	for len(client.ch) > 0 {
		<-client.ch
	}
	if err := s.deleteDocumentInternal("places", "berlin", "en"); err != nil {
		t.Fatal(err)
	}
	if n := s.VectorIndex.CollectionSize("places"); n != 0 {
		t.Fatalf("vectors left after delete: %d", n)
	}
	if _, _, ok, _ := s.GeoStore.Get("places", doc.ID); ok || s.GeoHashIndex.Len("places") != 0 {
		t.Fatal("delete kept the geo point")
	}
	if _, hit := s.LockFreeCache.Get(cache.BuildCacheKey("places", "berlin", "en")); hit {
		t.Fatal("delete left the lock-free cache entry")
	}
	if ev := covHNextSSE(t, client); ev != "doc.deleted" {
		t.Fatalf("sse: %s", ev)
	}
	if _, err := s.loadDocByRef("places", "berlin", "en"); err == nil {
		t.Fatal("document still readable after delete")
	}
}

func TestCovHDeleteDocumentRefusals(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	if err := s.deleteDocumentInternal("c", "missing", "en"); err == nil {
		t.Fatal("deleting a missing document must fail")
	}
	_ = s.DB.Update(func(tx *bolt.Tx) error {
		return tx.Bucket([]byte("docs")).Put(storage.DocKey("c", genID("c", "bad", "en")), []byte{0x01})
	})
	if err := s.deleteDocumentInternal("c", "bad", "en"); err == nil {
		t.Fatal("a corrupt document must fail the delete")
	}

	// A backend that refuses the delete keeps the document indexed.
	mem, err := CreateBackend("memory", nil)
	if err != nil {
		t.Fatal(err)
	}
	s.Backends = NewBackendRegistry(nil)
	s.Backends.Register("ext", covHFailingBackend{mem})
	addTestDoc(t, s, "ext", "a", "en", "x", nil)
	if err := s.deleteDocumentInternal("ext", "a", "en"); err == nil || !strings.Contains(err.Error(), "covH") {
		t.Fatalf("backend refusal: %v", err)
	}
	if n := covHDocCount(t, s, "ext"); n != 1 {
		t.Fatal("a refused delete removed the index entry")
	}
}
