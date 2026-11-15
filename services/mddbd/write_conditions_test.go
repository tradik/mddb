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
	"mddb/internal/storage"
	proto "mddb/proto"

	bolt "go.etcd.io/bbolt"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// conflictBody is the 409 a lost conditional write answers with.
type conflictBody struct {
	Error            string `json:"error"`
	ExpectedRevision string `json:"expectedRevision"`
	CurrentRevision  string `json:"currentRevision"`
}

func decodeConflict(t *testing.T, rec *httptest.ResponseRecorder) conflictBody {
	t.Helper()
	var b conflictBody
	if rec.Code != http.StatusConflict || json.Unmarshal(rec.Body.Bytes(), &b) != nil || b.Error != "revision conflict" {
		t.Fatalf("want a 409 revision conflict, got %d %s", rec.Code, rec.Body.String())
	}
	return b
}

// storedRevision is the revision a read of the document reports now.
func storedRevision(t *testing.T, s *Server, coll, key string) string {
	t.Helper()
	d, err := s.loadDocByRef(coll, key, "en")
	if err != nil {
		t.Fatalf("load %s/%s: %v", coll, key, err)
	}
	return docRevision(d)
}

// #423: ifAbsent creates, refuses an existing document with its revision and
// writes nothing, and cannot be combined with expectedRevision.
func TestAdd_IfAbsentREST(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	body := map[string]any{"collection": "art", "key": "a", "lang": "en", "contentMd": "first", "ifAbsent": true}
	if rec := doRequest(t, s.handleAdd, body); rec.Code != http.StatusOK {
		t.Fatalf("create: %d %s", rec.Code, rec.Body.String())
	}
	rev := storedRevision(t, s, "art", "a")
	body["contentMd"] = "second"
	got := decodeConflict(t, doRequest(t, s.handleAdd, body))
	if got.CurrentRevision != rev || got.ExpectedRevision != "" {
		t.Fatalf("conflict body: %+v, stored revision %s", got, rev)
	}
	if d, _ := s.loadDocByRef("art", "a", "en"); d.ContentMD != "first" {
		t.Fatalf("a refused create must write nothing: %q", d.ContentMD)
	}
	body["expectedRevision"] = rev
	if rec := doRequest(t, s.handleAdd, body); rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "ifAbsent") {
		t.Fatalf("ifAbsent with expectedRevision: %d %s", rec.Code, rec.Body.String())
	}
	// Without ifAbsent the add is the upsert it always was.
	delete(body, "ifAbsent")
	if rec := doRequest(t, s.handleAdd, body); rec.Code != http.StatusOK {
		t.Fatalf("conditional upsert: %d %s", rec.Code, rec.Body.String())
	}
}

// An expired document is absent to a read, so a create replaces it.
func TestAdd_IfAbsentReplacesExpired(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	doc := addTestDoc(t, s, "art", "old", "en", "stale", nil)
	doc.ExpiresAt = 1
	buf, _ := json.Marshal(doc)
	if err := s.DBUpdateC("art", func(tx *bolt.Tx) error {
		return tx.Bucket([]byte("docs")).Put(storage.DocKey("art", doc.ID), buf)
	}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.addDocumentIf("art", "old", "en", nil, "fresh", 0, true, writeCondition{IfAbsent: true}); err != nil {
		t.Fatalf("expired counts as absent: %v", err)
	}
	if d, _ := s.loadDocByRef("art", "old", "en"); d.ContentMD != "fresh" || d.ExpiresAt != 0 {
		t.Fatalf("stored: %+v", d)
	}
}

// The check is in the write transaction: of N creates racing for one key,
// exactly one wins and every other gets the winner's revision.
func TestAdd_IfAbsentConcurrentCreates(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	const n = 16
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := range errs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, _, errs[i] = s.addDocumentIf("art", "race", "en", nil, "writer "+itoa(i), 0, true, writeCondition{IfAbsent: true})
		}(i)
	}
	wg.Wait()
	wins := 0
	var currents []string
	for _, err := range errs {
		var c *RevisionConflictError
		switch {
		case err == nil:
			wins++
		case errors.As(err, &c):
			currents = append(currents, c.Current)
		default:
			t.Fatal(err)
		}
	}
	if wins != 1 || len(currents) != n-1 {
		t.Fatalf("want one create and %d conflicts, got %d and %d", n-1, wins, len(currents))
	}
	rev := storedRevision(t, s, "art", "race")
	for _, c := range currents {
		if c != rev {
			t.Fatalf("a conflict reports %s, the winner is at %s", c, rev)
		}
	}
}

