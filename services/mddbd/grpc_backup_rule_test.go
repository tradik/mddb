package main

import (
	"context"
	"path/filepath"
	"slices"
	"testing"
	"time"

	proto "mddb/proto"
)

// OPS-023 over gRPC: a cron rule's backup block travels in AutomationRuleProto,
// through the same validation and secret masking as REST.

// grpcS3Backup is the wire form of fakeS3Action, with every field set.
func grpcS3Backup(f *fakeS3) *proto.BackupActionProto {
	return &proto.BackupActionProto{
		Target: BackupTargetS3, Path: "unused", Bucket: "backups", Prefix: "prod/mddb-", Keep: 3,
		Collections: []string{"blog"},
		StorageConfig: &proto.StorageConfigProto{Endpoint: f.endpoint(), Region: "us-east-1",
			AccessKey: "test", SecretKey: "test-secret", SecretKeySet: true},
	}
}

// assertGRPCBackup checks every field of a backup read back over gRPC, and
// that the secret stays on the server.
func assertGRPCBackup(t *testing.T, where string, f *fakeS3, got *proto.BackupActionProto) {
	t.Helper()
	if got.GetTarget() != BackupTargetS3 || got.GetPath() != "unused" || got.GetBucket() != "backups" ||
		got.GetPrefix() != "prod/mddb-" || got.GetKeep() != 3 || !slices.Equal(got.GetCollections(), []string{"blog"}) {
		t.Errorf("%s: backup %+v", where, got)
	}
	sc := got.GetStorageConfig()
	if sc.GetEndpoint() != f.endpoint() || sc.GetRegion() != "us-east-1" || sc.GetAccessKey() != "test" || sc.GetUseTls() {
		t.Errorf("%s: storage config %+v", where, sc)
	}
	if sc.GetSecretKey() != "" || !sc.GetSecretKeySet() {
		t.Errorf("%s: secret key %q, secretKeySet %v", where, sc.GetSecretKey(), sc.GetSecretKeySet())
	}
}

func TestGRPCBackupRuleRoundTrip(t *testing.T) {
	s := newBackupTestServer(t)
	g := &GRPCServer{server: s}
	ctx := context.Background()
	f := newFakeS3(t)

	created, err := g.CreateAutomation(ctx, &proto.CreateAutomationRequest{Rule: &proto.AutomationRuleProto{
		Type: "cron", Name: "offsite", Enabled: true, Schedule: "0 3 * * *", Backup: grpcS3Backup(f)}})
	if err != nil {
		t.Fatal(err)
	}
	assertGRPCBackup(t, "create", f, created.GetBackup())

	got, err := g.GetAutomation(ctx, &proto.GetAutomationRequest{Id: created.GetId()})
	if err != nil {
		t.Fatal(err)
	}
	assertGRPCBackup(t, "get", f, got.GetBackup())

	list, err := g.ListAutomation(ctx, &proto.ListAutomationRequest{Type: "cron"})
	if err != nil || len(list.GetRules()) != 1 {
		t.Fatalf("list: %v %v", list, err)
	}
	assertGRPCBackup(t, "list", f, list.GetRules()[0].GetBackup())

	// The secret reaches storage; secretKeySet sent by the client does not.
	stored := s.AutomationManager.stored(created.GetId()).Backup.StorageConfig
	if stored.SecretKey != "test-secret" || stored.SecretKeySet {
		t.Errorf("stored storage config %+v", stored)
	}

	// Toggling `enabled` without the backup keeps it, secret and all.
	off, err := g.UpdateAutomation(ctx, &proto.UpdateAutomationRequest{Id: created.GetId(),
		Rule: &proto.AutomationRuleProto{Name: "offsite", Schedule: "0 3 * * *"}})
	if err != nil {
		t.Fatal(err)
	}
	if off.GetEnabled() {
		t.Error("the update did not disable the rule")
	}
	assertGRPCBackup(t, "update without backup", f, off.GetBackup())
	if sc := s.AutomationManager.stored(created.GetId()).Backup.StorageConfig; sc.SecretKey != "test-secret" {
		t.Errorf("an update without the backup lost the secret: %+v", sc)
	}

	// Sending the backup back as read (masked secret) keeps the stored secret.
	if _, err := g.UpdateAutomation(ctx, &proto.UpdateAutomationRequest{Id: created.GetId(),
		Rule: &proto.AutomationRuleProto{Name: "offsite", Enabled: true, Schedule: "0 3 * * *", Backup: got.GetBackup()}}); err != nil {
		t.Fatal(err)
	}
	if sc := s.AutomationManager.stored(created.GetId()).Backup.StorageConfig; sc.SecretKey != "test-secret" {
		t.Errorf("an update with the masked secret lost it: %+v", sc)
	}

	// Validation is the REST validation.
	bad := grpcS3Backup(f)
	bad.StorageConfig.Endpoint = ""
	if _, err := g.CreateAutomation(ctx, &proto.CreateAutomationRequest{Rule: &proto.AutomationRuleProto{
		Type: "cron", Name: "bad", Schedule: "0 3 * * *", Backup: bad}}); err == nil {
		t.Error("an s3 backup without an endpoint was accepted")
	}
	if _, err := g.CreateAutomation(ctx, &proto.CreateAutomationRequest{Rule: &proto.AutomationRuleProto{
		Type: "cron", Name: "both", Schedule: "0 3 * * *", WebhookId: "w", Backup: grpcS3Backup(f)}}); err == nil {
		t.Error("a rule with webhookId and backup was accepted")
	}
}

func TestGRPCBackupRuleRuns(t *testing.T) {
	s := newBackupTestServer(t)
	addTestDoc(t, s, "blog", "a", "en", "kept", nil)
	g := &GRPCServer{server: s}
	cs := NewCronScheduler(s)
	s.CronScheduler = cs

	created, err := g.CreateAutomation(context.Background(), &proto.CreateAutomationRequest{Rule: &proto.AutomationRuleProto{
		Type: "cron", Name: "nightly", Enabled: true, Schedule: "0 3 * * *",
		Backup: &proto.BackupActionProto{Path: "grpc", Keep: 2}}})
	if err != nil {
		t.Fatal(err)
	}
	if b := created.GetBackup(); b.GetTarget() != BackupTargetLocal || b.GetPrefix() != defaultBackupPrefix ||
		!slices.Equal(b.GetCollections(), []string{"*"}) || b.GetStorageConfig() != nil {
		t.Errorf("defaults not filled: %+v", b)
	}
	entry := cs.cron.Entry(cs.entryMap[created.GetId()])
	if !entry.Valid() {
		t.Fatal("the gRPC backup rule has no cron entry")
	}
	clock := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	cs.now = func() time.Time { return clock }
	for range 3 {
		clock = entry.Schedule.Next(clock)
		entry.Job.Run()
	}
	got := backupsIn(t, filepath.Join(backupDir(), "grpc"))
	want := []string{"mddb-20261031T030000Z.db", "mddb-20261101T030000Z.db"}
	if !slices.Equal(got, want) {
		t.Errorf("backups %v, want %v", got, want)
	}
}

func TestGRPCBackupActionNil(t *testing.T) {
	if backupActionToProto(nil) != nil || backupActionFromProto(nil) != nil {
		t.Error("a rule without a backup gained one")
	}
}
