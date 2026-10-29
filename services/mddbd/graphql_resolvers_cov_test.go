package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	gql "mddb/graphql"
	"mddb/internal/storage"

	bolt "go.etcd.io/bbolt"
)

// covFAdapter is gqlAdapter on the fuller handler test server, which carries
// the vector, FTS, TTL and schema subsystems the document resolvers reach.
func covFAdapter(t *testing.T) (*GraphQLAdapter, *Server) {
	t.Helper()
	s, cleanup := newHandlerTestServer(t)
	t.Cleanup(cleanup)
	return &GraphQLAdapter{server: s, mcp: NewDirectClient(s)}, s
}

// covFStoredDoc reads a document straight from storage, bypassing every
// GraphQL and MCP conversion, so a test sees what was actually written.
func covFStoredDoc(t *testing.T, s *Server, coll, key, lang string) *storage.Doc {
	t.Helper()
	var doc *storage.Doc
	err := s.DBViewC(coll, func(tx *bolt.Tx) error {
		v := tx.Bucket([]byte("docs")).Get(storage.DocKey(coll, genID(coll, key, lang)))
		if v == nil {
			return nil
		}
		d, err := loadDoc(v)
		doc = d
		return err
	})
	if err != nil {
		t.Fatalf("read %s/%s/%s: %v", coll, key, lang, err)
	}
	return doc
}

// covFAdapterCall is one adapter method invoked with valid-looking arguments.
type covFAdapterCall struct {
	name string
	run  func(ctx context.Context, a *GraphQLAdapter) error
}

func covFIgnore[T any](_ T, err error) error { return err }

// covFCollectionCalls covers every adapter method that names a collection —
// each must check permission on it before touching anything.
func covFCollectionCalls() []covFAdapterCall {
	key := "k1"
	q := "hello"
	return []covFAdapterCall{
		{"GetDocument", func(ctx context.Context, a *GraphQLAdapter) error {
			return covFIgnore(a.GetDocument(ctx, "docs", "k1", "en", nil))
		}},
		{"SearchDocuments", func(ctx context.Context, a *GraphQLAdapter) error {
			return covFIgnore(a.SearchDocuments(ctx, gql.SearchInput{Collection: "docs"}))
		}},
		{"AddDocument", func(ctx context.Context, a *GraphQLAdapter) error {
			return covFIgnore(a.AddDocument(ctx, gql.AddDocumentInput{Collection: "docs", Key: "k1", Lang: "en", ContentMd: "x"}))
		}},
		{"UpdateDocument", func(ctx context.Context, a *GraphQLAdapter) error {
			return covFIgnore(a.UpdateDocument(ctx, gql.UpdateDocumentInput{Collection: "docs", Key: "k1", Lang: "en"}))
		}},
		{"DeleteDocument", func(ctx context.Context, a *GraphQLAdapter) error {
			return a.DeleteDocument(ctx, "docs", "k1", "en")
		}},
		{"DeleteCollection", func(ctx context.Context, a *GraphQLAdapter) error {
			return covFIgnore(a.DeleteCollection(ctx, "docs"))
		}},
		{"AddBatch", func(ctx context.Context, a *GraphQLAdapter) error {
			return covFIgnore(a.AddBatch(ctx, "docs", []*gql.AddBatchDocumentInput{{Key: "k1", Lang: "en", ContentMd: "x"}}))
		}},
		{"IngestDocuments", func(ctx context.Context, a *GraphQLAdapter) error {
			return covFIgnore(a.IngestDocuments(ctx, "docs", []*gql.IngestDocumentInput{{Key: &key, Lang: "en", ContentMd: "x"}}, nil))
		}},
		{"SetTTL", func(ctx context.Context, a *GraphQLAdapter) error {
			return covFIgnore(a.SetTTL(ctx, "docs", "k1", "en", 60))
		}},
		{"ImportURL", func(ctx context.Context, a *GraphQLAdapter) error {
			return covFIgnore(a.ImportURL(ctx, "docs", "https://example.com/a", &key, "en", nil, nil))
		}},
		{"VectorSearch", func(ctx context.Context, a *GraphQLAdapter) error {
			return covFIgnore(a.VectorSearch(ctx, gql.VectorSearchInput{Collection: "docs", Query: &q}))
		}},
		{"VectorReindex", func(ctx context.Context, a *GraphQLAdapter) error {
			return covFIgnore(a.VectorReindex(ctx, "docs", nil))
		}},
		{"FullTextSearch", func(ctx context.Context, a *GraphQLAdapter) error {
			return covFIgnore(a.FullTextSearch(ctx, gql.FTSInput{Collection: "docs", Query: q}))
		}},
		{"CodeGraph", func(ctx context.Context, a *GraphQLAdapter) error {
			return covFIgnore(a.CodeGraph(ctx, gql.CodeGraphInput{Collection: "docs", Key: "k1"}))
		}},
		{"CodeSymbol", func(ctx context.Context, a *GraphQLAdapter) error {
			return covFIgnore(a.CodeSymbol(ctx, gql.CodeSymbolInput{Collection: "docs", Name: "k1"}))
		}},
		{"IndexingStatus", func(ctx context.Context, a *GraphQLAdapter) error {
			coll := "docs"
			return covFIgnore(a.IndexingStatus(ctx, &coll))
		}},
		{"IndexingStatus(all)", func(ctx context.Context, a *GraphQLAdapter) error {
			return covFIgnore(a.IndexingStatus(ctx, nil))
		}},
	}
}

