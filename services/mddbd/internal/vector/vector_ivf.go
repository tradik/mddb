package vector

import (
	"math"
	"math/rand"
	"sort"
	"sync"
	"sync/atomic"
)

// IVFIndex implements VectorSearcher using Inverted File Index.
// Vectors are clustered via k-means; search probes the nearest clusters.
type IVFIndex struct {
	mu      sync.RWMutex
	data    map[string]*ivfCollection // per-collection
	nProbe  int                       // clusters to search (default 10)
	maxIter int                       // k-means iterations (default 20)
	ready   atomic.Bool
}

type ivfCollection struct {
	centroids [][]float32            // nClusters centroids
	clusters  []map[string][]float32 // cluster_id -> docID -> vector
	allVecs   map[string][]float32   // all vectors for retraining
	trainState
}

// NewIVFIndex creates a new IVF index.
func NewIVFIndex(nProbe, maxIter int) *IVFIndex {
	if nProbe <= 0 {
		nProbe = 10
	}
	if maxIter <= 0 {
		maxIter = 20
	}
	return &IVFIndex{
		data:    make(map[string]*ivfCollection),
		nProbe:  nProbe,
		maxIter: maxIter,
	}
}

// Name implements the VectorSearcher interface.
func (idx *IVFIndex) Name() string { return "ivf" }

// IsReady implements the VectorSearcher interface.
func (idx *IVFIndex) IsReady() bool { return idx.ready.Load() }

// SetReady implements the VectorSearcher interface.
func (idx *IVFIndex) SetReady() { idx.ready.Store(true) }

func (idx *IVFIndex) getOrCreate(collection string) *ivfCollection {
	c, ok := idx.data[collection]
	if !ok {
		c = &ivfCollection{
			allVecs: make(map[string][]float32),
		}
		idx.data[collection] = c
	}
	return c
}

// Add implements the VectorSearcher interface.
func (idx *IVFIndex) Add(collection, docID string, vector []float32) error {
	// An empty vector is refused before it is stored anywhere (#252): it
	// cannot be compared with anything, and in the HNSW graph it poisoned
	// every add that followed it.
	if err := CheckVector(vector, 0); err != nil {
		return err
	}
	idx.mu.Lock()
	defer idx.mu.Unlock()

	c := idx.getOrCreate(collection)
	c.allVecs[docID] = vector

	// If trained, assign to nearest cluster
	if c.trained && len(c.centroids) > 0 {
		ci := nearestCentroid(vector, c.centroids)
		if c.clusters[ci] == nil {
			c.clusters[ci] = make(map[string][]float32)
		}
		c.clusters[ci][docID] = vector
	}
	return nil
}

// Remove implements the VectorSearcher interface.
func (idx *IVFIndex) Remove(collection, docID string) {
	idx.mu.Lock()
	defer idx.mu.Unlock()

	c, ok := idx.data[collection]
	if !ok {
		return
	}
	delete(c.allVecs, docID)
	for i := range c.clusters {
		delete(c.clusters[i], docID)
	}
}

