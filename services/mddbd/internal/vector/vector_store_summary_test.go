package vector

import (
	"path/filepath"
	"testing"

	"mddb/internal/binlog"

	bolt "go.etcd.io/bbolt"
)

// RAG-009: summary vectors live beside the chunks under their own prefix, and
// nothing that walks chunks sees them.

func TestSummaryVectorsStayApartFromChunks(t *testing.T) {
	vs, _ := openStore(t, true)
	bl, err := binlog.NewBinlog(filepath.Join(t.TempDir(), "b.db"), binlog.BinlogConfig{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = bl.Close() })
	vs.SetBinlog(bl)

	if err := vs.PutChunks("c", "d", []ChunkEmbedding{{ChunkIndex: 0, Vector: []float32{1, 0}}, {ChunkIndex: 1, Vector: []float32{0, 1}}}, "m", "h"); err != nil {
		t.Fatal(err)
	}
	if err := vs.PutChunksQuantized("c", "q", []ChunkEmbedding{{ChunkIndex: 0, Vector: []float32{1, 1}}}, "m", "h", QuantInt8); err != nil {
		t.Fatal(err)
	}
	before := bl.CurrentLSN()
	if err := vs.PutSummary("c", "d", []float32{0.5, 0.5}, "m", "sh"); err != nil {
		t.Fatal(err)
	}
	if bl.CurrentLSN() == before {
		t.Fatal("a summary write skipped the binlog")
	}
	if !IsSummaryKey(SummaryKey("c", "d")) || IsSummaryKey([]byte("vec|c|d#0")) {
		t.Fatal("IsSummaryKey")
	}

	if rec := vs.GetSummary("c", "d"); rec == nil || rec.ContentHash != "sh" || len(rec.Vector) != 2 {
		t.Fatalf("GetSummary: %+v", rec)
	}
	if vs.GetSummary("c", "none") != nil {
		t.Fatal("a missing summary was found")
	}
	if got := vs.LoadSummaries("c"); len(got) != 1 || got["d"] == nil {
		t.Fatalf("LoadSummaries: %v", got)
	}
	if got := vs.SummaryVectors("c", []string{"d", "none"}); len(got) != 1 {
		t.Fatalf("SummaryVectors: %v", got)
	}

	// Chunk paths do not see the summary.
	if recs, _ := vs.LoadCollection("c"); len(recs) != 3 {
		t.Fatalf("LoadCollection: %d records", len(recs))
	}
	if counts, _ := vs.CountChunksByCollection(); counts["c"] != 3 {
		t.Fatalf("chunk count: %v", counts)
	}
	if counts, _ := vs.CountByCollection(); counts["c"] != 2 {
		t.Fatalf("document count: %v", counts)
	}
	vs.CleanStaleChunks("c", "d", 1, nil)
	if vs.GetSummary("c", "d") == nil {
		t.Fatal("stale-chunk cleanup removed the summary")
	}

	// A document's chunk vectors from disk, float32 and quantized.
	if got := vs.DocumentChunkVectors("c", "d"); len(got) != 1 {
		t.Fatalf("chunks of d after cleanup: %d", len(got))
	}
	if got := vs.DocumentChunkVectors("c", "q"); len(got) != 1 || len(got[0]) != 2 {
		t.Fatalf("quantized chunks: %v", got)
	}

	before = bl.CurrentLSN()
	if err := vs.DeleteSummary("c", "d"); err != nil || vs.GetSummary("c", "d") != nil || bl.CurrentLSN() == before {
		t.Fatalf("DeleteSummary: %v", err)
	}
	before = bl.CurrentLSN()
	if err := vs.DeleteSummary("c", "d"); err != nil || bl.CurrentLSN() != before {
		t.Fatal("deleting nothing wrote to the binlog")
	}

	_ = vs.PutSummary("c", "d", []float32{1, 0}, "m", "sh")
	if n, err := vs.DeleteCollection("c"); err != nil || n != 3 || len(vs.LoadSummaries("c")) != 0 {
		t.Fatalf("DeleteCollection removed %d (%v)", n, err)
	}
}

func TestSummaryVectorsWithoutABucketOrWithBadRecords(t *testing.T) {
	vs, db := openStore(t, false)
	if err := vs.PutSummary("c", "d", []float32{1}, "m", "h"); err == nil {
		t.Fatal("stored without a bucket")
	}
	if vs.GetSummary("c", "d") != nil || len(vs.LoadSummaries("c")) != 0 || len(vs.SummaryVectors("c", []string{"d"})) != 0 ||
		vs.DocumentChunkVectors("c", "d") != nil || vs.DeleteSummary("c", "d") != nil {
		t.Fatal("reads without a bucket")
	}
	if err := vs.EnsureBucket(); err != nil {
		t.Fatal(err)
	}
	_ = db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte("vectors"))
		_ = b.Put(SummaryKey("c", "bad"), []byte{1, 2})
		_ = b.Put(buildChunkKey("c", "bad", 0), []byte{1, 2})
		return nil
	})
	if len(vs.LoadSummaries("c")) != 0 || len(vs.SummaryVectors("c", []string{"bad"})) != 0 || vs.DocumentChunkVectors("c", "bad") != nil {
		t.Fatal("an undecodable record was returned")
	}
}

// A key the bucket cannot delete — a nested bucket under a vector key — fails
// the collection delete rather than reporting it done.
func TestDeleteCollectionReportsAFailedDelete(t *testing.T) {
	vs, db := openStore(t, true)
	_ = db.Update(func(tx *bolt.Tx) error {
		_, err := tx.Bucket([]byte("vectors")).CreateBucket([]byte("vec|c|nested"))
		return err
	})
	if _, err := vs.DeleteCollection("c"); err == nil {
		t.Fatal("a failed delete was reported as done")
	}
}
