package main

import (
	"context"
	"testing"
	"time"

	bolt "go.etcd.io/bbolt"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"mddb/internal/automationlog"
	"mddb/internal/fts"
	"mddb/internal/storage"
	pb "mddb/proto"
)

// newTestGRPCServerFull extends newTestGRPCServer with all managers needed
// for the 24 new gRPC RPCs.
func newTestGRPCServerFull(t *testing.T) (*GRPCServer, *Server, func()) {
	t.Helper()
	gs, s, cleanup := newTestGRPCServer(t)

	// SynonymManager
	s.SynonymManager = fts.NewSynonymManager(s.DB)
	if err := s.SynonymManager.EnsureBucket(); err != nil {
		cleanup()
		t.Fatal(err)
	}

	// StopWordManager
	s.StopWordManager = fts.NewStopWordManager(s.DB)
	if err := s.StopWordManager.EnsureBucket(); err != nil {
		cleanup()
		t.Fatal(err)
	}

	// AutomationManager
	s.AutomationManager = NewAutomationManager(s.DB)
	if err := s.AutomationManager.EnsureBucket(); err != nil {
		cleanup()
		t.Fatal(err)
	}

	// AutomationLogStore
	s.AutomationLogStore = automationlog.NewStore(s.DB, 24*time.Hour)
	if err := s.AutomationLogStore.EnsureBucket(); err != nil {
		cleanup()
		t.Fatal(err)
	}

	// CollectionManager
	s.CollectionManager = NewCollectionManager(s.DB)
	if err := s.CollectionManager.EnsureBucket(); err != nil {
		cleanup()
		t.Fatal(err)
	}

	return gs, s, cleanup
}

// ---------------------------------------------------------------------------
// DeleteDocument
// ---------------------------------------------------------------------------

