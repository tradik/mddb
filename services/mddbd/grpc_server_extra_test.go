package main

import (
	"context"
	"strings"
	"testing"

	bolt "go.etcd.io/bbolt"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"mddb/internal/storage"
	pb "mddb/proto"
)

// ---------------------------------------------------------------------------
// Test: VectorSearch with query vector and filter (covers filterMeta path)
// ---------------------------------------------------------------------------

func TestGRPCVectorSearch_WithFilter(t *testing.T) {
	gs, s, cleanup := newTestGRPCServer(t)
	defer cleanup()

	// Add documents with meta
	addDocViaGRPC(t, gs, "blog", "vf1", "en", "vector filter content 1", map[string]*pb.MetaValues{
		"category": {Values: []string{"tech"}},
	})
	addDocViaGRPC(t, gs, "blog", "vf2", "en", "vector filter content 2", map[string]*pb.MetaValues{
		"category": {Values: []string{"cooking"}},
	})

	// Manually add vectors
	docID1 := genID("blog", "vf1", "en")
	docID2 := genID("blog", "vf2", "en")
	if err := s.VectorIndex.Add("blog", docID1, []float32{1.0, 0.0, 0.0}); err != nil {
		t.Fatal(err)
	}
	if err := s.VectorIndex.Add("blog", docID2, []float32{0.0, 1.0, 0.0}); err != nil {
		t.Fatal(err)
	}

	// Manually add meta index entries so filter works
	_ = s.DB.Update(func(tx *bolt.Tx) error {
		bIdx := tx.Bucket(s.BucketNames.IdxMeta)
		_ = bIdx.Put(append(storage.MetaKeyPrefix("blog", "category", "tech"), []byte(docID1)...), []byte("1"))
		_ = bIdx.Put(append(storage.MetaKeyPrefix("blog", "category", "cooking"), []byte(docID2)...), []byte("1"))
		return nil
	})

	resp, err := gs.VectorSearch(context.Background(), &pb.VectorSearchRequest{
		Collection:  "blog",
		QueryVector: []float32{1.0, 0.0, 0.0},
		TopK:        5,
		FilterMeta: map[string]*pb.MetaValues{
			"category": {Values: []string{"tech"}},
		},
	})
	if err != nil {
		t.Fatalf("vector search with filter: %v", err)
	}
	// Should find only the tech doc
	if resp.Total != 1 {
		t.Errorf("expected 1 result with filter, got %d", resp.Total)
	}
}

// Test VectorSearch with no embedding provider and text query
func TestGRPCVectorSearch_NoEmbeddingProvider(t *testing.T) {
	gs, s, cleanup := newTestGRPCServer(t)
	defer cleanup()

	s.Embedding = nil

	_, err := gs.VectorSearch(context.Background(), &pb.VectorSearchRequest{
		Collection: "blog",
		Query:      "hello world",
	})
	if err == nil {
		t.Fatal("expected error when no embedding provider")
	}
	st, _ := status.FromError(err)
	if st.Code() != codes.FailedPrecondition {
		t.Errorf("expected FailedPrecondition, got %v", st.Code())
	}
}

// Test VectorSearch with default topK
func TestGRPCVectorSearch_DefaultTopK(t *testing.T) {
	gs, _, cleanup := newTestGRPCServer(t)
	defer cleanup()

	resp, err := gs.VectorSearch(context.Background(), &pb.VectorSearchRequest{
		Collection:  "blog",
		QueryVector: []float32{1.0, 0.0, 0.0},
		TopK:        0, // should default to 5
	})
	if err != nil {
		t.Fatalf("vector search: %v", err)
	}
	// Empty collection, should get 0 results
	if resp.Total != 0 {
		t.Errorf("expected 0 results for empty collection, got %d", resp.Total)
	}
}

// Test VectorSearch with includeContent=false
func TestGRPCVectorSearch_ExcludeContent(t *testing.T) {
	gs, s, cleanup := newTestGRPCServer(t)
	defer cleanup()

	addDocViaGRPC(t, gs, "blog", "excl1", "en", "some content to exclude", nil)
	docID := genID("blog", "excl1", "en")
	if err := s.VectorIndex.Add("blog", docID, []float32{1.0, 0.0, 0.0}); err != nil {
		t.Fatal(err)
	}

	resp, err := gs.VectorSearch(context.Background(), &pb.VectorSearchRequest{
		Collection:     "blog",
		QueryVector:    []float32{1.0, 0.0, 0.0},
		TopK:           5,
		IncludeContent: false,
	})
	if err != nil {
		t.Fatalf("vector search: %v", err)
	}
	if resp.Total == 0 {
		t.Fatal("expected at least 1 result")
	}
	// Content should be empty
	if resp.Results[0].Document.ContentMd != "" {
		t.Errorf("expected empty content when IncludeContent=false, got %q", resp.Results[0].Document.ContentMd)
	}
}

