package main

import (
	"context"
	"errors"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	pb "mddb/proto"
)

// GO-043: the gate's limits other than document count and bytes — vectors,
// the disk reserve, the vector RAM limit — its usage cache, its reports.

func TestQuotaMaxVectorsRefusesNewDocuments(t *testing.T) {
	gs, s := quotaTestServer(t)
	setTestQuota(t, s, "q", CollectionQuota{MaxVectors: 1})
	if err := s.VectorIndex.Add("q", "a#0", []float32{1, 0, 0}); err != nil {
		t.Fatal(err)
	}
	_, err := gs.Add(context.Background(), &pb.AddRequest{Collection: "q", Key: "n", Lang: "en", ContentMd: "new"})
	wantGRPCQuota(t, err)
	if !strings.Contains(err.Error(), "vectors") {
		t.Fatalf("err = %v, want the vectors limit named", err)
	}
}

func TestDiskReserveRefusesWritesButNotReadsOrBackups(t *testing.T) {
	t.Setenv("MDDB_DISK_RESERVE_BYTES", "1000")
	gs, s := quotaTestServer(t)
	free := uint64(10)
	orig := quotaDiskSpace
	quotaDiskSpace = func(string) (uint64, uint64, error) { return free, 1 << 30, nil }
	t.Cleanup(func() { quotaDiskSpace = orig })

	rec := doRequest(t, s.handleAdd, map[string]any{"collection": "q", "key": "n", "lang": "en", "contentMd": "new"})
	wantHTTPQuota(t, rec, http.StatusInsufficientStorage, quotaDiskReserve)
	if !strings.Contains(rec.Body.String(), "below the reserve of 1000") {
		t.Fatalf("body = %s", rec.Body.String())
	}
	// Collections without a quota are refused too: the reserve is server-wide.
	_, err := gs.Add(context.Background(), &pb.AddRequest{Collection: "other", Key: "n", Lang: "en", ContentMd: "new"})
	wantGRPCQuota(t, err)

	// Reads and backups still work.
	got := doRequest(t, s.handleGet, map[string]any{"collection": "q", "key": "a", "lang": "en"})
	if got.Code != http.StatusOK {
		t.Fatalf("read refused below the reserve: %d %s", got.Code, got.Body.String())
	}
	if err := s.backupTo(filepath.Join(testBackupDir(t), "backup.db")); err != nil {
		t.Fatalf("backup refused below the reserve: %v", err)
	}

	// Space freed: writes resume. A filesystem that cannot be asked is not
	// known to be full, so it does not stop writes either.
	free = 1 << 20
	if _, err := gs.Add(context.Background(), &pb.AddRequest{Collection: "q", Key: "n", Lang: "en", ContentMd: "new"}); err != nil {
		t.Fatalf("write refused above the reserve: %v", err)
	}
	quotaDiskSpace = func(string) (uint64, uint64, error) { return 0, 0, errors.New("no statfs") }
	if _, err := gs.Add(context.Background(), &pb.AddRequest{Collection: "q", Key: "m", Lang: "en", ContentMd: "new"}); err != nil {
		t.Fatalf("write refused when the filesystem could not be asked: %v", err)
	}
}

func TestDiskReserveIgnoredWithoutPath(t *testing.T) {
	qs := &quotaState{diskReserve: 10}
	if qe := qs.checkDiskReserve(""); qe != nil {
		t.Fatalf("a server without a path has no filesystem to measure: %v", qe)
	}
}

