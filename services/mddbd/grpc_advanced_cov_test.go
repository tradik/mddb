package main

// Coverage for grpc_advanced.go (covE batch): automation, collection
// configuration, duplicates, revisions and the cross-search permission walk.

import (
	"context"
	"fmt"
	"testing"
	"time"

	"mddb/internal/automationlog"
	"mddb/internal/storage"
	pb "mddb/proto"

	bolt "go.etcd.io/bbolt"
	"google.golang.org/grpc/codes"
)

func covEAdvancedGuarded(gs *GRPCServer) []covERPC {
	return []covERPC{
		{"ListAutomation", func(ctx context.Context) error {
			_, err := gs.ListAutomation(ctx, &pb.ListAutomationRequest{})
			return err
		}},
		{"CreateAutomation", func(ctx context.Context) error {
			_, err := gs.CreateAutomation(ctx, &pb.CreateAutomationRequest{Rule: &pb.AutomationRuleProto{}})
			return err
		}},
		{"GetAutomation", func(ctx context.Context) error {
			_, err := gs.GetAutomation(ctx, &pb.GetAutomationRequest{Id: "x"})
			return err
		}},
		{"UpdateAutomation", func(ctx context.Context) error {
			_, err := gs.UpdateAutomation(ctx, &pb.UpdateAutomationRequest{Id: "x", Rule: &pb.AutomationRuleProto{}})
			return err
		}},
		{"DeleteAutomation", func(ctx context.Context) error {
			_, err := gs.DeleteAutomation(ctx, &pb.DeleteAutomationRequest{Id: "x"})
			return err
		}},
		{"TestAutomation", func(ctx context.Context) error {
			_, err := gs.TestAutomation(ctx, &pb.TestAutomationRequest{Id: "x"})
			return err
		}},
		{"GetAutomationLogs", func(ctx context.Context) error {
			_, err := gs.GetAutomationLogs(ctx, &pb.GetAutomationLogsRequest{})
			return err
		}},
		{"GetCollectionConfig", func(ctx context.Context) error {
			_, err := gs.GetCollectionConfig(ctx, &pb.GetCollectionConfigRequest{Collection: "c"})
			return err
		}},
		{"SetCollectionConfig", func(ctx context.Context) error {
			_, err := gs.SetCollectionConfig(ctx, &pb.SetCollectionConfigRequest{Collection: "c"})
			return err
		}},
		{"ListCollectionConfigs", func(ctx context.Context) error {
			_, err := gs.ListCollectionConfigs(ctx, &pb.ListCollectionConfigsRequest{})
			return err
		}},
		{"FindDuplicates", func(ctx context.Context) error {
			_, err := gs.FindDuplicates(ctx, &pb.FindDuplicatesRequest{Collection: "c"})
			return err
		}},
		{"ListRevisions", func(ctx context.Context) error {
			_, err := gs.ListRevisions(ctx, &pb.ListRevisionsRequest{Collection: "c", Key: "k", Lang: "en"})
			return err
		}},
		{"RestoreRevision", func(ctx context.Context) error {
			_, err := gs.RestoreRevision(ctx, &pb.RestoreRevisionRequest{Collection: "c", Key: "k", Lang: "en", Timestamp: 1})
			return err
		}},
	}
}

func TestCovEAdvancedPermissionDenied(t *testing.T) {
	gs, s := covEServer(t)
	covEDenyAll(t, s)
	covERun(t, context.Background(), covEAdvancedGuarded(gs), codes.PermissionDenied, "")
	if n := len(s.AutomationManager.List("")); n != 0 {
		t.Fatalf("refused calls left %d rules", n)
	}
}

func TestCovEAdvancedReadOnly(t *testing.T) {
	gs, s := covEServer(t)
	s.Mode = ModeRead
	var writes []covERPC
	for _, r := range covEAdvancedGuarded(gs) {
		switch r.name {
		case "CreateAutomation", "UpdateAutomation", "DeleteAutomation", "SetCollectionConfig", "RestoreRevision":
			writes = append(writes, r)
		}
	}
	covERun(t, context.Background(), writes, codes.PermissionDenied, "read-only mode")
}

