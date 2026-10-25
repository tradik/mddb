package main

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	json "mddb/internal/jsonx"
	vec "mddb/internal/vector"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// #346: the vector index load reports progress, loads small collections
// first, and serves each collection as soon as its own vectors are in.

func TestVectorLoadStateOrderReadinessAndProgress(t *testing.T) {
	l := newVectorLoadState(map[string]int{"big": 3000, "small": 2, "mid": 10})
	if got := l.order(); strings.Join(got, ",") != "small,mid,big" {
		t.Errorf("order %v, want smallest first", got)
	}
	if !l.collectionReady("no-vectors") {
		t.Error("a collection with no vectors should be searchable at once")
	}
	if l.collectionReady("small") {
		t.Error("ready before it was loaded")
	}
	l.begin("small")
	l.add(2)
	l.finishCollection("small")
	if !l.collectionReady("small") || l.collectionReady("big") {
		t.Error("per-collection readiness")
	}
	l.started = time.Now().Add(-10 * time.Second)
	l.lastLog = time.Now().Add(-2 * time.Minute) // the next add logs
	l.begin("big")
	l.add(1000)
	p := l.progress()
	if p == nil || p.CollectionsDone != 1 || p.CollectionsTotal != 3 || p.VectorsTotal != 3012 ||
		p.VectorsDone != 1002 || p.Current != "big" || p.EtaSeconds <= 0 || len(p.ReadyCollections) != 1 {
		t.Fatalf("progress %+v", p)
	}
	l.finish()
	if l.progress() != nil || !l.collectionReady("big") {
		t.Error("after finish: no progress, everything ready")
	}
	if (newVectorLoadState(map[string]int{})).progress().Percent != 0 {
		t.Error("empty load")
	}
}

func TestReadySearcherAndRefusals(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	flat := vec.NewVectorIndex() // not ready
	s.VectorSearchers = map[string]vec.VectorSearcher{"flat": flat, "hnsw": vec.NewHNSWIndex(8, 0, 32)}

	if _, _, err := s.readySearcher("c", "nope"); !errors.Is(err, errUnknownAlgorithm) {
		t.Errorf("unknown algorithm: %v", err)
	}
	var loading *vectorLoadingError
	if _, _, err := s.readySearcher("c", "hnsw"); !errors.As(err, &loading) || loading.Progress != nil {
		t.Errorf("before any load: %v", err)
	}

	l := newVectorLoadState(map[string]int{"big": 100})
	s.vectorLoad.Store(l)
	if sr, algo, err := s.readySearcher("empty", "hnsw"); err != nil || algo != "hnsw" || sr == nil {
		t.Errorf("a collection without vectors: %v %s", err, algo)
	}
	_, _, err := s.readySearcher("big", "hnsw")
	if !errors.As(err, &loading) || loading.Progress == nil || !strings.Contains(err.Error(), "big") {
		t.Fatalf("loading refusal: %v", err)
	}

	rec := httptest.NewRecorder()
	s.writeVectorUnavailable(rec, err, "flat")
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if rec.Code != http.StatusServiceUnavailable || rec.Header().Get("Retry-After") == "" || body["progress"] == nil || body["collection"] != "big" {
		t.Errorf("503: %d %q %s", rec.Code, rec.Header().Get("Retry-After"), rec.Body.String())
	}
	rec = httptest.NewRecorder()
	s.writeVectorUnavailable(rec, errUnknownAlgorithm, "flat")
	if rec.Code != http.StatusBadRequest {
		t.Errorf("unknown algorithm answered %d", rec.Code)
	}
	if status.Code(vectorSearcherStatus(err)) != codes.Unavailable || status.Code(vectorSearcherStatus(errUnknownAlgorithm)) != codes.InvalidArgument {
		t.Error("gRPC statuses")
	}
	l.add(40)
	l.started = time.Now().Add(-time.Second)
	if _, _, err := s.readySearcher("big", "hnsw"); !strings.Contains(err.Error(), "%") {
		t.Errorf("the message should carry the percentage: %v", err)
	}

	// Its own vectors in: searchable through the requested algorithm even
	// though the searcher as a whole is not ready.
	l.finishCollection("big")
	if _, algo, err := s.readySearcher("big", "hnsw"); err != nil || algo != "hnsw" {
		t.Errorf("after its load: %v %s", err, algo)
	}
}

