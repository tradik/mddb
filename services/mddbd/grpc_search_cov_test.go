package main

// Coverage for grpc_search.go (covE batch): the search RPCs that delegate to
// the shared search code, plus TTL, URL import, reindexing, the advisor and
// the indexing status.

import (
	"context"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"

	"mddb/internal/storage"
	pb "mddb/proto"

	bolt "go.etcd.io/bbolt"
	"google.golang.org/grpc/codes"
)

func covESearchGuarded(gs *GRPCServer) []covERPC {
	return []covERPC{
		{"VectorSearch", func(ctx context.Context) error {
			_, err := gs.VectorSearch(ctx, &pb.VectorSearchRequest{Collection: "c", Query: "q"})
			return err
		}},
		{"VectorReindex", func(ctx context.Context) error {
			_, err := gs.VectorReindex(ctx, &pb.VectorReindexRequest{Collection: "c"})
			return err
		}},
		{"VectorStats", func(ctx context.Context) error {
			_, err := gs.VectorStats(ctx, &pb.VectorStatsRequest{})
			return err
		}},
		{"DeleteBatch", func(ctx context.Context) error {
			_, err := gs.DeleteBatch(ctx, &pb.DeleteBatchRequest{Collection: "c"})
			return err
		}},
		{"UpdateBatch", func(ctx context.Context) error {
			_, err := gs.UpdateBatch(ctx, &pb.UpdateBatchRequest{Collection: "c"})
			return err
		}},
		{"ImportURL", func(ctx context.Context) error {
			_, err := gs.ImportURL(ctx, &pb.ImportURLRequest{Collection: "c", Url: "https://example.com/a.md", Lang: "en"})
			return err
		}},
		{"SetTTL", func(ctx context.Context) error {
			_, err := gs.SetTTL(ctx, &pb.SetTTLRequest{Collection: "c", Key: "k", Lang: "en"})
			return err
		}},
		{"FTS", func(ctx context.Context) error {
			_, err := gs.FTS(ctx, &pb.FTSRequest{Collection: "c", Query: "q"})
			return err
		}},
		{"FTSReindex", func(ctx context.Context) error {
			_, err := gs.FTSReindex(ctx, &pb.FTSReindexRequest{Collection: "c"})
			return err
		}},
		{"HybridSearch", func(ctx context.Context) error {
			_, err := gs.HybridSearch(ctx, &pb.HybridSearchRequest{Collection: "c", Query: "q"})
			return err
		}},
		{"SearchAdvisor", func(ctx context.Context) error {
			_, err := gs.SearchAdvisor(ctx, &pb.SearchAdvisorRequest{Collection: "c"})
			return err
		}},
		{"GetIndexingStatus", func(ctx context.Context) error {
			_, err := gs.GetIndexingStatus(ctx, &pb.IndexingStatusRequest{})
			return err
		}},
	}
}

func TestCovESearchPermissionDenied(t *testing.T) {
	gs, s := covEServer(t)
	covEDenyAll(t, s)
	covERun(t, context.Background(), covESearchGuarded(gs), codes.PermissionDenied, "")
}

func TestCovESearchReadOnly(t *testing.T) {
	gs, s := covEServer(t)
	s.Mode = ModeRead
	var writes []covERPC
	for _, r := range covESearchGuarded(gs) {
		switch r.name {
		case "VectorReindex", "DeleteBatch", "UpdateBatch", "ImportURL", "SetTTL", "FTSReindex":
			writes = append(writes, r)
		}
	}
	covERun(t, context.Background(), writes, codes.PermissionDenied, "read-only mode")
}

