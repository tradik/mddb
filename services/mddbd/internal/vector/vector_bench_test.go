package vector

import (
	"fmt"
	"math"
	"math/rand"
	"os"
	"testing"

	bolt "go.etcd.io/bbolt"
)

// generateRandomVector creates a normalized random vector of given dimensions.
func generateRandomVector(dims int, rng *rand.Rand) []float32 {
	vec := make([]float32, dims)
	var norm float32
	for i := range vec {
		v := float32(rng.NormFloat64())
		vec[i] = v
		norm += v * v
	}
	norm = float32(math.Sqrt(float64(norm)))
	if norm > 0 {
		for i := range vec {
			vec[i] /= norm
		}
	}
	return vec
}

// populateIndex fills a VectorIndex with n random vectors of given dimensions.
func populateIndex(idx *VectorIndex, collection string, n, dims int) {
	rng := rand.New(rand.NewSource(42)) //nolint:gosec // G404: math/rand fine for test data generation
	for i := 0; i < n; i++ {
		if err := idx.Add(collection, fmt.Sprintf("doc-%d", i), generateRandomVector(dims, rng)); err != nil {
			panic(err) // helper without a testing handle; a valid vector cannot fail
		}
	}
}

// --- Benchmarks for VectorIndex.Search ---

func BenchmarkVectorSearch_1K_768(b *testing.B) {
	benchmarkSearch(b, 1000, 768)
}

func BenchmarkVectorSearch_1K_1536(b *testing.B) {
	benchmarkSearch(b, 1000, 1536)
}

func BenchmarkVectorSearch_5K_768(b *testing.B) {
	benchmarkSearch(b, 5000, 768)
}

func BenchmarkVectorSearch_5K_1536(b *testing.B) {
	benchmarkSearch(b, 5000, 1536)
}

func BenchmarkVectorSearch_10K_768(b *testing.B) {
	benchmarkSearch(b, 10000, 768)
}

func BenchmarkVectorSearch_10K_1536(b *testing.B) {
	benchmarkSearch(b, 10000, 1536)
}

func BenchmarkVectorSearch_50K_768(b *testing.B) {
	benchmarkSearch(b, 50000, 768)
}

func BenchmarkVectorSearch_50K_1536(b *testing.B) {
	benchmarkSearch(b, 50000, 1536)
}

func benchmarkSearch(b *testing.B, numDocs, dims int) {
	idx := NewVectorIndex()
	idx.SetReady()
	populateIndex(idx, "bench", numDocs, dims)

	rng := rand.New(rand.NewSource(99)) //nolint:gosec // G404: math/rand fine for test data generation
	query := generateRandomVector(dims, rng)

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = idx.Search("bench", query, 10, 0.0, nil)
	}
}

// --- Benchmarks for VectorIndex.SearchWithFilter ---

func BenchmarkVectorSearchFilter_10K_768_10pct(b *testing.B) {
	benchmarkSearchWithFilter(b, 10000, 768, 0.10)
}

func BenchmarkVectorSearchFilter_10K_768_50pct(b *testing.B) {
	benchmarkSearchWithFilter(b, 10000, 768, 0.50)
}

func benchmarkSearchWithFilter(b *testing.B, numDocs, dims int, filterRatio float64) {
	idx := NewVectorIndex()
	idx.SetReady()
	populateIndex(idx, "bench", numDocs, dims)

	rng := rand.New(rand.NewSource(99)) //nolint:gosec // G404: math/rand fine for test data generation
	query := generateRandomVector(dims, rng)

	// Build filter set
	allowed := make(map[string]bool)
	filterCount := int(float64(numDocs) * filterRatio)
	for i := 0; i < filterCount; i++ {
		allowed[fmt.Sprintf("doc-%d", i)] = true
	}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = idx.SearchWithFilter("bench", query, 10, 0.0, allowed, nil)
	}
}

// --- Benchmarks for CosineSimilarity ---

func BenchmarkCosineSimilarity_768(b *testing.B) {
	benchmarkCosine(b, 768)
}

func BenchmarkCosineSimilarity_1536(b *testing.B) {
	benchmarkCosine(b, 1536)
}

func BenchmarkCosineSimilarity_1024(b *testing.B) {
	benchmarkCosine(b, 1024)
}

func benchmarkCosine(b *testing.B, dims int) {
	rng := rand.New(rand.NewSource(42)) //nolint:gosec // G404: math/rand fine for test data generation
	a := generateRandomVector(dims, rng)
	vecB := generateRandomVector(dims, rng)

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = CosineSimilarity(a, vecB)
	}
}