// Train implements the Trainable interface. Runs k-means clustering.
func (idx *IVFIndex) Train(collection string, vectors map[string][]float32) {
	if len(vectors) == 0 {
		return
	}

	// Determine number of clusters: sqrt(N), min 1, max 256
	nClusters := int(math.Sqrt(float64(len(vectors))))
	if nClusters < 1 {
		nClusters = 1
	}
	if nClusters > 256 {
		nClusters = 256
	}

	// Collect all vectors into a slice
	vecs := make([][]float32, 0, len(vectors))
	ids := make([]string, 0, len(vectors))
	for id, v := range vectors {
		ids = append(ids, id)
		vecs = append(vecs, v)
	}

	// K-means clustering
	centroids := kmeansInit(vecs, nClusters)
	assignments := make([]int, len(vecs))

	for iter := 0; iter < idx.maxIter; iter++ {
		// Assign each vector to nearest centroid
		for i, v := range vecs {
			assignments[i] = nearestCentroid(v, centroids)
		}
		// Recompute centroids
		newCentroids := recomputeCentroids(vecs, assignments, nClusters, len(vecs[0]))
		centroids = newCentroids
	}

	// Build clusters
	clusters := make([]map[string][]float32, nClusters)
	for i := range clusters {
		clusters[i] = make(map[string][]float32)
	}
	for i, id := range ids {
		ci := assignments[i]
		clusters[ci][id] = vecs[i]
	}

	idx.mu.Lock()
	c := idx.getOrCreate(collection)
	c.centroids = centroids
	c.clusters = clusters
	for id, v := range vectors {
		c.allVecs[id] = v
	}
	// Vectors added while training ran are in the collection but not in the
	// snapshot it trained on; encode them into the new structure too.
	for id, v := range c.allVecs {
		if _, inSnapshot := vectors[id]; !inSnapshot && len(v) == len(centroids[0]) {
			c.clusters[nearestCentroid(v, centroids)][id] = v
		}
	}
	c.markTrained(len(c.allVecs))
	idx.mu.Unlock()
}

// Search implements the VectorSearcher interface.
// Uses goroutine parallelism when probed clusters contain enough vectors.
func (idx *IVFIndex) Search(collection string, query []float32, topK int, threshold float64, metric SimilarityFunc) []VectorResult {
	idx.autoTrain(collection)
	idx.mu.RLock()
	defer idx.mu.RUnlock()

	c, ok := idx.data[collection]
	if !ok || !c.trained || len(c.centroids) == 0 {
		return nil
	}
	if topK <= 0 {
		topK = 5
	}
	if metric == nil {
		metric = CosineSimilarity
	}

	probeIndices := nearestNCentroids(query, c.centroids, idx.nProbe)

	return idx.searchClusters(c, query, topK, threshold, metric, probeIndices, nil)
}

// SearchWithFilter implements the VectorSearcher interface.
// Uses goroutine parallelism when probed clusters contain enough vectors.
func (idx *IVFIndex) SearchWithFilter(collection string, query []float32, topK int, threshold float64, allowed map[string]bool, metric SimilarityFunc) []VectorResult {
	idx.autoTrain(collection)
	idx.mu.RLock()
	defer idx.mu.RUnlock()

	c, ok := idx.data[collection]
	if !ok || !c.trained || len(c.centroids) == 0 {
		return nil
	}
	if topK <= 0 {
		topK = 5
	}
	if metric == nil {
		metric = CosineSimilarity
	}

	probeIndices := nearestNCentroids(query, c.centroids, idx.nProbe)

	filter := func(docID string) bool {
		return allowed[BaseDocID(docID)]
	}
	return idx.searchClusters(c, query, topK, threshold, metric, probeIndices, filter)
}

