package main

import (
	"context"
	"net/http"
	"strings"
	"testing"

	pb "mddb/proto"
)

// #325: chunk size and mode were server-wide. A collection now sets its own,
// every transport carries it, and every path that derives chunks uses it.

func TestCollectionChunkSizeDecidesHowManyChunksAreEmbedded(t *testing.T) {
	_, s, cleanup := newTestGRPCServerFull(t)
	defer cleanup()
	t.Setenv("MDDB_EMBEDDING_CHUNK_SIZE", "1500")
	s.Embedding = &mockEmbedding{dims: 3, model: "test-model"}
	s.startEmbeddingWorker(s.Embedding)
	defer s.EmbeddingWorker.Stop()

	if err := s.CollectionManager.Set("small", &CollectionConfig{
		Type: "default", Embedding: &EmbeddingChunkingDef{ChunkSize: 200, ChunkMode: "prose"},
	}); err != nil {
		t.Fatal(err)
	}
	var paras []string
	for i := 0; i < 12; i++ {
		paras = append(paras, strings.Repeat("words about chunking ", 8))
	}
	body := strings.Join(paras, "\n\n") // ~2000 characters
	for _, coll := range []string{"small", "default"} {
		doc := addTestDoc(t, s, coll, "long", "en", body, nil)
		waitForEmbedding(t, s, coll, doc.ID)
	}

	small, def := vectorStatsFor(t, s, "small"), vectorStatsFor(t, s, "default")
	if small["chunkSize"].(float64) != 200 || def["chunkSize"].(float64) != 1500 {
		t.Errorf("effective chunk size: small %v, default %v", small["chunkSize"], def["chunkSize"])
	}
	if small["chunkMode"] != "prose" || def["chunkMode"] != nil {
		t.Errorf("chunk mode: small %v, default %v", small["chunkMode"], def["chunkMode"])
	}
	if small["total_chunks"].(float64) <= def["total_chunks"].(float64) {
		t.Errorf("a 200-character collection made %v chunks, the 1500 default %v", small["total_chunks"], def["total_chunks"])
	}
}

func TestChunkSettingsResolveCollectionThenServer(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	t.Setenv("MDDB_EMBEDDING_CHUNK_SIZE", "900")
	codeDoc := addTestDoc(t, s, "plain", "main.c", "en", "int main(void) { return 0; }", nil)

	if got := s.chunkSizeFor("plain"); got != 900 {
		t.Errorf("no override: size %d, want the server's 900", got)
	}
	if got := s.chunkModeFor("plain", &codeDoc); got != ChunkModeFor(&codeDoc) {
		t.Errorf("no override: mode %q, want the document's own %q", got, ChunkModeFor(&codeDoc))
	}
	_ = s.CollectionManager.Set("plain", &CollectionConfig{Embedding: &EmbeddingChunkingDef{ChunkMode: "prose"}})
	if got := s.chunkModeFor("plain", &codeDoc); got != ChunkModeProse {
		t.Errorf("collection mode prose: got %q", got)
	}
	if got := s.chunkSizeFor("plain"); got != 900 {
		t.Errorf("mode-only override changed the size: %d", got)
	}
	if (&Server{}).chunkSizeFor("x") != 900 {
		t.Error("a server without a collection manager should use the server size")
	}
}

func TestChunkingIsValidatedOnEveryTransport(t *testing.T) {
	for _, bad := range []*EmbeddingChunkingDef{{ChunkSize: 50}, {ChunkSize: -1}, {ChunkSize: 200000}, {ChunkMode: "lines"}} {
		if validateChunking(bad) == nil {
			t.Errorf("%+v accepted", bad)
		}
	}
	for _, good := range []*EmbeddingChunkingDef{nil, {}, {ChunkSize: 100}, {ChunkSize: 4000, ChunkMode: "code"}} {
		if err := validateChunking(good); err != nil {
			t.Errorf("%+v rejected: %v", good, err)
		}
	}

	gs, s, cleanup := newTestGRPCServerFull(t)
	defer cleanup()

	rec := doRequest(t, s.handleCollectionConfigSet, map[string]any{"collection": "r", "embedding": map[string]any{"chunkSize": 10}})
	if rec.Code != http.StatusBadRequest {
		t.Errorf("REST accepted chunkSize 10: %d", rec.Code)
	}
	rec = doRequest(t, s.handleCollectionConfigSet, map[string]any{"collection": "r", "embedding": map[string]any{"chunkSize": 400, "chunkMode": "code"}})
	if rec.Code != http.StatusOK || s.chunkSizeFor("r") != 400 {
		t.Errorf("REST set: %d %s, size %d", rec.Code, rec.Body.String(), s.chunkSizeFor("r"))
	}

	if _, err := gs.SetCollectionConfig(context.Background(), &pb.SetCollectionConfigRequest{
		Collection: "g", Embedding: &pb.EmbeddingChunkingProto{ChunkMode: "lines"},
	}); err == nil {
		t.Error("gRPC accepted chunk mode lines")
	}
	if _, err := gs.SetCollectionConfig(context.Background(), &pb.SetCollectionConfigRequest{
		Collection: "g", Embedding: &pb.EmbeddingChunkingProto{ChunkSize: 300},
	}); err != nil {
		t.Fatal(err)
	}
	got, err := gs.GetCollectionConfig(context.Background(), &pb.GetCollectionConfigRequest{Collection: "g"})
	if err != nil || got.GetConfig().GetEmbedding().GetChunkSize() != 300 {
		t.Errorf("gRPC round trip: %v %v", got, err)
	}
	if chunkingToProto(nil) != nil {
		t.Error("nil chunking should stay nil")
	}

	ts := &MCPToolServer{client: NewDirectClient(s), globalMode: ModeRW}
	if _, err := ts.toolSetCollectionConfig(context.Background(), map[string]interface{}{
		"collection": "m", "embedding": map[string]interface{}{"chunk_size": float64(5)},
	}); err == nil {
		t.Error("MCP accepted chunk_size 5")
	}
	if _, err := ts.toolSetCollectionConfig(context.Background(), map[string]interface{}{
		"collection": "m", "embedding": map[string]interface{}{"chunk_size": float64(700), "chunk_mode": "prose"},
	}); err != nil {
		t.Fatal(err)
	}
	if s.chunkSizeFor("m") != 700 || s.collectionChunking("m").ChunkMode != "prose" {
		t.Errorf("MCP set: %+v", s.collectionChunking("m"))
	}
}

// The passage a search returns is re-derived from the chunk index, so it
// must be cut at the size the document was embedded with.
func TestPassageUsesTheCollectionChunkSize(t *testing.T) {
	var paras []string
	for i := 0; i < 10; i++ {
		paras = append(paras, strings.Repeat("p", 150))
	}
	body := strings.Join(paras, "\n\n")
	text, _, _ := chunkPassageWithLinesSize(body, 3, 0, ChunkModeProse, 200)
	want := chunkTextsMode(body, 200, ChunkModeProse)[3]
	if text != want {
		t.Errorf("passage 3 at size 200 = %q, want %q", text, want)
	}
}
