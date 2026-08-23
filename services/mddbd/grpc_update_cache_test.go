package main

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"mddb/internal/cache"
	proto "mddb/proto"
)

// GO-038: gRPC UpdateDocument ran no cache invalidation at all, so a gRPC Get
// — the only read path that consults the document cache — kept serving the
// pre-update content for up to the cache TTL.
func TestGRPCUpdateDocumentInvalidatesReadCache(t *testing.T) {
	s, cleanup := newTestServer(t)
	defer cleanup()
	g := &GRPCServer{server: s}
	ctx := context.Background()

	if _, _, err := s.addDocument("posts", "hello", "en", nil, "old content", 0, true); err != nil {
		t.Fatal(err)
	}

	// Prime the read cache through the path that populates it.
	got, err := g.Get(ctx, &proto.GetRequest{Collection: "posts", Key: "hello", Lang: "en"})
	if err != nil {
		t.Fatal(err)
	}
	if got.ContentMd != "old content" {
		t.Fatalf("precondition: ContentMd = %q", got.ContentMd)
	}

	if _, err := g.UpdateDocument(ctx, &proto.UpdateDocumentRequest{
		Collection: "posts", Key: "hello", Lang: "en",
		UpdateContent: true, ContentMd: "new content",
	}); err != nil {
		t.Fatal(err)
	}

	got, err = g.Get(ctx, &proto.GetRequest{Collection: "posts", Key: "hello", Lang: "en"})
	if err != nil {
		t.Fatal(err)
	}
	if got.ContentMd != "new content" {
		t.Fatalf("gRPC Get after UpdateDocument = %q, want %q — stale read cache survived the update", got.ContentMd, "new content")
	}
}

// GO-038: PATCH /v1/update had the same gap — it reindexed FTS but left the
// document read cache untouched.
func TestRESTUpdateInvalidatesReadCache(t *testing.T) {
	s, cleanup := newTestServer(t)
	defer cleanup()
	g := &GRPCServer{server: s}
	ctx := context.Background()

	if _, _, err := s.addDocument("posts", "hello", "en", nil, "old content", 0, true); err != nil {
		t.Fatal(err)
	}
	if _, err := g.Get(ctx, &proto.GetRequest{Collection: "posts", Key: "hello", Lang: "en"}); err != nil {
		t.Fatal(err)
	}
	cacheKey := cache.BuildCacheKey("posts", "hello", "en")
	if _, found := s.Cache.Get(cacheKey); !found {
		t.Fatal("precondition: read cache not primed")
	}

	body := `{"collection":"posts","key":"hello","lang":"en","contentMd":"new content"}`
	req := httptest.NewRequest("PATCH", "/v1/update", strings.NewReader(body))
	w := httptest.NewRecorder()
	s.handleUpdate(w, req)
	if w.Code != 200 {
		t.Fatalf("PATCH /v1/update = %d: %s", w.Code, w.Body.String())
	}

	if data, found := s.Cache.Get(cacheKey); found {
		doc, err := unmarshalDoc(data)
		if err == nil && doc.ContentMD != "new content" {
			t.Fatalf("read cache still holds %q after PATCH update", doc.ContentMD)
		}
	}
	got, err := g.Get(ctx, &proto.GetRequest{Collection: "posts", Key: "hello", Lang: "en"})
	if err != nil {
		t.Fatal(err)
	}
	if got.ContentMd != "new content" {
		t.Fatalf("gRPC Get after PATCH update = %q, want %q", got.ContentMd, "new content")
	}
}

// GO-038 (second half): gRPC UpdateDocument never reindexed FTS, so the index
// kept matching the old content and never learned the new.
func TestGRPCUpdateDocumentReindexesFTS(t *testing.T) {
	s, cleanup := newTestServerWithFTS(t)
	defer cleanup()
	g := &GRPCServer{server: s}
	ctx := context.Background()

	if _, _, err := s.addDocument("posts", "hello", "en", nil, "aurora borealis tonight", 0, true); err != nil {
		t.Fatal(err)
	}
	if _, err := g.UpdateDocument(ctx, &proto.UpdateDocumentRequest{
		Collection: "posts", Key: "hello", Lang: "en",
		UpdateContent: true, ContentMd: "zenith telescope observation",
	}); err != nil {
		t.Fatal(err)
	}

	hits, err := s.FTSIndex.Search("posts", "zenith", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) == 0 {
		t.Fatal("FTS does not match the post-update content — gRPC update skipped reindexing")
	}
}