// With auth on, a request without credentials is refused by every document
// resolver before it reaches storage — and refused as unauthenticated, not as
// some downstream failure that happens to look like one.
func TestCovFEveryResolverRefusesAnAnonymousCaller(t *testing.T) {
	a, s := covFAdapter(t)
	withAuth(t, s)

	calls := append(covFCollectionCalls(), covFAdapterCall{"VectorStats", func(ctx context.Context, a *GraphQLAdapter) error {
		return covFIgnore(a.VectorStats(ctx))
	}})
	for _, c := range calls {
		t.Run(c.name, func(t *testing.T) {
			if err := c.run(context.Background(), a); !errors.Is(err, ErrUnauthenticated) {
				t.Errorf("err = %v, want ErrUnauthenticated", err)
			}
		})
	}
	if doc := covFStoredDoc(t, s, "docs", "k1", "en"); doc != nil {
		t.Errorf("an anonymous caller wrote a document: %+v", doc)
	}
}

// An authenticated user with no grant on the collection is stopped at the
// permission check, and nothing they asked for is written.
func TestCovFEveryCollectionResolverChecksPermission(t *testing.T) {
	a, s := covFAdapter(t)
	withAuth(t, s)
	addTestDoc(t, s, "docs", "existing", "en", "kept", nil)
	ctx := context.WithValue(context.Background(), authContextKey, &JWTClaims{Username: "reader"})

	for _, c := range covFCollectionCalls() {
		t.Run(c.name, func(t *testing.T) {
			err := c.run(ctx, a)
			if err == nil {
				t.Fatal("a user without a grant was let through")
			}
			if errors.Is(err, ErrUnauthenticated) {
				t.Errorf("an authenticated user was told they are unauthenticated: %v", err)
			}
		})
	}
	if doc := covFStoredDoc(t, s, "docs", "k1", "en"); doc != nil {
		t.Errorf("a user without a grant wrote a document: %+v", doc)
	}
	if doc := covFStoredDoc(t, s, "docs", "existing", "en"); doc == nil {
		t.Error("a user without a grant deleted the collection")
	}
}

// A TTL given with the document is applied in the same call: the stored
// document expires about TTL seconds from now.
func TestCovFAddDocumentAppliesItsTTL(t *testing.T) {
	a, s := covFAdapter(t)
	ttl := 120
	doc, err := a.AddDocument(context.Background(), gql.AddDocumentInput{
		Collection: "docs", Key: "short-lived", Lang: "en", ContentMd: "gone soon", TTL: &ttl,
	})
	if err != nil {
		t.Fatal(err)
	}
	if doc.Key != "short-lived" || doc.ContentMd != "gone soon" {
		t.Errorf("returned document: %+v", doc)
	}
	stored := covFStoredDoc(t, s, "docs", "short-lived", "en")
	if stored == nil {
		t.Fatal("document was not stored")
	}
	now := time.Now().Unix()
	if stored.ExpiresAt < now+110 || stored.ExpiresAt > now+130 {
		t.Errorf("ExpiresAt = %d, want about %d", stored.ExpiresAt, now+120)
	}
}

