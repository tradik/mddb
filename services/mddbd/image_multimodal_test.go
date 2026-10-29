package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"image"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	gql "mddb/graphql"
	"mddb/internal/binlog"
	"mddb/internal/embedding"
	vec "mddb/internal/vector"
	proto "mddb/proto"
)

// RAG-010 with a provider that embeds images. colourProvider is that
// provider in-process: text and pictures share a space of three colours, a
// text's vector counting the colour words in it and an image's being its
// average colour. With it, a text query finds a picture that has no alt
// text — deterministically, in CI. TestImages_MultimodalProviderE2E does the
// same against a real provider, when one is configured.

type colourProvider struct {
	textCalls, imageCalls atomic.Int64
	failImages            atomic.Bool
}

var errColourProvider = errors.New("colour provider refused")

// colourDims pads the three colours to a size every index accepts.
const colourDims = 16

func colourVector(r, g, b float64) []float32 {
	n := math.Sqrt(r*r+g*g+b*b) + 1e-9
	v := make([]float32, colourDims)
	v[0], v[1], v[2] = float32(r/n), float32(g/n), float32(b/n)
	return v
}

func (p *colourProvider) Embed(_ context.Context, text string, _ embedding.Role) ([]float32, error) {
	p.textCalls.Add(1)
	t := strings.ToLower(text)
	return colourVector(0.01+float64(strings.Count(t, "red")), 0.01+float64(strings.Count(t, "green")), 0.01+float64(strings.Count(t, "blue"))), nil
}

func (p *colourProvider) EmbedBatch(ctx context.Context, texts []string, role embedding.Role) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i, t := range texts {
		out[i], _ = p.Embed(ctx, t, role)
	}
	return out, nil
}

func (p *colourProvider) Model() string   { return "colour-text" }
func (p *colourProvider) Dimensions() int { return colourDims }
func (p *colourProvider) ImageModel() string {
	return "colour-image"
}

func (p *colourProvider) EmbedImage(_ context.Context, img embedding.Image, _ embedding.Role) ([]float32, error) {
	p.imageCalls.Add(1)
	if p.failImages.Load() {
		return nil, errColourProvider
	}
	m, _, err := image.Decode(bytes.NewReader(img.Data))
	if err != nil {
		return nil, err
	}
	var r, g, b float64
	bounds := m.Bounds()
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			cr, cg, cb, _ := m.At(x, y).RGBA()
			r, g, b = r+float64(cr), g+float64(cg), b+float64(cb)
		}
	}
	return colourVector(r, g, b), nil
}

func (p *colourProvider) EmbedImageText(ctx context.Context, text string, role embedding.Role) ([]float32, error) {
	return p.Embed(ctx, text, role)
}

// multimodalServer is imageServer's collection embedded by colourProvider.
func multimodalServer(t *testing.T) (*Server, *colourProvider, func()) {
	t.Helper()
	t.Setenv("MDDB_SEARCH_STATS", "false")
	s, cleanup := newHandlerTestServer(t)
	s.VectorIndex.SetReady()
	for _, sr := range s.VectorSearchers {
		sr.SetReady()
	}
	p := &colourProvider{}
	s.Embedding = p
	s.startEmbeddingWorker(p)
	files := map[string][]byte{"manual.md": readImageFixture(t, "manual.md"),
		"seal.png": readImageFixture(t, "img/seal.png"), "wiring.jpg": readImageFixture(t, "img/wiring.jpg")}
	rec := uploadFiles(t, s, files, []string{"manual.md", "seal.png", "wiring.jpg"},
		map[string]string{"collection": "mm", "lang": "en", "extractImages": "true"})
	if rec.Code != http.StatusOK {
		t.Fatalf("upload: %d %s", rec.Code, rec.Body.String())
	}
	waitIndexed(t, s, "mm")
	if n := s.imageIndex().size("mm"); n != 3 {
		t.Fatalf("%d image vectors, want 3", n)
	}
	return s, p, func() { s.EmbeddingWorker.Stop(); cleanup() }
}

