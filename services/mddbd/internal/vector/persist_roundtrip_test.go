package vector

import (
	"errors"
	"fmt"
	"math/rand"
	"path/filepath"
	"reflect"
	"testing"
)

// Round trips of every persistable index through a checkpoint file
// (SRCH-023): what is read back answers exactly as what was written.

// persistVecs returns n reproducible vectors of dim values, keyed "d<i>".
func persistVecs(n, dim int, seed int64) map[string][]float32 {
	rng := rand.New(rand.NewSource(seed)) // #nosec G404 -- test data
	out := make(map[string][]float32, n)
	for i := range n {
		v := make([]float32, dim)
		for j := range v {
			v[j] = rng.Float32()*2 - 1
		}
		out[fmt.Sprintf("d%d", i)] = v
	}
	return out
}

// persistIndexes is one fresh instance of every index the server runs.
func persistIndexes() map[string]VectorSearcher {
	return map[string]VectorSearcher{
		"flat": NewVectorIndex(),
		"hnsw": NewHNSWIndex(8, 64, 64),
		"ivf":  NewIVFIndex(4, 5),
		"pq":   NewPQIndex(4, 16, 5),
		"opq":  NewOPQIndex(4, 16, 3, 2),
		"sq":   NewSQIndex(),
		"sq4":  NewSQ4Index(),
		"bq":   NewBQIndex(4),
	}
}

// persistFill adds vectors to every index and trains the trainable ones.
func persistFill(t *testing.T, idx map[string]VectorSearcher, coll string, vecs map[string][]float32) {
	t.Helper()
	for _, s := range idx {
		for id, v := range vecs {
			if err := s.Add(coll, id, v); err != nil {
				t.Fatalf("%s add %s: %v", s.Name(), id, err)
			}
		}
		if tr, ok := s.(Trainable); ok {
			tr.Train(coll, vecs)
		}
	}
}

// persistWrite checkpoints idx and reads the file back.
func persistWrite(t *testing.T, idx map[string]VectorSearcher, coll string) (*Checkpoint, CheckpointInfo, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "sub", coll+".ann")
	info, err := WriteCheckpoint(path, CheckpointMeta{Collection: coll, Fingerprint: "m\x00v", Vectors: 7, CreatedUnix: 42}, idx)
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	cp, err := ReadCheckpoint(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	return cp, info, path
}

func TestPersistRoundTripAnswersAlike(t *testing.T) {
	const coll = "c"
	vecs := persistVecs(400, 16, 1)
	orig := persistIndexes()
	persistFill(t, orig, coll, vecs)
	// A removed document leaves an HNSW tombstone, which is written in full.
	for _, s := range orig {
		s.Remove(coll, "d0")
	}
	stored := map[string][]float32{}
	for id, v := range vecs {
		if id != "d0" {
			stored[id] = v
		}
	}

	cp, info, _ := persistWrite(t, orig, coll)
	want := []string{"hnsw", "ivf", "opq", "pq", "sq", "sq4"}
	if !reflect.DeepEqual(info.Sections, want) || info.Bytes <= 0 {
		t.Fatalf("sections %v bytes %d", info.Sections, info.Bytes)
	}
	if cp.Meta != (CheckpointMeta{Collection: coll, Fingerprint: "m\x00v", Vectors: 7, CreatedUnix: 42}) {
		t.Fatalf("meta %+v", cp.Meta)
	}
	if cp.Has("flat") || !cp.Has("hnsw") {
		t.Fatal("Has")
	}

	loaded := persistIndexes()
	for _, name := range want {
		missing, err := cp.Restore(name, loaded[name], stored)
		if err != nil || len(missing) != 0 {
			t.Fatalf("%s restore: %v missing %d", name, err, len(missing))
		}
	}
	for _, name := range want {
		persistSameAnswers(t, name, orig[name], loaded[name], coll)
	}
	if got := loaded["hnsw"].(*HNSWIndex).DeletedSince(coll); got != 1 {
		t.Fatalf("tombstones %d", got)
	}
}

// persistSameAnswers checks that a restored index answers as the original
// does, and is trained when the original was.
func persistSameAnswers(t *testing.T, name string, orig, loaded VectorSearcher, coll string) {
	t.Helper()
	for qid, q := range persistVecs(20, 16, 2) {
		a := orig.Search(coll, q, 10, -1, nil)
		b := loaded.Search(coll, q, 10, -1, nil)
		if !reflect.DeepEqual(a, b) {
			t.Fatalf("%s query %s: %v != %v", name, qid, a, b)
		}
	}
	if r, ok := loaded.(BuildReporter); ok && !r.Trained(coll) {
		t.Fatalf("%s not trained after restore", name)
	}
}

func TestPersistRestoreReportsWhatTheCheckpointMissed(t *testing.T) {
	const coll = "c"
	vecs := persistVecs(300, 8, 3)
	orig := persistIndexes()
	persistFill(t, orig, coll, vecs)
	cp, _, _ := persistWrite(t, orig, coll)

	stored := map[string][]float32{}
	for id, v := range vecs {
		stored[id] = v
	}
	for id, v := range persistVecs(5, 8, 4) {
		stored["late-"+id] = v
	}
	// One replaced vector: the trained indexes file it again; the graph
	// cannot, so it is stale.
	stored["d1"] = persistVecs(1, 8, 5)["d0"]
	for _, name := range []string{"ivf", "pq", "opq"} {
		missing, err := cp.Restore(name, persistIndexes()[name], stored)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if len(missing) != 6 || missing[0] != "d1" {
			t.Fatalf("%s missing %v", name, missing)
		}
	}
	// The scalar indexes code every stored vector of their dimension again;
	// only one of another dimension is left for Add to refuse.
	stored["odd"] = []float32{1}
	for _, name := range []string{"sq", "sq4"} {
		missing, err := cp.Restore(name, persistIndexes()[name], stored)
		if err != nil || !reflect.DeepEqual(missing, []string{"odd"}) {
			t.Fatalf("%s: %v missing %v", name, err, missing)
		}
	}
	if _, err := cp.Restore("hnsw", NewHNSWIndex(8, 64, 64), stored); !errors.Is(err, ErrCheckpointStale) {
		t.Fatalf("hnsw: %v", err)
	}
}

