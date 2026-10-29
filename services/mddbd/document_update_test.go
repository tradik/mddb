package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	gql "mddb/graphql"
	json "mddb/internal/jsonx"
	"mddb/internal/schema"
	"mddb/internal/storage"
	proto "mddb/proto"

	bolt "go.etcd.io/bbolt"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestUpdate_ConcurrentEditsOneConflicts(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	doc := addTestDoc(t, s, "fs", "notes.md", "en", "line one\n", nil)
	base := docRevision(&doc)

	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := range errs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			content := "edit " + itoa(i) + "\n"
			_, errs[i] = s.updateDocument("fs", "notes.md", "en", DocPatch{Content: &content, ExpectedRevision: base})
		}(i)
	}
	wg.Wait()
	conflicts := 0
	for _, err := range errs {
		var c *RevisionConflictError
		if errors.As(err, &c) {
			conflicts++
			if c.Expected != base || c.Current == "" || c.Current == base {
				t.Fatalf("conflict: %+v", c)
			}
		} else if err != nil {
			t.Fatal(err)
		}
	}
	if conflicts != 1 {
		t.Fatalf("exactly one edit must lose, got %d conflicts", conflicts)
	}
	// The survivor's write is what is stored, and its revision moved on.
	cur, _ := s.loadDocByRef("fs", "notes.md", "en")
	if !strings.HasPrefix(cur.ContentMD, "edit ") || docRevision(cur) == base {
		t.Fatalf("stored: %+v", cur)
	}
}

func TestUpdate_UnconditionalAndAdd(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	addTestDoc(t, s, "fs", "a.md", "en", "x", nil)
	content := "y"
	if _, err := s.updateDocument("fs", "a.md", "en", DocPatch{Content: &content}); err != nil {
		t.Fatal("no expectedRevision is unconditional, as before")
	}
	if _, err := s.updateDocument("fs", "missing", "en", DocPatch{Content: &content}); !errors.Is(err, errDocNotFound) {
		t.Fatalf("missing: %v", err)
	}
	if _, err := s.updateDocument("fs", "a.md", "en", DocPatch{}); err == nil {
		t.Fatal("empty patch")
	}
	if _, err := s.updateDocument("", "a.md", "en", DocPatch{Content: &content}); err == nil {
		t.Fatal("missing collection")
	}
	// A conditional add of a document that does not exist yet conflicts.
	var c *RevisionConflictError
	if _, _, err := s.addDocumentIf("fs", "new.md", "en", nil, "z", 0, true, "abc"); !errors.As(err, &c) || c.Current != "" {
		t.Fatalf("conditional create: %v", err)
	}
	cur, _ := s.loadDocByRef("fs", "a.md", "en")
	if _, _, err := s.addDocumentIf("fs", "a.md", "en", nil, "w", 0, true, docRevision(cur)); err != nil {
		t.Fatalf("conditional upsert at the right revision: %v", err)
	}
	if docRevision(nil) != "" || (&RevisionConflictError{Expected: "a"}).Error() == "" {
		t.Fatal("helpers")
	}
}

