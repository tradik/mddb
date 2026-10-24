package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	pb "mddb/proto"
)

// #331: deleting a collection left its vectors in the index, in storage and
// in /v1/vector-stats, so a collection written again under the same name
// reported more embedded documents than it held.

func embeddedCollection(t *testing.T, s *Server, collection string, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		doc := addTestDoc(t, s, collection, "doc-"+string(rune('a'+i)), "en", "content about deletion", nil)
		waitForEmbedding(t, s, collection, doc.ID)
	}
}

func vectorStatsFor(t *testing.T, s *Server, collection string) map[string]any {
	t.Helper()
	rec := httptest.NewRecorder()
	s.handleVectorStats(rec, httptest.NewRequest(http.MethodGet, "/v1/vector-stats", nil))
	var resp struct {
		Collections map[string]map[string]any `json:"collections"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	return resp.Collections[collection]
}

func TestDeletingACollectionRemovesEverythingDerivedFromIt(t *testing.T) {
	for _, via := range []string{"http", "grpc", "mcp"} {
		t.Run(via, func(t *testing.T) {
			gs, s, cleanup := newTestGRPCServerFull(t)
			defer cleanup()
			s.Embedding = &mockEmbedding{dims: 3, model: "test-model"}
			s.startEmbeddingWorker(s.Embedding)
			defer s.EmbeddingWorker.Stop()
			embeddedCollection(t, s, "gone", 5)

			switch via {
			case "http":
				rec := doRequest(t, s.handleDeleteCollection, map[string]any{"collection": "gone"})
				if rec.Code != http.StatusOK {
					t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
				}
			case "grpc":
				resp, err := gs.DeleteCollection(context.Background(), &pb.DeleteCollectionRequest{Collection: "gone"})
				if err != nil || resp.DeletedCount != 5 {
					t.Fatalf("DeleteCollection = %v, %v", resp, err)
				}
			case "mcp":
				resp, err := NewDirectClient(s).DeleteCollection(context.Background(), &MCPDeleteCollectionRequest{Collection: "gone"})
				if err != nil || resp.Deleted != 5 {
					t.Fatalf("DeleteCollection = %v, %v", resp, err)
				}
			}

			if stats := vectorStatsFor(t, s, "gone"); stats != nil {
				t.Errorf("vector-stats still reports the deleted collection: %v", stats)
			}
			for name, searcher := range s.VectorSearchers {
				if n := searcher.CollectionSize("gone"); n != 0 {
					t.Errorf("%s still holds %d vectors of the deleted collection", name, n)
				}
			}
			if hits, _ := s.FTSIndex.Search("gone", "deletion", 10); len(hits) != 0 {
				t.Errorf("full-text search still finds %d documents of the deleted collection", len(hits))
			}

			// Written again under the same name, it starts from zero.
			embeddedCollection(t, s, "gone", 1)
			stats := vectorStatsFor(t, s, "gone")
			if stats["embedded_documents"] != float64(1) || stats["total_documents"] != float64(1) {
				t.Errorf("recreated collection: %v, want 1 embedded of 1", stats)
			}
		})
	}
}

func TestDeleteCollectionRequiresAName(t *testing.T) {
	_, s, cleanup := newTestGRPCServerFull(t)
	defer cleanup()
	if _, err := NewDirectClient(s).DeleteCollection(context.Background(), &MCPDeleteCollectionRequest{}); err == nil {
		t.Error("an empty collection name was accepted")
	}
}

// The worker asks before storing: a document deleted while its embedding was
// in flight gets no vectors back.
func TestDocumentExistsReflectsDeletes(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	doc := addTestDoc(t, s, "c", "k", "en", "x", nil)
	ix := serverIndexes{s}
	if !ix.DocumentExists("c", doc.ID) {
		t.Fatal("a stored document reported missing")
	}
	if err := s.deleteDocumentInternal("c", "k", "en"); err != nil {
		t.Fatal(err)
	}
	if ix.DocumentExists("c", doc.ID) {
		t.Error("a deleted document reported present")
	}
}