func TestPersistRestoreRefusesAnotherCorpus(t *testing.T) {
	const coll = "c"
	orig := persistIndexes()
	persistFill(t, orig, coll, persistVecs(100, 8, 6))
	cp, _, _ := persistWrite(t, orig, coll)
	other := persistVecs(100, 8, 7)
	for _, name := range []string{"ivf", "pq", "opq", "sq", "sq4", "hnsw"} {
		if _, err := cp.Restore(name, persistIndexes()[name], other); !errors.Is(err, ErrCheckpointStale) {
			t.Fatalf("%s: %v", name, err)
		}
	}
	// Another dimension is another corpus too.
	wide := persistVecs(100, 12, 6)
	if _, err := cp.Restore("ivf", NewIVFIndex(4, 5), wide); !errors.Is(err, ErrCheckpointStale) {
		t.Fatalf("dimension: %v", err)
	}
}

func TestPersistRestoreRefusesOtherSettingsAndSections(t *testing.T) {
	const coll = "c"
	vecs := persistVecs(200, 8, 8)
	orig := persistIndexes()
	persistFill(t, orig, coll, vecs)
	cp, _, _ := persistWrite(t, orig, coll)

	cases := []struct {
		name    string
		section string
		idx     VectorSearcher
		want    error
	}{
		{"hnsw m", "hnsw", NewHNSWIndex(9, 64, 64), ErrCheckpointStale},
		{"pq subspaces", "pq", NewPQIndex(2, 16, 5), ErrCheckpointStale},
		{"opq subspaces", "opq", NewOPQIndex(2, 16, 3, 2), ErrCheckpointStale},
		{"pq codebook", "pq", NewPQIndex(4, 8, 5), ErrCheckpointCorrupt},
		{"hnsw from trained", "ivf", NewHNSWIndex(8, 64, 64), ErrCheckpointCorrupt},
		{"pq from graph", "hnsw", NewPQIndex(4, 16, 5), ErrCheckpointCorrupt},
		{"opq from graph", "hnsw", NewOPQIndex(4, 16, 3, 2), ErrCheckpointCorrupt},
		{"opq from pq", "pq", NewOPQIndex(4, 16, 3, 2), ErrCheckpointCorrupt},
		{"ivf from pq", "pq", NewIVFIndex(4, 5), ErrCheckpointCorrupt},
		{"sq from sq4", "sq4", NewSQIndex(), ErrCheckpointCorrupt},
		{"sq4 from ivf", "ivf", NewSQ4Index(), ErrCheckpointCorrupt},
		{"sq4 from sq", "sq", NewSQ4Index(), ErrCheckpointCorrupt},
	}
	for _, c := range cases {
		_, err := c.idx.(Persistable).persistRestore(coll, cp.sections[c.section], vecs)
		if !errors.Is(err, c.want) {
			t.Errorf("%s: %v", c.name, err)
		}
	}
	if _, err := cp.Restore("flat", NewVectorIndex(), vecs); err == nil {
		t.Error("flat keeps no checkpoint")
	}
	if _, err := cp.Restore("bq", NewBQIndex(4), vecs); err == nil {
		t.Error("bq keeps no checkpoint")
	}
	delete(cp.sections, "sq")
	if _, err := cp.Restore("sq", NewSQIndex(), vecs); !errors.Is(err, ErrCheckpointStale) {
		t.Errorf("no section: %v", err)
	}
}

func TestPersistStampsGrow(t *testing.T) {
	const coll = "c"
	idx := persistIndexes()
	for _, s := range idx {
		if p, ok := s.(Persistable); ok && p.PersistStamp(coll) != 0 {
			t.Fatalf("%s: stamp of an unknown collection", s.Name())
		}
	}
	persistFill(t, idx, coll, persistVecs(50, 8, 9))
	for _, s := range idx {
		if p, ok := s.(Persistable); ok && p.PersistStamp(coll) == 0 {
			t.Fatalf("%s: no stamp", s.Name())
		}
	}
	h := idx["hnsw"].(*HNSWIndex)
	before := h.PersistStamp(coll)
	h.Remove(coll, "d3")
	h.Remove(coll, "nope")
	if h.PersistStamp(coll) != before+1 {
		t.Fatal("remove did not move the stamp once")
	}
	h.DropCollection(coll)
	if h.PersistStamp(coll) != 0 {
		t.Fatal("drop kept the stamp")
	}
}

func TestPersistNothingBuiltWritesNoFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.ann")
	idx := persistIndexes()
	// Vectors, but nothing trained and no graph for this collection.
	_ = idx["flat"].Add("c", "a", []float32{1, 2})
	_ = idx["ivf"].Add("c", "a", []float32{1, 2})
	_ = idx["hnsw"].Add("other", "a", []float32{1, 2})
	info, err := WriteCheckpoint(path, CheckpointMeta{Collection: "c"}, idx)
	if err != nil || info.Bytes != 0 || len(info.Sections) != 0 {
		t.Fatalf("%+v %v", info, err)
	}
	if _, err := ReadCheckpoint(path); err == nil {
		t.Fatal("a file was left")
	}
}