func TestCovEAdvancedWithoutManagers(t *testing.T) {
	gs, s := covEServer(t)
	s.AutomationManager, s.AutomationLogStore, s.CollectionManager = nil, nil, nil
	var rpcs []covERPC
	for _, r := range covEAdvancedGuarded(gs) {
		switch r.name {
		case "FindDuplicates", "ListRevisions", "RestoreRevision":
		default:
			rpcs = append(rpcs, r)
		}
	}
	covERun(t, context.Background(), rpcs, codes.FailedPrecondition, "not initialized")
}

// covEAutomationServer wires the automation manager to its server and a
// running cron scheduler.
func covEAutomationServer(t *testing.T) (*GRPCServer, *Server) {
	t.Helper()
	gs, s := covEServer(t)
	s.AutomationManager.SetServer(s)
	s.CronScheduler = NewCronScheduler(s)
	s.CronScheduler.Start()
	t.Cleanup(s.CronScheduler.Stop)
	return gs, s
}

func TestCovEAutomationLifecycle(t *testing.T) {
	gs, s := covEAutomationServer(t)
	ctx := context.Background()

	_, err := gs.CreateAutomation(ctx, &pb.CreateAutomationRequest{})
	covEWant(t, "no rule", err, codes.InvalidArgument, "missing rule")
	_, err = gs.CreateAutomation(ctx, &pb.CreateAutomationRequest{Rule: &pb.AutomationRuleProto{Type: "webhook"}})
	covEWant(t, "invalid rule", err, codes.InvalidArgument, "name is required")
	_, err = gs.GetAutomation(ctx, &pb.GetAutomationRequest{})
	covEWant(t, "get without id", err, codes.InvalidArgument, "missing id")
	_, err = gs.UpdateAutomation(ctx, &pb.UpdateAutomationRequest{Id: "x"})
	covEWant(t, "update without rule", err, codes.InvalidArgument, "missing id or rule")
	_, err = gs.UpdateAutomation(ctx, &pb.UpdateAutomationRequest{Id: "missing", Rule: &pb.AutomationRuleProto{Name: "n", Type: "webhook", Url: "https://example.com"}})
	covEWant(t, "update unknown", err, codes.Internal, "rule not found")
	_, err = gs.DeleteAutomation(ctx, &pb.DeleteAutomationRequest{})
	covEWant(t, "delete without id", err, codes.InvalidArgument, "missing id")

	wh, err := gs.CreateAutomation(ctx, &pb.CreateAutomationRequest{Rule: &pb.AutomationRuleProto{
		Type: "webhook", Name: "hook", Url: "https://example.com/h", Enabled: true}})
	if err != nil {
		t.Fatal(err)
	}
	cron, err := gs.CreateAutomation(ctx, &pb.CreateAutomationRequest{Rule: &pb.AutomationRuleProto{
		Type: "cron", Name: "nightly", Schedule: "0 0 * * * *", WebhookId: wh.Id, Enabled: true,
		SearchParamsJson: `{"limit":5}`}})
	if err != nil {
		t.Fatal(err)
	}
	if cron.SearchParamsJson != `{"limit":5}` {
		t.Fatalf("search params lost: %q", cron.SearchParamsJson)
	}
	if n := len(s.CronScheduler.entryMap); n != 1 {
		t.Fatalf("scheduler holds %d entries after create, want 1", n)
	}

	upd, err := gs.UpdateAutomation(ctx, &pb.UpdateAutomationRequest{Id: cron.Id, Rule: &pb.AutomationRuleProto{
		Type: "cron", Name: "paused", Schedule: "0 0 * * * *", WebhookId: wh.Id, Enabled: false}})
	if err != nil || upd.Name != "paused" {
		t.Fatalf("update cron: %v %+v", err, upd)
	}
	if n := len(s.CronScheduler.entryMap); n != 0 {
		t.Fatalf("disabled cron still scheduled: %d entries", n)
	}

	got, err := gs.GetAutomation(ctx, &pb.GetAutomationRequest{Id: cron.Id})
	if err != nil || got.Enabled {
		t.Fatalf("get: %v %+v", err, got)
	}
	del, err := gs.DeleteAutomation(ctx, &pb.DeleteAutomationRequest{Id: cron.Id})
	if err != nil || del.Status != "deleted" {
		t.Fatalf("delete: %v %+v", err, del)
	}
	_, err = gs.GetAutomation(ctx, &pb.GetAutomationRequest{Id: cron.Id})
	covEWant(t, "deleted rule", err, codes.NotFound, "")

	covECloseDB(t, s)
	_, err = gs.DeleteAutomation(ctx, &pb.DeleteAutomationRequest{Id: wh.Id})
	covEWant(t, "delete on closed db", err, codes.Internal, "database not open")
}

