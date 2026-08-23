package main

import (
	"math"
	"testing"

	json "mddb/internal/jsonx"
)

func TestPCA2DSeparatesClusters(t *testing.T) {
	// Two tight clusters along the first dimension: PCA axis 1 must
	// separate them clearly in x.
	var vectors [][]float32
	for i := 0; i < 10; i++ {
		vectors = append(vectors, []float32{10 + float32(i)*0.01, 0, 0})
		vectors = append(vectors, []float32{-10 - float32(i)*0.01, 0, 0})
	}
	axis1, axis2, mean := pca2D(vectors)

	var xs []float64
	for _, v := range vectors {
		x, _ := projectPoint(v, mean, axis1, axis2)
		xs = append(xs, x)
	}
	// Points from opposite clusters must land on opposite sides.
	for i := 0; i < len(xs); i += 2 {
		if xs[i]*xs[i+1] >= 0 {
			t.Fatalf("clusters not separated: x[%d]=%f x[%d]=%f", i, xs[i], i+1, xs[i+1])
		}
	}
	// Axes must be (near) orthogonal, axis1 unit length.
	var dot, n1, n2 float64
	for j := range axis1 {
		dot += axis1[j] * axis2[j]
		n1 += axis1[j] * axis1[j]
		n2 += axis2[j] * axis2[j]
	}
	if math.Abs(dot) > 1e-6 {
		t.Errorf("axes not orthogonal: dot = %e", dot)
	}
	if math.Abs(n1-1) > 1e-6 {
		t.Errorf("axis1 not unit length: %f", n1)
	}
	if n2 > 1+1e-6 {
		t.Errorf("axis2 longer than unit: %f", n2)
	}
}

func TestHandleVectorProjection(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	// Missing collection
	rec := doRequest(t, s.handleVectorProjection, VectorProjectionRequest{})
	if rec.Code != 400 {
		t.Errorf("missing collection: status %d, want 400", rec.Code)
	}

	// Empty collection
	rec = doRequest(t, s.handleVectorProjection, VectorProjectionRequest{Collection: "empty"})
	if rec.Code != 200 {
		t.Fatalf("empty collection: status %d", rec.Code)
	}

	// Populated collection
	doc := addTestDoc(t, s, "viz", "alpha", "en", "# Alpha", nil)
	if err := s.VectorStore.Put("viz", doc.ID, []float32{1, 0, 0}, "m", "h1"); err != nil {
		t.Fatal(err)
	}
	doc2 := addTestDoc(t, s, "viz", "beta", "en", "# Beta", nil)
	if err := s.VectorStore.Put("viz", doc2.ID, []float32{0, 1, 0}, "m", "h2"); err != nil {
		t.Fatal(err)
	}

	rec = doRequest(t, s.handleVectorProjection, VectorProjectionRequest{
		Collection:  "viz",
		QueryVector: []float32{1, 0, 0},
	})
	if rec.Code != 200 {
		t.Fatalf("projection: status %d, body %s", rec.Code, rec.Body.String())
	}
	var resp VectorProjectionResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Sampled != 2 || len(resp.Points) != 2 {
		t.Fatalf("sampled = %d points = %d, want 2/2", resp.Sampled, len(resp.Points))
	}
	if resp.Dimensions != 3 {
		t.Errorf("dimensions = %d, want 3", resp.Dimensions)
	}
	if resp.Query == nil {
		t.Error("query point missing from projection")
	}
	keys := map[string]bool{}
	for _, p := range resp.Points {
		keys[p.Key] = true
	}
	if !keys["alpha"] || !keys["beta"] {
		t.Errorf("document keys not resolved: %+v", resp.Points)
	}
}

func TestHandleVectorProjectionSampling(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	for i := 0; i < 30; i++ {
		if err := s.VectorStore.Put("big", string(rune('a'+i%26))+string(rune('0'+i/26)), []float32{float32(i), 1, 0}, "m", "h"); err != nil {
			t.Fatal(err)
		}
	}

	rec := doRequest(t, s.handleVectorProjection, VectorProjectionRequest{Collection: "big", Sample: 10})
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var resp VectorProjectionResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Sampled != 10 {
		t.Errorf("sampled = %d, want 10", resp.Sampled)
	}
	if resp.Total != 30 {
		t.Errorf("total = %d, want 30", resp.Total)
	}
}
