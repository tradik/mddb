package main

import (
	"context"
	"fmt"
	"testing"

	"mddb/internal/binlog"
	"mddb/internal/storage"
	vec "mddb/internal/vector"
)

// RAG-009: summary vectors never mix with chunk vectors — not when loaded,
// replicated, counted, or searched in a disk-only collection.

func TestSummaryIndex_LoadReplicateAndStayApart(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	if err := s.VectorStore.PutChunks("c", "d1", []vec.ChunkEmbedding{{ChunkIndex: 0, Vector: []float32{0, 1, 0}}}, "m", "h"); err != nil {
		t.Fatal(err)
	}
	if err := s.VectorStore.PutSummary("c", "d1", []float32{1, 0, 0}, "m", "sh"); err != nil {
		t.Fatal(err)
	}
	_ = s.summaries().add("stale", "x", []float32{1, 0, 0}, false)
	s.loadVectorIndex()
	if s.summaries().size("stale") != 0 {
		t.Fatal("a reload kept what the store does not have")
	}
	if !s.summaries().has("c", "d1") || s.VectorIndex.CollectionSize("c") != 1 {
		t.Fatalf("summary indexed %v, chunk index holds %d", s.summaries().has("c", "d1"), s.VectorIndex.CollectionSize("c"))
	}
	if counts, _ := s.VectorStore.CountChunksByCollection(); counts["c"] != 1 {
		t.Fatalf("the summary was counted as a chunk: %v", counts)
	}

	// Replication: a summary entry reaches the summary index, not the chunks.
	ra := NewReplicationApplier(s)
	rec := vec.MarshalEmbeddingRecord(&vec.EmbeddingRecord{DocID: "d2", Vector: []float32{0, 0, 1}, Dimensions: 3})
	ra.applyVector(&binlog.BinlogEntry{Type: binlog.BinlogPut, BucketName: "vectors", Key: vec.SummaryKey("c", "d2"), Value: rec})
	if !s.summaries().has("c", "d2") || s.VectorIndex.CollectionSize("c") != 1 {
		t.Fatal("a replicated summary went to the wrong index")
	}
	ra.applyVector(&binlog.BinlogEntry{Type: binlog.BinlogDelete, BucketName: "vectors", Key: vec.SummaryKey("c", "d2")})
	if s.summaries().has("c", "d2") {
		t.Fatal("a replicated summary delete was ignored")
	}
	s.applyReplicatedSummary(&binlog.BinlogEntry{Type: binlog.BinlogPut, Key: []byte("vsum|short")})
	s.applyReplicatedSummary(&binlog.BinlogEntry{Type: binlog.BinlogPut, Key: vec.SummaryKey("c", "d3"), Value: []byte{1}})
	if s.summaries().has("c", "d3") {
		t.Fatal("an undecodable summary was indexed")
	}
}

// A disk-only collection keeps its summaries quantized in RAM and rescores
// them from disk; the chunks of a summary hit come from disk too.
func TestSummaryIndex_DiskOnlyAndFiltered(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	for _, sr := range s.VectorSearchers {
		sr.SetReady()
	}
	s.Embedding = summaryEmbedding{}
	if err := s.CollectionManager.Set("disk", &CollectionConfig{Quantization: "int8", DiskOnlyVectors: true,
		Retrieval: &RetrievalProfileDef{SummaryMode: SummaryModeExpand}}); err != nil {
		t.Fatal(err)
	}
	ix := serverIndexes{s}
	ctx := context.Background()
	q, _ := summaryEmbedding{}.Embed(ctx, generalQuery, 0)
	for i, d := range []struct{ key, content, summary string }{
		{"plant", plantContent, plantSummary},
		{"garden", "Tomatoes need sun.", "Gardening basics."},
	} {
		doc := addTestDoc(t, s, "disk", d.key, "en", d.content, map[string][]string{"tag": {d.key}})
		for c, chunk := range []string{d.content, d.content + " more"} {
			v, _ := summaryEmbedding{}.Embed(ctx, chunk, 0)
			_ = s.VectorStore.PutChunks("disk", doc.ID, []vec.ChunkEmbedding{{ChunkIndex: c, Vector: v}}, "m", "h")
			_ = ix.IndexChunk("disk", fmt.Sprintf("%s#%d", doc.ID, c), v)
		}
		sv, _ := summaryEmbedding{}.Embed(ctx, d.summary, 0)
		_ = s.VectorStore.PutSummary("disk", doc.ID, sv, "m", "h")
		if err := ix.IndexSummary("disk", doc.ID, sv); err != nil {
			t.Fatal(i, err)
		}
	}
	if s.summaries().flat.CollectionSize("disk") != 0 {
		t.Fatal("a disk-only collection's summaries reached the float32 index")
	}
	hits := s.searchSummaries("disk", q, 5, 0, nil, false, nil)
	if len(hits) == 0 || hits[0].Score < 0.5 {
		t.Fatalf("disk-only summary search: %+v", hits)
	}
	allowed, _ := s.allowedDocIDs("disk", map[string][]string{"tag": {"garden"}}, nil)
	if hits := s.searchSummaries("disk", q, 5, 0.3, allowed, true, nil); len(hits) != 0 {
		t.Fatalf("the filter admitted the plant: %+v", hits)
	}
	if hits := s.searchSummaries("disk", q, 5, 0.99, nil, false, nil); len(hits) != 0 {
		t.Fatalf("threshold: %+v", hits)
	}
	m := map[string]any{"collection": "disk", "query": generalQuery, "threshold": 0.3, "format": "passages"}
	rec := doRequest(t, s.handleVectorSearch, m)
	if keys := sumPassageKeys(body(t, rec.Body.Bytes())); len(keys) != 2 || keys[0] != "plant" {
		t.Fatalf("disk-only expand: %s", rec.Body.String())
	}

	// Flat, filtered.
	_ = s.summaries().add("flat", "a", q, false)
	if hits := s.searchSummaries("flat", q, 5, 0, map[string]bool{"b": true}, true, nil); len(hits) != 0 {
		t.Fatalf("flat filter: %+v", hits)
	}
	if got := s.bestChunks("flat", "a", q, nil, 3); len(got) != 1 || got[0] != 0 {
		t.Fatalf("a document with no chunks: %v", got)
	}
	_ = s.VectorIndex.Add("flat", "a#0", []float32{1, 0})
	_ = s.VectorIndex.Add("flat", "a#1", q)
	if got := s.bestChunks("flat", "a", q, nil, 1); len(got) != 1 || got[0] != 1 {
		t.Fatalf("best chunk with the default metric: %v", got)
	}
	// A collection without summaries: nothing to search, nothing quantized.
	if s.searchSummaries("plain", q, 5, 0, nil, false, nil) != nil || s.collectionQuantType("plain") != vec.QuantNone {
		t.Fatal("a collection without summaries")
	}
}

func TestSummaryIndex_Small(t *testing.T) {
	if documentSummary(&storage.Doc{Meta: map[string][]string{"summary": {"  a", "b "}}}) != "a b" {
		t.Fatal("documentSummary")
	}
	s := &Server{}
	if s.collectionQuantType("x") != vec.QuantNone {
		t.Fatal("no manager")
	}
	x := s.summaries()
	if err := x.add("c", "d", []float32{}, false); err == nil || x.has("c", "d") {
		t.Fatal("an empty vector was indexed")
	}
	s.dropDocumentSummary("c", "d") // no store: the index alone
}
