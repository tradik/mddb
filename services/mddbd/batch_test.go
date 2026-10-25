package main

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"

	"mddb/internal/cache"
	"mddb/internal/indexqueue"
	"mddb/internal/storage"
	proto "mddb/proto"

	bolt "go.etcd.io/bbolt"
)

// newTestServerForBatch creates a minimal Server with a temp BoltDB and all
// required buckets, suitable for batch processor tests.
func newTestServerForBatch(t *testing.T) (*Server, func()) {
	t.Helper()

	f, err := os.CreateTemp("", "batch_test_*.db")
	if err != nil {
		t.Fatal(err)
	}
	_ = f.Close()

	db, err := bolt.Open(f.Name(), 0600, &bolt.Options{Timeout: 1 * time.Second})
	if err != nil {
		_ = os.Remove(f.Name())
		t.Fatal(err)
	}

	// Create all required buckets
	err = db.Update(func(tx *bolt.Tx) error {
		buckets := []string{
			"docs", "idxmeta", "rev", "bykey", "vectors",
			"fts_tokens", "webhooks", "schemas", "auth_users",
			"auth_groups", "ttl",
		}
		for _, name := range buckets {
			if _, err := tx.CreateBucketIfNotExists([]byte(name)); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		_ = db.Close()
		_ = os.Remove(f.Name())
		t.Fatal(err)
	}

	srv := &Server{
		DB:   db,
		Path: f.Name(),
		Mode: ModeRW,
		BucketNames: BucketNames{
			Docs:    []byte("docs"),
			IdxMeta: []byte("idxmeta"),
			Rev:     []byte("rev"),
			ByKey:   []byte("bykey"),
		},
		Cache: cache.NewDocumentCache(100, 60),
	}

	// Set up IndexQueue so batch update can enqueue reindex jobs
	srv.IndexQueue = indexqueue.NewIndexQueue(serverIndexStore{s: srv}, 2)

	cleanup := func() {
		srv.IndexQueue.Shutdown()
		_ = db.Close()
		_ = os.Remove(f.Name())
	}

	return srv, cleanup
}

// makeBatchDoc is a helper to create a proto.BatchDocument.
func makeBatchDoc(key, lang, content string, meta map[string]*proto.MetaValues, saveRevision bool) *proto.BatchDocument {
	return &proto.BatchDocument{
		Key:          key,
		Lang:         lang,
		ContentMd:    content,
		Meta:         meta,
		SaveRevision: saveRevision,
	}
}

// -----------------------------------------------------------------------
// BatchProcessor tests (batch.go)
// -----------------------------------------------------------------------

func TestBatchProcessor_NewBatchProcessor_DefaultWorkers(t *testing.T) {
	srv, cleanup := newTestServerForBatch(t)
	defer cleanup()

	bp := NewBatchProcessor(srv, 0)
	if bp.maxWorkers != 4 {
		t.Errorf("expected default 4 workers, got %d", bp.maxWorkers)
	}

	bp2 := NewBatchProcessor(srv, -1)
	if bp2.maxWorkers != 4 {
		t.Errorf("expected default 4 workers for negative input, got %d", bp2.maxWorkers)
	}
}

func TestBatchProcessor_NewBatchProcessor_CustomWorkers(t *testing.T) {
	srv, cleanup := newTestServerForBatch(t)
	defer cleanup()

	bp := NewBatchProcessor(srv, 16)
	if bp.maxWorkers != 16 {
		t.Errorf("expected 16 workers, got %d", bp.maxWorkers)
	}
}

func TestBatchProcessor_EmptyBatch(t *testing.T) {
	srv, cleanup := newTestServerForBatch(t)
	defer cleanup()

	bp := NewBatchProcessor(srv, 4)
	resp, err := bp.ProcessBatch(context.Background(), "blog", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Added != 0 || resp.Updated != 0 || resp.Failed != 0 {
		t.Errorf("expected all zeros for empty batch, got added=%d updated=%d failed=%d",
			resp.Added, resp.Updated, resp.Failed)
	}
}

func TestBatchProcessor_AddSingleDocument(t *testing.T) {
	srv, cleanup := newTestServerForBatch(t)
	defer cleanup()

	bp := NewBatchProcessor(srv, 4)
	docs := []*proto.BatchDocument{
		makeBatchDoc("post1", "en", "# Hello World", nil, false),
	}

	resp, err := bp.ProcessBatch(context.Background(), "blog", docs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Added != 1 {
		t.Errorf("expected 1 added, got %d", resp.Added)
	}
	if resp.Updated != 0 {
		t.Errorf("expected 0 updated, got %d", resp.Updated)
	}
	if resp.Failed != 0 {
		t.Errorf("expected 0 failed, got %d (errors: %v)", resp.Failed, resp.Errors)
	}

	// Verify document is stored in BoltDB
	docID := genID("blog", "post1", "en")
	err = srv.DBViewC("blog", func(tx *bolt.Tx) error {
		bDocs := tx.Bucket([]byte("docs"))
		v := bDocs.Get(storage.DocKey("blog", docID))
		if v == nil {
			t.Error("document not found in docs bucket")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("DB.View failed: %v", err)
	}
}

func TestBatchProcessor_AddMultipleDocuments(t *testing.T) {
	srv, cleanup := newTestServerForBatch(t)
	defer cleanup()

	bp := NewBatchProcessor(srv, 4)
	docs := make([]*proto.BatchDocument, 10)
	for i := 0; i < 10; i++ {
		docs[i] = makeBatchDoc(
			"post"+string(rune('0'+i)),
			"en",
			"# Content "+string(rune('0'+i)),
			nil,
			false,
		)
	}

	resp, err := bp.ProcessBatch(context.Background(), "blog", docs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Added != 10 {
		t.Errorf("expected 10 added, got %d", resp.Added)
	}
	if resp.Failed != 0 {
		t.Errorf("expected 0 failed, got %d (errors: %v)", resp.Failed, resp.Errors)
	}
}

func TestBatchProcessor_AddWithMeta(t *testing.T) {
	srv, cleanup := newTestServerForBatch(t)
	defer cleanup()

	bp := NewBatchProcessor(srv, 4)
	meta := map[string]*proto.MetaValues{
		"tag":    {Values: []string{"go", "database"}},
		"author": {Values: []string{"alice"}},
	}
	docs := []*proto.BatchDocument{
		makeBatchDoc("post1", "en", "# Hello", meta, false),
	}

	resp, err := bp.ProcessBatch(context.Background(), "blog", docs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Added != 1 {
		t.Errorf("expected 1 added, got %d", resp.Added)
	}

	// Verify metadata indices created
	docID := genID("blog", "post1", "en")
	err = srv.DBViewC("blog", func(tx *bolt.Tx) error {
		bIdx := tx.Bucket([]byte("idxmeta"))

		// Check tag=go index
		key := append(storage.MetaKeyPrefix("blog", "tag", "go"), []byte(docID)...)
		if v := bIdx.Get(key); v == nil {
			t.Error("expected index entry for tag=go")
		}

		// Check author=alice index
		key2 := append(storage.MetaKeyPrefix("blog", "author", "alice"), []byte(docID)...)
		if v := bIdx.Get(key2); v == nil {
			t.Error("expected index entry for author=alice")
		}

		return nil
	})
	if err != nil {
		t.Fatalf("DB.View failed: %v", err)
	}
}

func TestBatchProcessor_AddWithRevision(t *testing.T) {
	srv, cleanup := newTestServerForBatch(t)
	defer cleanup()

	bp := NewBatchProcessor(srv, 4)
	docs := []*proto.BatchDocument{
		makeBatchDoc("post1", "en", "# Version 1", nil, true),
	}

	resp, err := bp.ProcessBatch(context.Background(), "blog", docs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Added != 1 {
		t.Errorf("expected 1 added, got %d", resp.Added)
	}

	// Verify revision stored
	docID := genID("blog", "post1", "en")
	revPrefix := storage.RevPrefix("blog", docID)
	revCount := 0
	err = srv.DBViewC("blog", func(tx *bolt.Tx) error {
		bRev := tx.Bucket([]byte("rev"))
		c := bRev.Cursor()
		for k, _ := c.Seek(revPrefix); k != nil && len(k) >= len(revPrefix); k, _ = c.Next() {
			if string(k[:len(revPrefix)]) != string(revPrefix) {
				break
			}
			revCount++
		}
		return nil
	})
	if err != nil {
		t.Fatalf("DB.View failed: %v", err)
	}
	if revCount != 1 {
		t.Errorf("expected 1 revision, got %d", revCount)
	}
}

func TestBatchProcessor_InvalidDocuments(t *testing.T) {
	srv, cleanup := newTestServerForBatch(t)
	defer cleanup()

	bp := NewBatchProcessor(srv, 4)
	docs := []*proto.BatchDocument{
		makeBatchDoc("", "en", "content", nil, false),      // missing key
		makeBatchDoc("post1", "", "content", nil, false),   // missing lang
		makeBatchDoc("", "", "content", nil, false),        // missing both
		makeBatchDoc("post2", "en", "# Valid", nil, false), // valid
	}

	resp, err := bp.ProcessBatch(context.Background(), "blog", docs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Added != 1 {
		t.Errorf("expected 1 added, got %d", resp.Added)
	}
	if resp.Failed != 3 {
		t.Errorf("expected 3 failed, got %d", resp.Failed)
	}
	if len(resp.Errors) != 3 {
		t.Errorf("expected 3 error messages, got %d", len(resp.Errors))
	}
}

func TestBatchProcessor_UpdateExistingDocument(t *testing.T) {
	srv, cleanup := newTestServerForBatch(t)
	defer cleanup()

	bp := NewBatchProcessor(srv, 4)

	// First batch: add document
	docs1 := []*proto.BatchDocument{
		makeBatchDoc("post1", "en", "# Version 1", nil, false),
	}
	resp1, err := bp.ProcessBatch(context.Background(), "blog", docs1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp1.Added != 1 {
		t.Fatalf("expected 1 added, got %d", resp1.Added)
	}

	// Second batch: same key+lang should be an update
	docs2 := []*proto.BatchDocument{
		makeBatchDoc("post1", "en", "# Version 2", nil, false),
	}
	resp2, err := bp.ProcessBatch(context.Background(), "blog", docs2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp2.Updated != 1 {
		t.Errorf("expected 1 updated, got %d", resp2.Updated)
	}
	if resp2.Added != 0 {
		t.Errorf("expected 0 added on update, got %d", resp2.Added)
	}

	// Verify updated content
	docID := genID("blog", "post1", "en")
	err = srv.DBViewC("blog", func(tx *bolt.Tx) error {
		bDocs := tx.Bucket([]byte("docs"))
		v := bDocs.Get(storage.DocKey("blog", docID))
		if v == nil {
			t.Error("document not found after update")
			return nil
		}
		doc, err := unmarshalDoc(v)
		if err != nil {
			return err
		}
		if doc.ContentMD != "# Version 2" {
			t.Errorf("expected updated content '# Version 2', got %q", doc.ContentMD)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("DB.View failed: %v", err)
	}
}

func TestBatchProcessor_ByKeyIndex(t *testing.T) {
	srv, cleanup := newTestServerForBatch(t)
	defer cleanup()

	bp := NewBatchProcessor(srv, 4)
	docs := []*proto.BatchDocument{
		makeBatchDoc("homepage", "en_US", "# Home", nil, false),
	}

	_, err := bp.ProcessBatch(context.Background(), "site", docs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify bykey index
	err = srv.DBViewC("site", func(tx *bolt.Tx) error {
		bByK := tx.Bucket([]byte("bykey"))
		byKeyK := storage.ByKeyKey("site", "homepage", "en_US")
		v := bByK.Get(byKeyK)
		if v == nil {
			t.Error("bykey index not created")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("DB.View failed: %v", err)
	}
}

func TestBatchProcessor_ConcurrentProcessing(t *testing.T) {
	srv, cleanup := newTestServerForBatch(t)
	defer cleanup()

	bp := NewBatchProcessor(srv, 8)

	// Process multiple batches concurrently
	var wg sync.WaitGroup
	errors := make([]error, 5)
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			docs := []*proto.BatchDocument{
				makeBatchDoc("doc"+string(rune('A'+idx)), "en", "content", nil, false), //nolint:gosec // G115: safe int→rune for ASCII letters A-Z in test
			}
			_, errors[idx] = bp.ProcessBatch(context.Background(), "concurrent", docs)
		}(i)
	}
	wg.Wait()

	for i, err := range errors {
		if err != nil {
			t.Errorf("batch %d failed: %v", i, err)
		}
	}
}

// -----------------------------------------------------------------------
// FinalBatchProcessor tests (batch_final.go)
// -----------------------------------------------------------------------

func TestFinalBatchProcessor_NewFinalBatchProcessor_DefaultWorkers(t *testing.T) {
	srv, cleanup := newTestServerForBatch(t)
	defer cleanup()

	fbp := NewFinalBatchProcessor(srv, 0)
	if fbp.maxWorkers != 8 {
		t.Errorf("expected default 8 workers, got %d", fbp.maxWorkers)
	}

	fbp2 := NewFinalBatchProcessor(srv, -5)
	if fbp2.maxWorkers != 8 {
		t.Errorf("expected default 8 workers for negative input, got %d", fbp2.maxWorkers)
	}
}

func TestFinalBatchProcessor_EmptyBatch(t *testing.T) {
	srv, cleanup := newTestServerForBatch(t)
	defer cleanup()

	fbp := NewFinalBatchProcessor(srv, 4)
	resp, err := fbp.ProcessBatch(context.Background(), "blog", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Added != 0 || resp.Updated != 0 || resp.Failed != 0 {
		t.Errorf("expected all zeros, got added=%d updated=%d failed=%d",
			resp.Added, resp.Updated, resp.Failed)
	}
}

func TestFinalBatchProcessor_AddDocuments(t *testing.T) {
	srv, cleanup := newTestServerForBatch(t)
	defer cleanup()

	fbp := NewFinalBatchProcessor(srv, 4)
	meta := map[string]*proto.MetaValues{
		"category": {Values: []string{"tech"}},
	}
	docs := []*proto.BatchDocument{
		makeBatchDoc("p1", "en", "# Post 1", meta, false),
		makeBatchDoc("p2", "en", "# Post 2", nil, false),
		makeBatchDoc("p3", "fr", "# Poste 3", nil, true),
	}

	resp, err := fbp.ProcessBatch(context.Background(), "blog", docs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Added != 3 {
		t.Errorf("expected 3 added, got %d (errors: %v)", resp.Added, resp.Errors)
	}
	if resp.Failed != 0 {
		t.Errorf("expected 0 failed, got %d (errors: %v)", resp.Failed, resp.Errors)
	}

	// Verify documents exist in DB
	for _, key := range []string{"p1", "p2"} {
		docID := genID("blog", key, "en")
		err = srv.DBViewC("blog", func(tx *bolt.Tx) error {
			bDocs := tx.Bucket([]byte("docs"))
			if v := bDocs.Get(storage.DocKey("blog", docID)); v == nil {
				t.Errorf("document %s not found in docs bucket", key)
			}
			return nil
		})
		if err != nil {
			t.Fatalf("DB.View failed: %v", err)
		}
	}

	// Verify revision stored for p3
	docID := genID("blog", "p3", "fr")
	revPrefix := storage.RevPrefix("blog", docID)
	revCount := 0
	err = srv.DBViewC("blog", func(tx *bolt.Tx) error {
		bRev := tx.Bucket([]byte("rev"))
		c := bRev.Cursor()
		for k, _ := c.Seek(revPrefix); k != nil && len(k) >= len(revPrefix); k, _ = c.Next() {
			if string(k[:len(revPrefix)]) != string(revPrefix) {
				break
			}
			revCount++
		}
		return nil
	})
	if err != nil {
		t.Fatalf("DB.View failed: %v", err)
	}
	if revCount != 1 {
		t.Errorf("expected 1 revision for p3, got %d", revCount)
	}
}

func TestFinalBatchProcessor_InvalidDocuments(t *testing.T) {
	srv, cleanup := newTestServerForBatch(t)
	defer cleanup()

	fbp := NewFinalBatchProcessor(srv, 4)
	docs := []*proto.BatchDocument{
		makeBatchDoc("", "en", "missing key", nil, false),
		makeBatchDoc("ok", "en", "valid", nil, false),
		makeBatchDoc("noLang", "", "missing lang", nil, false),
	}

	resp, err := fbp.ProcessBatch(context.Background(), "test", docs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Added != 1 {
		t.Errorf("expected 1 added, got %d", resp.Added)
	}
	if resp.Failed != 2 {
		t.Errorf("expected 2 failed, got %d (errors: %v)", resp.Failed, resp.Errors)
	}
}

func TestFinalBatchProcessor_UpdateExistingDocument(t *testing.T) {
	srv, cleanup := newTestServerForBatch(t)
	defer cleanup()

	fbp := NewFinalBatchProcessor(srv, 4)

	// Add first
	docs1 := []*proto.BatchDocument{
		makeBatchDoc("myKey", "en", "original content", nil, false),
	}
	resp1, err := fbp.ProcessBatch(context.Background(), "coll", docs1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp1.Added != 1 {
		t.Fatalf("expected 1 added, got %d", resp1.Added)
	}

	// Update with same key+lang
	docs2 := []*proto.BatchDocument{
		makeBatchDoc("myKey", "en", "updated content", nil, false),
	}
	resp2, err := fbp.ProcessBatch(context.Background(), "coll", docs2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp2.Updated != 1 {
		t.Errorf("expected 1 updated, got %d", resp2.Updated)
	}
	if resp2.Added != 0 {
		t.Errorf("expected 0 added on update, got %d", resp2.Added)
	}
}

func TestFinalBatchProcessor_CacheUpdate(t *testing.T) {
	srv, cleanup := newTestServerForBatch(t)
	defer cleanup()

	fbp := NewFinalBatchProcessor(srv, 4)
	docs := []*proto.BatchDocument{
		makeBatchDoc("cached", "en", "# Cached content", nil, false),
	}

	_, err := fbp.ProcessBatch(context.Background(), "blog", docs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify document was cached
	cacheKey := cache.BuildCacheKey("blog", "cached", "en")
	data, found := srv.Cache.Get(cacheKey)
	if !found {
		t.Error("document not found in cache after batch add")
	}
	if data == nil {
		t.Error("cached data is nil")
	}
}

// -----------------------------------------------------------------------
// BatchUpdater tests (batchupdate.go)
// -----------------------------------------------------------------------

func TestBatchUpdater_NewBatchUpdater_DefaultWorkers(t *testing.T) {
	srv, cleanup := newTestServerForBatch(t)
	defer cleanup()

	bu := NewBatchUpdater(srv, 0)
	if bu.maxWorkers != 8 {
		t.Errorf("expected default 8 workers, got %d", bu.maxWorkers)
	}
}

func TestBatchUpdater_EmptyBatch(t *testing.T) {
	srv, cleanup := newTestServerForBatch(t)
	defer cleanup()

	bu := NewBatchUpdater(srv, 4)
	resp, err := bu.ProcessBatchUpdate(context.Background(), "blog", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Updated != 0 || resp.NotFound != 0 || resp.Failed != 0 {
		t.Errorf("expected all zeros, got updated=%d notFound=%d failed=%d",
			resp.Updated, resp.NotFound, resp.Failed)
	}
}

func TestBatchUpdater_UpdateNonExistentDocuments(t *testing.T) {
	srv, cleanup := newTestServerForBatch(t)
	defer cleanup()

	bu := NewBatchUpdater(srv, 4)
	updates := []*proto.UpdateDocument{
		{Key: "nonexistent", Lang: "en", ContentMd: "new content"},
	}

	resp, err := bu.ProcessBatchUpdate(context.Background(), "blog", updates)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.NotFound != 1 {
		t.Errorf("expected 1 not found, got %d", resp.NotFound)
	}
	if resp.Updated != 0 {
		t.Errorf("expected 0 updated, got %d", resp.Updated)
	}
}

func TestBatchUpdater_UpdateExistingDocuments(t *testing.T) {
	srv, cleanup := newTestServerForBatch(t)
	defer cleanup()

	// First add documents using BatchProcessor
	bp := NewBatchProcessor(srv, 4)
	addDocs := []*proto.BatchDocument{
		makeBatchDoc("post1", "en", "# Original 1", nil, false),
		makeBatchDoc("post2", "en", "# Original 2", nil, false),
	}
	addResp, err := bp.ProcessBatch(context.Background(), "blog", addDocs)
	if err != nil {
		t.Fatalf("add failed: %v", err)
	}
	if addResp.Added != 2 {
		t.Fatalf("expected 2 added, got %d", addResp.Added)
	}

	// Now update them
	bu := NewBatchUpdater(srv, 4)
	updates := []*proto.UpdateDocument{
		{Key: "post1", Lang: "en", ContentMd: "# Updated 1"},
		{Key: "post2", Lang: "en", ContentMd: "# Updated 2"},
	}

	resp, err := bu.ProcessBatchUpdate(context.Background(), "blog", updates)
	if err != nil {
		t.Fatalf("update failed: %v", err)
	}
	if resp.Updated != 2 {
		t.Errorf("expected 2 updated, got %d (errors: %v)", resp.Updated, resp.Errors)
	}
	if resp.Failed != 0 {
		t.Errorf("expected 0 failed, got %d", resp.Failed)
	}

	// Verify updated content
	docID1 := genID("blog", "post1", "en")
	err = srv.DBViewC("blog", func(tx *bolt.Tx) error {
		bDocs := tx.Bucket([]byte("docs"))
		v := bDocs.Get(storage.DocKey("blog", docID1))
		if v == nil {
			t.Error("post1 not found after update")
			return nil
		}
		doc, err := unmarshalDoc(v)
		if err != nil {
			return err
		}
		if doc.ContentMD != "# Updated 1" {
			t.Errorf("expected updated content, got %q", doc.ContentMD)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("DB.View failed: %v", err)
	}
}

func TestBatchUpdater_InvalidUpdateDocuments(t *testing.T) {
	srv, cleanup := newTestServerForBatch(t)
	defer cleanup()

	bu := NewBatchUpdater(srv, 4)
	updates := []*proto.UpdateDocument{
		{Key: "", Lang: "en", ContentMd: "missing key"},
		{Key: "post", Lang: "", ContentMd: "missing lang"},
	}

	resp, err := bu.ProcessBatchUpdate(context.Background(), "blog", updates)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Failed != 2 {
		t.Errorf("expected 2 failed, got %d", resp.Failed)
	}
}

func TestBatchUpdater_MixedResults(t *testing.T) {
	srv, cleanup := newTestServerForBatch(t)
	defer cleanup()

	// Add one document
	bp := NewBatchProcessor(srv, 4)
	addDocs := []*proto.BatchDocument{
		makeBatchDoc("exists", "en", "# Exists", nil, false),
	}
	_, err := bp.ProcessBatch(context.Background(), "blog", addDocs)
	if err != nil {
		t.Fatalf("add failed: %v", err)
	}

	// Update mix: existing + nonexistent + invalid
	bu := NewBatchUpdater(srv, 4)
	updates := []*proto.UpdateDocument{
		{Key: "exists", Lang: "en", ContentMd: "# Updated"},    // should succeed
		{Key: "missing", Lang: "en", ContentMd: "# Not found"}, // not found
		{Key: "", Lang: "en", ContentMd: "# Invalid"},          // invalid
	}

	resp, err := bu.ProcessBatchUpdate(context.Background(), "blog", updates)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Updated != 1 {
		t.Errorf("expected 1 updated, got %d", resp.Updated)
	}
	if resp.NotFound != 1 {
		t.Errorf("expected 1 not found, got %d", resp.NotFound)
	}
	if resp.Failed != 1 {
		t.Errorf("expected 1 failed, got %d", resp.Failed)
	}
}

func TestBatchUpdater_WithRevision(t *testing.T) {
	srv, cleanup := newTestServerForBatch(t)
	defer cleanup()

	// Add a document
	bp := NewBatchProcessor(srv, 4)
	addDocs := []*proto.BatchDocument{
		makeBatchDoc("post1", "en", "# Version 1", nil, false),
	}
	_, err := bp.ProcessBatch(context.Background(), "blog", addDocs)
	if err != nil {
		t.Fatalf("add failed: %v", err)
	}

	// Update with revision
	bu := NewBatchUpdater(srv, 4)
	updates := []*proto.UpdateDocument{
		{Key: "post1", Lang: "en", ContentMd: "# Version 2", SaveRevision: true},
	}

	resp, err := bu.ProcessBatchUpdate(context.Background(), "blog", updates)
	if err != nil {
		t.Fatalf("update failed: %v", err)
	}
	if resp.Updated != 1 {
		t.Errorf("expected 1 updated, got %d", resp.Updated)
	}

	// Verify revision exists
	docID := genID("blog", "post1", "en")
	revPrefix := storage.RevPrefix("blog", docID)
	revCount := 0
	err = srv.DBViewC("blog", func(tx *bolt.Tx) error {
		bRev := tx.Bucket([]byte("rev"))
		c := bRev.Cursor()
		for k, _ := c.Seek(revPrefix); k != nil && len(k) >= len(revPrefix); k, _ = c.Next() {
			if string(k[:len(revPrefix)]) != string(revPrefix) {
				break
			}
			revCount++
		}
		return nil
	})
	if err != nil {
		t.Fatalf("DB.View failed: %v", err)
	}
	if revCount != 1 {
		t.Errorf("expected 1 revision, got %d", revCount)
	}
}

func TestBatchUpdater_CacheUpdate(t *testing.T) {
	srv, cleanup := newTestServerForBatch(t)
	defer cleanup()

	// Add a document
	bp := NewBatchProcessor(srv, 4)
	addDocs := []*proto.BatchDocument{
		makeBatchDoc("cacheTest", "en", "# Original", nil, false),
	}
	_, err := bp.ProcessBatch(context.Background(), "test", addDocs)
	if err != nil {
		t.Fatalf("add failed: %v", err)
	}

	// Update it
	bu := NewBatchUpdater(srv, 4)
	updates := []*proto.UpdateDocument{
		{Key: "cacheTest", Lang: "en", ContentMd: "# Updated"},
	}
	_, err = bu.ProcessBatchUpdate(context.Background(), "test", updates)
	if err != nil {
		t.Fatalf("update failed: %v", err)
	}

	// Verify cache was updated
	cacheKey := cache.BuildCacheKey("test", "cacheTest", "en")
	data, found := srv.Cache.Get(cacheKey)
	if !found {
		t.Error("updated document not in cache")
	}
	if data == nil {
		t.Error("cached data is nil")
	}
}

// -----------------------------------------------------------------------
// BatchDeleter tests (batchdelete.go)
// -----------------------------------------------------------------------

func TestBatchDeleter_NewBatchDeleter_DefaultWorkers(t *testing.T) {
	srv, cleanup := newTestServerForBatch(t)
	defer cleanup()

	bd := NewBatchDeleter(srv, 0)
	if bd.maxWorkers != 8 {
		t.Errorf("expected default 8 workers, got %d", bd.maxWorkers)
	}
}

func TestBatchDeleter_EmptyBatch(t *testing.T) {
	srv, cleanup := newTestServerForBatch(t)
	defer cleanup()

	bd := NewBatchDeleter(srv, 4)
	resp, err := bd.ProcessBatchDelete(context.Background(), "blog", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Deleted != 0 || resp.NotFound != 0 || resp.Failed != 0 {
		t.Errorf("expected all zeros, got deleted=%d notFound=%d failed=%d",
			resp.Deleted, resp.NotFound, resp.Failed)
	}
}

func TestBatchDeleter_DeleteNonExistent(t *testing.T) {
	srv, cleanup := newTestServerForBatch(t)
	defer cleanup()

	bd := NewBatchDeleter(srv, 4)
	deletes := []*proto.DeleteDocument{
		{Key: "ghost", Lang: "en"},
	}

	resp, err := bd.ProcessBatchDelete(context.Background(), "blog", deletes)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.NotFound != 1 {
		t.Errorf("expected 1 not found, got %d", resp.NotFound)
	}
	if resp.Deleted != 0 {
		t.Errorf("expected 0 deleted, got %d", resp.Deleted)
	}
}

func TestBatchDeleter_DeleteExistingDocuments(t *testing.T) {
	srv, cleanup := newTestServerForBatch(t)
	defer cleanup()

	// Add documents first
	bp := NewBatchProcessor(srv, 4)
	meta := map[string]*proto.MetaValues{
		"tag": {Values: []string{"go"}},
	}
	addDocs := []*proto.BatchDocument{
		makeBatchDoc("post1", "en", "# Post 1", meta, false),
		makeBatchDoc("post2", "en", "# Post 2", nil, false),
	}
	addResp, err := bp.ProcessBatch(context.Background(), "blog", addDocs)
	if err != nil {
		t.Fatalf("add failed: %v", err)
	}
	if addResp.Added != 2 {
		t.Fatalf("expected 2 added, got %d", addResp.Added)
	}

	// Delete them
	bd := NewBatchDeleter(srv, 4)
	deletes := []*proto.DeleteDocument{
		{Key: "post1", Lang: "en"},
		{Key: "post2", Lang: "en"},
	}

	resp, err := bd.ProcessBatchDelete(context.Background(), "blog", deletes)
	if err != nil {
		t.Fatalf("delete failed: %v", err)
	}
	if resp.Deleted != 2 {
		t.Errorf("expected 2 deleted, got %d (errors: %v)", resp.Deleted, resp.Errors)
	}
	if resp.Failed != 0 {
		t.Errorf("expected 0 failed, got %d", resp.Failed)
	}

	// Verify documents are gone
	docID1 := genID("blog", "post1", "en")
	err = srv.DBViewC("blog", func(tx *bolt.Tx) error {
		bDocs := tx.Bucket([]byte("docs"))
		if v := bDocs.Get(storage.DocKey("blog", docID1)); v != nil {
			t.Error("post1 should be deleted from docs bucket")
		}

		// Verify bykey index removed
		bByK := tx.Bucket([]byte("bykey"))
		if v := bByK.Get(storage.ByKeyKey("blog", "post1", "en")); v != nil {
			t.Error("bykey index for post1 should be removed")
		}

		// Verify metadata index removed
		bIdx := tx.Bucket([]byte("idxmeta"))
		metaKey := append(storage.MetaKeyPrefix("blog", "tag", "go"), []byte(docID1)...)
		if v := bIdx.Get(metaKey); v != nil {
			t.Error("metadata index for post1 tag=go should be removed")
		}

		return nil
	})
	if err != nil {
		t.Fatalf("DB.View failed: %v", err)
	}
}

func TestBatchDeleter_InvalidDocuments(t *testing.T) {
	srv, cleanup := newTestServerForBatch(t)
	defer cleanup()

	bd := NewBatchDeleter(srv, 4)
	deletes := []*proto.DeleteDocument{
		{Key: "", Lang: "en"},
		{Key: "post", Lang: ""},
	}

	resp, err := bd.ProcessBatchDelete(context.Background(), "blog", deletes)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Failed != 2 {
		t.Errorf("expected 2 failed, got %d", resp.Failed)
	}
}

func TestBatchDeleter_MixedResults(t *testing.T) {
	srv, cleanup := newTestServerForBatch(t)
	defer cleanup()

	// Add one document
	bp := NewBatchProcessor(srv, 4)
	addDocs := []*proto.BatchDocument{
		makeBatchDoc("real", "en", "# Real doc", nil, false),
	}
	_, err := bp.ProcessBatch(context.Background(), "blog", addDocs)
	if err != nil {
		t.Fatalf("add failed: %v", err)
	}

	// Delete mix: existing + nonexistent + invalid
	bd := NewBatchDeleter(srv, 4)
	deletes := []*proto.DeleteDocument{
		{Key: "real", Lang: "en"},  // should succeed
		{Key: "ghost", Lang: "en"}, // not found
		{Key: "", Lang: "en"},      // invalid
	}

	resp, err := bd.ProcessBatchDelete(context.Background(), "blog", deletes)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Deleted != 1 {
		t.Errorf("expected 1 deleted, got %d", resp.Deleted)
	}
	if resp.NotFound != 1 {
		t.Errorf("expected 1 not found, got %d", resp.NotFound)
	}
	if resp.Failed != 1 {
		t.Errorf("expected 1 failed, got %d", resp.Failed)
	}
}

func TestBatchDeleter_CacheInvalidation(t *testing.T) {
	srv, cleanup := newTestServerForBatch(t)
	defer cleanup()

	// Add a document (use FinalBatchProcessor to get cache populated)
	fbp := NewFinalBatchProcessor(srv, 4)
	addDocs := []*proto.BatchDocument{
		makeBatchDoc("cached", "en", "# Cached", nil, false),
	}
	_, err := fbp.ProcessBatch(context.Background(), "test", addDocs)
	if err != nil {
		t.Fatalf("add failed: %v", err)
	}

	// Verify it is in cache
	cacheKey := cache.BuildCacheKey("test", "cached", "en")
	_, found := srv.Cache.Get(cacheKey)
	if !found {
		t.Fatal("document should be in cache after add")
	}

	// Delete it
	bd := NewBatchDeleter(srv, 4)
	deletes := []*proto.DeleteDocument{
		{Key: "cached", Lang: "en"},
	}
	_, err = bd.ProcessBatchDelete(context.Background(), "test", deletes)
	if err != nil {
		t.Fatalf("delete failed: %v", err)
	}

	// Verify cache invalidated
	_, found = srv.Cache.Get(cacheKey)
	if found {
		t.Error("cache should be invalidated after delete")
	}
}

func TestBatchDeleter_DeleteWithRevisions(t *testing.T) {
	srv, cleanup := newTestServerForBatch(t)
	defer cleanup()

	// Add document with revision
	bp := NewBatchProcessor(srv, 4)
	addDocs := []*proto.BatchDocument{
		makeBatchDoc("rev-doc", "en", "# With Revision", nil, true),
	}
	_, err := bp.ProcessBatch(context.Background(), "blog", addDocs)
	if err != nil {
		t.Fatalf("add failed: %v", err)
	}

	// Verify revision exists
	docID := genID("blog", "rev-doc", "en")
	revPrefix := storage.RevPrefix("blog", docID)
	revCount := 0
	_ = srv.DBViewC("blog", func(tx *bolt.Tx) error {
		bRev := tx.Bucket([]byte("rev"))
		c := bRev.Cursor()
		for k, _ := c.Seek(revPrefix); k != nil && len(k) >= len(revPrefix); k, _ = c.Next() {
			if string(k[:len(revPrefix)]) != string(revPrefix) {
				break
			}
			revCount++
		}
		return nil
	})
	if revCount == 0 {
		t.Fatal("expected at least 1 revision before delete")
	}

	// Delete the document
	bd := NewBatchDeleter(srv, 4)
	deletes := []*proto.DeleteDocument{
		{Key: "rev-doc", Lang: "en"},
	}
	resp, err := bd.ProcessBatchDelete(context.Background(), "blog", deletes)
	if err != nil {
		t.Fatalf("delete failed: %v", err)
	}
	if resp.Deleted != 1 {
		t.Errorf("expected 1 deleted, got %d", resp.Deleted)
	}

	// Verify revisions are cleaned up
	revCount = 0
	_ = srv.DBViewC("blog", func(tx *bolt.Tx) error {
		bRev := tx.Bucket([]byte("rev"))
		c := bRev.Cursor()
		for k, _ := c.Seek(revPrefix); k != nil && len(k) >= len(revPrefix); k, _ = c.Next() {
			if string(k[:len(revPrefix)]) != string(revPrefix) {
				break
			}
			revCount++
		}
		return nil
	})
	if revCount != 0 {
		t.Errorf("expected 0 revisions after delete, got %d", revCount)
	}
}

// -----------------------------------------------------------------------
// Table-driven tests
// -----------------------------------------------------------------------

func TestBatchProcessor_TableDriven(t *testing.T) {
	tests := []struct {
		name        string
		docs        []*proto.BatchDocument
		wantAdded   int32
		wantUpdated int32
		wantFailed  int32
	}{
		{
			name:        "empty batch",
			docs:        nil,
			wantAdded:   0,
			wantUpdated: 0,
			wantFailed:  0,
		},
		{
			name: "single valid doc",
			docs: []*proto.BatchDocument{
				makeBatchDoc("k1", "en", "content", nil, false),
			},
			wantAdded:   1,
			wantUpdated: 0,
			wantFailed:  0,
		},
		{
			name: "all invalid",
			docs: []*proto.BatchDocument{
				makeBatchDoc("", "", "no key or lang", nil, false),
				makeBatchDoc("", "en", "no key", nil, false),
			},
			wantAdded:   0,
			wantUpdated: 0,
			wantFailed:  2,
		},
		{
			name: "mixed valid and invalid",
			docs: []*proto.BatchDocument{
				makeBatchDoc("good1", "en", "ok", nil, false),
				makeBatchDoc("", "en", "bad", nil, false),
				makeBatchDoc("good2", "fr", "aussi ok", nil, false),
			},
			wantAdded:   2,
			wantUpdated: 0,
			wantFailed:  1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv, cleanup := newTestServerForBatch(t)
			defer cleanup()

			bp := NewBatchProcessor(srv, 4)
			resp, err := bp.ProcessBatch(context.Background(), "test", tt.docs)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if resp.Added != tt.wantAdded {
				t.Errorf("added: got %d, want %d", resp.Added, tt.wantAdded)
			}
			if resp.Updated != tt.wantUpdated {
				t.Errorf("updated: got %d, want %d", resp.Updated, tt.wantUpdated)
			}
			if resp.Failed != tt.wantFailed {
				t.Errorf("failed: got %d, want %d (errors: %v)", resp.Failed, tt.wantFailed, resp.Errors)
			}
		})
	}
}
