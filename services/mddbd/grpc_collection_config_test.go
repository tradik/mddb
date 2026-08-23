package main

import (
	"context"
	"testing"

	pb "mddb/proto"
)

// GO-035: CollectionConfigProto carried 8 of CollectionConfig's 18 fields, so
// a gRPC client could neither read nor set the other ten. These tests pin the
// two properties that closing that gap must not break: every field survives a
// round trip, and setting one field leaves the rest alone.

// fullConfig is a collection config with every field set to something
// distinguishable, so a field dropped in conversion shows up as a mismatch
// rather than as a zero that happens to match the default.
func fullConfig() *CollectionConfig {
	return &CollectionConfig{
		Type:            "website",
		Description:     "every field populated",
		Icon:            "📚",
		Color:           "#3b82f6",
		CustomMeta:      map[string]string{"team": "search"},
		StorageBackend:  "s3",
		StorageConfig:   &StorageConfigDef{Endpoint: "s3.example.com", Bucket: "docs", Region: "eu-central-1", AccessKey: "AKIA", SecretKey: "s3cret", Prefix: "mddb/", UseTLS: true},
		Quantization:    "int8",
		DiskOnlyVectors: true,
		TrackAccess:     true,
		TrackHot:        true,
		SpellCorrect:    true,
		SpellLang:       "pl",
		MaxRevisions:    7,
		Encrypted:       true,
		WordPress:       &WordPressTargetConfig{URL: "https://blog.example.com", APIKey: "wp-key"},
		ResponsePrompt:  "answer in numbered steps",
	}
}

func TestGRPCCollectionConfigRoundTripsEveryField(t *testing.T) {
	gs, _, cleanup := newTestGRPCServerFull(t)
	defer cleanup()

	want := fullConfig()
	if err := gs.server.CollectionManager.Set("everything", want); err != nil {
		t.Fatalf("seed: %v", err)
	}

	resp, err := gs.GetCollectionConfig(context.Background(), &pb.GetCollectionConfigRequest{Collection: "everything"})
	if err != nil {
		t.Fatalf("GetCollectionConfig: %v", err)
	}
	got := resp.GetConfig()
	if got == nil {
		t.Fatal("no config returned")
	}

	checks := []struct {
		field string
		got   any
		want  any
	}{
		{"type", got.GetType(), want.Type},
		{"description", got.GetDescription(), want.Description},
		{"icon", got.GetIcon(), want.Icon},
		{"color", got.GetColor(), want.Color},
		{"custom_meta[team]", got.GetCustomMeta()["team"], want.CustomMeta["team"]},
		{"max_revisions", int(got.GetMaxRevisions()), want.MaxRevisions},
		{"response_prompt", got.GetResponsePrompt(), want.ResponsePrompt},
		{"storage_backend", got.GetStorageBackend(), want.StorageBackend},
		{"quantization", got.GetQuantization(), want.Quantization},
		{"disk_only_vectors", got.GetDiskOnlyVectors(), want.DiskOnlyVectors},
		{"encrypted", got.GetEncrypted(), want.Encrypted},
		{"track_access", got.GetTrackAccess(), want.TrackAccess},
		{"track_hot", got.GetTrackHot(), want.TrackHot},
		{"spell_correct", got.GetSpellCorrect(), want.SpellCorrect},
		{"spell_lang", got.GetSpellLang(), want.SpellLang},
		{"storage_config.endpoint", got.GetStorageConfig().GetEndpoint(), want.StorageConfig.Endpoint},
		{"storage_config.bucket", got.GetStorageConfig().GetBucket(), want.StorageConfig.Bucket},
		{"storage_config.region", got.GetStorageConfig().GetRegion(), want.StorageConfig.Region},
		{"storage_config.access_key", got.GetStorageConfig().GetAccessKey(), want.StorageConfig.AccessKey},
		{"storage_config.prefix", got.GetStorageConfig().GetPrefix(), want.StorageConfig.Prefix},
		{"storage_config.use_tls", got.GetStorageConfig().GetUseTls(), want.StorageConfig.UseTLS},
		{"wordpress.url", got.GetWordpress().GetUrl(), want.WordPress.URL},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s: got %v, want %v", c.field, c.got, c.want)
		}
	}
}

