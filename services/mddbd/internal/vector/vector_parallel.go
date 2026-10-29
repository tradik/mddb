package vector

import (
	"os"
	"runtime"
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
	workers, minSize := parallelSettings(os.Getenv)
	parallelSearchConfig.workers.Store(int32(workers)) // #nosec G115 -- bounded by maxParallelWorkers
	parallelSearchConfig.minSize.Store(int32(minSize)) // #nosec G115 -- bounded by 1<<30
}

// parallelSettings reads MDDB_VECTOR_PARALLEL_WORKERS and
// MDDB_VECTOR_PARALLEL_MIN_SIZE through getenv, keeping the defaults for a
// value that is missing, not a number, or out of range.
func parallelSettings(getenv func(string) string) (workers, minSize int) {
	return envInt(getenv("MDDB_VECTOR_PARALLEL_WORKERS"), min(runtime.NumCPU(), 16), maxParallelWorkers),
		envInt(getenv("MDDB_VECTOR_PARALLEL_MIN_SIZE"), 2048, 1<<30)
}

// envInt is v as a number in 1..limit, or def. It is parsed as a 32-bit
// number: the values are stored in atomic.Int32s, and parsing to that size
// makes the bound visible where the conversion happens, not only via limit.
func envInt(v string, def, limit int) int {
	if n, err := strconv.ParseInt(v, 10, 32); err == nil && n > 0 && n <= int64(limit) {
		return int(n)
	}
	return def
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
	workers := max(1, min(cfgWorkers, maxWorkers))

	// Single worker: skip goroutine overhead
	if workers == 1 {
		return mergeTop([][]VectorResult{scoreRange(entries, 0, n, query, topK, threshold, metric, filter)}, topK)
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
	return mergeTop(partials, topK)
}

// scoreRange scores entries[start:end] sequentially, returning the topK best
// that pass the filter and the threshold, unsorted; topK <= 0 returns them all.
func scoreRange(
	entries []vectorEntry,
	start, end int,
	query []float32,
	topK int,
	threshold float64,
	metric SimilarityFunc,
	filter func(string) bool,
) []VectorResult {
	// GO-018: an empty or inverted range (start >= end) happens when the
	// worker split hands a later worker a start index past n; it has nothing
	// to score.
	if start >= end {
		return nil
	}
	best := newTopK(topK)
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
			best.add(VectorResult{DocID: e.docID, Score: score})
		}
	}
	return best.results()
}
