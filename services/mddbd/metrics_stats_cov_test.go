package main

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"mddb/internal/binlog"
	"mddb/internal/embedding"
)

func TestCovAMetricsStatsEmbeddingQueueSize(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	a := &serverMetricsStats{s: s}

	if _, present := a.EmbeddingQueueSize(); present {
		t.Fatal("no worker: queue must be reported absent")
	}
	t.Setenv("MDDB_EMBEDDING_QUEUE_WAIT", "0")
	s.EmbeddingWorker = NewEmbeddingWorker(&mockEmbedding{dims: 4, model: "m"}, s.VectorStore, s.VectorIndex, 4)
	s.EmbeddingWorker.Enqueue(EmbeddingJob{Collection: "c", DocID: "a"})
	s.EmbeddingWorker.Enqueue(EmbeddingJob{Collection: "c", DocID: "b"})
	if n, present := a.EmbeddingQueueSize(); !present || n != 2 {
		t.Fatalf("queue = %d (present %v), want 2", n, present)
	}
}

func TestCovAMetricsStatsEmbeddingCacheStats(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	a := &serverMetricsStats{s: s}

	s.Embedding = &mockEmbedding{dims: 4, model: "m"}
	if _, _, _, present := a.EmbeddingCacheStats(); present {
		t.Fatal("an uncached provider must report no cache")
	}

	s.Embedding = embedding.NewCachingProvider(&mockEmbedding{dims: 4, model: "m"}, 10, time.Minute)
	ctx := context.Background()
	_, _ = s.Embedding.Embed(ctx, "x", embedding.RoleQuery) // miss
	_, _ = s.Embedding.Embed(ctx, "x", embedding.RoleQuery) // hit
	hits, misses, size, present := a.EmbeddingCacheStats()
	if !present || hits != 1 || misses != 1 || size != 1 {
		t.Fatalf("cache stats = %d/%d/%d present=%v, want 1/1/1 true", hits, misses, size, present)
	}
}

func TestCovAMetricsStatsBinlogStats(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	a := &serverMetricsStats{s: s}

	if _, present := a.BinlogStats(); present {
		t.Fatal("no binlog: stats must be absent")
	}
	bl, err := binlog.NewBinlog(filepath.Join(t.TempDir(), "x.db"), binlog.BinlogConfig{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = bl.Close() }()
	if err := bl.Append(&binlog.BinlogEntry{BucketName: "docs", Key: []byte("k"), Value: []byte("v")}); err != nil {
		t.Fatal(err)
	}
	s.Binlog = bl
	v, present := a.BinlogStats()
	if !present || v.CurrentLSN != bl.CurrentLSN() || v.CurrentLSN == 0 {
		t.Fatalf("binlog view = %+v present=%v, want LSN %d", v, present, bl.CurrentLSN())
	}
}

func TestCovAMetricsStatsDBStatsCached(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	a := &serverMetricsStats{s: s}

	first := a.DBStats()
	stamp := a.cacheStamp
	// Dropping the webhook manager must not show until the cache expires.
	s.WebhookManager = nil
	second := a.DBStats()
	if a.cacheStamp != stamp {
		t.Fatal("a second call within 15s must use the cache")
	}
	if second.SizeBytes != first.SizeBytes || second.WebhookCount != first.WebhookCount {
		t.Fatalf("cached stats differ: %+v vs %+v", first, second)
	}
}
