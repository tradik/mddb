package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	bolt "go.etcd.io/bbolt"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"mddb/internal/cache"
	"mddb/internal/fts"
	"mddb/internal/indexqueue"
	"mddb/internal/metrics"
	"mddb/internal/schema"
	"mddb/internal/storage"
	"mddb/internal/ttl"
	"mddb/internal/vector"
	"mddb/internal/webhooks"
	pb "mddb/proto"
)

// newTestGRPCServer creates a fully-initialised GRPCServer suitable for tests.
// It opens a temp BoltDB, creates all required buckets, and wires up the
// subsystems that gRPC methods depend on (Cache, FTSIndex, IndexQueue,
// schema.SchemaManager, WebhookManager, VectorStore, VectorIndex, TTLManager, Metrics).
// The returned cleanup function must be deferred by the caller.
func newTestGRPCServer(t *testing.T) (*GRPCServer, *Server, func()) {
	t.Helper()

	dbPath := filepath.Join(t.TempDir(), "test.db")
	db, err := bolt.Open(dbPath, 0600, nil)
	if err != nil {
		t.Fatal(err)
	}

	s := &Server{
		DB:   db,
		Path: dbPath,
		Mode: ModeRW,
		BucketNames: BucketNames{
			Docs:    []byte("docs"),
			IdxMeta: []byte("idxmeta"),
			Rev:     []byte("rev"),
			ByKey:   []byte("bykey"),
		},
		Cache:         cache.NewDocumentCache(100, 60),
		LockFreeCache: cache.NewLockFreeCache(100, 60),
	}

	// Create all required buckets
	if err := s.ensureBuckets(); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}

	// Additional buckets that some methods access directly
	if err := db.Update(func(tx *bolt.Tx) error {
		for _, name := range []string{"vectors", "fts_tokens", "webhooks", "schemas", "auth_users", "auth_groups", "ttl", "fts", "ftsrev", "ttlrev"} {
			if _, err := tx.CreateBucketIfNotExists([]byte(name)); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}

	// IndexQueue
	s.IndexQueue = indexqueue.NewIndexQueue(serverIndexStore{s: s}, 2)

	// VectorStore & VectorIndex
	s.VectorStore = vector.NewVectorStore(db)
	if err := s.VectorStore.EnsureBucket(); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	s.VectorIndex = vector.NewVectorIndex()
	s.VectorIndex.SetReady()
	s.VectorSearchers = map[string]vector.VectorSearcher{
		"flat": s.VectorIndex,
	}

	// TTL
	s.TTLManager = ttl.NewTTLManager(db, serverTTLReaper{s: s})
	if err := s.TTLManager.EnsureBuckets(); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}

	// FTS
	s.FTSIndex = fts.NewFTSIndex(db)
	if err := s.FTSIndex.EnsureBuckets(); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	langReg := fts.NewLangRegistry("en")
	fts.RegisterDefaultLanguages(langReg)
	s.FTSIndex.SetLangRegistry(langReg)

	// Webhooks
	s.WebhookManager = webhooks.NewWebhookManager(db)
	if err := s.WebhookManager.EnsureBucket(); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	if err := s.WebhookManager.LoadAll(); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}

	// Schema
	s.SchemaManager = schema.NewSchemaManager(db)
	if err := s.SchemaManager.EnsureBucket(); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	if err := s.SchemaManager.LoadAll(); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}

	// Metrics (disabled)
	s.Metrics = metrics.NewMetrics(false, &serverMetricsStats{s: s})

	gs := NewGRPCServer(s)

	// s.DB, not db: a restore replaces the handle — swapDatabase closes the old
	// one and installs a new one — so closing the captured db leaves the live
	// database open. On Unix the file is removed anyway; on Windows the temp
	// directory cannot be removed and the test fails in cleanup, which is where
	// this was found (TestGRPCRestore_Success).
	cleanup := func() {
		s.IndexQueue.Shutdown()
		if s.DB != nil {
			_ = s.DB.Close()
		}
	}

	return gs, s, cleanup
}

// addDocViaGRPC is a helper that adds a document via the gRPC Add method.
func addDocViaGRPC(t *testing.T, gs *GRPCServer, coll, key, lang, content string, meta map[string]*pb.MetaValues) *pb.Document {
	t.Helper()
	doc, err := gs.Add(context.Background(), &pb.AddRequest{
		Collection: coll,
		Key:        key,
		Lang:       lang,
		ContentMd:  content,
		Meta:       meta,
	})
	if err != nil {
		t.Fatalf("addDocViaGRPC: %v", err)
	}
	return doc
}

