package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"mddb/internal/embedding"
	json "mddb/internal/jsonx"
	proto "mddb/proto"
)

// docGateEmbedding embeds queries at once and holds document embeddings until
// release is closed, so a test can observe a document waiting for its vector.
type docGateEmbedding struct {
	mockEmbedding
	release chan struct{}
	once    sync.Once
}

func newDocGateEmbedding() *docGateEmbedding {
	return &docGateEmbedding{mockEmbedding: mockEmbedding{dims: 3, model: "gated-model"}, release: make(chan struct{})}
}

func (g *docGateEmbedding) Embed(ctx context.Context, text string, role embedding.Role) ([]float32, error) {
	if role == embedding.RoleDocument {
		select {
		case <-g.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return g.mockEmbedding.Embed(ctx, text, role)
}

func (g *docGateEmbedding) open() { g.once.Do(func() { close(g.release) }) }

// indexingServer is a test server with a running embedding worker behind a gate.
func indexingServer(t *testing.T) (*Server, *docGateEmbedding, func()) {
	t.Helper()
	s, cleanup := newHandlerTestServer(t)
	s.VectorIndex.SetReady()
	g := newDocGateEmbedding()
	s.Embedding = g
	s.startEmbeddingWorker(g)
	return s, g, func() {
		g.open()
		s.EmbeddingWorker.Stop()
		cleanup()
	}
}

func addViaHTTP(t *testing.T, s *Server, key, content string) AddResponse {
	t.Helper()
	rec := doRequest(t, s.handleAdd, map[string]any{
		"collection": "notes", "key": key, "lang": "en", "contentMd": content,
	})
	if rec.Code != 200 {
		t.Fatalf("add: %d %s", rec.Code, rec.Body.String())
	}
	var resp AddResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	return resp
}

func TestIndexing_AddThenSearchWithWaitForKeys(t *testing.T) {
	s, g, cleanup := indexingServer(t)
	defer cleanup()

	added := addViaHTTP(t, s, "Fresh", "a brand new zebra note")
	if added.Indexing == nil || !added.Indexing.EmbeddingQueued || added.Key != "Fresh" {
		t.Fatalf("add response: %+v", added)
	}

	st := s.IndexingStatus("notes")
	if st.PendingEmbeddings < 1 || st.Indexed || st.OldestPendingAt == nil || st.Provider != "gated-model" || st.Progress >= 1 {
		t.Fatalf("status while pending: %+v", st)
	}

	// Without waiting, the vector search cannot see the document yet.
	plain := vectorSearchNotes(t, s, VectorSearchRequest{Collection: "notes", Query: "zebra", TopK: 5})
	if len(plain.Results) != 0 {
		t.Fatalf("not embedded yet, got %+v", plain.Results)
	}

	// Waiting for the key returns it once the embedding lands.
	go func() { time.Sleep(30 * time.Millisecond); g.open() }()
	resp := vectorSearchNotes(t, s, VectorSearchRequest{Collection: "notes", Query: "zebra", TopK: 5,
		IndexWait: IndexWait{WaitForIndex: 5000, WaitForKeys: []string{"fresh"}}})
	if resp.Indexing == nil || resp.Indexing.TimedOut || len(resp.Results) != 1 {
		t.Fatalf("after waiting: %+v", resp)
	}
	st = s.IndexingStatus("notes")
	if !st.Indexed || st.Progress != 1 || st.CompletedEmbeddings < 1 || st.OldestPendingAt != nil {
		t.Fatalf("status after: %+v", st)
	}
}

func vectorSearchNotes(t *testing.T, s *Server, req VectorSearchRequest) VectorSearchResponseHTTP {
	t.Helper()
	rec := doRequest(t, s.handleVectorSearch, req)
	if rec.Code != 200 {
		t.Fatalf("vector search: %d %s", rec.Code, rec.Body.String())
	}
	var resp VectorSearchResponseHTTP
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	return resp
}

func TestIndexing_TimeoutIsNotAnError(t *testing.T) {
	s, _, cleanup := indexingServer(t)
	defer cleanup()
	addViaHTTP(t, s, "slow", "a slow giraffe note")

	resp := vectorSearchNotes(t, s, VectorSearchRequest{Collection: "notes", Query: "giraffe", TopK: 5,
		IndexWait: IndexWait{WaitForIndex: 40}})
	if resp.Indexing == nil || !resp.Indexing.TimedOut || resp.Indexing.WaitedMs < 30 {
		t.Fatalf("timeout: %+v", resp.Indexing)
	}
	// Waiting for an unrelated key does not wait at all.
	resp = vectorSearchNotes(t, s, VectorSearchRequest{Collection: "notes", Query: "giraffe", TopK: 5,
		IndexWait: IndexWait{WaitForIndex: 5000, WaitForKeys: []string{"other"}}})
	if resp.Indexing == nil || resp.Indexing.TimedOut {
		t.Fatalf("unrelated key: %+v", resp.Indexing)
	}
}

func TestIndexing_ReadLevelIndexedPlusPending(t *testing.T) {
	s, _, cleanup := indexingServer(t)
	defer cleanup()
	addViaHTTP(t, s, "p1", "pending pelican story")
	addViaHTTP(t, s, "p2", "nothing to see")

	resp := vectorSearchNotes(t, s, VectorSearchRequest{Collection: "notes", Query: "pelican", TopK: 5,
		IncludeContent: true, IndexWait: IndexWait{ReadLevel: ReadLevelIndexedPlusPending}})
	if len(resp.Results) != 1 || !resp.Results[0].PendingEmbedding || resp.Results[0].Document.Key != "p1" ||
		resp.Results[0].Document.ContentMD == "" {
		t.Fatalf("pending match: %+v", resp.Results)
	}

	// MCP semantic_search: same through the tool and its arguments.
	tools := &MCPToolServer{client: NewDirectClient(s)}
	out, err := tools.toolSemanticSearch(context.Background(), map[string]interface{}{
		"collection": "notes", "query": "pelican", "read_level": "indexedPlusPending",
		"wait_for_index": 20, "wait_for_keys": []interface{}{"p1"},
	})
	if err != nil || !strings.Contains(out, `"pendingEmbedding": true`) || !strings.Contains(out, `"timedOut": true`) {
		t.Fatalf("MCP: %v %s", err, out)
	}

	// Hybrid already finds pending documents by text; now it says so.
	rec := doRequest(t, s.handleHybridSearch, HybridSearchRequest{Collection: "notes", Query: "pelican", TopK: 5,
		IndexWait: IndexWait{WaitForIndex: 10}})
	var hyb HybridSearchResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &hyb); err != nil || len(hyb.Results) == 0 || !hyb.Results[0].PendingEmbedding || hyb.Indexing == nil {
		t.Fatalf("hybrid: %d %s", rec.Code, rec.Body.String())
	}
	out, err = tools.toolHybridSearch(context.Background(), map[string]interface{}{"collection": "notes", "query": "pelican"})
	if err != nil || !strings.Contains(out, `"pendingEmbedding": true`) {
		t.Fatalf("MCP hybrid: %v %s", err, out)
	}

	// Cross-collection search waits on every target.
	rec = doRequest(t, s.handleCrossSearch, CrossSearchRequest{Query: "pelican", TargetCollections: []string{"notes"},
		IndexWait: IndexWait{WaitForIndex: 10}})
	if !strings.Contains(rec.Body.String(), `"timedOut":true`) {
		t.Fatalf("cross: %d %s", rec.Code, rec.Body.String())
	}
	out, err = tools.toolCrossSearch(context.Background(), map[string]interface{}{
		"query": "pelican", "target_collections": []interface{}{"notes"}, "wait_for_index": 10,
	})
	if err != nil || !strings.Contains(out, `"timedOut": true`) {
		t.Fatalf("MCP cross: %v %s", err, out)
	}
}

func TestIndexing_Validation(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	s.VectorIndex.SetReady()
	bad := []IndexWait{{ReadLevel: "eventually"}, {WaitForIndex: -1}}
	for _, w := range bad {
		if code := doRequest(t, s.handleVectorSearch, VectorSearchRequest{Collection: "c", QueryVector: []float32{1, 0, 0}, IndexWait: w}).Code; code != 422 {
			t.Errorf("vector %+v: %d", w, code)
		}
		if code := doRequest(t, s.handleHybridSearch, HybridSearchRequest{Collection: "c", Query: "q", IndexWait: w}).Code; code != 422 {
			t.Errorf("hybrid %+v: %d", w, code)
		}
		if code := doRequest(t, s.handleCrossSearch, CrossSearchRequest{Query: "q", TargetCollections: []string{"c"}, IndexWait: w}).Code; code != 422 {
			t.Errorf("cross %+v: %d", w, code)
		}
		dc := NewDirectClient(s)
		if _, err := dc.VectorSearch(context.Background(), &MCPVectorSearchRequest{Collection: "c", Query: "q", IndexWait: w}); err == nil {
			t.Errorf("MCP vector %+v accepted", w)
		}
		if _, err := dc.HybridSearch(context.Background(), &MCPHybridSearchRequest{Collection: "c", Query: "q", IndexWait: w}); err == nil {
			t.Errorf("MCP hybrid %+v accepted", w)
		}
		if _, err := dc.CrossSearch(context.Background(), &MCPCrossSearchRequest{Query: "q", TargetCollections: []string{"c"}, IndexWait: w}); err == nil {
			t.Errorf("MCP cross %+v accepted", w)
		}
	}
}

func TestIndexing_StatusSurfaces(t *testing.T) {
	s, g, cleanup := indexingServer(t)
	defer cleanup()
	addViaHTTP(t, s, "x", "status body")

	// REST
	rec := httptest.NewRecorder()
	s.handleIndexingStatus(rec, httptest.NewRequest(http.MethodGet, "/v1/indexing-status?collection=notes", nil))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"pendingEmbeddings":1`) {
		t.Fatalf("REST: %d %s", rec.Code, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	s.handleIndexingStatus(rec, httptest.NewRequest(http.MethodPost, "/v1/indexing-status", nil))
	if rec.Code != 405 {
		t.Fatalf("POST: %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	s.handleStats(rec, httptest.NewRequest(http.MethodGet, "/v1/stats", nil))
	if !strings.Contains(rec.Body.String(), `"indexing":{`) {
		t.Fatalf("/v1/stats: %s", rec.Body.String())
	}

	// MCP
	tools := &MCPToolServer{client: NewDirectClient(s)}
	out, err := tools.toolGetIndexingStatus(context.Background(), map[string]interface{}{"collection": "notes"})
	if err != nil || !strings.Contains(out, `"indexed": false`) {
		t.Fatalf("MCP: %v %s", err, out)
	}
	out, err = tools.toolAddDocument(context.Background(), map[string]interface{}{
		"collection": "notes", "key": "y", "lang": "en", "content_md": "another",
	})
	if err != nil || !strings.Contains(out, `"embeddingQueued": true`) {
		t.Fatalf("MCP add: %v %s", err, out)
	}

	// gRPC
	gs := &GRPCServer{server: s}
	gr, err := gs.GetIndexingStatus(context.Background(), &proto.IndexingStatusRequest{Collection: "notes"})
	if err != nil || gr.PendingEmbeddings < 1 || gr.OldestPendingAt == 0 || gr.Indexed {
		t.Fatalf("gRPC: %v %+v", err, gr)
	}

	// GraphQL
	adapter := &GraphQLAdapter{server: s, mcp: NewDirectClient(s)}
	coll := "notes"
	gq, err := adapter.IndexingStatus(context.Background(), &coll)
	if err != nil || gq.PendingEmbeddings < 1 || gq.OldestPendingAt == nil || gq.Collection == nil || gq.Provider == nil {
		t.Fatalf("GraphQL: %v %+v", err, gq)
	}

	// Drained: collection.indexed is announced, status says indexed.
	g.open()
	if !s.EmbeddingWorker.Pending().Wait(context.Background(), "notes", nil) {
		t.Fatal("drain")
	}
	if all, _ := adapter.IndexingStatus(context.Background(), nil); !all.Indexed || all.Collection != nil {
		t.Fatalf("server-wide after drain: %+v", all)
	}
	gr, _ = gs.GetIndexingStatus(context.Background(), &proto.IndexingStatusRequest{})
	if !gr.Indexed || gr.OldestPendingAt != 0 {
		t.Fatalf("gRPC after drain: %+v", gr)
	}
}

func TestIndexing_NoWorkerAndHelpers(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	st := s.IndexingStatus("x")
	if !st.Indexed || st.Progress != 1 || st.Provider != "" {
		t.Fatalf("no worker: %+v", st)
	}
	if s.writeIndexing(addTestDoc(t, s, "x", "k", "en", "body", nil)) != nil {
		t.Fatal("no worker: nothing queued")
	}
	if s.waitForIndex(context.Background(), "x", IndexWait{}) != nil {
		t.Fatal("no wait asked")
	}
	if r := s.waitForIndex(context.Background(), "x", IndexWait{WaitForIndex: 10}); r == nil || r.TimedOut {
		t.Fatalf("no worker: nothing to wait for: %+v", r)
	}
	if s.pendingMatches("x", "", nil, nil, 5, nil) != nil || s.pendingMatches("x", "q", nil, nil, 0, nil) != nil {
		t.Fatal("pendingMatches without query or limit")
	}
	s.announceIndexed("x") // no hub: no-op
	s.SSEHub = NewSSEHub(true, 0, 0)
	s.announceIndexed("x") // hub without clients
	if clampUint(1<<63+5) <= 0 || clampUint(7) != 7 {
		t.Fatal("clampUint")
	}
	if keyMatcher("c", nil) != nil || !keyMatcher("c", []string{"Doc"})("c|doc|en") || keyMatcher("c", []string{"doc"})("c|docs|en") {
		t.Fatal("keyMatcher")
	}
}
