package main

import (
	"bytes"
	"fmt"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	json "mddb/internal/jsonx"
	"mddb/internal/metrics"
	vec "mddb/internal/vector"
)

// SRCH-023: the HNSW graph and the trained structures are read back from a
// checkpoint at start instead of being built again.

// persistAlgorithms are the indexes the server runs.
func persistSearchers(flat *vec.VectorIndex) map[string]vec.VectorSearcher {
	return map[string]vec.VectorSearcher{
		"flat": flat, "hnsw": vec.NewHNSWIndex(8, 64, 64), "ivf": vec.NewIVFIndex(4, 5),
		"pq": vec.NewPQIndex(4, 16, 5), "opq": vec.NewOPQIndex(4, 16, 3, 2),
		"sq": vec.NewSQIndex(), "sq4": vec.NewSQ4Index(), "bq": vec.NewBQIndex(4),
	}
}

// persistServer is a server with checkpoints on, every index, and n stored
// vectors in each named collection. Its checkpoint files are removed with it.
func persistServer(t *testing.T, sizes map[string]int) *Server {
	t.Helper()
	t.Setenv("MDDB_VECTOR_INDEX_PERSIST", "true")
	s, cleanup := newHandlerTestServer(t)
	t.Cleanup(func() {
		s.stopCheckpoints()
		s.closeCollectionFiles()
		cleanup()
		_ = os.RemoveAll(s.Path + ".ann")
		_ = os.RemoveAll(collectionDir(s.Path))
	})
	s.Metrics = metrics.NewMetrics(true, nil)
	s.VectorSearchers = persistSearchers(s.VectorIndex)
	for coll, n := range sizes {
		putPersistVectors(t, s, coll, 0, n, 1)
	}
	return s
}

// putPersistVectors stores vectors from..to of a collection.
func putPersistVectors(t *testing.T, s *Server, coll string, from, to int, seed int64) {
	t.Helper()
	rng := rand.New(rand.NewSource(seed + int64(from))) // #nosec G404 -- test data
	for i := from; i < to; i++ {
		v := make([]float32, 8)
		for j := range v {
			v[j] = rng.Float32()*2 - 1
		}
		if err := s.VectorStore.Put(coll, fmt.Sprint("k", i), v, "m", "h"); err != nil {
			t.Fatal(err)
		}
	}
}

// loadAndTrain runs the startup load and waits for the training behind it.
func loadAndTrain(t *testing.T, s *Server) {
	t.Helper()
	s.loadVectorIndex()
	select {
	case <-s.vectorTrain.Load().done:
	case <-time.After(30 * time.Second):
		t.Fatal("training did not finish")
	}
}

// restart drops every in-memory index and what the server knew about its
// checkpoints, as a new process starts, after writing them as a clean stop
// does.
func restart(t *testing.T, s *Server, stop bool) {
	t.Helper()
	if stop {
		s.stopCheckpoints()
	}
	s.VectorIndex = vec.NewVectorIndex()
	s.VectorSearchers = persistSearchers(s.VectorIndex)
	s.vectorLoad.persist.mu.Lock()
	s.vectorLoad.persist.state = nil
	s.vectorLoad.persist.loop = nil
	s.vectorLoad.persist.mu.Unlock()
}

// persistQuery is a fixed query.
var persistQuery = []float32{0.1, -0.2, 0.3, 0.4, -0.5, 0.6, 0.7, -0.8}

// searchAll runs the query on every index that keeps a checkpoint. BQ is
// left out: it breaks Hamming ties in map order, so even two rebuilds of it
// can list a tie in either order.
func searchAll(s *Server, coll string) map[string][]vec.VectorResult {
	out := map[string][]vec.VectorResult{}
	for name, sr := range s.VectorSearchers {
		if name != "bq" {
			out[name] = sr.Search(coll, persistQuery, 10, -1, nil)
		}
	}
	return out
}

