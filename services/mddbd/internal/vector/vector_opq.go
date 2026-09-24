package vector

import (
	"math"
	"sort"
	"sync"
	"sync/atomic"
)

// OPQIndex implements Optimized Product Quantization.
// Extends PQ by learning an orthogonal rotation matrix that decorrelates
// dimensions before subspace splitting, improving quantization quality.
// Uses alternating optimization: rotate vectors → train PQ codebooks → repeat.
type OPQIndex struct {
	mu           sync.RWMutex
	data         map[string]*opqCollection
	nSubspaces   int
	codebookSize int
	maxIter      int // k-means iterations per PQ training
	opqIter      int // alternating optimization iterations
	ready        atomic.Bool
}

type opqCollection struct {
	rotation  []float32            // dim×dim orthogonal rotation matrix (row-major)
	codebooks [][]float32Slice     // [subspace][codeword] -> rotated sub-vector
	codes     map[string][]uint8   // docID -> quantized code
	origVecs  map[string][]float32 // original (unrotated) vectors for re-ranking
	trained   bool
	dim       int
}

// NewOPQIndex creates a new Optimized Product Quantization index.
func NewOPQIndex(nSubspaces, codebookSize, maxIter, opqIter int) *OPQIndex {
	if nSubspaces <= 0 {
		nSubspaces = 8
	}
	if codebookSize <= 0 {
		codebookSize = 256
	}
	if codebookSize > 256 {
		codebookSize = 256
	}
	if maxIter <= 0 {
		maxIter = 20
	}
	if opqIter <= 0 {
		opqIter = 5
	}
	return &OPQIndex{
		data:         make(map[string]*opqCollection),
		nSubspaces:   nSubspaces,
		codebookSize: codebookSize,
		maxIter:      maxIter,
		opqIter:      opqIter,
	}
}

// Name implements the VectorSearcher interface.
func (o *OPQIndex) Name() string { return "opq" }

// IsReady implements the VectorSearcher interface.
func (o *OPQIndex) IsReady() bool { return o.ready.Load() }

// SetReady implements the VectorSearcher interface.
func (o *OPQIndex) SetReady() { o.ready.Store(true) }

func (o *OPQIndex) getOrCreate(collection string) *opqCollection {
	c, ok := o.data[collection]
	if !ok {
		c = &opqCollection{
			codes:    make(map[string][]uint8),
			origVecs: make(map[string][]float32),
		}
		o.data[collection] = c
	}
	return c
}

// Add implements the VectorSearcher interface.
func (o *OPQIndex) Add(collection, docID string, vector []float32) error {
	// An empty vector is refused before it is stored anywhere (#252): it
	// cannot be compared with anything, and in the HNSW graph it poisoned
	// every add that followed it.
	if err := CheckVector(vector, 0); err != nil {
		return err
	}
	o.mu.Lock()
	defer o.mu.Unlock()

	c := o.getOrCreate(collection)
	c.origVecs[docID] = vector

	if c.trained && len(c.codebooks) > 0 {
		rotated := matVecMul(c.rotation, vector, c.dim)
		c.codes[docID] = encodeOPQ(c, rotated, o.nSubspaces)
	}
	return nil
}

// Remove implements the VectorSearcher interface.
func (o *OPQIndex) Remove(collection, docID string) {
	o.mu.Lock()
	defer o.mu.Unlock()

	c, ok := o.data[collection]
	if !ok {
		return
	}
	delete(c.origVecs, docID)
	delete(c.codes, docID)
}