func TestGRPCDeleteDocument_Success(t *testing.T) {
	gs, _, cleanup := newTestGRPCServerFull(t)
	defer cleanup()

	addDocViaGRPC(t, gs, "blog", "del1", "en", "to be deleted", nil)
	resp, err := gs.DeleteDocument(context.Background(), &pb.DeleteDocumentRequest{
		Collection: "blog", Key: "del1", Lang: "en",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Status != "deleted" {
		t.Errorf("expected status=deleted, got %s", resp.Status)
	}
	// Verify it's gone from BoltDB (cache may still hold it)
	docID := genID("blog", "del1", "en")
	var found bool
	_ = gs.server.DB.View(func(tx *bolt.Tx) error {
		bDocs := tx.Bucket(gs.server.BucketNames.Docs)
		if bDocs.Get(storage.DocKey("blog", docID)) != nil {
			found = true
		}
		return nil
	})
	if found {
		t.Fatal("expected document to be deleted from BoltDB")
	}
}

func TestGRPCDeleteDocument_MissingFields(t *testing.T) {
	gs, _, cleanup := newTestGRPCServerFull(t)
	defer cleanup()

	_, err := gs.DeleteDocument(context.Background(), &pb.DeleteDocumentRequest{Collection: "blog", Key: "k"})
	if err == nil {
		t.Fatal("expected error for missing lang")
	}
}

func TestGRPCDeleteDocument_ReadOnly(t *testing.T) {
	gs, s, cleanup := newTestGRPCServerFull(t)
	defer cleanup()
	s.Mode = ModeRead

	_, err := gs.DeleteDocument(context.Background(), &pb.DeleteDocumentRequest{
		Collection: "blog", Key: "k", Lang: "en",
	})
	if st, ok := status.FromError(err); !ok || st.Code() != codes.PermissionDenied {
		t.Errorf("expected PermissionDenied, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// DeleteCollection
// ---------------------------------------------------------------------------

func TestGRPCDeleteCollection_Success(t *testing.T) {
	gs, _, cleanup := newTestGRPCServerFull(t)
	defer cleanup()

	addDocViaGRPC(t, gs, "temp", "a", "en", "aaa", nil)
	addDocViaGRPC(t, gs, "temp", "b", "en", "bbb", nil)

	resp, err := gs.DeleteCollection(context.Background(), &pb.DeleteCollectionRequest{Collection: "temp"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.DeletedCount != 2 {
		t.Errorf("expected deletedCount=2, got %d", resp.DeletedCount)
	}
}

func TestGRPCDeleteCollection_MissingCollection(t *testing.T) {
	gs, _, cleanup := newTestGRPCServerFull(t)
	defer cleanup()

	_, err := gs.DeleteCollection(context.Background(), &pb.DeleteCollectionRequest{})
	if st, ok := status.FromError(err); !ok || st.Code() != codes.InvalidArgument {
		t.Errorf("expected InvalidArgument, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// Synonyms
// ---------------------------------------------------------------------------

func TestGRPCSynonyms_CRUD(t *testing.T) {
	gs, _, cleanup := newTestGRPCServerFull(t)
	defer cleanup()

	// Add
	_, err := gs.AddSynonym(context.Background(), &pb.AddSynonymRequest{
		Collection: "blog", Term: "fast", Synonyms: []string{"quick", "rapid"},
	})
	if err != nil {
		t.Fatalf("AddSynonym: %v", err)
	}

	// List
	list, err := gs.ListSynonyms(context.Background(), &pb.ListSynonymsRequest{Collection: "blog"})
	if err != nil {
		t.Fatalf("ListSynonyms: %v", err)
	}
	if list.Total != 1 {
		t.Errorf("expected 1 synonym entry, got %d", list.Total)
	}

	// Delete
	_, err = gs.DeleteSynonym(context.Background(), &pb.DeleteSynonymRequest{Collection: "blog", Term: "fast"})
	if err != nil {
		t.Fatalf("DeleteSynonym: %v", err)
	}

	// Verify deleted
	list, _ = gs.ListSynonyms(context.Background(), &pb.ListSynonymsRequest{Collection: "blog"})
	if list.Total != 0 {
		t.Errorf("expected 0 after delete, got %d", list.Total)
	}
}

func TestGRPCAddSynonym_MissingFields(t *testing.T) {
	gs, _, cleanup := newTestGRPCServerFull(t)
	defer cleanup()

	_, err := gs.AddSynonym(context.Background(), &pb.AddSynonymRequest{Collection: "blog", Term: "fast"})
	if st, ok := status.FromError(err); !ok || st.Code() != codes.InvalidArgument {
		t.Errorf("expected InvalidArgument, got %v", err)
	}
}

func TestGRPCListSynonyms_MissingCollection(t *testing.T) {
	gs, _, cleanup := newTestGRPCServerFull(t)
	defer cleanup()

	_, err := gs.ListSynonyms(context.Background(), &pb.ListSynonymsRequest{})
	if st, ok := status.FromError(err); !ok || st.Code() != codes.InvalidArgument {
		t.Errorf("expected InvalidArgument, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// Stopwords
// ---------------------------------------------------------------------------

func TestGRPCStopwords_CRUD(t *testing.T) {
	gs, _, cleanup := newTestGRPCServerFull(t)
	defer cleanup()

	// Add
	addResp, err := gs.AddStopwords(context.Background(), &pb.AddStopwordsRequest{
		Collection: "blog", Words: []string{"foo", "bar"},
	})
	if err != nil {
		t.Fatalf("AddStopwords: %v", err)
	}
	if addResp.Added != 2 {
		t.Errorf("expected added=2, got %d", addResp.Added)
	}

	// List
	list, err := gs.ListStopwords(context.Background(), &pb.ListStopwordsRequest{Collection: "blog"})
	if err != nil {
		t.Fatalf("ListStopwords: %v", err)
	}
	if list.Custom < 2 {
		t.Errorf("expected at least 2 custom stopwords, got %d", list.Custom)
	}

	// Delete
	delResp, err := gs.DeleteStopwords(context.Background(), &pb.DeleteStopwordsRequest{
		Collection: "blog", Words: []string{"foo"},
	})
	if err != nil {
		t.Fatalf("DeleteStopwords: %v", err)
	}
	if delResp.Deleted != 1 {
		t.Errorf("expected deleted=1, got %d", delResp.Deleted)
	}
}

func TestGRPCAddStopwords_ReadOnly(t *testing.T) {
	gs, s, cleanup := newTestGRPCServerFull(t)
	defer cleanup()
	s.Mode = ModeRead

	_, err := gs.AddStopwords(context.Background(), &pb.AddStopwordsRequest{
		Collection: "blog", Words: []string{"foo"},
	})
	if st, ok := status.FromError(err); !ok || st.Code() != codes.PermissionDenied {
		t.Errorf("expected PermissionDenied, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// GetMetaKeys
// ---------------------------------------------------------------------------

func TestGRPCGetMetaKeys_Success(t *testing.T) {
	gs, s, cleanup := newTestGRPCServerFull(t)
	defer cleanup()

	// Insert doc with meta and manually add index entries
	addDocViaGRPC(t, gs, "blog", "mk1", "en", "meta key test", map[string]*pb.MetaValues{
		"category": {Values: []string{"tech"}},
	})
	docID := genID("blog", "mk1", "en")
	_ = s.DB.Update(func(tx *bolt.Tx) error {
		bIdx := tx.Bucket(s.BucketNames.IdxMeta)
		return bIdx.Put(append(storage.MetaKeyPrefix("blog", "category", "tech"), []byte(docID)...), []byte("1"))
	})

	resp, err := gs.GetMetaKeys(context.Background(), &pb.GetMetaKeysRequest{Collection: "blog"})
	if err != nil {
		t.Fatalf("GetMetaKeys: %v", err)
	}
	if _, ok := resp.Meta["category"]; !ok {
		t.Error("expected 'category' in meta keys")
	}
}

func TestGRPCGetMetaKeys_MissingCollection(t *testing.T) {
	gs, _, cleanup := newTestGRPCServerFull(t)
	defer cleanup()

	_, err := gs.GetMetaKeys(context.Background(), &pb.GetMetaKeysRequest{})
	if st, ok := status.FromError(err); !ok || st.Code() != codes.InvalidArgument {
		t.Errorf("expected InvalidArgument, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// GetChecksum
// ---------------------------------------------------------------------------

func TestGRPCGetChecksum_Success(t *testing.T) {
	gs, _, cleanup := newTestGRPCServerFull(t)
	defer cleanup()

	addDocViaGRPC(t, gs, "blog", "cs1", "en", "checksum test", nil)

	resp, err := gs.GetChecksum(context.Background(), &pb.GetChecksumRequest{Collection: "blog"})
	if err != nil {
		t.Fatalf("GetChecksum: %v", err)
	}
	if resp.Checksum == "" {
		t.Error("expected non-empty checksum")
	}
	if resp.DocumentCount < 1 {
		t.Errorf("expected documentCount >= 1, got %d", resp.DocumentCount)
	}
}

func TestGRPCGetChecksum_MissingCollection(t *testing.T) {
	gs, _, cleanup := newTestGRPCServerFull(t)
	defer cleanup()

	_, err := gs.GetChecksum(context.Background(), &pb.GetChecksumRequest{})
	if st, ok := status.FromError(err); !ok || st.Code() != codes.InvalidArgument {
		t.Errorf("expected InvalidArgument, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// Automation CRUD
// ---------------------------------------------------------------------------

func TestGRPCAutomation_CRUD(t *testing.T) {
	gs, _, cleanup := newTestGRPCServerFull(t)
	defer cleanup()

	// Create
	created, err := gs.CreateAutomation(context.Background(), &pb.CreateAutomationRequest{
		Rule: &pb.AutomationRuleProto{
			Type:    "webhook",
			Name:    "test-hook",
			Enabled: true,
			Url:     "http://example.com/hook",
			Method:  "POST",
		},
	})
	if err != nil {
		t.Fatalf("CreateAutomation: %v", err)
	}
	if created.Id == "" {
		t.Error("expected non-empty ID")
	}
	if created.Name != "test-hook" {
		t.Errorf("expected name=test-hook, got %s", created.Name)
	}

	// Get
	got, err := gs.GetAutomation(context.Background(), &pb.GetAutomationRequest{Id: created.Id})
	if err != nil {
		t.Fatalf("GetAutomation: %v", err)
	}
	if got.Name != "test-hook" {
		t.Errorf("expected name=test-hook, got %s", got.Name)
	}

	// Update
	updated, err := gs.UpdateAutomation(context.Background(), &pb.UpdateAutomationRequest{
		Id: created.Id,
		Rule: &pb.AutomationRuleProto{
			Name:    "renamed-hook",
			Url:     "http://example.com/v2",
			Method:  "POST",
			Enabled: true,
		},
	})
	if err != nil {
		t.Fatalf("UpdateAutomation: %v", err)
	}
	if updated.Name != "renamed-hook" {
		t.Errorf("expected name=renamed-hook, got %s", updated.Name)
	}

	// List
	list, err := gs.ListAutomation(context.Background(), &pb.ListAutomationRequest{})
	if err != nil {
		t.Fatalf("ListAutomation: %v", err)
	}
	if list.Total != 1 {
		t.Errorf("expected 1 rule, got %d", list.Total)
	}

	// Delete
	delResp, err := gs.DeleteAutomation(context.Background(), &pb.DeleteAutomationRequest{Id: created.Id})
	if err != nil {
		t.Fatalf("DeleteAutomation: %v", err)
	}
	if delResp.Status != "deleted" {
		t.Errorf("expected status=deleted, got %s", delResp.Status)
	}

	// Verify deleted
	_, err = gs.GetAutomation(context.Background(), &pb.GetAutomationRequest{Id: created.Id})
	if st, ok := status.FromError(err); !ok || st.Code() != codes.NotFound {
		t.Errorf("expected NotFound after delete, got %v", err)
	}
}

func TestGRPCAutomation_ReadOnly(t *testing.T) {
	gs, s, cleanup := newTestGRPCServerFull(t)
	defer cleanup()
	s.Mode = ModeRead

	_, err := gs.CreateAutomation(context.Background(), &pb.CreateAutomationRequest{
		Rule: &pb.AutomationRuleProto{Type: "webhook", Name: "x"},
	})
	if st, ok := status.FromError(err); !ok || st.Code() != codes.PermissionDenied {
		t.Errorf("expected PermissionDenied, got %v", err)
	}
}

func TestGRPCAutomation_ListByType(t *testing.T) {
	gs, _, cleanup := newTestGRPCServerFull(t)
	defer cleanup()

	whResp, err := gs.CreateAutomation(context.Background(), &pb.CreateAutomationRequest{
		Rule: &pb.AutomationRuleProto{Type: "webhook", Name: "hook1", Url: "http://a.com"},
	})
	if err != nil {
		t.Fatalf("CreateAutomation(webhook): %v", err)
	}
	if _, err := gs.CreateAutomation(context.Background(), &pb.CreateAutomationRequest{
		Rule: &pb.AutomationRuleProto{Type: "trigger", Name: "trig1", Collection: "blog", SearchType: "fts", Query: "test", WebhookId: whResp.GetId()},
	}); err != nil {
		t.Fatalf("CreateAutomation(trigger): %v", err)
	}

	// List only webhooks
	list, err := gs.ListAutomation(context.Background(), &pb.ListAutomationRequest{Type: "webhook"})
	if err != nil {
		t.Fatalf("ListAutomation(webhook): %v", err)
	}
	if list.Total != 1 {
		t.Errorf("expected 1 webhook, got %d", list.Total)
	}
}

// ---------------------------------------------------------------------------
// GetAutomationLogs
// ---------------------------------------------------------------------------

func TestGRPCGetAutomationLogs_Empty(t *testing.T) {
	gs, _, cleanup := newTestGRPCServerFull(t)
	defer cleanup()

	resp, err := gs.GetAutomationLogs(context.Background(), &pb.GetAutomationLogsRequest{Limit: 10})
	if err != nil {
		t.Fatalf("GetAutomationLogs: %v", err)
	}
	if resp.Total != 0 {
		t.Errorf("expected 0 logs, got %d", resp.Total)
	}
}

// ---------------------------------------------------------------------------
// CollectionConfig
// ---------------------------------------------------------------------------

func TestGRPCCollectionConfig_CRUD(t *testing.T) {
	gs, _, cleanup := newTestGRPCServerFull(t)
	defer cleanup()

	// Set
	setResp, err := gs.SetCollectionConfig(context.Background(), &pb.SetCollectionConfigRequest{
		Collection:  "blog",
		Type:        "website",
		Description: "Blog articles",
		Icon:        "📝",
		Color:       "#3b82f6",
	})
	if err != nil {
		t.Fatalf("SetCollectionConfig: %v", err)
	}
	if setResp.Status != "ok" {
		t.Errorf("expected status=ok, got %s", setResp.Status)
	}

	// Get
	getResp, err := gs.GetCollectionConfig(context.Background(), &pb.GetCollectionConfigRequest{Collection: "blog"})
	if err != nil {
		t.Fatalf("GetCollectionConfig: %v", err)
	}
	if !getResp.Configured {
		t.Error("expected configured=true")
	}
	if getResp.Config.Type != "website" {
		t.Errorf("expected type=website, got %s", getResp.Config.Type)
	}
	if getResp.Config.Description != "Blog articles" {
		t.Errorf("expected description='Blog articles', got %s", getResp.Config.Description)
	}

	// List
	listResp, err := gs.ListCollectionConfigs(context.Background(), &pb.ListCollectionConfigsRequest{})
	if err != nil {
		t.Fatalf("ListCollectionConfigs: %v", err)
	}
	if listResp.Total != 1 {
		t.Errorf("expected 1 config, got %d", listResp.Total)
	}
}

func TestGRPCCollectionConfig_NotFound(t *testing.T) {
	gs, _, cleanup := newTestGRPCServerFull(t)
	defer cleanup()

	resp, err := gs.GetCollectionConfig(context.Background(), &pb.GetCollectionConfigRequest{Collection: "nonexistent"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Configured {
		t.Error("expected configured=false for nonexistent collection")
	}
}

func TestGRPCSetCollectionConfig_ReadOnly(t *testing.T) {
	gs, s, cleanup := newTestGRPCServerFull(t)
	defer cleanup()
	s.Mode = ModeRead

	_, err := gs.SetCollectionConfig(context.Background(), &pb.SetCollectionConfigRequest{
		Collection: "blog", Type: "website",
	})
	if st, ok := status.FromError(err); !ok || st.Code() != codes.PermissionDenied {
		t.Errorf("expected PermissionDenied, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// ListRevisions
// ---------------------------------------------------------------------------

func TestGRPCListRevisions_MissingFields(t *testing.T) {
	gs, _, cleanup := newTestGRPCServerFull(t)
	defer cleanup()

	_, err := gs.ListRevisions(context.Background(), &pb.ListRevisionsRequest{Collection: "blog"})
	if st, ok := status.FromError(err); !ok || st.Code() != codes.InvalidArgument {
		t.Errorf("expected InvalidArgument, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// RestoreRevision
// ---------------------------------------------------------------------------

func TestGRPCRestoreRevision_Success(t *testing.T) {
	gs, _, cleanup := newTestGRPCServerFull(t)
	defer cleanup()

	// Add v1 with revision
	_, err := gs.Add(context.Background(), &pb.AddRequest{
		Collection: "blog", Key: "restore1", Lang: "en",
		ContentMd:    "# Original",
		SaveRevision: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Get the revision timestamp
	revs, err := gs.ListRevisions(context.Background(), &pb.ListRevisionsRequest{
		Collection: "blog", Key: "restore1", Lang: "en",
	})
	if err != nil || revs.Total == 0 {
		t.Fatal("expected at least 1 revision")
	}
	ts := revs.Revisions[0].Timestamp

	// Overwrite content
	_, err = gs.Add(context.Background(), &pb.AddRequest{
		Collection: "blog", Key: "restore1", Lang: "en",
		ContentMd: "# Modified",
	})
	if err != nil {
		t.Fatal(err)
	}

	// Restore to original
	doc, err := gs.RestoreRevision(context.Background(), &pb.RestoreRevisionRequest{
		Collection: "blog", Key: "restore1", Lang: "en", Timestamp: ts,
	})
	if err != nil {
		t.Fatalf("RestoreRevision: %v", err)
	}
	if doc.ContentMd != "# Original" {
		t.Errorf("expected restored content='# Original', got '%s'", doc.ContentMd)
	}
}

func TestGRPCRestoreRevision_NotFound(t *testing.T) {
	gs, _, cleanup := newTestGRPCServerFull(t)
	defer cleanup()

	_, err := gs.RestoreRevision(context.Background(), &pb.RestoreRevisionRequest{
		Collection: "blog", Key: "nonexistent", Lang: "en", Timestamp: 1234567890,
	})
	if st, ok := status.FromError(err); !ok || st.Code() != codes.NotFound {
		t.Errorf("expected NotFound, got %v", err)
	}
}

func TestGRPCRestoreRevision_ReadOnly(t *testing.T) {
	gs, s, cleanup := newTestGRPCServerFull(t)
	defer cleanup()
	s.Mode = ModeRead

	_, err := gs.RestoreRevision(context.Background(), &pb.RestoreRevisionRequest{
		Collection: "blog", Key: "k", Lang: "en", Timestamp: 1,
	})
	if st, ok := status.FromError(err); !ok || st.Code() != codes.PermissionDenied {
		t.Errorf("expected PermissionDenied, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// FindDuplicates
// ---------------------------------------------------------------------------

func TestGRPCFindDuplicates_MissingCollection(t *testing.T) {
	gs, _, cleanup := newTestGRPCServerFull(t)
	defer cleanup()

	_, err := gs.FindDuplicates(context.Background(), &pb.FindDuplicatesRequest{})
	if st, ok := status.FromError(err); !ok || st.Code() != codes.InvalidArgument {
		t.Errorf("expected InvalidArgument, got %v", err)
	}
}

func TestGRPCFindDuplicates_InvalidMode(t *testing.T) {
	gs, _, cleanup := newTestGRPCServerFull(t)
	defer cleanup()

	_, err := gs.FindDuplicates(context.Background(), &pb.FindDuplicatesRequest{
		Collection: "blog", Mode: "invalid",
	})
	if st, ok := status.FromError(err); !ok || st.Code() != codes.InvalidArgument {
		t.Errorf("expected InvalidArgument, got %v", err)
	}
}

func TestGRPCFindDuplicates_EmptyCollection(t *testing.T) {
	gs, _, cleanup := newTestGRPCServerFull(t)
	defer cleanup()

	resp, err := gs.FindDuplicates(context.Background(), &pb.FindDuplicatesRequest{
		Collection: "empty",
	})
	if err != nil {
		t.Fatalf("FindDuplicates: %v", err)
	}
	if resp.TotalDocuments != 0 {
		t.Errorf("expected 0 documents, got %d", resp.TotalDocuments)
	}
}

// ---------------------------------------------------------------------------
// CrossSearch
// ---------------------------------------------------------------------------

func TestGRPCCrossSearch_MissingTargets(t *testing.T) {
	gs, _, cleanup := newTestGRPCServerFull(t)
	defer cleanup()

	_, err := gs.CrossSearch(context.Background(), &pb.CrossSearchRequest{Query: "test"})
	if st, ok := status.FromError(err); !ok || st.Code() != codes.InvalidArgument {
		t.Errorf("expected InvalidArgument, got %v", err)
	}
}

func TestGRPCCrossSearch_MissingQuery(t *testing.T) {
	gs, _, cleanup := newTestGRPCServerFull(t)
	defer cleanup()

	_, err := gs.CrossSearch(context.Background(), &pb.CrossSearchRequest{
		TargetCollections: []string{"blog"},
	})
	if st, ok := status.FromError(err); !ok || st.Code() != codes.InvalidArgument {
		t.Errorf("expected InvalidArgument for missing query, got %v", err)
	}
}

func TestGRPCCrossSearch_WithQueryVector(t *testing.T) {
	gs, s, cleanup := newTestGRPCServerFull(t)
	defer cleanup()

	// Add doc and vector
	addDocViaGRPC(t, gs, "blog", "cs1", "en", "cross search test", nil)
	docID := genID("blog", "cs1", "en")
	s.VectorIndex.Add("blog", docID, []float32{1.0, 0.0, 0.0})

	resp, err := gs.CrossSearch(context.Background(), &pb.CrossSearchRequest{
		TargetCollections: []string{"blog"},
		QueryVector:       []float32{1.0, 0.0, 0.0},
		TopK:              5,
	})
	if err != nil {
		t.Fatalf("CrossSearch: %v", err)
	}
	if resp.Total < 1 {
		t.Errorf("expected at least 1 result, got %d", resp.Total)
	}
}

// ---------------------------------------------------------------------------
// Conversion helpers
// ---------------------------------------------------------------------------

func TestAutomationRuleProtoConversion(t *testing.T) {
	rule := AutomationRule{
		ID:      "test-id",
		Type:    "trigger",
		Name:    "test-trigger",
		Enabled: true,
		SearchParams: map[string]interface{}{
			"algorithm": "bm25",
			"fuzzy":     float64(1),
		},
	}

	protoRule := automationRuleToProto(&rule)
	if protoRule.Id != "test-id" {
		t.Errorf("expected id=test-id, got %s", protoRule.Id)
	}
	if protoRule.SearchParamsJson == "" {
		t.Error("expected non-empty SearchParamsJson")
	}

	// Convert back
	back := protoToAutomationRule(protoRule)
	if back.ID != "test-id" {
		t.Errorf("expected id=test-id, got %s", back.ID)
	}
	if back.SearchParams == nil {
		t.Error("expected non-nil SearchParams after roundtrip")
	}
	if back.SearchParams["algorithm"] != "bm25" {
		t.Errorf("expected algorithm=bm25, got %v", back.SearchParams["algorithm"])
	}
}
