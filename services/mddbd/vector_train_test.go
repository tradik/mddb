package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	json "mddb/internal/jsonx"
	vec "mddb/internal/vector"
	"mddb/proto"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// GO-047: the trainable indexes train behind the load; flat and HNSW answer
// for a collection as soon as its vectors are in.

// gatedIVF is an IVF index whose training, once done, waits at a gate before
// it returns — the window in which the index is trained but the trainer has
// not finished the collection.
type gatedIVF struct {
	*vec.IVFIndex
	started chan string
	gate    chan struct{}
}

func newGatedIVF() *gatedIVF {
	return &gatedIVF{IVFIndex: vec.NewIVFIndex(4, 5), started: make(chan string, 8), gate: make(chan struct{})}
}

func (g *gatedIVF) Train(collection string, vectors map[string][]float32) {
	g.IVFIndex.Train(collection, vectors)
	g.started <- collection
	<-g.gate
}

// trainServer is a server with flat, hnsw and a gated ivf over the given
// collections' stored vectors.
func trainServer(t *testing.T, sizes map[string]int) (*Server, *gatedIVF) {
	t.Helper()
	s, cleanup := newHandlerTestServer(t)
	t.Cleanup(cleanup)
	s.VectorIndex = vec.NewVectorIndex()
	ivf := newGatedIVF()
	s.VectorSearchers = map[string]vec.VectorSearcher{
		"flat": s.VectorIndex, "hnsw": vec.NewHNSWIndex(8, 0, 32), "ivf": ivf, "bq": vec.NewBQIndex(0),
	}
	for coll, n := range sizes {
		for i := 0; i < n; i++ {
			if err := s.VectorStore.Put(coll, genID(coll, fmt.Sprint("k", i), "en"),
				[]float32{float32(i%7) + 1, float32(i%5) + 1, float32(i%3) + 1}, "m", "h"); err != nil {
				t.Fatal(err)
			}
		}
	}
	return s, ivf
}

// loadReturns runs the startup load and fails if it waits for training.
func loadReturns(t *testing.T, s *Server) {
	t.Helper()
	done := make(chan struct{})
	go func() { s.loadVectorIndex(); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the load waited for the training")
	}
}

func waitStarted(t *testing.T, g *gatedIVF, want string) {
	t.Helper()
	select {
	case got := <-g.started:
		if got != want {
			t.Fatalf("trained %q first, want %q", got, want)
		}
	case <-time.After(10 * time.Second):
		t.Fatalf("%s never trained", want)
	}
}

func noneStarted(t *testing.T, g *gatedIVF) {
	t.Helper()
	select {
	case got := <-g.started:
		t.Fatalf("%s trained, it should not have been", got)
	case <-time.After(50 * time.Millisecond):
	}
}

func stopTrainer(t *testing.T, s *Server) {
	t.Helper()
	tr := s.vectorTrain.Load()
	tr.stop()
	select {
	case <-tr.done:
	default:
		t.Fatal("the trainer is still running")
	}
}

func TestTrainingDoesNotBlockFlatAndHNSW(t *testing.T) {
	s, ivf := trainServer(t, map[string]int{"docs": 40})
	loadReturns(t, s)
	waitStarted(t, ivf, "docs")

	q := []float32{1, 1, 1}
	for _, algo := range []string{"flat", "hnsw", "bq"} {
		sr, ran, err := s.readySearcher("docs", algo)
		if err != nil || ran != algo {
			t.Fatalf("%s while ivf trains: %v %s", algo, err, ran)
		}
		if len(sr.Search("docs", q, 3, 0, nil)) == 0 {
			t.Errorf("%s found nothing", algo)
		}
	}

	// auto: ivf is trained but its collection is not finished — not a
	// candidate, and explain says why.
	in := s.planInput(planRequest{Collection: "docs"})
	if in.Built["ivf"] || strings.Join(in.Training, ",") != "ivf" {
		t.Errorf("plan input while training: built %v training %v", in.Built, in.Training)
	}
	ps, err := s.planSearch(planRequest{Collection: "docs", Algorithm: AlgorithmAuto, SearchTopK: 3})
	if err != nil || (ps.Algorithm != "flat" && ps.Algorithm != "hnsw") {
		t.Fatalf("auto while training: %v %s", err, ps.Algorithm)
	}
	if !strings.Contains(strings.Join(ps.Plan.Reasons, "\n"), "ivf: training") {
		t.Errorf("explain does not say ivf is training: %v", ps.Plan.Reasons)
	}

	close(ivf.gate)
	stopTrainer(t, s)
	if _, ran, err := s.readySearcher("docs", "ivf"); err != nil || ran != "ivf" {
		t.Errorf("ivf after training: %v %s", err, ran)
	}
	if in := s.planInput(planRequest{Collection: "docs"}); !in.Built["ivf"] || len(in.Training) != 0 {
		t.Errorf("plan input after training: %+v", in)
	}
}

