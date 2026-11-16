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
//
// The flat index and HNSW share the scan (SRCH-024): HNSW answers a filter
// admitting too little for its graph the same way, rather than scoring the
// whole collection as it did until 2.19.8.

// prefilterVisited, when set, is told how many vectors each exact filtered
// scan looked at. Tests use it to hold the prefilter to the admitted set.
var prefilterVisited func(visited int)

// chunkIndex is, per collection, the chunk keys each document holds: what
// lets an exact filtered scan reach a chunked document's vectors without a
// pass over the collection.
type chunkIndex map[string]map[string][]string

// note records a key just added to a collection. Caller holds the write lock
// and has checked the key was not there before.
func (c chunkIndex) note(collection, key string) {
	doc := BaseDocID(key)
	if doc == key {
		return
	}
	docs := c[collection]
	if docs == nil {
		docs = make(map[string][]string)
		c[collection] = docs
	}
	docs[doc] = append(docs[doc], key)
}

// forget drops a key just removed from a collection. Caller holds the write
// lock.
func (c chunkIndex) forget(collection, key string) {
	doc := BaseDocID(key)
	if doc == key {
		return
	}
	docs := c[collection]
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

// rebuild replaces a collection's entry with the chunk keys of vectors.
// Caller holds the write lock.
func (c chunkIndex) rebuild(collection string, vectors map[string][]float32) {
	delete(c, collection)
	for key := range vectors {
		c.note(collection, key)
	}
}

// admittedPerWorker is how many admitted documents one goroutine looks up.
// A lookup is a few cache misses in a large map, so the scan is bound by
// memory latency rather than arithmetic and gains from parallelism long
// before an arithmetic scan would (parallelSearchConfig.MinSize). Measured at
// 200k vectors: one goroutine took 2.2 ms for 20k admitted and 7.1 ms for
// 50k, more than the 4.7–5.1 ms of a parallel scan of the whole collection;
// split 512 to a goroutine they took 0.6 and 1.3 ms.
const admittedPerWorker = 512

// admittedScan is an exact scan of what a filter admits: every vector of a
// collection whose document the filter allows — the document's own key and
// its chunk keys, each looked up once — scored against the query.
type admittedScan struct {
	coll      map[string][]float32
	chunks    map[string][]string // the collection's entry of a chunkIndex
	allowed   map[string]bool
	query     []float32
	topK      int
	threshold float64
	metric    SimilarityFunc
}

// run returns the topK best of the admitted vectors. Caller holds at least
// the read lock of the index the maps belong to until this returns.
func (a admittedScan) run() []VectorResult {
	docs := make([]string, 0, len(a.allowed))
	for doc, ok := range a.allowed {
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
			partials[w], visits[w] = a.score(docs[min(w*per, len(docs)):min((w+1)*per, len(docs))])
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
	return mergeTop(partials, a.topK)
}

// score is one worker's share of run: its topK best and how many vectors it
// looked up.
func (a admittedScan) score(docs []string) ([]VectorResult, int) {
	best := newTopK(a.topK)
	visited := 0
	score := func(key string, vec []float32) {
		visited++
		if len(vec) != len(a.query) {
			return // absent, or another embedding model's vector; see sameDimension
		}
		if s := a.metric(a.query, vec); float64(s) >= a.threshold {
			best.add(VectorResult{DocID: key, Score: s})
		}
	}
	for _, doc := range docs {
		// A key is admitted when its BaseDocID is, on this path and on the
		// full scan alike, so an allowed ID that reads as a chunk key admits
		// only through its parent.
		if BaseDocID(doc) == doc {
			score(doc, a.coll[doc])
		}
		for _, key := range a.chunks[doc] {
			score(key, a.coll[key])
		}
	}
	return best.results(), visited
}
