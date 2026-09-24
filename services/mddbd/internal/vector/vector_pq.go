package vector

import (
	"math"
	"math/rand"
	"sort"
	"sync"
	"sync/atomic"
)

// PQIndex implements VectorSearcher using Product Quantization.
// Vectors are split into subspaces, each quantized with its own codebook.
// Search uses Asymmetric Distance Computation (ADC) for efficiency.
type PQIndex struct {
	mu           sync.RWMutex
	data         map[string]*pqCollection
	nSubspaces   int // number of sub-vectors (default 8)
	codebookSize int // entries per codebook (default 256)
	maxIter      int // k-means iterations for training
	ready        atomic.Bool
}

type pqCollection struct {
	codebooks [][]float32Slice     // [subspace][codeword] -> sub-vector
	codes     map[string][]uint8   // docID -> quantized code (one byte per subspace)
	origVecs  map[string][]float32 // original vectors for re-ranking
	trained   bool
	dim       int // vector dimensionality
}

type float32Slice = []float32

// NewPQIndex creates a new Product Quantization index.
func NewPQIndex(nSubspaces, codebookSize, maxIter int) *PQIndex {
	if nSubspaces <= 0 {
		nSubspaces = 8
	}
	if codebookSize <= 0 {
		codebookSize = 256
	}
	if codebookSize > 256 {
		codebookSize = 256 // limited to uint8
	}
	if maxIter <= 0 {
		maxIter = 20
	}
	return &PQIndex{
		data:         make(map[string]*pqCollection),
		nSubspaces:   nSubspaces,
		codebookSize: codebookSize,
		maxIter:      maxIter,
	}
}

// Name implements the VectorSearcher interface.
func (p *PQIndex) Name() string { return "pq" }

// IsReady implements the VectorSearcher interface.
func (p *PQIndex) IsReady() bool { return p.ready.Load() }

// SetReady implements the VectorSearcher interface.
func (p *PQIndex) SetReady() { p.ready.Store(true) }

func (p *PQIndex) getOrCreate(collection string) *pqCollection {
	c, ok := p.data[collection]
	if !ok {
		c = &pqCollection{
			codes:    make(map[string][]uint8),
			origVecs: make(map[string][]float32),
		}
		p.data[collection] = c
	}
	return c
}

