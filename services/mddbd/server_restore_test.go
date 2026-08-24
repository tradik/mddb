package main

import (
	"mddb/internal/cache"
	"mddb/internal/schema"
	"mddb/internal/webhooks"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	bolt "go.etcd.io/bbolt"
)

// openDocsDB opens a fresh BoltDB with a "docs" bucket containing one key, so
// DBView readers in the race test always have something to Get.
func openDocsDB(t *testing.T, path string) *bolt.DB {
	t.Helper()
	db, err := bolt.Open(path, 0600, nil)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.Update(func(tx *bolt.Tx) error {
		b, err := tx.CreateBucketIfNotExists([]byte("docs"))
		if err != nil {
			return err
		}
		return b.Put([]byte("k"), []byte("v"))
	}); err != nil {
		t.Fatalf("seed db: %v", err)
	}
	return db
}

// swapServerDB mirrors ReplicationClient.replaceDatabase: open a new handle and
// swap it in under the restore write lock, closing the old one. The close is
// the dangerous point — withRestoreLock must have drained all DBView readers.
func swapServerDB(t *testing.T, s *Server) {
	t.Helper()
	np := filepath.Join(t.TempDir(), "swap.db")
	ndb := openDocsDB(t, np)
	if err := s.withRestoreLock(func() error {
		old := s.DB
		s.DB = ndb
		return old.Close()
	}); err != nil {
		t.Fatalf("swap: %v", err)
	}
}

// TestDBViewConcurrentWithRestore_NoRace is the GO-004 regression: many readers
// hit DBView while the DB handle is repeatedly closed and swapped. Run with
// `go test -race`; without the restoreMu guard the closed handle would be read
// mid-View (panic / "database not open") and the pointer write would race.
func TestDBViewConcurrentWithRestore_NoRace(t *testing.T) {
	dir := t.TempDir()
	s := &Server{DB: openDocsDB(t, filepath.Join(dir, "base.db"))}
	t.Cleanup(func() { _ = s.DB.Close() })

	stop := make(chan struct{})
	var wg sync.WaitGroup

	// Reader goroutines: each read goes through DBView (restore read lock).
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				_ = s.DBView(func(tx *bolt.Tx) error {
					if b := tx.Bucket([]byte("docs")); b != nil {
						_ = b.Get([]byte("k"))
					}
					return nil
				})
			}
		}()
	}

	// Concurrent restores swap the handle out from under the readers.
	for i := 0; i < 30; i++ {
		swapServerDB(t, s)
	}

	close(stop)
	wg.Wait()
}

// TestDBUpdateConcurrentWithRestore_NoRace is the write-path twin of the above.
func TestDBUpdateConcurrentWithRestore_NoRace(t *testing.T) {
	dir := t.TempDir()
	s := &Server{DB: openDocsDB(t, filepath.Join(dir, "base.db"))}
	t.Cleanup(func() { _ = s.DB.Close() })

	stop := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				_ = s.DBUpdate(func(tx *bolt.Tx) error {
					if b := tx.Bucket([]byte("docs")); b != nil {
						return b.Put([]byte("k"), []byte("v2"))
					}
					return nil
				})
			}
		}()
	}
	for i := 0; i < 20; i++ {
		swapServerDB(t, s)
	}
	close(stop)
	wg.Wait()
}

// TestDocumentCacheClose_StopsCleanupGoroutine proves Close() reaps the cleanup
// goroutine, so a restore that recycles caches can't leak one per restore.
func TestDocumentCacheClose_StopsCleanupGoroutine(t *testing.T) {
	runtime.GC()
	before := runtime.NumGoroutine()

	caches := make([]*cache.DocumentCache, 0, 50)
	for i := 0; i < 50; i++ {
		caches = append(caches, cache.NewDocumentCache(10, 60))
	}
	for _, c := range caches {
		c.Close()
		c.Close() // idempotent — must not panic on double close
	}

	// Cleanup goroutines exit asynchronously after close(stopCh); poll briefly.
	deadline := time.Now().Add(2 * time.Second)
	for runtime.NumGoroutine() > before+5 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if got := runtime.NumGoroutine(); got > before+5 {
		t.Errorf("cleanup goroutines leaked: before=%d after=%d (Close did not stop them)", before, got)
	}
}