// Test VectorSearch with includeContent=true
func TestGRPCVectorSearch_IncludeContent(t *testing.T) {
	gs, s, cleanup := newTestGRPCServer(t)
	defer cleanup()

	addDocViaGRPC(t, gs, "blog", "incl1", "en", "included content", nil)
	docID := genID("blog", "incl1", "en")
	if err := s.VectorIndex.Add("blog", docID, []float32{1.0, 0.0, 0.0}); err != nil {
		t.Fatal(err)
	}

	resp, err := gs.VectorSearch(context.Background(), &pb.VectorSearchRequest{
		Collection:     "blog",
		QueryVector:    []float32{1.0, 0.0, 0.0},
		TopK:           5,
		IncludeContent: true,
	})
	if err != nil {
		t.Fatalf("vector search: %v", err)
	}
	if resp.Total == 0 {
		t.Fatal("expected at least 1 result")
	}
	if resp.Results[0].Document.ContentMd != "included content" {
		t.Errorf("expected content 'included content', got %q", resp.Results[0].Document.ContentMd)
	}
}

// ---------------------------------------------------------------------------
// Test: Search with offset beyond total
// ---------------------------------------------------------------------------

func TestGRPCSearch_OffsetBeyondTotal(t *testing.T) {
	gs, s, cleanup := newTestGRPCServer(t)
	defer cleanup()

	addDocForSearch(t, s, "blog", "id-one", "one", "en", "content", nil)

	resp, err := gs.Search(context.Background(), &pb.SearchRequest{
		Collection: "blog",
		Offset:     100,
		Limit:      10,
	})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if resp.Total != 1 {
		t.Errorf("expected total=1, got %d", resp.Total)
	}
	if len(resp.Documents) != 0 {
		t.Errorf("expected 0 documents when offset>total, got %d", len(resp.Documents))
	}
}

// ---------------------------------------------------------------------------
// Test: Search with ascending/descending sort
// ---------------------------------------------------------------------------

func TestGRPCSearch_SortDesc(t *testing.T) {
	gs, s, cleanup := newTestGRPCServer(t)
	defer cleanup()

	addDocForSearch(t, s, "blog", "id-za", "za", "en", "c", nil)
	addDocForSearch(t, s, "blog", "id-aa", "aa", "en", "a", nil)
	addDocForSearch(t, s, "blog", "id-ma", "ma", "en", "b", nil)

	resp, err := gs.Search(context.Background(), &pb.SearchRequest{
		Collection: "blog",
		Sort:       "key",
		Asc:        false,
	})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(resp.Documents) != 3 {
		t.Fatalf("expected 3 docs, got %d", len(resp.Documents))
	}
	if resp.Documents[0].Key != "za" {
		t.Errorf("expected first doc to be 'za' (desc), got %s", resp.Documents[0].Key)
	}
	if resp.Documents[2].Key != "aa" {
		t.Errorf("expected last doc to be 'aa' (desc), got %s", resp.Documents[2].Key)
	}
}

// ---------------------------------------------------------------------------
// Test: Restore with valid backup file
// ---------------------------------------------------------------------------

func TestGRPCRestore_Success(t *testing.T) {
	gs, _, cleanup := newTestGRPCServer(t)
	defer cleanup()

	t.Setenv("MDDB_BACKUP_DIR", t.TempDir())

	_, err := gs.Backup(context.Background(), &pb.BackupRequest{To: "restore-test.db"})
	if err != nil {
		t.Fatalf("backup: %v", err)
	}

	resp, err := gs.Restore(context.Background(), &pb.RestoreRequest{From: "restore-test.db"})
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	if !strings.HasSuffix(resp.Restored, "restore-test.db") {
		t.Errorf("unexpected restored path: %s", resp.Restored)
	}
}

// ---------------------------------------------------------------------------
// Test: Restore with nonexistent file
// ---------------------------------------------------------------------------

