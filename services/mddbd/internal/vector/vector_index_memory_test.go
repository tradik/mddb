package vector

import "testing"

// GO-043: MemoryBytes is what MDDB_VECTOR_RAM_LIMIT_BYTES is compared with.
func TestVectorIndexMemoryBytes(t *testing.T) {
	vi := NewVectorIndex()
	if got := vi.MemoryBytes(); got != 0 {
		t.Fatalf("empty index: %d bytes", got)
	}
	for _, k := range []string{"a#0", "a#1", "b#0"} {
		if err := vi.Add("three", k, []float32{1, 2, 3}); err != nil {
			t.Fatal(err)
		}
	}
	if err := vi.Add("four", "x#0", []float32{1, 2, 3, 4}); err != nil {
		t.Fatal(err)
	}
	// 3 vectors x 3 dims x 4 bytes + 1 x 4 x 4.
	if got := vi.MemoryBytes(); got != 3*3*4+4*4 {
		t.Fatalf("MemoryBytes = %d, want %d", got, 3*3*4+4*4)
	}
	vi.Remove("four", "x#0")
	if got := vi.MemoryBytes(); got != 36 {
		t.Fatalf("after removing the only vector of a collection: %d", got)
	}
}
