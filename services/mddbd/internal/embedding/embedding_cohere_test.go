package embedding

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	json "mddb/internal/jsonx"
)

func TestCohereEmbeddingProvider_New(t *testing.T) {
	p := NewCohereProvider("test-key", "", "embed-english-v3.0", 1024)
	if p.apiURL != "https://api.cohere.ai/v1" {
		t.Errorf("apiURL = %q, want default", p.apiURL)
	}
	if p.Model() != "embed-english-v3.0" {
		t.Errorf("Model = %q", p.Model())
	}
	if p.Dimensions() != 1024 {
		t.Errorf("Dimensions = %d", p.Dimensions())
	}
}

func TestCohereEmbeddingProvider_NewCustomURL(t *testing.T) {
	p := NewCohereProvider("test-key", "https://custom.api.com", "model", 512)
	if p.apiURL != "https://custom.api.com" {
		t.Errorf("apiURL = %q, want custom", p.apiURL)
	}
}

func TestCohereEmbeddingProvider_Embed(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify request
		if r.Method != "POST" {
			t.Errorf("Method = %q, want POST", r.Method)
		}
		if r.URL.Path != "/embed" {
			t.Errorf("Path = %q, want /embed", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("Authorization = %q", r.Header.Get("Authorization"))
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("Content-Type = %q", r.Header.Get("Content-Type"))
		}

		var req cohereEmbedRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode request: %v", err)
		}
		if len(req.Texts) != 1 {
			t.Errorf("texts len = %d, want 1", len(req.Texts))
		}
		if req.InputType != "search_document" {
			t.Errorf("InputType = %q, want search_document", req.InputType)
		}

		resp := cohereEmbedResponse{
			Embeddings: [][]float32{{0.1, 0.2, 0.3}},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	p := NewCohereProvider("test-key", server.URL, "embed-english-v3.0", 3)

	vec, err := p.Embed(context.Background(), "hello world")
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if len(vec) != 3 {
		t.Fatalf("vec length = %d, want 3", len(vec))
	}
	if vec[0] != 0.1 || vec[1] != 0.2 || vec[2] != 0.3 {
		t.Errorf("vec = %v", vec)
	}
	// Dimensions should be updated from response
	if p.Dimensions() != 3 {
		t.Errorf("Dimensions = %d, want 3", p.Dimensions())
	}
}

func TestCohereEmbeddingProvider_EmbedBatch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req cohereEmbedRequest
		_ = json.NewDecoder(r.Body).Decode(&req)

		resp := cohereEmbedResponse{
			Embeddings: make([][]float32, len(req.Texts)),
		}
		for i := range req.Texts {
			resp.Embeddings[i] = []float32{float32(i) * 0.1, float32(i) * 0.2}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	p := NewCohereProvider("test-key", server.URL, "model", 2)

	vecs, err := p.EmbedBatch(context.Background(), []string{"a", "b", "c"})
	if err != nil {
		t.Fatalf("EmbedBatch: %v", err)
	}
	if len(vecs) != 3 {
		t.Fatalf("vecs length = %d, want 3", len(vecs))
	}
}

func TestCohereEmbeddingProvider_EmbedAPIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"rate limited"}`, http.StatusTooManyRequests)
	}))
	defer server.Close()

	p := NewCohereProvider("test-key", server.URL, "model", 2)

	_, err := p.Embed(context.Background(), "hello")
	if err == nil {
		t.Fatal("expected error for API error")
	}
}

func TestCohereEmbeddingProvider_EmbedMismatchedCount(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Return fewer embeddings than requested
		resp := cohereEmbedResponse{
			Embeddings: [][]float32{{0.1, 0.2}},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	p := NewCohereProvider("test-key", server.URL, "model", 2)

	_, err := p.EmbedBatch(context.Background(), []string{"a", "b"})
	if err == nil {
		t.Fatal("expected error for mismatched embedding count")
	}
}

func TestCohereEmbeddingProvider_EmbedEmptyResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := cohereEmbedResponse{
			Embeddings: [][]float32{},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	p := NewCohereProvider("test-key", server.URL, "model", 2)

	_, err := p.Embed(context.Background(), "hello")
	if err == nil {
		t.Fatal("expected error for empty response")
	}
}

func TestCohereEmbeddingProvider_EmbedInvalidJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("{invalid json"))
	}))
	defer server.Close()

	p := NewCohereProvider("test-key", server.URL, "model", 2)

	_, err := p.Embed(context.Background(), "hello")
	if err == nil {
		t.Fatal("expected error for invalid JSON response")
	}
}

func TestCohereEmbeddingProvider_EmbedContextCancelled(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
	}))
	defer server.Close()

	p := NewCohereProvider("test-key", server.URL, "model", 2)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	_, err := p.Embed(ctx, "hello")
	if err == nil {
		t.Fatal("expected error for cancelled context")
	}
}

func TestCohereEmbeddingProvider_EmbedConnectionError(t *testing.T) {
	// Use a server that's already closed
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	server.Close()

	p := NewCohereProvider("test-key", server.URL, "model", 2)

	_, err := p.Embed(context.Background(), "hello")
	if err == nil {
		t.Fatal("expected error for connection failure")
	}
}
