package main

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mddb/internal/automationlog"
	"mddb/internal/embedding"
	"mddb/internal/storage"

	bolt "go.etcd.io/bbolt"
)

// covC: automation, revisions, collection config, curation and cross-search
// tools of the direct client.

// covCFailEmbedding is a provider whose every embedding fails.
type covCFailEmbedding struct{ mockEmbedding }

func (*covCFailEmbedding) Embed(context.Context, string, embedding.Role) ([]float32, error) {
	return nil, errors.New("provider down")
}

func TestCovCAutomationToolsReportAMissingSubsystem(t *testing.T) {
	c := NewDirectClient(&Server{})
	ctx := context.Background()
	errs := map[string]error{}
	_, errs["ListAutomation"] = c.ListAutomation(ctx, "")
	_, errs["CreateAutomation"] = c.CreateAutomation(ctx, AutomationRule{})
	_, errs["GetAutomation"] = c.GetAutomation(ctx, "x")
	_, errs["UpdateAutomation"] = c.UpdateAutomation(ctx, "x", AutomationRule{})
	errs["DeleteAutomation"] = c.DeleteAutomation(ctx, "x")
	_, errs["TestAutomation"] = c.TestAutomation(ctx, "x")
	_, errs["ListAutomationLogs"] = c.ListAutomationLogs(ctx, 0, "", "", "")
	_, errs["ListCurationRules"] = c.ListCurationRules(ctx, "")
	_, errs["CreateCurationRule"] = c.CreateCurationRule(ctx, &CurationRule{})
	_, errs["UpdateCurationRule"] = c.UpdateCurationRule(ctx, &CurationRule{ID: "x"})
	errs["DeleteCurationRule"] = c.DeleteCurationRule(ctx, "x")
	for name, err := range errs {
		if err == nil || !strings.Contains(err.Error(), "not initialized") {
			t.Errorf("%s: err = %v, want a not-initialized error", name, err)
		}
	}
	if err := c.Close(); err != nil {
		t.Errorf("Close = %v", err)
	}
}