func TestGRPCRestore_NonexistentFile(t *testing.T) {
	gs, _, cleanup := newTestGRPCServer(t)
	defer cleanup()

	t.Setenv("MDDB_BACKUP_DIR", t.TempDir())

	_, err := gs.Restore(context.Background(), &pb.RestoreRequest{From: "nonexistent.db"})
	if err == nil {
		t.Fatal("expected error for nonexistent file")
	}
	st, _ := status.FromError(err)
	if st.Code() != codes.InvalidArgument {
		t.Errorf("expected InvalidArgument, got %v", st.Code())
	}
}

// ---------------------------------------------------------------------------
// Test: Stats with revisions and meta indices
// ---------------------------------------------------------------------------

func TestGRPCStats_WithRevisionsAndMeta(t *testing.T) {
	gs, s, cleanup := newTestGRPCServer(t)
	defer cleanup()

	// Add a doc with revision
	_, err := gs.Add(context.Background(), &pb.AddRequest{
		Collection:   "blog",
		Key:          "statpost",
		Lang:         "en",
		ContentMd:    "stats test",
		SaveRevision: true,
		Meta: map[string]*pb.MetaValues{
			"tag": {Values: []string{"stats"}},
		},
	})
	if err != nil {
		t.Fatalf("add: %v", err)
	}

	// Manually add a meta index entry
	docID := genID("blog", "statpost", "en")
	_ = s.DB.Update(func(tx *bolt.Tx) error {
		bIdx := tx.Bucket(s.BucketNames.IdxMeta)
		return bIdx.Put(append(storage.MetaKeyPrefix("blog", "tag", "stats"), []byte(docID)...), []byte("1"))
	})

	resp, err := gs.Stats(context.Background(), &pb.StatsRequest{})
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	if resp.TotalDocuments < 1 {
		t.Errorf("expected at least 1 document, got %d", resp.TotalDocuments)
	}
	if resp.TotalRevisions < 1 {
		t.Errorf("expected at least 1 revision, got %d", resp.TotalRevisions)
	}
	if resp.TotalMetaIndices < 1 {
		t.Errorf("expected at least 1 meta index, got %d", resp.TotalMetaIndices)
	}
	if resp.DatabaseSize <= 0 {
		t.Errorf("expected database size > 0, got %d", resp.DatabaseSize)
	}
}

// ---------------------------------------------------------------------------
// Test: FTS with limit
// ---------------------------------------------------------------------------

func TestGRPCFTS_CustomLimit(t *testing.T) {
	gs, s, cleanup := newTestGRPCServer(t)
	defer cleanup()

	// Add docs and index them in FTS
	for i := 0; i < 5; i++ {
		key := "ftslimit" + string(rune('a'+i))
		addDocViaGRPC(t, gs, "blog", key, "en", "golang programming language test", nil)
		docID := genID("blog", key, "en")
		_ = s.FTSIndex.Index("blog", docID, "golang programming language test")
	}

	resp, err := gs.FTS(context.Background(), &pb.FTSRequest{
		Collection: "blog",
		Query:      "golang",
		Limit:      2,
	})
	if err != nil {
		t.Fatalf("fts: %v", err)
	}
	if resp.Total > 2 {
		t.Errorf("expected at most 2 results with limit=2, got %d", resp.Total)
	}
}

// ---------------------------------------------------------------------------
// Test: Webhook read-only checks for ListWebhooks / DeleteWebhook without manager
// ---------------------------------------------------------------------------

func TestGRPCListWebhooks_NoManager(t *testing.T) {
	gs, s, cleanup := newTestGRPCServer(t)
	defer cleanup()

	s.WebhookManager = nil
	_, err := gs.ListWebhooks(context.Background(), &pb.ListWebhooksRequest{})
	if err == nil {
		t.Fatal("expected error when webhook manager nil")
	}
	st, _ := status.FromError(err)
	if st.Code() != codes.FailedPrecondition {
		t.Errorf("expected FailedPrecondition, got %v", st.Code())
	}
}

func TestGRPCDeleteWebhook_NoManager(t *testing.T) {
	gs, s, cleanup := newTestGRPCServer(t)
	defer cleanup()

	s.WebhookManager = nil
	_, err := gs.DeleteWebhook(context.Background(), &pb.DeleteWebhookRequest{Id: "some"})
	if err == nil {
		t.Fatal("expected error when webhook manager nil")
	}
	st, _ := status.FromError(err)
	if st.Code() != codes.FailedPrecondition {
		t.Errorf("expected FailedPrecondition, got %v", st.Code())
	}
}

