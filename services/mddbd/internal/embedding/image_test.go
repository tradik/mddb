package embedding

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	json "mddb/internal/jsonx"
)

// Image embedding (RAG-010): which providers embed images, what they send,
// and the cache passing it all through.

var testImage = Image{Data: []byte("\x89PNG fake"), MimeType: "image/png"}

func TestImageDataURIAndOptionalInterface(t *testing.T) {
	if got := testImage.DataURI(); got != "data:image/png;base64,iVBORyBmYWtl" {
		t.Fatalf("DataURI = %q", got)
	}
	if _, ok := ImageEmbedderOf(nil); ok {
		t.Fatal("nil embeds images")
	}
	if _, ok := ImageEmbedderOf(NewLexicalProvider(64)); ok {
		t.Fatal("lexical embeds images")
	}
	if _, ok := ImageEmbedderOf(NewOpenAIProvider("k", "http://x", "text-embedding-3-small", 3)); ok {
		t.Fatal("openai embeds images")
	}
	if _, ok := ImageEmbedderOf(NewOllamaProvider("http://x", "nomic-embed-text", 3)); ok {
		t.Fatal("ollama embeds images")
	}
	off := NewVoyageProvider("k", "http://x", "voyage-3", 3)
	off.SetImageModel("none")
	if _, ok := ImageEmbedderOf(off); ok {
		t.Fatal("images off still embeds images")
	}
	if DocumentVariantOf(NewLexicalProvider(64)) != "" {
		t.Fatal("a provider without a variant")
	}
}

func TestVoyageImageModel(t *testing.T) {
	for _, c := range []struct{ text, set, want string }{
		{"voyage-3", "", DefaultVoyageImageModel},
		{"voyage-multimodal-3", "", "voyage-multimodal-3"},
		{"voyage-3", "voyage-multimodal-3", "voyage-multimodal-3"},
		{"voyage-3", "none", ""},
	} {
		p := NewVoyageProvider("k", "http://x", c.text, 3)
		p.SetImageModel(c.set)
		if p.ImageModel() != c.want {
			t.Errorf("%s/%s: %q", c.text, c.set, p.ImageModel())
		}
	}
}

// multimodalServer answers like Voyage's /multimodalembeddings, checking the
// request, or with status when it is not 200.
func multimodalServer(t *testing.T, status int, reply string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/multimodalembeddings" || r.Header.Get("Authorization") != "Bearer vo" {
			t.Errorf("request %s %v", r.URL.Path, r.Header)
		}
		var req voyageMultimodalRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		item := req.Inputs[0].Content[0]
		if req.Model != "voyage-multimodal-3.5" || (item.Type == "image_base64") == (item.ImageBase64 == "") {
			t.Errorf("body %+v", req)
		}
		w.WriteHeader(status)
		_, _ = io.WriteString(w, reply)
	}))
}