// gRPC, MCP and GraphQL take ifAbsent too; each reports the conflict its
// own way.
func TestAdd_IfAbsentOtherSurfaces(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	ctx := context.Background()
	g := &GRPCServer{server: s}
	req := &proto.AddRequest{Collection: "art", Key: "g", Lang: "en", ContentMd: "x", IfAbsent: true}
	if _, err := g.Add(ctx, req); err != nil {
		t.Fatalf("gRPC create: %v", err)
	}
	if _, err := g.Add(ctx, req); status.Code(err) != codes.Aborted || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("gRPC duplicate: %v", err)
	}

	tools := &MCPToolServer{client: NewDirectClient(s)}
	args := map[string]interface{}{"collection": "art", "key": "m", "lang": "en", "content_md": "x", "if_absent": true}
	if _, err := tools.toolAddDocument(ctx, args); err != nil {
		t.Fatalf("MCP create: %v", err)
	}
	if _, err := tools.toolAddDocument(ctx, args); err == nil || !strings.Contains(err.Error(), "revision conflict") {
		t.Fatalf("MCP duplicate: %v", err)
	}

	adapter := &GraphQLAdapter{server: s, mcp: NewDirectClient(s)}
	yes := true
	in := gql.AddDocumentInput{Collection: "art", Key: "q", Lang: "en", ContentMd: "x", IfAbsent: &yes}
	if _, err := adapter.AddDocument(ctx, in); err != nil {
		t.Fatalf("GraphQL create: %v", err)
	}
	if _, err := adapter.AddDocument(ctx, in); err == nil || !strings.Contains(err.Error(), "revision conflict") {
		t.Fatalf("GraphQL duplicate: %v", err)
	}
}

// #424: a delete naming a revision removes the document only at that
// revision; a missing document conflicts with an empty current revision; a
// delete naming none behaves as it always has.
func TestDelete_ConditionalREST(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	addTestDoc(t, s, "art", "a", "en", "v1", nil)
	rev := storedRevision(t, s, "art", "a")
	content := "v2"
	if _, err := s.updateDocument("art", "a", "en", DocPatch{Content: &content}); err != nil {
		t.Fatal(err)
	}
	cur := storedRevision(t, s, "art", "a")
	del := func(body map[string]any) *httptest.ResponseRecorder { return doRequest(t, s.handleDelete, body) }

	got := decodeConflict(t, del(map[string]any{"collection": "art", "key": "a", "lang": "en", "expectedRevision": rev}))
	if got.ExpectedRevision != rev || got.CurrentRevision != cur {
		t.Fatalf("stale delete: %+v", got)
	}
	if _, err := s.loadDocByRef("art", "a", "en"); err != nil {
		t.Fatal("a refused delete must leave the document")
	}
	if rec := del(map[string]any{"collection": "art", "key": "a", "lang": "en", "expectedRevision": cur}); rec.Code != http.StatusOK {
		t.Fatalf("delete at the current revision: %d %s", rec.Code, rec.Body.String())
	}
	got = decodeConflict(t, del(map[string]any{"collection": "art", "key": "a", "lang": "en", "expectedRevision": cur}))
	if got.CurrentRevision != "" {
		t.Fatalf("missing document: %+v", got)
	}
	if rec := del(map[string]any{"collection": "art", "key": "a", "lang": "en"}); rec.Code != http.StatusNotFound {
		t.Fatalf("unconditional delete of a missing document keeps its 404: %d", rec.Code)
	}
	addTestDoc(t, s, "art", "b", "en", "x", nil)
	if rec := del(map[string]any{"collection": "art", "key": "b", "lang": "en"}); rec.Code != http.StatusOK {
		t.Fatalf("unconditional delete: %d", rec.Code)
	}
}