func TestVectorRAMLimitRefusesNewVectorsInRAM(t *testing.T) {
	t.Setenv("MDDB_VECTOR_RAM_LIMIT_BYTES", "8")
	gs, s := quotaTestServer(t)
	s.Embedding = &mockEmbedding{dims: 3, model: "m"}
	if err := s.VectorIndex.Add("q", "a#0", []float32{1, 0, 0}); err != nil {
		t.Fatal(err)
	}
	if got := s.VectorIndex.MemoryBytes(); got != 12 {
		t.Fatalf("MemoryBytes = %d, want 12", got)
	}
	_, err := gs.Add(context.Background(), &pb.AddRequest{Collection: "q", Key: "n", Lang: "en", ContentMd: "new"})
	wantGRPCQuota(t, err)
	if !strings.Contains(err.Error(), quotaVectorRAM) {
		t.Fatalf("err = %v", err)
	}
	// Updates that do not grow, and disk-only collections, are let through.
	if _, err := gs.UpdateDocument(context.Background(), &pb.UpdateDocumentRequest{Collection: "q", Key: "a", Lang: "en",
		UpdateContent: true, ContentMd: "x"}); err != nil {
		t.Fatalf("shrinking update refused: %v", err)
	}
	if err := s.CollectionManager.Set("disk", &CollectionConfig{Quantization: "int8", DiskOnlyVectors: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := gs.Add(context.Background(), &pb.AddRequest{Collection: "disk", Key: "n", Lang: "en", ContentMd: "new"}); err != nil {
		t.Fatalf("disk-only collection refused: %v", err)
	}
	// With a quota on top, the RAM limit is checked first.
	setTestQuota(t, s, "q", CollectionQuota{MaxDocuments: 100})
	_, err = gs.Add(context.Background(), &pb.AddRequest{Collection: "q", Key: "o", Lang: "en", ContentMd: "new"})
	wantGRPCQuota(t, err)
}

func TestVectorRAMLimitWithoutIndexOrQuota(t *testing.T) {
	t.Setenv("MDDB_VECTOR_RAM_LIMIT_BYTES", "8")
	gs, s := quotaTestServer(t)
	s.Embedding = &mockEmbedding{dims: 3, model: "m"}
	s.VectorIndex = nil
	if _, err := gs.Add(context.Background(), &pb.AddRequest{Collection: "q", Key: "n", Lang: "en", ContentMd: "new"}); err != nil {
		t.Fatalf("no in-memory index, nothing to limit: %v", err)
	}
}

func TestQuotaUsageFollowsEveryCommit(t *testing.T) {
	_, s := quotaTestServer(t)
	setTestQuota(t, s, "q", CollectionQuota{MaxDocuments: 3})
	addTestDoc(t, s, "q", "c", "en", "third", nil) // 3 of 3, recorded on commit
	if _, _, err := s.addDocument("q", "d", "en", nil, "fourth", 0, true); asQuotaError(err) == nil {
		t.Fatalf("4th document: err = %v", err)
	}
	// A delete is a write the gate never sees; the cached count must not
	// survive it.
	if err := s.deleteDocumentInternal("q", "a", "en"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.addDocument("q", "d", "en", nil, "fourth", 0, true); err != nil {
		t.Fatalf("room after a delete: %v", err)
	}
	if _, _, err := s.addDocument("q", "e", "en", nil, "fifth", 0, true); asQuotaError(err) == nil {
		t.Fatalf("over again: err = %v", err)
	}
	// Rewriting an existing document is not a new one.
	if _, _, err := s.addDocument("q", "b", "en", nil, "rewritten", 0, true); err != nil {
		t.Fatalf("rewrite refused: %v", err)
	}
}

func TestQuotaCountsARepeatedBatchKeyOnce(t *testing.T) {
	gs, s := quotaTestServer(t)
	setTestQuota(t, s, "q", CollectionQuota{MaxDocuments: 3})
	resp, err := gs.AddBatch(context.Background(), &pb.AddBatchRequest{Collection: "q", Documents: []*pb.BatchDocument{
		{Key: "n", Lang: "en", ContentMd: "one"}, {Key: "n", Lang: "en", ContentMd: "two"},
	}})
	if err != nil || resp.Failed != 0 {
		t.Fatalf("batch writing one new key twice: resp %v, err %v", resp, err)
	}
	if n := quotaDocCount(t, s, "q"); n != 3 {
		t.Fatalf("%d documents, want 3", n)
	}
}

func TestQuotaUsageReadPathAndOrdering(t *testing.T) {
	_, s := quotaTestServer(t)
	u := s.collectionUsage("q")
	if u.docs != 2 || u.bytes <= 0 {
		t.Fatalf("usage = %+v", u)
	}
	// A read recorded the usage; a second read is served from it.
	if again := s.collectionUsage("q"); again != u {
		t.Fatalf("second read %+v, first %+v", again, u)
	}
	// An older commit never replaces a newer one.
	qs := s.quotas()
	e := qs.usage["q"]
	qs.remember(e.db, e.txid-1, "q", quotaUsage{docs: 99})
	if qs.usage["q"].usage != u {
		t.Fatal("an older commit overwrote a newer usage record")
	}
}

func TestQuotaWritesHelpersSkipFailedDocuments(t *testing.T) {
	ok, failed := &ProcessedDoc{DocID: "x", Buf: []byte("abc")}, &ProcessedDoc{DocID: "y", Error: errors.New("bad")}
	if w := docQuotaWrites("c", []*ProcessedDoc{ok, failed})(); len(w) != 1 || w[0].size != 3 {
		t.Fatalf("docQuotaWrites = %+v", w)
	}
	uok, ufailed := &UpdatedDoc{DocID: "x", Buf: []byte("ab")}, &UpdatedDoc{DocID: "y", Error: errors.New("bad")}
	if w := updateQuotaWrites("c", []*UpdatedDoc{uok, ufailed})(); len(w) != 1 || w[0].size != 2 {
		t.Fatalf("updateQuotaWrites = %+v", w)
	}
}
