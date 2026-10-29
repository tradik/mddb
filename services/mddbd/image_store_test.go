package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"mddb/internal/encryption"
	proto "mddb/proto"

	bolt "go.etcd.io/bbolt"
)

// Image bytes, the image endpoint, the images collection type and the write
// paths' image options (RAG-010).

func TestImageEndpoint(t *testing.T) {
	s, cleanup := imageServer(t)
	defer cleanup()
	get := func(url string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		s.handleImage(rec, httptest.NewRequest(http.MethodGet, url, nil))
		return rec
	}
	rec := get("/v1/image?collection=docs&key=manual%2Fimages%2Fseal.png&lang=en")
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "image/png" || rec.Header().Get("X-Content-Type-Options") != "nosniff" ||
		!bytes.Equal(rec.Body.Bytes(), readImageFixture(t, "img/seal.png")) {
		t.Fatalf("image: %d %v", rec.Code, rec.Header())
	}
	if rec = get("/v1/image?collection=docs&key=manual&lang=en"); rec.Code != http.StatusNotFound {
		t.Fatalf("a document with no image: %d", rec.Code)
	}
	if rec = get("/v1/image?collection=docs&key=manual"); rec.Code != http.StatusBadRequest {
		t.Fatalf("no lang: %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	s.handleImage(rec, httptest.NewRequest(http.MethodPost, "/v1/image", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST: %d", rec.Code)
	}
	covGEnableAuth(t, s)
	if rec = get("/v1/image?collection=docs&key=manual%2Fimages%2Fseal.png&lang=en"); rec.Code != http.StatusForbidden {
		t.Fatalf("without read permission: %d", rec.Code)
	}
}

func TestImageBytesEncryptedAndRestored(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	t.Setenv("MDDB_ENCRYPTION_KEY", genKey(t))
	e, _ := encryption.NewEncryptor()
	SetGlobalEncryptor(e)
	defer SetGlobalEncryptor(nil)
	e.SetCollectionEnabled("vault", true)
	png := solidPNG(20, 20, fixtureRed)
	if err := s.putImageBytes("vault", "vault|a|en", png); err != nil {
		t.Fatal(err)
	}
	var raw []byte
	_ = s.DBViewC("vault", func(tx *bolt.Tx) error {
		raw = bytes.Clone(tx.Bucket(bucketImages).Get(imageBytesKey("vault", "vault|a|en")))
		return nil
	})
	if !encryption.IsEncrypted(raw) || !bytes.Equal(s.imageBytes("vault", "vault|a|en"), png) {
		t.Fatal("an encrypted collection's image bytes are sealed and read back")
	}
	SetGlobalEncryptor(nil)
	if s.imageBytes("vault", "vault|a|en") != nil {
		t.Fatal("sealed bytes without a key read as nothing")
	}

	// A failed write puts back what the document had, or removes what it
	// did not have.
	d := convertedDoc{Key: "a", Image: &extractedImage{Data: solidPNG(20, 20, fixtureBlue)}}
	if err := s.putImageBytes("plain", "plain|a|en", png); err != nil {
		t.Fatal(err)
	}
	errWrite := errors.New("write refused")
	if err := s.withImageBytes("plain", "en", d, func() error { return errWrite }); !errors.Is(err, errWrite) || !bytes.Equal(s.imageBytes("plain", "plain|a|en"), png) {
		t.Fatalf("restore: %v", err)
	}
	d.Key = "b"
	if err := s.withImageBytes("plain", "en", d, func() error { return errWrite }); !errors.Is(err, errWrite) || s.imageBytes("plain", "plain|b|en") != nil {
		t.Fatalf("remove: %v", err)
	}
	if s.countImages("") != 2 || s.countImages("plain") != 1 { // vault and plain
		t.Fatalf("count: %d %d", s.countImages(""), s.countImages("plain"))
	}
}