func TestLoadVectorIndexReportsAndFinishes(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	s.VectorIndex = vec.NewVectorIndex()
	s.VectorSearchers = newVectorSearchers(s.VectorIndex, s.QuantizedVecIndex, 10)
	for _, c := range []struct {
		coll string
		n    int
	}{{"small", 2}, {"big", 1200}} {
		for i := 0; i < c.n; i++ {
			if err := s.VectorStore.Put(c.coll, genID(c.coll, string(rune('a'+i%26))+strings.Repeat("x", i/26), "en"),
				[]float32{float32(i%7) + 1, float32(i%5) + 1, 1}, "m", "h"); err != nil {
				t.Fatal(err)
			}
		}
	}
	s.loadVectorIndex()
	l := s.vectorLoad.Load()
	if l == nil || l.progress() != nil || !l.collectionReady("big") || !l.collectionReady("small") {
		t.Fatal("the load did not finish")
	}
	if l.vectorsDone != l.vectorsTotal {
		t.Errorf("counted %d of %d vectors", l.vectorsDone, l.vectorsTotal)
	}
	if _, _, err := s.readySearcher("big", "hnsw"); err != nil {
		t.Errorf("after the load: %v", err)
	}

	// While a load is running, /health and /v1/vector-stats show it.
	s.vectorLoad.Store(newVectorLoadState(map[string]int{"x": 5}))
	s.Ready = true
	rec := httptest.NewRecorder()
	s.handleHealth(rec, httptest.NewRequest(http.MethodGet, "/health", nil))
	if !strings.Contains(rec.Body.String(), `"vectorIndex"`) {
		t.Errorf("health: %s", rec.Body.String())
	}
	rec = httptest.NewRecorder()
	s.handleVectorStats(rec, httptest.NewRequest(http.MethodGet, "/v1/vector-stats", nil))
	if !strings.Contains(rec.Body.String(), `"indexProgress"`) {
		t.Errorf("vector-stats: %s", rec.Body.String())
	}
}

func TestVectorLoadEdges(t *testing.T) {
	l := newVectorLoadState(map[string]int{"b": 1, "a": 1})
	if got := l.order(); strings.Join(got, ",") != "a,b" {
		t.Errorf("ties go by name: %v", got)
	}
	if msg := (&vectorLoadingError{Collection: "c"}).Error(); !strings.Contains(msg, `"c"`) || strings.Contains(msg, "%") {
		t.Errorf("message without progress: %s", msg)
	}

	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	flat := vec.NewVectorIndex()
	flat.SetReady()
	s.VectorSearchers = map[string]vec.VectorSearcher{"flat": flat, "hnsw": vec.NewHNSWIndex(8, 0, 32)}
	s.vectorLoad.Store(newVectorLoadState(map[string]int{"big": 10}))
	if _, algo, err := s.readySearcher("big", "hnsw"); err != nil || algo != "flat" {
		t.Errorf("flat fallback: %v %s", err, algo)
	}

	rec := httptest.NewRecorder()
	s.writeVectorUnavailable(rec, &vectorLoadingError{Collection: "c"}, "flat")
	if rec.Header().Get("Retry-After") != "30" {
		t.Errorf("Retry-After without an estimate: %q", rec.Header().Get("Retry-After"))
	}
	rec = httptest.NewRecorder()
	s.writeVectorUnavailable(rec, &vectorLoadingError{Collection: "c", Progress: &vectorLoadProgress{EtaSeconds: 42}}, "flat")
	if rec.Header().Get("Retry-After") != "42" {
		t.Errorf("Retry-After from the estimate: %q", rec.Header().Get("Retry-After"))
	}
}