// gRPC, MCP and GraphQL deletes take the revision too.
func TestDelete_ConditionalOtherSurfaces(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	ctx := context.Background()
	for _, k := range []string{"g", "m", "q"} {
		addTestDoc(t, s, "art", k, "en", "x", nil)
	}

	g := &GRPCServer{server: s}
	if _, err := g.DeleteDocument(ctx, &proto.DeleteDocumentRequest{Collection: "art", Key: "g", Lang: "en", ExpectedRevision: "stale"}); status.Code(err) != codes.Aborted {
		t.Fatalf("gRPC stale: %v", err)
	}
	if _, err := g.DeleteDocument(ctx, &proto.DeleteDocumentRequest{Collection: "art", Key: "g", Lang: "en",
		ExpectedRevision: storedRevision(t, s, "art", "g")}); err != nil {
		t.Fatalf("gRPC delete: %v", err)
	}

	tools := &MCPToolServer{client: NewDirectClient(s)}
	args := map[string]interface{}{"collection": "art", "key": "m", "lang": "en", "expected_revision": "stale"}
	if _, err := tools.toolDeleteDocument(ctx, args); err == nil || !strings.Contains(err.Error(), "revision conflict") {
		t.Fatalf("MCP stale: %v", err)
	}
	args["expected_revision"] = storedRevision(t, s, "art", "m")
	if _, err := tools.toolDeleteDocument(ctx, args); err != nil {
		t.Fatalf("MCP delete: %v", err)
	}

	adapter := &GraphQLAdapter{server: s, mcp: NewDirectClient(s)}
	stale := "stale"
	if err := adapter.DeleteDocument(ctx, "art", "q", "en", &stale); err == nil {
		t.Fatal("GraphQL stale")
	}
	cur := storedRevision(t, s, "art", "q")
	if err := adapter.DeleteDocument(ctx, "art", "q", "en", &cur); err != nil {
		t.Fatalf("GraphQL delete: %v", err)
	}
	if _, err := s.loadDocByRef("art", "q", "en"); err == nil {
		t.Fatal("deleted")
	}
}

func TestGRPCDeleteError(t *testing.T) {
	for err, want := range map[error]codes.Code{
		errDocNotFound:                        codes.NotFound,
		&RevisionConflictError{Expected: "a"}: codes.Aborted,
		errors.New("disk"):                    codes.Internal,
	} {
		if got := status.Code(grpcDeleteError(err)); got != want {
			t.Errorf("%v: %v, want %v", err, got, want)
		}
	}
	if msg := (&RevisionConflictError{Current: "c"}).Error(); !strings.Contains(msg, "already exists") {
		t.Errorf("ifAbsent conflict message: %s", msg)
	}
}