// TestDocumentCacheClear_ResetsContents covers the in-place reset the restore
// uses instead of allocating a new cache.
func TestDocumentCacheClear_ResetsContents(t *testing.T) {
	c := cache.NewDocumentCache(10, 60)
	defer c.Close()
	c.Set("a", []byte("1"))
	c.Set("b", []byte("2"))
	if _, _, size := c.Stats(); size != 2 {
		t.Fatalf("expected 2 entries, got %d", size)
	}
	c.Clear()
	if _, _, size := c.Stats(); size != 0 {
		t.Fatalf("expected empty cache after Clear, got %d", size)
	}
	if _, ok := c.Get("a"); ok {
		t.Error("entry survived Clear")
	}
}

// TestRebuildInMemoryState_InPlace verifies the restore reloads caches/managers
// WITHOUT swapping the Server-level pointers (the GO-004 fix), so concurrent
// readers of those fields never observe a swap.
func TestRebuildInMemoryState_InPlace(t *testing.T) {
	dir := t.TempDir()
	db := openDocsDB(t, filepath.Join(dir, "base.db"))
	t.Cleanup(func() { _ = db.Close() })

	cache := cache.NewDocumentCache(10, 60)
	t.Cleanup(cache.Close)
	sm := schema.NewSchemaManager(db)
	if err := sm.EnsureBucket(); err != nil {
		t.Fatal(err)
	}
	wm := webhooks.NewWebhookManager(db)
	if err := wm.EnsureBucket(); err != nil {
		t.Fatal(err)
	}

	s := &Server{DB: db, Cache: cache, SchemaManager: sm, WebhookManager: wm}
	cache.Set("stale", []byte("x")) // should be cleared by the rebuild
	rc := &ReplicationClient{server: s}

	if err := s.withRestoreLock(func() error {
		rc.server.rebuildInMemoryState()
		return nil
	}); err != nil {
		t.Fatalf("rebuild: %v", err)
	}

	if s.Cache != cache {
		t.Error("Cache pointer was swapped (expected in-place Clear)")
	}
	if s.SchemaManager != sm {
		t.Error("schema.SchemaManager pointer was swapped (expected in-place reload)")
	}
	if s.WebhookManager != wm {
		t.Error("WebhookManager pointer was swapped (expected in-place reload)")
	}
	if _, _, size := s.Cache.Stats(); size != 0 {
		t.Errorf("cache not cleared by rebuild, size=%d", size)
	}
}

// TestSchemaManagerReload_RepointsDB confirms reload swaps the db handle and
// reloads schemas from it, keeping the same manager instance.
func TestSchemaManagerReload_RepointsDB(t *testing.T) {
	dir := t.TempDir()
	db1 := openDocsDB(t, filepath.Join(dir, "1.db"))
	sm := schema.NewSchemaManager(db1)
	if err := sm.EnsureBucket(); err != nil {
		t.Fatal(err)
	}
	if err := sm.Set("blog", `{"required":["title"]}`); err != nil {
		t.Fatal(err)
	}
	_ = db1.Close()

	// A second DB with no schemas — after reload, the blog schema must be gone.
	db2 := openDocsDB(t, filepath.Join(dir, "2.db"))
	t.Cleanup(func() { _ = db2.Close() })
	if err := sm.Reload(db2); err != nil {
		t.Fatalf("reload: %v", err)
	}
	if _, found := sm.Get("blog"); found {
		t.Error("schema from the old DB survived reload onto a fresh DB")
	}
}
