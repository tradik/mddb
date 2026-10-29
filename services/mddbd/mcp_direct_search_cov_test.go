package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"mddb/internal/fts"
	"mddb/internal/storage"
)

// covC: the search, TTL, import and full-text maintenance tools of the
// direct client — chunk, window, passages, groupBy, wait and pending variants.

var covCQueryVector = []float32{0.1, 0.1, 0.1}

// covCEmbeddedServer has a running embedding worker, so documents become
// searchable by vector once waitForEmbedding returns.
func covCEmbeddedServer(t *testing.T) (*DirectClient, *Server) {
	t.Helper()
	c, s := covCServer(t)
	s.VectorIndex.SetReady()
	s.Embedding = &mockEmbedding{dims: 3, model: "test-model"}
	s.startEmbeddingWorker(s.Embedding)
	t.Cleanup(s.EmbeddingWorker.Stop)
	return c, s
}

// covCEmbedded adds a document and waits until its vector is searchable.
func covCEmbedded(t *testing.T, s *Server, coll, key, content string, meta map[string][]string) storage.Doc {
	t.Helper()
	doc := addTestDoc(t, s, coll, key, "en", content, meta)
	waitForEmbedding(t, s, coll, doc.ID)
	return doc
}

func TestCovCVectorSearchRejectsWhatItCannotRun(t *testing.T) {
	c, s := covCServer(t)
	s.VectorIndex.SetReady()
	ctx := context.Background()
	cases := map[string]*MCPVectorSearchRequest{
		"empty groupBy":      {Collection: "v", Query: "q", GroupBy: &GroupBy{}},
		"no collection":      {Query: "q"},
		"no query":           {Collection: "v"},
		"unknown mode":       {Collection: "v", QueryVector: covCQueryVector, RetrievalMode: "sideways"},
		"no provider, query": {Collection: "v", Query: "q"},
	}
	for name, req := range cases {
		if resp, err := c.VectorSearch(ctx, req); err == nil {
			t.Errorf("%s: searched anyway: %+v", name, resp)
		}
	}
	s.Embedding = &covCFailEmbedding{}
	if _, err := c.VectorSearch(ctx, &MCPVectorSearchRequest{Collection: "v", Query: "q"}); err == nil ||
		!strings.Contains(err.Error(), "provider down") {
		t.Errorf("failing provider: %v", err)
	}
}

func TestCovCVectorSearchWithAFilterNothingMatches(t *testing.T) {
	c, s := covCServer(t)
	s.VectorIndex.SetReady()
	s.Embedding = &mockEmbedding{dims: 3, model: "test-model"}
	resp, err := c.VectorSearch(context.Background(), &MCPVectorSearchRequest{Collection: "v", QueryVector: covCQueryVector,
		FilterMeta: map[string][]string{"tag": {"absent"}}})
	if err != nil || len(resp.Results) != 0 || resp.Model != "test-model" || resp.Dimensions != 3 || resp.DistanceMetric != "cosine" {
		t.Fatalf("filtered-to-nothing search = %+v, %v", resp, err)
	}
}

