package vector

import (
	"os"
	"runtime"
	"sort"
	"strconv"
	"sync"
	"sync/atomic"
)

// parallelConfig holds runtime-resolved parallel search settings.
// Fields are read atomically so tests can mutate them without races against
// concurrent searches — and so running tests with t.Parallel() stays safe.
type parallelConfig struct {
	workers atomic.Int32
	minSize atomic.Int32
}

// Workers returns the configured worker count.
func (c *parallelConfig) Workers() int { return int(c.workers.Load()) }

// MinSize returns the minimum collection size that triggers parallel scoring.
func (c *parallelConfig) MinSize() int { return int(c.minSize.Load()) }

// parallelSearchConfig is the global parallel search config.
var parallelSearchConfig parallelConfig

// maxParallelWorkers caps the worker count to a sensible upper bound,
// keeping int → int32 conversions safe and protecting against misconfiguration.
const maxParallelWorkers = 256

func init() {
	workers := min(runtime.NumCPU(), 16)
	if v := os.Getenv("MDDB_VECTOR_PARALLEL_WORKERS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= maxParallelWorkers {
			workers = n
		}
	}
	parallelSearchConfig.workers.Store(int32(workers)) // #nosec G115 -- bounded by maxParallelWorkers

	minSize := 2048
	if v := os.Getenv("MDDB_VECTOR_PARALLEL_MIN_SIZE"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= (1<<30) {
			minSize = n
		}
	}
	parallelSearchConfig.minSize.Store(int32(minSize)) // #nosec G115 -- bounded by 1<<30
}

// vectorEntry is a snapshot of a map entry for parallel processing.
// Copies slice headers (not underlying float data) for zero-copy fan-out.
type vectorEntry struct {
	docID  string
	vector []float32
}

// snapshotMap converts a map into a slice for parallel iteration.
// O(n) pointer copies — does not deep-copy vector data.
func snapshotMap(m map[string][]float32) []vectorEntry {
	entries := make([]vectorEntry, 0, len(m))
	for id, vec := range m {
		entries = append(entries, vectorEntry{docID: id, vector: vec})
	}
	return entries
}

// parallelScore scores entries across multiple goroutines and returns
// merged, sorted, topK-limited results. Each worker operates on a
// disjoint index range — zero contention during scoring.
// filter: nil = no filter (Search), non-nil = per-entry filter (SearchWithFilter).
func parallelScore(
	entries []vectorEntry,
	query []float32,
	topK int,
	threshold float64,
	metric SimilarityFunc,
	filter func(string) bool,
) []VectorResult {
	n := len(entries)
	if n == 0 {
		return nil
	}

	// Determine actual worker count, capped by data size
	cfgWorkers := parallelSearchConfig.Workers()
	cfgMinSize := parallelSearchConfig.MinSize()
	maxWorkers := (n + cfgMinSize - 1) / cfgMinSize
	workers := min(cfgWorkers, maxWorkers)
	if workers < 1 {
		workers = 1
	}

	// Single worker: skip goroutine overhead
	if workers == 1 {
		results := scoreRange(entries, 0, n, query, topK, threshold, metric, filter)
		sort.Slice(results, func(i, j int) bool {
			if results[i].Score != results[j].Score {
				return results[i].Score > results[j].Score
			}
			return results[i].DocID < results[j].DocID
		})
		if len(results) > topK {
			results = results[:topK]
		}
		return results
	}

	chunkSize := (n + workers - 1) / workers

	// partials: each worker writes EXCLUSIVELY to partials[workerID] exactly once.
	// Disjoint index writes are race-free per the Go memory model, and the
	// wg.Wait() below establishes a happens-before edge for the merge loop.
	// Do NOT add a mutex or channel here — it would hurt perf without adding safety.
	partials := make([][]VectorResult, workers)

	var wg sync.WaitGroup
	wg.Add(workers)

	for w := 0; w < workers; w++ {
		go func(workerID int) {
			defer wg.Done()
			start := workerID * chunkSize
			if start >= n {
				// Fewer non-empty chunks than workers — this worker has no
				// work (GO-018). Leave partials[workerID] nil.
				return
			}
			end := min(start+chunkSize, n)
			partials[workerID] = scoreRange(entries, start, end, query, topK, threshold, metric, filter)
		}(w)
	}

	wg.Wait()

	// Merge partial results
	total := 0
	for _, p := range partials {
		total += len(p)
	}
	merged := make([]VectorResult, 0, total)
	for _, p := range partials {
		merged = append(merged, p...)
	}

	// Sort by score descending, docID ascending (tiebreaker for determinism)
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

// scoreRange scores entries[start:end] sequentially, returning filtered results.
func scoreRange(
	entries []vectorEntry,
	start, end int,
	query []float32,
	topK int,
	threshold float64,
	metric SimilarityFunc,
	filter func(string) bool,
) []VectorResult {
	// GO-018: an empty or inverted range (start >= end) must not reach
	// make(): min(end-start, topK*2) would be negative and panic with
	// "makeslice: cap out of range". This happens when the worker split
	// hands a later worker a start index past n.
	if start >= end {
		return nil
	}
	capHint := end - start
	if topK > 0 && topK*2 < capHint {
		capHint = topK * 2
	}
	results := make([]VectorResult, 0, capHint)
	for i := start; i < end; i++ {
		e := &entries[i]
		if filter != nil && !filter(e.docID) {
			continue
		}
		if len(e.vector) != len(query) {
			continue // another embedding model's vector; see sameDimension
		}
		score := metric(query, e.vector)
		if float64(score) >= threshold {
			results = append(results, VectorResult{DocID: e.docID, Score: score})
		}
	}
	return results
}
