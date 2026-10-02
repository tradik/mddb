package vector

import "testing"

// #269: a quantized collection whose vectors do not all agree with each other
// or with the query. Reported from production as ~15 panics per 10 minutes:
//
//	runtime error: index out of range [384] with length 384
//	mddb/internal/vector.CosineSimInt8
//
// 384 bytes is what a 768-dimension vector takes in int4, two dimensions per
// byte. A collection created as int8 and later switched to int4 (or the
// reverse) holds both, and the index scored every vector with the function
// for the type the collection started with.

func vec(dim int, seed float32) []float32 {
	v := make([]float32, dim)
	for i := range v {
		v[i] = seed + float32(i%7)/10
	}
	return v
}

func TestAQuantizedCollectionHoldingBothTypesIsSearchable(t *testing.T) {
	qt := QuantInt8
	qi := NewQuantizedVectorIndex(func(string) QuantizationType { return qt })

	if err := qi.Add("c", "old", vec(768, 0.1)); err != nil {
		t.Fatal(err)
	}
	qt = QuantInt4 // the collection's quantization is changed
	if err := qi.Add("c", "new", vec(768, 0.2)); err != nil {
		t.Fatal(err)
	}

	for _, search := range []func() []VectorResult{
		func() []VectorResult { return qi.Search("c", vec(768, 0.1), 5, -1, nil) },
		func() []VectorResult {
			return qi.SearchWithFilter("c", vec(768, 0.1), 5, -1, map[string]bool{"old": true, "new": true}, nil)
		},
	} {
		got := map[string]bool{}
		for _, r := range search() {
			got[r.DocID] = true
		}
		if !got["old"] || !got["new"] {
			t.Errorf("found %v, want both the int8 and the int4 vector", got)
		}
	}
}

// Embeddings from two models in one collection: 384 from the old one, 768
// from the new. A query from one model cannot be compared with vectors from
// the other, and those must be left out — not returned with a score of 0,
// which a threshold of 0 lets through as a match.
func TestAQuantizedSearchSkipsVectorsOfAnotherDimension(t *testing.T) {
	for _, qt := range []QuantizationType{QuantInt8, QuantInt4} {
		t.Run(string(qt), func(t *testing.T) {
			qi := NewQuantizedVectorIndex(func(string) QuantizationType { return qt })
			if err := qi.Add("c", "small", vec(384, 0.1)); err != nil {
				t.Fatal(err)
			}
			if err := qi.Add("c", "large", vec(768, 0.1)); err != nil {
				t.Fatal(err)
			}
			for _, r := range qi.Search("c", vec(768, 0.1), 5, 0, nil) {
				if r.DocID == "small" {
					t.Errorf("a 384-dimension vector was returned for a 768-dimension query (score %v)", r.Score)
				}
			}
		})
	}
}

// The similarity functions are exported and take whatever they are given. A
// vector whose payload is shorter than its dimensions says is malformed, and
// the answer is 0, not a crash.
func TestQuantizedSimilarityNeverReadsPastThePayload(t *testing.T) {
	full8 := QuantizeFloat32(vec(8, 0.1), QuantInt8)
	short := &QuantizedVector{Type: QuantInt8, Dims: 8, Data: make([]byte, 4)}
	full4 := QuantizeFloat32(vec(8, 0.1), QuantInt4)
	short4 := &QuantizedVector{Type: QuantInt4, Dims: 8, Data: make([]byte, 2)}

	for name, f := range map[string]func() float32{
		"int8 short b": func() float32 { return CosineSimInt8(full8, short) },
		"int8 short a": func() float32 { return CosineSimInt8(short, full8) },
		"int4 short b": func() float32 { return CosineSimInt4(full4, short4) },
		"int4 short a": func() float32 { return CosineSimInt4(short4, full4) },
		"int8 on int4": func() float32 { return CosineSimInt8(full4, full4) },
	} {
		t.Run(name, func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("panicked: %v", r)
				}
			}()
			if got := f(); got != 0 {
				t.Errorf("= %v, want 0", got)
			}
		})
	}
}