func TestImagesCollectionTakesImageFiles(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	if err := s.CollectionManager.Set("pics", &CollectionConfig{Type: collectionTypeImages}); err != nil {
		t.Fatal(err)
	}
	png := solidPNG(32, 32, fixtureGreen)
	rec := uploadFile(t, s, "Lamp.png", png, map[string]string{"collection": "pics", "lang": "en", "meta": `{"altText":["Green status lamp"]}`})
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"format":"png"`) {
		t.Fatalf("image upload: %d %s", rec.Code, rec.Body.String())
	}
	doc := getTestDoc(t, s, "pics", "lamp", "en")
	if doc.ContentMD != "![Green status lamp](lamp.png)" || first(doc.Meta["kind"]) != "image" || doc.Meta["parent"] != nil {
		t.Fatalf("image document: %+v", doc)
	}
	if !bytes.Equal(s.imageBytes("pics", doc.ID), png) {
		t.Fatal("bytes")
	}
	// Not an image under an image's name, and a Markdown file whose images
	// are extracted without being asked.
	if rec = uploadFile(t, s, "fake.png", []byte("not a picture"), map[string]string{"collection": "pics", "lang": "en"}); rec.Code != http.StatusBadRequest {
		t.Fatalf("fake image: %d %s", rec.Code, rec.Body.String())
	}
	md := "![Lamp](data:image/png;base64," + base64.StdEncoding.EncodeToString(png) + ")"
	if rec = uploadFile(t, s, "page.md", []byte(md), map[string]string{"collection": "pics", "lang": "en"}); rec.Code != http.StatusOK ||
		!strings.Contains(rec.Body.String(), `"key":"page/images/image-1.png"`) {
		t.Fatalf("default extraction: %d %s", rec.Code, rec.Body.String())
	}
	// Elsewhere an image file is still no document format, and an upload of
	// image files alone with extraction on is refused.
	if rec = uploadFile(t, s, "a.png", png, map[string]string{"collection": "notes", "lang": "en"}); rec.Code != http.StatusBadRequest {
		t.Fatalf("image elsewhere: %d", rec.Code)
	}
	files := map[string][]byte{"a.png": png, "b.png": png}
	if rec = uploadFiles(t, s, files, []string{"a.png", "b.png"}, map[string]string{"collection": "notes", "lang": "en", "extractImages": "true"}); rec.Code != http.StatusBadRequest {
		t.Fatalf("images alone: %d %s", rec.Code, rec.Body.String())
	}
	// An image too large to be an asset stays a file to store, and fails.
	big := append(solidPNG(20, 20, fixtureRed), make([]byte, maxImageBytes)...)
	files = map[string][]byte{"page.md": []byte("![](big.png)"), "big.png": big}
	rec = uploadFiles(t, s, files, []string{"page.md", "big.png"}, map[string]string{"collection": "notes", "lang": "en", "extractImages": "true"})
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"failed":1`) {
		t.Fatalf("large asset: %d %s", rec.Code, rec.Body.String())
	}
	if c, err := s.convertFile(context.Background(), "pics", "txt", []byte("hello"), false, imageOptions{}); err != nil || c.Markdown != "hello" {
		t.Fatalf("text in an images collection: %+v %v", c, err)
	}
}

func TestIngestImagesOnEveryPath(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	ctx := context.Background()
	png, jpg := readImageFixture(t, "img/seal.png"), readImageFixture(t, "img/wiring.jpg")
	md := string(readImageFixture(t, "manual.md"))

	// gRPC: images and options travel.
	g := &GRPCServer{server: s}
	resp, err := g.Ingest(ctx, &proto.IngestRequest{Collection: "g", Options: &proto.IngestOptions{ExtractImages: true, SkipDuplicates: true},
		Documents: []*proto.IngestDocument{{Key: "manual.md", Lang: "en", ContentMd: md,
			Images: []*proto.IngestImage{{Path: "img/seal.png", Content: png}, {Path: "img/wiring.jpg", Content: jpg}}}}})
	if err != nil || resp.Added != 4 {
		t.Fatalf("grpc: %v %+v", err, resp)
	}
	// Ingested again with a changed picture and the same text: only the
	// image whose bytes changed is written.
	resp, err = g.Ingest(ctx, &proto.IngestRequest{Collection: "g", Options: &proto.IngestOptions{ExtractImages: true, SkipDuplicates: true},
		Documents: []*proto.IngestDocument{{Key: "manual.md", Lang: "en", ContentMd: md,
			Images: []*proto.IngestImage{{Path: "img/seal.png", Content: solidPNG(40, 30, fixtureGreen)}, {Path: "img/wiring.jpg", Content: jpg}}}}})
	if err != nil || resp.Updated != 1 || resp.Skipped != 3 {
		t.Fatalf("grpc again: %v %+v", err, resp)
	}
	if !bytes.Equal(s.imageBytes("g", genID("g", "manual.md/images/seal.png", "en")), solidPNG(40, 30, fixtureGreen)) {
		t.Fatal("the changed picture was stored")
	}

	// MCP: the tool reads images and options; bad base64 is refused.
	tools := &MCPToolServer{client: NewDirectClient(s), globalMode: ModeRW}
	out, err := tools.mcpCallTool(ctx, "ingest_documents", map[string]interface{}{"collection": "m",
		"options": map[string]interface{}{"extract_images": true, "fetch_remote_images": false},
		"documents": []interface{}{map[string]interface{}{"key": "manual.md", "lang": "en", "content_md": md,
			"images": []interface{}{map[string]interface{}{"path": "img/seal.png", "content_base64": base64.StdEncoding.EncodeToString(png)}}}}})
	if err != nil || !strings.Contains(out, `"added": 3`) {
		t.Fatalf("mcp ingest: %v %s", err, out)
	}
	if _, err := tools.mcpCallTool(ctx, "ingest_documents", map[string]interface{}{"collection": "m",
		"documents": []interface{}{map[string]interface{}{"key": "x.md", "lang": "en", "content_md": "x",
			"images": []interface{}{map[string]interface{}{"path": "a.png", "content_base64": "!!!"}}}}}); err == nil {
		t.Fatal("bad image base64")
	}

	// REST: a document whose key names another format keeps its links as
	// text; a failed write stores no bytes.
	rec := doRequest(t, s.handleIngest, map[string]any{"collection": "r", "options": map[string]any{"extractImages": true},
		"documents": []any{map[string]any{"key": "notes.txt", "lang": "en", "contentMd": "![](a.png)", "images": []any{map[string]any{"path": "a.png", "contentBase64": png}}}}})
	if !strings.Contains(rec.Body.String(), `"added":1`) {
		t.Fatalf("txt: %s", rec.Body.String())
	}
	s.storeIngestImages("r", map[string][]byte{"r|x|en": png}, []*ProcessedDoc{nil, {DocID: "r|x|en", Error: errors.New("failed")}})
	if s.imageBytes("r", "r|x|en") != nil {
		t.Fatal("bytes of a failed write")
	}
	if ingestImagesFromProto(nil) != nil || ingestImageFiles(nil) != nil {
		t.Fatal("nothing in, nothing out")
	}
}