// --- Benchmarks for VectorIndex.Add ---

func BenchmarkVectorAdd_768(b *testing.B) {
	benchmarkAdd(b, 768)
}

func BenchmarkVectorAdd_1536(b *testing.B) {
	benchmarkAdd(b, 1536)
}

func benchmarkAdd(b *testing.B, dims int) {
	idx := NewVectorIndex()
	idx.SetReady()
	rng := rand.New(rand.NewSource(42)) //nolint:gosec // G404: math/rand fine for test data generation

	vecs := make([][]float32, b.N)
	for i := range vecs {
		vecs[i] = generateRandomVector(dims, rng)
	}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if err := idx.Add("bench", fmt.Sprintf("doc-%d", i), vecs[i]); err != nil {
			b.Fatal(err)
		}
	}
}

// --- Benchmarks for VectorStore (BoltDB persistence) ---

func BenchmarkVectorStorePut_768(b *testing.B) {
	benchmarkStorePut(b, 768)
}

func BenchmarkVectorStorePut_1536(b *testing.B) {
	benchmarkStorePut(b, 1536)
}

func benchmarkStorePut(b *testing.B, dims int) {
	dbPath := b.TempDir() + "/bench.db"
	db, err := bolt.Open(dbPath, 0600, nil)
	if err != nil {
		b.Fatal(err)
	}
	defer func() {
		_ = db.Close()
		_ = os.Remove(dbPath)
	}()

	vs := NewVectorStore(db)
	if err := vs.EnsureBucket(); err != nil {
		b.Fatal(err)
	}

	rng := rand.New(rand.NewSource(42)) //nolint:gosec // G404: math/rand fine for test data generation
	vecs := make([][]float32, b.N)
	for i := range vecs {
		vecs[i] = generateRandomVector(dims, rng)
	}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = vs.Put("bench", fmt.Sprintf("doc-%d", i), vecs[i], "test-model", "hash123")
	}
}

func BenchmarkVectorStoreGet(b *testing.B) {
	dbPath := b.TempDir() + "/bench.db"
	db, err := bolt.Open(dbPath, 0600, nil)
	if err != nil {
		b.Fatal(err)
	}
	defer func() {
		_ = db.Close()
		_ = os.Remove(dbPath)
	}()

	vs := NewVectorStore(db)
	if err := vs.EnsureBucket(); err != nil {
		b.Fatal(err)
	}

	rng := rand.New(rand.NewSource(42)) //nolint:gosec // G404: math/rand fine for test data generation
	// Pre-populate with 1000 records
	for i := 0; i < 1000; i++ {
		vec := generateRandomVector(768, rng)
		_ = vs.Put("bench", fmt.Sprintf("doc-%d", i), vec, "test-model", "hash123")
	}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, _ = vs.Get("bench", fmt.Sprintf("doc-%d", i%1000))
	}
}

// --- Benchmarks for marshal/unmarshal ---

func BenchmarkMarshalEmbeddingRecord_768(b *testing.B) {
	benchmarkMarshal(b, 768)
}

func BenchmarkMarshalEmbeddingRecord_1536(b *testing.B) {
	benchmarkMarshal(b, 1536)
}

func benchmarkMarshal(b *testing.B, dims int) {
	rng := rand.New(rand.NewSource(42)) //nolint:gosec // G404: math/rand fine for test data generation
	rec := &EmbeddingRecord{
		DocID:       "test-doc-id-12345",
		Vector:      generateRandomVector(dims, rng),
		Model:       "text-embedding-3-small",
		Dimensions:  dims,
		CreatedAt:   1709136000,
		ContentHash: "abcdef0123456789",
	}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = MarshalEmbeddingRecord(rec)
	}
}

func BenchmarkUnmarshalEmbeddingRecord_768(b *testing.B) {
	benchmarkUnmarshal(b, 768)
}

func BenchmarkUnmarshalEmbeddingRecord_1536(b *testing.B) {
	benchmarkUnmarshal(b, 1536)
}

func benchmarkUnmarshal(b *testing.B, dims int) {
	rng := rand.New(rand.NewSource(42)) //nolint:gosec // G404: math/rand fine for test data generation
	rec := &EmbeddingRecord{
		DocID:       "test-doc-id-12345",
		Vector:      generateRandomVector(dims, rng),
		Model:       "text-embedding-3-small",
		Dimensions:  dims,
		CreatedAt:   1709136000,
		ContentHash: "abcdef0123456789",
	}
	data := MarshalEmbeddingRecord(rec)

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, _ = UnmarshalEmbeddingRecord(data)
	}
}
