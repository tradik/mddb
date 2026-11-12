package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"mddb/internal/embedding"
	pb "mddb/proto"
)

// #415: an Ollama provider configured with an OpenAI model name. Every
// vector-stats surface must name the service that is called (ollama), so the
// mismatch is visible, instead of repeating the model name as the provider.
func mismatchedProvider() embedding.Provider {
	return embedding.NewOllamaProvider("http://127.0.0.1:1", "text-embedding-3-small", 1536)
}

func TestVectorStatsNamesTheProviderNotTheModel(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	s.Embedding = mismatchedProvider()

	rec := httptest.NewRecorder()
	s.handleVectorStats(rec, httptest.NewRequest(http.MethodGet, "/v1/vector-stats", nil))
	var resp map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["provider"] != "ollama" || resp["model"] != "text-embedding-3-small" {
		t.Errorf("REST provider/model = %v/%v, want ollama/text-embedding-3-small", resp["provider"], resp["model"])
	}
}

func TestGRPCAndMCPVectorStatsNameTheProvider(t *testing.T) {
	s, g, cleanup := grpcParityServer(t)
	defer cleanup()
	s.Embedding = mismatchedProvider()

	gr, err := g.VectorStats(context.Background(), &pb.VectorStatsRequest{})
	if err != nil || gr.Provider != "ollama" || gr.Model != "text-embedding-3-small" {
		t.Errorf("gRPC = %+v, %v", gr, err)
	}
	mr, err := (&DirectClient{server: s}).VectorStats(context.Background())
	if err != nil || mr.Provider != "ollama" || mr.Model != "text-embedding-3-small" {
		t.Errorf("MCP = %+v, %v", mr, err)
	}
}
