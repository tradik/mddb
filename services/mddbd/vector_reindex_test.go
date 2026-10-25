package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"mddb/internal/embedding"
	json "mddb/internal/jsonx"
	vec "mddb/internal/vector"
	pb "mddb/proto"

	bolt "go.etcd.io/bbolt"
)

// #347: a reindex can run as a job, and HTTP, gRPC and MCP share one
// implementation.

// gatedEmbedding embeds once the test lets it, so a job can be caught running.
type gatedEmbedding struct {
	mockEmbedding
	gate chan struct{}
}

func (g *gatedEmbedding) Embed(ctx context.Context, text string, role embedding.Role) ([]float32, error) {
	select {
	case <-g.gate:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return g.mockEmbedding.Embed(ctx, text, role)
}

func reindexServer(t *testing.T) *Server {
	t.Helper()
	s, cleanup := newHandlerTestServer(t)
	t.Cleanup(cleanup)
	for i := 0; i < 3; i++ {
		addTestDoc(t, s, "docs", "d"+string(rune('a'+i)), "en", "a document about reindexing number "+string(rune('a'+i)), nil)
	}
	return s
}

func TestAsyncReindexJob(t *testing.T) {
	s := reindexServer(t)
	gate := make(chan struct{})
	s.Embedding = &gatedEmbedding{mockEmbedding: mockEmbedding{dims: 3, model: "m"}, gate: gate}

	rec := doRequest(t, s.handleVectorReindex, map[string]any{"collection": "docs", "force": true, "async": true})
	var job vectorReindexJob
	_ = json.Unmarshal(rec.Body.Bytes(), &job)
	if rec.Code != http.StatusAccepted || job.ID == "" || job.Status != "running" ||
		rec.Header().Get("Location") != "/v1/vector-reindex/jobs/"+job.ID {
		t.Fatalf("start: %d %s", rec.Code, rec.Body.String())
	}

	// A second reindex of the same collection while one runs is refused.
	rec = doRequest(t, s.handleVectorReindex, map[string]any{"collection": "docs", "async": true})
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), job.ID) {
		t.Errorf("concurrent reindex: %d %s", rec.Code, rec.Body.String())
	}

	close(gate)
	deadline := time.Now().Add(5 * time.Second)
	var got vectorReindexJob
	for time.Now().Before(deadline) {
		rec = httptest.NewRecorder()
		s.handleVectorReindexJobs(rec, httptest.NewRequest(http.MethodGet, "/v1/vector-reindex/jobs/"+job.ID, nil))
		_ = json.Unmarshal(rec.Body.Bytes(), &got)
		if got.Status != "running" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if got.Status != "completed" || got.Result.Embedded != 3 || got.Result.Total != 3 || got.Done != 3 || got.Percent != 100 || got.FinishedAt == nil {
		t.Fatalf("finished job: %+v", got)
	}

	rec = httptest.NewRecorder()
	s.handleVectorReindexJobs(rec, httptest.NewRequest(http.MethodGet, "/v1/vector-reindex/jobs", nil))
	if !strings.Contains(rec.Body.String(), job.ID) {
		t.Errorf("job list: %s", rec.Body.String())
	}
	rec = httptest.NewRecorder()
	s.handleVectorReindexJobs(rec, httptest.NewRequest(http.MethodGet, "/v1/vector-reindex/jobs/nope", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("unknown job: %d", rec.Code)
	}
}

func TestReindexJobFailureAndLimit(t *testing.T) {
	s := reindexServer(t)
	job, err := s.startReindexJob("", false) // no collection: the job fails
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if j, _ := s.reindexJobs.get(job.ID); j.Status != "running" {
			if j.Status != "failed" || j.Error == "" {
				t.Errorf("failed job: %+v", j)
			}
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	// Finished jobs past the limit are dropped, oldest first.
	var jobs reindexJobs
	for i := 0; i < maxReindexJobs+5; i++ {
		j, _ := jobs.start("c"+string(rune('a'+i%26))+string(rune('a'+i/26)), false)
		jobs.update(j.ID, func(x *vectorReindexJob) { x.Status = "completed" })
	}
	if n := len(jobs.list()); n > maxReindexJobs+1 {
		t.Errorf("%d jobs kept, limit %d", n, maxReindexJobs)
	}
}

func TestSyncReindexAndTransportsShareOneImplementation(t *testing.T) {
	gs, s, cleanup := newTestGRPCServerFull(t)
	defer cleanup()
	s.Embedding = &mockEmbedding{dims: 3, model: "m"}
	addTestDoc(t, s, "docs", "a", "en", "first document", nil)
	addTestDoc(t, s, "docs", "b", "en", "second document", nil)

	rec := doRequest(t, s.handleVectorReindex, map[string]any{"collection": "docs", "force": true})
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"embedded":2`) {
		t.Fatalf("sync reindex: %d %s", rec.Code, rec.Body.String())
	}
	for _, body := range []map[string]any{{"collection": ""}, {"collection": "docs"}} {
		srv := s
		if body["collection"] == "docs" {
			srv = &Server{} // no embedding provider
		}
		if rec := doRequest(t, srv.handleVectorReindex, body); rec.Code != http.StatusBadRequest {
			t.Errorf("%v: %d", body, rec.Code)
		}
	}

	// gRPC used to store one whole-document vector and skip the indexes; it
	// now writes chunk vectors the index can search at once.
	resp, err := gs.VectorReindex(context.Background(), &pb.VectorReindexRequest{Collection: "docs", Force: true})
	if err != nil || resp.GetEmbedded() != 2 {
		t.Fatalf("gRPC reindex: %v %v", resp, err)
	}
	records, _ := s.VectorStore.LoadCollection("docs")
	for key := range records {
		if !strings.Contains(key, "#") {
			t.Errorf("gRPC reindex stored a whole-document vector %q", key)
		}
	}
	m, err := NewDirectClient(s).VectorReindex(context.Background(), &MCPVectorReindexRequest{Collection: "docs", Force: true})
	if err != nil || m.Embedded != 2 {
		t.Errorf("MCP reindex: %+v %v", m, err)
	}
	if _, err := NewDirectClient(s).VectorReindex(context.Background(), &MCPVectorReindexRequest{}); err == nil {
		t.Error("MCP reindex without a collection")
	}
}

// fixedEmbedding returns err, or vector for every text.
type fixedEmbedding struct {
	mockEmbedding
	vector []float32
	err    error
}

func (f *fixedEmbedding) Embed(context.Context, string, embedding.Role) ([]float32, error) {
	return f.vector, f.err
}

func TestReindexOutcomes(t *testing.T) {
	s := reindexServer(t)
	s.Embedding = &mockEmbedding{dims: 3, model: "m"}
	if res, err := s.reindexVectors(context.Background(), "docs", true, nil); err != nil || res.Embedded != 3 {
		t.Fatalf("first: %+v %v", res, err)
	}
	// Unchanged content is skipped without force.
	if res, _ := s.reindexVectors(context.Background(), "docs", false, nil); res.Skipped != 3 || res.Embedded != 0 {
		t.Errorf("unchanged: %+v", res)
	}
	// A failing provider is counted per document, with the reason.
	s.Embedding = &fixedEmbedding{mockEmbedding: mockEmbedding{dims: 3, model: "m"}, err: context.DeadlineExceeded}
	if res, _ := s.reindexVectors(context.Background(), "docs", true, nil); res.Failed != 3 || len(res.Errors) != 3 {
		t.Errorf("provider errors: %+v", res)
	}
	// An empty vector is stored but refused by the indexes (#252): failed.
	s.Embedding = &fixedEmbedding{mockEmbedding: mockEmbedding{dims: 3, model: "m"}, vector: []float32{}}
	if res, _ := s.reindexVectors(context.Background(), "docs", true, nil); res.Failed != 3 || !strings.Contains(strings.Join(res.Errors, " "), "index") {
		t.Errorf("index refusals: %+v", res)
	}

	// Quantized and disk-only collections take their own storage paths.
	s.Embedding = &mockEmbedding{dims: 3, model: "m"}
	for name, cfg := range map[string]*CollectionConfig{
		"q8":   {Type: "default", Quantization: "int8"},
		"disk": {Type: "default", Quantization: "int8", DiskOnlyVectors: true},
	} {
		_ = s.CollectionManager.Set(name, cfg)
		addTestDoc(t, s, name, "k", "en", "quantized content", nil)
		if res, err := s.reindexVectors(context.Background(), name, true, nil); err != nil || res.Embedded != 1 {
			t.Errorf("%s: %+v %v", name, res, err)
		}
	}
	// An empty document is skipped.
	addTestDoc(t, s, "blank", "k", "en", "x", nil)
	r := vectorReindexResult{}
	s.reindexOne(context.Background(), "blank", "id", "", ChunkModeProse, true, true, 1500, vec.QuantNone, false, &r)
	if r.Skipped != 1 {
		t.Errorf("empty content: %+v", r)
	}

	// Past the job limit with every job still running, nothing is dropped.
	var jobs reindexJobs
	for i := 0; i < maxReindexJobs+2; i++ {
		_, _ = jobs.start("r"+string(rune('a'+i%26))+string(rune('a'+i/26)), false)
	}
	if n := len(jobs.list()); n != maxReindexJobs+2 {
		t.Errorf("running jobs were dropped: %d", n)
	}
}

func TestReindexErrorPaths(t *testing.T) {
	s := reindexServer(t)
	s.Embedding = nil
	if _, err := s.reindexVectors(context.Background(), "docs", true, nil); err != errNoEmbeddingProvider {
		t.Errorf("no provider: %v", err)
	}
	s.Embedding = &mockEmbedding{dims: 3, model: "m"}

	// Content that yields no chunk is skipped.
	r := vectorReindexResult{}
	s.reindexOne(context.Background(), "docs", "ws", " \n\t ", ChunkModeProse, true, true, 1500, vec.QuantNone, false, &r)
	if r.Skipped+r.Embedded+r.Failed != 1 {
		t.Errorf("whitespace content: %+v", r)
	}

	// An unreadable record is passed over; the rest are reindexed.
	_ = s.DBUpdateC("docs", func(tx *bolt.Tx) error {
		return tx.Bucket(s.BucketNames.Docs).Put([]byte("doc|docs|broken"), []byte("not a document"))
	})
	if res, err := s.reindexVectors(context.Background(), "docs", true, nil); err != nil || res.Embedded != 3 {
		t.Errorf("with a broken record: %+v %v", res, err)
	}

	// A store that cannot write fails each document with the reason.
	other, cleanup := newHandlerTestServer(t)
	cleanup() // closes its database
	s.VectorStore = other.VectorStore
	if res, _ := s.reindexVectors(context.Background(), "docs", true, nil); res.Failed != 3 || !strings.Contains(strings.Join(res.Errors, " "), "store") {
		t.Errorf("store errors: %+v", res)
	}

	// No docs bucket: nothing to do. A closed database: an error.
	empty := &Server{DB: other.DB, BucketNames: s.BucketNames, Embedding: s.Embedding}
	if _, err := empty.reindexVectors(context.Background(), "docs", true, nil); err == nil {
		t.Error("reading a closed database should fail")
	}
}
