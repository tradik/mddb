package embedding

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	json "mddb/internal/jsonx"
)

func TestVoyageEmbeddingProvider_New(t *testing.T) {
	p := NewVoyageProvider("vo-test", "https://api.voyageai.com/v1", "voyage-3", 1024)
	if p.Model() != "voyage-3" {
		t.Errorf("Model = %q", p.Model())
	}
	if p.Dimensions() != 1024 {
		t.Errorf("Dimensions = %d", p.Dimensions())
	}
}

func TestVoyageEmbeddingProvider_Embed(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Errorf("Method = %q, want POST", r.Method)
		}
		if r.URL.Path != "/embeddings" {
			t.Errorf("Path = %q, want /embeddings", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer vo-test" {
			t.Errorf("Authorization = %q", r.Header.Get("Authorization"))
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("Content-Type = %q", r.Header.Get("Content-Type"))
		}

		var req voyageEmbeddingRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode request: %v", err)
		}
		if len(req.Input) != 1 {
			t.Errorf("input len = %d, want 1", len(req.Input))
		}

		resp := voyageEmbeddingResponse{
			Data: []voyageEmbeddingData{
				{
					Embedding: []float64{0.4, 0.5, 0.6},
					Index:     0,
				},
			},
			Model: "voyage-3",
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	p := NewVoyageProvider("vo-test", server.URL, "voyage-3", 3)

	vec, err := p.Embed(context.Background(), "hello world")
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if len(vec) != 3 {
		t.Fatalf("vec length = %d, want 3", len(vec))
	}
	if vec[0] != float32(0.4) {
		t.Errorf("vec[0] = %f, want 0.4", vec[0])
	}
	// Dimensions should be updated from response
	if p.Dimensions() != 3 {
		t.Errorf("Dimensions = %d, want 3", p.Dimensions())
	}
}

func TestVoyageEmbeddingProvider_EmbedBatch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req voyageEmbeddingRequest
		_ = json.NewDecoder(r.Body).Decode(&req)

		resp := voyageEmbeddingResponse{
			Data: make([]voyageEmbeddingData, len(req.Input)),
		}
		for i := range req.Input {
			resp.Data[i] = voyageEmbeddingData{
				Embedding: []float64{float64(i) * 0.1, float64(i) * 0.2},
				Index:     i,
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	p := NewVoyageProvider("vo-test", server.URL, "model", 2)

	vecs, err := p.EmbedBatch(context.Background(), []string{"a", "b", "c"})
	if err != nil {
		t.Fatalf("EmbedBatch: %v", err)
	}
	if len(vecs) != 3 {
		t.Fatalf("vecs length = %d, want 3", len(vecs))
	}
}

func TestVoyageEmbeddingProvider_EmbedAPIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
	}))
	defer server.Close()

	p := NewVoyageProvider("bad-key", server.URL, "model", 2)

	_, err := p.Embed(context.Background(), "hello")
	if err == nil {
		t.Fatal("expected error for API error")
	}
}

func TestVoyageEmbeddingProvider_EmbedEmptyResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := voyageEmbeddingResponse{Data: []voyageEmbeddingData{}}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	p := NewVoyageProvider("vo-test", server.URL, "model", 2)

	_, err := p.Embed(context.Background(), "hello")
	if err == nil {
		t.Fatal("expected error for empty response")
	}
}

func TestVoyageEmbeddingProvider_EmbedInvalidJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("{invalid"))
	}))
	defer server.Close()

	p := NewVoyageProvider("vo-test", server.URL, "model", 2)

	_, err := p.Embed(context.Background(), "hello")
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestVoyageEmbeddingProvider_EmbedContextCancelled(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
	}))
	defer server.Close()

	p := NewVoyageProvider("vo-test", server.URL, "model", 2)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	_, err := p.Embed(ctx, "hello")
	if err == nil {
		t.Fatal("expected error for cancelled context")
	}
}

func TestVoyageEmbeddingProvider_EmbedConnectionError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	server.Close()

	p := NewVoyageProvider("vo-test", server.URL, "model", 2)

	_, err := p.Embed(context.Background(), "hello")
	if err == nil {
		t.Fatal("expected error for connection failure")
	}
}

func TestVoyageEmbeddingProvider_EmbedBatchOrdering(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := voyageEmbeddingResponse{
			Data: []voyageEmbeddingData{
				{Embedding: []float64{0.2}, Index: 1},
				{Embedding: []float64{0.1}, Index: 0},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	p := NewVoyageProvider("vo-test", server.URL, "model", 1)

	vecs, err := p.EmbedBatch(context.Background(), []string{"first", "second"})
	if err != nil {
		t.Fatalf("EmbedBatch: %v", err)
	}
	if vecs[0][0] != float32(0.1) {
		t.Errorf("vecs[0][0] = %f, want 0.1", vecs[0][0])
	}
	if vecs[1][0] != float32(0.2) {
		t.Errorf("vecs[1][0] = %f, want 0.2", vecs[1][0])
	}
}
