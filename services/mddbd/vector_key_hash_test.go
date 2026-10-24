package main

import (
	"encoding/json"
	"net/http"
	"testing"
)

// A document key containing '#' — code collections key functions as
// file#function, the form the code_graph documentation itself uses — made
// every vector search return nothing. A chunk's index key is
// "<docID>#<chunk>", and the parent was recovered by cutting at the first
// '#', so `t|a0#f|en#0` became `t|a0`: a document that does not exist, and
// each hit was dropped without an error. vector-stats still counted every
// document as embedded.
func TestVectorSearchFindsDocumentsWhoseKeyContainsAHash(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	s.Embedding = &mockEmbedding{dims: 3, model: "test-model"}
	s.startEmbeddingWorker(s.Embedding)
	defer s.EmbeddingWorker.Stop()
	for _, searcher := range s.VectorSearchers {
		searcher.SetReady() // as startup does once indexes are loaded
	}

	keys := []string{"a0#f", "a1#f", "src/x.c#parse_request"}
	for _, k := range keys {
		doc := addTestDoc(t, s, "t", k, "en", "copyin from user space "+k, nil)
		waitForEmbedding(t, s, "t", doc.ID)
	}

	for algo := range s.VectorSearchers {
		if algo == "quantized" {
			continue // only for collections configured for quantization
		}
		t.Run(algo, func(t *testing.T) {
			rec := doRequest(t, s.handleVectorSearch, map[string]any{
				"collection": "t", "query": "copy from user", "topK": 3, "threshold": -1, "algorithm": algo,
			})
			if rec.Code != http.StatusOK {
				t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
			}
			var resp VectorSearchResponseHTTP
			if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
				t.Fatal(err)
			}
			if resp.Total != len(keys) {
				t.Errorf("total = %d, want %d: documents keyed with '#' are not found", resp.Total, len(keys))
			}
		})
	}
}

// The report's second symptom: hybrid search scored the vector side 0 for the
// same documents.
func TestHybridSearchScoresDocumentsWhoseKeyContainsAHash(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	s.Embedding = &mockEmbedding{dims: 3, model: "test-model"}
	s.startEmbeddingWorker(s.Embedding)
	defer s.EmbeddingWorker.Stop()
	for _, searcher := range s.VectorSearchers {
		searcher.SetReady()
	}
	doc := addTestDoc(t, s, "t", "a0#f", "en", "copyin from user space", nil)
	waitForEmbedding(t, s, "t", doc.ID)

	rec := doRequest(t, s.handleHybridSearch, map[string]any{"collection": "t", "query": "copyin", "topK": 3})
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var resp HybridSearchResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Results) == 0 || resp.Results[0].VectorScore == 0 {
		t.Errorf("vector side of hybrid search found nothing for a key with '#': %+v", resp.Results)
	}
}
