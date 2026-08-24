package vector

import (
	"math"
	"testing"
)

// SRCH-003. Packing two dimensions into a byte is the kind of code that works
// for even dimension counts and quietly loses the last value for odd ones, so
// the packing has its own tests before any of the search behaviour.

func trainedSQ4(t *testing.T, vectors map[string][]float32) *SQ4Index {
	t.Helper()
	idx := NewSQ4Index()
	idx.Train("c", vectors)
	return idx
}

func TestSQ4PacksTwoDimensionsPerByte(t *testing.T) {
	idx := trainedSQ4(t, map[string][]float32{
		"a": {0, 0.25, 0.5, 0.75, 1},
		"b": {1, 0.75, 0.5, 0.25, 0},
	})

	// Five dimensions need three bytes: two full pairs and a half.
	if got := idx.CodeBytes("c"); got != 3 {
		t.Errorf("CodeBytes = %d, want 3 for five dimensions", got)
	}

	idx.mu.RLock()
	packed := idx.data["c"].codes["a"]
	idx.mu.RUnlock()

	if len(packed) != 3 {
		t.Fatalf("packed into %d bytes, want 3", len(packed))
	}
	// The odd last dimension must survive: it lands in the low nibble of the
	// final byte, and its high nibble is unused. Vector "a" runs 0 → 1, so
	// dimension 0 is the smallest code and dimension 4 the largest.
	if first, last := codeAt(packed, 0), codeAt(packed, 4); last <= first {
		t.Errorf("dimension 0 encoded to %d and dimension 4 to %d; the tail was dropped", first, last)
	}
}

func TestSQ4RoundTripsMonotonically(t *testing.T) {
	// Sixteen levels across a known range: the codes must increase with the
	// value, which is the only property the distance table depends on.
	vectors := map[string][]float32{}
	for i := 0; i <= 20; i++ {
		vectors[string(rune('a'+i))] = []float32{float32(i) / 20}
	}
	idx := trainedSQ4(t, vectors)

	idx.mu.RLock()
	c := idx.data["c"]
	idx.mu.RUnlock()

	var last uint8
	for i := 0; i <= 20; i++ {
		code := c.quantize(0, float32(i)/20)
		if code < last {
			t.Errorf("value %.2f encoded to %d after %d — codes must not decrease", float32(i)/20, code, last)
		}
		last = code
	}
	if last != sq4Levels-1 {
		t.Errorf("the largest value encoded to %d, want %d", last, sq4Levels-1)
	}
}

func TestSQ4ClipsRatherThanWraps(t *testing.T) {
	// A value far outside the trained range must land at the nearest end. If
	// it wrapped, the most extreme vector in a collection would score as the
	// least extreme — a silent, spectacular ranking bug.
	idx := trainedSQ4(t, map[string][]float32{
		"a": {0}, "b": {0.5}, "c": {1},
	})

	idx.mu.RLock()
	c := idx.data["c"]
	idx.mu.RUnlock()

	if got := c.quantize(0, -1000); got != 0 {
		t.Errorf("a value below the range encoded to %d, want 0", got)
	}
	if got := c.quantize(0, 1000); got != sq4Levels-1 {
		t.Errorf("a value above the range encoded to %d, want %d", got, sq4Levels-1)
	}
}

func TestSQ4ConstantDimensionDoesNotDivideByZero(t *testing.T) {
	// Every vector sharing a value in one dimension gives that dimension a
	// zero range. A scale of 255/0 would be +Inf and every distance NaN.
	idx := trainedSQ4(t, map[string][]float32{
		"a": {0.5, 0.1},
		"b": {0.5, 0.9},
	})

	results := idx.Search("c", []float32{0.5, 0.9}, 2, 0, nil)
	if len(results) == 0 {
		t.Fatal("a constant dimension produced no results at all")
	}
	for _, r := range results {
		if math.IsNaN(float64(r.Score)) || math.IsInf(float64(r.Score), 0) {
			t.Errorf("%s scored %v", r.DocID, r.Score)
		}
	}
}

func TestSQ4FindsTheNearestVector(t *testing.T) {
	idx := trainedSQ4(t, map[string][]float32{
		"north": {0, 1, 0, 0},
		"east":  {1, 0, 0, 0},
		"south": {0, -1, 0, 0},
		"west":  {-1, 0, 0, 0},
	})

	results := idx.Search("c", []float32{0.1, 0.99, 0, 0}, 1, 0, nil)
	if len(results) != 1 {
		t.Fatalf("got %d results, want 1", len(results))
	}
	if results[0].DocID != "north" {
		t.Errorf("nearest to a northward query is %q", results[0].DocID)
	}
}