// firstImageHit is the key of the first image hit of a REST or MCP response.
func firstImageHit(t *testing.T, m map[string]any) string {
	t.Helper()
	hits, _ := m["imageHits"].([]any)
	if len(hits) == 0 {
		t.Fatalf("no image hits: %v", m)
	}
	return hits[0].(map[string]any)["key"].(string)
}

func TestImages_TextQueryFindsAnImageWithoutAltText(t *testing.T) {
	s, _, cleanup := multimodalServer(t)
	defer cleanup()
	ctx := context.Background()
	// wiring.jpg has no alt text and nothing blue in its caption: only its
	// picture is blue.
	const want = "manual/images/wiring.jpg"
	rec := doRequest(t, s.handleVectorSearch, map[string]any{"collection": "mm", "query": "blue", "includeImages": true, "explain": true})
	if got := firstImageHit(t, body(t, rec.Body.Bytes())); got != want {
		t.Fatalf("rest vector: %s", got)
	}
	if !strings.Contains(rec.Body.String(), "includeImages: 3 image vectors searched with the query embedded by colour-image; 3 matched") {
		t.Fatalf("plan: %s", rec.Body.String())
	}
	rec = doRequest(t, s.handleVectorSearch, map[string]any{"collection": "mm", "query": "blue", "includeImages": true, "format": "passages"})
	if got := firstImageHit(t, body(t, rec.Body.Bytes())); got != want {
		t.Fatalf("rest vector passages: %s", got)
	}
	rec = doRequest(t, s.handleHybridSearch, map[string]any{"collection": "mm", "query": "blue", "includeImages": true})
	if got := firstImageHit(t, body(t, rec.Body.Bytes())); got != want {
		t.Fatalf("rest hybrid: %s", got)
	}
	tools := &MCPToolServer{client: NewDirectClient(s), globalMode: ModeRW}
	for _, tool := range []string{"semantic_search", "hybrid_search"} {
		out, err := tools.mcpCallTool(ctx, tool, map[string]interface{}{"collection": "mm", "query": "blue", "include_images": true})
		if err != nil {
			t.Fatal(err)
		}
		if got := firstImageHit(t, covBUnmarshal(t, out)); got != want {
			t.Fatalf("mcp %s: %s", tool, got)
		}
	}
	g := &GRPCServer{server: s}
	gv, err := g.VectorSearch(ctx, &proto.VectorSearchRequest{Collection: "mm", Query: "blue", IncludeImages: true})
	if err != nil || len(gv.ImageHits) == 0 || gv.ImageHits[0].Key != want || gv.ImageHits[0].Score <= 0 {
		t.Fatalf("grpc vector: %v %v", err, gv)
	}
	gp, err := g.VectorSearch(ctx, &proto.VectorSearchRequest{Collection: "mm", Query: "blue", IncludeImages: true, Format: "passages"})
	if err != nil || len(gp.ImageHits) == 0 || gp.ImageHits[0].Key != want {
		t.Fatalf("grpc vector passages: %v %v", err, gp)
	}
	gh, err := g.HybridSearch(ctx, &proto.HybridSearchRequest{Collection: "mm", Query: "blue", IncludeImages: true})
	if err != nil || len(gh.ImageHits) == 0 || gh.ImageHits[0].Key != want {
		t.Fatalf("grpc hybrid: %v %v", err, gh)
	}
	ghp, err := g.HybridSearch(ctx, &proto.HybridSearchRequest{Collection: "mm", Query: "blue", IncludeImages: true, Format: "passages"})
	if err != nil || len(ghp.ImageHits) == 0 {
		t.Fatalf("grpc hybrid passages: %v %v", err, ghp)
	}
	a := &GraphQLAdapter{server: s, mcp: NewDirectClient(s)}
	q, yes, passages := "blue", true, "passages"
	qv, err := a.VectorSearch(ctx, gql.VectorSearchInput{Collection: "mm", Query: &q, IncludeImages: &yes})
	if err != nil || len(qv.ImageHits) == 0 || qv.ImageHits[0].Key != want || qv.ImageHits[0].Score == nil {
		t.Fatalf("graphql vector: %v %+v", err, qv)
	}
	qp, err := a.VectorSearch(ctx, gql.VectorSearchInput{Collection: "mm", Query: &q, IncludeImages: &yes, Format: &passages})
	if err != nil || len(qp.ImageHits) == 0 {
		t.Fatalf("graphql vector passages: %v", err)
	}

	// The status names the model and counts the vectors.
	st := s.IndexingStatus("mm").Images
	if st == nil || st.Documents != 3 || st.Embedded != 3 || st.Model != "colour-image" || st.Note != "" {
		t.Fatalf("status: %+v", st)
	}
	if all := s.IndexingStatus("").Images; all == nil || all.Embedded != 3 {
		t.Fatalf("server status: %+v", all)
	}
}

