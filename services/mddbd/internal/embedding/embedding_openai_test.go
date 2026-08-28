package embedding

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	json "mddb/internal/jsonx"
)

func TestOpenAIEmbeddingProvider_New(t *testing.T) {
	p := NewOpenAIProvider("sk-test", "https://api.openai.com/v1", "text-embedding-3-small", 1536)
	if p.Model() != "text-embedding-3-small" {
		t.Errorf("Model = %q", p.Model())
	}
	if p.Dimensions() != 1536 {
		t.Errorf("Dimensions = %d", p.Dimensions())
	}
}

func TestOpenAIEmbeddingProvider_Embed(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Errorf("Method = %q, want POST", r.Method)
		}
		if r.URL.Path != "/embeddings" {
			t.Errorf("Path = %q, want /embeddings", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer sk-test" {
			t.Errorf("Authorization = %q", r.Header.Get("Authorization"))
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("Content-Type = %q", r.Header.Get("Content-Type"))
		}

		var req openAIEmbeddingRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode request: %v", err)
		}
		if len(req.Input) != 1 {
			t.Errorf("input len = %d, want 1", len(req.Input))
		}

		resp := openAIEmbeddingResponse{
			Data: []openAIEmbeddingData{
				{
					Embedding: []float64{0.1, 0.2, 0.3},
					Index:     0,
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	p := NewOpenAIProvider("sk-test", server.URL, "text-embedding-3-small", 3)

	vec, err := p.Embed(context.Background(), "hello world", RoleDocument)
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if len(vec) != 3 {
		t.Fatalf("vec length = %d, want 3", len(vec))
	}
	if vec[0] != float32(0.1) {
		t.Errorf("vec[0] = %f, want 0.1", vec[0])
	}
}

func TestOpenAIEmbeddingProvider_EmbedBatch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req openAIEmbeddingRequest
		_ = json.NewDecoder(r.Body).Decode(&req)

		resp := openAIEmbeddingResponse{
			Data: make([]openAIEmbeddingData, len(req.Input)),
		}
		for i := range req.Input {
			resp.Data[i] = openAIEmbeddingData{
				Embedding: []float64{float64(i) * 0.1, float64(i) * 0.2},
				Index:     i,
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	p := NewOpenAIProvider("sk-test", server.URL, "model", 2)

	vecs, err := p.EmbedBatch(context.Background(), []string{"a", "b", "c"}, RoleDocument)
	if err != nil {
		t.Fatalf("EmbedBatch: %v", err)
	}
	if len(vecs) != 3 {
		t.Fatalf("vecs length = %d, want 3", len(vecs))
	}
	for i, v := range vecs {
		if len(v) != 2 {
			t.Errorf("vec[%d] length = %d, want 2", i, len(v))
		}
	}
}

func TestOpenAIEmbeddingProvider_EmbedAPIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":{"message":"rate limited"}}`, http.StatusTooManyRequests)
	}))
	defer server.Close()

	p := NewOpenAIProvider("sk-test", server.URL, "model", 2)

	_, err := p.Embed(context.Background(), "hello", RoleDocument)
	if err == nil {
		t.Fatal("expected error for API error")
	}
}

func TestOpenAIEmbeddingProvider_EmbedEmptyResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := openAIEmbeddingResponse{Data: []openAIEmbeddingData{}}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	p := NewOpenAIProvider("sk-test", server.URL, "model", 2)

	_, err := p.Embed(context.Background(), "hello", RoleDocument)
	if err == nil {
		t.Fatal("expected error for empty response")
	}
}

func TestOpenAIEmbeddingProvider_EmbedInvalidJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("{invalid json"))
	}))
	defer server.Close()

	p := NewOpenAIProvider("sk-test", server.URL, "model", 2)

	_, err := p.Embed(context.Background(), "hello", RoleDocument)
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestOpenAIEmbeddingProvider_EmbedContextCancelled(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
	}))
	defer server.Close()

	p := NewOpenAIProvider("sk-test", server.URL, "model", 2)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	_, err := p.Embed(ctx, "hello", RoleDocument)
	if err == nil {
		t.Fatal("expected error for cancelled context")
	}
}

func TestOpenAIEmbeddingProvider_EmbedConnectionError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	server.Close()

	p := NewOpenAIProvider("sk-test", server.URL, "model", 2)

	_, err := p.Embed(context.Background(), "hello", RoleDocument)
	if err == nil {
		t.Fatal("expected error for connection failure")
	}
}

func TestOpenAI_Float64sToFloat32s(t *testing.T) {
	in := []float64{1.0, 2.5, 3.7, 0.0, -1.0}
	out := float64sToFloat32s(in)
	if len(out) != len(in) {
		t.Fatalf("length = %d, want %d", len(out), len(in))
	}
	for i, v := range out {
		if float64(v) != float64(float32(in[i])) {
			t.Errorf("out[%d] = %f, want %f", i, v, float32(in[i]))
		}
	}
}

func TestOpenAI_Float64sToFloat32s_Empty(t *testing.T) {
	out := float64sToFloat32s(nil)
	if len(out) != 0 {
		t.Errorf("length = %d, want 0", len(out))
	}
}

func TestOpenAIEmbeddingProvider_EmbedBatchOrdering(t *testing.T) {
	// Test that ordering is preserved even when indices are out of order
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := openAIEmbeddingResponse{
			Data: []openAIEmbeddingData{
				{Embedding: []float64{0.2}, Index: 1},
				{Embedding: []float64{0.1}, Index: 0},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	p := NewOpenAIProvider("sk-test", server.URL, "model", 1)

	vecs, err := p.EmbedBatch(context.Background(), []string{"first", "second"}, RoleDocument)
	if err != nil {
		t.Fatalf("EmbedBatch: %v", err)
	}
	if len(vecs) != 2 {
		t.Fatalf("vecs len = %d, want 2", len(vecs))
	}
	// Index 0 should have the vector with 0.1
	if vecs[0][0] != float32(0.1) {
		t.Errorf("vecs[0][0] = %f, want 0.1", vecs[0][0])
	}
	// Index 1 should have the vector with 0.2
	if vecs[1][0] != float32(0.2) {
		t.Errorf("vecs[1][0] = %f, want 0.2", vecs[1][0])
	}
}
