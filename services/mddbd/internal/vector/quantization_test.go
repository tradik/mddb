package vector

import (
	"math"
	"math/rand"
	"os"
	"testing"

	bolt "go.etcd.io/bbolt"
)

// --- Scalar Quantization Round-Trip Tests ---

func TestQuantizeInt8RoundTrip(t *testing.T) {
	vec := []float32{-1.0, -0.5, 0.0, 0.25, 0.5, 1.0}

	qv := QuantizeFloat32(vec, QuantInt8)
	if qv == nil {
		t.Fatal("QuantizeFloat32 returned nil")
	}
	if qv.Type != QuantInt8 {
		t.Errorf("expected type int8, got %s", qv.Type)
	}
	if qv.Dims != len(vec) {
		t.Errorf("expected dims %d, got %d", len(vec), qv.Dims)
	}
	if len(qv.Data) != len(vec) {
		t.Errorf("expected data len %d, got %d", len(vec), len(qv.Data))
	}

	restored := DequantizeToFloat32(qv)
	if len(restored) != len(vec) {
		t.Fatalf("expected restored len %d, got %d", len(vec), len(restored))
	}

	// int8 quantization should have max error of scale/255
	scale := float32(2.0) // max - min = 1.0 - (-1.0) = 2.0
	maxErr := scale / 255.0
	for i, v := range vec {
		diff := float32(math.Abs(float64(restored[i] - v)))
		if diff > maxErr+1e-6 {
			t.Errorf("index %d: original=%f, restored=%f, diff=%f > maxErr=%f", i, v, restored[i], diff, maxErr)
		}
	}
}

func TestQuantizeInt4RoundTrip(t *testing.T) {
	vec := []float32{-1.0, -0.5, 0.0, 0.25, 0.5, 1.0, 0.75}

	qv := QuantizeFloat32(vec, QuantInt4)
	if qv == nil {
		t.Fatal("QuantizeFloat32 returned nil")
	}
	if qv.Type != QuantInt4 {
		t.Errorf("expected type int4, got %s", qv.Type)
	}
	if qv.Dims != len(vec) {
		t.Errorf("expected dims %d, got %d", len(vec), qv.Dims)
	}
	// 7 values -> 4 bytes (2 per byte, last byte has 1 value + padding)
	expectedBytes := (len(vec) + 1) / 2
	if len(qv.Data) != expectedBytes {
		t.Errorf("expected data len %d, got %d", expectedBytes, len(qv.Data))
	}

	restored := DequantizeToFloat32(qv)
	if len(restored) != len(vec) {
		t.Fatalf("expected restored len %d, got %d", len(vec), len(restored))
	}

	// int4: max error = scale/15
	scale := float32(2.0)
	maxErr := scale / 15.0
	for i, v := range vec {
		diff := float32(math.Abs(float64(restored[i] - v)))
		if diff > maxErr+1e-6 {
			t.Errorf("index %d: original=%f, restored=%f, diff=%f > maxErr=%f", i, v, restored[i], diff, maxErr)
		}
	}
}

func TestQuantizeFloat32ReturnsNil(t *testing.T) {
	vec := []float32{1.0, 2.0}
	if got := QuantizeFloat32(vec, QuantNone); got != nil {
		t.Error("QuantNone should return nil")
	}
}

func TestQuantizeEmpty(t *testing.T) {
	qv := QuantizeFloat32([]float32{}, QuantInt8)
	if qv.Dims != 0 {
		t.Error("expected 0 dims for empty vector")
	}
	qv4 := QuantizeFloat32([]float32{}, QuantInt4)
	if qv4.Dims != 0 {
		t.Error("expected 0 dims for empty vector")
	}
}

func TestQuantizeConstantVector(t *testing.T) {
	vec := []float32{0.5, 0.5, 0.5, 0.5}

	qv8 := QuantizeFloat32(vec, QuantInt8)
	restored8 := DequantizeToFloat32(qv8)
	for i, v := range restored8 {
		if math.Abs(float64(v-0.5)) > 0.01 {
			t.Errorf("int8 constant vector: index %d got %f, want 0.5", i, v)
		}
	}

	qv4 := QuantizeFloat32(vec, QuantInt4)
	restored4 := DequantizeToFloat32(qv4)
	for i, v := range restored4 {
		if math.Abs(float64(v-0.5)) > 0.1 {
			t.Errorf("int4 constant vector: index %d got %f, want 0.5", i, v)
		}
	}
}

// --- Marshal/Unmarshal QuantizedVector ---