func TestVectorPersistRestartReadsTheCheckpoint(t *testing.T) {
	s := persistServer(t, map[string]int{"docs": 200})
	loadAndTrain(t, s)
	if st := s.vectorLoad.persist.status("docs"); st == nil || st.Source != annRebuilt || st.Reason != "no checkpoint" {
		t.Fatalf("first load: %+v", st)
	}
	before := searchAll(s, "docs")
	restart(t, s, true)
	if _, err := os.Stat(s.annIndexPath("docs")); err != nil {
		t.Fatalf("no checkpoint after a clean stop: %v", err)
	}

	loadAndTrain(t, s)
	st := s.vectorLoad.persist.status("docs")
	if st == nil || st.Source != annFromDisk || st.CaughtUp != 0 || len(st.Indexes) != 6 {
		t.Fatalf("second load: %+v", st)
	}
	if s.needsTraining("docs") {
		t.Fatal("an index read from disk is trained again")
	}
	if after := searchAll(s, "docs"); !reflect.DeepEqual(before, after) {
		t.Fatalf("answers changed:\n%v\n%v", before, after)
	}
	for _, algo := range []string{"hnsw", "ivf", "pq", "opq", "sq", "sq4"} {
		if _, ran, err := s.readySearcher("docs", algo); err != nil || ran != algo {
			t.Errorf("%s: %v %s", algo, err, ran)
		}
	}
	// Still current: the stop that follows writes nothing.
	at := st.lastWrite
	s.checkpointDue(true)
	if got := s.vectorLoad.persist.status("docs"); !got.lastWrite.Equal(at) {
		t.Fatal("an unchanged checkpoint was written again")
	}
}

func TestVectorPersistCatchesUpLaterWrites(t *testing.T) {
	s := persistServer(t, map[string]int{"docs": 150})
	loadAndTrain(t, s)
	restart(t, s, true)
	putPersistVectors(t, s, "docs", 150, 160, 2)

	loadAndTrain(t, s)
	st := s.vectorLoad.persist.status("docs")
	if st.Source != annFromDisk || st.CaughtUp != 10 {
		t.Fatalf("%+v", st)
	}
	for name, sr := range s.VectorSearchers {
		if sr.CollectionSize("docs") != 160 {
			t.Errorf("%s holds %d", name, sr.CollectionSize("docs"))
		}
	}
	// What was caught up is written at the next opportunity.
	s.checkpointDue(false)
	if got := s.vectorLoad.persist.status("docs"); got.Checkpoint == nil || got.Checkpoint.Error != "" || !got.current {
		t.Fatalf("not written again: %+v", got)
	}
}

