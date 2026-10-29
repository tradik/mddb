package vector

import (
	"fmt"
	"math/rand/v2"
	"reflect"
	"sort"
	"testing"
)

// SRCH-013: the exact prefilter's cost follows the filter, not the collection.

// countVisits records what each filtered flat search looked at, for one test.
func countVisits(t *testing.T) *[]int {
	t.Helper()
	var visits []int
	prefilterVisited = func(n int) { visits = append(visits, n) }
	t.Cleanup(func() { prefilterVisited = nil })
	return &visits
}

// fullScan is the filtered top k the way the full scan finds it: every key
// whose document is allowed, scored.
func fullScan(vi *VectorIndex, collection string, q []float32, k int, allowed map[string]bool) []VectorResult {
	var out []VectorResult
	for key, v := range vi.collections[collection] {
		if allowed[BaseDocID(key)] {
			out = append(out, VectorResult{DocID: key, Score: CosineSimilarity(q, v)})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		return out[i].DocID < out[j].DocID
	})
	return out[:min(k, len(out))]
}

func TestPrefilterVisitsOnlyTheAdmittedDocuments(t *testing.T) {
	r := rand.New(rand.NewPCG(4, 2)) //nolint:gosec // G404: deterministic test data
	vi := NewVectorIndex()
	allowed := map[string]bool{}
	for i := range 20_000 {
		v := make([]float32, 8)
		for j := range v {
			v[j] = r.Float32() - 0.5
		}
		id := fmt.Sprintf("d%05d", i)
		if err := vi.Add("c", id, v); err != nil {
			t.Fatal(err)
		}
		if i%10 == 0 { // 2,000 admitted: several workers
			allowed[id] = true
		}
	}
	visits := countVisits(t)
	q := []float32{1, 0, 0, 0, 0, 0, 0, 0}

	got := vi.SearchWithFilter("c", q, 10, -1, allowed, nil)
	if len(*visits) != 1 || (*visits)[0] != len(allowed) {
		t.Fatalf("the prefilter looked at %v vectors for a filter admitting %d of 20,000", *visits, len(allowed))
	}
	if want := fullScan(vi, "c", q, 10, allowed); !reflect.DeepEqual(got, want) {
		t.Errorf("prefilter = %v,\nexact    = %v", got, want)
	}
}

// A filter admitting at least as many documents as the collection holds is
// cheaper to apply in one pass over the collection.
func TestPrefilterScansWhenTheFilterAdmitsEverything(t *testing.T) {
	vi := NewVectorIndex()
	allowed := map[string]bool{"x": true, "y": true, "z": true}
	for _, id := range []string{"x", "y", "w"} {
		if err := vi.Add("c", id, []float32{1, 0}); err != nil {
			t.Fatal(err)
		}
	}
	visits := countVisits(t)
	got := vi.SearchWithFilter("c", []float32{1, 0}, 5, 0, allowed, nil)
	if want := []VectorResult{{DocID: "x", Score: 1}, {DocID: "y", Score: 1}}; !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
	if !reflect.DeepEqual(*visits, []int{3}) {
		t.Errorf("visited %v, want one scan of 3", *visits)
	}
	if DeduplicateChunkResults(nil) != nil {
		t.Error("nothing to deduplicate came back as something")
	}
}

func TestParallelSettingsFromTheEnvironment(t *testing.T) {
	env := func(workers, minSize string) func(string) string {
		return func(k string) string {
			return map[string]string{"MDDB_VECTOR_PARALLEL_WORKERS": workers, "MDDB_VECTOR_PARALLEL_MIN_SIZE": minSize}[k]
		}
	}
	if w, m := parallelSettings(env("3", "100")); w != 3 || m != 100 {
		t.Errorf("set: %d workers, min size %d", w, m)
	}
	for _, bad := range [][2]string{{"", ""}, {"x", "y"}, {"0", "-1"}, {"257", "2000000000"}} {
		if w, m := parallelSettings(env(bad[0], bad[1])); w < 1 || w > 16 || m != 2048 {
			t.Errorf("%q: %d workers, min size %d, want the defaults", bad, w, m)
		}
	}
}

// A chunked document is reached through its chunk keys, gaps in the numbering
// included, and loses exactly the chunks removed from it.
func TestPrefilterReachesEveryChunkOfAnAdmittedDocument(t *testing.T) {
	vi := NewVectorIndex()
	add := func(key string, v ...float32) {
		t.Helper()
		if err := vi.Add("c", key, v); err != nil {
			t.Fatal(err)
		}
	}
	add("doc#0", 1, 0)
	add("doc#2", 0.9, 0.1) // no doc#1: a gap
	add("doc#2", 0.8, 0.2) // overwrite: still one key
	add("doc", 0, 1)       // the document also has an unchunked vector
	add("other#0", 1, 0)
	add("file#fn", 1, 0) // not a chunk: '#' followed by letters
	for i := range 10 {
		add(fmt.Sprintf("pad%d", i), 0, 1)
	}
	visits := countVisits(t)
	q := []float32{1, 0}

	keys := func(rs []VectorResult) []string {
		out := make([]string, len(rs))
		for i, r := range rs {
			out[i] = r.DocID
		}
		sort.Strings(out)
		return out
	}
	allowed := map[string]bool{"doc": true, "file#fn": true, "other": false, "doc#9": true}
	got := keys(vi.SearchWithFilter("c", q, 10, -1, allowed, nil))
	if want := []string{"doc", "doc#0", "doc#2", "file#fn"}; !reflect.DeepEqual(got, want) {
		t.Errorf("admitted %v, want %v", got, want)
	}
	if (*visits)[0] != 4 {
		t.Errorf("visited %d vectors, want 4 (doc, its two chunks, file#fn)", (*visits)[0])
	}

	vi.Remove("c", "doc#0")
	vi.Remove("c", "doc") // the unchunked key; its chunks stay
	vi.Remove("c", "missing#3")
	got = keys(vi.SearchWithFilter("c", q, 10, -1, map[string]bool{"doc": true}, nil))
	if want := []string{"doc#2"}; !reflect.DeepEqual(got, want) {
		t.Errorf("after removals admitted %v, want %v", got, want)
	}
	vi.Remove("c", "doc#2")
	if _, ok := vi.chunkKeys["c"]["doc"]; ok {
		t.Error("a document with no chunks left is still listed")
	}

	vi.DropCollection("c")
	if _, ok := vi.chunkKeys["c"]; ok {
		t.Error("dropping the collection left its chunk keys")
	}
}