func TestUpdate_SurfacesReportConflicts(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	doc := addTestDoc(t, s, "fs", "a.go", "en", "package a\n\nfunc A() {}\n", nil)
	rev := docRevision(&doc)

	// REST get returns the revision; PATCH with a stale one is a 409.
	rec := doRequest(t, s.handleGet, map[string]any{"collection": "fs", "key": "a.go", "lang": "en"})
	if !strings.Contains(rec.Body.String(), `"revision":"`+rev+`"`) {
		t.Fatalf("get: %s", rec.Body.String())
	}
	patch := func(body string) *httptest.ResponseRecorder {
		r := httptest.NewRecorder()
		s.handleUpdate(r, httptest.NewRequest(http.MethodPatch, "/v1/update", strings.NewReader(body)))
		return r
	}
	rec = patch(`{"collection":"fs","key":"a.go","lang":"en","contentMd":"package a\n\nfunc B() {}\n","expectedRevision":"` + rev + `"}`)
	var updated DocResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &updated); err != nil || rec.Code != 200 || updated.Revision == rev {
		t.Fatalf("update: %d %s", rec.Code, rec.Body.String())
	}
	// Symbols follow the content on update now, not only on add.
	if updated.Meta[MetaKeyDefines][0] != "B" {
		t.Fatalf("code symbols re-extracted: %v", updated.Meta)
	}
	rec = patch(`{"collection":"fs","key":"a.go","lang":"en","contentMd":"x","expectedRevision":"` + rev + `"}`)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), updated.Revision) {
		t.Fatalf("stale PATCH: %d %s", rec.Code, rec.Body.String())
	}
	if rec = patch(`{"collection":"fs","key":"nope","lang":"en","contentMd":"x"}`); rec.Code != 404 {
		t.Fatalf("missing: %d", rec.Code)
	}
	rec = doRequest(t, s.handleAdd, map[string]any{"collection": "fs", "key": "a.go", "lang": "en", "contentMd": "y", "expectedRevision": rev})
	if rec.Code != http.StatusConflict {
		t.Fatalf("stale add: %d", rec.Code)
	}

	// MCP: the update tool takes expected_revision; the conflict is an error.
	tools := &MCPToolServer{client: NewDirectClient(s)}
	ctx := context.Background()
	if _, err := tools.toolUpdateDocument(ctx, map[string]interface{}{"collection": "fs", "key": "a.go", "lang": "en",
		"content_md": "z", "expected_revision": rev}); err == nil || !strings.Contains(err.Error(), "revision conflict") {
		t.Fatalf("MCP stale: %v", err)
	}
	out, err := tools.toolUpdateDocument(ctx, map[string]interface{}{"collection": "fs", "key": "a.go", "lang": "en",
		"meta": map[string]interface{}{"owner": "ann"}, "expected_revision": updated.Revision})
	if err != nil || !strings.Contains(out, `"revision"`) {
		t.Fatalf("MCP update: %v %s", err, out)
	}
	// The MCP update used to skip the metadata index: the new meta is now searchable.
	if ids, _ := s.allowedDocIDs("fs", map[string][]string{"owner": {"ann"}}, nil); len(ids) != 1 {
		t.Fatal("MCP meta update must reach the index")
	}
	if _, err := tools.toolAddDocument(ctx, map[string]interface{}{"collection": "fs", "key": "a.go", "lang": "en",
		"content_md": "q", "expected_revision": "stale"}); err == nil {
		t.Fatal("MCP add stale")
	}
	got, err := NewDirectClient(s).Get(ctx, &MCPGetRequest{Collection: "fs", Key: "a.go", Lang: "en"})
	if err != nil || got.Revision == "" {
		t.Fatalf("MCP get revision: %v %+v", err, got)
	}

	// gRPC: ABORTED on conflict, NOT_FOUND on a missing document.
	g := &GRPCServer{server: s}
	_, err = g.UpdateDocument(ctx, &proto.UpdateDocumentRequest{Collection: "fs", Key: "a.go", Lang: "en", UpdateContent: true,
		ContentMd: "g", ExpectedRevision: rev})
	if status.Code(err) != codes.Aborted {
		t.Fatalf("gRPC stale: %v", err)
	}
	pd, err := g.UpdateDocument(ctx, &proto.UpdateDocumentRequest{Collection: "fs", Key: "a.go", Lang: "en", UpdateContent: true,
		ContentMd: "g", ExpectedRevision: got.Revision})
	if err != nil || pd.Revision == "" {
		t.Fatalf("gRPC update: %v %+v", err, pd)
	}
	if _, err := g.UpdateDocument(ctx, &proto.UpdateDocumentRequest{Collection: "fs", Key: "zz", Lang: "en", UpdateContent: true}); status.Code(err) != codes.NotFound {
		t.Fatalf("gRPC missing: %v", err)
	}
	if _, err := g.UpdateDocument(ctx, &proto.UpdateDocumentRequest{Collection: "fs", Key: "a.go", Lang: "en", UpdateMeta: true,
		Meta: map[string]*proto.MetaValues{"k": {Values: []string{"v"}}}}); err != nil {
		t.Fatalf("gRPC meta: %v", err)
	}
	if pg, err := g.Get(ctx, &proto.GetRequest{Collection: "fs", Key: "a.go", Lang: "en"}); err != nil || pg.Revision == "" {
		t.Fatalf("gRPC get revision: %v", err)
	}

	// GraphQL: expectedRevision on the input, revision on the document.
	adapter := &GraphQLAdapter{server: s, mcp: NewDirectClient(s)}
	stale := rev
	if _, err := adapter.UpdateDocument(ctx, gql.UpdateDocumentInput{Collection: "fs", Key: "a.go", Lang: "en",
		ContentMd: &stale, ExpectedRevision: &stale}); err == nil {
		t.Fatal("GraphQL stale")
	}
	cur, _ := s.loadDocByRef("fs", "a.go", "en")
	curRev := docRevision(cur)
	gd, err := adapter.UpdateDocument(ctx, gql.UpdateDocumentInput{Collection: "fs", Key: "a.go", Lang: "en",
		ContentMd: &stale, ExpectedRevision: &curRev})
	if err != nil || gd.Revision == nil {
		t.Fatalf("GraphQL update: %v %+v", err, gd)
	}
}

