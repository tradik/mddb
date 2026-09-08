package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	vector "mddb/internal/vector"
)

// What a caller is told when a write lands without its vector (#232).
//
// The documents ARE stored, so `failed` stays 0 and must: the write succeeded.
// What was missing is any way to learn that the collection is now partly
// invisible to vector and hybrid search — a 4,000-document import answered
// `{"added":4000,"failed":0}` with 1,002 embedded.

// stallEmbeddingWorker gives a server an embedding queue with one slot, no
// workers draining it and no patience, so the second document of any batch is
// abandoned. That is the shape of a real overrun, arrived at in a millisecond
// rather than by writing four thousand documents.
func stallEmbeddingWorker(t *testing.T, srv *Server) {
	t.Helper()
	t.Setenv("MDDB_EMBEDDING_QUEUE_WAIT", "0")

	vs := vector.NewVectorStore(srv.DB)
	if err := vs.EnsureBucket(); err != nil {
		t.Fatalf("EnsureBucket: %v", err)
	}
	index := vector.NewVectorIndex()
	index.SetReady()

	provider := &embWorkerMockProvider{model: "test-model", dims: 3, vector: []float32{0.1, 0.2, 0.3}}
	srv.EmbeddingWorker = NewEmbeddingWorker(provider, vs, index, 1)
	srv.VectorStore = vs
	srv.VectorIndex = index
}

func TestABatchReportsTheDocumentsItCouldNotEmbed(t *testing.T) {
	srv, cleanup := newHandlerTestServer(t)
	defer cleanup()
	stallEmbeddingWorker(t, srv)

	body := `{"collection":"c","documents":[
		{"key":"a","lang":"en","contentMd":"first body"},
		{"key":"b","lang":"en","contentMd":"second body"},
		{"key":"d","lang":"en","contentMd":"third body"}]}`
	rec := httptest.NewRecorder()
	srv.handleAddBatch(rec, httptest.NewRequest(http.MethodPost, "/v1/add-batch", strings.NewReader(body)))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	var resp AddBatchHTTPResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if resp.Added != 3 || resp.Failed != 0 {
		t.Errorf("added/failed = %d/%d, want 3/0 — the documents were stored", resp.Added, resp.Failed)
	}
	if resp.EmbeddingDropped != 2 {
		t.Errorf("embeddingDropped = %d, want 2 (one slot took the first)", resp.EmbeddingDropped)
	}
}

// A healthy import must look exactly as it did before: the field is omitted
// rather than reported as zero, so nothing downstream has to learn about it.
func TestAHealthyBatchSaysNothingAboutEmbeddings(t *testing.T) {
	srv, cleanup := newHandlerTestServer(t)
	defer cleanup()

	body := `{"collection":"c","documents":[{"key":"a","lang":"en","contentMd":"body"}]}`
	rec := httptest.NewRecorder()
	srv.handleAddBatch(rec, httptest.NewRequest(http.MethodPost, "/v1/add-batch", strings.NewReader(body)))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "embeddingDropped") {
		t.Errorf("a clean import mentions embeddingDropped: %s", rec.Body.String())
	}
}

// The counter has to be readable after the fact too: whoever finds "embedded
// is lower than total" days later did not see the response, and needs to know
// whether the queue is still draining or gave up.
func TestVectorStatsReportsTheQueueAndWhatItLost(t *testing.T) {
	srv, cleanup := newHandlerTestServer(t)
	defer cleanup()
	stallEmbeddingWorker(t, srv)

	for i := 0; i < 4; i++ {
		_ = srv.EmbeddingWorker.Enqueue(EmbeddingJob{Collection: "c", DocID: "d", ContentMD: "x"})
	}

	rec := httptest.NewRecorder()
	srv.handleVectorStats(rec, httptest.NewRequest(http.MethodGet, "/v1/vector-stats", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}

	var resp struct {
		Queue struct {
			Size    int    `json:"size"`
			Depth   int    `json:"depth"`
			Dropped uint64 `json:"dropped"`
			Wait    string `json:"wait"`
		} `json:"queue"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if resp.Queue.Dropped != 3 {
		t.Errorf("queue.dropped = %d, want 3", resp.Queue.Dropped)
	}
	if resp.Queue.Size != 1 || resp.Queue.Depth != 1 {
		t.Errorf("queue size/depth = %d/%d, want 1/1", resp.Queue.Size, resp.Queue.Depth)
	}
	if resp.Queue.Wait != "0s" {
		t.Errorf("queue.wait = %q, want the configured 0s", resp.Queue.Wait)
	}
}