func TestCovESearchValidation(t *testing.T) {
	gs, _ := covEServer(t)
	ctx := context.Background()
	cases := []struct {
		name string
		call func() error
		part string
	}{
		{"vector oversample", func() error {
			_, err := gs.VectorSearch(ctx, &pb.VectorSearchRequest{Collection: "c", Query: "q", Oversample: 1e6})
			return err
		}, "oversample"},
		{"hybrid missing query", func() error {
			_, err := gs.HybridSearch(ctx, &pb.HybridSearchRequest{Collection: "c"})
			return err
		}, "missing required fields"},
		{"hybrid strategy", func() error {
			_, err := gs.HybridSearch(ctx, &pb.HybridSearchRequest{Collection: "c", Query: "q", Strategy: "nope"})
			return err
		}, "unknown strategy"},
		{"hybrid distance sort", func() error {
			_, err := gs.HybridSearch(ctx, &pb.HybridSearchRequest{Collection: "c", Query: "q", Sort: "distance"})
			return err
		}, "only supported over HTTP"},
		{"hybrid unknown sort", func() error {
			_, err := gs.HybridSearch(ctx, &pb.HybridSearchRequest{Collection: "c", Query: "q", Sort: "nope"})
			return err
		}, "unknown sort"},
		{"advisor without collection", func() error {
			_, err := gs.SearchAdvisor(ctx, &pb.SearchAdvisorRequest{})
			return err
		}, "collection is required"},
		{"import without key", func() error {
			_, err := gs.ImportURL(ctx, &pb.ImportURLRequest{Collection: "c", Url: "https://example.com/", Lang: "en"})
			return err
		}, "cannot derive key"},
	}
	for _, c := range cases {
		covEWant(t, c.name, c.call(), codes.InvalidArgument, c.part)
	}
}

func TestCovESearchAdvisor(t *testing.T) {
	gs, s := covEServer(t)
	ctx := context.Background()
	addTestDoc(t, s, "c", "a", "en", "deploy the service with one command", nil)
	addTestDoc(t, s, "c", "b", "en", "backup and restore the database", nil)

	resp, err := gs.SearchAdvisor(ctx, &pb.SearchAdvisorRequest{Collection: "c"})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Profile.Collection != "c" || resp.Profile.Documents != 2 || resp.Profile.Sampled != 2 ||
		resp.SearchType == "" || len(resp.Reasons) == 0 {
		t.Fatalf("advisor: %+v", resp)
	}
	if resp.Profile.Semantic != nil {
		t.Errorf("semantic = %v with nothing embedded, want unset", *resp.Profile.Semantic)
	}

	covEDenyAll(t, s)
	_, err = gs.SearchAdvisor(ctx, &pb.SearchAdvisorRequest{Collection: "c"})
	covEWant(t, "advisor denied", err, codes.PermissionDenied, "forbidden")
	s.AuthManager = nil

	covECloseDB(t, s)
	_, err = gs.SearchAdvisor(ctx, &pb.SearchAdvisorRequest{Collection: "c"})
	covEWant(t, "advisor closed db", err, codes.Internal, "database not open")
}

func TestCovEClampInt32(t *testing.T) {
	for _, c := range []struct {
		in   int
		want int32
	}{
		{7, 7},
		{math.MaxInt32 + 1, math.MaxInt32},
		{math.MinInt32 - 1, math.MinInt32},
	} {
		if got := clampInt32(c.in); got != c.want {
			t.Fatalf("clampInt32(%d) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestCovEImportURL(t *testing.T) {
	t.Setenv("MDDB_OUTBOUND_ALLOW_PRIVATE", "true")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/guides/intro.md" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte("---\ntitle: Intro\n---\n# Hello\n\nBody text."))
	}))
	defer srv.Close()

	gs, s := covEServer(t)
	ctx := context.Background()

	doc, err := gs.ImportURL(ctx, &pb.ImportURLRequest{Collection: "c", Url: srv.URL + "/guides/intro.md", Lang: "en",
		Meta: map[string]*pb.MetaValues{"source": {Values: []string{"web"}}}})
	if err != nil {
		t.Fatal(err)
	}
	if doc.Key != "intro" || doc.Meta["title"].Values[0] != "Intro" || doc.Meta["source"].Values[0] != "web" ||
		doc.ContentMd != "# Hello\n\nBody text." {
		t.Fatalf("imported: %+v", doc)
	}

	_, err = gs.ImportURL(ctx, &pb.ImportURLRequest{Collection: "c", Key: "k", Url: srv.URL + "/missing.md", Lang: "en"})
	covEWant(t, "404", err, codes.Internal, "fetch failed")

	// A body without frontmatter still gets metadata from the request; a
	// schema it does not satisfy makes the write fail.
	if err := s.SchemaManager.Set("c", `{"required":["author"]}`); err != nil {
		t.Fatal(err)
	}
	_, err = gs.ImportURL(ctx, &pb.ImportURLRequest{Collection: "c", Key: "k2", Url: srv.URL + "/guides/intro.md", Lang: "en"})
	covEWant(t, "schema", err, codes.Internal, "author")
}

