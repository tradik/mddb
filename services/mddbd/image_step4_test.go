package main

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	gql "mddb/graphql"
	"mddb/internal/embedding"
	proto "mddb/proto"
)

// RAG-010 step 4: without a multimodal provider, images are stored as
// documents, their alt text and captions are searchable, and a hit on a
// document returns its images with their alt text — on every surface.

// uploadFiles posts several files in one multipart request.
func uploadFiles(t *testing.T, s *Server, files map[string][]byte, order []string, fields map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	var b bytes.Buffer
	w := multipart.NewWriter(&b)
	for k, v := range fields {
		_ = w.WriteField(k, v)
	}
	for _, name := range order {
		part, err := w.CreateFormFile("files[]", name)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = part.Write(files[name])
	}
	_ = w.Close()
	req := httptest.NewRequest(http.MethodPost, "/v1/upload", &b)
	req.Header.Set("Content-Type", w.FormDataContentType())
	rec := httptest.NewRecorder()
	s.handleUpload(rec, req)
	return rec
}

// waitIndexed waits for the collection's embedding jobs to finish.
func waitIndexed(t *testing.T, s *Server, collection string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for s.IndexingStatus(collection).PendingEmbeddings > 0 {
		if time.Now().After(deadline) {
			t.Fatal("embeddings still pending")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// imageServer holds the Markdown fixture, uploaded with its two image files,
// and the PDF fixture, ingested, both with extraction on, embedded by the
// lexical provider: a provider that embeds no images.
func imageServer(t *testing.T) (*Server, func()) {
	t.Helper()
	t.Setenv("MDDB_SEARCH_STATS", "false")
	s, cleanup := newHandlerTestServer(t)
	s.VectorIndex.SetReady()
	for _, sr := range s.VectorSearchers {
		sr.SetReady()
	}
	s.Embedding = embedding.NewLexicalProvider(256)
	s.startEmbeddingWorker(s.Embedding)
	files := map[string][]byte{"manual.md": readImageFixture(t, "manual.md"),
		"seal.png": readImageFixture(t, "img/seal.png"), "wiring.jpg": readImageFixture(t, "img/wiring.jpg")}
	rec := uploadFiles(t, s, files, []string{"manual.md", "seal.png", "wiring.jpg"},
		map[string]string{"collection": "docs", "lang": "en", "extractImages": "true"})
	if rec.Code != http.StatusOK {
		t.Fatalf("upload: %d %s", rec.Code, rec.Body.String())
	}
	ing := doRequest(t, s.handleIngest, map[string]any{"collection": "docs", "options": map[string]any{"extractImages": true},
		"documents": []any{map[string]any{"key": "manual-pdf", "lang": "en", "format": "pdf", "contentBase64": readImageFixture(t, "manual.pdf")}}})
	if ing.Code != http.StatusOK || !strings.Contains(ing.Body.String(), `"added":4`) {
		t.Fatalf("ingest: %d %s", ing.Code, ing.Body.String())
	}
	waitIndexed(t, s, "docs")
	return s, func() { s.EmbeddingWorker.Stop(); cleanup() }
}

// imagesOf returns the alt texts (or captions, for an image without one) of
// the images on each result, by result key.
func imagesOf(results []any) map[string][]string {
	out := map[string][]string{}
	for _, r := range results {
		m := r.(map[string]any)
		doc, _ := m["document"].(map[string]any)
		key, _ := doc["key"].(string)
		if key == "" {
			key, _ = m["key"].(string) // a passage
		}
		imgs, _ := m["images"].([]any)
		for _, im := range imgs {
			ref := im.(map[string]any)
			label, _ := ref["altText"].(string)
			if label == "" {
				label, _ = ref["caption"].(string)
			}
			out[key] = append(out[key], label)
		}
	}
	return out
}

func TestImages_Step4HitReturnsItsImagesWithAltText(t *testing.T) {
	s, cleanup := imageServer(t)
	defer cleanup()

	// The Markdown document's hit carries its three kept images, in order;
	// the PDF's carries its three pictures with their captions.
	rec := doRequest(t, s.handleFTS, map[string]any{"collection": "docs", "query": "pump", "includeImages": true})
	got := imagesOf(body(t, rec.Body.Bytes())["results"].([]any))
	if strings.Join(got["manual"], "|") != "Exploded view of the pump seal|Figure 2: wiring of the motor|Green status lamp" {
		t.Fatalf("manual images: %v", got)
	}
	if strings.Join(got["manual-pdf"], "|") != "Figure 1: Exploded view of the pump|Figure 2: Wiring diagram|Figure 3: Status lamp" {
		t.Fatalf("pdf images: %v", got)
	}
	var pdfRef map[string]any
	for _, r := range body(t, rec.Body.Bytes())["results"].([]any) {
		if m := r.(map[string]any); m["document"].(map[string]any)["key"] == "manual-pdf" {
			pdfRef = m["images"].([]any)[1].(map[string]any)
		}
	}
	if pdfRef["page"] != 2.0 || pdfRef["mimeType"] != "image/png" || pdfRef["parent"] != "manual-pdf" ||
		pdfRef["url"] != "/v1/image?collection=docs&key=manual-pdf%2Fimages%2Fpage-2-image-1.png&lang=en" {
		t.Fatalf("pdf image ref: %v", pdfRef)
	}

	// Alt text and captions are in full-text search: the image is a hit of
	// its own.
	rec = doRequest(t, s.handleFTS, map[string]any{"collection": "docs", "query": "exploded"})
	if !strings.Contains(rec.Body.String(), `"key":"manual/images/seal.png"`) || strings.Contains(rec.Body.String(), `"images"`) {
		t.Fatalf("alt text not searchable, or images without includeImages: %s", rec.Body.String())
	}
	rec = doRequest(t, s.handleFTS, map[string]any{"collection": "docs", "query": "lamp", "format": "passages", "includeImages": true})
	if p := imagesOf(body(t, rec.Body.Bytes())["passages"].([]any)); len(p["manual"]) != 3 {
		t.Fatalf("passage images: %s", rec.Body.String())
	}

	// The image documents: kind, parent, alt text, caption, page.
	img := getTestDoc(t, s, "docs", "manual-pdf/images/page-1-image-1.jpg", "en")
	for k, want := range map[string]string{"kind": "image", "parent": "manual-pdf", "page": "1", "caption": "Figure 1: Exploded view of the pump", "mimeType": "image/jpeg"} {
		if first(img.Meta[k]) != want {
			t.Fatalf("meta.%s = %v, want %s", k, img.Meta[k], want)
		}
	}
	if md := getTestDoc(t, s, "docs", "manual", "en"); len(md.Meta["imageWarnings"]) != 2 {
		t.Fatalf("skipped images are reported: %v", md.Meta["imageWarnings"])
	}
}

// getTestDoc reads a stored document.
func getTestDoc(t *testing.T, s *Server, coll, key, lang string) MCPDocument {
	t.Helper()
	doc, err := NewDirectClient(s).Get(context.Background(), &MCPGetRequest{Collection: coll, Key: key, Lang: lang})
	if err != nil {
		t.Fatalf("%s: %v", key, err)
	}
	return *doc
}

func TestImages_Step4OnEverySurface(t *testing.T) {
	s, cleanup := imageServer(t)
	defer cleanup()
	ctx := context.Background()
	want := func(name string, n int, results any) {
		t.Helper()
		raw, _ := json.Marshal(results)
		var list []any
		_ = json.Unmarshal(raw, &list)
		if got := imagesOf(list); len(got["manual"]) != n {
			t.Fatalf("%s: manual has %d images, want %d: %s", name, len(got["manual"]), n, raw)
		}
	}

	// REST vector and hybrid: images, and a plan line saying nothing could
	// be searched by picture.
	rec := doRequest(t, s.handleVectorSearch, map[string]any{"collection": "docs", "query": "pump seal", "topK": 10, "includeImages": true, "explain": true})
	b := body(t, rec.Body.Bytes())
	want("rest vector", 3, b["results"])
	if !strings.Contains(rec.Body.String(), "includeImages: images attached to hits by meta.parent; no image vectors to search") {
		t.Fatalf("plan: %s", rec.Body.String())
	}
	rec = doRequest(t, s.handleVectorSearch, map[string]any{"collection": "docs", "query": "pump seal", "topK": 10, "includeImages": true, "format": "passages"})
	want("rest vector passages", 3, body(t, rec.Body.Bytes())["passages"])
	rec = doRequest(t, s.handleHybridSearch, map[string]any{"collection": "docs", "query": "pump seal", "topK": 10, "includeImages": true})
	want("rest hybrid", 3, body(t, rec.Body.Bytes())["results"])
	rec = doRequest(t, s.handleHybridSearch, map[string]any{"collection": "docs", "query": "pump seal", "topK": 10, "includeImages": true, "format": "passages"})
	want("rest hybrid passages", 3, body(t, rec.Body.Bytes())["passages"])
	rec = doRequest(t, s.handleCrossSearch, map[string]any{"targetCollections": []string{"docs"}, "query": "pump seal", "topK": 10, "includeImages": true})
	want("rest cross", 3, body(t, rec.Body.Bytes())["results"])
	rec = doRequest(t, s.handleCrossSearch, map[string]any{"targetCollections": []string{"docs"}, "query": "pump seal", "includeImages": true, "format": "passages"})
	want("rest cross passages", 3, body(t, rec.Body.Bytes())["passages"])
	rec = doRequest(t, s.handleCrossSearch, map[string]any{"targetCollections": []string{"docs"}, "query": "pump", "searchType": "fts", "includeImages": true})
	want("rest cross fts", 3, body(t, rec.Body.Bytes())["results"])
	rec = doRequest(t, s.handleCrossSearch, map[string]any{"targetCollections": []string{"docs"}, "query": "pump", "searchType": "fts", "includeImages": true, "format": "passages"})
	want("rest cross fts passages", 3, body(t, rec.Body.Bytes())["passages"])

	// MCP, through the tools as an agent calls them.
	tools := &MCPToolServer{client: NewDirectClient(s), globalMode: ModeRW}
	for _, c := range []struct {
		tool string
		args map[string]interface{}
		key  string
	}{
		{"semantic_search", map[string]interface{}{"collection": "docs", "query": "pump seal", "top_k": 10.0, "include_images": true}, "results"},
		{"semantic_search", map[string]interface{}{"collection": "docs", "query": "pump seal", "top_k": 10.0, "include_images": true, "format": "passages"}, "passages"},
		{"full_text_search", map[string]interface{}{"collection": "docs", "query": "pump", "include_images": true}, "results"},
		{"hybrid_search", map[string]interface{}{"collection": "docs", "query": "pump seal", "top_k": 10.0, "include_images": true}, "results"},
		{"hybrid_search", map[string]interface{}{"collection": "docs", "query": "pump seal", "top_k": 10.0, "include_images": true, "format": "passages"}, "passages"},
		{"cross_search", map[string]interface{}{"target_collections": []interface{}{"docs"}, "query": "pump seal", "include_images": true}, "results"},
		{"cross_search", map[string]interface{}{"target_collections": []interface{}{"docs"}, "query": "pump seal", "include_images": true, "format": "passages"}, "passages"},
		{"cross_search", map[string]interface{}{"target_collections": []interface{}{"docs"}, "query": "pump", "search_type": "fts", "include_images": true}, "results"},
		{"cross_search", map[string]interface{}{"target_collections": []interface{}{"docs"}, "query": "pump", "search_type": "fts", "include_images": true, "format": "passages"}, "passages"},
	} {
		out, err := tools.mcpCallTool(ctx, c.tool, c.args)
		if err != nil {
			t.Fatalf("%s: %v", c.tool, err)
		}
		want("mcp "+c.tool, 3, covBUnmarshal(t, out)[c.key])
	}

	// gRPC.
	g := &GRPCServer{server: s}
	gv, err := g.VectorSearch(ctx, &proto.VectorSearchRequest{Collection: "docs", Query: "pump seal", TopK: 10, IncludeImages: true})
	if err != nil || len(protoImagesOf(gv.Results, "manual")) != 3 {
		t.Fatalf("grpc vector: %v %v", err, gv)
	}
	gp, err := g.VectorSearch(ctx, &proto.VectorSearchRequest{Collection: "docs", Query: "pump seal", TopK: 10, IncludeImages: true, Format: "passages"})
	if err != nil || protoPassageImages(gp.Passages) != 6 {
		t.Fatalf("grpc vector passages: %v %v", err, gp)
	}
	gf, err := g.FTS(ctx, &proto.FTSRequest{Collection: "docs", Query: "pump", IncludeImages: true})
	if err != nil || len(protoImagesOf(gf.Results, "manual")) != 3 {
		t.Fatalf("grpc fts: %v %v", err, gf)
	}
	gh, err := g.HybridSearch(ctx, &proto.HybridSearchRequest{Collection: "docs", Query: "pump seal", TopK: 10, IncludeImages: true})
	if err != nil || len(protoImagesOf(gh.Results, "manual")) != 3 {
		t.Fatalf("grpc hybrid: %v %v", err, gh)
	}
	ghp, err := g.HybridSearch(ctx, &proto.HybridSearchRequest{Collection: "docs", Query: "pump seal", TopK: 10, IncludeImages: true, Format: "passages"})
	if err != nil || protoPassageImages(ghp.Passages) != 6 {
		t.Fatalf("grpc hybrid passages: %v %v", err, ghp)
	}
	gc, err := g.CrossSearch(ctx, &proto.CrossSearchRequest{TargetCollections: []string{"docs"}, Query: "pump seal", TopK: 10, IncludeImages: true})
	if err != nil || len(protoCrossImagesOf(gc.Results, "manual")) != 3 {
		t.Fatalf("grpc cross: %v %v", err, gc)
	}
	st, err := g.GetIndexingStatus(ctx, &proto.IndexingStatusRequest{Collection: "docs"})
	if err != nil || st.Images.GetDocuments() != 6 || st.Images.GetEmbedded() != 0 || !strings.Contains(st.Images.GetNote(), "does not embed images") {
		t.Fatalf("grpc indexing status: %v %v", err, st)
	}

	// GraphQL.
	a := &GraphQLAdapter{server: s, mcp: NewDirectClient(s)}
	q, yes, ten := "pump seal", true, 10
	qv, err := a.VectorSearch(ctx, gql.VectorSearchInput{Collection: "docs", Query: &q, TopK: &ten, IncludeImages: &yes})
	if err != nil || len(gqlImagesOf(qv.Results)) != 3 {
		t.Fatalf("graphql vector: %v", err)
	}
	passages := "passages"
	qp, err := a.VectorSearch(ctx, gql.VectorSearchInput{Collection: "docs", Query: &q, TopK: &ten, IncludeImages: &yes, Format: &passages})
	if err != nil || len(qp.Passages) == 0 {
		t.Fatalf("graphql vector passages: %v", err)
	}
	qf, err := a.FullTextSearch(ctx, gql.FTSInput{Collection: "docs", Query: "pump", IncludeImages: &yes})
	if err != nil || len(gqlFTSImagesOf(qf.Results)) != 3 {
		t.Fatalf("graphql fts: %v", err)
	}
	gs, err := a.IndexingStatus(ctx, nil)
	if err != nil || gs.Images == nil || gs.Images.Documents != 6 || gs.Images.Note == nil {
		t.Fatalf("graphql indexing status: %v %+v", err, gs)
	}
}

// protoImagesOf returns the images of the result for key.
func protoImagesOf[T interface {
	GetDocument() *proto.Document
	GetImages() []*proto.ImageRef
}](results []T, key string) []*proto.ImageRef {
	for _, r := range results {
		if r.GetDocument().GetKey() == key {
			return r.GetImages()
		}
	}
	return nil
}

func gqlImagesOf(results []*gql.VectorSearchResult) []*gql.ImageRef {
	for _, r := range results {
		if r.Document.Key == "manual" {
			return r.Images
		}
	}
	return nil
}

func gqlFTSImagesOf(results []*gql.FTSResult) []*gql.ImageRef {
	for _, r := range results {
		if r.Document.Key == "manual" {
			return r.Images
		}
	}
	return nil
}

// protoPassageImages counts the images on passages.
func protoPassageImages(ps []*proto.Passage) int {
	n := 0
	for _, p := range ps {
		n += len(p.Images)
	}
	return n
}

// protoCrossImagesOf returns the images of the cross-search result for key.
func protoCrossImagesOf(results []*proto.CrossSearchResultItem, key string) []*proto.ImageRef {
	for _, r := range results {
		if r.GetDocument().GetKey() == key {
			return r.GetImages()
		}
	}
	return nil
}
