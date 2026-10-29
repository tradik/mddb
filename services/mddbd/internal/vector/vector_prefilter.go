package vector

import "sync"

// The exact prefilter visits only the documents a filter admits (SRCH-013).
//
// The planner answers a selective filter by scanning exactly what it admits.
// Until 2.18.9 the flat index did that by walking every vector in the
// collection and dropping the ones the filter refused, so the "prefilter" cost
// what the collection cost, not what the filter admitted: 6.1 ms at 200k
// vectors and 79 ms (p99 834 ms) at 2M for a filter admitting 2%. Now a search
// whose allowed set is smaller than the collection looks up the admitted
// documents' vectors and scores those alone.
//
// A filter admits documents, and a chunked document is stored as "docID#0",
// "docID#1", ... so the index keeps, per document, the chunk keys it holds.
// Unchunked documents — the common case — need no entry: the document ID is
// the key. Chunk numbers are not assumed to be contiguous; the list holds
// exactly the keys stored.

// prefilterVisited, when set, is told how many vectors each filtered flat
// search looked at. Tests use it to hold the prefilter to the admitted set.
var prefilterVisited func(visited int)

// noteKeyLocked records a key just added to a collection. Caller holds the
// write lock and has checked the key was not there before.
func (vi *VectorIndex) noteKeyLocked(collection, key string) {
	doc := BaseDocID(key)
	if doc == key {
		return
	}
	docs := vi.chunkKeys[collection]
	if docs == nil {
		docs = make(map[string][]string)
		vi.chunkKeys[collection] = docs
	}
	docs[doc] = append(docs[doc], key)
}

// forgetKeyLocked drops a key just removed from a collection. Caller holds the
// write lock.
func (vi *VectorIndex) forgetKeyLocked(collection, key string) {
	doc := BaseDocID(key)
	if doc == key {
		return
	}
	docs := vi.chunkKeys[collection]
	keys := docs[doc]
	for i, k := range keys {
		if k == key {
			keys[i] = keys[len(keys)-1]
			keys = keys[:len(keys)-1]
			break
		}
	}
	if len(keys) == 0 {
		delete(docs, doc)
		return
	}
	docs[doc] = keys
}

// admittedPerWorker is how many admitted documents one goroutine looks up.
// A lookup is a few cache misses in a large map, so the scan is bound by
// memory latency rather than arithmetic and gains from parallelism long
// before an arithmetic scan would (parallelSearchConfig.MinSize). Measured at
// 200k vectors: one goroutine took 2.2 ms for 20k admitted and 7.1 ms for
// 50k, more than the 4.7–5.1 ms of a parallel scan of the whole collection;
// split 512 to a goroutine they took 0.6 and 1.3 ms.
const admittedPerWorker = 512

// searchAdmittedLocked scores every vector of the collection whose document
// the filter admits — the document's own key and its chunk keys, each looked
// up once — and returns the topK best. Caller holds at least the read lock,
// for the lookups, until this returns.
func (vi *VectorIndex) searchAdmittedLocked(collection string, coll map[string][]float32, allowed map[string]bool,
	query []float32, topK int, threshold float64, metric SimilarityFunc) []VectorResult {
	docs := make([]string, 0, len(allowed))
	for doc, ok := range allowed {
		if ok {
			docs = append(docs, doc)
		}
	}
	workers := max(1, min(parallelSearchConfig.Workers(), (len(docs)+admittedPerWorker-1)/admittedPerWorker))
	per := (len(docs) + workers - 1) / workers
	partials := make([][]VectorResult, workers)
	visits := make([]int, workers)
	var wg sync.WaitGroup
	for w := range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// Each worker writes only its own slots; wg.Wait orders the reads.
			partials[w], visits[w] = vi.scoreAdmitted(collection, coll, docs[min(w*per, len(docs)):min((w+1)*per, len(docs))],
				query, topK, threshold, metric)
		}()
	}
	wg.Wait()
	if prefilterVisited != nil {
		total := 0
		for _, n := range visits {
			total += n
		}
		prefilterVisited(total)
	}
	return mergeTop(partials, topK)
}

// scoreAdmitted is one worker's share of searchAdmittedLocked: its topK best
// and how many vectors it looked up.
func (vi *VectorIndex) scoreAdmitted(collection string, coll map[string][]float32, docs []string,
	query []float32, topK int, threshold float64, metric SimilarityFunc) ([]VectorResult, int) {
	chunks := vi.chunkKeys[collection]
	best := newTopK(topK)
	visited := 0
	score := func(key string, vec []float32) {
		visited++
		if len(vec) != len(query) {
			return // absent, or another embedding model's vector; see sameDimension
		}
		if s := metric(query, vec); float64(s) >= threshold {
			best.add(VectorResult{DocID: key, Score: s})
		}
	}
	for _, doc := range docs {
		// A key is admitted when its BaseDocID is, on this path and on the
		// full scan alike, so an allowed ID that reads as a chunk key admits
		// only through its parent.
		if BaseDocID(doc) == doc {
			score(doc, coll[doc])
		}
		for _, key := range chunks[doc] {
			score(key, coll[key])
		}
	}
	return best.results(), visited
}