func TestSecondCollectionReadyWithoutFirstsTraining(t *testing.T) {
	s, ivf := trainServer(t, map[string]int{"a": 5, "b": 30})
	loadReturns(t, s)
	waitStarted(t, ivf, "a")

	l := s.vectorLoad.Load()
	if !l.collectionReady("b") {
		t.Fatal("b waits for a's training")
	}
	if _, ran, err := s.readySearcher("b", "hnsw"); err != nil || ran != "hnsw" {
		t.Fatalf("hnsw on b: %v %s", err, ran)
	}
	for _, c := range []string{"a", "b"} {
		var loading *vectorLoadingError
		if _, _, err := s.readySearcher(c, "ivf"); !errors.As(err, &loading) || loading.Algorithm != "ivf" {
			t.Errorf("ivf on %s before training: %v", c, err)
		}
	}

	// Both reported, with what is training now.
	rec := httptest.NewRecorder()
	s.handleVectorStats(rec, httptest.NewRequest(http.MethodGet, "/v1/vector-stats", nil))
	var stats struct {
		IndexTraining *vectorTrainingStatus `json:"indexTraining"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &stats)
	if st := stats.IndexTraining; st == nil || st.Current != "a" || strings.Join(st.Pending, ",") != "a,b" ||
		strings.Join(st.Indexes, ",") != "ivf" {
		t.Errorf("vector-stats: %s", rec.Body.String())
	}
	if got := s.IndexingStatus("").TrainingCollections; strings.Join(got, ",") != "a,b" {
		t.Errorf("indexing-status: %v", got)
	}
	if got := s.IndexingStatus("b").TrainingCollections; strings.Join(got, ",") != "b" {
		t.Errorf("indexing-status for b: %v", got)
	}
	if got := s.IndexingStatus("other").TrainingCollections; got != nil {
		t.Errorf("indexing-status for a collection not training: %v", got)
	}
	g := &GRPCServer{server: s}
	gs, err := g.GetIndexingStatus(context.Background(), &proto.IndexingStatusRequest{})
	if err != nil || strings.Join(gs.TrainingCollections, ",") != "a,b" {
		t.Errorf("gRPC indexing status: %v %v", err, gs)
	}
	a := &GraphQLAdapter{server: s, mcp: NewDirectClient(s)}
	if qs, err := a.IndexingStatus(context.Background(), nil); err != nil || strings.Join(qs.TrainingCollections, ",") != "a,b" {
		t.Errorf("GraphQL indexing status: %v %+v", err, qs)
	}

	close(ivf.gate)
	waitStarted(t, ivf, "b")
	stopTrainer(t, s)
	if s.vectorTrainingStatus() != nil || s.trainingCollections("") != nil {
		t.Error("training reported after it ended")
	}
}

func TestExplicitIVFBeforeAndAfterTraining(t *testing.T) {
	s, ivf := trainServer(t, map[string]int{"docs": 40})
	loadReturns(t, s)
	waitStarted(t, ivf, "docs")

	search := func() *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		body := `{"collection":"docs","queryVector":[1,1,1],"algorithm":"ivf","topK":3}`
		s.handleVectorSearch(rec, httptest.NewRequest(http.MethodPost, "/v1/vector-search", bytes.NewBufferString(body)))
		return rec
	}
	rec := search()
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), `\"docs\"`) ||
		!strings.Contains(rec.Body.String(), "training") || rec.Header().Get("Retry-After") == "" {
		t.Fatalf("ivf before training: %d %s", rec.Code, rec.Body.String())
	}
	g := &GRPCServer{server: s}
	_, err := g.VectorSearch(context.Background(), &proto.VectorSearchRequest{Collection: "docs", QueryVector: []float32{1, 1, 1}, Algorithm: "ivf", TopK: 3})
	if status.Code(err) != codes.Unavailable || !strings.Contains(err.Error(), `"docs"`) {
		t.Errorf("gRPC ivf before training: %v", err)
	}

	close(ivf.gate)
	stopTrainer(t, s)
	if rec := search(); rec.Code != http.StatusOK {
		t.Fatalf("ivf after training: %d %s", rec.Code, rec.Body.String())
	}
}

func TestShutdownDuringTrainingStopsTheTrainer(t *testing.T) {
	s, ivf := trainServer(t, map[string]int{"a": 5, "b": 30})
	loadReturns(t, s)
	waitStarted(t, ivf, "a")

	var step *shutdownStep
	for _, st := range s.shutdownSteps() {
		if st.name == "vector-training" {
			step = &st
		}
	}
	if step == nil {
		t.Fatal("no vector-training shutdown step")
	}
	stopped := make(chan struct{})
	go func() { step.fn(); close(stopped) }()
	select {
	case <-stopped:
		t.Fatal("the step returned while a training was still running")
	case <-time.After(50 * time.Millisecond):
	}
	close(ivf.gate)
	select {
	case <-stopped:
	case <-time.After(10 * time.Second):
		t.Fatal("the step did not return")
	}
	noneStarted(t, ivf) // b was cancelled, not trained
	select {
	case <-s.vectorTrain.Load().done:
	default:
		t.Fatal("the trainer goroutine is still running")
	}
}

func TestReloadWaitsForThePreviousTrainer(t *testing.T) {
	s, ivf := trainServer(t, nil)
	vecs := map[string][]float32{"x": {1, 2, 3}, "y": {3, 2, 1}}
	first := s.startVectorTraining(1)
	first.enqueue("a", vecs) // left open: the load is still running
	waitStarted(t, ivf, "a")

	second := s.startVectorTraining(1)
	second.enqueue("b", vecs)
	second.close()
	noneStarted(t, ivf) // the first is still training
	close(ivf.gate)
	waitStarted(t, ivf, "b")
	stopTrainer(t, s)
	<-first.done
}

func TestDroppedCollectionIsNotTrained(t *testing.T) {
	s, ivf := trainServer(t, map[string]int{"a": 5, "b": 30})
	loadReturns(t, s)
	waitStarted(t, ivf, "a")

	s.dropCollectionVectors("a") // training now: finishes, then forgotten
	s.dropCollectionVectors("b") // queued: never trained
	if !s.vectorTrain.Load().training("a") || s.vectorTrain.Load().training("b") {
		t.Error("drop: the current collection stays until done, a queued one goes")
	}
	close(ivf.gate)
	<-s.vectorTrain.Load().done // the load closed the queue: b is dequeued and skipped
	noneStarted(t, ivf)
	if s.vectorTrain.Load().training("a") {
		t.Error("a still training after it finished")
	}
}

func TestVectorTrainerEdges(t *testing.T) {
	s, _ := trainServer(t, nil)
	if s.vectorTraining("c", s.VectorIndex) || s.vectorTrainingStatus() != nil || s.trainingCollections("") != nil {
		t.Error("no trainer: nothing is training")
	}

	// A disk-only collection (no float32 vectors) is not queued.
	tr := s.startVectorTraining(1)
	tr.enqueue("disk", nil)
	if tr.training("disk") || s.vectorTrainingStatus() != nil {
		t.Error("a collection without vectors to train waits for nothing")
	}
	tr.close()
	stopTrainer(t, s)

	// Cancelled with a job queued: whichever the select takes, nothing is
	// trained.
	for i := 0; i < 64; i++ {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		tr := &vectorTrainer{jobs: make(chan vectorTrainJob, 1), cancel: cancel, done: make(chan struct{}),
			train: func(string, map[string][]float32) { t.Error("trained after cancel") }}
		tr.enqueue("c", map[string][]float32{"x": {1}})
		tr.run(ctx, nil)
	}

	msg := (&vectorLoadingError{Collection: "c", Algorithm: "pq"}).Error()
	if !strings.Contains(msg, `"c"`) || !strings.Contains(msg, "pq") || !strings.Contains(msg, "training") {
		t.Errorf("training refusal: %s", msg)
	}
}