// addDocForSearch is a helper that inserts a document directly into BoltDB
// using protobuf serialization and a simple (non-composite) docID. This works
// around the fact that the gRPC Search method uses ExtractPart(k, 2) to extract
// the docID from the key, which only returns the text up to the next pipe. Using
// a simple docID without pipes allows Search to correctly look up the doc.
func addDocForSearch(t *testing.T, s *Server, coll, docID, key, lang, content string, meta map[string][]string) {
	t.Helper()
	doc := storage.Doc{
		ID:        docID,
		Key:       key,
		Lang:      lang,
		ContentMD: content,
		Meta:      meta,
		AddedAt:   time.Now().Unix(),
		UpdatedAt: time.Now().Unix(),
	}
	buf, err := marshalDoc(&doc)
	if err != nil {
		t.Fatalf("addDocForSearch marshal: %v", err)
	}
	err = s.DB.Update(func(tx *bolt.Tx) error {
		bDocs := tx.Bucket(s.BucketNames.Docs)
		return bDocs.Put(storage.DocKey(coll, docID), buf)
	})
	if err != nil {
		t.Fatalf("addDocForSearch put: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Test: Add
// ---------------------------------------------------------------------------

func TestGRPCAdd_Success(t *testing.T) {
	gs, _, cleanup := newTestGRPCServer(t)
	defer cleanup()

	doc, err := gs.Add(context.Background(), &pb.AddRequest{
		Collection: "blog",
		Key:        "hello",
		Lang:       "en",
		ContentMd:  "# Hello World",
		Meta: map[string]*pb.MetaValues{
			"tag": {Values: []string{"go", "test"}},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if doc.Key != "hello" {
		t.Errorf("expected key=hello, got %s", doc.Key)
	}
	if doc.Lang != "en" {
		t.Errorf("expected lang=en, got %s", doc.Lang)
	}
	if doc.ContentMd != "# Hello World" {
		t.Errorf("unexpected contentMd: %s", doc.ContentMd)
	}
	if doc.AddedAt == 0 || doc.UpdatedAt == 0 {
		t.Error("expected addedAt and updatedAt to be set")
	}
	// Check meta
	if tagVals, ok := doc.Meta["tag"]; !ok || len(tagVals.Values) != 2 {
		t.Errorf("expected 2 tag values, got %+v", doc.Meta)
	}
}

func TestGRPCAdd_MissingFields(t *testing.T) {
	gs, _, cleanup := newTestGRPCServer(t)
	defer cleanup()

	tests := []struct {
		name string
		req  *pb.AddRequest
	}{
		{"missing collection", &pb.AddRequest{Key: "k", Lang: "en"}},
		{"missing key", &pb.AddRequest{Collection: "c", Lang: "en"}},
		{"missing lang", &pb.AddRequest{Collection: "c", Key: "k"}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := gs.Add(context.Background(), tc.req)
			if err == nil {
				t.Fatal("expected error for missing fields")
			}
			st, ok := status.FromError(err)
			if !ok || st.Code() != codes.InvalidArgument {
				t.Errorf("expected InvalidArgument, got %v", err)
			}
		})
	}
}

func TestGRPCAdd_ReadOnlyMode(t *testing.T) {
	gs, s, cleanup := newTestGRPCServer(t)
	defer cleanup()

	s.Mode = ModeRead
	_, err := gs.Add(context.Background(), &pb.AddRequest{
		Collection: "blog",
		Key:        "hello",
		Lang:       "en",
		ContentMd:  "content",
	})
	if err == nil {
		t.Fatal("expected error in read-only mode")
	}
	st, _ := status.FromError(err)
	if st.Code() != codes.PermissionDenied {
		t.Errorf("expected PermissionDenied, got %v", st.Code())
	}
}

func TestGRPCAdd_UpdateExistingDocument(t *testing.T) {
	gs, _, cleanup := newTestGRPCServer(t)
	defer cleanup()

	// Add first
	doc1, err := gs.Add(context.Background(), &pb.AddRequest{
		Collection: "blog",
		Key:        "post1",
		Lang:       "en",
		ContentMd:  "original",
	})
	if err != nil {
		t.Fatalf("first add: %v", err)
	}
	originalAddedAt := doc1.AddedAt

	// Small delay to ensure updatedAt differs
	time.Sleep(10 * time.Millisecond)

	// Update same doc
	doc2, err := gs.Add(context.Background(), &pb.AddRequest{
		Collection: "blog",
		Key:        "post1",
		Lang:       "en",
		ContentMd:  "updated",
	})
	if err != nil {
		t.Fatalf("second add: %v", err)
	}
	if doc2.ContentMd != "updated" {
		t.Errorf("expected updated content, got %s", doc2.ContentMd)
	}
	if doc2.AddedAt != originalAddedAt {
		t.Errorf("addedAt should be preserved; got %d, want %d", doc2.AddedAt, originalAddedAt)
	}
}

func TestGRPCAdd_WithRevision(t *testing.T) {
	gs, s, cleanup := newTestGRPCServer(t)
	defer cleanup()

	_, err := gs.Add(context.Background(), &pb.AddRequest{
		Collection:   "blog",
		Key:          "rev-doc",
		Lang:         "en",
		ContentMd:    "version 1",
		SaveRevision: true,
	})
	if err != nil {
		t.Fatalf("add with revision: %v", err)
	}

	// Check that a revision was stored in the rev bucket
	var revCount int
	_ = s.DB.View(func(tx *bolt.Tx) error {
		bRev := tx.Bucket(s.BucketNames.Rev)
		c := bRev.Cursor()
		prefix := storage.RevPrefix("blog", genID("blog", "rev-doc", "en"))
		for k, _ := c.Seek(prefix); k != nil && len(k) >= len(prefix) && string(k[:len(prefix)]) == string(prefix); k, _ = c.Next() {
			revCount++
		}
		return nil
	})
	if revCount == 0 {
		t.Error("expected at least one revision to be stored")
	}
}

// ---------------------------------------------------------------------------
// Test: Get
// ---------------------------------------------------------------------------

func TestGRPCGet_Success(t *testing.T) {
	gs, _, cleanup := newTestGRPCServer(t)
	defer cleanup()

	addDocViaGRPC(t, gs, "blog", "mypost", "en", "Hello content", nil)

	doc, err := gs.Get(context.Background(), &pb.GetRequest{
		Collection: "blog",
		Key:        "mypost",
		Lang:       "en",
	})
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if doc.ContentMd != "Hello content" {
		t.Errorf("expected 'Hello content', got %s", doc.ContentMd)
	}
}

func TestGRPCGet_NotFound(t *testing.T) {
	gs, _, cleanup := newTestGRPCServer(t)
	defer cleanup()

	_, err := gs.Get(context.Background(), &pb.GetRequest{
		Collection: "blog",
		Key:        "nonexistent",
		Lang:       "en",
	})
	if err == nil {
		t.Fatal("expected not found error")
	}
	st, _ := status.FromError(err)
	if st.Code() != codes.NotFound {
		t.Errorf("expected NotFound, got %v", st.Code())
	}
}

func TestGRPCGet_MissingFields(t *testing.T) {
	gs, _, cleanup := newTestGRPCServer(t)
	defer cleanup()

	tests := []struct {
		name string
		req  *pb.GetRequest
	}{
		{"missing collection", &pb.GetRequest{Key: "k", Lang: "en"}},
		{"missing key", &pb.GetRequest{Collection: "c", Lang: "en"}},
		{"missing lang", &pb.GetRequest{Collection: "c", Key: "k"}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := gs.Get(context.Background(), tc.req)
			if err == nil {
				t.Fatal("expected error")
			}
			st, _ := status.FromError(err)
			if st.Code() != codes.InvalidArgument {
				t.Errorf("expected InvalidArgument, got %v", st.Code())
			}
		})
	}
}

func TestGRPCGet_WithEnvSubstitution(t *testing.T) {
	gs, _, cleanup := newTestGRPCServer(t)
	defer cleanup()

	addDocViaGRPC(t, gs, "blog", "envpost", "en", "Hello %%NAME%%!", nil)

	doc, err := gs.Get(context.Background(), &pb.GetRequest{
		Collection: "blog",
		Key:        "envpost",
		Lang:       "en",
		Env:        map[string]string{"NAME": "World"},
	})
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if doc.ContentMd != "Hello World!" {
		t.Errorf("expected 'Hello World!', got %q", doc.ContentMd)
	}
}

func TestGRPCGet_CacheHit(t *testing.T) {
	gs, _, cleanup := newTestGRPCServer(t)
	defer cleanup()

	// Add a document (which populates the cache)
	addDocViaGRPC(t, gs, "blog", "cached", "en", "cached content", nil)

	// First Get (may read from DB, populate cache)
	doc1, err := gs.Get(context.Background(), &pb.GetRequest{
		Collection: "blog",
		Key:        "cached",
		Lang:       "en",
	})
	if err != nil {
		t.Fatalf("first get: %v", err)
	}

	// Second Get (should hit cache)
	doc2, err := gs.Get(context.Background(), &pb.GetRequest{
		Collection: "blog",
		Key:        "cached",
		Lang:       "en",
	})
	if err != nil {
		t.Fatalf("second get: %v", err)
	}

	if doc1.ContentMd != doc2.ContentMd {
		t.Error("cached response should be identical")
	}
}

// ---------------------------------------------------------------------------
// Test: Search
// ---------------------------------------------------------------------------

func TestGRPCSearch_NoFilter(t *testing.T) {
	gs, s, cleanup := newTestGRPCServer(t)
	defer cleanup()

	// Insert docs directly with simple docIDs so gRPC Search can find them
	addDocForSearch(t, s, "blog", "id-a", "a", "en", "aaa", nil)
	addDocForSearch(t, s, "blog", "id-b", "b", "en", "bbb", nil)
	addDocForSearch(t, s, "blog", "id-c", "c", "en", "ccc", nil)

	resp, err := gs.Search(context.Background(), &pb.SearchRequest{
		Collection: "blog",
	})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if resp.Total != 3 {
		t.Errorf("expected 3 documents, got %d", resp.Total)
	}
}

func TestGRPCSearch_MissingCollection(t *testing.T) {
	gs, _, cleanup := newTestGRPCServer(t)
	defer cleanup()

	_, err := gs.Search(context.Background(), &pb.SearchRequest{})
	if err == nil {
		t.Fatal("expected error for missing collection")
	}
	st, _ := status.FromError(err)
	if st.Code() != codes.InvalidArgument {
		t.Errorf("expected InvalidArgument, got %v", st.Code())
	}
}

func TestGRPCSearch_Pagination(t *testing.T) {
	gs, s, cleanup := newTestGRPCServer(t)
	defer cleanup()

	for i := 0; i < 10; i++ {
		id := "id" + string(rune('0'+i))
		key := "post" + string(rune('0'+i))
		addDocForSearch(t, s, "blog", id, key, "en", "content", nil)
	}

	resp, err := gs.Search(context.Background(), &pb.SearchRequest{
		Collection: "blog",
		Limit:      3,
		Offset:     0,
	})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if resp.Total != 10 {
		t.Errorf("expected total=10, got %d", resp.Total)
	}
	if len(resp.Documents) != 3 {
		t.Errorf("expected 3 documents in page, got %d", len(resp.Documents))
	}
}

func TestGRPCSearch_EmptyCollection(t *testing.T) {
	gs, _, cleanup := newTestGRPCServer(t)
	defer cleanup()

	resp, err := gs.Search(context.Background(), &pb.SearchRequest{
		Collection: "empty",
	})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if resp.Total != 0 {
		t.Errorf("expected 0 results, got %d", resp.Total)
	}
}

func TestGRPCSearch_WithMetaFilter(t *testing.T) {
	gs, s, cleanup := newTestGRPCServer(t)
	defer cleanup()

	// Insert docs with meta; also manually index metadata
	addDocForSearch(t, s, "blog", "id-p1", "p1", "en", "c1", map[string][]string{
		"category": {"tech"},
	})
	addDocForSearch(t, s, "blog", "id-p2", "p2", "en", "c2", map[string][]string{
		"category": {"cooking"},
	})
	// Manually add meta index entries so the filter path works
	_ = s.DB.Update(func(tx *bolt.Tx) error {
		bIdx := tx.Bucket(s.BucketNames.IdxMeta)
		_ = bIdx.Put(append(storage.MetaKeyPrefix("blog", "category", "tech"), []byte("id-p1")...), []byte("1"))
		_ = bIdx.Put(append(storage.MetaKeyPrefix("blog", "category", "cooking"), []byte("id-p2")...), []byte("1"))
		return nil
	})

	resp, err := gs.Search(context.Background(), &pb.SearchRequest{
		Collection: "blog",
		FilterMeta: map[string]*pb.MetaValues{
			"category": {Values: []string{"tech"}},
		},
	})
	if err != nil {
		t.Fatalf("search with filter: %v", err)
	}
	if resp.Total != 1 {
		t.Errorf("expected 1 result for tech category, got %d", resp.Total)
	}
	if len(resp.Documents) > 0 && resp.Documents[0].Key != "p1" {
		t.Errorf("expected doc p1, got %s", resp.Documents[0].Key)
	}
}

func TestGRPCSearch_SortByKey(t *testing.T) {
	gs, s, cleanup := newTestGRPCServer(t)
	defer cleanup()

	addDocForSearch(t, s, "blog", "id-cherry", "cherry", "en", "c", nil)
	addDocForSearch(t, s, "blog", "id-apple", "apple", "en", "a", nil)
	addDocForSearch(t, s, "blog", "id-banana", "banana", "en", "b", nil)

	resp, err := gs.Search(context.Background(), &pb.SearchRequest{
		Collection: "blog",
		Sort:       "key",
		Asc:        true,
	})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(resp.Documents) != 3 {
		t.Fatalf("expected 3 docs, got %d", len(resp.Documents))
	}
	if resp.Documents[0].Key != "apple" {
		t.Errorf("expected first doc to be 'apple', got %s", resp.Documents[0].Key)
	}
	if resp.Documents[2].Key != "cherry" {
		t.Errorf("expected last doc to be 'cherry', got %s", resp.Documents[2].Key)
	}
}

// ---------------------------------------------------------------------------
// Test: AddBatch
// ---------------------------------------------------------------------------

func TestGRPCAddBatch_Success(t *testing.T) {
	gs, _, cleanup := newTestGRPCServer(t)
	defer cleanup()

	resp, err := gs.AddBatch(context.Background(), &pb.AddBatchRequest{
		Collection: "blog",
		Documents: []*pb.BatchDocument{
			{Key: "b1", Lang: "en", ContentMd: "batch 1"},
			{Key: "b2", Lang: "en", ContentMd: "batch 2"},
			{Key: "b3", Lang: "en", ContentMd: "batch 3"},
		},
	})
	if err != nil {
		t.Fatalf("add batch: %v", err)
	}
	if resp.Added != 3 {
		t.Errorf("expected 3 added, got %d", resp.Added)
	}

	// Verify documents can be retrieved
	for _, key := range []string{"b1", "b2", "b3"} {
		doc, err := gs.Get(context.Background(), &pb.GetRequest{
			Collection: "blog",
			Key:        key,
			Lang:       "en",
		})
		if err != nil {
			t.Errorf("get %s: %v", key, err)
			continue
		}
		if doc.Key != key {
			t.Errorf("expected key=%s, got %s", key, doc.Key)
		}
	}
}

func TestGRPCAddBatch_MissingCollection(t *testing.T) {
	gs, _, cleanup := newTestGRPCServer(t)
	defer cleanup()

	_, err := gs.AddBatch(context.Background(), &pb.AddBatchRequest{
		Documents: []*pb.BatchDocument{
			{Key: "k", Lang: "en"},
		},
	})
	if err == nil {
		t.Fatal("expected error for missing collection")
	}
	st, _ := status.FromError(err)
	if st.Code() != codes.InvalidArgument {
		t.Errorf("expected InvalidArgument, got %v", st.Code())
	}
}

func TestGRPCAddBatch_EmptyDocuments(t *testing.T) {
	gs, _, cleanup := newTestGRPCServer(t)
	defer cleanup()

	resp, err := gs.AddBatch(context.Background(), &pb.AddBatchRequest{
		Collection: "blog",
		Documents:  []*pb.BatchDocument{},
	})
	if err != nil {
		t.Fatalf("expected no error for empty batch, got %v", err)
	}
	if resp == nil {
		t.Fatal("expected non-nil response")
	}
}

func TestGRPCAddBatch_ReadOnlyMode(t *testing.T) {
	gs, s, cleanup := newTestGRPCServer(t)
	defer cleanup()

	s.Mode = ModeRead
	_, err := gs.AddBatch(context.Background(), &pb.AddBatchRequest{
		Collection: "blog",
		Documents:  []*pb.BatchDocument{{Key: "k", Lang: "en"}},
	})
	if err == nil {
		t.Fatal("expected error in read-only mode")
	}
	st, _ := status.FromError(err)
	if st.Code() != codes.PermissionDenied {
		t.Errorf("expected PermissionDenied, got %v", st.Code())
	}
}

// ---------------------------------------------------------------------------
// Test: DeleteBatch
// ---------------------------------------------------------------------------

func TestGRPCDeleteBatch_Success(t *testing.T) {
	gs, _, cleanup := newTestGRPCServer(t)
	defer cleanup()

	// Add documents first
	addDocViaGRPC(t, gs, "blog", "d1", "en", "content1", nil)
	addDocViaGRPC(t, gs, "blog", "d2", "en", "content2", nil)

	resp, err := gs.DeleteBatch(context.Background(), &pb.DeleteBatchRequest{
		Collection: "blog",
		Documents: []*pb.DeleteDocument{
			{Key: "d1", Lang: "en"},
			{Key: "d2", Lang: "en"},
		},
	})
	if err != nil {
		t.Fatalf("delete batch: %v", err)
	}
	if resp.Deleted != 2 {
		t.Errorf("expected 2 deleted, got %d", resp.Deleted)
	}

	// Verify deleted
	_, err = gs.Get(context.Background(), &pb.GetRequest{
		Collection: "blog",
		Key:        "d1",
		Lang:       "en",
	})
	if err == nil {
		t.Error("expected not found after delete")
	}
}

func TestGRPCDeleteBatch_MissingCollection(t *testing.T) {
	gs, _, cleanup := newTestGRPCServer(t)
	defer cleanup()

	_, err := gs.DeleteBatch(context.Background(), &pb.DeleteBatchRequest{
		Documents: []*pb.DeleteDocument{{Key: "k", Lang: "en"}},
	})
	if err == nil {
		t.Fatal("expected error")
	}
	st, _ := status.FromError(err)
	if st.Code() != codes.InvalidArgument {
		t.Errorf("expected InvalidArgument, got %v", st.Code())
	}
}

func TestGRPCDeleteBatch_ReadOnlyMode(t *testing.T) {
	gs, s, cleanup := newTestGRPCServer(t)
	defer cleanup()

	s.Mode = ModeRead
	_, err := gs.DeleteBatch(context.Background(), &pb.DeleteBatchRequest{
		Collection: "blog",
		Documents:  []*pb.DeleteDocument{{Key: "k", Lang: "en"}},
	})
	if err == nil {
		t.Fatal("expected error in read-only mode")
	}
	st, _ := status.FromError(err)
	if st.Code() != codes.PermissionDenied {
		t.Errorf("expected PermissionDenied, got %v", st.Code())
	}
}

// ---------------------------------------------------------------------------
// Test: UpdateBatch
// ---------------------------------------------------------------------------

func TestGRPCUpdateBatch_MissingCollection(t *testing.T) {
	gs, _, cleanup := newTestGRPCServer(t)
	defer cleanup()

	_, err := gs.UpdateBatch(context.Background(), &pb.UpdateBatchRequest{
		Documents: []*pb.UpdateDocument{{Key: "k", Lang: "en"}},
	})
	if err == nil {
		t.Fatal("expected error")
	}
	st, _ := status.FromError(err)
	if st.Code() != codes.InvalidArgument {
		t.Errorf("expected InvalidArgument, got %v", st.Code())
	}
}

func TestGRPCUpdateBatch_ReadOnlyMode(t *testing.T) {
	gs, s, cleanup := newTestGRPCServer(t)
	defer cleanup()

	s.Mode = ModeRead
	_, err := gs.UpdateBatch(context.Background(), &pb.UpdateBatchRequest{
		Collection: "blog",
		Documents:  []*pb.UpdateDocument{{Key: "k", Lang: "en"}},
	})
	if err == nil {
		t.Fatal("expected error in read-only mode")
	}
	st, _ := status.FromError(err)
	if st.Code() != codes.PermissionDenied {
		t.Errorf("expected PermissionDenied, got %v", st.Code())
	}
}

// ---------------------------------------------------------------------------
// Test: Truncate
// ---------------------------------------------------------------------------

func TestGRPCTruncate_Success(t *testing.T) {
	gs, s, cleanup := newTestGRPCServer(t)
	defer cleanup()

	docID := genID("blog", "truncdoc", "en")

	// Manually insert revisions with distinct timestamps to guarantee
	// multiple revision keys (the gRPC Add method uses time.Now().Unix()
	// which may produce the same second across rapid calls).
	doc := storage.Doc{ID: docID, Key: "truncdoc", Lang: "en", ContentMD: "v"}
	for i := 0; i < 5; i++ {
		doc.UpdatedAt = int64(1000 + i)
		buf, err := marshalDoc(&doc)
		if err != nil {
			t.Fatal(err)
		}
		revKey := []byte("rev|blog|" + docID + "|" + string(rune('0'+i)))
		_ = s.DB.Update(func(tx *bolt.Tx) error {
			bRev := tx.Bucket(s.BucketNames.Rev)
			return bRev.Put(revKey, buf)
		})
	}

	// Also ensure a doc exists in docs bucket so Truncate can find docIDs
	docBuf, _ := marshalDoc(&doc)
	_ = s.DB.Update(func(tx *bolt.Tx) error {
		return tx.Bucket(s.BucketNames.Docs).Put(storage.DocKey("blog", docID), docBuf)
	})

	// Verify revisions exist
	var revCountBefore int
	_ = s.DB.View(func(tx *bolt.Tx) error {
		bRev := tx.Bucket(s.BucketNames.Rev)
		c := bRev.Cursor()
		prefix := storage.RevPrefix("blog", docID)
		for k, _ := c.Seek(prefix); k != nil && len(k) >= len(prefix) && string(k[:len(prefix)]) == string(prefix); k, _ = c.Next() {
			revCountBefore++
		}
		return nil
	})
	if revCountBefore != 5 {
		t.Fatalf("expected 5 revisions, got %d", revCountBefore)
	}

	// Truncate to keep last 2
	resp, err := gs.Truncate(context.Background(), &pb.TruncateRequest{
		Collection: "blog",
		KeepRevs:   2,
	})
	if err != nil {
		t.Fatalf("truncate: %v", err)
	}
	if resp.Status != "truncated" {
		t.Errorf("expected status 'truncated', got %s", resp.Status)
	}

	// Check revisions after
	var revCountAfter int
	_ = s.DB.View(func(tx *bolt.Tx) error {
		bRev := tx.Bucket(s.BucketNames.Rev)
		c := bRev.Cursor()
		prefix := storage.RevPrefix("blog", docID)
		for k, _ := c.Seek(prefix); k != nil && len(k) >= len(prefix) && string(k[:len(prefix)]) == string(prefix); k, _ = c.Next() {
			revCountAfter++
		}
		return nil
	})
	if revCountAfter > 2 {
		t.Errorf("expected at most 2 revisions after truncate, got %d", revCountAfter)
	}
}

func TestGRPCTruncate_MissingCollection(t *testing.T) {
	gs, _, cleanup := newTestGRPCServer(t)
	defer cleanup()

	_, err := gs.Truncate(context.Background(), &pb.TruncateRequest{})
	if err == nil {
		t.Fatal("expected error")
	}
	st, _ := status.FromError(err)
	if st.Code() != codes.InvalidArgument {
		t.Errorf("expected InvalidArgument, got %v", st.Code())
	}
}

func TestGRPCTruncate_ReadOnlyMode(t *testing.T) {
	gs, s, cleanup := newTestGRPCServer(t)
	defer cleanup()

	s.Mode = ModeRead
	_, err := gs.Truncate(context.Background(), &pb.TruncateRequest{
		Collection: "blog",
		KeepRevs:   1,
	})
	if err == nil {
		t.Fatal("expected error")
	}
	st, _ := status.FromError(err)
	if st.Code() != codes.PermissionDenied {
		t.Errorf("expected PermissionDenied, got %v", st.Code())
	}
}

// ---------------------------------------------------------------------------
// Test: Stats
// ---------------------------------------------------------------------------

func TestGRPCStats_EmptyDB(t *testing.T) {
	gs, _, cleanup := newTestGRPCServer(t)
	defer cleanup()

	resp, err := gs.Stats(context.Background(), &pb.StatsRequest{})
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	if resp.TotalDocuments != 0 {
		t.Errorf("expected 0 documents, got %d", resp.TotalDocuments)
	}
	if resp.Mode != "wr" {
		t.Errorf("expected mode=wr, got %s", resp.Mode)
	}
}

func TestGRPCStats_WithDocuments(t *testing.T) {
	gs, _, cleanup := newTestGRPCServer(t)
	defer cleanup()

	addDocViaGRPC(t, gs, "blog", "s1", "en", "content", nil)
	addDocViaGRPC(t, gs, "blog", "s2", "en", "content", nil)
	addDocViaGRPC(t, gs, "pages", "s3", "en", "content", nil)

	resp, err := gs.Stats(context.Background(), &pb.StatsRequest{})
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	if resp.TotalDocuments != 3 {
		t.Errorf("expected 3 documents, got %d", resp.TotalDocuments)
	}
	if len(resp.Collections) != 2 {
		t.Errorf("expected 2 collections, got %d", len(resp.Collections))
	}
}

// ---------------------------------------------------------------------------
// Test: Backup
// ---------------------------------------------------------------------------

func TestGRPCBackup_Success(t *testing.T) {
	gs, _, cleanup := newTestGRPCServer(t)
	defer cleanup()

	bdir := t.TempDir()
	t.Setenv("MDDB_BACKUP_DIR", bdir)
	resp, err := gs.Backup(context.Background(), &pb.BackupRequest{
		To: "backup.db",
	})
	if err != nil {
		t.Fatalf("backup: %v", err)
	}
	want := filepath.Join(bdir, "backup.db")
	wantResolved, _ := filepath.EvalSymlinks(bdir)
	wantResolved = filepath.Join(wantResolved, "backup.db")
	if resp.Backup != want && resp.Backup != wantResolved {
		t.Errorf("unexpected backup path %s (want %s or %s)", resp.Backup, want, wantResolved)
	}

	if _, err := os.Stat(resp.Backup); os.IsNotExist(err) {
		t.Error("backup file does not exist")
	}
}

func TestGRPCBackup_DefaultFilename(t *testing.T) {
	gs, _, cleanup := newTestGRPCServer(t)
	defer cleanup()

	resp, err := gs.Backup(context.Background(), &pb.BackupRequest{})
	if err != nil {
		t.Fatalf("backup: %v", err)
	}
	if resp.Backup == "" {
		t.Error("expected non-empty backup filename")
	}
	// Clean up the backup file
	_ = os.Remove(resp.Backup)
}

// ---------------------------------------------------------------------------
// Test: Restore
// ---------------------------------------------------------------------------

func TestGRPCRestore_MissingFilename(t *testing.T) {
	gs, _, cleanup := newTestGRPCServer(t)
	defer cleanup()

	_, err := gs.Restore(context.Background(), &pb.RestoreRequest{})
	if err == nil {
		t.Fatal("expected error for missing filename")
	}
	st, _ := status.FromError(err)
	if st.Code() != codes.InvalidArgument {
		t.Errorf("expected InvalidArgument, got %v", st.Code())
	}
}

func TestGRPCRestore_ReadOnlyMode(t *testing.T) {
	gs, s, cleanup := newTestGRPCServer(t)
	defer cleanup()

	s.Mode = ModeRead
	_, err := gs.Restore(context.Background(), &pb.RestoreRequest{
		From: "/tmp/test.db",
	})
	if err == nil {
		t.Fatal("expected error")
	}
	st, _ := status.FromError(err)
	if st.Code() != codes.PermissionDenied {
		t.Errorf("expected PermissionDenied, got %v", st.Code())
	}
}

// ---------------------------------------------------------------------------
// Test: SetTTL
// ---------------------------------------------------------------------------

func TestGRPCSetTTL_Success(t *testing.T) {
	gs, _, cleanup := newTestGRPCServer(t)
	defer cleanup()

	// Add a doc first
	addDocViaGRPC(t, gs, "blog", "ttldoc", "en", "expires soon", nil)

	doc, err := gs.SetTTL(context.Background(), &pb.SetTTLRequest{
		Collection: "blog",
		Key:        "ttldoc",
		Lang:       "en",
		Ttl:        3600, // 1 hour
	})
	if err != nil {
		t.Fatalf("set ttl: %v", err)
	}
	if doc.ExpiresAt == 0 {
		t.Error("expected expiresAt to be set")
	}
	// ExpiresAt should be roughly now + 3600
	expected := time.Now().Unix() + 3600
	if doc.ExpiresAt < expected-5 || doc.ExpiresAt > expected+5 {
		t.Errorf("expiresAt %d not close to expected %d", doc.ExpiresAt, expected)
	}
}

func TestGRPCSetTTL_ClearTTL(t *testing.T) {
	gs, _, cleanup := newTestGRPCServer(t)
	defer cleanup()

	addDocViaGRPC(t, gs, "blog", "ttlclear", "en", "content", nil)

	// Set TTL
	_, err := gs.SetTTL(context.Background(), &pb.SetTTLRequest{
		Collection: "blog",
		Key:        "ttlclear",
		Lang:       "en",
		Ttl:        3600,
	})
	if err != nil {
		t.Fatalf("set ttl: %v", err)
	}

	// Clear TTL (ttl=0)
	doc, err := gs.SetTTL(context.Background(), &pb.SetTTLRequest{
		Collection: "blog",
		Key:        "ttlclear",
		Lang:       "en",
		Ttl:        0,
	})
	if err != nil {
		t.Fatalf("clear ttl: %v", err)
	}
	if doc.ExpiresAt != 0 {
		t.Errorf("expected expiresAt=0 after clearing TTL, got %d", doc.ExpiresAt)
	}
}

func TestGRPCSetTTL_NotFound(t *testing.T) {
	gs, _, cleanup := newTestGRPCServer(t)
	defer cleanup()

	_, err := gs.SetTTL(context.Background(), &pb.SetTTLRequest{
		Collection: "blog",
		Key:        "nonexistent",
		Lang:       "en",
		Ttl:        100,
	})
	if err == nil {
		t.Fatal("expected not found error")
	}
	st, _ := status.FromError(err)
	if st.Code() != codes.NotFound {
		t.Errorf("expected NotFound, got %v", st.Code())
	}
}

func TestGRPCSetTTL_MissingFields(t *testing.T) {
	gs, _, cleanup := newTestGRPCServer(t)
	defer cleanup()

	_, err := gs.SetTTL(context.Background(), &pb.SetTTLRequest{
		Collection: "blog",
	})
	if err == nil {
		t.Fatal("expected error")
	}
	st, _ := status.FromError(err)
	if st.Code() != codes.InvalidArgument {
		t.Errorf("expected InvalidArgument, got %v", st.Code())
	}
}

func TestGRPCSetTTL_ReadOnlyMode(t *testing.T) {
	gs, s, cleanup := newTestGRPCServer(t)
	defer cleanup()

	s.Mode = ModeRead
	_, err := gs.SetTTL(context.Background(), &pb.SetTTLRequest{
		Collection: "blog",
		Key:        "k",
		Lang:       "en",
		Ttl:        100,
	})
	if err == nil {
		t.Fatal("expected error")
	}
	st, _ := status.FromError(err)
	if st.Code() != codes.PermissionDenied {
		t.Errorf("expected PermissionDenied, got %v", st.Code())
	}
}

// ---------------------------------------------------------------------------
// Test: FTS
// ---------------------------------------------------------------------------

func TestGRPCFTS_MissingFields(t *testing.T) {
	gs, _, cleanup := newTestGRPCServer(t)
	defer cleanup()

	tests := []struct {
		name string
		req  *pb.FTSRequest
	}{
		{"missing all", &pb.FTSRequest{}},
		{"missing query", &pb.FTSRequest{Collection: "blog"}},
		{"missing collection", &pb.FTSRequest{Query: "hello"}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := gs.FTS(context.Background(), tc.req)
			if err == nil {
				t.Fatal("expected error")
			}
			st, _ := status.FromError(err)
			if st.Code() != codes.InvalidArgument {
				t.Errorf("expected InvalidArgument, got %v", st.Code())
			}
		})
	}
}

func TestGRPCFTS_NoFTSIndex(t *testing.T) {
	gs, s, cleanup := newTestGRPCServer(t)
	defer cleanup()

	s.FTSIndex = nil
	_, err := gs.FTS(context.Background(), &pb.FTSRequest{
		Collection: "blog",
		Query:      "hello",
	})
	if err == nil {
		t.Fatal("expected error when FTS not initialized")
	}
	st, _ := status.FromError(err)
	if st.Code() != codes.FailedPrecondition {
		t.Errorf("expected FailedPrecondition, got %v", st.Code())
	}
}

func TestGRPCFTS_SearchWithResults(t *testing.T) {
	gs, s, cleanup := newTestGRPCServer(t)
	defer cleanup()

	// Add doc and manually index in FTS
	addDocViaGRPC(t, gs, "blog", "ftspost", "en", "golang is a great programming language", nil)

	docID := genID("blog", "ftspost", "en")
	if err := s.FTSIndex.Index("blog", docID, "golang is a great programming language"); err != nil {
		t.Fatalf("fts index: %v", err)
	}

	resp, err := gs.FTS(context.Background(), &pb.FTSRequest{
		Collection: "blog",
		Query:      "golang",
	})
	if err != nil {
		t.Fatalf("fts search: %v", err)
	}
	if resp.Total == 0 {
		t.Error("expected at least 1 FTS result")
	}
}

// ---------------------------------------------------------------------------
// Test: FTS Language Endpoints
// ---------------------------------------------------------------------------

func TestGRPCFTSLanguages(t *testing.T) {
	gs, _, cleanup := newTestGRPCServer(t)
	defer cleanup()

	resp, err := gs.FTSLanguages(context.Background(), &pb.FTSLanguagesRequest{})
	if err != nil {
		t.Fatalf("FTSLanguages: %v", err)
	}
	if resp.DefaultLang != "en" {
		t.Errorf("expected default lang 'en', got %q", resp.DefaultLang)
	}
	if len(resp.Languages) == 0 {
		t.Error("expected at least one language")
	}
	// Check that English and Polish are present
	found := map[string]bool{}
	for _, l := range resp.Languages {
		found[l.Code] = true
	}
	for _, want := range []string{"en", "pl", "de", "fr", "es"} {
		if !found[want] {
			t.Errorf("expected language %q in list", want)
		}
	}
}

func TestGRPCFTSReindex_MissingCollection(t *testing.T) {
	gs, _, cleanup := newTestGRPCServer(t)
	defer cleanup()

	_, err := gs.FTSReindex(context.Background(), &pb.FTSReindexRequest{})
	if err == nil {
		t.Fatal("expected error for missing collection")
	}
	st, _ := status.FromError(err)
	if st.Code() != codes.InvalidArgument {
		t.Errorf("expected InvalidArgument, got %v", st.Code())
	}
}

func TestGRPCFTSReindex_Success(t *testing.T) {
	gs, s, cleanup := newTestGRPCServer(t)
	defer cleanup()

	// Insert a Polish doc directly into BoltDB to avoid IndexQueue async
	addDocForSearch(t, s, "notes", "plnote1", "plnote", "pl", "programowanie jest fajne", nil)

	resp, err := gs.FTSReindex(context.Background(), &pb.FTSReindexRequest{Collection: "notes"})
	if err != nil {
		t.Fatalf("FTSReindex: %v", err)
	}
	if resp.Status != "ok" {
		t.Errorf("expected status ok, got %q", resp.Status)
	}
	if resp.Reindexed == 0 {
		t.Error("expected at least 1 reindexed document")
	}
}

func TestGRPCFTS_WithLang(t *testing.T) {
	gs, s, cleanup := newTestGRPCServer(t)
	defer cleanup()

	// Insert and index a German document directly
	addDocForSearch(t, s, "articles", "deart1", "deart", "de", "die Programmierung ist wunderbar", nil)
	_ = s.FTSIndex.IndexWithLang("articles", "deart1", "die Programmierung ist wunderbar", "de")

	resp, err := gs.FTS(context.Background(), &pb.FTSRequest{
		Collection: "articles",
		Query:      "Programmierung",
		Lang:       "de",
	})
	if err != nil {
		t.Fatalf("FTS with lang: %v", err)
	}
	if resp.Lang != "de" {
		t.Errorf("expected lang 'de' in response, got %q", resp.Lang)
	}
}

// ---------------------------------------------------------------------------
// Test: Schema CRUD
// ---------------------------------------------------------------------------

func TestGRPCSetSchema_Success(t *testing.T) {
	gs, _, cleanup := newTestGRPCServer(t)
	defer cleanup()

	schema := `{"required":["title"],"properties":{"title":{"type":"string"}}}`
	resp, err := gs.SetSchema(context.Background(), &pb.SetSchemaRequest{
		Collection: "articles",
		Schema:     schema,
	})
	if err != nil {
		t.Fatalf("set schema: %v", err)
	}
	if resp.Status != "ok" {
		t.Errorf("expected status ok, got %s", resp.Status)
	}
}

func TestGRPCSetSchema_MissingCollection(t *testing.T) {
	gs, _, cleanup := newTestGRPCServer(t)
	defer cleanup()

	_, err := gs.SetSchema(context.Background(), &pb.SetSchemaRequest{
		Schema: `{}`,
	})
	if err == nil {
		t.Fatal("expected error")
	}
	st, _ := status.FromError(err)
	if st.Code() != codes.InvalidArgument {
		t.Errorf("expected InvalidArgument, got %v", st.Code())
	}
}

func TestGRPCSetSchema_MissingSchema(t *testing.T) {
	gs, _, cleanup := newTestGRPCServer(t)
	defer cleanup()

	_, err := gs.SetSchema(context.Background(), &pb.SetSchemaRequest{
		Collection: "articles",
	})
	if err == nil {
		t.Fatal("expected error")
	}
	st, _ := status.FromError(err)
	if st.Code() != codes.InvalidArgument {
		t.Errorf("expected InvalidArgument, got %v", st.Code())
	}
}

func TestGRPCGetSchema_Success(t *testing.T) {
	gs, _, cleanup := newTestGRPCServer(t)
	defer cleanup()

	schema := `{"required":["title"],"properties":{"title":{"type":"string"}}}`
	_, _ = gs.SetSchema(context.Background(), &pb.SetSchemaRequest{
		Collection: "articles",
		Schema:     schema,
	})

	resp, err := gs.GetSchema(context.Background(), &pb.GetSchemaRequest{
		Collection: "articles",
	})
	if err != nil {
		t.Fatalf("get schema: %v", err)
	}
	if !resp.Enabled {
		t.Error("expected schema to be enabled")
	}
	if resp.Schema != schema {
		t.Errorf("expected schema %s, got %s", schema, resp.Schema)
	}
}

func TestGRPCGetSchema_NotSet(t *testing.T) {
	gs, _, cleanup := newTestGRPCServer(t)
	defer cleanup()

	resp, err := gs.GetSchema(context.Background(), &pb.GetSchemaRequest{
		Collection: "nope",
	})
	if err != nil {
		t.Fatalf("get schema: %v", err)
	}
	if resp.Enabled {
		t.Error("expected schema to not be enabled")
	}
}

func TestGRPCGetSchema_MissingCollection(t *testing.T) {
	gs, _, cleanup := newTestGRPCServer(t)
	defer cleanup()

	_, err := gs.GetSchema(context.Background(), &pb.GetSchemaRequest{})
	if err == nil {
		t.Fatal("expected error")
	}
	st, _ := status.FromError(err)
	if st.Code() != codes.InvalidArgument {
		t.Errorf("expected InvalidArgument, got %v", st.Code())
	}
}

func TestGRPCDeleteSchema_Success(t *testing.T) {
	gs, _, cleanup := newTestGRPCServer(t)
	defer cleanup()

	// Set then delete
	_, _ = gs.SetSchema(context.Background(), &pb.SetSchemaRequest{
		Collection: "articles",
		Schema:     `{"required":["title"]}`,
	})

	resp, err := gs.DeleteSchema(context.Background(), &pb.DeleteSchemaRequest{
		Collection: "articles",
	})
	if err != nil {
		t.Fatalf("delete schema: %v", err)
	}
	if resp.Status != "ok" {
		t.Errorf("expected status ok, got %s", resp.Status)
	}

	// Verify deleted
	getResp, _ := gs.GetSchema(context.Background(), &pb.GetSchemaRequest{
		Collection: "articles",
	})
	if getResp.Enabled {
		t.Error("schema should be disabled after delete")
	}
}

func TestGRPCDeleteSchema_MissingCollection(t *testing.T) {
	gs, _, cleanup := newTestGRPCServer(t)
	defer cleanup()

	_, err := gs.DeleteSchema(context.Background(), &pb.DeleteSchemaRequest{})
	if err == nil {
		t.Fatal("expected error")
	}
	st, _ := status.FromError(err)
	if st.Code() != codes.InvalidArgument {
		t.Errorf("expected InvalidArgument, got %v", st.Code())
	}
}

func TestGRPCDeleteSchema_ReadOnlyMode(t *testing.T) {
	gs, s, cleanup := newTestGRPCServer(t)
	defer cleanup()

	s.Mode = ModeRead
	_, err := gs.DeleteSchema(context.Background(), &pb.DeleteSchemaRequest{
		Collection: "articles",
	})
	if err == nil {
		t.Fatal("expected error")
	}
	st, _ := status.FromError(err)
	if st.Code() != codes.PermissionDenied {
		t.Errorf("expected PermissionDenied, got %v", st.Code())
	}
}

func TestGRPCListSchemas(t *testing.T) {
	gs, _, cleanup := newTestGRPCServer(t)
	defer cleanup()

	_, _ = gs.SetSchema(context.Background(), &pb.SetSchemaRequest{
		Collection: "a",
		Schema:     `{"required":["x"]}`,
	})
	_, _ = gs.SetSchema(context.Background(), &pb.SetSchemaRequest{
		Collection: "b",
		Schema:     `{"required":["y"]}`,
	})

	resp, err := gs.ListSchemas(context.Background(), &pb.ListSchemasRequest{})
	if err != nil {
		t.Fatalf("list schemas: %v", err)
	}
	if len(resp.Schemas) != 2 {
		t.Errorf("expected 2 schemas, got %d", len(resp.Schemas))
	}
}

// ---------------------------------------------------------------------------
// Test: ValidateDocument
// ---------------------------------------------------------------------------

func TestGRPCValidateDocument_Valid(t *testing.T) {
	gs, _, cleanup := newTestGRPCServer(t)
	defer cleanup()

	_, _ = gs.SetSchema(context.Background(), &pb.SetSchemaRequest{
		Collection: "articles",
		Schema:     `{"required":["title"],"properties":{"title":{"type":"string"}}}`,
	})

	resp, err := gs.ValidateDocument(context.Background(), &pb.ValidateDocumentRequest{
		Collection: "articles",
		Meta: map[string]*pb.MetaValues{
			"title": {Values: []string{"My Article"}},
		},
	})
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if !resp.Valid {
		t.Errorf("expected valid=true, errors=%v", resp.Errors)
	}
}

func TestGRPCValidateDocument_Invalid(t *testing.T) {
	gs, _, cleanup := newTestGRPCServer(t)
	defer cleanup()

	_, _ = gs.SetSchema(context.Background(), &pb.SetSchemaRequest{
		Collection: "articles",
		Schema:     `{"required":["title"],"properties":{"title":{"type":"string"}}}`,
	})

	resp, err := gs.ValidateDocument(context.Background(), &pb.ValidateDocumentRequest{
		Collection: "articles",
		Meta:       map[string]*pb.MetaValues{},
	})
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if resp.Valid {
		t.Error("expected valid=false for missing required field")
	}
	if len(resp.Errors) == 0 {
		t.Error("expected validation errors")
	}
}

func TestGRPCValidateDocument_NoSchema(t *testing.T) {
	gs, _, cleanup := newTestGRPCServer(t)
	defer cleanup()

	resp, err := gs.ValidateDocument(context.Background(), &pb.ValidateDocumentRequest{
		Collection: "noschem",
		Meta:       map[string]*pb.MetaValues{},
	})
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if !resp.Valid {
		t.Error("expected valid=true when no schema is set")
	}
}

func TestGRPCValidateDocument_MissingCollection(t *testing.T) {
	gs, _, cleanup := newTestGRPCServer(t)
	defer cleanup()

	_, err := gs.ValidateDocument(context.Background(), &pb.ValidateDocumentRequest{})
	if err == nil {
		t.Fatal("expected error")
	}
	st, _ := status.FromError(err)
	if st.Code() != codes.InvalidArgument {
		t.Errorf("expected InvalidArgument, got %v", st.Code())
	}
}

// ---------------------------------------------------------------------------
// Test: Add with schema validation
// ---------------------------------------------------------------------------

func TestGRPCAdd_SchemaValidation(t *testing.T) {
	gs, _, cleanup := newTestGRPCServer(t)
	defer cleanup()

	// Set a schema requiring "title"
	_, _ = gs.SetSchema(context.Background(), &pb.SetSchemaRequest{
		Collection: "strict",
		Schema:     `{"required":["title"]}`,
	})

	// Add without required meta should fail
	_, err := gs.Add(context.Background(), &pb.AddRequest{
		Collection: "strict",
		Key:        "bad",
		Lang:       "en",
		ContentMd:  "content",
		Meta:       map[string]*pb.MetaValues{},
	})
	if err == nil {
		t.Fatal("expected schema validation error")
	}
	st, _ := status.FromError(err)
	if st.Code() != codes.InvalidArgument {
		t.Errorf("expected InvalidArgument, got %v", st.Code())
	}

	// Add with required meta should succeed
	doc, err := gs.Add(context.Background(), &pb.AddRequest{
		Collection: "strict",
		Key:        "good",
		Lang:       "en",
		ContentMd:  "content",
		Meta: map[string]*pb.MetaValues{
			"title": {Values: []string{"Good Title"}},
		},
	})
	if err != nil {
		t.Fatalf("add with valid meta: %v", err)
	}
	if doc.Key != "good" {
		t.Errorf("expected key=good, got %s", doc.Key)
	}
}

// ---------------------------------------------------------------------------
// Test: Webhooks (registration without HTTP calls)
// ---------------------------------------------------------------------------

func TestGRPCRegisterWebhook_NoManager(t *testing.T) {
	gs, s, cleanup := newTestGRPCServer(t)
	defer cleanup()

	s.WebhookManager = nil
	_, err := gs.RegisterWebhook(context.Background(), &pb.RegisterWebhookRequest{
		Url:    "http://example.com/hook",
		Events: []string{"doc.added"},
	})
	if err == nil {
		t.Fatal("expected error when webhook manager not initialized")
	}
	st, _ := status.FromError(err)
	if st.Code() != codes.FailedPrecondition {
		t.Errorf("expected FailedPrecondition, got %v", st.Code())
	}
}

func TestGRPCListWebhooks_Empty(t *testing.T) {
	gs, _, cleanup := newTestGRPCServer(t)
	defer cleanup()

	resp, err := gs.ListWebhooks(context.Background(), &pb.ListWebhooksRequest{})
	if err != nil {
		t.Fatalf("list webhooks: %v", err)
	}
	if len(resp.Webhooks) != 0 {
		t.Errorf("expected 0 webhooks, got %d", len(resp.Webhooks))
	}
}

func TestGRPCRegisterAndListWebhook(t *testing.T) {
	gs, _, cleanup := newTestGRPCServer(t)
	defer cleanup()

	wh, err := gs.RegisterWebhook(context.Background(), &pb.RegisterWebhookRequest{
		Url:        "http://example.com/hook",
		Events:     []string{"doc.added", "doc.updated"},
		Collection: "blog",
	})
	if err != nil {
		t.Fatalf("register webhook: %v", err)
	}
	if wh.Id == "" {
		t.Error("expected non-empty webhook ID")
	}
	if wh.Url != "http://example.com/hook" {
		t.Errorf("expected url, got %s", wh.Url)
	}

	listResp, err := gs.ListWebhooks(context.Background(), &pb.ListWebhooksRequest{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(listResp.Webhooks) != 1 {
		t.Errorf("expected 1 webhook, got %d", len(listResp.Webhooks))
	}
}

func TestGRPCDeleteWebhook_Success(t *testing.T) {
	gs, _, cleanup := newTestGRPCServer(t)
	defer cleanup()

	wh, _ := gs.RegisterWebhook(context.Background(), &pb.RegisterWebhookRequest{
		Url:    "http://example.com/hook",
		Events: []string{"doc.added"},
	})

	resp, err := gs.DeleteWebhook(context.Background(), &pb.DeleteWebhookRequest{
		Id: wh.Id,
	})
	if err != nil {
		t.Fatalf("delete webhook: %v", err)
	}
	if resp.Status != "deleted" {
		t.Errorf("expected status deleted, got %s", resp.Status)
	}

	// Verify deleted
	listResp, _ := gs.ListWebhooks(context.Background(), &pb.ListWebhooksRequest{})
	if len(listResp.Webhooks) != 0 {
		t.Errorf("expected 0 webhooks after delete, got %d", len(listResp.Webhooks))
	}
}

func TestGRPCDeleteWebhook_MissingID(t *testing.T) {
	gs, _, cleanup := newTestGRPCServer(t)
	defer cleanup()

	_, err := gs.DeleteWebhook(context.Background(), &pb.DeleteWebhookRequest{})
	if err == nil {
		t.Fatal("expected error")
	}
	st, _ := status.FromError(err)
	if st.Code() != codes.InvalidArgument {
		t.Errorf("expected InvalidArgument, got %v", st.Code())
	}
}

// ---------------------------------------------------------------------------
// Test: VectorSearch
// ---------------------------------------------------------------------------

func TestGRPCVectorSearch_MissingCollection(t *testing.T) {
	gs, _, cleanup := newTestGRPCServer(t)
	defer cleanup()

	_, err := gs.VectorSearch(context.Background(), &pb.VectorSearchRequest{
		Query: "hello",
	})
	if err == nil {
		t.Fatal("expected error")
	}
	st, _ := status.FromError(err)
	if st.Code() != codes.InvalidArgument {
		t.Errorf("expected InvalidArgument, got %v", st.Code())
	}
}

func TestGRPCVectorSearch_MissingQueryAndVector(t *testing.T) {
	gs, _, cleanup := newTestGRPCServer(t)
	defer cleanup()

	_, err := gs.VectorSearch(context.Background(), &pb.VectorSearchRequest{
		Collection: "blog",
	})
	if err == nil {
		t.Fatal("expected error")
	}
	st, _ := status.FromError(err)
	if st.Code() != codes.InvalidArgument {
		t.Errorf("expected InvalidArgument, got %v", st.Code())
	}
}

func TestGRPCVectorSearch_IndexNotReady(t *testing.T) {
	gs, s, cleanup := newTestGRPCServer(t)
	defer cleanup()

	// Create a new index that is NOT ready
	s.VectorIndex = vector.NewVectorIndex() // ready defaults to false
	s.VectorSearchers = map[string]vector.VectorSearcher{
		"flat": s.VectorIndex,
	}

	_, err := gs.VectorSearch(context.Background(), &pb.VectorSearchRequest{
		Collection:  "blog",
		QueryVector: []float32{1.0, 0.0, 0.0},
	})
	if err == nil {
		t.Fatal("expected error")
	}
	st, _ := status.FromError(err)
	if st.Code() != codes.Unavailable {
		t.Errorf("expected Unavailable, got %v", st.Code())
	}
}

func TestGRPCVectorSearch_WithQueryVector(t *testing.T) {
	gs, s, cleanup := newTestGRPCServer(t)
	defer cleanup()

	// Add a doc
	addDocViaGRPC(t, gs, "blog", "vec1", "en", "vector content", nil)

	// Manually add a vector to the index
	docID := genID("blog", "vec1", "en")
	s.VectorIndex.Add("blog", docID, []float32{1.0, 0.0, 0.0})

	resp, err := gs.VectorSearch(context.Background(), &pb.VectorSearchRequest{
		Collection:  "blog",
		QueryVector: []float32{1.0, 0.0, 0.0},
		TopK:        5,
	})
	if err != nil {
		t.Fatalf("vector search: %v", err)
	}
	if resp.Total != 1 {
		t.Errorf("expected 1 result, got %d", resp.Total)
	}
	if len(resp.Results) > 0 && resp.Results[0].Score < 0.99 {
		t.Errorf("expected score close to 1.0, got %f", resp.Results[0].Score)
	}
}

// ---------------------------------------------------------------------------
// Test: VectorStats
// ---------------------------------------------------------------------------

func TestGRPCVectorStats_NoEmbedding(t *testing.T) {
	gs, _, cleanup := newTestGRPCServer(t)
	defer cleanup()

	resp, err := gs.VectorStats(context.Background(), &pb.VectorStatsRequest{})
	if err != nil {
		t.Fatalf("vector stats: %v", err)
	}
	if resp.Enabled {
		t.Error("expected enabled=false when no embedding provider")
	}
}

func TestGRPCVectorStats_WithDocuments(t *testing.T) {
	gs, _, cleanup := newTestGRPCServer(t)
	defer cleanup()

	addDocViaGRPC(t, gs, "blog", "vs1", "en", "content", nil)
	addDocViaGRPC(t, gs, "blog", "vs2", "en", "content", nil)

	resp, err := gs.VectorStats(context.Background(), &pb.VectorStatsRequest{})
	if err != nil {
		t.Fatalf("vector stats: %v", err)
	}
	// Should have collections from docs even if no embeddings
	if _, ok := resp.Collections["blog"]; !ok {
		t.Error("expected 'blog' in collections")
	}
}

// ---------------------------------------------------------------------------
// Test: VectorReindex
// ---------------------------------------------------------------------------

func TestGRPCVectorReindex_MissingCollection(t *testing.T) {
	gs, _, cleanup := newTestGRPCServer(t)
	defer cleanup()

	_, err := gs.VectorReindex(context.Background(), &pb.VectorReindexRequest{})
	if err == nil {
		t.Fatal("expected error")
	}
	st, _ := status.FromError(err)
	if st.Code() != codes.InvalidArgument {
		t.Errorf("expected InvalidArgument, got %v", st.Code())
	}
}

func TestGRPCVectorReindex_NoEmbeddingProvider(t *testing.T) {
	gs, _, cleanup := newTestGRPCServer(t)
	defer cleanup()

	_, err := gs.VectorReindex(context.Background(), &pb.VectorReindexRequest{
		Collection: "blog",
	})
	if err == nil {
		t.Fatal("expected error")
	}
	st, _ := status.FromError(err)
	if st.Code() != codes.FailedPrecondition {
		t.Errorf("expected FailedPrecondition, got %v", st.Code())
	}
}

func TestGRPCVectorReindex_ReadOnlyMode(t *testing.T) {
	gs, s, cleanup := newTestGRPCServer(t)
	defer cleanup()

	s.Mode = ModeRead
	_, err := gs.VectorReindex(context.Background(), &pb.VectorReindexRequest{
		Collection: "blog",
	})
	if err == nil {
		t.Fatal("expected error")
	}
	st, _ := status.FromError(err)
	if st.Code() != codes.PermissionDenied {
		t.Errorf("expected PermissionDenied, got %v", st.Code())
	}
}

// ---------------------------------------------------------------------------
// Test: ImportURL
// ---------------------------------------------------------------------------

func TestGRPCImportURL_MissingFields(t *testing.T) {
	gs, _, cleanup := newTestGRPCServer(t)
	defer cleanup()

	tests := []struct {
		name string
		req  *pb.ImportURLRequest
	}{
		{"missing collection", &pb.ImportURLRequest{Url: "http://example.com/test.md", Lang: "en"}},
		{"missing url", &pb.ImportURLRequest{Collection: "blog", Lang: "en"}},
		{"missing lang", &pb.ImportURLRequest{Collection: "blog", Url: "http://example.com/test.md"}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := gs.ImportURL(context.Background(), tc.req)
			if err == nil {
				t.Fatal("expected error")
			}
			st, _ := status.FromError(err)
			if st.Code() != codes.InvalidArgument {
				t.Errorf("expected InvalidArgument, got %v", st.Code())
			}
		})
	}
}

func TestGRPCImportURL_ReadOnlyMode(t *testing.T) {
	gs, s, cleanup := newTestGRPCServer(t)
	defer cleanup()

	s.Mode = ModeRead
	_, err := gs.ImportURL(context.Background(), &pb.ImportURLRequest{
		Collection: "blog",
		Url:        "http://example.com/test.md",
		Lang:       "en",
	})
	if err == nil {
		t.Fatal("expected error")
	}
	st, _ := status.FromError(err)
	if st.Code() != codes.PermissionDenied {
		t.Errorf("expected PermissionDenied, got %v", st.Code())
	}
}

// ---------------------------------------------------------------------------
// Test: Export
// ---------------------------------------------------------------------------

func TestGRPCExport_Unimplemented(t *testing.T) {
	gs, _, cleanup := newTestGRPCServer(t)
	defer cleanup()

	err := gs.Export(&pb.ExportRequest{Collection: "blog"}, nil)
	if err == nil {
		t.Fatal("expected error")
	}
	st, _ := status.FromError(err)
	if st.Code() != codes.Unimplemented {
		t.Errorf("expected Unimplemented, got %v", st.Code())
	}
}

// ---------------------------------------------------------------------------
// Test: docToProto helper
// ---------------------------------------------------------------------------

func TestDocToProto(t *testing.T) {
	doc := &storage.Doc{
		ID:        "test-id",
		Key:       "mykey",
		Lang:      "en",
		ContentMD: "# Hello",
		AddedAt:   1000,
		UpdatedAt: 2000,
		ExpiresAt: 3000,
		Meta: map[string][]string{
			"tag":    {"go", "grpc"},
			"author": {"alice"},
		},
	}

	pb := docToProto(doc)

	if pb.Id != "test-id" {
		t.Errorf("expected id=test-id, got %s", pb.Id)
	}
	if pb.Key != "mykey" {
		t.Errorf("expected key=mykey, got %s", pb.Key)
	}
	if pb.Lang != "en" {
		t.Errorf("expected lang=en, got %s", pb.Lang)
	}
	if pb.ContentMd != "# Hello" {
		t.Errorf("unexpected content: %s", pb.ContentMd)
	}
	if pb.AddedAt != 1000 {
		t.Errorf("expected addedAt=1000, got %d", pb.AddedAt)
	}
	if pb.UpdatedAt != 2000 {
		t.Errorf("expected updatedAt=2000, got %d", pb.UpdatedAt)
	}
	if pb.ExpiresAt != 3000 {
		t.Errorf("expected expiresAt=3000, got %d", pb.ExpiresAt)
	}
	if tags, ok := pb.Meta["tag"]; !ok || len(tags.Values) != 2 {
		t.Errorf("expected 2 tag values, got %+v", pb.Meta)
	}
	if authors, ok := pb.Meta["author"]; !ok || authors.Values[0] != "alice" {
		t.Errorf("expected author=alice, got %+v", pb.Meta)
	}
}

func TestDocToProto_NilMeta(t *testing.T) {
	doc := &storage.Doc{
		ID:   "test",
		Key:  "k",
		Lang: "en",
		Meta: nil,
	}
	p := docToProto(doc)
	if p.Meta == nil {
		t.Error("expected empty meta map, not nil")
	}
}

// ---------------------------------------------------------------------------
// Test: NewGRPCServer
// ---------------------------------------------------------------------------

func TestNewGRPCServer(t *testing.T) {
	gs, _, cleanup := newTestGRPCServer(t)
	defer cleanup()

	if gs == nil {
		t.Fatal("expected non-nil GRPCServer")
		return
	}
	if gs.server == nil {
		t.Error("expected non-nil server")
	}
	if gs.batchProcessor == nil {
		t.Error("expected non-nil batch processor")
	}
	if gs.batchDeleter == nil {
		t.Error("expected non-nil batch deleter")
	}
	if gs.batchUpdater == nil {
		t.Error("expected non-nil batch updater")
	}
}

// ---------------------------------------------------------------------------
// Test: SetSchema + Add integration (schema enforced on add)
// ---------------------------------------------------------------------------

func TestGRPCSetSchema_ReadOnlyMode(t *testing.T) {
	gs, s, cleanup := newTestGRPCServer(t)
	defer cleanup()

	s.Mode = ModeRead
	_, err := gs.SetSchema(context.Background(), &pb.SetSchemaRequest{
		Collection: "articles",
		Schema:     `{}`,
	})
	if err == nil {
		t.Fatal("expected error")
	}
	st, _ := status.FromError(err)
	if st.Code() != codes.PermissionDenied {
		t.Errorf("expected PermissionDenied, got %v", st.Code())
	}
}

// ---------------------------------------------------------------------------
// Test: RegisterWebhook + DeleteWebhook flow
// ---------------------------------------------------------------------------

func TestGRPCRegisterWebhook_ReadOnlyMode(t *testing.T) {
	gs, s, cleanup := newTestGRPCServer(t)
	defer cleanup()

	s.Mode = ModeRead
	_, err := gs.RegisterWebhook(context.Background(), &pb.RegisterWebhookRequest{
		Url:    "http://example.com/hook",
		Events: []string{"doc.added"},
	})
	if err == nil {
		t.Fatal("expected error")
	}
	st, _ := status.FromError(err)
	if st.Code() != codes.PermissionDenied {
		t.Errorf("expected PermissionDenied, got %v", st.Code())
	}
}

func TestGRPCDeleteWebhook_ReadOnlyMode(t *testing.T) {
	gs, s, cleanup := newTestGRPCServer(t)
	defer cleanup()

	s.Mode = ModeRead
	_, err := gs.DeleteWebhook(context.Background(), &pb.DeleteWebhookRequest{
		Id: "some-id",
	})
	if err == nil {
		t.Fatal("expected error")
	}
	st, _ := status.FromError(err)
	if st.Code() != codes.PermissionDenied {
		t.Errorf("expected PermissionDenied, got %v", st.Code())
	}
}

// ---------------------------------------------------------------------------
// Test: Multiple collections isolation
// ---------------------------------------------------------------------------

func TestGRPCSearch_CollectionIsolation(t *testing.T) {
	gs, s, cleanup := newTestGRPCServer(t)
	defer cleanup()

	addDocForSearch(t, s, "blog", "id-blog-x", "x", "en", "blog doc", nil)
	addDocForSearch(t, s, "pages", "id-pages-x", "x", "en", "pages doc", nil)

	blogResp, err := gs.Search(context.Background(), &pb.SearchRequest{Collection: "blog"})
	if err != nil {
		t.Fatalf("search blog: %v", err)
	}
	if blogResp.Total != 1 {
		t.Errorf("expected 1 blog doc, got %d", blogResp.Total)
	}

	pagesResp, err := gs.Search(context.Background(), &pb.SearchRequest{Collection: "pages"})
	if err != nil {
		t.Fatalf("search pages: %v", err)
	}
	if pagesResp.Total != 1 {
		t.Errorf("expected 1 pages doc, got %d", pagesResp.Total)
	}
}

// ---------------------------------------------------------------------------
// Test: Add + Get roundtrip with complex meta
// ---------------------------------------------------------------------------

func TestGRPCAddGet_ComplexMeta(t *testing.T) {
	gs, _, cleanup := newTestGRPCServer(t)
	defer cleanup()

	meta := map[string]*pb.MetaValues{
		"tags":     {Values: []string{"go", "grpc", "testing"}},
		"author":   {Values: []string{"alice"}},
		"category": {Values: []string{"tech", "backend"}},
	}
	addDocViaGRPC(t, gs, "blog", "complex", "en", "Complex meta doc", meta)

	doc, err := gs.Get(context.Background(), &pb.GetRequest{
		Collection: "blog",
		Key:        "complex",
		Lang:       "en",
	})
	if err != nil {
		t.Fatalf("get: %v", err)
	}

	// Verify all meta preserved
	for key, expected := range meta {
		got, ok := doc.Meta[key]
		if !ok {
			t.Errorf("missing meta key %s", key)
			continue
		}
		if len(got.Values) != len(expected.Values) {
			t.Errorf("meta %s: expected %d values, got %d", key, len(expected.Values), len(got.Values))
		}
	}
}

// ---------------------------------------------------------------------------
// Test: Add + Get with different languages
// ---------------------------------------------------------------------------

func TestGRPCAddGet_MultiLanguage(t *testing.T) {
	gs, _, cleanup := newTestGRPCServer(t)
	defer cleanup()

	addDocViaGRPC(t, gs, "blog", "hello", "en", "Hello World", nil)
	addDocViaGRPC(t, gs, "blog", "hello", "de", "Hallo Welt", nil)
	addDocViaGRPC(t, gs, "blog", "hello", "fr", "Bonjour le Monde", nil)

	tests := []struct {
		lang    string
		content string
	}{
		{"en", "Hello World"},
		{"de", "Hallo Welt"},
		{"fr", "Bonjour le Monde"},
	}

	for _, tc := range tests {
		t.Run(tc.lang, func(t *testing.T) {
			doc, err := gs.Get(context.Background(), &pb.GetRequest{
				Collection: "blog",
				Key:        "hello",
				Lang:       tc.lang,
			})
			if err != nil {
				t.Fatalf("get %s: %v", tc.lang, err)
			}
			if doc.ContentMd != tc.content {
				t.Errorf("expected %q, got %q", tc.content, doc.ContentMd)
			}
		})
	}
}