// Add implements the VectorSearcher interface.
func (p *PQIndex) Add(collection, docID string, vector []float32) error {
	// An empty vector is refused before it is stored anywhere (#252): it
	// cannot be compared with anything, and in the HNSW graph it poisoned
	// every add that followed it.
	if err := CheckVector(vector, 0); err != nil {
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()

	c := p.getOrCreate(collection)
	c.origVecs[docID] = vector

	// If trained, encode the vector
	if c.trained && len(c.codebooks) > 0 {
		c.codes[docID] = p.encode(c, vector)
	}
	return nil
}

// Remove implements the VectorSearcher interface.
func (p *PQIndex) Remove(collection, docID string) {
	p.mu.Lock()
	defer p.mu.Unlock()

	c, ok := p.data[collection]
	if !ok {
		return
	}
	delete(c.origVecs, docID)
	delete(c.codes, docID)
}

// Train implements the Trainable interface. Learns codebooks via k-means on sub-vectors.
func (p *PQIndex) Train(collection string, vectors map[string][]float32) {
	if len(vectors) == 0 {
		return
	}

	// Get dimensionality
	var dim int
	for _, v := range vectors {
		dim = len(v)
		break
	}
	if dim == 0 {
		return
	}

	// Adjust nSubspaces if dim is not evenly divisible
	nSub := p.nSubspaces
	if nSub > dim {
		nSub = dim
	}
	subDim := dim / nSub

	// Collect vectors
	allVecs := make([][]float32, 0, len(vectors))
	allIDs := make([]string, 0, len(vectors))
	for id, v := range vectors {
		allIDs = append(allIDs, id)
		allVecs = append(allVecs, v)
	}

	// Train codebooks per subspace
	codebooks := make([][]float32Slice, nSub)
	codeSize := p.codebookSize
	if codeSize > len(allVecs) {
		codeSize = len(allVecs)
	}

	for s := 0; s < nSub; s++ {
		start := s * subDim
		end := start + subDim
		if s == nSub-1 {
			end = dim // handle remainder
		}

		// Extract sub-vectors
		subVecs := make([][]float32, len(allVecs))
		for i, v := range allVecs {
			subVecs[i] = v[start:end]
		}

		// K-means on sub-vectors
		centroids := kmeansInitPQ(subVecs, codeSize)
		assignments := make([]int, len(subVecs))
		subVecDim := end - start

		for iter := 0; iter < p.maxIter; iter++ {
			for i, sv := range subVecs {
				assignments[i] = nearestCentroidPQ(sv, centroids)
			}
			centroids = recomputeCentroidsPQ(subVecs, assignments, codeSize, subVecDim)
		}
		codebooks[s] = make([]float32Slice, len(centroids))
		copy(codebooks[s], centroids)
	}

	// Encode all vectors
	codes := make(map[string][]uint8, len(allVecs))
	for i, v := range allVecs {
		code := make([]uint8, nSub)
		for s := 0; s < nSub; s++ {
			start := s * subDim
			end := start + subDim
			if s == nSub-1 {
				end = dim
			}
			code[s] = uint8(nearestCentroidPQ(v[start:end], codebooks[s])) // #nosec G115 -- subvector index always < 256
		}
		codes[allIDs[i]] = code
	}

	p.mu.Lock()
	c := p.getOrCreate(collection)
	c.codebooks = codebooks
	c.codes = codes
	c.trained = true
	c.dim = dim
	// Ensure origVecs has all vectors
	for i, id := range allIDs {
		c.origVecs[id] = allVecs[i]
	}
	p.mu.Unlock()
}

func (p *PQIndex) encode(c *pqCollection, vector []float32) []uint8 {
	nSub := len(c.codebooks)
	subDim := c.dim / nSub
	code := make([]uint8, nSub)
	for s := 0; s < nSub; s++ {
		start := s * subDim
		end := start + subDim
		if s == nSub-1 {
			end = c.dim
		}
		code[s] = uint8(nearestCentroidPQ(vector[start:end], c.codebooks[s])) // #nosec G115 -- subvector index always < 256
	}
	return code
}

// Search uses Asymmetric Distance Computation (ADC).
// Pre-computes distance tables between query sub-vectors and codebook entries,
// then sums distances for each document using quantized codes.
func (p *PQIndex) Search(collection string, query []float32, topK int, threshold float64, metric SimilarityFunc) []VectorResult {
	p.mu.RLock()
	defer p.mu.RUnlock()

	c, ok := p.data[collection]
	if !ok || !c.trained || len(c.codebooks) == 0 {
		return nil
	}
	if topK <= 0 {
		topK = 5
	}

	return p.adcSearch(c, query, topK, threshold, nil, metric)
}

// SearchWithFilter implements the VectorSearcher interface.
func (p *PQIndex) SearchWithFilter(collection string, query []float32, topK int, threshold float64, allowed map[string]bool, metric SimilarityFunc) []VectorResult {
	p.mu.RLock()
	defer p.mu.RUnlock()

	c, ok := p.data[collection]
	if !ok || !c.trained || len(c.codebooks) == 0 {
		return nil
	}
	if topK <= 0 {
		topK = 5
	}

	return p.adcSearch(c, query, topK, threshold, allowed, metric)
}

func (p *PQIndex) adcSearch(c *pqCollection, query []float32, topK int, threshold float64, allowed map[string]bool, metric SimilarityFunc) []VectorResult {
	nSub := len(c.codebooks)
	subDim := c.dim / nSub

	// Build distance tables: distTable[subspace][codeword] = squared distance
	distTable := make([][]float32, nSub)
	for s := 0; s < nSub; s++ {
		start := s * subDim
		end := start + subDim
		if s == nSub-1 {
			end = len(query)
		}
		querySub := query[start:end]
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

	// Score each document using distance tables
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

	// Re-rank top candidates with exact similarity
	if metric == nil {
		metric = CosineSimilarity
	}
	rerank := topK * 3
	if rerank > len(candidates) {
		rerank = len(candidates)
	}

	results := make([]VectorResult, 0, topK)
	for i := 0; i < rerank; i++ {
		cand := candidates[i]
		vec, ok := c.origVecs[cand.docID]
		if !ok {
			continue
		}
		score := metric(query, vec)
		if float64(score) >= threshold {
			results = append(results, VectorResult{DocID: cand.docID, Score: score})
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
func (p *PQIndex) CollectionSize(collection string) int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	c, ok := p.data[collection]
	if !ok {
		return 0
	}
	return len(c.origVecs)
}

// Collections implements the VectorSearcher interface.
func (p *PQIndex) Collections() []string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	names := make([]string, 0, len(p.data))
	for name := range p.data {
		names = append(names, name)
	}
	return names
}

// --- PQ k-means helpers (separate from IVF to avoid confusion) ---

func kmeansInitPQ(vecs [][]float32, k int) [][]float32 {
	if len(vecs) == 0 || k == 0 {
		return nil
	}
	dim := len(vecs[0])
	centroids := make([][]float32, 0, k)
	centroids = append(centroids, copyVecPQ(vecs[rand.Intn(len(vecs))])) // #nosec G404 -- not security-sensitive

	for len(centroids) < k {
		dists := make([]float64, len(vecs))
		total := 0.0
		for i, v := range vecs {
			minDist := math.MaxFloat64
			for _, c := range centroids {
				d := euclideanDistSqPQ(v, c)
				if d < minDist {
					minDist = d
				}
			}
			dists[i] = minDist
			total += minDist
		}
		if total == 0 {
			centroids = append(centroids, make([]float32, dim))
			continue
		}
		r := rand.Float64() * total // #nosec G404 -- not security-sensitive
		cumulative := 0.0
		selected := 0
		for i, d := range dists {
			cumulative += d
			if cumulative >= r {
				selected = i
				break
			}
		}
		centroids = append(centroids, copyVecPQ(vecs[selected]))
	}
	return centroids
}

func recomputeCentroidsPQ(vecs [][]float32, assignments []int, k, dim int) [][]float32 {
	sums := make([][]float64, k)
	counts := make([]int, k)
	for i := range sums {
		sums[i] = make([]float64, dim)
	}
	for i, v := range vecs {
		ci := assignments[i]
		counts[ci]++
		for j, val := range v {
			if j < dim {
				sums[ci][j] += float64(val)
			}
		}
	}
	centroids := make([][]float32, k)
	for i := range centroids {
		centroids[i] = make([]float32, dim)
		if counts[i] > 0 {
			for j := range centroids[i] {
				centroids[i][j] = float32(sums[i][j] / float64(counts[i]))
			}
		}
	}
	return centroids
}

func nearestCentroidPQ(vec []float32, centroids [][]float32) int {
	best := 0
	bestDist := math.MaxFloat64
	for i, c := range centroids {
		d := euclideanDistSqPQ(vec, c)
		if d < bestDist {
			bestDist = d
			best = i
		}
	}
	return best
}

func euclideanDistSqPQ(a, b []float32) float64 {
	var sum float64
	for i := range a {
		if i >= len(b) {
			break
		}
		d := float64(a[i]) - float64(b[i])
		sum += d * d
	}
	return sum
}

func copyVecPQ(v []float32) []float32 {
	c := make([]float32, len(v))
	copy(c, v)
	return c
}