func TestCovCVectorSearchWindowModeAndDamagedHits(t *testing.T) {
	c, s := covCEmbeddedServer(t)
	covCEmbedded(t, s, "w", "good", "# Heading\n\nthe passage an agent wants", nil)
	gone := covCEmbedded(t, s, "w", "gone", "removed behind the index's back", nil)
	bad := covCEmbedded(t, s, "w", "bad", "damaged on disk", nil)
	covCDel(t, s, "docs", storage.DocKey("w", gone.ID))
	covCPut(t, s, "docs", storage.DocKey("w", bad.ID), covCGarbage)

	resp, err := c.VectorSearch(context.Background(), &MCPVectorSearchRequest{Collection: "w", Query: "passage",
		RetrievalMode: RetrievalModeWindow, TopK: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Results) != 1 || resp.Results[0].Document.Key != "good" {
		t.Fatalf("results = %+v, want only the intact document", resp.Results)
	}
	r := resp.Results[0]
	if r.ChunkIndex == nil || !strings.Contains(r.ChunkText, "passage an agent wants") || r.Document.ContentMD != "" {
		t.Errorf("window hit = %+v", r)
	}
}

// A collection that caps its context budget gets results cut to it on the
// vector and hybrid paths alike.
func TestCovCSearchesApplyTheContextBudget(t *testing.T) {
	c, s := covCEmbeddedServer(t)
	if err := s.CollectionManager.Set("budget", &CollectionConfig{Retrieval: &RetrievalProfileDef{ContextTokenBudget: 5}}); err != nil {
		t.Fatal(err)
	}
	long := strings.Repeat("budget words ", 50)
	covCEmbedded(t, s, "budget", "a", long, nil)
	covCEmbedded(t, s, "budget", "b", long, nil)
	ctx := context.Background()

	vr, err := c.VectorSearch(ctx, &MCPVectorSearchRequest{Collection: "budget", Query: "budget", IncludeContent: true, TopK: 5})
	if err != nil || !vr.ContextTruncated || vr.Total >= 2 {
		t.Errorf("vector search under a budget = truncated %v, total %d, %v", vr.ContextTruncated, vr.Total, err)
	}
	hr, err := c.HybridSearch(ctx, &MCPHybridSearchRequest{Collection: "budget", Query: "budget", TopK: 5})
	if err != nil || !hr.ContextTruncated || hr.Total >= 2 {
		t.Errorf("hybrid search under a budget = truncated %v, total %d, %v", hr.ContextTruncated, hr.Total, err)
	}
}

// A disk-only quantized collection is searched through the quantized index
// and rescored from the full vectors on disk.
func TestCovCVectorSearchPicksTheQuantizedIndexForADiskOnlyCollection(t *testing.T) {
	c, s := covCEmbeddedServer(t)
	if err := s.CollectionManager.Set("compact", &CollectionConfig{Quantization: "int8", DiskOnlyVectors: true}); err != nil {
		t.Fatal(err)
	}
	addTestDoc(t, s, "compact", "q", "en", "quantized words", nil)
	deadline := time.Now().Add(5 * time.Second)
	for !s.QuantizedVecIndex.HasCollection("compact") {
		if time.Now().After(deadline) {
			t.Fatal("the document never reached the quantized index")
		}
		time.Sleep(10 * time.Millisecond)
	}
	resp, err := c.VectorSearch(context.Background(), &MCPVectorSearchRequest{Collection: "compact", Query: "quantized"})
	if err != nil || resp.Algorithm != "quantized" || len(resp.Results) != 1 || resp.Results[0].Score < 0.99 {
		t.Fatalf("disk-only search = %+v, %v", resp, err)
	}
}

// readLevel indexedPlusPending returns documents still waiting for their
// embedding, marked as such, in both result and passage formats.
func TestCovCVectorSearchIncludesPendingDocuments(t *testing.T) {
	s, _, cleanup := indexingServer(t)
	defer cleanup()
	c := NewDirectClient(s)
	addViaHTTP(t, s, "p1", "pending pelican story")
	ctx := context.Background()

	resp, err := c.VectorSearch(ctx, &MCPVectorSearchRequest{Collection: "notes", Query: "pelican",
		IndexWait: IndexWait{ReadLevel: ReadLevelIndexedPlusPending}})
	if err != nil || len(resp.Results) != 1 || !resp.Results[0].PendingEmbedding || resp.Results[0].Document.ContentMD != "" {
		t.Fatalf("pending results = %+v, %v", resp, err)
	}
	pass, err := c.VectorSearch(ctx, &MCPVectorSearchRequest{Collection: "notes", Query: "pelican",
		IndexWait: IndexWait{ReadLevel: ReadLevelIndexedPlusPending}, PassageOptions: PassageOptions{Format: FormatPassages}})
	if err != nil || pass.Passages == nil || len(pass.Passages.Passages) != 1 ||
		!strings.Contains(pass.Passages.Passages[0].Content, "pelican") {
		t.Fatalf("pending passages = %+v, %v", pass.Passages, err)
	}
}

func TestCovCHybridSearchVariants(t *testing.T) {
	c, s := covCEmbeddedServer(t)
	covCEmbedded(t, s, "h", "alpha", "alpha words here", nil)
	covCEmbedded(t, s, "h", "beta", "beta words there", nil)
	ctx := context.Background()

	for name, req := range map[string]*MCPHybridSearchRequest{
		"no query":      {Collection: "h"},
		"empty groupBy": {Collection: "h", Query: "alpha", GroupBy: &GroupBy{}},
	} {
		if _, err := c.HybridSearch(ctx, req); err == nil {
			t.Errorf("%s: searched anyway", name)
		}
	}
	rrf, err := c.HybridSearch(ctx, &MCPHybridSearchRequest{Collection: "h", Query: "alpha", Strategy: "rrf", TopK: 5})
	if err != nil || rrf.Strategy != "rrf" || rrf.Total == 0 || rrf.Results[0].Document.Key != "alpha" {
		t.Fatalf("rrf = %+v, %v", rrf, err)
	}

	// A pinned document joins the results and the list is cut back to topK.
	s.CurationManager = NewCurationManager(s.DB)
	if err := s.CurationManager.EnsureBucket(); err != nil {
		t.Fatal(err)
	}
	if err := s.CurationManager.Set(&CurationRule{Collection: "h", Query: "alpha", Enabled: true,
		Pins: []PinnedDoc{{Key: "beta", Lang: "en", Position: 1}}}); err != nil {
		t.Fatal(err)
	}
	pinned, err := c.HybridSearch(ctx, &MCPHybridSearchRequest{Collection: "h", Query: "alpha", TopK: 1})
	if err != nil || pinned.Total != 1 || pinned.Results[0].Document.Key != "beta" || !pinned.Results[0].Pinned {
		t.Fatalf("pinned = %+v, %v", pinned, err)
	}

	s.Embedding = &covCFailEmbedding{}
	if _, err := c.HybridSearch(ctx, &MCPHybridSearchRequest{Collection: "h", Query: "alpha"}); err == nil {
		t.Error("hybrid search with a failing provider succeeded")
	}
}

func TestCovCVectorStatsCountsEmbeddedDocuments(t *testing.T) {
	c, s := covCEmbeddedServer(t)
	covCEmbedded(t, s, "vs", "a", "stats words", nil)
	resp, err := c.VectorStats(context.Background())
	if err != nil || !resp.Enabled || resp.Model != "test-model" || resp.Dimensions != 3 {
		t.Fatalf("VectorStats = %+v, %v", resp, err)
	}
	if got := resp.Collections["vs"]; got.TotalDocuments != 1 || got.EmbeddedDocuments == 0 {
		t.Errorf("collection stats = %+v", got)
	}
}

func TestCovCImportURL(t *testing.T) {
	c, s := covCServer(t)
	ctx := context.Background()
	if _, err := c.ImportURL(ctx, &MCPImportURLRequest{Collection: "imp", URL: "https://example.com/x"}); err == nil {
		t.Error("an import without lang was accepted")
	}
	if _, err := c.ImportURL(ctx, &MCPImportURLRequest{Collection: "imp", URL: "https://example.com/", Lang: "en"}); err == nil ||
		!strings.Contains(err.Error(), "derive key") {
		t.Errorf("an import with no key to derive: %v", err)
	}

	t.Setenv("MDDB_OUTBOUND_ALLOW_PRIVATE", "true")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("---\ntitle: Guide\n---\n# Body text\n"))
	}))
	defer srv.Close()
	doc, err := c.ImportURL(ctx, &MCPImportURLRequest{Collection: "imp", URL: srv.URL + "/guide.md", Lang: "en",
		Meta: map[string][]string{"source": {"web"}}})
	if err != nil {
		t.Fatal(err)
	}
	if doc.Key != "guide" || doc.Meta["title"][0] != "Guide" || doc.Meta["source"][0] != "web" || !strings.Contains(doc.ContentMD, "Body text") {
		t.Errorf("imported document = %+v", doc)
	}
	if err := s.SchemaManager.Set("strict", `{"required":["author"]}`); err != nil {
		t.Fatal(err)
	}
	if _, err := c.ImportURL(ctx, &MCPImportURLRequest{Collection: "strict", URL: srv.URL + "/guide.md", Lang: "en"}); err == nil {
		t.Error("an import violating the collection schema was stored")
	}
}