func TestGRPCCollectionConfigMasksSecrets(t *testing.T) {
	gs, _, cleanup := newTestGRPCServerFull(t)
	defer cleanup()

	if err := gs.server.CollectionManager.Set("secrets", fullConfig()); err != nil {
		t.Fatalf("seed: %v", err)
	}

	resp, err := gs.GetCollectionConfig(context.Background(), &pb.GetCollectionConfigRequest{Collection: "secrets"})
	if err != nil {
		t.Fatalf("GetCollectionConfig: %v", err)
	}
	cfg := resp.GetConfig()

	if got := cfg.GetStorageConfig().GetSecretKey(); got != "" {
		t.Errorf("S3 secret key was returned over gRPC: %q", got)
	}
	if got := cfg.GetWordpress().GetApiKey(); got != "" {
		t.Errorf("WordPress API key was returned over gRPC: %q", got)
	}
	// Masked is not the same as absent — a client configuring a collection has
	// to be able to tell "no credential" from "one I am not being shown".
	if !cfg.GetStorageConfig().GetSecretKeySet() {
		t.Error("secret_key_set should report that a secret is stored")
	}
	if !cfg.GetWordpress().GetApiKeySet() {
		t.Error("api_key_set should report that a key is stored")
	}
}

func TestGRPCCollectionConfigListMasksSecrets(t *testing.T) {
	gs, _, cleanup := newTestGRPCServerFull(t)
	defer cleanup()

	if err := gs.server.CollectionManager.Set("secrets", fullConfig()); err != nil {
		t.Fatalf("seed: %v", err)
	}

	resp, err := gs.ListCollectionConfigs(context.Background(), &pb.ListCollectionConfigsRequest{})
	if err != nil {
		t.Fatalf("ListCollectionConfigs: %v", err)
	}

	var found bool
	for _, entry := range resp.GetConfigs() {
		if entry.GetCollection() != "secrets" {
			continue
		}
		found = true
		if got := entry.GetConfig().GetStorageConfig().GetSecretKey(); got != "" {
			t.Errorf("listing leaked the S3 secret key: %q", got)
		}
		if got := entry.GetConfig().GetWordpress().GetApiKey(); got != "" {
			t.Errorf("listing leaked the WordPress API key: %q", got)
		}
		// The listing must carry the same fields as the single-config read.
		if !entry.GetConfig().GetEncrypted() {
			t.Error("listing dropped encrypted")
		}
		if entry.GetConfig().GetQuantization() != "int8" {
			t.Error("listing dropped quantization")
		}
	}
	if !found {
		t.Fatal("collection missing from the listing")
	}
}