func TestSQ4RespectsAFilter(t *testing.T) {
	idx := trainedSQ4(t, map[string][]float32{
		"a": {1, 0}, "b": {0, 1}, "c": {0.7, 0.7},
	})

	results := idx.SearchWithFilter("c", []float32{1, 0}, 3, 0,
		map[string]bool{"b": true, "c": true}, nil)

	for _, r := range results {
		if r.DocID == "a" {
			t.Error("a document outside the filter was returned")
		}
	}
	if len(results) == 0 {
		t.Error("the filter excluded everything")
	}
}

func TestSQ4AddAfterTrainingIsSearchable(t *testing.T) {
	// A document written after the index was trained must be findable without
	// waiting for a reindex.
	idx := trainedSQ4(t, map[string][]float32{"a": {1, 0}, "b": {0, 1}})
	idx.Add("c", "late", []float32{0.6, 0.8})

	// Query pointing between the two trained vectors but closest to the new
	// one. "a" and "b" are further away, so if the late document were missing
	// from the codes it would simply never appear.
	results := idx.Search("c", []float32{0.6, 0.8}, 1, 0, nil)
	if len(results) != 1 || results[0].DocID != "late" {
		t.Errorf("results = %v, want the document added after training", results)
	}
}

func TestSQ4RemoveDropsBothCopies(t *testing.T) {
	idx := trainedSQ4(t, map[string][]float32{"a": {1, 0}, "b": {0, 1}})
	idx.Remove("c", "a")

	if got := idx.CollectionSize("c"); got != 1 {
		t.Errorf("CollectionSize = %d after removing one of two", got)
	}
	for _, r := range idx.Search("c", []float32{1, 0}, 5, 0, nil) {
		if r.DocID == "a" {
			t.Error("a removed document is still returned; its code outlived its vector")
		}
	}
}

func TestSQ4EmptyAndDegenerateInputs(t *testing.T) {
	idx := NewSQ4Index()

	// Nothing trained: a search must be empty, not a panic.
	if got := idx.Search("nothing", []float32{1, 0}, 5, 0, nil); got != nil {
		t.Errorf("an untrained index returned %v", got)
	}
	if got := idx.CollectionSize("nothing"); got != 0 {
		t.Errorf("CollectionSize = %d on an untrained index", got)
	}
	if got := idx.CodeBytes("nothing"); got != 0 {
		t.Errorf("CodeBytes = %d on an untrained index", got)
	}

	idx.Train("empty", map[string][]float32{})
	idx.Train("zero-dim", map[string][]float32{"a": {}})

	trained := trainedSQ4(t, map[string][]float32{"a": {1, 0}})
	if got := trained.Search("c", []float32{1, 0}, 0, 0, nil); got != nil {
		t.Errorf("topK of zero returned %v", got)
	}
	// A query shorter than the indexed vectors is a caller error, not a crash.
	if got := trained.Search("c", []float32{1}, 1, 0, nil); len(got) != 1 {
		t.Errorf("a short query returned %d results", len(got))
	}
}

func TestSQ4ReportsItsCollections(t *testing.T) {
	idx := NewSQ4Index()
	idx.Train("beta", map[string][]float32{"a": {1}})
	idx.Train("alpha", map[string][]float32{"a": {1}})

	got := idx.Collections()
	if len(got) != 2 || got[0] != "alpha" || got[1] != "beta" {
		t.Errorf("Collections = %v, want them sorted", got)
	}
}

func TestSQ4NameAndReadiness(t *testing.T) {
	idx := NewSQ4Index()
	if idx.Name() != "sq4" {
		t.Errorf("Name = %q", idx.Name())
	}
	if idx.IsReady() {
		t.Error("a fresh index reports itself ready")
	}
	idx.SetReady()
	if !idx.IsReady() {
		t.Error("SetReady did not take")
	}
}

// The percentile clip is the design decision that separates this from a naive
// min/max quantizer; if it stops working the recall gate is the only thing
// that would notice, and only in aggregate.
func TestSQ4ClipRangeIgnoresOutliers(t *testing.T) {
	sorted := make([]float32, 0, 100)
	for i := 0; i < 99; i++ {
		sorted = append(sorted, float32(i)/100)
	}
	sorted = append(sorted, 1000) // one far outlier

	low, high := clipRange(sorted, 0.01)
	if high >= 1000 {
		t.Errorf("high = %v, the outlier stretched the range", high)
	}
	if low < 0 {
		t.Errorf("low = %v", low)
	}
}