func TestImages_QueryImage(t *testing.T) {
	s, _, cleanup := multimodalServer(t)
	defer cleanup()
	ctx := context.Background()
	blue := solidPNG(20, 20, fixtureBlue)
	b64 := base64.StdEncoding.EncodeToString(blue)

	rec := doRequest(t, s.handleVectorSearch, map[string]any{"collection": "mm", "queryImage": b64, "topK": 2})
	m := body(t, rec.Body.Bytes())
	res := m["results"].([]any)
	if rec.Code != http.StatusOK || len(res) != 2 || res[0].(map[string]any)["document"].(map[string]any)["key"] != "manual/images/wiring.jpg" ||
		m["algorithm"] != "image" || m["model"] != "colour-image" || m["dimensions"] != float64(colourDims) {
		t.Fatalf("rest: %d %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), `"contentMd":"!`) {
		t.Fatal("content without includeContent")
	}
	rec = doRequest(t, s.handleVectorSearch, map[string]any{"collection": "mm", "queryImage": "data:image/png;base64," + b64, "format": "passages"})
	if p := body(t, rec.Body.Bytes())["passages"].([]any); len(p) != 3 || p[0].(map[string]any)["source"] != "image" {
		t.Fatalf("passages: %s", rec.Body.String())
	}

	// An http(s) URL goes through the SSRF guard: refused on loopback, read
	// when the operator allows private addresses.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(blue) }))
	defer srv.Close()
	t.Setenv("MDDB_OUTBOUND_ALLOW_PRIVATE", "false")
	t.Setenv("MDDB_OUTBOUND_ALLOWLIST", "")
	rec = doRequest(t, s.handleVectorSearch, map[string]any{"collection": "mm", "queryImage": srv.URL + "/blue.png"})
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "not allowed") {
		t.Fatalf("ssrf: %d %s", rec.Code, rec.Body.String())
	}
	t.Setenv("MDDB_OUTBOUND_ALLOW_PRIVATE", "true")
	rec = doRequest(t, s.handleVectorSearch, map[string]any{"collection": "mm", "queryImage": srv.URL + "/blue.png", "includeContent": true})
	if !strings.Contains(rec.Body.String(), `"key":"manual/images/wiring.jpg"`) || !strings.Contains(rec.Body.String(), `"contentMd":"![](wiring.jpg)`) {
		t.Fatalf("url: %s", rec.Body.String())
	}

	// Bytes that are not an image, and base64 that is not base64: 400.
	for _, bad := range []string{base64.StdEncoding.EncodeToString([]byte("plain text")), "!!!"} {
		rec = doRequest(t, s.handleVectorSearch, map[string]any{"collection": "mm", "queryImage": bad})
		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "invalid queryImage") {
			t.Fatalf("bad image: %d %s", rec.Code, rec.Body.String())
		}
	}

	// MCP, gRPC and GraphQL run the same search.
	out, err := (&MCPToolServer{client: NewDirectClient(s), globalMode: ModeRW}).mcpCallTool(ctx, "semantic_search",
		map[string]interface{}{"collection": "mm", "query_image": b64})
	if err != nil || !strings.Contains(out, `"algorithm": "image"`) || !strings.Contains(out, "manual/images/wiring.jpg") {
		t.Fatalf("mcp: %v %s", err, out)
	}
	mp, err := NewDirectClient(s).VectorSearch(ctx, &MCPVectorSearchRequest{Collection: "mm", QueryImage: b64, PassageOptions: PassageOptions{Format: FormatPassages}})
	if err != nil || mp.Passages == nil || mp.Passages.Total != 3 {
		t.Fatalf("mcp passages: %v %+v", err, mp)
	}
	gv, err := (&GRPCServer{server: s}).VectorSearch(ctx, &proto.VectorSearchRequest{Collection: "mm", QueryImage: b64})
	if err != nil || len(gv.Results) == 0 || gv.Results[0].Document.Key != "manual/images/wiring.jpg" || gv.Algorithm != "image" {
		t.Fatalf("grpc: %v %v", err, gv)
	}
	qv, err := (&GraphQLAdapter{server: s, mcp: NewDirectClient(s)}).VectorSearch(ctx, gql.VectorSearchInput{Collection: "mm", QueryImage: &b64})
	if err != nil || len(qv.Results) == 0 || qv.Results[0].Document.Key != "manual/images/wiring.jpg" {
		t.Fatalf("graphql: %v", err)
	}
	if _, err := NewDirectClient(s).VectorSearch(ctx, &MCPVectorSearchRequest{Collection: "mm", QueryImage: "!!!"}); !errors.Is(err, errQueryImage) {
		t.Fatalf("mcp bad image: %v", err)
	}
}