func TestCovCSetTTL(t *testing.T) {
	c, s := covCServer(t)
	ctx := context.Background()
	addTestDoc(t, s, "ttl", "keep", "en", "x", nil)
	bad := addTestDoc(t, s, "ttl", "bad", "en", "x", nil)
	covCPut(t, s, "docs", storage.DocKey("ttl", bad.ID), covCGarbage)

	if _, err := c.SetTTL(ctx, &MCPSetTTLRequest{Collection: "ttl", Key: "keep"}); err == nil {
		t.Error("SetTTL without lang was accepted")
	}
	if _, err := c.SetTTL(ctx, &MCPSetTTLRequest{Collection: "ttl", Key: "never", Lang: "en", TTL: 60}); err == nil ||
		err.Error() != "document not found" {
		t.Errorf("SetTTL on a missing document: %v", err)
	}
	if _, err := c.SetTTL(ctx, &MCPSetTTLRequest{Collection: "ttl", Key: "bad", Lang: "en", TTL: 60}); err == nil {
		t.Error("SetTTL on a damaged document succeeded")
	}
	expiresAt := func() int64 {
		meta, err := c.GetDocumentMeta(ctx, &MCPGetDocMetaRequest{Collection: "ttl", Key: "keep", Lang: "en"})
		if err != nil {
			t.Fatal(err)
		}
		return meta.ExpiresAt
	}
	if _, err := c.SetTTL(ctx, &MCPSetTTLRequest{Collection: "ttl", Key: "keep", Lang: "en", TTL: 60}); err != nil || expiresAt() == 0 {
		t.Fatalf("SetTTL 60: %v, expiresAt %d", err, expiresAt())
	}
	// A TTL of zero clears the expiry.
	if _, err := c.SetTTL(ctx, &MCPSetTTLRequest{Collection: "ttl", Key: "keep", Lang: "en"}); err != nil || expiresAt() != 0 {
		t.Fatalf("SetTTL 0: %v, expiresAt %d", err, expiresAt())
	}
}