// searchClusters searches probed clusters, using parallel goroutines when
// the total probed vectors exceed the parallel threshold.
func (idx *IVFIndex) searchClusters(c *ivfCollection, query []float32, topK int, threshold float64, metric SimilarityFunc, probeIndices []int, filter func(string) bool) []VectorResult {
	// Count total vectors in probed clusters
	totalVectors := 0
	for _, ci := range probeIndices {
		if ci < len(c.clusters) {
			totalVectors += len(c.clusters[ci])
		}
	}

	// Parallel: one goroutine per cluster when enough data
	if totalVectors >= parallelSearchConfig.MinSize() && len(probeIndices) >= 2 {
		partials := make([][]VectorResult, len(probeIndices))
		var wg sync.WaitGroup
		wg.Add(len(probeIndices))

		for wi, ci := range probeIndices {
			go func(workerID, clusterIdx int) {
				defer wg.Done()
				if clusterIdx >= len(c.clusters) {
					return
				}
				cluster := c.clusters[clusterIdx]
				local := make([]VectorResult, 0, len(cluster))
				for docID, vec := range cluster {
					if filter != nil && !filter(docID) {
						continue
					}
					score := metric(query, vec)
					if float64(score) >= threshold {
						local = append(local, VectorResult{DocID: docID, Score: score})
					}
				}
				partials[workerID] = local
			}(wi, ci)
		}

		wg.Wait()

		total := 0
		for _, p := range partials {
			total += len(p)
		}
		merged := make([]VectorResult, 0, total)
		for _, p := range partials {
			merged = append(merged, p...)
		}

		sort.Slice(merged, func(i, j int) bool {
			if merged[i].Score != merged[j].Score {
				return merged[i].Score > merged[j].Score
			}
			return merged[i].DocID < merged[j].DocID
		})
		if len(merged) > topK {
			merged = merged[:topK]
		}
		return merged
	}

	// Sequential path
	results := make([]VectorResult, 0, topK)
	for _, ci := range probeIndices {
		if ci >= len(c.clusters) {
			continue
		}
		for docID, vec := range c.clusters[ci] {
			if filter != nil && !filter(docID) {
				continue
			}
			score := metric(query, vec)
			if float64(score) >= threshold {
				results = append(results, VectorResult{DocID: docID, Score: score})
			}
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
func (idx *IVFIndex) CollectionSize(collection string) int {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	c, ok := idx.data[collection]
	if !ok {
		return 0
	}
	return len(c.allVecs)
}

// Collections implements the VectorSearcher interface.
func (idx *IVFIndex) Collections() []string {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	names := make([]string, 0, len(idx.data))
	for name := range idx.data {
		names = append(names, name)
	}
	return names
}

// --- k-means helpers ---

func kmeansInit(vecs [][]float32, k int) [][]float32 {
	// k-means++ initialization
	if len(vecs) == 0 || k == 0 {
		return nil
	}
	dim := len(vecs[0])
	centroids := make([][]float32, 0, k)

	// Pick first centroid randomly
	centroids = append(centroids, copyVec(vecs[rand.Intn(len(vecs))])) // #nosec G404 -- not security-sensitive

	for len(centroids) < k {
		// Compute distances to nearest centroid
		dists := make([]float64, len(vecs))
		total := 0.0
		for i, v := range vecs {
			minDist := math.MaxFloat64
			for _, c := range centroids {
				d := euclideanDistSq(v, c)
				if d < minDist {
					minDist = d
				}
			}
			dists[i] = minDist
			total += minDist
		}
		if total == 0 {
			// All points are identical to existing centroids
			centroids = append(centroids, make([]float32, dim))
			continue
		}
		// Weighted random selection
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
		centroids = append(centroids, copyVec(vecs[selected]))
	}
	return centroids
}

func recomputeCentroids(vecs [][]float32, assignments []int, k, dim int) [][]float32 {
	sums := make([][]float64, k)
	counts := make([]int, k)
	for i := range sums {
		sums[i] = make([]float64, dim)
	}
	for i, v := range vecs {
		ci := assignments[i]
		counts[ci]++
		for j, val := range v {
			sums[ci][j] += float64(val)
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

func nearestCentroid(vec []float32, centroids [][]float32) int {
	best := 0
	bestDist := math.MaxFloat64
	for i, c := range centroids {
		d := euclideanDistSq(vec, c)
		if d < bestDist {
			bestDist = d
			best = i
		}
	}
	return best
}

func nearestNCentroids(vec []float32, centroids [][]float32, n int) []int {
	type cd struct {
		idx  int
		dist float64
	}
	dists := make([]cd, len(centroids))
	for i, c := range centroids {
		dists[i] = cd{idx: i, dist: euclideanDistSq(vec, c)}
	}
	sort.Slice(dists, func(i, j int) bool {
		return dists[i].dist < dists[j].dist
	})
	if n > len(dists) {
		n = len(dists)
	}
	result := make([]int, n)
	for i := 0; i < n; i++ {
		result[i] = dists[i].idx
	}
	return result
}

func copyVec(v []float32) []float32 {
	c := make([]float32, len(v))
	copy(c, v)
	return c
}