func TestCovETestAutomation(t *testing.T) {
	gs, s := covEAutomationServer(t)
	ctx := context.Background()

	_, err := gs.TestAutomation(ctx, &pb.TestAutomationRequest{})
	covEWant(t, "no id", err, codes.InvalidArgument, "missing id")
	_, err = gs.TestAutomation(ctx, &pb.TestAutomationRequest{Id: "missing"})
	covEWant(t, "unknown", err, codes.NotFound, "")

	wh, err := s.AutomationManager.Create(AutomationRule{Type: "webhook", Name: "hook", URL: "https://example.com", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	_, err = gs.TestAutomation(ctx, &pb.TestAutomationRequest{Id: wh.ID})
	covEWant(t, "not a trigger", err, codes.InvalidArgument, "only trigger rules")

	// No query means no search type, which RunTrigger refuses.
	bare, err := s.AutomationManager.Create(AutomationRule{Type: "trigger", Name: "bare", Collection: "blog", WebhookID: wh.ID, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	_, err = gs.TestAutomation(ctx, &pb.TestAutomationRequest{Id: bare.ID})
	covEWant(t, "no search type", err, codes.Internal, "unknown trigger search type")

	addTestDoc(t, s, "blog", "go", "en", "golang programming tutorial", nil)
	// Index hits without a readable document are skipped.
	if err := s.FTSIndex.Index("blog", "ghost", "golang ghost"); err != nil {
		t.Fatal(err)
	}
	if err := s.FTSIndex.Index("blog", "broken", "golang broken"); err != nil {
		t.Fatal(err)
	}
	if err := s.DBUpdateC("blog", func(tx *bolt.Tx) error {
		return tx.Bucket([]byte("docs")).Put(storage.DocKey("blog", "broken"), []byte{0xff, 0x01})
	}); err != nil {
		t.Fatal(err)
	}
	trig, err := s.AutomationManager.Create(AutomationRule{Type: "trigger", Name: "t", Collection: "blog",
		SearchType: "fts", Query: "golang", WebhookID: wh.ID, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	resp, err := gs.TestAutomation(ctx, &pb.TestAutomationRequest{Id: trig.ID})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Total != 1 || resp.Matches[0].Key != "go" || resp.Trigger.Id != trig.ID {
		t.Fatalf("test automation: %+v", resp)
	}

	// Without a documents bucket nothing can be loaded.
	if err := s.DBUpdateC("blog", func(tx *bolt.Tx) error { return tx.DeleteBucket([]byte("docs")) }); err != nil {
		t.Fatal(err)
	}
	resp, err = gs.TestAutomation(ctx, &pb.TestAutomationRequest{Id: trig.ID})
	if err != nil || resp.Total != 0 {
		t.Fatalf("no docs bucket: %v %+v", err, resp)
	}
}

func TestCovEGetAutomationLogs(t *testing.T) {
	gs, s := covEServer(t)
	ctx := context.Background()
	for i, st := range []string{"success", "error"} {
		if err := s.AutomationLogStore.Log(automationlog.Entry{RuleID: "r1", RuleName: "rule", RuleType: "trigger",
			Status: st, HTTPStatus: 200 + i, DurationMs: 7, Attempt: 1, Timestamp: time.Now().Unix() + int64(i)}); err != nil {
			t.Fatal(err)
		}
	}
	resp, err := gs.GetAutomationLogs(ctx, &pb.GetAutomationLogsRequest{RuleId: "r1"})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Total != 2 || len(resp.Logs) != 2 || resp.HasMore || resp.Logs[0].RuleName != "rule" || resp.Logs[0].DurationMs != 7 {
		t.Fatalf("logs: %+v", resp)
	}
	page, err := gs.GetAutomationLogs(ctx, &pb.GetAutomationLogsRequest{Limit: 1})
	if err != nil || len(page.Logs) != 1 || !page.HasMore || page.NextCursor == "" {
		t.Fatalf("paged logs: %v %+v", err, page)
	}

	covECloseDB(t, s)
	_, err = gs.GetAutomationLogs(ctx, &pb.GetAutomationLogsRequest{})
	covEWant(t, "closed db", err, codes.Internal, "database not open")
}

func TestCovECollectionConfigs(t *testing.T) {
	gs, s := covEServer(t)
	ctx := context.Background()

	_, err := gs.GetCollectionConfig(ctx, &pb.GetCollectionConfigRequest{})
	covEWant(t, "get without collection", err, codes.InvalidArgument, "missing collection")
	_, err = gs.SetCollectionConfig(ctx, &pb.SetCollectionConfigRequest{})
	covEWant(t, "set without collection", err, codes.InvalidArgument, "missing collection")
	_, err = gs.SetCollectionConfig(ctx, &pb.SetCollectionConfigRequest{Collection: "c", MaxRevisions: -1})
	covEWant(t, "negative revisions", err, codes.InvalidArgument, "max_revisions")

	for _, c := range []string{"acme/one", "other"} {
		if _, err := gs.SetCollectionConfig(ctx, &pb.SetCollectionConfigRequest{Collection: c, Description: c}); err != nil {
			t.Fatal(err)
		}
	}
	list, err := gs.ListCollectionConfigs(covEClaimsCtx(&JWTClaims{Username: "u", Tenant: "acme"}), &pb.ListCollectionConfigsRequest{})
	if err != nil || list.Total != 1 || list.Configs[0].Collection != "acme/one" {
		t.Fatalf("tenant listing: %v %+v", err, list)
	}

	covECloseDB(t, s)
	_, err = gs.SetCollectionConfig(ctx, &pb.SetCollectionConfigRequest{Collection: "c"})
	covEWant(t, "closed db", err, codes.Internal, "database not open")
}

func TestCovECrossSearchAdminSkipsEmptySource(t *testing.T) {
	_, g, cleanup := grpcParityServer(t)
	defer cleanup()
	covEDenyAll(t, g.server)
	// The empty source collection is not a permission check of its own.
	resp, err := g.CrossSearch(covEAdminCtx(), &pb.CrossSearchRequest{Query: "backup", TargetCollections: []string{"repo"}, SearchType: "fts"})
	if err != nil || len(resp.Results) == 0 || resp.Results[0].Collection != "repo" {
		t.Fatalf("cross search as admin: %v %+v", err, resp)
	}
}

func TestCovEFindDuplicates(t *testing.T) {
	_, g, cleanup := grpcParityServer(t)
	defer cleanup()
	s := g.server
	ctx := context.Background()
	for _, k := range []string{"a", "b"} {
		doc := addTestDoc(t, s, "dup", k, "en", "the very same body", nil)
		waitForEmbedding(t, s, "dup", doc.ID)
	}

	_, err := g.FindDuplicates(ctx, &pb.FindDuplicatesRequest{Collection: "dup", Mode: "nope"})
	covEWant(t, "bad mode", err, codes.InvalidArgument, "mode must be")

	resp, err := g.FindDuplicates(ctx, &pb.FindDuplicatesRequest{Collection: "dup", IncludeContent: true})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Mode != "both" || resp.Threshold != 0.9 || resp.TotalDocuments != 2 || len(resp.ExactGroups) != 1 ||
		len(resp.ExactGroups[0].Documents) != 2 || resp.ExactGroups[0].Documents[0].ContentMd != "the very same body" {
		t.Fatalf("duplicates: %+v", resp)
	}

	covECloseDB(t, s)
	_, err = g.FindDuplicates(ctx, &pb.FindDuplicatesRequest{Collection: "dup"})
	covEWant(t, "closed db", err, codes.Internal, "database not open")
}

// covEPutRev writes one raw revision entry.
func covEPutRev(t *testing.T, s *Server, coll, docID, suffix string, val []byte) {
	t.Helper()
	if err := s.DBUpdateC(coll, func(tx *bolt.Tx) error {
		return tx.Bucket([]byte("rev")).Put(append(storage.RevPrefix(coll, docID), suffix...), val)
	}); err != nil {
		t.Fatal(err)
	}
}

func TestCovEListRevisions(t *testing.T) {
	gs, s := covEServer(t)
	ctx := context.Background()
	id := genID("c", "k", "en")

	_, err := gs.ListRevisions(ctx, &pb.ListRevisionsRequest{Collection: "c", Key: "k"})
	covEWant(t, "missing lang", err, codes.InvalidArgument, "missing required fields")

	for i, body := range []string{"first", "second"} {
		d := storage.Doc{ID: id, Key: "k", Lang: "en", ContentMD: body, UpdatedAt: int64(100 + i), Meta: map[string][]string{"v": {body}}}
		buf, err := marshalDoc(&d)
		if err != nil {
			t.Fatal(err)
		}
		covEPutRev(t, s, "c", id, fmt.Sprintf("%020d", int64(100+i)), buf)
	}
	covEPutRev(t, s, "c", id, "", []byte("x"))                                    // no timestamp
	covEPutRev(t, s, "c", id, "notanumber", []byte("x"))                          // unparsable timestamp
	covEPutRev(t, s, "c", id, fmt.Sprintf("%020d", 50), []byte{0xff, 0x00, 0x01}) // undecodable

	resp, err := gs.ListRevisions(ctx, &pb.ListRevisionsRequest{Collection: "c", Key: "k", Lang: "en"})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Total != 2 || resp.Revisions[0].Timestamp != 101 || resp.Revisions[0].ContentMd != "second" ||
		resp.Revisions[1].Meta["v"].Values[0] != "first" {
		t.Fatalf("revisions: %+v", resp.Revisions)
	}

	if err := s.DBUpdateC("c", func(tx *bolt.Tx) error { return tx.DeleteBucket([]byte("rev")) }); err != nil {
		t.Fatal(err)
	}
	resp, err = gs.ListRevisions(ctx, &pb.ListRevisionsRequest{Collection: "c", Key: "k", Lang: "en"})
	if err != nil || resp.Total != 0 {
		t.Fatalf("no rev bucket: %v %+v", err, resp)
	}
	_, err = gs.RestoreRevision(ctx, &pb.RestoreRevisionRequest{Collection: "c", Key: "k", Lang: "en", Timestamp: 100})
	covEWant(t, "restore without rev bucket", err, codes.NotFound, "revision not found")

	covECloseDB(t, s)
	_, err = gs.ListRevisions(ctx, &pb.ListRevisionsRequest{Collection: "c", Key: "k", Lang: "en"})
	covEWant(t, "closed db", err, codes.Internal, "database not open")
}

func TestCovERestoreRevision(t *testing.T) {
	gs, s := covEServer(t)
	ctx := context.Background()
	id := genID("c", "k", "en")

	_, err := gs.RestoreRevision(ctx, &pb.RestoreRevisionRequest{Collection: "c", Key: "k", Lang: "en"})
	covEWant(t, "no timestamp", err, codes.InvalidArgument, "missing required fields")

	d := storage.Doc{ID: id, Key: "k", Lang: "en", ContentMD: "old body", Meta: map[string][]string{"v": {"1"}}}
	buf, err := marshalDoc(&d)
	if err != nil {
		t.Fatal(err)
	}
	covEPutRev(t, s, "c", id, fmt.Sprintf("%020d", 42), buf)

	// A schema the revision no longer satisfies makes the write fail.
	if err := s.SchemaManager.Set("c", `{"required":["title"]}`); err != nil {
		t.Fatal(err)
	}
	_, err = gs.RestoreRevision(ctx, &pb.RestoreRevisionRequest{Collection: "c", Key: "k", Lang: "en", Timestamp: 42})
	covEWant(t, "schema refuses", err, codes.Internal, "title")

	if err := s.SchemaManager.Delete("c"); err != nil {
		t.Fatal(err)
	}
	doc, err := gs.RestoreRevision(ctx, &pb.RestoreRevisionRequest{Collection: "c", Key: "k", Lang: "en", Timestamp: 42})
	if err != nil || doc.ContentMd != "old body" || doc.Meta["v"].Values[0] != "1" {
		t.Fatalf("restore: %v %+v", err, doc)
	}
}
