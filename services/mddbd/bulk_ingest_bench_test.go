package main

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"mddb/internal/cache"
	"mddb/internal/fts"
	"mddb/proto"

	bolt "go.etcd.io/bbolt"
)

// GO-027 baseline. The bulk path pushes each chunk through
// processBatchWithDocs and then firePostBatchHooks, and the FTS hooks open a
// write transaction per document per index — three of them — so the cost
// scales with documents times indexes rather than with the job.
//
// Run: go test -bench BenchmarkBulkIngest -benchmem -run '^$' .

func newBenchServer(b *testing.B) (*Server, func()) {
	b.Helper()
	dir := b.TempDir()
	db, err := bolt.Open(dir+"/bench.db", 0600, nil)
	if err != nil {
		b.Fatal(err)
	}
	s := &Server{
		DB:   db,
		Path: dir + "/bench.db",
		Mode: ModeRW,
		BucketNames: BucketNames{
			Docs:    []byte("docs"),
			IdxMeta: []byte("idxmeta"),
			Rev:     []byte("rev"),
			ByKey:   []byte("bykey"),
		},
		Cache: cache.NewDocumentCache(100, 60),
	}
	if err := s.ensureBuckets(); err != nil {
		b.Fatal(err)
	}
	s.FTSIndex = fts.NewFTSIndex(db)
	if err := s.FTSIndex.EnsureBuckets(); err != nil {
		b.Fatal(err)
	}
	langReg := fts.NewLangRegistry("en")
	fts.RegisterDefaultLanguages(langReg)
	s.FTSIndex.SetStemmer(fts.NewPorterStemmer())
	s.FTSIndex.SetLangRegistry(langReg)

	return s, func() { _ = db.Close() }
}

func benchDocs(n int) []*proto.BatchDocument {
	body := strings.Repeat("The quick brown fox jumps over the lazy dog near the riverbank. ", 12)
	docs := make([]*proto.BatchDocument, n)
	for i := range docs {
		docs[i] = &proto.BatchDocument{
			Key:       fmt.Sprintf("guides/page-%06d", i),
			Lang:      "en",
			ContentMd: body,
			Meta: map[string]*proto.MetaValues{
				"tags":     {Values: []string{"guide", "docs"}},
				"category": {Values: []string{"documentation"}},
			},
		}
	}
	return docs
}

// benchmarkBulk runs the write plus the indexing hooks the bulk job fires,
// reporting documents per second so the two modes compare directly.
func benchmarkBulk(b *testing.B, n int, opts postBatchOptions) {
	ctx := context.Background()
	b.ReportAllocs()
	for b.Loop() {
		b.StopTimer()
		s, cleanup := newBenchServer(b)
		docs := benchDocs(n)
		b.StartTimer()

		for i := 0; i < len(docs); i += bulkJobChunkSize {
			end := min(i+bulkJobChunkSize, len(docs))
			chunk := docs[i:end]
			_, processed, err := s.processBatchWithDocs(ctx, "bench", chunk)
			if err != nil {
				b.Fatal(err)
			}
			s.firePostBatchHooks("bench", processed, opts)
		}

		b.StopTimer()
		cleanup()
		b.StartTimer()
	}
	b.ReportMetric(float64(n)/b.Elapsed().Seconds()*float64(b.N), "docs/s")
}

// The current behaviour: FTS indexed inline, per document, per chunk.
func BenchmarkBulkIngest1000Inline(b *testing.B) {
	benchmarkBulk(b, 1000, postBatchOptions{})
}

// The same write path with FTS skipped, which isolates how much of the cost
// is the per-document index transactions rather than the document write.
func BenchmarkBulkIngest1000NoFTS(b *testing.B) {
	benchmarkBulk(b, 1000, postBatchOptions{SkipFTS: true})
}