// A document the store refuses produces an error and no document.
func TestCovFAddDocumentReportsARefusedWrite(t *testing.T) {
	a, _ := covFAdapter(t)
	doc, err := a.AddDocument(context.Background(), gql.AddDocumentInput{Collection: "docs", Lang: "en", ContentMd: "no key"})
	if err == nil || doc != nil {
		t.Fatalf("AddDocument without a key = %v, %v; want an error", doc, err)
	}
}

// Failures from the storage layer surface unchanged rather than as an empty
// success.
func TestCovFStorageFailuresReachTheCaller(t *testing.T) {
	a, _ := covFAdapter(t)
	ctx := context.Background()

	if doc, err := a.UpdateDocument(ctx, gql.UpdateDocumentInput{Collection: "docs", Key: "missing", Lang: "en"}); err == nil || doc != nil {
		t.Errorf("UpdateDocument of a missing document = %v, %v", doc, err)
	}
	if n, err := a.DeleteCollection(ctx, ""); err == nil || n != 0 {
		t.Errorf("DeleteCollection(\"\") = %d, %v", n, err)
	}
	if doc, err := a.SetTTL(ctx, "docs", "missing", "en", 60); err == nil || doc != nil {
		t.Errorf("SetTTL of a missing document = %v, %v", doc, err)
	}
	if res, err := a.FullTextSearch(ctx, gql.FTSInput{Collection: "docs"}); err == nil || res != nil {
		t.Errorf("FullTextSearch without a query = %v, %v", res, err)
	}
}

// A corrupt record makes the listing fail instead of silently returning a
// shorter page.
func TestCovFSearchReportsACorruptDocument(t *testing.T) {
	a, s := covFAdapter(t)
	addTestDoc(t, s, "docs", "good", "en", "fine", nil)
	if err := s.DBUpdateC("docs", func(tx *bolt.Tx) error {
		return tx.Bucket([]byte("docs")).Put([]byte("doc|docs|corrupt"), []byte("not a document"))
	}); err != nil {
		t.Fatal(err)
	}
	conn, err := a.SearchDocuments(context.Background(), gql.SearchInput{Collection: "docs"})
	if err == nil || conn != nil {
		t.Fatalf("SearchDocuments over a corrupt record = %v, %v; want an error", conn, err)
	}
}