// ---------------------------------------------------------------------------
// Test: Webhook registration with invalid events
// ---------------------------------------------------------------------------

func TestGRPCRegisterWebhook_InvalidEvent(t *testing.T) {
	gs, _, cleanup := newTestGRPCServer(t)
	defer cleanup()

	_, err := gs.RegisterWebhook(context.Background(), &pb.RegisterWebhookRequest{
		Url:    "http://example.com/hook",
		Events: []string{"invalid.event"},
	})
	if err == nil {
		t.Fatal("expected error for invalid event")
	}
	st, _ := status.FromError(err)
	if st.Code() != codes.InvalidArgument {
		t.Errorf("expected InvalidArgument, got %v", st.Code())
	}
}

// ---------------------------------------------------------------------------
// Test: SetSchema invalid JSON
// ---------------------------------------------------------------------------

func TestGRPCSetSchema_InvalidJSON(t *testing.T) {
	gs, _, cleanup := newTestGRPCServer(t)
	defer cleanup()

	_, err := gs.SetSchema(context.Background(), &pb.SetSchemaRequest{
		Collection: "articles",
		Schema:     "not valid json",
	})
	if err == nil {
		t.Fatal("expected error for invalid JSON schema")
	}
	st, _ := status.FromError(err)
	if st.Code() != codes.InvalidArgument {
		t.Errorf("expected InvalidArgument, got %v", st.Code())
	}
}

// ---------------------------------------------------------------------------
// Test: VectorStats with embedded documents
// ---------------------------------------------------------------------------

func TestGRPCVectorStats_WithVectors(t *testing.T) {
	gs, s, cleanup := newTestGRPCServer(t)
	defer cleanup()

	addDocViaGRPC(t, gs, "blog", "vstats1", "en", "content", nil)
	addDocViaGRPC(t, gs, "blog", "vstats2", "en", "content", nil)

	// Add vectors to the store
	docID1 := genID("blog", "vstats1", "en")
	docID2 := genID("blog", "vstats2", "en")
	_ = s.VectorStore.Put("blog", docID1, []float32{1.0, 0.0}, "test-model", "hash1")
	_ = s.VectorStore.Put("blog", docID2, []float32{0.0, 1.0}, "test-model", "hash2")

	resp, err := gs.VectorStats(context.Background(), &pb.VectorStatsRequest{})
	if err != nil {
		t.Fatalf("vector stats: %v", err)
	}
	blogStats, ok := resp.Collections["blog"]
	if !ok {
		t.Fatal("expected blog collection in vector stats")
	}
	if blogStats.EmbeddedDocuments != 2 {
		t.Errorf("expected 2 embedded documents, got %d", blogStats.EmbeddedDocuments)
	}
	if blogStats.TotalDocuments != 2 {
		t.Errorf("expected 2 total documents, got %d", blogStats.TotalDocuments)
	}
}

// ---------------------------------------------------------------------------
// Test: UpdateBatch success
// ---------------------------------------------------------------------------

func TestGRPCUpdateBatch_Success(t *testing.T) {
	gs, _, cleanup := newTestGRPCServer(t)
	defer cleanup()

	// Add docs first
	addDocViaGRPC(t, gs, "blog", "ub1", "en", "original1", nil)
	addDocViaGRPC(t, gs, "blog", "ub2", "en", "original2", nil)

	resp, err := gs.UpdateBatch(context.Background(), &pb.UpdateBatchRequest{
		Collection: "blog",
		Documents: []*pb.UpdateDocument{
			{Key: "ub1", Lang: "en", ContentMd: "updated1"},
			{Key: "ub2", Lang: "en", ContentMd: "updated2"},
		},
	})
	if err != nil {
		t.Fatalf("update batch: %v", err)
	}
	if resp.Updated < 2 {
		t.Errorf("expected 2 updated, got %d", resp.Updated)
	}
}

// ---------------------------------------------------------------------------
// Test: Truncate read-only for completeness of coverage
// ---------------------------------------------------------------------------

func TestGRPCTruncate_EmptyCollection(t *testing.T) {
	gs, _, cleanup := newTestGRPCServer(t)
	defer cleanup()

	// Truncate a collection with no docs - should succeed without errors
	resp, err := gs.Truncate(context.Background(), &pb.TruncateRequest{
		Collection: "empty-coll",
		KeepRevs:   1,
	})
	if err != nil {
		t.Fatalf("truncate empty collection: %v", err)
	}
	if resp.Status != "truncated" {
		t.Errorf("expected status 'truncated', got %s", resp.Status)
	}
}