func TestVoyageEmbedImage(t *testing.T) {
	ok := multimodalServer(t, http.StatusOK, `{"data":[{"embedding":[0.1,0.2,0.3],"index":0}]}`)
	defer ok.Close()
	p := NewVoyageProvider("vo", ok.URL, "voyage-3", 3)
	v, err := p.EmbedImage(context.Background(), testImage, RoleDocument)
	if err != nil || len(v) != 3 {
		t.Fatalf("image: %v %v", v, err)
	}
	if v, err = p.EmbedImageText(context.Background(), "a blue square", RoleQuery); err != nil || len(v) != 3 {
		t.Fatalf("text: %v %v", v, err)
	}
	for name, srv := range map[string]*httptest.Server{
		"status":    multimodalServer(t, http.StatusBadRequest, `{"detail":"bad image"}`),
		"not json":  multimodalServer(t, http.StatusOK, `not json`),
		"bad index": multimodalServer(t, http.StatusOK, `{"data":[{"embedding":[0.1],"index":5}]}`),
	} {
		p := NewVoyageProvider("vo", srv.URL, "voyage-3", 3)
		if _, err := p.EmbedImage(context.Background(), testImage, RoleDocument); err == nil {
			t.Errorf("%s: no error", name)
		}
		srv.Close()
	}
	off := NewVoyageProvider("vo", ok.URL, "voyage-3", 3)
	off.SetImageModel("none")
	if _, err := off.EmbedImage(context.Background(), testImage, RoleDocument); err == nil {
		t.Fatal("no multimodal model")
	}
	bad := NewVoyageProvider("vo", "http://bad\x7furl", "voyage-3", 3)
	if _, err := bad.EmbedImage(context.Background(), testImage, RoleDocument); err == nil || !strings.Contains(err.Error(), "create request") {
		t.Fatalf("bad url: %v", err)
	}
	if _, err := bad.EmbedBatch(context.Background(), []string{"x"}, RoleQuery); err == nil {
		t.Fatal("bad url, text")
	}
	closed := multimodalServer(t, http.StatusOK, "")
	closed.Close()
	gone := NewVoyageProvider("vo", closed.URL, "voyage-3", 3)
	if _, err := gone.EmbedImage(context.Background(), testImage, RoleDocument); err == nil {
		t.Fatal("unreachable server")
	}
}

func TestCohereImageModel(t *testing.T) {
	for model, want := range map[string]bool{
		"embed-english-v2.0":            false,
		"embed-english-v3.0":            true,
		"embed-multilingual-light-v3.0": true,
		"embed-v4.0":                    true,
		"embed-v12":                     true,
		"custom-model":                  false,
	} {
		p := NewCohereProvider("k", "", model, 3)
		if (p.ImageModel() != "") != want {
			t.Errorf("%s: %q", model, p.ImageModel())
		}
	}
	p := NewCohereProvider("k", "", "embed-v4.0", 3)
	p.SetImageModel("none")
	if p.ImageModel() != "" {
		t.Fatal("images off")
	}
}

func TestCohereEmbedImage(t *testing.T) {
	var gotType string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		_ = json.NewDecoder(r.Body).Decode(&req)
		gotType, _ = req["input_type"].(string)
		switch {
		case req["images"] != nil && strings.HasPrefix(req["images"].([]any)[0].(string), "data:image/png;base64,"):
			_, _ = io.WriteString(w, `{"embeddings":[[0.1,0.2,0.3]]}`)
		default:
			_, _ = io.WriteString(w, `{"embeddings":[[0.3,0.2,0.1]]}`)
		}
	}))
	defer srv.Close()
	p := NewCohereProvider("k", srv.URL, "embed-v4.0", 3)
	v, err := p.EmbedImage(context.Background(), testImage, RoleQuery)
	if err != nil || v[0] != 0.1 || gotType != "image" {
		t.Fatalf("image: %v %v %s", v, err, gotType)
	}
	if v, err = p.EmbedImageText(context.Background(), "red", RoleQuery); err != nil || v[0] != 0.3 || gotType != "search_query" {
		t.Fatalf("text: %v %v %s", v, err, gotType)
	}
	for name, reply := range map[string]struct {
		status int
		body   string
	}{
		"status":   {http.StatusTooManyRequests, `{"message":"slow down"}`},
		"not json": {http.StatusOK, `nope`},
		"empty":    {http.StatusOK, `{"embeddings":[]}`},
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(reply.status)
			_, _ = io.WriteString(w, reply.body)
		}))
		if _, err := NewCohereProvider("k", srv.URL, "embed-v4.0", 3).EmbedImage(context.Background(), testImage, RoleDocument); err == nil {
			t.Errorf("%s: no error", name)
		}
		srv.Close()
	}
	bad := NewCohereProvider("k", "http://bad\x7furl", "embed-v4.0", 3)
	if _, err := bad.EmbedImage(context.Background(), testImage, RoleDocument); err == nil {
		t.Fatal("bad url")
	}
	if _, err := bad.Embed(context.Background(), "x", RoleDocument); err == nil {
		t.Fatal("bad url, text")
	}
	srv.Close()
	if _, err := p.EmbedImage(context.Background(), testImage, RoleDocument); err == nil {
		t.Fatal("unreachable server")
	}
}

