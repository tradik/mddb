package main

import (
	"os"
	"sync"
	"time"

	bolt "go.etcd.io/bbolt"

	"mddb/internal/embedding"
	"mddb/internal/metrics"
)

// serverMetricsStats adapts *Server to metrics.StatsProvider, supplying the
// server-derived gauges for the /metrics endpoint. It inverts the former
// Metrics->*Server dependency so the metrics package stays importable.
type serverMetricsStats struct {
	s *Server

	cacheMu    sync.Mutex
	cachedDB   *metrics.DBStats
	cacheStamp time.Time
}

func (a *serverMetricsStats) Mode() string { return string(a.s.Mode) }

func (a *serverMetricsStats) VectorIndexReady() bool {
	return a.s.VectorIndex != nil && a.s.VectorIndex.IsReady()
}

func (a *serverMetricsStats) EmbeddingConfigured() bool { return a.s.Embedding != nil }

func (a *serverMetricsStats) EmbeddingQueueSize() (int, bool) {
	if a.s.EmbeddingWorker == nil {
		return 0, false
	}
	return len(a.s.EmbeddingWorker.jobs), true
}

// EmbeddingCacheStats reports the embedding cache when one is in use (RAG-003).
//
// The provider is wrapped only when caching is enabled, so the type assertion
// is also the "is it on" test — no second flag to keep in step with the wiring.
func (a *serverMetricsStats) EmbeddingCacheStats() (uint64, uint64, int, bool) {
	cache, ok := a.s.Embedding.(*embedding.CachingProvider)
	if !ok {
		return 0, 0, 0, false
	}
	hits, misses, size := cache.Stats()
	return hits, misses, size, true
}

func (a *serverMetricsStats) ReplicationRole() string { return a.s.ReplicationRole }

// RetrievalGain reports the gain counters since start (AGT-004).
func (a *serverMetricsStats) RetrievalGain() (metrics.RetrievalGainView, bool) {
	if a.s.Gain == nil {
		return metrics.RetrievalGainView{}, false
	}
	return a.s.Gain.Lifetime(), true
}

func (a *serverMetricsStats) BinlogStats() (metrics.BinlogStatsView, bool) {
	if a.s.Binlog == nil {
		return metrics.BinlogStatsView{}, false
	}
	b := a.s.Binlog.Stats()
	return metrics.BinlogStatsView{
		CurrentLSN:  b.CurrentLSN,
		FileSize:    b.FileSize,
		OldestLSN:   b.OldestLSN,
		Subscribers: b.Subscribers,
	}, true
}

// DBStats gathers per-collection counts and DB-derived gauges, cached for 15s
// (the BoltDB scan is expensive).
func (a *serverMetricsStats) DBStats() metrics.DBStats {
	a.cacheMu.Lock()
	defer a.cacheMu.Unlock()

	if a.cachedDB != nil && time.Since(a.cacheStamp) < 15*time.Second {
		return *a.cachedDB
	}

	ds := metrics.DBStats{Collections: make(map[string]metrics.CollectionStats)}

	if info, err := os.Stat(a.s.Path); err == nil {
		ds.SizeBytes = info.Size()
	}

	// #349: the shared snapshot. A scrape never waits for a walk; until the
	// first one finishes the per-collection gauges are simply absent.
	if snap := a.s.dbStats(0); snap != nil {
		for name, cc := range snap.Collections {
			ds.Collections[name] = metrics.CollectionStats{Documents: cc.Documents, Revisions: cc.Revisions,
				MetaIndex: cc.MetaIndices, Embeddings: cc.Embeddings}
		}
	}
	if a.s.WebhookManager != nil {
		ds.WebhookCount = len(a.s.WebhookManager.List())
	}
	if a.s.SchemaManager != nil {
		ds.SchemaCount = len(a.s.SchemaManager.List())
	}
	ds.Fullness, ds.EmbeddingBudgetUsed = a.s.quotaGauges() // GO-043

	a.cachedDB = &ds
	a.cacheStamp = time.Now()
	return ds
}

// DBWriteTransactions reads the committed transaction ID from the database
// meta page. bbolt advances it by one per write commit and never otherwise, so
// it is an exact commit counter at the cost of one empty read transaction.
// With one file per collection (#336) it is the sum over the main file and
// every collection file.
func (a *serverMetricsStats) DBWriteTransactions() uint64 {
	var total int
	_ = a.s.DBView(func(tx *bolt.Tx) error {
		total = tx.ID()
		return nil
	})
	if a.s.collectionStore() != nil {
		_ = a.s.DBViewAll(func(tx *bolt.Tx) error {
			total += tx.ID()
			return nil
		})
	}
	return uint64(max(total, 0))
}
