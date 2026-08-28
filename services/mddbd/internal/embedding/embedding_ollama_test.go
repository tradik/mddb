package embedding

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	json "mddb/internal/jsonx"
)

func TestOllamaEmbeddingProvider_New(t *testing.T) {
	p := NewOllamaProvider("http://localhost:11434", "nomic-embed-text", 768)
	if p.Model() != "nomic-embed-text" {
		t.Errorf("Model = %q", p.Model())
	}
	if p.Dimensions() != 768 {
		t.Errorf("Dimensions = %d", p.Dimensions())
	}
}

func TestOllamaEmbeddingProvider_Embed(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Errorf("Method = %q, want POST", r.Method)
		}
		if r.URL.Path != "/api/embed" {
			t.Errorf("Path = %q, want /api/embed", r.URL.Path)
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("Content-Type = %q", r.Header.Get("Content-Type"))
		}

		var req ollamaEmbedRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode request: %v", err)
		}
		if req.Model != "nomic-embed-text" {
			t.Errorf("model = %q, want nomic-embed-text", req.Model)
		}
		if req.Input == "" {
			t.Error("input should not be empty")
		}

		resp := ollamaEmbedResponse{
			Model:      "nomic-embed-text",
			Embeddings: [][]float64{{0.1, 0.2, 0.3, 0.4}},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	p := NewOllamaProvider(server.URL, "nomic-embed-text", 4)

	vec, err := p.Embed(context.Background(), "hello world", RoleDocument)
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if len(vec) != 4 {
		t.Fatalf("vec length = %d, want 4", len(vec))
	}
	if vec[0] != float32(0.1) {
		t.Errorf("vec[0] = %f, want 0.1", vec[0])
	}
	// Dimensions should be updated from response
	if p.Dimensions() != 4 {
		t.Errorf("Dimensions = %d, want 4", p.Dimensions())
	}
}

func TestOllamaEmbeddingProvider_EmbedBatch(t *testing.T) {
	callCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		resp := ollamaEmbedResponse{
			Model:      "nomic",
			Embeddings: [][]float64{{float64(callCount) * 0.1, float64(callCount) * 0.2}},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	p := NewOllamaProvider(server.URL, "nomic", 2)

	vecs, err := p.EmbedBatch(context.Background(), []string{"a", "b", "c"}, RoleDocument)
	if err != nil {
		t.Fatalf("EmbedBatch: %v", err)
	}
	if len(vecs) != 3 {
		t.Fatalf("vecs length = %d, want 3", len(vecs))
	}
	// Ollama EmbedBatch calls Embed individually, so callCount should be 3
	if callCount != 3 {
		t.Errorf("expected 3 API calls, got %d", callCount)
	}
}

func TestOllamaEmbeddingProvider_EmbedBatchError(t *testing.T) {
	callCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		if callCount == 2 {
			http.Error(w, `{"error":"model not found"}`, http.StatusInternalServerError)
			return
		}
		resp := ollamaEmbedResponse{
			Model:      "nomic",
			Embeddings: [][]float64{{0.1, 0.2}},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	p := NewOllamaProvider(server.URL, "nomic", 2)

	_, err := p.EmbedBatch(context.Background(), []string{"a", "b", "c"}, RoleDocument)
	if err == nil {
		t.Fatal("expected error when one embed fails in batch")
	}
}

func TestOllamaEmbeddingProvider_EmbedAPIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"model not found"}`, http.StatusNotFound)
	}))
	defer server.Close()

	p := NewOllamaProvider(server.URL, "nonexistent-model", 768)

	_, err := p.Embed(context.Background(), "hello", RoleDocument)
	if err == nil {
		t.Fatal("expected error for API error")
	}
}

func TestOllamaEmbeddingProvider_EmbedEmptyEmbeddings(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := ollamaEmbedResponse{
			Model:      "nomic",
			Embeddings: [][]float64{},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	p := NewOllamaProvider(server.URL, "nomic", 768)

	_, err := p.Embed(context.Background(), "hello", RoleDocument)
	if err == nil {
		t.Fatal("expected error for empty embeddings")
	}
}

func TestOllamaEmbeddingProvider_EmbedInvalidJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("{invalid"))
	}))
	defer server.Close()

	p := NewOllamaProvider(server.URL, "nomic", 768)

	_, err := p.Embed(context.Background(), "hello", RoleDocument)
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestOllamaEmbeddingProvider_EmbedContextCancelled(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
	}))
	defer server.Close()

	p := NewOllamaProvider(server.URL, "nomic", 768)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	_, err := p.Embed(ctx, "hello", RoleDocument)
	if err == nil {
		t.Fatal("expected error for cancelled context")
	}
}

func TestOllamaEmbeddingProvider_EmbedConnectionError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	server.Close()

	p := NewOllamaProvider(server.URL, "nomic", 768)

	_, err := p.Embed(context.Background(), "hello", RoleDocument)
	if err == nil {
		t.Fatal("expected error for connection failure")
	}
}