func TestImages_QueryImageNeedsAMultimodalProvider(t *testing.T) {
	s, cleanup := imageServer(t) // lexical: no images
	defer cleanup()
	b64 := base64.StdEncoding.EncodeToString(solidPNG(20, 20, fixtureBlue))
	rec := doRequest(t, s.handleVectorSearch, map[string]any{"collection": "docs", "queryImage": b64})
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "queryImage needs an embedding provider that embeds images") {
		t.Fatalf("rest: %d %s", rec.Code, rec.Body.String())
	}
	_, err := (&GRPCServer{server: s}).VectorSearch(context.Background(), &proto.VectorSearchRequest{Collection: "docs", QueryImage: b64})
	if err == nil || !strings.Contains(err.Error(), "FailedPrecondition") {
		t.Fatalf("grpc: %v", err)
	}
	// includeImages with image vectors but no image provider says so.
	_ = s.imageIndex().add("docs", "docs|manual/images/seal.png|en", colourVector(1, 0, 0), false)
	rec = doRequest(t, s.handleVectorSearch, map[string]any{"collection": "docs", "query": "pump", "includeImages": true, "explain": true})
	if !strings.Contains(rec.Body.String(), "includeImages: 1 image vectors not searched: the embedding provider does not embed images") {
		t.Fatalf("plan: %s", rec.Body.String())
	}
}