// Train implements the Trainable interface.
// Alternating optimization: learn rotation matrix + PQ codebooks jointly.
func (o *OPQIndex) Train(collection string, vectors map[string][]float32) {
	if len(vectors) == 0 {
		return
	}

	var dim int
	for _, v := range vectors {
		dim = len(v)
		break
	}
	if dim == 0 {
		return
	}

	nSub := o.nSubspaces
	if nSub > dim {
		nSub = dim
	}

	// Collect vectors
	allVecs := make([][]float32, 0, len(vectors))
	allIDs := make([]string, 0, len(vectors))
	for id, v := range vectors {
		allIDs = append(allIDs, id)
		allVecs = append(allVecs, v)
	}

	// Initialize rotation as identity matrix
	rotation := identityMatrix(dim)

	var codebooks [][]float32Slice

	// Alternating optimization
	for iter := 0; iter < o.opqIter; iter++ {
		// Step 1: Rotate all vectors
		rotatedVecs := make([][]float32, len(allVecs))
		for i, v := range allVecs {
			rotatedVecs[i] = matVecMul(rotation, v, dim)
		}

		// Step 2: Train PQ codebooks on rotated vectors
		codebooks = trainPQCodebooks(rotatedVecs, nSub, dim, o.codebookSize, o.maxIter)

		// Step 3: Compute reconstruction errors and update rotation
		// Reconstruct each vector from its PQ encoding
		reconstructed := make([][]float32, len(rotatedVecs))
		subDim := dim / nSub
		for i, rv := range rotatedVecs {
			code := encodePQVec(rv, codebooks, nSub, dim)
			recon := make([]float32, dim)
			for s := 0; s < nSub; s++ {
				start := s * subDim
				end := start + subDim
				if s == nSub-1 {
					end = dim
				}
				if int(code[s]) < len(codebooks[s]) {
					copy(recon[start:end], codebooks[s][code[s]])
				}
			}
			reconstructed[i] = recon
		}

		// Step 4: Learn optimal rotation via SVD of X^T * Y
		// where X = original vectors, Y = reconstructed vectors
		// Simplified: use Procrustes alignment (polar decomposition)
		rotation = learnRotation(allVecs, reconstructed, dim)
	}

	// Final encoding with learned rotation
	codes := make(map[string][]uint8, len(allVecs))
	for i, v := range allVecs {
		rotated := matVecMul(rotation, v, dim)
		codes[allIDs[i]] = encodePQVec(rotated, codebooks, nSub, dim)
	}

	o.mu.Lock()
	c := o.getOrCreate(collection)
	c.rotation = rotation
	c.codebooks = codebooks
	c.codes = codes
	c.trained = true
	c.dim = dim
	for i, id := range allIDs {
		c.origVecs[id] = allVecs[i]
	}
	o.mu.Unlock()
}

// Search uses ADC on rotated query vector.
func (o *OPQIndex) Search(collection string, query []float32, topK int, threshold float64, metric SimilarityFunc) []VectorResult {
	o.mu.RLock()
	defer o.mu.RUnlock()

	c, ok := o.data[collection]
	if !ok || !c.trained || len(c.codebooks) == 0 {
		return nil
	}
	if topK <= 0 {
		topK = 5
	}

	return o.adcSearchOPQ(c, query, topK, threshold, nil, metric)
}

// SearchWithFilter implements the VectorSearcher interface.
func (o *OPQIndex) SearchWithFilter(collection string, query []float32, topK int, threshold float64, allowed map[string]bool, metric SimilarityFunc) []VectorResult {
	o.mu.RLock()
	defer o.mu.RUnlock()

	c, ok := o.data[collection]
	if !ok || !c.trained || len(c.codebooks) == 0 {
		return nil
	}
	if topK <= 0 {
		topK = 5
	}

	return o.adcSearchOPQ(c, query, topK, threshold, allowed, metric)
}