func TestMarshalUnmarshalQuantizedVector(t *testing.T) {
	vec := []float32{-0.3, 0.1, 0.5, 0.9, -0.7}

	for _, qt := range []QuantizationType{QuantInt8, QuantInt4} {
		qv := QuantizeFloat32(vec, qt)
		data := MarshalQuantizedVector(qv)
		restored, err := UnmarshalQuantizedVector(data)
		if err != nil {
			t.Fatalf("%s: UnmarshalQuantizedVector failed: %v", qt, err)
		}

		if restored.Type != qv.Type {
			t.Errorf("%s: type mismatch: got %s, want %s", qt, restored.Type, qv.Type)
		}
		if restored.Dims != qv.Dims {
			t.Errorf("%s: dims mismatch: got %d, want %d", qt, restored.Dims, qv.Dims)
		}
		if restored.Min != qv.Min {
			t.Errorf("%s: min mismatch: got %f, want %f", qt, restored.Min, qv.Min)
		}
		if restored.Max != qv.Max {
			t.Errorf("%s: max mismatch: got %f, want %f", qt, restored.Max, qv.Max)
		}
		if len(restored.Data) != len(qv.Data) {
			t.Errorf("%s: data len mismatch: got %d, want %d", qt, len(restored.Data), len(qv.Data))
		}
	}
}

// --- Quantized Embedding Record Serialization ---

func TestMarshalUnmarshalEmbeddingRecordQuantized(t *testing.T) {
	rec := &EmbeddingRecord{
		DocID:       "test|doc|en_us",
		Vector:      []float32{0.1, 0.2, 0.3, -0.5, 1.0},
		Model:       "text-embedding-3-small",
		Dimensions:  5,
		CreatedAt:   1700000000,
		ContentHash: "abc123def456",
	}

	for _, qt := range []QuantizationType{QuantInt8, QuantInt4} {
		data := marshalEmbeddingRecordQuantized(rec, qt)

		if !isQuantizedRecord(data) {
			t.Fatalf("%s: expected quantized record marker", qt)
		}

		got, qv, err := unmarshalEmbeddingRecordQuantized(data)
		if err != nil {
			t.Fatalf("%s: unmarshal failed: %v", qt, err)
		}

		if got.DocID != rec.DocID {
			t.Errorf("%s: DocID: got %q, want %q", qt, got.DocID, rec.DocID)
		}
		if got.Model != rec.Model {
			t.Errorf("%s: Model: got %q, want %q", qt, got.Model, rec.Model)
		}
		if got.Dimensions != rec.Dimensions {
			t.Errorf("%s: Dimensions: got %d, want %d", qt, got.Dimensions, rec.Dimensions)
		}
		if got.CreatedAt != rec.CreatedAt {
			t.Errorf("%s: CreatedAt: got %d, want %d", qt, got.CreatedAt, rec.CreatedAt)
		}
		if got.ContentHash != rec.ContentHash {
			t.Errorf("%s: ContentHash: got %q, want %q", qt, got.ContentHash, rec.ContentHash)
		}
		if len(got.Vector) != len(rec.Vector) {
			t.Fatalf("%s: Vector length: got %d, want %d", qt, len(got.Vector), len(rec.Vector))
		}
		if qv == nil {
			t.Fatalf("%s: expected non-nil QuantizedVector", qt)
		}
		if qv.Type != qt {
			t.Errorf("%s: qv type: got %s, want %s", qt, qv.Type, qt)
		}
	}
}

func TestIsQuantizedRecord(t *testing.T) {
	// v1 record (starts with model length, not version 2)
	rec := &EmbeddingRecord{
		DocID: "d", Vector: []float32{1.0}, Model: "m", Dimensions: 1, CreatedAt: 1, ContentHash: "h",
	}
	v1 := MarshalEmbeddingRecord(rec)
	if isQuantizedRecord(v1) {
		t.Error("v1 record should not be identified as quantized")
	}

	// v2 record
	v2 := marshalEmbeddingRecordQuantized(rec, QuantInt8)
	if !isQuantizedRecord(v2) {
		t.Error("v2 record should be identified as quantized")
	}
}

// --- Quantized Similarity ---

func TestCosineSimInt8(t *testing.T) {
	a := []float32{1.0, 0.0, 0.0}
	b := []float32{1.0, 0.0, 0.0}

	qa := QuantizeFloat32(a, QuantInt8)
	qb := QuantizeFloat32(b, QuantInt8)

	score := CosineSimInt8(qa, qb)
	if score < 0.99 {
		t.Errorf("identical vectors should have cosine ~1.0, got %f", score)
	}
}

