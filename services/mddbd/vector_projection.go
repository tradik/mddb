package main

import (
	"context"
	"errors"
	"math"
	"net/http"
	"sort"
	"time"

	"mddb/internal/storage"

	bolt "go.etcd.io/bbolt"
	json "mddb/internal/jsonx"
)

// Vector-space projection: server-side PCA reduction of a collection's
// embeddings to 2D for visual exploration in the panel (cluster structure,
// outliers, query diagnostics). Sampling keeps the payload and the O(n·d)
// projection bounded regardless of collection size.

const (
	projectionDefaultSample = 1000
	projectionMaxSample     = 2000
)

// VectorProjectionRequest represents a POST /v1/vector-projection request.
type VectorProjectionRequest struct {
	Collection  string    `json:"collection"`
	Sample      int       `json:"sample"`      // max points to project (default 1000, cap 2000)
	Query       string    `json:"query"`       // optional: text query to embed and project
	QueryVector []float32 `json:"queryVector"` // optional: pre-computed query vector to project
}

// VectorProjectionPoint is a single projected embedding.
type VectorProjectionPoint struct {
	ID         string  `json:"id"`  // index key (docID or docID#N)
	Key        string  `json:"key"` // document key (human-readable)
	DocID      string  `json:"docId"`
	ChunkIndex int     `json:"chunkIndex"`
	X          float64 `json:"x"`
	Y          float64 `json:"y"`
}

// VectorProjectionResponse is the response of /v1/vector-projection.
type VectorProjectionResponse struct {
	Points     []VectorProjectionPoint `json:"points"`
	Query      *VectorProjectionPoint  `json:"query,omitempty"` // projected query vector
	Total      int                     `json:"total"`           // vectors in the collection
	Sampled    int                     `json:"sampled"`         // vectors actually projected
	Dimensions int                     `json:"dimensions"`
}

// handleVectorProjection handles POST /v1/vector-projection.
func (s *Server) handleVectorProjection(w http.ResponseWriter, r *http.Request) {
	var req VectorProjectionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		bad(w, err)
		return
	}
	if req.Collection == "" {
		bad(w, errors.New("missing collection"))
		return
	}
	if s.AuthManager != nil {
		if err := s.AuthManager.CheckPermission(r.Context(), req.Collection, PermRead); err != nil {
			http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
			return
		}
	}

	sample := req.Sample
	if sample <= 0 {
		sample = projectionDefaultSample
	}
	if sample > projectionMaxSample {
		sample = projectionMaxSample
	}

	records, err := s.VectorStore.LoadCollection(req.Collection)
	if err != nil {
		bad(w, err)
		return
	}
	if len(records) == 0 {
		ok(w, VectorProjectionResponse{Points: []VectorProjectionPoint{}})
		return
	}

	// Deterministic sampling: sort keys, take an evenly spaced subset.
	ids := make([]string, 0, len(records))
	for id := range records {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	total := len(ids)
	if len(ids) > sample {
		step := float64(len(ids)) / float64(sample)
		picked := make([]string, 0, sample)
		for i := 0; i < sample; i++ {
			picked = append(picked, ids[int(float64(i)*step)])
		}
		ids = picked
	}

	vectors := make([][]float32, 0, len(ids))
	kept := make([]string, 0, len(ids))
	dims := 0
	for _, id := range ids {
		rec := records[id]
		if rec == nil || len(rec.Vector) == 0 {
			continue
		}
		if dims == 0 {
			dims = len(rec.Vector)
		}
		if len(rec.Vector) != dims {
			continue // mixed-dimension records cannot share a projection
		}
		vectors = append(vectors, rec.Vector)
		kept = append(kept, id)
	}
	if len(vectors) == 0 {
		ok(w, VectorProjectionResponse{Points: []VectorProjectionPoint{}, Total: total, Dimensions: dims})
		return
	}

	axis1, axis2, mean := pca2D(vectors)

	// Resolve human-readable document keys for the sampled points.
	docKeys := s.projectionDocKeys(req.Collection, kept)

	points := make([]VectorProjectionPoint, 0, len(vectors))
	for i, v := range vectors {
		x, y := projectPoint(v, mean, axis1, axis2)
		docID, chunkIdx := splitChunkKey(kept[i])
		points = append(points, VectorProjectionPoint{
			ID:         kept[i],
			Key:        docKeys[docID],
			DocID:      docID,
			ChunkIndex: chunkIdx,
			X:          x,
			Y:          y,
		})
	}

	resp := VectorProjectionResponse{
		Points:     points,
		Total:      total,
		Sampled:    len(points),
		Dimensions: dims,
	}
	queryVector := req.QueryVector
	if len(queryVector) == 0 && req.Query != "" && s.Embedding != nil {
		ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
		defer cancel()
		if qv, embErr := s.Embedding.Embed(ctx, req.Query); embErr == nil {
			queryVector = qv
		}
	}
	if len(queryVector) == dims {
		x, y := projectPoint(queryVector, mean, axis1, axis2)
		resp.Query = &VectorProjectionPoint{ID: "query", X: x, Y: y}
	}

	if s.Metrics != nil {
		s.Metrics.IncOp("vector_projection", "")
	}
	ok(w, resp)
}