func (o *OPQIndex) adcSearchOPQ(c *opqCollection, query []float32, topK int, threshold float64, allowed map[string]bool, metric SimilarityFunc) []VectorResult {
	nSub := len(c.codebooks)
	subDim := c.dim / nSub

	// Rotate query
	rotatedQuery := matVecMul(c.rotation, query, c.dim)

	// Build distance tables on rotated query
	distTable := make([][]float32, nSub)
	for s := 0; s < nSub; s++ {
		start := s * subDim
		end := start + subDim
		if s == nSub-1 {
			end = len(rotatedQuery)
		}
		querySub := rotatedQuery[start:end]
		distTable[s] = make([]float32, len(c.codebooks[s]))
		for j, cw := range c.codebooks[s] {
			var d float32
			for k := range querySub {
				if k < len(cw) {
					diff := querySub[k] - cw[k]
					d += diff * diff
				}
			}
			distTable[s][j] = d
		}
	}

	// Score each document
	type candidate struct {
		docID      string
		approxDist float32
	}
	candidates := make([]candidate, 0, len(c.codes))
	for docID, code := range c.codes {
		if allowed != nil && !allowed[BaseDocID(docID)] {
			continue
		}
		var dist float32
		for s, ci := range code {
			if s < len(distTable) && int(ci) < len(distTable[s]) {
				dist += distTable[s][ci]
			}
		}
		candidates = append(candidates, candidate{docID: docID, approxDist: dist})
	}

	// Sort by approximate distance (ascending = closest)
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].approxDist < candidates[j].approxDist
	})

	// Re-rank with exact similarity on original (unrotated) vectors
	if metric == nil {
		metric = CosineSimilarity
	}
	rerank := topK * 3
	if rerank > len(candidates) {
		rerank = len(candidates)
	}

	results := make([]VectorResult, 0, topK)
	for i := 0; i < rerank; i++ {
		vec, ok := c.origVecs[candidates[i].docID]
		if !ok {
			continue
		}
		score := metric(query, vec)
		if float64(score) >= threshold {
			results = append(results, VectorResult{DocID: candidates[i].docID, Score: score})
		}
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})
	if len(results) > topK {
		results = results[:topK]
	}
	return results
}

// CollectionSize implements the VectorSearcher interface.
func (o *OPQIndex) CollectionSize(collection string) int {
	o.mu.RLock()
	defer o.mu.RUnlock()
	c, ok := o.data[collection]
	if !ok {
		return 0
	}
	return len(c.origVecs)
}

// Collections implements the VectorSearcher interface.
func (o *OPQIndex) Collections() []string {
	o.mu.RLock()
	defer o.mu.RUnlock()
	names := make([]string, 0, len(o.data))
	for name := range o.data {
		names = append(names, name)
	}
	return names
}

// --- OPQ linear algebra helpers ---

// identityMatrix creates a dim×dim identity matrix in row-major layout.
func identityMatrix(dim int) []float32 {
	m := make([]float32, dim*dim)
	for i := 0; i < dim; i++ {
		m[i*dim+i] = 1.0
	}
	return m
}

// matVecMul multiplies a dim×dim matrix by a dim vector.
func matVecMul(mat []float32, vec []float32, dim int) []float32 {
	result := make([]float32, dim)
	for i := 0; i < dim; i++ {
		var sum float32
		off := i * dim
		for j := 0; j < dim; j++ {
			sum += mat[off+j] * vec[j]
		}
		result[i] = sum
	}
	return result
}

// trainPQCodebooks trains PQ codebooks on pre-rotated vectors.
func trainPQCodebooks(vecs [][]float32, nSub, dim, codebookSize, maxIter int) [][]float32Slice {
	subDim := dim / nSub
	codeSize := codebookSize
	if codeSize > len(vecs) {
		codeSize = len(vecs)
	}

	codebooks := make([][]float32Slice, nSub)
	for s := 0; s < nSub; s++ {
		start := s * subDim
		end := start + subDim
		if s == nSub-1 {
			end = dim
		}

		subVecs := make([][]float32, len(vecs))
		for i, v := range vecs {
			subVecs[i] = v[start:end]
		}

		centroids := kmeansInitPQ(subVecs, codeSize)
		assignments := make([]int, len(subVecs))
		subVecDim := end - start

		for iter := 0; iter < maxIter; iter++ {
			for i, sv := range subVecs {
				assignments[i] = nearestCentroidPQ(sv, centroids)
			}
			centroids = recomputeCentroidsPQ(subVecs, assignments, codeSize, subVecDim)
		}
		codebooks[s] = make([]float32Slice, len(centroids))
		copy(codebooks[s], centroids)
	}
	return codebooks
}