func TestCovCFTSMaintenanceTools(t *testing.T) {
	c, s := covCServer(t)
	ctx := context.Background()
	if _, err := c.FTSReindex(ctx, &MCPFTSReindexRequest{}); err == nil {
		t.Error("FTSReindex without a collection was accepted")
	}
	none := NewDirectClient(&Server{})
	if _, err := none.FTSReindex(ctx, &MCPFTSReindexRequest{Collection: "f"}); err == nil {
		t.Error("FTSReindex without a full-text index succeeded")
	}
	if langs, err := none.FTSLanguages(ctx); err != nil || len(langs.Languages) != 0 {
		t.Errorf("FTSLanguages without an index = %+v, %v", langs, err)
	}

	reg := fts.NewLangRegistry("en")
	fts.RegisterDefaultLanguages(reg)
	s.FTSIndex.SetLangRegistry(reg)
	langs, err := c.FTSLanguages(ctx)
	if err != nil || langs.DefaultLang != "en" || len(langs.Languages) == 0 {
		t.Fatalf("FTSLanguages = %+v, %v", langs, err)
	}
	for _, l := range langs.Languages {
		if l.Code == "" || l.Name == "" {
			t.Errorf("language without a code or name: %+v", l)
		}
	}

	closed, _ := covCClosedClient(t, "f")
	if _, err := closed.FTSReindex(ctx, &MCPFTSReindexRequest{Collection: "f"}); err == nil {
		t.Error("FTSReindex on a closed database succeeded")
	}
}

func TestCovCSearchAdvisor(t *testing.T) {
	c, s := covCServer(t)
	if _, err := c.SearchAdvisor(context.Background(), ""); err == nil {
		t.Error("SearchAdvisor without a collection was accepted")
	}
	addTestDoc(t, s, "adv", "a", "en", "advice words", nil)
	rec, err := c.SearchAdvisor(context.Background(), "adv")
	if err != nil || rec == nil {
		t.Fatalf("SearchAdvisor = %+v, %v", rec, err)
	}
}
