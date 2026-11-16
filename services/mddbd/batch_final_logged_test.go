package main

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"mddb/internal/cache"
	"mddb/proto"
)

// The extreme-mode batch path wrote documents without a single binlog
// entry: nothing it stored reached a follower, and on a node that did not
// lead it committed anyway. It now commits as the standard path does.
func TestFinalBatchIsReplicatedAndRefusedOffTheLeader(t *testing.T) {
	s, bl := orderServer(t)
	bl.SetWriteGate(nil)
	s.UseExtreme = true
	fbp := NewFinalBatchProcessor(s, 2)
	docs := []*proto.BatchDocument{
		covHBatchDoc("a", "body a", map[string]string{"tag": "x"}, true),
		covHBatchDoc("b", "body b", nil, false),
	}
	resp, err := fbp.ProcessBatch(context.Background(), "c", docs)
	if err != nil || resp.Added != 2 {
		t.Fatalf("batch: %v %+v", err, resp)
	}
	// What committed warms the cache, the lock-free one in extreme mode.
	if _, ok := s.Cache.Get(cache.BuildCacheKey("c", "a", "en")); !ok {
		t.Error("the committed document is not cached")
	}
	s.LockFreeCache = cache.NewLockFreeCache(10, 60)
	defer s.LockFreeCache.Close()
	if _, err := fbp.ProcessBatch(context.Background(), "c", docs[1:]); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.LockFreeCache.Get(cache.BuildCacheKey("c", "b", "en")); !ok {
		t.Error("the committed document is not in the lock-free cache")
	}

	f := newHandlerTestServerAt(t, filepath.Join(t.TempDir(), "follower.db"))
	entries, err := bl.ReadFrom(0)
	if err != nil {
		t.Fatal(err)
	}
	if err := NewReplicationApplier(f).ApplyBatch(entries); err != nil {
		t.Fatal(err)
	}
	leader, follower := nodeContent(t, s), nodeContent(t, f)
	if len(leader) == 0 || len(leader) != len(follower) {
		t.Fatalf("leader holds %d keys, a follower replaying its log %d", len(leader), len(follower))
	}
	for k, v := range leader {
		if follower[k] != v {
			t.Fatalf("the follower differs at %s", k)
		}
	}

	gate := errors.New("not the leader")
	bl.SetWriteGate(func() error { return gate })
	before := bl.CurrentLSN()
	resp, err = fbp.ProcessBatch(context.Background(), "c", []*proto.BatchDocument{covHBatchDoc("z", "body z", nil, false)})
	if err != nil || resp.Added != 0 || resp.Failed != 1 {
		t.Fatalf("a batch on a node that does not lead: %v %+v", err, resp)
	}
	if bl.CurrentLSN() != before || len(nodeContent(t, s)) != len(leader) {
		t.Fatal("a refused batch was committed")
	}
}

// A document the store cannot encode fails alone.
func TestFinalBatchDocumentThatCannotBeEncoded(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	fbp := NewFinalBatchProcessor(s, 1)
	p := fbp.processDocumentFast("c", &proto.BatchDocument{Key: "k", Lang: "en", ContentMd: "\xff\xfe"}, nil, 1)
	if p.Error == nil {
		t.Fatal("encoded a document that is not valid UTF-8")
	}
}