func TestCovEImportURLWithoutFrontmatter(t *testing.T) {
	t.Setenv("MDDB_OUTBOUND_ALLOW_PRIVATE", "true")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("plain body"))
	}))
	defer srv.Close()
	gs, _ := covEServer(t)
	doc, err := gs.ImportURL(context.Background(), &pb.ImportURLRequest{Collection: "c", Url: srv.URL + "/plain.txt", Lang: "en",
		Meta: map[string]*pb.MetaValues{"k": {Values: []string{"v"}}}})
	if err != nil || doc.Key != "plain" || doc.ContentMd != "plain body" || doc.Meta["k"].Values[0] != "v" {
		t.Fatalf("plain import: %v %+v", err, doc)
	}
}

func TestCovESetTTLFailures(t *testing.T) {
	gs, s := covEServer(t)
	ctx := context.Background()
	if err := s.DBUpdateC("c", func(tx *bolt.Tx) error {
		return tx.Bucket([]byte("docs")).Put(storage.DocKey("c", genID("c", "bad", "en")), []byte{0xff, 0x00})
	}); err != nil {
		t.Fatal(err)
	}
	_, err := gs.SetTTL(ctx, &pb.SetTTLRequest{Collection: "c", Key: "bad", Lang: "en", Ttl: 10})
	covEWant(t, "corrupt doc", err, codes.Internal, "")

	covECloseDB(t, s)
	_, err = gs.SetTTL(ctx, &pb.SetTTLRequest{Collection: "c", Key: "k", Lang: "en", Ttl: 10})
	covEWant(t, "closed db", err, codes.Internal, "database not open")
}

func TestCovEReindexFailures(t *testing.T) {
	gs, s := covEServer(t)
	ctx := context.Background()
	s.Embedding = &mockEmbedding{dims: 3, model: "covE"}
	covECloseDB(t, s)
	_, err := gs.VectorReindex(ctx, &pb.VectorReindexRequest{Collection: "c"})
	covEWant(t, "vector reindex", err, codes.Internal, "database not open")
	_, err = gs.FTSReindex(ctx, &pb.FTSReindexRequest{Collection: "c"})
	covEWant(t, "fts reindex", err, codes.Internal, "database not open")
}

func TestCovEWithoutFTSIndex(t *testing.T) {
	gs, s := covEServer(t)
	s.FTSIndex = nil
	ctx := context.Background()
	_, err := gs.FTSReindex(ctx, &pb.FTSReindexRequest{Collection: "c"})
	covEWant(t, "fts reindex", err, codes.FailedPrecondition, "not initialized")
	langs, err := gs.FTSLanguages(ctx, &pb.FTSLanguagesRequest{})
	if err != nil || len(langs.Languages) != 0 || langs.DefaultLang != "" {
		t.Fatalf("languages without index: %v %+v", err, langs)
	}
}

func TestCovEVectorStatsWithProvider(t *testing.T) {
	_, g, cleanup := grpcParityServer(t)
	defer cleanup()
	resp, err := g.VectorStats(context.Background(), &pb.VectorStatsRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if !resp.Enabled || resp.Model != "test-model" || resp.Provider != "test-model" || resp.Dimensions != 3 ||
		resp.Collections["repo"] == nil || resp.Collections["repo"].TotalDocuments != 3 {
		t.Fatalf("vector stats: %+v", resp)
	}
}

func TestCovEFTSFacets(t *testing.T) {
	_, g, cleanup := grpcParityServer(t)
	defer cleanup()
	resp, err := g.FTS(context.Background(), &pb.FTSRequest{Collection: "repo", Query: "backup", FacetBy: []string{"dir"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Results) == 0 || resp.Facets["dir"] == nil || len(resp.Facets["dir"].Buckets) == 0 {
		t.Fatalf("fts facets: %+v", resp)
	}
}

func TestCovEIndexingStatusForAdmin(t *testing.T) {
	gs, s := covEServer(t)
	covEDenyAll(t, s)
	resp, err := gs.GetIndexingStatus(covEAdminCtx(), &pb.IndexingStatusRequest{Collection: "c"})
	if err != nil || resp.Collection != "c" || resp.PendingEmbeddings != 0 {
		t.Fatalf("indexing status: %v %+v", err, resp)
	}
}
