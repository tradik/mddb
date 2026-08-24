package embedding

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// SRCH-001: a fresh install with no configuration has no semantic search, and
// a great many of those machines are already running Ollama. The gap was never
// the model — nothing asked.

func TestPickEmbeddingModelPrefersQualityOverInstallOrder(t *testing.T) {
	// all-minilm is listed first and is the weaker model. Installation order
	// is not a preference.
	model, dims, ok := pickEmbeddingModel([]string{"all-minilm:latest", "nomic-embed-text:latest"})
	if !ok {
		t.Fatal("no model was picked")
	}
	if model != "nomic-embed-text:latest" {
		t.Errorf("picked %q, want nomic-embed-text:latest", model)
	}
	if dims != 768 {
		t.Errorf("dimensions %d, want 768", dims)
	}
}

func TestPickEmbeddingModelKeepsTheFullTaggedName(t *testing.T) {
	// Ollama's API expects the name back exactly as reported, tag included.
	model, _, ok := pickEmbeddingModel([]string{"nomic-embed-text:v1.5"})
	if !ok || model != "nomic-embed-text:v1.5" {
		t.Fatalf("got %q, want the tagged name", model)
	}
}

func TestPickEmbeddingModelIgnoresModelsItCannotSize(t *testing.T) {
	// Ollama does not report embedding dimensions. Guessing wrong writes
	// vectors no later query can match, which is worse than no configuration —
	// so an unknown model is left for a human.
	if _, _, ok := pickEmbeddingModel([]string{"llama3:8b", "mistral:latest", "some-new-embedder"}); ok {
		t.Error("a model of unknown dimensionality was auto-selected")
	}
}

func TestPickEmbeddingModelOnAnEmptyList(t *testing.T) {
	if _, _, ok := pickEmbeddingModel(nil); ok {
		t.Error("something was picked from nothing")
	}
}

func TestDetectLocalProviderFindsAModel(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/tags" {
			t.Errorf("probed %s, want /api/tags", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"models":[{"name":"llama3:8b"},{"name":"nomic-embed-text:latest"}]}`))
	}))
	defer server.Close()

	t.Setenv("OLLAMA_HOST", server.URL)
	detected := DetectLocalProvider(context.Background())

	if detected == nil {
		t.Fatal("a running Ollama with an embedding model was not detected")
	}
	if detected.Model != "nomic-embed-text:latest" || detected.Dimensions != 768 {
		t.Errorf("detected %+v", detected)
	}
	if detected.Provider == nil {
		t.Error("no provider was built")
	}
	if detected.APIURL != server.URL {
		t.Errorf("APIURL %q, want %q", detected.APIURL, server.URL)
	}
}

func TestDetectLocalProviderIgnoresAnOllamaWithNoEmbeddingModel(t *testing.T) {
	// Chat models are not embedding models. Using one would produce vectors
	// that are not embeddings.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"models":[{"name":"llama3:8b"}]}`))
	}))
	defer server.Close()

	t.Setenv("OLLAMA_HOST", server.URL)
	if got := DetectLocalProvider(context.Background()); got != nil {
		t.Errorf("detected %+v from a chat-only Ollama", got)
	}
}

func TestDetectLocalProviderOnNothingListening(t *testing.T) {
	// The ordinary case on most machines, and not an error.
	t.Setenv("OLLAMA_HOST", "http://127.0.0.1:1")
	if got := DetectLocalProvider(context.Background()); got != nil {
		t.Errorf("detected %+v with nothing listening", got)
	}
}

func TestDetectLocalProviderRespectsTheOptOut(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("the probe ran despite the opt-out")
		_, _ = w.Write([]byte(`{"models":[{"name":"nomic-embed-text:latest"}]}`))
	}))
	defer server.Close()

	t.Setenv("OLLAMA_HOST", server.URL)
	t.Setenv("MDDB_EMBEDDING_AUTODETECT", "0")

	if got := DetectLocalProvider(context.Background()); got != nil {
		t.Errorf("detected %+v with the probe disabled", got)
	}
}

func TestDetectLocalProviderOnABadResponse(t *testing.T) {
	cases := map[string]http.HandlerFunc{
		"error status": func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusInternalServerError) },
		"not JSON":     func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("<html>not ollama</html>")) },
		"empty list":   func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{"models":[]}`)) },
	}

	for name, handler := range cases {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(handler)
			defer server.Close()
			t.Setenv("OLLAMA_HOST", server.URL)

			if got := DetectLocalProvider(context.Background()); got != nil {
				t.Errorf("detected %+v from a %s response", got, name)
			}
		})
	}
}

// OLLAMA_HOST is conventionally written without a scheme.
func TestDetectLocalProviderAcceptsASchemelessHost(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"models":[{"name":"mxbai-embed-large:latest"}]}`))
	}))
	defer server.Close()

	host := server.URL[len("http://"):]
	t.Setenv("OLLAMA_HOST", host)

	detected := DetectLocalProvider(context.Background())
	if detected == nil {
		t.Fatal("a schemeless OLLAMA_HOST was not understood")
	}
	if detected.Dimensions != 1024 {
		t.Errorf("dimensions %d, want 1024", detected.Dimensions)
	}
}

func TestDetectLocalProviderTrimsATrailingSlash(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/tags" {
			t.Errorf("probed %s — a trailing slash was carried into the path", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"models":[{"name":"bge-m3:latest"}]}`))
	}))
	defer server.Close()

	t.Setenv("OLLAMA_HOST", server.URL+"/")
	if got := DetectLocalProvider(context.Background()); got == nil {
		t.Fatal("a trailing slash defeated detection")
	}
}

func TestEnvOr(t *testing.T) {
	t.Setenv("MDDB_TEST_ENVOR", "")
	if got := envOr("MDDB_TEST_ENVOR", "fallback"); got != "fallback" {
		t.Errorf("an empty variable should fall back, got %q", got)
	}
	t.Setenv("MDDB_TEST_ENVOR", "set")
	if got := envOr("MDDB_TEST_ENVOR", "fallback"); got != "set" {
		t.Errorf("got %q, want the set value", got)
	}
}