// A nil entry in a batch is skipped rather than crashing the request or
// counting as a failure.
func TestCovFAddBatchSkipsNilEntries(t *testing.T) {
	a, s := covFAdapter(t)
	yes := true
	res, err := a.AddBatch(context.Background(), "docs", []*gql.AddBatchDocumentInput{
		nil,
		{Key: "b1", Lang: "en", ContentMd: "batch one", SaveRevision: &yes},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Added != 1 || res.Failed != 0 {
		t.Errorf("result = %+v, want one added and none failed", res)
	}
	if covFStoredDoc(t, s, "docs", "b1", "en") == nil {
		t.Error("the batch document was not stored")
	}
}

// Every optional ingest field reaches the stored document: the explicit key,
// frontmatter extraction, and the provenance metadata.
func TestCovFIngestCarriesEveryOptionalField(t *testing.T) {
	a, s := covFAdapter(t)
	key, url, scraper := "page-1", "https://example.com/page", "covF-bot"
	yes, no := true, false
	scrapedAt, ttl := 1700000000, 300
	res, err := a.IngestDocuments(context.Background(), "scraped", []*gql.IngestDocumentInput{
		nil,
		{
			URL: &url, Key: &key, Lang: "en",
			ContentMd:          "---\ntitle: From frontmatter\n---\nbody text",
			Meta:               []*gql.MetaInput{{Key: "tag", Values: []string{"t1"}}},
			ExtractFrontmatter: &yes, ScrapedAt: &scrapedAt, Scraper: &scraper, TTL: &ttl,
		},
	}, &gql.IngestOptionsInput{
		SkipDuplicates: &no, SkipEmbeddings: &yes, SkipFts: &no, SkipWebhooks: &yes,
		AutoConfigureCollection: &yes, SaveRevision: &no,
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Added != 1 || res.Failed != 0 || res.Collection != "scraped" || res.DurationMs < 0 {
		t.Errorf("result = %+v", res)
	}
	doc := covFStoredDoc(t, s, "scraped", "page-1", "en")
	if doc == nil {
		t.Fatal("ingested document was not stored under its explicit key")
	}
	for k, want := range map[string]string{
		"title": "From frontmatter", "tag": "t1", "source_url": url,
		"scraper": scraper, "scraped_at": "1700000000",
	} {
		if got := doc.Meta[k]; len(got) != 1 || got[0] != want {
			t.Errorf("meta[%q] = %v, want [%s]", k, got, want)
		}
	}
	if cfg, ok := s.CollectionManager.Get("scraped"); !ok || cfg.Type != "scraping" {
		t.Errorf("autoConfigureCollection did not configure the collection: %+v", cfg)
	}
}

// When every document is refused — here by the collection's schema — the
// ingest is an error, not a success reporting zero added.
func TestCovFIngestFailsWhenEveryDocumentFails(t *testing.T) {
	a, s := covFAdapter(t)
	if err := s.SchemaManager.Set("docs", `{"required":["title"]}`); err != nil {
		t.Fatal(err)
	}
	key := "untitled"
	res, err := a.IngestDocuments(context.Background(), "docs", []*gql.IngestDocumentInput{
		{Key: &key, Lang: "en", ContentMd: "no title in meta"},
	}, nil)
	if err == nil || res != nil {
		t.Fatalf("IngestDocuments with only failing documents = %+v, %v; want an error", res, err)
	}
}

// ImportURL fetches the page, stores it under the requested key and applies
// the TTL. Loopback is opened for this test only — the SSRF guard refuses it
// by default.
func TestCovFImportURLStoresTheFetchedPage(t *testing.T) {
	t.Setenv("MDDB_OUTBOUND_ALLOW_PRIVATE", "true")
	page := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("---\nauthor: remote\n---\nimported body"))
	}))
	defer page.Close()

	a, s := covFAdapter(t)
	key, ttl := "imported", 600
	doc, err := a.ImportURL(context.Background(), "docs", page.URL+"/post", &key, "en",
		[]*gql.MetaInput{{Key: "author", Values: []string{"override"}}}, &ttl)
	if err != nil {
		t.Fatal(err)
	}
	if doc.Key != "imported" || doc.ContentMd != "imported body" {
		t.Errorf("returned document: %+v", doc)
	}
	stored := covFStoredDoc(t, s, "docs", "imported", "en")
	if stored == nil {
		t.Fatal("imported document was not stored")
	}
	if got := stored.Meta["author"]; len(got) != 1 || got[0] != "override" {
		t.Errorf("request meta did not override frontmatter: %v", got)
	}
	if now := time.Now().Unix(); stored.ExpiresAt < now+590 || stored.ExpiresAt > now+610 {
		t.Errorf("ExpiresAt = %d, want about %d", stored.ExpiresAt, now+600)
	}
}

// An oversample outside 1–10 is refused, not clamped (SRCH-005).
func TestCovFVectorSearchRefusesAnOutOfRangeOversample(t *testing.T) {
	a, _ := covFAdapter(t)
	over := 50.0
	res, err := a.VectorSearch(context.Background(), gql.VectorSearchInput{Collection: "docs", QueryVector: []float64{1, 0, 0}, Oversample: &over})
	if err == nil || res != nil {
		t.Fatalf("VectorSearch with oversample 50 = %v, %v; want an error", res, err)
	}
}

// A search with neither text nor vector is a caller error from the engine.
func TestCovFVectorSearchNeedsAQuery(t *testing.T) {
	a, _ := covFAdapter(t)
	res, err := a.VectorSearch(context.Background(), gql.VectorSearchInput{Collection: "docs"})
	if err == nil || res != nil {
		t.Fatalf("VectorSearch without a query = %v, %v; want an error", res, err)
	}
}