// This is the RAG-001 regression, extended to the fields GO-035 added. It is
// the reason those booleans carry proto3 presence: a client changing an icon
// sends nothing for `encrypted`, and an unset bool arriving as false would
// turn encryption off on a collection whose owner never asked.
func TestGRPCSetCollectionConfigLeavesUnmentionedFieldsAlone(t *testing.T) {
	gs, _, cleanup := newTestGRPCServerFull(t)
	defer cleanup()

	if err := gs.server.CollectionManager.Set("keep", fullConfig()); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// A client updating nothing but the icon.
	if _, err := gs.SetCollectionConfig(context.Background(), &pb.SetCollectionConfigRequest{
		Collection: "keep",
		Type:       "website",
		Icon:       "🔧",
	}); err != nil {
		t.Fatalf("SetCollectionConfig: %v", err)
	}

	cfg, found := gs.server.CollectionManager.Get("keep")
	if !found {
		t.Fatal("collection disappeared")
	}
	if cfg.Icon != "🔧" {
		t.Errorf("icon should have changed, got %q", cfg.Icon)
	}

	want := fullConfig()
	if !cfg.Encrypted {
		t.Error("encryption was switched off by an unrelated update")
	}
	if !cfg.DiskOnlyVectors || !cfg.TrackAccess || !cfg.TrackHot || !cfg.SpellCorrect {
		t.Errorf("boolean settings were cleared: %+v", cfg)
	}
	if cfg.StorageBackend != want.StorageBackend || cfg.Quantization != want.Quantization || cfg.SpellLang != want.SpellLang {
		t.Errorf("string settings were cleared: %+v", cfg)
	}
	if cfg.StorageConfig == nil || cfg.StorageConfig.SecretKey != want.StorageConfig.SecretKey {
		t.Errorf("storage credentials were lost: %+v", cfg.StorageConfig)
	}
	if cfg.WordPress == nil || cfg.WordPress.APIKey != want.WordPress.APIKey {
		t.Errorf("WordPress key was lost: %+v", cfg.WordPress)
	}
}

// Presence has to work in both directions: a client that explicitly asks for
// false must get false, or the merge would make settings impossible to turn
// off over gRPC.
func TestGRPCSetCollectionConfigCanTurnBooleansOff(t *testing.T) {
	gs, _, cleanup := newTestGRPCServerFull(t)
	defer cleanup()

	if err := gs.server.CollectionManager.Set("off", fullConfig()); err != nil {
		t.Fatalf("seed: %v", err)
	}

	off := false
	if _, err := gs.SetCollectionConfig(context.Background(), &pb.SetCollectionConfigRequest{
		Collection:   "off",
		Type:         "website",
		Encrypted:    &off,
		TrackHot:     &off,
		SpellCorrect: &off,
	}); err != nil {
		t.Fatalf("SetCollectionConfig: %v", err)
	}

	cfg, _ := gs.server.CollectionManager.Get("off")
	if cfg.Encrypted || cfg.TrackHot || cfg.SpellCorrect {
		t.Errorf("explicit false was ignored: %+v", cfg)
	}
	// Untouched neighbours stay on, which is what separates "sent false" from
	// "not sent".
	if !cfg.TrackAccess || !cfg.DiskOnlyVectors {
		t.Errorf("neighbouring booleans were cleared: %+v", cfg)
	}
}