// imageStub is an image-capable provider for the cache tests.
type imageStub struct {
	textCalls, imageCalls int
	fail                  bool
}

var errStub = errors.New("stub failed")

func (s *imageStub) Embed(context.Context, string, Role) ([]float32, error) { return []float32{1}, nil }
func (s *imageStub) EmbedBatch(context.Context, []string, Role) ([][]float32, error) {
	return [][]float32{{1}}, nil
}
func (s *imageStub) Model() string      { return "stub" }
func (s *imageStub) Dimensions() int    { return 1 }
func (s *imageStub) ImageModel() string { return "stub-image" }
func (s *imageStub) EmbedImage(context.Context, Image, Role) ([]float32, error) {
	s.imageCalls++
	return []float32{2}, nil
}
func (s *imageStub) EmbedImageText(context.Context, string, Role) ([]float32, error) {
	s.textCalls++
	if s.fail {
		return nil, errStub
	}
	return []float32{3}, nil
}

func TestCachePassesImagesThrough(t *testing.T) {
	stub := &imageStub{}
	c := NewCachingProvider(stub, 8, 0)
	ie, ok := ImageEmbedderOf(c)
	if !ok || ie.ImageModel() != "stub-image" {
		t.Fatal("the cache hides the provider's images")
	}
	if v, err := ie.EmbedImage(context.Background(), testImage, RoleDocument); err != nil || v[0] != 2 {
		t.Fatalf("image: %v %v", v, err)
	}
	for i := 0; i < 2; i++ {
		if v, err := ie.EmbedImageText(context.Background(), "blue", RoleQuery); err != nil || v[0] != 3 {
			t.Fatalf("text: %v %v", v, err)
		}
	}
	if stub.textCalls != 1 {
		t.Fatalf("image-space text embedded %d times", stub.textCalls)
	}
	// Keyed apart from the same text's ordinary vector.
	if v, _ := c.Embed(context.Background(), "blue", RoleQuery); v[0] != 1 {
		t.Fatal("the ordinary vector")
	}
	stub.fail = true
	if _, err := ie.EmbedImageText(context.Background(), "red", RoleQuery); !errors.Is(err, errStub) {
		t.Fatalf("failure: %v", err)
	}

	plain := NewCachingProvider(NewLexicalProvider(16), 8, 0).(*CachingProvider)
	if plain.ImageModel() != "" {
		t.Fatal("lexical through the cache")
	}
	if _, err := plain.EmbedImage(context.Background(), testImage, RoleDocument); !errors.Is(err, ErrNoImageEmbedding) {
		t.Fatalf("image: %v", err)
	}
	if _, err := plain.EmbedImageText(context.Background(), "x", RoleQuery); !errors.Is(err, ErrNoImageEmbedding) {
		t.Fatalf("text: %v", err)
	}
}

func TestNewProviderImageModel(t *testing.T) {
	t.Setenv("MDDB_EMBEDDING_CACHE_SIZE", "0")
	t.Setenv("MDDB_EMBEDDING_API_KEY", "k")
	t.Setenv("MDDB_EMBEDDING_PROVIDER", "voyage")
	t.Setenv("MDDB_EMBEDDING_IMAGE_MODEL", "voyage-multimodal-3")
	if ie, ok := ImageEmbedderOf(NewProvider()); !ok || ie.ImageModel() != "voyage-multimodal-3" {
		t.Fatal("voyage image model from the environment")
	}
	t.Setenv("MDDB_EMBEDDING_PROVIDER", "cohere")
	t.Setenv("MDDB_EMBEDDING_MODEL", "embed-v4.0")
	t.Setenv("MDDB_EMBEDDING_IMAGE_MODEL", "none")
	if _, ok := ImageEmbedderOf(NewProvider()); ok {
		t.Fatal("cohere with images off")
	}
}
