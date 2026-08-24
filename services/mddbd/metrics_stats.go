package main

import (
	"bytes"
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

	_ = a.s.DBView(func(tx *bolt.Tx) error {
		if b := tx.Bucket([]byte("docs")); b != nil {
			c := b.Cursor()
			for k, _ := c.First(); k != nil; k, _ = c.Next() {
				if coll := metricsExtractCollection(k); coll != "" {
					cm := ds.Collections[coll]
					cm.Documents++
					ds.Collections[coll] = cm
				}
			}
		}
		if b := tx.Bucket([]byte("rev")); b != nil {
			c := b.Cursor()
			for k, _ := c.First(); k != nil; k, _ = c.Next() {
				if coll := metricsExtractCollection(k); coll != "" {
					cm := ds.Collections[coll]
					cm.Revisions++
					ds.Collections[coll] = cm
				}
			}
		}
		if b := tx.Bucket([]byte("idxmeta")); b != nil {
			c := b.Cursor()
			for k, _ := c.First(); k != nil; k, _ = c.Next() {
				if coll := metricsExtractCollection(k); coll != "" {
					cm := ds.Collections[coll]
					cm.MetaIndex++
					ds.Collections[coll] = cm
				}
			}
		}
		return nil
	})

	if a.s.VectorStore != nil {
		if counts, err := a.s.VectorStore.CountByCollection(); err == nil {
			for coll, n := range counts {
				cm := ds.Collections[coll]
				cm.Embeddings = n
				ds.Collections[coll] = cm
			}
		}
	}
	if a.s.WebhookManager != nil {
		ds.WebhookCount = len(a.s.WebhookManager.List())
	}
	if a.s.SchemaManager != nil {
		ds.SchemaCount = len(a.s.SchemaManager.List())
	}

	a.cachedDB = &ds
	a.cacheStamp = time.Now()
	return ds
}

// metricsExtractCollection pulls the collection name from a BoltDB key like "doc|blog|id".
func metricsExtractCollection(key []byte) string {
	i := bytes.IndexByte(key, '|')
	if i < 0 {
		return ""
	}
	rest := key[i+1:]
	j := bytes.IndexByte(rest, '|')
	if j < 0 {
		return string(rest)
	}
	return string(rest[:j])
}