// A vector query returns the nearest document first, with its score, rank and
// content, and the response names the model and dimensions.
func TestCovFVectorSearchRanksByTheGivenVector(t *testing.T) {
	a, s := covFAdapter(t)
	s.VectorIndex.SetReady()
	addTestDoc(t, s, "docs", "east", "en", "points east", nil)
	addTestDoc(t, s, "docs", "north", "en", "points north", nil)
	for key, v := range map[string][]float32{"east": {1, 0, 0}, "north": {0, 1, 0}} {
		if err := s.VectorIndex.Add("docs", genID("docs", key, "en"), v); err != nil {
			t.Fatal(err)
		}
	}

	text, topK, threshold, over, yes := "ignored when a vector is given", 2, 0.0, 2.0, true
	res, err := a.VectorSearch(context.Background(), gql.VectorSearchInput{
		Collection: "docs", Query: &text, QueryVector: []float64{1, 0.1, 0},
		TopK: &topK, Threshold: &threshold, IncludeContent: &yes, Oversample: &over,
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Total != len(res.Results) || len(res.Results) == 0 {
		t.Fatalf("response = %+v", res)
	}
	first := res.Results[0]
	if first.Document.Key != "east" || first.Document.ContentMd != "points east" || first.Rank != 1 || first.Score <= 0 {
		t.Errorf("first result = %+v / %+v", first, first.Document)
	}
	if res.Model == nil || res.Dimensions == nil {
		t.Error("model and dimensions must always be present")
	}
}

// Without an embedding provider the stats say "disabled" rather than failing.
func TestCovFVectorStatsWithoutAProvider(t *testing.T) {
	a, s := covFAdapter(t)
	addTestDoc(t, s, "docs", "d1", "en", "one", nil)
	st, err := a.VectorStats(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if st.Enabled || st.IndexReady {
		t.Errorf("stats report vectors enabled with no provider: %+v", st)
	}
}

// A reindex embeds the collection and answers with the provider's stats.
func TestCovFVectorReindexReturnsFreshStats(t *testing.T) {
	a, s := covFAdapter(t)
	s.Embedding = &mockEmbedding{dims: 3, model: "covF-model"}
	addTestDoc(t, s, "docs", "r1", "en", "a document to embed", nil)
	force := true
	st, err := a.VectorReindex(context.Background(), "docs", &force)
	if err != nil {
		t.Fatal(err)
	}
	if !st.Enabled || st.Model == nil || *st.Model != "covF-model" || st.Dimensions == nil || *st.Dimensions != 3 {
		t.Errorf("stats after reindex = %+v", st)
	}
}

// A filter that is not a map of conditions is refused before any search runs,
// by both the listing and the vector search.
func TestCovFMalformedFilterIsRefused(t *testing.T) {
	a, _ := covFAdapter(t)
	bad := map[string]any{"category": 5}
	if conn, err := a.SearchDocuments(context.Background(), gql.SearchInput{Collection: "docs", Filter: bad}); err == nil || conn != nil {
		t.Errorf("SearchDocuments with a malformed filter = %v, %v; want an error", conn, err)
	}
	q := "x"
	if res, err := a.VectorSearch(context.Background(), gql.VectorSearchInput{Collection: "docs", Query: &q, Filter: bad}); err == nil || res != nil {
		t.Errorf("VectorSearch with a malformed filter = %v, %v; want an error", res, err)
	}
}

// With groupBy, each result names its group and its rank inside the group,
// and no group keeps more than perGroup results (SRCH-016).
func TestCovFVectorSearchGroupsResults(t *testing.T) {
	a, s := covFAdapter(t)
	s.VectorIndex.SetReady()
	docs := map[string]struct {
		cat string
		v   []float32
	}{
		"t1": {"tech", []float32{1, 0, 0}},
		"t2": {"tech", []float32{0.9, 0.1, 0}},
		"c1": {"cooking", []float32{0, 1, 0}},
	}
	for key, d := range docs {
		addTestDoc(t, s, "docs", key, "en", "about "+d.cat, map[string][]string{"category": {d.cat}})
		if err := s.VectorIndex.Add("docs", genID("docs", key, "en"), d.v); err != nil {
			t.Fatal(err)
		}
	}
	one, topK := 1, 10
	res, err := a.VectorSearch(context.Background(), gql.VectorSearchInput{
		Collection: "docs", QueryVector: []float64{1, 0, 0}, TopK: &topK,
		GroupBy: &gql.GroupByInput{Key: "category", PerGroup: &one},
	})
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]int{}
	for _, r := range res.Results {
		if r.Group == nil || r.GroupRank == nil {
			t.Fatalf("grouped result without group fields: %+v", r)
		}
		if *r.GroupRank != 1 {
			t.Errorf("perGroup 1 returned group rank %d", *r.GroupRank)
		}
		seen[*r.Group]++
	}
	if seen["tech"] != 1 || seen["cooking"] != 1 || len(seen) != 2 {
		t.Errorf("groups = %v, want one tech and one cooking", seen)
	}
}