func TestCosineSimInt4(t *testing.T) {
	a := []float32{1.0, 0.0, 0.0}
	b := []float32{1.0, 0.0, 0.0}

	qa := QuantizeFloat32(a, QuantInt4)
	qb := QuantizeFloat32(b, QuantInt4)

	score := CosineSimInt4(qa, qb)
	if score < 0.99 {
		t.Errorf("identical vectors should have cosine ~1.0, got %f", score)
	}
}

func TestCosineSimInt8DifferentVectors(t *testing.T) {
	// Verify that similar vectors score higher than dissimilar ones
	a := []float32{0.9, 0.1, 0.05, 0.02}
	b := []float32{0.85, 0.15, 0.03, 0.01} // similar to a
	c := []float32{0.01, 0.02, 0.9, 0.8}   // very different from a

	qa := QuantizeFloat32(a, QuantInt8)
	qb := QuantizeFloat32(b, QuantInt8)
	qc := QuantizeFloat32(c, QuantInt8)

	scoreAB := CosineSimInt8(qa, qb)
	scoreAC := CosineSimInt8(qa, qc)

	if scoreAB <= scoreAC {
		t.Errorf("similar vectors should score higher: AB=%f, AC=%f", scoreAB, scoreAC)
	}
}

// --- Quantized Search Accuracy ---

func TestQuantizedSearchRanking(t *testing.T) {
	// Verify that quantized search preserves ranking order vs float32 search
	rng := rand.New(rand.NewSource(42)) //nolint:gosec // G404: math/rand fine for test data generation
	dims := 128
	numVecs := 50

	// Generate random vectors
	vecs := make(map[string][]float32, numVecs)
	for i := 0; i < numVecs; i++ {
		v := make([]float32, dims)
		for j := range v {
			v[j] = rng.Float32()*2 - 1
		}
		vecs[string(rune('A'+i/26))+string(rune('a'+i%26))] = v
	}

	query := make([]float32, dims)
	for j := range query {
		query[j] = rng.Float32()*2 - 1
	}

	// Float32 baseline
	floatIndex := NewVectorIndex()
	for id, v := range vecs {
		if err := floatIndex.Add("test", id, v); err != nil {
			t.Fatal(err)
		}
	}
	floatResults := floatIndex.Search("test", query, 10, 0, nil)

	// Quantized int8
	qi8 := NewQuantizedVectorIndex(func(string) QuantizationType { return QuantInt8 })
	for id, v := range vecs {
		if err := qi8.Add("test", id, v); err != nil {
			t.Fatal(err)
		}
	}
	qi8Results := qi8.Search("test", query, 10, 0, nil)

	// Check overlap: at least 7/10 top results should match
	floatTop := make(map[string]bool)
	for _, r := range floatResults {
		floatTop[r.DocID] = true
	}
	overlap := 0
	for _, r := range qi8Results {
		if floatTop[r.DocID] {
			overlap++
		}
	}

	if overlap < 7 {
		t.Errorf("int8 quantized search: only %d/10 overlap with float32 top-10 (expected >= 7)", overlap)
	}
}

// --- VectorStore Quantized Storage Integration ---

func TestVectorStorePutQuantized(t *testing.T) {
	dbPath := t.TempDir() + "/test.db"
	db, err := bolt.Open(dbPath, 0600, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = db.Close()
		_ = os.Remove(dbPath)
	}()

	vs := NewVectorStore(db)
	if err := vs.EnsureBucket(); err != nil {
		t.Fatal(err)
	}

	vec := []float32{0.1, 0.2, 0.3, -0.5, 1.0}

	// Store with int8 quantization
	if err := vs.PutQuantized("coll", "doc1", vec, "model", "hash1", QuantInt8); err != nil {
		t.Fatal(err)
	}

	// Retrieve
	rec, err := vs.Get("coll", "doc1")
	if err != nil {
		t.Fatal(err)
	}
	if rec == nil {
		t.Fatal("expected non-nil record")
	}
	if rec.DocID != "doc1" {
		t.Errorf("DocID: got %q, want %q", rec.DocID, "doc1")
	}
	if rec.Model != "model" {
		t.Errorf("Model: got %q, want %q", rec.Model, "model")
	}
	if len(rec.Vector) != len(vec) {
		t.Fatalf("Vector len: got %d, want %d", len(rec.Vector), len(vec))
	}

	// Vectors should be approximately equal (dequantized)
	scale := float32(1.5) // max(1.0) - min(-0.5) = 1.5
	maxErr := scale / 255.0
	for i := range vec {
		diff := float32(math.Abs(float64(rec.Vector[i] - vec[i])))
		if diff > maxErr+0.01 {
			t.Errorf("index %d: got %f, want ~%f (diff=%f)", i, rec.Vector[i], vec[i], diff)
		}
	}
}

