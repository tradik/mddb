package main

import (
	"context"
	"slices"
	"strings"
	"testing"

	proto "mddb/proto"
)

// INT-022 over gRPC and MCP: a cron rule's sync block travels through the
// same validation and secret masking as a REST body.

func TestGRPCSyncRuleRoundTrip(t *testing.T) {
	s := newBackupTestServer(t)
	g := &GRPCServer{server: s}
	ctx := context.Background()
	in := &proto.SyncActionProto{Source: SyncSourceS3, Collection: "kb", Lang: "en", Url: "u",
		Urls: []string{"a"}, Bucket: "docs", Prefix: "site/", Prune: true, MaxDocuments: 5, MaxBytes: 1024,
		StorageConfig: &proto.StorageConfigProto{Endpoint: "e:9000", AccessKey: "k", SecretKey: "secret"}}
	created, err := g.CreateAutomation(ctx, &proto.CreateAutomationRequest{Rule: &proto.AutomationRuleProto{
		Type: "cron", Name: "kb", Enabled: true, Schedule: "0 * * * *", Sync: in}})
	if err != nil {
		t.Fatal(err)
	}
	got := created.GetSync()
	if got.GetSource() != SyncSourceS3 || got.GetCollection() != "kb" || got.GetLang() != "en" || got.GetUrl() != "u" ||
		!slices.Equal(got.GetUrls(), []string{"a"}) || got.GetBucket() != "docs" || got.GetPrefix() != "site/" ||
		!got.GetPrune() || got.GetMaxDocuments() != 5 || got.GetMaxBytes() != 1024 {
		t.Errorf("read back: %+v", got)
	}
	if sc := got.GetStorageConfig(); sc.GetSecretKey() != "" || !sc.GetSecretKeySet() || sc.GetEndpoint() != "e:9000" {
		t.Errorf("storage config: %+v", sc)
	}
	if stored := s.AutomationManager.stored(created.GetId()); stored.Sync.StorageConfig.SecretKey != "secret" {
		t.Error("the secret was not stored")
	}
	if syncActionToProto(nil) != nil || syncActionFromProto(nil) != nil {
		t.Error("nil converted to something")
	}
}

func TestMCPSyncRuleArgs(t *testing.T) {
	rule, err := mcpArgsToAutomationRule(map[string]interface{}{"type": "cron", "name": "n", "schedule": "0 * * * *",
		"sync": map[string]interface{}{"source": "urls", "urls": []interface{}{"https://example.com/"},
			"collection": "c", "lang": "en", "prune": true, "maxDocuments": 7.0}})
	if err != nil || rule.Sync == nil || !rule.Sync.Prune || rule.Sync.MaxDocuments != 7 || rule.Sync.URLs[0] != "https://example.com/" {
		t.Fatalf("%+v, %v", rule.Sync, err)
	}
	if _, err := mcpArgsToAutomationRule(map[string]interface{}{"sync": map[string]interface{}{"prune": "yes"}}); err == nil ||
		!strings.Contains(err.Error(), "sync") {
		t.Errorf("a sync field of the wrong type: %v", err)
	}
	for _, tool := range []string{"create_automation", "update_automation"} {
		ts := &MCPToolServer{client: &mcpAdminFake{}, globalMode: ModeRW}
		if _, err := ts.mcpCallTool(context.Background(), tool, map[string]interface{}{"id": "x", "sync": map[string]interface{}{"urls": "one"}}); err == nil {
			t.Errorf("%s accepted a broken sync", tool)
		}
	}
}