// The other direction, on the two booleans the "turn off" case deliberately
// leaves untouched — so every optional bool is exercised in both states.
func TestGRPCSetCollectionConfigCanTurnBooleansOn(t *testing.T) {
	gs, _, cleanup := newTestGRPCServerFull(t)
	defer cleanup()

	if err := gs.server.CollectionManager.Set("on", &CollectionConfig{Type: "default"}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	on := true
	if _, err := gs.SetCollectionConfig(context.Background(), &pb.SetCollectionConfigRequest{
		Collection:      "on",
		Type:            "default",
		DiskOnlyVectors: &on,
		TrackAccess:     &on,
	}); err != nil {
		t.Fatalf("SetCollectionConfig: %v", err)
	}

	cfg, _ := gs.server.CollectionManager.Get("on")
	if !cfg.DiskOnlyVectors || !cfg.TrackAccess {
		t.Errorf("explicit true was ignored: %+v", cfg)
	}
	if cfg.Encrypted || cfg.TrackHot || cfg.SpellCorrect {
		t.Errorf("unmentioned booleans were switched on: %+v", cfg)
	}
}

// The WordPress key follows the same round-trip rule as the S3 secret: a
// client that read the masked config and wrote back the target keeps its key.
func TestGRPCSetCollectionConfigKeepsTheWordPressKeyOnRoundTrip(t *testing.T) {
	gs, _, cleanup := newTestGRPCServerFull(t)
	defer cleanup()

	if err := gs.server.CollectionManager.Set("wp", fullConfig()); err != nil {
		t.Fatalf("seed: %v", err)
	}

	read, err := gs.GetCollectionConfig(context.Background(), &pb.GetCollectionConfigRequest{Collection: "wp"})
	if err != nil {
		t.Fatalf("GetCollectionConfig: %v", err)
	}
	target := read.GetConfig().GetWordpress()
	target.Url = "https://moved.example.com"

	if _, err := gs.SetCollectionConfig(context.Background(), &pb.SetCollectionConfigRequest{
		Collection: "wp",
		Type:       "website",
		Wordpress:  target,
	}); err != nil {
		t.Fatalf("SetCollectionConfig: %v", err)
	}

	cfg, _ := gs.server.CollectionManager.Get("wp")
	if cfg.WordPress.URL != "https://moved.example.com" {
		t.Errorf("URL should have changed, got %q", cfg.WordPress.URL)
	}
	if cfg.WordPress.APIKey != "wp-key" {
		t.Errorf("key lost on round trip, got %q", cfg.WordPress.APIKey)
	}
}

func TestGRPCSetCollectionConfigKeepsSecretsOnRoundTrip(t *testing.T) {
	gs, _, cleanup := newTestGRPCServerFull(t)
	defer cleanup()

	if err := gs.server.CollectionManager.Set("rt", fullConfig()); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// The read-modify-write a UI performs: read the config (secrets masked),
	// change the bucket, write it back. Without carry-over the mask is written
	// as the new secret and the credential is gone.
	read, err := gs.GetCollectionConfig(context.Background(), &pb.GetCollectionConfigRequest{Collection: "rt"})
	if err != nil {
		t.Fatalf("GetCollectionConfig: %v", err)
	}
	sc := read.GetConfig().GetStorageConfig()
	sc.Bucket = "renamed"

	if _, err := gs.SetCollectionConfig(context.Background(), &pb.SetCollectionConfigRequest{
		Collection:    "rt",
		Type:          "website",
		StorageConfig: sc,
	}); err != nil {
		t.Fatalf("SetCollectionConfig: %v", err)
	}

	cfg, _ := gs.server.CollectionManager.Get("rt")
	if cfg.StorageConfig.Bucket != "renamed" {
		t.Errorf("bucket should have changed, got %q", cfg.StorageConfig.Bucket)
	}
	if cfg.StorageConfig.SecretKey != "s3cret" {
		t.Errorf("secret key lost on round trip, got %q", cfg.StorageConfig.SecretKey)
	}
}

func TestGRPCSetCollectionConfigStoresANewSecret(t *testing.T) {
	gs, _, cleanup := newTestGRPCServerFull(t)
	defer cleanup()

	if err := gs.server.CollectionManager.Set("rotate", fullConfig()); err != nil {
		t.Fatalf("seed: %v", err)
	}

	if _, err := gs.SetCollectionConfig(context.Background(), &pb.SetCollectionConfigRequest{
		Collection:    "rotate",
		Type:          "website",
		StorageConfig: &pb.StorageConfigProto{Endpoint: "s3.example.com", Bucket: "docs", SecretKey: "rotated"},
		Wordpress:     &pb.WordPressTargetProto{Url: "https://blog.example.com", ApiKey: "new-wp-key"}, // #nosec G101 -- fake credential in a test fixture
	}); err != nil {
		t.Fatalf("SetCollectionConfig: %v", err)
	}

	cfg, _ := gs.server.CollectionManager.Get("rotate")
	if cfg.StorageConfig.SecretKey != "rotated" {
		t.Errorf("new secret was not stored, got %q", cfg.StorageConfig.SecretKey)
	}
	if cfg.WordPress.APIKey != "new-wp-key" {
		t.Errorf("new WordPress key was not stored, got %q", cfg.WordPress.APIKey)
	}
}

// --- converters, nil paths -------------------------------------------------
//
// Every converter here has a nil branch, and each one guards a different way a
// config can arrive incomplete: no storage block, no publishing target, a
// fresh collection with nothing stored yet.

func TestConvertersHandleNil(t *testing.T) {
	if storageConfigToProto(nil) != nil {
		t.Error("nil storage config should convert to nil")
	}
	if wordPressToProto(nil) != nil {
		t.Error("nil WordPress target should convert to nil")
	}
	if collectionConfigToProto(nil) != nil {
		t.Error("nil config should convert to nil")
	}

	// A config with no blocks converts without inventing them.
	bare := collectionConfigToProto(&CollectionConfig{Type: "default"})
	if bare.GetStorageConfig() != nil || bare.GetWordpress() != nil {
		t.Error("absent blocks should stay absent")
	}
	if bare.GetType() != "default" {
		t.Error("conversion dropped an unrelated field")
	}
}

func TestFromProtoNilMeansLeaveTheStoredValueAlone(t *testing.T) {
	storage := &StorageConfigDef{Bucket: "docs", SecretKey: "s3cret"}
	if got := storageConfigFromProto(nil, storage); got != storage {
		t.Error("a nil storage block should leave the stored one in place")
	}
	target := &WordPressTargetConfig{URL: "https://x.example.com", APIKey: "wp-key"}
	if got := wordPressFromProto(nil, target); got != target {
		t.Error("a nil target should leave the stored one in place")
	}
}

func TestFromProtoOnAFreshCollectionHasNothingToCarryOver(t *testing.T) {
	// Nothing stored yet: an empty secret stays empty rather than dereferencing
	// the config that is not there.
	storage := storageConfigFromProto(&pb.StorageConfigProto{Bucket: "docs"}, nil)
	if storage.Bucket != "docs" || storage.SecretKey != "" {
		t.Errorf("unexpected storage config: %+v", storage)
	}
	target := wordPressFromProto(&pb.WordPressTargetProto{Url: "https://x.example.com"}, nil)
	if target.URL != "https://x.example.com" || target.APIKey != "" {
		t.Errorf("unexpected target: %+v", target)
	}
}

func TestApplyRequestIgnoresEmptyStringsAndUnsetBooleans(t *testing.T) {
	cfg := fullConfig()
	before := *cfg

	// An entirely empty request: nothing is mentioned, so nothing changes.
	applyCollectionConfigRequest(cfg, &pb.SetCollectionConfigRequest{Collection: "x"})

	if cfg.StorageBackend != before.StorageBackend ||
		cfg.Quantization != before.Quantization ||
		cfg.SpellLang != before.SpellLang {
		t.Errorf("empty strings overwrote stored settings: %+v", cfg)
	}
	if !cfg.Encrypted || !cfg.DiskOnlyVectors || !cfg.TrackAccess || !cfg.TrackHot || !cfg.SpellCorrect {
		t.Errorf("unset booleans overwrote stored settings: %+v", cfg)
	}
	if cfg.StorageConfig != before.StorageConfig || cfg.WordPress != before.WordPress {
		t.Error("nil blocks replaced the stored ones")
	}
}

func TestApplyRequestSetsTheStringsItIsGiven(t *testing.T) {
	cfg := &CollectionConfig{Type: "default"}
	applyCollectionConfigRequest(cfg, &pb.SetCollectionConfigRequest{
		Collection:     "x",
		StorageBackend: "memory",
		Quantization:   "int8",
		SpellLang:      "pl",
	})
	if cfg.StorageBackend != "memory" || cfg.Quantization != "int8" || cfg.SpellLang != "pl" {
		t.Errorf("strings were not applied: %+v", cfg)
	}
}

func TestCarryOverSecretsOnANilConfigIsANoOp(t *testing.T) {
	// The caller reaches this when a request produced no config at all;
	// crashing there would turn a no-op into an outage.
	carryOverSecrets(nil, fullConfig())
}