func TestImages_WorkerReusesBudgetsAndCleansUp(t *testing.T) {
	s, p, cleanup := multimodalServer(t)
	defer cleanup()
	ctx := context.Background()
	id := genID("mm", "manual/images/wiring.jpg", "en")

	// An unchanged image is not embedded again: a re-upload calls nothing.
	calls := p.imageCalls.Load()
	r := imageRefresh{provider: p, store: s.VectorStore, index: serverIndexes{s}}
	if out, err := r.run(ctx, "mm", id, false); out != jobDone || err != nil || p.imageCalls.Load() != calls {
		t.Fatalf("unchanged: %v %v calls %d", out, err, p.imageCalls.Load()-calls)
	}
	// An index that lost it gets it back from the store, uncalled.
	s.imageIndex().remove("mm", id)
	if out, err := r.run(ctx, "mm", id, false); out != jobDone || err != nil || !s.imageIndex().has("mm", id) || p.imageCalls.Load() != calls {
		t.Fatalf("reindexed from store: %v %v", out, err)
	}
	// A spent budget parks the job before any call.
	spent := newEmbeddingBudget(func(string) *CollectionQuota { return &CollectionQuota{EmbeddingCallsPerDay: 1} },
		func(qe *QuotaError) error { return qe })
	spent.spendTokens("mm", 1)
	r.budget = spent
	if out, _ := r.run(ctx, "mm", id, true); out != jobParked || p.imageCalls.Load() != calls {
		t.Fatalf("budget: %v", out)
	}
	// A failing provider fails the job; retries spend the budget each time.
	r.budget, r.attempts = nil, 1
	p.failImages.Store(true)
	if out, err := r.run(ctx, "mm", id, true); out != jobFailed || !errors.Is(err, errColourProvider) {
		t.Fatalf("failure: %v %v", out, err)
	}
	p.failImages.Store(false)
	// A document deleted while it was embedded gets no vector.
	r.exists = func() bool { return false }
	if out, err := r.run(ctx, "mm", id, true); out != jobDone || err != nil {
		t.Fatalf("deleted: %v %v", out, err)
	}
	r.exists = nil
	// Bytes that are no longer an image fail; a document with no bytes is done.
	if err := s.putImageBytes("mm", id, []byte("garbage")); err != nil {
		t.Fatal(err)
	}
	if out, err := r.run(ctx, "mm", id, false); out != jobFailed || err == nil {
		t.Fatalf("garbage: %v %v", out, err)
	}
	if out, err := r.run(ctx, "mm", "mm|nothing|en", false); out != jobDone || err != nil {
		t.Fatalf("no bytes: %v %v", out, err)
	}
	if err := s.putImageBytes("mm", id, readImageFixture(t, "img/wiring.jpg")); err != nil {
		t.Fatal(err)
	}

	// Reindex re-embeds every image with force.
	calls = p.imageCalls.Load()
	res, err := s.reindexVectors(ctx, "mm", true, nil)
	if err != nil || len(res.Errors) > 0 || p.imageCalls.Load()-calls != 3 {
		t.Fatalf("reindex: %v %+v, %d image calls", err, res, p.imageCalls.Load()-calls)
	}
	var budgetRes vectorReindexResult
	setTestQuota(t, s, "mm", CollectionQuota{EmbeddingCallsPerDay: 1})
	s.quotas().budget.spendTokens("mm", 1)
	s.reindexImage(ctx, "mm", id, true, &budgetRes)
	p.failImages.Store(true)
	setTestQuota(t, s, "mm", CollectionQuota{})
	s.reindexImage(ctx, "mm", id, true, &budgetRes)
	p.failImages.Store(false)
	if len(budgetRes.Errors) != 2 || !strings.Contains(budgetRes.Errors[0], "embedding budget exhausted") {
		t.Fatalf("reindex errors: %v", budgetRes.Errors)
	}

	// The index is rebuilt from the store at load.
	s.imageIndex().reset()
	s.loadVectorIndex()
	if n := s.imageIndex().size("mm"); n != 3 {
		t.Fatalf("loaded %d image vectors", n)
	}

	// Replication: a vimg| write reaches the image index, a delete leaves it.
	rec := s.VectorStore.GetImage("mm", id)
	data := vec.MarshalEmbeddingRecord(rec)
	ra := NewReplicationApplier(s)
	s.imageIndex().remove("mm", id)
	ra.applyVector(&binlog.BinlogEntry{Type: binlog.BinlogPut, BucketName: "vectors", Key: vec.ImageKey("mm", id), Value: data})
	if !s.imageIndex().has("mm", id) {
		t.Fatal("replicated put")
	}
	ra.applyVector(&binlog.BinlogEntry{Type: binlog.BinlogDelete, BucketName: "vectors", Key: vec.ImageKey("mm", id)})
	s.applyReplicatedImage(&binlog.BinlogEntry{Key: []byte("vimg|short")})
	s.applyReplicatedImage(&binlog.BinlogEntry{Type: binlog.BinlogPut, Key: vec.ImageKey("mm", id), Value: []byte("x")})
	if s.imageIndex().has("mm", id) {
		t.Fatal("replicated delete")
	}

	// Deleting the image document removes its bytes and vector; deleting
	// the collection removes the rest.
	if err := s.deleteDocumentInternal("mm", "manual/images/seal.png", "en"); err != nil {
		t.Fatal(err)
	}
	sealID := genID("mm", "manual/images/seal.png", "en")
	if s.imageBytes("mm", sealID) != nil || s.VectorStore.GetImage("mm", sealID) != nil || s.imageIndex().has("mm", sealID) {
		t.Fatal("document delete left the image behind")
	}
	if _, err := s.deleteCollection("mm"); err != nil {
		t.Fatal(err)
	}
	if s.countImages("mm") != 0 || s.imageIndex().size("mm") != 0 || len(s.VectorStore.LoadImages("mm")) != 0 {
		t.Fatal("collection delete left images behind")
	}
}

