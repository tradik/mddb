package main

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"mddb/internal/binlog"
	"mddb/internal/embedding"
	"mddb/internal/metrics"
	vec "mddb/internal/vector"

	bolt "go.etcd.io/bbolt"
)

// The failure paths of RAG-010's storage, embedding and search.

func TestImageBytesBinlogAndCollectionDelete(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	bl, err := binlog.NewBinlog(filepath.Join(t.TempDir(), "bl.db"), binlog.BinlogConfig{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = bl.Close() }()
	s.Binlog = bl
	before := bl.CurrentLSN()
	if err := s.putImageBytes("c", "c|a|en", solidPNG(20, 20, fixtureRed)); err != nil {
		t.Fatal(err)
	}
	s.deleteImageBytes("c", "c|a|en")
	s.deleteImageBytes("c", "c|a|en") // nothing left: nothing logged
	if bl.CurrentLSN()-before != 2 {
		t.Fatalf("binlog entries: %d", bl.CurrentLSN()-before)
	}

	// A file without the bucket has nothing to delete; a read transaction
	// cannot delete what is there.
	db, err := bolt.Open(filepath.Join(t.TempDir(), "bare.db"), 0o600, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var bo binlog.BinlogOps
	if err := db.Update(func(tx *bolt.Tx) error { return deleteCollectionImageBytes(tx, "c", &bo) }); err != nil {
		t.Fatal(err)
	}
	if err := s.putImageBytes("c", "c|b|en", solidPNG(20, 20, fixtureRed)); err != nil {
		t.Fatal(err)
	}
	if err := s.DBViewC("c", func(tx *bolt.Tx) error { return deleteCollectionImageBytes(tx, "c", &bo) }); err == nil {
		t.Fatal("deleted in a read transaction")
	}
}

// failingIndexer is an imageIndexer whose index refuses vectors.
type failingIndexer struct{ serverIndexes }

func (failingIndexer) IndexImage(string, string, []float32) error { return errors.New("index refused") }
func (failingIndexer) HasImage(string, string) bool               { return false }

func TestImageRefreshFailures(t *testing.T) {
	s, p, cleanup := multimodalServer(t)
	defer cleanup()
	ctx := context.Background()
	id := genID("mm", "manual/images/wiring.jpg", "en")

	// The index refuses the vector.
	r := imageRefresh{provider: p, store: s.VectorStore, index: failingIndexer{serverIndexes{s}}}
	if out, err := r.run(ctx, "mm", id, true); out != jobFailed || err == nil || !strings.Contains(err.Error(), "index") {
		t.Fatalf("index: %v %v", out, err)
	}
	// The store refuses it: a vector store without its bucket.
	db, err := bolt.Open(filepath.Join(t.TempDir(), "novec.db"), 0o600, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	r = imageRefresh{provider: p, store: vec.NewVectorStore(db), index: serverIndexes{s}}
	if out, err := r.run(ctx, "mm", id, true); out != jobFailed || err == nil || !strings.Contains(err.Error(), "store") {
		t.Fatalf("store: %v %v", out, err)
	}
	// Retries wait between attempts, and the worker logs and counts a
	// failure.
	old := embeddingRetryDelay
	embeddingRetryDelay = 0
	defer func() { embeddingRetryDelay = old }()
	p.failImages.Store(true)
	defer p.failImages.Store(false)
	r = imageRefresh{provider: p, store: s.VectorStore, index: serverIndexes{s}, attempts: 2}
	calls := p.imageCalls.Load()
	if out, _ := r.run(ctx, "mm", id, true); out != jobFailed || p.imageCalls.Load()-calls != 2 {
		t.Fatalf("retries: %v %d", out, p.imageCalls.Load()-calls)
	}
	w := &EmbeddingWorker{provider: p, vectorStore: s.VectorStore, indexer: serverIndexes{s}, metrics: metrics.NewMetrics(false, nil)}
	_ = s.VectorStore.DeleteImage("mm", id) // forces a new embedding
	if out := w.processImage(EmbeddingJob{Collection: "mm", DocID: id, Image: true}); out != jobFailed {
		t.Fatalf("worker: %v", out)
	}
	if out := w.processImage(EmbeddingJob{Collection: "mm", DocID: id}); out != jobDone {
		t.Fatalf("not an image document: %v", out)
	}
}

func TestImageSearchFailures(t *testing.T) {
	s, p, cleanup := multimodalServer(t)
	defer cleanup()
	ctx := context.Background()
	if hits := s.imageHitsFor(ctx, "mm", "blue", 3, 0, nil, false, nil); len(hits) == 0 {
		t.Fatal("no plan to explain into")
	}
	p.failImages.Store(true)
	defer p.failImages.Store(false)
	plan := &SearchPlan{}
	b64 := base64.StdEncoding.EncodeToString(solidPNG(20, 20, fixtureBlue))
	if _, _, _, err := s.searchByImage(ctx, "mm", b64, 3, 0, nil, false); !errors.Is(err, errEmbedQuery) {
		t.Fatalf("query image: %v", err)
	}
	s.Embedding = &failingTextProvider{p}
	if hits := s.imageHitsFor(ctx, "mm", "blue", 3, 0, nil, false, plan); hits != nil || !strings.Contains(strings.Join(plan.Reasons, " "), "image search skipped") {
		t.Fatalf("text: %v %v", hits, plan.Reasons)
	}
	var none []VectorSearchResultItem
	attachImages(s, none, func(*VectorSearchResultItem) imageOwner { return imageOwner{} }, func(*VectorSearchResultItem, []ImageRef) {})
}

// failingTextProvider cannot embed text into the image space.
type failingTextProvider struct{ *colourProvider }

func (failingTextProvider) EmbedImageText(context.Context, string, embedding.Role) ([]float32, error) {
	return nil, errColourProvider
}

func TestImportURLFrontmatterAndRefusedContent(t *testing.T) {
	if meta, body := parseFrontmatter("---"); meta != nil || body != "---" {
		t.Fatalf("no newline: %v %q", meta, body)
	}
	if meta, body := parseFrontmatter("---\n: no key\ntitle: T\n---\nbody"); first(meta["title"]) != "T" || len(meta) != 1 || body != "body" {
		t.Fatalf("empty key: %v %q", meta, body)
	}
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("bad \xff\xfe utf-8")) }))
	defer srv.Close()
	t.Setenv("MDDB_OUTBOUND_ALLOW_PRIVATE", "true")
	rec := doRequest(t, s.handleImportURL, map[string]any{"collection": "c", "url": srv.URL + "/page.md", "lang": "en"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid UTF-8 stored: %d %s", rec.Code, rec.Body.String())
	}
}