func TestCovCAutomationLifecycle(t *testing.T) {
	c, s := covCServer(t)
	s.AutomationManager = NewAutomationManager(s.DB)
	if err := s.AutomationManager.EnsureBucket(); err != nil {
		t.Fatal(err)
	}
	s.AutomationManager.SetServer(s)
	ctx := context.Background()
	addTestDoc(t, s, "auto", "z", "en", "a striped zebra", nil)

	hook, err := c.CreateAutomation(ctx, AutomationRule{Type: "webhook", Name: "hook", URL: "https://example.com/h", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if list, err := c.ListAutomation(ctx, "webhook"); err != nil || list.Total != 1 || list.Rules[0].ID != hook.ID {
		t.Fatalf("ListAutomation = %+v, %v", list, err)
	}
	if got, err := c.GetAutomation(ctx, hook.ID); err != nil || got.Name != "hook" {
		t.Fatalf("GetAutomation = %+v, %v", got, err)
	}
	if _, err := c.GetAutomation(ctx, "nope"); err == nil {
		t.Error("an unknown rule was found")
	}
	renamed, err := c.UpdateAutomation(ctx, hook.ID, AutomationRule{Type: "webhook", Name: "renamed", URL: "https://example.com/h"})
	if err != nil || renamed.Name != "renamed" {
		t.Fatalf("UpdateAutomation = %+v, %v", renamed, err)
	}

	if _, err := c.TestAutomation(ctx, "nope"); err == nil || err.Error() != "not found" {
		t.Errorf("testing an unknown rule: %v", err)
	}
	if _, err := c.TestAutomation(ctx, hook.ID); err == nil || !strings.Contains(err.Error(), "only test trigger") {
		t.Errorf("testing a webhook: %v", err)
	}
	// A trigger without a query has no search type to run.
	bare, err := c.CreateAutomation(ctx, AutomationRule{Type: "trigger", Name: "bare", Collection: "auto", WebhookID: hook.ID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.TestAutomation(ctx, bare.ID); !errors.Is(err, ErrUnknownSearchType) {
		t.Errorf("testing a trigger with no search type: %v", err)
	}
	fts, err := c.CreateAutomation(ctx, AutomationRule{Type: "trigger", Name: "zebras", Collection: "auto",
		SearchType: "fts", Query: "zebra", WebhookID: hook.ID})
	if err != nil {
		t.Fatal(err)
	}
	out, err := c.TestAutomation(ctx, fts.ID)
	if err != nil || !strings.Contains(out, `"total": 1`) || !strings.Contains(out, `"name": "zebras"`) {
		t.Fatalf("TestAutomation = %s, %v", out, err)
	}

	if err := c.DeleteAutomation(ctx, fts.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := c.GetAutomation(ctx, fts.ID); err == nil {
		t.Error("a deleted rule is still found")
	}
}

func TestCovCListAutomationLogsPages(t *testing.T) {
	c, s := covCServer(t)
	s.AutomationLogStore = automationlog.NewStore(s.DB, time.Hour)
	if err := s.AutomationLogStore.EnsureBucket(); err != nil {
		t.Fatal(err)
	}
	for i, status := range []string{"success", "error"} {
		if err := s.AutomationLogStore.Log(automationlog.Entry{ID: "log" + string(rune('a'+i)), RuleID: "r", Status: status}); err != nil {
			t.Fatal(err)
		}
	}
	ctx := context.Background()
	all, err := c.ListAutomationLogs(ctx, 0, "", "", "")
	if err != nil || all.Total != 2 || len(all.Logs) != 2 || all.HasMore {
		t.Fatalf("all logs = %+v, %v", all, err)
	}
	page, err := c.ListAutomationLogs(ctx, 1, "", "r", "error")
	if err != nil || page.Total != 1 || len(page.Logs) != 1 || page.Logs[0].Status != "error" {
		t.Fatalf("filtered page = %+v, %v", page, err)
	}

	// A store whose database is gone reports it.
	db, err := bolt.Open(filepath.Join(t.TempDir(), "logs.db"), 0o600, nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	s.AutomationLogStore = automationlog.NewStore(db, time.Hour)
	if _, err := c.ListAutomationLogs(ctx, 0, "", "", ""); err == nil {
		t.Error("listing logs from a closed database succeeded")
	}
}

func TestCovCRevisionsListAndRestore(t *testing.T) {
	c, s := covCServer(t)
	ctx := context.Background()
	addTestDoc(t, s, "rv", "page", "en", "first version", nil)
	doc := addTestDoc(t, s, "rv", "page", "en", "second version", nil)
	prefix := storage.RevPrefix("rv", doc.ID)
	// Damaged revision keys and records are skipped, not reported.
	covCPut(t, s, "rev", prefix, []byte("x"))
	covCPut(t, s, "rev", append(append([]byte{}, prefix...), "notatime"...), []byte("x"))
	covCPut(t, s, "rev", append(append([]byte{}, prefix...), "00000000000000000001"...), covCGarbage)

	list, err := c.ListRevisions(ctx, "rv", "page", "en")
	if err != nil || list.Total == 0 {
		t.Fatalf("ListRevisions = %+v, %v", list, err)
	}
	oldest := list.Revisions[list.Total-1]
	for _, r := range list.Revisions {
		if r.Timestamp == 1 {
			t.Error("a damaged revision was listed")
		}
	}
	if oldest.ContentMD != "first version" {
		t.Fatalf("oldest revision = %+v", oldest)
	}

	restored, err := c.RestoreRevision(ctx, "rv", "page", "en", oldest.Timestamp)
	if err != nil || restored.ContentMD != "first version" {
		t.Fatalf("RestoreRevision = %+v, %v", restored, err)
	}
	if _, err := c.RestoreRevision(ctx, "rv", "page", "en", 42); err == nil {
		t.Error("restoring a revision that does not exist succeeded")
	}
	if _, err := c.RestoreRevision(ctx, "rv", "page", "en", 1); err == nil {
		t.Error("restoring a damaged revision succeeded")
	}
	// A revision the collection's schema now rejects is not written back.
	if err := s.SchemaManager.Set("rv", `{"required":["author"]}`); err != nil {
		t.Fatal(err)
	}
	if _, err := c.RestoreRevision(ctx, "rv", "page", "en", oldest.Timestamp); err == nil {
		t.Error("a revision violating the schema was restored")
	}
}

func TestCovCRevisionsWithoutTheirBucketOrDatabase(t *testing.T) {
	bare := covCBareClient(t)
	ctx := context.Background()
	if list, err := bare.ListRevisions(ctx, "c", "k", "en"); err != nil || list.Total != 0 {
		t.Errorf("ListRevisions on a bare database = %+v, %v", list, err)
	}
	if _, err := bare.RestoreRevision(ctx, "c", "k", "en", 1); err == nil || !errors.Is(err, errNotFound) {
		t.Errorf("RestoreRevision on a bare database: %v", err)
	}
	closed, _ := covCClosedClient(t, "c")
	if _, err := closed.ListRevisions(ctx, "c", "k", "en"); err == nil {
		t.Error("ListRevisions on a closed database succeeded")
	}
}

func TestCovCCollectionConfigMergesWhatWasSent(t *testing.T) {
	c, _ := covCServer(t)
	ctx := context.Background()
	got, err := c.GetCollectionConfig(ctx, "cfg")
	if err != nil || got.Configured || got.Config.Type != "default" {
		t.Fatalf("unconfigured collection = %+v, %v", got, err)
	}
	if err := c.SetCollectionConfig(ctx, &MCPSetCollectionConfigRequest{Collection: "cfg",
		WordPress: &WordPressTargetConfig{URL: "http://public.example.com"}}); err == nil {
		t.Error("a plain-http WordPress target was accepted")
	}
	if err := c.SetCollectionConfig(ctx, &MCPSetCollectionConfigRequest{Collection: "cfg",
		Retrieval: &RetrievalProfileDef{DefaultSearchType: "telepathy"}}); err == nil {
		t.Error("an invalid retrieval profile was accepted")
	}
	if err := c.SetCollectionConfig(ctx, &MCPSetCollectionConfigRequest{Collection: "cfg", Icon: "book", Color: "#123456",
		CustomMeta: map[string]string{"team": "docs"}, WordPress: &WordPressTargetConfig{URL: "https://blog.example.com"},
		Retrieval: &RetrievalProfileDef{TopK: 7}}); err != nil {
		t.Fatal(err)
	}
	// A later call that sends only a description keeps everything else.
	if err := c.SetCollectionConfig(ctx, &MCPSetCollectionConfigRequest{Collection: "cfg", Description: "manuals"}); err != nil {
		t.Fatal(err)
	}
	got, err = c.GetCollectionConfig(ctx, "cfg")
	cfg := got.Config
	if err != nil || !got.Configured || cfg.Icon != "book" || cfg.Color != "#123456" || cfg.CustomMeta["team"] != "docs" ||
		cfg.WordPress == nil || cfg.WordPress.URL != "https://blog.example.com" || cfg.Retrieval == nil ||
		cfg.Retrieval.TopK != 7 || cfg.Description != "manuals" {
		t.Fatalf("merged config = %+v, %v", cfg, err)
	}
}

func TestCovCCurationRuleLifecycle(t *testing.T) {
	c, s := covCServer(t)
	s.CurationManager = NewCurationManager(s.DB)
	if err := s.CurationManager.EnsureBucket(); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := c.CreateCurationRule(ctx, nil); err == nil {
		t.Error("a nil rule was created")
	}
	if _, err := c.CreateCurationRule(ctx, &CurationRule{Collection: "cu"}); err == nil {
		t.Error("a rule without a query was created")
	}
	rule, err := c.CreateCurationRule(ctx, &CurationRule{ID: "chosen", Collection: "cu", Query: "go", Enabled: true})
	if err != nil || rule.ID == "" || rule.ID == "chosen" {
		t.Fatalf("CreateCurationRule = %+v, %v (the id must be assigned, not taken)", rule, err)
	}
	if all, err := c.ListCurationRules(ctx, ""); err != nil || len(all) != 1 {
		t.Fatalf("ListCurationRules all = %v, %v", all, err)
	}
	if mine, _ := c.ListCurationRules(ctx, "cu"); len(mine) != 1 {
		t.Fatalf("ListCurationRules cu = %v", mine)
	}

	if _, err := c.UpdateCurationRule(ctx, &CurationRule{}); err == nil {
		t.Error("an update without an id was accepted")
	}
	if _, err := c.UpdateCurationRule(ctx, &CurationRule{ID: "nope", Query: "x"}); err == nil {
		t.Error("an update of an unknown rule was accepted")
	}
	if _, err := c.UpdateCurationRule(ctx, &CurationRule{ID: rule.ID, Query: "go", MatchMode: "fuzzy"}); err == nil {
		t.Error("an invalid match mode was accepted")
	}
	updated, err := c.UpdateCurationRule(ctx, &CurationRule{ID: rule.ID, Query: "golang"})
	if err != nil || updated.Collection != "cu" || updated.CreatedAt != rule.CreatedAt {
		t.Fatalf("UpdateCurationRule = %+v, %v (collection and creation time carry over)", updated, err)
	}

	if err := c.DeleteCurationRule(ctx, "nope"); err == nil {
		t.Error("deleting an unknown rule succeeded")
	}
	if err := c.DeleteCurationRule(ctx, rule.ID); err != nil {
		t.Fatal(err)
	}
	if all, _ := c.ListCurationRules(ctx, ""); len(all) != 0 {
		t.Errorf("rule survived its deletion: %v", all)
	}
}

func TestCovCCrossSearchRefusesWhatItCannotRun(t *testing.T) {
	c, s := covCServer(t)
	s.VectorIndex.SetReady()
	addTestDoc(t, s, "xs", "a", "en", "x", nil)
	ctx := context.Background()
	targets := []string{"xs"}
	cases := map[string]*MCPCrossSearchRequest{
		"empty groupBy":         {TargetCollections: targets, Query: "q", GroupBy: &GroupBy{}},
		"unknown algorithm":     {TargetCollections: targets, Query: "q", Algorithm: "telepathy"},
		"doc without its coll":  {TargetCollections: targets, SourceDocID: "a"},
		"source not embedded":   {TargetCollections: targets, SourceCollection: "xs", SourceDocID: "nope"},
		"no provider for query": {TargetCollections: targets, Query: "q"},
		"nothing to search by":  {TargetCollections: targets},
	}
	for name, req := range cases {
		if resp, err := c.CrossSearch(ctx, req); err == nil {
			t.Errorf("%s: searched anyway: %+v", name, resp)
		}
	}
	if _, err := c.CrossSearch(ctx, cases["source not embedded"]); !errors.Is(err, errSourceNoEmbedding) {
		t.Errorf("source not embedded: %v", err)
	}
	if _, err := c.CrossSearch(ctx, cases["no provider for query"]); !errors.Is(err, errNoEmbeddingProvider) {
		t.Errorf("no provider: %v", err)
	}
	// A document whose stored vector stands in for the query.
	doc := addTestDoc(t, s, "xs", "b", "en", "y", nil)
	if err := s.VectorStore.Put("xs", doc.ID, []float32{0.1, 0.2, 0.3}, "m", "h"); err != nil {
		t.Fatal(err)
	}
	resp, err := c.CrossSearch(ctx, &MCPCrossSearchRequest{TargetCollections: targets, SourceCollection: "xs", SourceDocID: doc.ID})
	if err != nil || resp.Algorithm != "flat" || resp.SourceDocID != doc.ID {
		t.Errorf("search by source document = %+v, %v", resp, err)
	}
	s.Embedding = &covCFailEmbedding{}
	if _, err := c.CrossSearch(ctx, &MCPCrossSearchRequest{TargetCollections: targets, Query: "q"}); !errors.Is(err, errEmbedQuery) {
		t.Errorf("failing provider: %v", err)
	}
}

func TestCovCFindDuplicatesAndIngest(t *testing.T) {
	c, s := covCServer(t)
	ctx := context.Background()
	text := "the quick brown fox jumps over the lazy dog while the cat sleeps on the warm mat all afternoon"
	ing, err := c.Ingest(ctx, &MCPIngestRequest{Collection: "dup", Documents: []MCPIngestDocument{
		{Key: "one", Lang: "en", ContentMD: text}, {Key: "two", Lang: "en", ContentMD: text},
	}, Options: MCPIngestOptions{SkipEmbeddings: true}})
	if err != nil || ing.Added != 2 || ing.Collection != "dup" {
		t.Fatalf("Ingest = %+v, %v", ing, err)
	}
	dups, err := c.FindDuplicates(ctx, &MCPFindDuplicatesRequest{Collection: "dup", Mode: "minhash"})
	if err != nil || dups.TotalDocuments != 2 || len(dups.MinHashGroups) != 1 || len(dups.MinHashGroups[0].Documents) != 2 {
		t.Fatalf("FindDuplicates = %+v, %v", dups, err)
	}

	closeStorage(t, s, "dup")
	if _, err := c.Ingest(ctx, &MCPIngestRequest{Collection: "dup", Documents: []MCPIngestDocument{
		{Key: "three", Lang: "en", ContentMD: "more"},
	}}); err == nil {
		t.Error("ingest into a closed database reported success")
	}
}