// ---------------------------------------------------------------------------
// Test: Export with nil stream
// ---------------------------------------------------------------------------

func TestGRPCExport_ReturnsUnimplemented(t *testing.T) {
	gs, _, cleanup := newTestGRPCServer(t)
	defer cleanup()

	err := gs.Export(&pb.ExportRequest{}, nil)
	if err == nil {
		t.Fatal("expected error")
	}
	st, _ := status.FromError(err)
	if st.Code() != codes.Unimplemented {
		t.Errorf("expected Unimplemented, got %v", st.Code())
	}
}

// ---------------------------------------------------------------------------
// Test: NewGRPCServer with extreme mode
// ---------------------------------------------------------------------------

func TestNewGRPCServer_ExtremeMode(t *testing.T) {
	gs, s, cleanup := newTestGRPCServer(t)
	defer cleanup()

	s.UseExtreme = true
	gsExtreme := NewGRPCServer(s)
	if gsExtreme == nil {
		t.Fatal("expected non-nil GRPCServer in extreme mode")
	}
	if s.finalBatchProcessor == nil {
		t.Error("expected finalBatchProcessor to be set in extreme mode")
	}
	_ = gs // keep linter happy
}

// ---------------------------------------------------------------------------
// Test: Search with multiple meta filter values (intersection)
// ---------------------------------------------------------------------------

func TestGRPCSearch_MultipleMetaFilters(t *testing.T) {
	gs, s, cleanup := newTestGRPCServer(t)
	defer cleanup()

	// Insert docs with multiple meta fields
	addDocForSearch(t, s, "blog", "id-mf1", "mf1", "en", "c1", map[string][]string{
		"category": {"tech"},
		"status":   {"published"},
	})
	addDocForSearch(t, s, "blog", "id-mf2", "mf2", "en", "c2", map[string][]string{
		"category": {"tech"},
		"status":   {"draft"},
	})

	// Manually index meta
	_ = s.DB.Update(func(tx *bolt.Tx) error {
		bIdx := tx.Bucket(s.BucketNames.IdxMeta)
		_ = bIdx.Put(append(storage.MetaKeyPrefix("blog", "category", "tech"), []byte("id-mf1")...), []byte("1"))
		_ = bIdx.Put(append(storage.MetaKeyPrefix("blog", "category", "tech"), []byte("id-mf2")...), []byte("1"))
		_ = bIdx.Put(append(storage.MetaKeyPrefix("blog", "status", "published"), []byte("id-mf1")...), []byte("1"))
		_ = bIdx.Put(append(storage.MetaKeyPrefix("blog", "status", "draft"), []byte("id-mf2")...), []byte("1"))
		return nil
	})

	resp, err := gs.Search(context.Background(), &pb.SearchRequest{
		Collection: "blog",
		FilterMeta: map[string]*pb.MetaValues{
			"category": {Values: []string{"tech"}},
			"status":   {Values: []string{"published"}},
		},
	})
	if err != nil {
		t.Fatalf("search with multi filter: %v", err)
	}
	// Intersection should yield only mf1
	if resp.Total != 1 {
		t.Errorf("expected 1 result for intersected filter, got %d", resp.Total)
	}
}

// ---------------------------------------------------------------------------
// Test: Get with cache and env from cache hit
// ---------------------------------------------------------------------------

func TestGRPCGet_CacheHitWithEnv(t *testing.T) {
	gs, _, cleanup := newTestGRPCServer(t)
	defer cleanup()

	addDocViaGRPC(t, gs, "blog", "cacheenv", "en", "Hello %%USER%%!", nil)

	// First get to populate cache
	_, err := gs.Get(context.Background(), &pb.GetRequest{
		Collection: "blog",
		Key:        "cacheenv",
		Lang:       "en",
	})
	if err != nil {
		t.Fatalf("first get: %v", err)
	}

	// Second get with env - should hit cache but apply env
	doc, err := gs.Get(context.Background(), &pb.GetRequest{
		Collection: "blog",
		Key:        "cacheenv",
		Lang:       "en",
		Env:        map[string]string{"USER": "Alice"},
	})
	if err != nil {
		t.Fatalf("cached get with env: %v", err)
	}
	if doc.ContentMd != "Hello Alice!" {
		t.Errorf("expected 'Hello Alice!', got %q", doc.ContentMd)
	}
}