// projectionDocKeys resolves document keys for the base doc IDs of the
// sampled points in a single read transaction.
func (s *Server) projectionDocKeys(collection string, ids []string) map[string]string {
	keys := make(map[string]string)
	_ = s.DBView(func(tx *bolt.Tx) error {
		bDocs := tx.Bucket([]byte("docs"))
		if bDocs == nil {
			return nil
		}
		for _, id := range ids {
			docID, _ := splitChunkKey(id)
			if _, done := keys[docID]; done {
				continue
			}
			v := bDocs.Get(storage.DocKey(collection, docID))
			if v == nil {
				continue
			}
			if doc, err := loadDoc(v); err == nil {
				keys[docID] = doc.Key
			}
		}
		return nil
	})
	return keys
}

// pca2D computes the top two principal axes of the vectors via power
// iteration on the (implicit) covariance matrix — O(iterations·n·d), no
// external dependencies, plenty accurate for visualization purposes.
func pca2D(vectors [][]float32) (axis1, axis2, mean []float64) {
	n := len(vectors)
	d := len(vectors[0])

	mean = make([]float64, d)
	for _, v := range vectors {
		for j, x := range v {
			mean[j] += float64(x)
		}
	}
	for j := range mean {
		mean[j] /= float64(n)
	}

	axis1 = powerIteration(vectors, mean, nil)
	axis2 = powerIteration(vectors, mean, axis1)
	return axis1, axis2, mean
}

// powerIteration finds the dominant eigenvector of the covariance matrix,
// deflating against `orthogonalTo` when provided (for the second component).
func powerIteration(vectors [][]float32, mean []float64, orthogonalTo []float64) []float64 {
	d := len(mean)
	// Deterministic starting vector (no RNG needed for visualization).
	v := make([]float64, d)
	for j := range v {
		v[j] = 1.0 / math.Sqrt(float64(d))
	}

	centered := func(i, j int) float64 { return float64(vectors[i][j]) - mean[j] }

	for iter := 0; iter < 30; iter++ {
		// w = C·v computed as sum over rows: (x_i·v)·x_i without materializing C
		w := make([]float64, d)
		for i := range vectors {
			var dot float64
			for j := 0; j < d; j++ {
				dot += centered(i, j) * v[j]
			}
			for j := 0; j < d; j++ {
				w[j] += dot * centered(i, j)
			}
		}
		if orthogonalTo != nil {
			var proj float64
			for j := 0; j < d; j++ {
				proj += w[j] * orthogonalTo[j]
			}
			for j := 0; j < d; j++ {
				w[j] -= proj * orthogonalTo[j]
			}
		}
		var norm float64
		for _, x := range w {
			norm += x * x
		}
		norm = math.Sqrt(norm)
		if norm < 1e-12 {
			// Degenerate direction (e.g. rank-1 data when computing the
			// second axis): there is no variance left to capture. Return a
			// zero axis so all points project to 0 on it.
			return make([]float64, d)
		}
		for j := range w {
			w[j] /= norm
		}
		v = w
	}
	// Guarantee orthogonality against the first axis even if the iteration
	// stopped before full convergence.
	if orthogonalTo != nil {
		var proj float64
		for j := 0; j < d; j++ {
			proj += v[j] * orthogonalTo[j]
		}
		var norm float64
		for j := 0; j < d; j++ {
			v[j] -= proj * orthogonalTo[j]
			norm += v[j] * v[j]
		}
		norm = math.Sqrt(norm)
		if norm < 1e-12 {
			return make([]float64, d)
		}
		for j := range v {
			v[j] /= norm
		}
	}
	return v
}

// projectPoint projects a vector onto the two principal axes.
func projectPoint(v []float32, mean, axis1, axis2 []float64) (x, y float64) {
	for j := range v {
		c := float64(v[j]) - mean[j]
		x += c * axis1[j]
		y += c * axis2[j]
	}
	return x, y
}
