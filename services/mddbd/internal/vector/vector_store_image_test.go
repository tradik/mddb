package vector

import (
	"testing"
)

// RAG-010: image vectors live beside chunks and summaries under their own
// prefix; nothing that walks chunks or summaries sees them, and a collection
// delete removes them.

func TestImageVectorsStayApart(t *testing.T) {
	vs, _ := openStore(t, true)
	if err := vs.PutChunks("c", "d", []ChunkEmbedding{{ChunkIndex: 0, Vector: []float32{1, 0}}}, "m", "h"); err != nil {
		t.Fatal(err)
	}
	if err := vs.PutSummary("c", "d", []float32{0.5, 0.5}, "m", "sh"); err != nil {
		t.Fatal(err)
	}
	if err := vs.PutImage("c", "d|img", []float32{0, 1, 0}, "mm", "ih"); err != nil {
		t.Fatal(err)
	}
	if !IsImageKey(ImageKey("c", "x")) || IsImageKey(SummaryKey("c", "x")) || IsSummaryKey(ImageKey("c", "x")) {
		t.Fatal("prefixes overlap")
	}
	if rec := vs.GetImage("c", "d|img"); rec == nil || rec.ContentHash != "ih" || rec.Model != "mm" || len(rec.Vector) != 3 {
		t.Fatalf("GetImage: %+v", rec)
	}
	if got := vs.LoadImages("c"); len(got) != 1 || got["d|img"] == nil {
		t.Fatalf("LoadImages: %v", got)
	}
	if got := vs.ImageVectors("c", []string{"d|img", "none"}); len(got) != 1 {
		t.Fatalf("ImageVectors: %v", got)
	}
	if got := vs.LoadSummaries("c"); len(got) != 1 {
		t.Fatalf("summaries see images: %v", got)
	}
	if recs, _ := vs.LoadCollection("c"); len(recs) != 1 {
		t.Fatalf("chunks see images: %d", len(recs))
	}
	if counts, _ := vs.CountByCollection(); counts["c"] != 1 {
		t.Fatalf("counts see images: %v", counts)
	}
	if err := vs.DeleteImage("c", "d|img"); err != nil || vs.GetImage("c", "d|img") != nil {
		t.Fatalf("DeleteImage: %v", err)
	}
	if err := vs.PutImage("c", "e", []float32{1}, "mm", "h"); err != nil {
		t.Fatal(err)
	}
	if n, err := vs.DeleteCollection("c"); err != nil || n != 3 || len(vs.LoadImages("c")) != 0 {
		t.Fatalf("DeleteCollection: %d %v", n, err)
	}
}