func TestMCPUploadFileImages(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	ctx := context.Background()
	tools := &MCPToolServer{client: NewDirectClient(s), globalMode: ModeRW}
	png := readImageFixture(t, "img/seal.png")
	out, err := tools.mcpCallTool(ctx, "upload_file", map[string]interface{}{"collection": "u", "lang": "en", "filename": "guide.md",
		"content": base64.StdEncoding.EncodeToString([]byte("# Guide\n\n![Seal](img/seal.png)")), "extract_images": true,
		"images": []interface{}{map[string]interface{}{"path": "img/seal.png", "content_base64": base64.StdEncoding.EncodeToString(png)}}})
	if err != nil || !strings.Contains(out, `"guide/images/seal.png"`) {
		t.Fatalf("upload_file: %v %s", err, out)
	}
	if !bytes.Equal(s.imageBytes("u", genID("u", "guide/images/seal.png", "en")), png) {
		t.Fatal("bytes")
	}
	if _, err := tools.mcpCallTool(ctx, "upload_file", map[string]interface{}{"collection": "u", "lang": "en", "filename": "g.md",
		"content": base64.StdEncoding.EncodeToString([]byte("x")), "images": []interface{}{map[string]interface{}{"content_base64": "!!!"}}}); err == nil {
		t.Fatal("bad image base64")
	}
	if _, err := NewDirectClient(s).UploadFile(ctx, &MCPUploadFileRequest{Collection: "u", Lang: "en", Filename: "a.exe", Content: []byte("x")}); err == nil {
		t.Fatal("unsupported format")
	}
	// A store that refuses the write: the image's bytes do not stay.
	setTestQuota(t, s, "full", CollectionQuota{MaxDocuments: 1})
	if _, err := NewDirectClient(s).UploadFile(ctx, &MCPUploadFileRequest{Collection: "full", Lang: "en", Filename: "g.md", ExtractImages: true,
		Content: []byte("![](s.png)"), Images: []IngestImage{{Path: "s.png", Content: png}}}); err == nil {
		t.Fatal("over quota")
	}
	if s.countImages("full") != 0 {
		t.Fatal("bytes of a refused image document")
	}
}

func TestImageStatusWithoutProvider(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	if err := s.putImageBytes("c", "c|a|en", solidPNG(20, 20, fixtureRed)); err != nil {
		t.Fatal(err)
	}
	st := s.IndexingStatus("c").Images
	if st == nil || st.Documents != 1 || !strings.Contains(st.Note, "no embedding provider") {
		t.Fatalf("status: %+v", st)
	}
	var ext extractedImage
	if d := imageDocOf(ext, "pics/photo.jpeg", "", nil); d.Markdown != "![](photo.jpeg)" {
		t.Fatalf("a name with its extension: %q", d.Markdown)
	}
	if isImageFile("", []byte("text")) {
		t.Fatal("text is no image")
	}
	if imageRefsToGQL(nil) != nil || imageRefsToProto(nil) != nil {
		t.Fatal("no refs")
	}
	s.CollectionManager = nil
	if s.imagesCollection("c") {
		t.Fatal("no collection manager, no images collections")
	}
}