func TestVectorStorePutChunksQuantized(t *testing.T) {
	dbPath := t.TempDir() + "/test.db"
	db, err := bolt.Open(dbPath, 0600, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = db.Close()
		_ = os.Remove(dbPath)
	}()

	vs := NewVectorStore(db)
	if err := vs.EnsureBucket(); err != nil {
		t.Fatal(err)
	}

	chunks := []ChunkEmbedding{
		{ChunkIndex: 0, Vector: []float32{0.1, 0.2, 0.3}},
		{ChunkIndex: 1, Vector: []float32{0.4, 0.5, 0.6}},
	}

	if err := vs.PutChunksQuantized("coll", "doc1", chunks, "model", "hash1", QuantInt4); err != nil {
		t.Fatal(err)
	}

	// Load collection
	records, err := vs.LoadCollection("coll")
	if err != nil {
		t.Fatal(err)
	}

	if len(records) != 2 {
		t.Fatalf("expected 2 records, got %d", len(records))
	}

	// Verify chunk keys exist
	if _, ok := records["doc1#0"]; !ok {
		t.Error("missing chunk doc1#0")
	}
	if _, ok := records["doc1#1"]; !ok {
		t.Error("missing chunk doc1#1")
	}
}

func TestVectorStoreLoadCollectionQuantized(t *testing.T) {
	dbPath := t.TempDir() + "/test.db"
	db, err := bolt.Open(dbPath, 0600, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = db.Close()
		_ = os.Remove(dbPath)
	}()

	vs := NewVectorStore(db)
	if err := vs.EnsureBucket(); err != nil {
		t.Fatal(err)
	}

	// Store one float32 and one quantized
	_ = vs.Put("coll", "doc1", []float32{0.1, 0.2}, "model", "h1")
	_ = vs.PutQuantized("coll", "doc2", []float32{0.3, 0.4}, "model", "h2", QuantInt8)

	records, quantized, err := vs.LoadCollectionQuantized("coll")
	if err != nil {
		t.Fatal(err)
	}

	if len(records) != 2 {
		t.Fatalf("expected 2 records, got %d", len(records))
	}

	// doc1 should not have quantized vector
	if _, ok := quantized["doc1"]; ok {
		t.Error("doc1 should not have quantized vector (stored as float32)")
	}

	// doc2 should have quantized vector
	qv, ok := quantized["doc2"]
	if !ok {
		t.Fatal("doc2 should have quantized vector")
	}
	if qv.Type != QuantInt8 {
		t.Errorf("doc2 quantized type: got %s, want int8", qv.Type)
	}
}

// --- ParseQuantization ---

func TestParseQuantization(t *testing.T) {
	tests := []struct {
		input string
		want  QuantizationType
	}{
		{"float32", QuantNone},
		{"int8", QuantInt8},
		{"int4", QuantInt4},
		{"", QuantNone},
		{"unknown", QuantNone},
	}

	for _, tt := range tests {
		got := ParseQuantization(tt.input)
		if got != tt.want {
			t.Errorf("ParseQuantization(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

// --- Compression ratio verification ---

func TestQuantizationCompressionRatio(t *testing.T) {
	dims := 1536 // typical OpenAI embedding size

	vec := make([]float32, dims)
	rng := rand.New(rand.NewSource(42)) //nolint:gosec // G404: math/rand fine for test data generation
	for i := range vec {
		vec[i] = rng.Float32()*2 - 1
	}

	float32Size := dims * 4 // 6144 bytes

	qv8 := QuantizeFloat32(vec, QuantInt8)
	int8Size := len(qv8.Data) // should be 1536 bytes

	qv4 := QuantizeFloat32(vec, QuantInt4)
	int4Size := len(qv4.Data) // should be 768 bytes

	ratio8 := float64(float32Size) / float64(int8Size)
	ratio4 := float64(float32Size) / float64(int4Size)

	if ratio8 < 3.9 || ratio8 > 4.1 {
		t.Errorf("int8 compression ratio: got %.1fx, want ~4x (float32=%d, int8=%d)", ratio8, float32Size, int8Size)
	}
	if ratio4 < 7.9 || ratio4 > 8.1 {
		t.Errorf("int4 compression ratio: got %.1fx, want ~8x (float32=%d, int4=%d)", ratio4, float32Size, int4Size)
	}

	t.Logf("Compression ratios for %d dims: int8=%.1fx (%d→%d bytes), int4=%.1fx (%d→%d bytes)",
		dims, ratio8, float32Size, int8Size, ratio4, float32Size, int4Size)
}