// #425: every metadata search surface carries the revision a get reports,
// and it moves when the document changes.
func TestSearch_ResultsCarryRevision(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	ctx := context.Background()
	addTestDoc(t, s, "art", "a", "en", "v1", map[string][]string{"tag": {"x"}})
	rev := func() string { return storedRevision(t, s, "art", "a") }
	check := func(when string) {
		t.Helper()
		want := rev()
		rec := doRequest(t, s.handleSearch, map[string]any{"collection": "art"})
		var docs []DocResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &docs); err != nil || len(docs) != 1 || docs[0].Revision != want {
			t.Fatalf("%s: REST %s, want revision %s", when, rec.Body.String(), want)
		}
		get := doRequest(t, s.handleGet, map[string]any{"collection": "art", "key": "a", "lang": "en"})
		if !strings.Contains(get.Body.String(), `"revision":"`+want+`"`) {
			t.Fatalf("%s: get %s", when, get.Body.String())
		}
		pr, err := (&GRPCServer{server: s}).Search(ctx, &proto.SearchRequest{Collection: "art"})
		if err != nil || pr.Documents[0].Revision != want {
			t.Fatalf("%s: gRPC %v %+v", when, err, pr)
		}
		mr, err := NewDirectClient(s).Search(ctx, &MCPSearchRequest{Collection: "art", IncludeContent: true})
		if err != nil || mr.Documents[0].Revision != want {
			t.Fatalf("%s: MCP %v %+v", when, err, mr)
		}
		gr, err := (&GraphQLAdapter{server: s, mcp: NewDirectClient(s)}).SearchDocuments(ctx, gql.SearchInput{Collection: "art"})
		if err != nil || gr.Edges[0].Node.Revision == nil || *gr.Edges[0].Node.Revision != want {
			t.Fatalf("%s: GraphQL %v %+v", when, err, gr)
		}
	}
	check("added")
	before := rev()
	content := "v2"
	if _, err := s.updateDocument("art", "a", "en", DocPatch{Content: &content}); err != nil {
		t.Fatal(err)
	}
	if rev() == before {
		t.Fatal("an update moves the revision")
	}
	check("updated")
}

// A code document's semantic facts are left out of a result, but its
// revision is the whole stored document's, as get reports it.
func TestSearch_RevisionCoversHiddenFacts(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	content := "package svc\n"
	idx := "v1 " + semanticContentHash(content) + " 000000000000 gopls/0.24.0"
	meta := map[string][]string{MetaKeySymIndex: {idx}, MetaKeySymRefs: {"svc/log.go#Log 1,2"}}
	if _, _, err := s.addDocumentIf(factsCollection, "svc/a.go", "en", meta, content, 0, false, writeCondition{}); err != nil {
		t.Fatal(err)
	}
	addTestDoc(t, s, factsCollection, "svc/b.md", "en", "plain", nil)
	page, total, err := s.searchDocuments(factsCollection, nil, "key", true, 0, 10)
	if err != nil || total != 2 {
		t.Fatalf("search: %v %d", err, total)
	}
	for _, d := range page {
		if hasSemanticFacts(d.Meta) {
			t.Fatalf("facts must stay hidden: %v", d.Meta)
		}
		if want := storedRevision(t, s, factsCollection, d.Key); d.Revision != want {
			t.Fatalf("%s: revision %s, get reports %s", d.Key, d.Revision, want)
		}
	}
}

func TestWriteConditionValidate(t *testing.T) {
	if err := (writeCondition{IfAbsent: true, ExpectedRevision: "r"}).validate(); !errors.Is(err, errConflictingConditions) {
		t.Fatalf("combined: %v", err)
	}
	if _, _, err := (&Server{}).addDocumentIf("c", "k", "en", nil, "x", 0, true, writeCondition{IfAbsent: true, ExpectedRevision: "r"}); !errors.Is(err, errConflictingConditions) {
		t.Fatalf("refused before any transaction: %v", err)
	}
	if err := (writeCondition{}).validate(); err != nil {
		t.Fatal(err)
	}
}

// search_documents declares the revision in its output schema, and a tool
// declared without an input schema gets an empty object one.
func TestApplyOutputSchemas_SearchRevision(t *testing.T) {
	tools := applyOutputSchemas([]MCPTool{{Name: "search_documents"}})
	if tools[0].InputSchema == nil || tools[0].OutputSchema == nil {
		t.Fatalf("schemas: %+v", tools[0])
	}
	items := mcpToolOutputSchemas["search_documents"]["properties"].(map[string]interface{})["documents"].(map[string]interface{})["items"]
	if _, ok := items.(map[string]interface{})["properties"].(map[string]interface{})["revision"]; !ok {
		t.Fatal("search_documents output schema lacks revision")
	}
}