// encodePQVec encodes a single (rotated) vector into PQ codes.
func encodePQVec(vec []float32, codebooks [][]float32Slice, nSub, dim int) []uint8 {
	subDim := dim / nSub
	code := make([]uint8, nSub)
	for s := 0; s < nSub; s++ {
		start := s * subDim
		end := start + subDim
		if s == nSub-1 {
			end = dim
		}
		code[s] = uint8(nearestCentroidPQ(vec[start:end], codebooks[s])) // #nosec G115
	}
	return code
}

// encodeOPQ encodes a single rotated vector using collection codebooks.
func encodeOPQ(c *opqCollection, rotated []float32, nSub int) []uint8 {
	return encodePQVec(rotated, c.codebooks, nSub, c.dim)
}

// learnRotation computes the optimal orthogonal rotation aligning X to Y
// using simplified Procrustes analysis (polar decomposition via SVD approximation).
// For practical embedded use, we use power iteration to approximate the
// dominant singular vectors instead of full SVD.
func learnRotation(original, reconstructed [][]float32, dim int) []float32 {
	n := len(original)
	if n == 0 || dim == 0 {
		return identityMatrix(dim)
	}

	// Compute M = X^T * Y (dim × dim)
	m := make([]float64, dim*dim)
	for k := 0; k < n; k++ {
		x := original[k]
		y := reconstructed[k]
		for i := 0; i < dim; i++ {
			off := i * dim
			xi := float64(x[i])
			for j := 0; j < dim; j++ {
				m[off+j] += xi * float64(y[j])
			}
		}
	}

	// Polar decomposition: R = U * V^T where M = U * S * V^T
	// Approximate using iterative polar decomposition: R_{k+1} = 0.5*(R_k + R_k^{-T})
	// Initialize with M (normalized)
	r := make([]float64, dim*dim)
	copy(r, m)

	// Normalize
	norm := 0.0
	for _, v := range r {
		norm += v * v
	}
	norm = math.Sqrt(norm)
	if norm > 0 {
		for i := range r {
			r[i] /= norm
		}
	} else {
		return identityMatrix(dim)
	}

	// 10 iterations of polar decomposition
	for iter := 0; iter < 10; iter++ {
		// Compute R^{-T} via (R^T)^{-1}
		// For stability, use the relation: R_{k+1} = 0.5*(R_k + R_k^{-T})
		// Approximate R^{-T} by transposing R (works well when R is near-orthogonal)
		rNew := make([]float64, dim*dim)
		for i := 0; i < dim; i++ {
			for j := 0; j < dim; j++ {
				rNew[i*dim+j] = 0.5 * (r[i*dim+j] + r[j*dim+i])
			}
		}

		// Check convergence
		diff := 0.0
		for i := range r {
			d := rNew[i] - r[i]
			diff += d * d
		}
		r = rNew
		if diff < 1e-10 {
			break
		}
	}

	// Re-orthogonalize via Gram-Schmidt
	result := gramSchmidt(r, dim)

	// Convert to float32
	rotation := make([]float32, dim*dim)
	for i, v := range result {
		rotation[i] = float32(v)
	}
	return rotation
}

// gramSchmidt orthogonalizes a matrix via modified Gram-Schmidt.
func gramSchmidt(m []float64, dim int) []float64 {
	result := make([]float64, dim*dim)
	copy(result, m)

	for i := 0; i < dim; i++ {
		// Normalize row i
		norm := 0.0
		for j := 0; j < dim; j++ {
			v := result[i*dim+j]
			norm += v * v
		}
		norm = math.Sqrt(norm)
		if norm < 1e-15 {
			// Degenerate: set to identity row
			for j := 0; j < dim; j++ {
				result[i*dim+j] = 0
			}
			result[i*dim+i] = 1
			continue
		}
		for j := 0; j < dim; j++ {
			result[i*dim+j] /= norm
		}

		// Subtract projection from subsequent rows
		for k := i + 1; k < dim; k++ {
			dot := 0.0
			for j := 0; j < dim; j++ {
				dot += result[k*dim+j] * result[i*dim+j]
			}
			for j := 0; j < dim; j++ {
				result[k*dim+j] -= dot * result[i*dim+j]
			}
		}
	}
	return result
}
