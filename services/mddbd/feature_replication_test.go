package main

import (
	"testing"

	"mddb/internal/binlog"
)

// GO-049: a follower applies a replicated switch and reloads the store, so
// the module is off there too; a follower without the store ignores it.
func TestFeatureSwitchReplicates(t *testing.T) {
	t.Setenv(featuresForceOffEnv, "")
	s, cleanup := applierExtraTestServer(t)
	defer cleanup()
	applier := NewReplicationApplier(s)
	val := []byte(`{"name":"geo","enabled":false,"updatedAt":"2026-11-11T10:00:00Z"}`)
	entry := &binlog.BinlogEntry{LSN: 1, Type: binlog.BinlogPut, BucketName: string(bucketFeatures), Key: []byte(FeatureGeo), Value: val}
	if err := applier.Apply(entry); err != nil {
		t.Fatalf("without the store: %v", err)
	}
	s.Features = NewFeatureManager(s.DB)
	if err := s.Features.LoadAll(); err != nil {
		t.Fatal(err)
	}
	entry.LSN = 2
	if err := applier.Apply(entry); err != nil {
		t.Fatal(err)
	}
	if s.Features.Enabled(FeatureGeo) {
		t.Error("the replicated switch is applied")
	}
}