// TestImages_MultimodalProviderE2E runs the acceptance test against a real
// multimodal provider: MDDB_TEST_MULTIMODAL_PROVIDER=voyage or cohere, with
// MDDB_TEST_MULTIMODAL_API_KEY (and optionally MDDB_TEST_MULTIMODAL_MODEL).
// Skipped without them, so CI stays offline.
func TestImages_MultimodalProviderE2E(t *testing.T) {
	provider, key := os.Getenv("MDDB_TEST_MULTIMODAL_PROVIDER"), os.Getenv("MDDB_TEST_MULTIMODAL_API_KEY")
	if provider == "" || key == "" {
		t.Skip("set MDDB_TEST_MULTIMODAL_PROVIDER (voyage, cohere) and MDDB_TEST_MULTIMODAL_API_KEY to run against a real provider")
	}
	t.Setenv("MDDB_EMBEDDING_PROVIDER", provider)
	t.Setenv("MDDB_EMBEDDING_API_KEY", key)
	if m := os.Getenv("MDDB_TEST_MULTIMODAL_MODEL"); m != "" {
		t.Setenv("MDDB_EMBEDDING_MODEL", m)
	} else if provider == "cohere" {
		t.Setenv("MDDB_EMBEDDING_MODEL", "embed-v4.0")
	}
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	s.VectorIndex.SetReady()
	for _, sr := range s.VectorSearchers {
		sr.SetReady()
	}
	s.Embedding = embedding.NewProvider()
	if _, ok := embedding.ImageEmbedderOf(s.Embedding); !ok {
		t.Fatalf("%s as configured embeds no images", provider)
	}
	s.startEmbeddingWorker(s.Embedding)
	defer s.EmbeddingWorker.Stop()
	md := "# Colours\n\n![](red.png)\n\n![](blue.png)\n"
	files := map[string][]byte{"colours.md": []byte(md), "red.png": solidPNG(64, 64, fixtureRed), "blue.png": solidPNG(64, 64, fixtureBlue)}
	rec := uploadFiles(t, s, files, []string{"colours.md", "red.png", "blue.png"}, map[string]string{"collection": "e2e", "lang": "en", "extractImages": "true"})
	if rec.Code != http.StatusOK {
		t.Fatalf("upload: %s", rec.Body.String())
	}
	waitIndexed(t, s, "e2e")
	rec = doRequest(t, s.handleVectorSearch, map[string]any{"collection": "e2e", "query": "a plain blue square", "includeImages": true})
	if got := firstImageHit(t, body(t, rec.Body.Bytes())); got != "colours/images/blue.png" {
		t.Fatalf("the blue image without alt text was not found first: %s", rec.Body.String())
	}
}