// rewriteCheckpoint changes the bytes of a collection's checkpoint.
func rewriteCheckpoint(t *testing.T, s *Server, from, to string, change func([]byte)) {
	t.Helper()
	b, err := os.ReadFile(s.annIndexPath(from)) // #nosec G304 -- a test file
	if err != nil {
		t.Fatal(err)
	}
	change(b)
	if err := os.WriteFile(s.annIndexPath(to), b, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestVectorPersistRebuildsWhatCannotBeVerified(t *testing.T) {
	cases := map[string]struct {
		damage     func(t *testing.T, s *Server)
		collection string
		source     string
		reason     string
	}{
		"corrupt": {func(t *testing.T, s *Server) {
			rewriteCheckpoint(t, s, "docs", "docs", func(b []byte) { b[len(b)/2] ^= 1 })
		}, "docs", annRebuilt, "checksum"},
		"version": {func(t *testing.T, s *Server) {
			rewriteCheckpoint(t, s, "docs", "docs", func(b []byte) { b[8]++ })
		}, "docs", annRebuilt, "another format version"},
		"model": {func(t *testing.T, s *Server) {
			if err := s.VectorStore.NoteProvenance("docs", "other-model", ""); err != nil {
				t.Fatal(err)
			}
		}, "docs", annRebuilt, "embedding model changed"},
		"another collection": {func(t *testing.T, s *Server) {
			putPersistVectors(t, s, "other", 0, 50, 3)
			rewriteCheckpoint(t, s, "docs", "other", func([]byte) {})
		}, "other", annRebuilt, "another collection"},
		"removed vector": {func(t *testing.T, s *Server) {
			if err := s.VectorStore.Delete("docs", "k3"); err != nil {
				t.Fatal(err)
			}
		}, "docs", annPartial, "hnsw: index checkpoint does not match"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			s := persistServer(t, map[string]int{"docs": 120})
			loadAndTrain(t, s)
			restart(t, s, true)
			c.damage(t, s)
			loadAndTrain(t, s)
			st := s.vectorLoad.persist.status(c.collection)
			if st.Source != c.source || !strings.Contains(st.Reason, c.reason) {
				t.Fatalf("%+v", st)
			}
			if got := len(s.VectorSearchers["ivf"].Search(c.collection, persistQuery, 5, -1, nil)); got != 5 {
				t.Fatalf("ivf after the load found %d", got)
			}
		})
	}
}

func TestVectorPersistOffIsThePreviousBehaviour(t *testing.T) {
	s := persistServer(t, map[string]int{"docs": 60})
	t.Setenv("MDDB_VECTOR_INDEX_PERSIST", "false")
	loadAndTrain(t, s)
	if s.checkpointsRunning() || s.vectorLoad.persist.status("docs") != nil || s.annHealth() != nil {
		t.Fatal("checkpoints are off but were used")
	}
	s.stopCheckpoints()
	if _, err := os.Stat(s.Path + ".ann"); !os.IsNotExist(err) {
		t.Fatalf("a checkpoint was written: %v", err)
	}
	s.sweepCheckpoints(nil) // nothing to do
	if summary := s.annSummary(); summary["enabled"] != false {
		t.Fatalf("%v", summary)
	}
}

func TestVectorPersistPerCollectionLayout(t *testing.T) {
	t.Setenv("MDDB_STORAGE_LAYOUT", "per-collection")
	s := persistServer(t, map[string]int{"Docs": 80})
	loadAndTrain(t, s)
	restart(t, s, true)
	want := filepath.Join(collectionDir(s.Path), "%44ocs.db.ann")
	if s.annIndexPath("Docs") != want {
		t.Fatalf("path %s", s.annIndexPath("Docs"))
	}
	if _, err := os.Stat(want); err != nil {
		t.Fatal(err)
	}
	loadAndTrain(t, s)
	if st := s.vectorLoad.persist.status("Docs"); st.Source != annFromDisk {
		t.Fatalf("%+v", st)
	}
}

func TestVectorPersistSweepAndInvalidate(t *testing.T) {
	t.Setenv("MDDB_STORAGE_LAYOUT", "single") // the paths below are this layout's
	s := persistServer(t, map[string]int{"docs": 40})
	loadAndTrain(t, s)
	s.checkpointDue(true)
	dir := s.Path + ".ann"
	other := filepath.Join(collectionDir(s.Path), "x.db.ann")
	_ = os.MkdirAll(collectionDir(s.Path), 0o750)
	for _, f := range []string{filepath.Join(dir, "gone.ann"), filepath.Join(dir, "docs.ann.tmp"), other,
		filepath.Join(collectionDir(s.Path), "keep.db")} {
		if err := os.WriteFile(f, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	s.sweepCheckpoints(map[string]int{"docs": 40})
	names := func(d string) []string {
		var out []string
		entries, _ := os.ReadDir(d)
		for _, e := range entries {
			out = append(out, e.Name())
		}
		return out
	}
	if got := names(dir); !reflect.DeepEqual(got, []string{"docs.ann"}) {
		t.Fatalf("after the sweep: %v", got)
	}
	if got := names(collectionDir(s.Path)); !reflect.DeepEqual(got, []string{"keep.db"}) {
		t.Fatalf("the other layout's files: %v", got)
	}

	t.Setenv("MDDB_STORAGE_LAYOUT", "per-collection")
	_ = os.WriteFile(filepath.Join(collectionDir(s.Path), "docs.db.ann"), []byte("x"), 0o600)
	_ = os.WriteFile(filepath.Join(collectionDir(s.Path), "gone.db.ann"), []byte("x"), 0o600)
	s.sweepCheckpoints(map[string]int{"docs": 40})
	if got := names(collectionDir(s.Path)); !reflect.DeepEqual(got, []string{"docs.db.ann", "keep.db"}) {
		t.Fatalf("per-collection sweep: %v", got)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("the single-file layout's directory was left")
	}

	s.invalidateCheckpoints()
	if got := names(collectionDir(s.Path)); !reflect.DeepEqual(got, []string{"keep.db"}) {
		t.Fatalf("after invalidation: %v", got)
	}
	if s.vectorLoad.persist.status("docs") != nil {
		t.Fatal("the state outlived the invalidation")
	}
	s.Path = ""
	s.invalidateCheckpoints() // an in-memory server has none
}

func TestVectorPersistDroppedCollectionLosesItsFile(t *testing.T) {
	s := persistServer(t, map[string]int{"docs": 40})
	loadAndTrain(t, s)
	s.checkpointDue(true)
	path := s.annIndexPath("docs")
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
	for _, sr := range s.VectorSearchers {
		sr.DropCollection("docs")
	}
	s.checkpointDue(false)
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("a dropped collection kept its checkpoint")
	}
	if s.vectorLoad.persist.status("docs") != nil {
		t.Fatal("and its state")
	}
}

func TestVectorPersistWriteFailureIsReported(t *testing.T) {
	t.Setenv("MDDB_STORAGE_LAYOUT", "single") // the paths below are this layout's
	s := persistServer(t, map[string]int{"docs": 40})
	loadAndTrain(t, s)
	// The checkpoint directory cannot be made: a file has its name.
	if err := os.WriteFile(s.Path+".ann", []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	s.checkpointDue(true)
	st := s.vectorLoad.persist.status("docs")
	if st.Checkpoint == nil || st.Checkpoint.Error == "" || st.current {
		t.Fatalf("%+v", st.Checkpoint)
	}
	_ = os.Remove(s.Path + ".ann")

	// Nothing built any more for it, though the collection is known: the
	// write finds no section and removes the old file.
	s.checkpointDue(true)
	for name, sr := range s.VectorSearchers {
		if _, ok := sr.(vec.Persistable); !ok {
			continue
		}
		s.VectorSearchers[name] = vec.NewVectorIndex()
	}
	s.VectorSearchers["hnsw"] = stampOnly{vec.NewHNSWIndex(8, 64, 64)}
	s.writeCheckpoint("docs", s.vectorLoad.persist.entry("docs"), 1)
	if _, err := os.Stat(s.annIndexPath("docs")); !os.IsNotExist(err) {
		t.Fatal("an empty checkpoint left the old file")
	}
}

// stampOnly is an HNSW index that claims a structure it does not write.
type stampOnly struct{ *vec.HNSWIndex }

func (stampOnly) PersistStamp(string) uint64 { return 1 }

func TestVectorPersistDue(t *testing.T) {
	t.Setenv("MDDB_VECTOR_INDEX_CHECKPOINT_INTERVAL_SEC", "60")
	t.Setenv("MDDB_VECTOR_INDEX_CHECKPOINT_CHANGES", "100")
	now := time.Now()
	cases := []struct {
		st    annCollection
		stamp uint64
		want  bool
	}{
		{annCollection{}, 1, true},
		{annCollection{lastWrite: now.Add(-time.Hour), current: true, stamp: 1}, 2, true},
		{annCollection{lastWrite: now.Add(-time.Second), current: true, stamp: 1}, 500, false},
		{annCollection{lastWrite: now.Add(-30 * time.Second), current: true, stamp: 1}, 500, true},
		{annCollection{lastWrite: now.Add(-30 * time.Second), current: true, stamp: 1}, 50, false},
		{annCollection{lastWrite: now.Add(-30 * time.Second), lastDuration: 2 * time.Second}, 1, false},
		{annCollection{lastWrite: now.Add(-30 * time.Second)}, 1, true},
	}
	for i, c := range cases {
		if got := c.st.due(c.stamp, now); got != c.want {
			t.Errorf("case %d: due %v", i, got)
		}
	}
}

func TestVectorPersistBackgroundLoop(t *testing.T) {
	old := annCheckpointTick
	annCheckpointTick = 10 * time.Millisecond
	t.Cleanup(func() { annCheckpointTick = old })
	s := persistServer(t, map[string]int{"docs": 40})
	loadAndTrain(t, s)
	if !s.checkpointsRunning() {
		t.Fatal("no background writer")
	}
	s.startCheckpoints() // a second load finds it running
	deadline := time.Now().Add(10 * time.Second)
	for {
		if st := s.vectorLoad.persist.status("docs"); st != nil && st.Checkpoint != nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the background writer wrote nothing")
		}
		time.Sleep(5 * time.Millisecond)
	}
	steps := s.shutdownSteps()
	found := false
	for _, st := range steps {
		found = found || st.name == "vector-checkpoints"
	}
	if !found {
		t.Fatal("no shutdown step")
	}
	s.stopCheckpoints()
	if s.checkpointsRunning() {
		t.Fatal("still running")
	}
}

func TestVectorPersistWaitsForTheLoad(t *testing.T) {
	s := persistServer(t, map[string]int{"docs": 10})
	s.checkpointDue(true) // no load yet: nothing to write
	if _, err := os.Stat(s.Path + ".ann"); !os.IsNotExist(err) {
		t.Fatal("written before the load")
	}
	if s.restoreIndexes("docs", nil) != nil {
		t.Fatal("restored nothing into something")
	}
}

func TestVectorPersistStatsAndHealth(t *testing.T) {
	// The single layout's stats snapshot lists the collection at once; the
	// fields under test are the same in both.
	t.Setenv("MDDB_STORAGE_LAYOUT", "single")
	s := persistServer(t, map[string]int{"docs": 40})
	loadAndTrain(t, s)
	rec := httptest.NewRecorder()
	s.handleVectorStats(rec, httptest.NewRequest(http.MethodGet, "/v1/vector-stats", nil))
	var stats struct {
		IndexPersistence map[string]any `json:"indexPersistence"`
		Collections      map[string]struct {
			IndexPersistence *annCollection `json:"indexPersistence"`
			HNSWTombstones   *int           `json:"hnswTombstones"`
		} `json:"collections"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &stats); err != nil {
		t.Fatal(err)
	}
	c := stats.Collections["docs"]
	if stats.IndexPersistence["enabled"] != true || c.IndexPersistence == nil || c.IndexPersistence.Source != annRebuilt || c.HNSWTombstones == nil {
		t.Fatalf("%s", rec.Body.String())
	}
	h := s.annHealth()
	if h["rebuilt"] != 1 || h["fromDisk"] != 0 {
		t.Fatalf("%v", h)
	}
	rec = httptest.NewRecorder()
	s.handleHealth(rec, httptest.NewRequest(http.MethodGet, "/health", nil))
	if !strings.Contains(rec.Body.String(), `"vectorIndexCheckpoints"`) {
		t.Fatalf("%s", rec.Body.String())
	}
	delete(s.VectorSearchers, "hnsw")
	info := map[string]any{}
	s.addPersistenceStats("none", info)
	if len(info) != 0 {
		t.Fatalf("%v", info)
	}
}

func TestVectorCompactEndpoint(t *testing.T) {
	s := persistServer(t, map[string]int{"docs": 40})
	loadAndTrain(t, s)
	for i := range 5 {
		s.VectorSearchers["hnsw"].Remove("docs", fmt.Sprint("k", i))
	}
	call := func(method, body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		s.handleVectorCompact(rec, httptest.NewRequest(method, "/v1/vector-compact", bytes.NewBufferString(body)))
		return rec
	}
	if rec := call(http.MethodGet, ""); rec.Code != http.StatusMethodNotAllowed || rec.Header().Get("Allow") != "POST" {
		t.Fatalf("GET: %d", rec.Code)
	}
	if rec := call(http.MethodPost, "{"); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad json: %d", rec.Code)
	}
	if rec := call(http.MethodPost, "{}"); rec.Code != http.StatusBadRequest {
		t.Fatalf("no collection: %d", rec.Code)
	}
	if rec := call(http.MethodPost, `{"collection":"nope"}`); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown collection: %d", rec.Code)
	}
	rec := call(http.MethodPost, `{"collection":"docs"}`)
	var out vectorCompactResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || rec.Code != http.StatusOK {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	if out.Collection != "docs" || out.Vectors != 35 || out.TombstonesBefore != 5 || out.TombstonesAfter != 0 {
		t.Fatalf("%+v", out)
	}
	delete(s.VectorSearchers, "hnsw")
	if rec := call(http.MethodPost, `{"collection":"docs"}`); rec.Code != http.StatusNotFound {
		t.Fatalf("no hnsw: %d", rec.Code)
	}
}

func TestVectorPersistTrainsOnlyWhatWasNotRead(t *testing.T) {
	s := persistServer(t, map[string]int{"docs": 120})
	loadAndTrain(t, s)
	restart(t, s, true)
	// PQ now splits vectors in two: its codebooks of four do not fit.
	s.VectorSearchers["pq"] = vec.NewPQIndex(2, 16, 5)
	loadAndTrain(t, s)
	st := s.vectorLoad.persist.status("docs")
	if st.Source != annPartial || st.Indexes["pq"] != annRebuilt || st.Indexes["ivf"] != annFromDisk {
		t.Fatalf("%+v", st)
	}
	if !s.VectorSearchers["pq"].(vec.BuildReporter).Trained("docs") {
		t.Fatal("pq was not trained")
	}
	if s.VectorSearchers["ivf"].(vec.Persistable).PersistStamp("docs") != 1 {
		t.Fatal("ivf was trained again")
	}
}

func TestVectorPersistSmallParts(t *testing.T) {
	s := persistServer(t, map[string]int{"docs": 5})
	if s.annHealth() != nil {
		t.Fatal("health before any load")
	}
	if got := joinReasons(joinReasons("", "a"), "b"); got != "a; b" {
		t.Fatal(got)
	}
	closeStorage(t, s, "docs")
	if got := s.annFingerprint("docs"); got != "" {
		t.Fatalf("fingerprint of an unreadable store: %q", got)
	}
}