func TestUpdate_TTLAndSchema(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	addTestDoc(t, s, "fs", "a.md", "en", "x", nil)
	ttl := int64(60)
	d, err := s.updateDocument("fs", "a.md", "en", DocPatch{TTL: &ttl})
	if err != nil || d.ExpiresAt == 0 {
		t.Fatalf("ttl: %v %+v", err, d)
	}
	zero := int64(0)
	if d, _ = s.updateDocument("fs", "a.md", "en", DocPatch{TTL: &zero}); d.ExpiresAt != 0 {
		t.Fatal("ttl 0 clears the expiry")
	}
	rec := httptest.NewRecorder()
	writeDocWriteError(rec, errors.New("boom"))
	if rec.Code != 400 {
		t.Fatalf("other errors: %d", rec.Code)
	}
}

// The gRPC Search walk cut document IDs wrongly and found nothing; it uses
// the shared index walk now.
func TestGRPCSearch_FindsDocuments(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	addTestDoc(t, s, "fs", "a/x.md", "en", "x", map[string][]string{"dir": {"/a"}})
	addTestDoc(t, s, "fs", "b/y.md", "en", "y", map[string][]string{"dir": {"/b"}})
	g := &GRPCServer{server: s}
	all, err := g.Search(context.Background(), &proto.SearchRequest{Collection: "fs"})
	if err != nil || len(all.Documents) != 2 {
		t.Fatalf("no filter: %v %+v", err, all)
	}
	one, err := g.Search(context.Background(), &proto.SearchRequest{Collection: "fs",
		FilterMeta: map[string]*proto.MetaValues{"dir": {Values: []string{"/b"}}}})
	if err != nil || len(one.Documents) != 1 || one.Documents[0].Key != "b/y.md" {
		t.Fatalf("filtered: %v %+v", err, one)
	}
}

// The refusals updateDocument shares across transports: a schema violation,
// an expired document, a corrupt one; and history trimmed to MaxRevisions.
func TestUpdate_RefusalsAndRetention(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	s.SchemaManager = schema.NewSchemaManager(s.DB)
	if err := s.SchemaManager.EnsureBucket(); err != nil {
		t.Fatal(err)
	}
	addTestDoc(t, s, "fs", "a.md", "en", "x", map[string][]string{"author": {"ann"}})
	if err := s.SchemaManager.Set("fs", `{"required":["author"]}`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.updateDocument("fs", "a.md", "en", DocPatch{HasMeta: true, Meta: map[string][]string{"tag": {"x"}}}); err == nil {
		t.Fatal("schema violation must be refused")
	}

	// Revisions are trimmed to the collection's MaxRevisions.
	if err := s.CollectionManager.Set("fs", &CollectionConfig{MaxRevisions: 1}); err != nil {
		t.Fatal(err)
	}
	for i := range 3 {
		c := "v" + itoa(i)
		if _, err := s.updateDocument("fs", "a.md", "en", DocPatch{Content: &c}); err != nil {
			t.Fatal(err)
		}
	}

	// An expired document is not there to update.
	doc := addTestDoc(t, s, "fs", "old.md", "en", "x", map[string][]string{"author": {"ann"}})
	doc.ExpiresAt = 1
	buf, _ := json.Marshal(doc)
	_ = s.DB.Update(func(tx *bolt.Tx) error { return tx.Bucket([]byte("docs")).Put(storage.DocKey("fs", doc.ID), buf) })
	c := "y"
	if _, err := s.updateDocument("fs", "old.md", "en", DocPatch{Content: &c}); !errors.Is(err, errDocNotFound) {
		t.Fatalf("expired: %v", err)
	}
	// A metadata value too long for an index key fails the update and leaves
	// the stored document as it was (the transaction rolls back).
	addTestDoc(t, s, "big", "b.md", "en", "x", nil)
	if _, err := s.updateDocument("big", "b.md", "en", DocPatch{HasMeta: true,
		Meta: map[string][]string{"k": {strings.Repeat("v", 40000)}}}); err == nil {
		t.Fatal("an oversized index key must fail")
	}
	if cur, _ := s.loadDocByRef("big", "b.md", "en"); len(cur.Meta) != 0 {
		t.Fatal("rolled back")
	}
	// A document that no longer decodes is not found either.
	_ = s.DB.Update(func(tx *bolt.Tx) error { return tx.Bucket([]byte("docs")).Put(storage.DocKey("fs", doc.ID), []byte("{")) })
	if _, err := s.updateDocument("fs", "old.md", "en", DocPatch{Content: &c}); !errors.Is(err, errDocNotFound) {
		t.Fatalf("corrupt: %v", err)
	}
}
