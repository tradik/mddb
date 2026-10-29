package main

import (
	"sort"
	"strings"
	"sync"

	"mddb/internal/binlog"
	"mddb/internal/storage"
	vec "mddb/internal/vector"
)

// The summary index (RAG-009).
//
// A document may carry a summary its client wrote — the server never writes
// one — in meta `summary`. It is embedded as one vector of its own and kept
// here, apart from every chunk index: no algorithm searches it, no chunk
// cleanup touches it, and a search reaches it only through the collection's
// summaryMode. One vector per document makes an exact scan cheap, so the
// index is flat; a disk-only collection keeps it quantized in RAM and rescores
// the candidates from disk, as its chunks are.

// summaryMetaKey is the metadata key a client writes a document's summary to.
const summaryMetaKey = "summary"

// documentSummary is the summary a document carries, "" when it has none.
func documentSummary(doc *storage.Doc) string {
	return strings.TrimSpace(strings.Join(doc.Meta[summaryMetaKey], " "))
}

// summaryIndex holds one vector per document: a summary's (RAG-009) or, in
// the server's second instance, an image's (RAG-010).
type summaryIndex struct {
	flat  *vec.VectorIndex
	quant *vec.QuantizedVectorIndex

	mu      sync.RWMutex
	present map[string]map[string]bool // collection -> docID
}

func newSummaryIndex(quantType func(string) vec.QuantizationType) *summaryIndex {
	return &summaryIndex{flat: vec.NewVectorIndex(), quant: vec.NewQuantizedVectorIndex(quantType),
		present: map[string]map[string]bool{}}
}

// summaries is the server's summary index, built on first use.
func (s *Server) summaries() *summaryIndex {
	s.summaryOnce.Do(func() { s.summaryIdx = newSummaryIndex(s.collectionQuantType) })
	return s.summaryIdx
}

// collectionQuantType is the quantization a collection is configured for.
func (s *Server) collectionQuantType(collection string) vec.QuantizationType {
	if s.CollectionManager == nil {
		return vec.QuantNone
	}
	if cfg, ok := s.CollectionManager.Get(collection); ok && cfg.Quantization != "" {
		return vec.ParseQuantization(cfg.Quantization)
	}
	return vec.QuantNone
}

// add indexes a document's summary vector, replacing the one it had.
func (x *summaryIndex) add(collection, docID string, v []float32, diskOnly bool) error {
	x.remove(collection, docID)
	var err error
	if diskOnly {
		err = x.quant.Add(collection, docID, v)
	} else {
		err = x.flat.Add(collection, docID, v)
	}
	if err != nil {
		return err
	}
	x.mu.Lock()
	if x.present[collection] == nil {
		x.present[collection] = map[string]bool{}
	}
	x.present[collection][docID] = true
	x.mu.Unlock()
	return nil
}

// remove drops a document's summary vector.
func (x *summaryIndex) remove(collection, docID string) {
	x.flat.Remove(collection, docID)
	x.quant.Remove(collection, docID)
	x.mu.Lock()
	delete(x.present[collection], docID)
	x.mu.Unlock()
}

// has reports whether a document's summary is indexed.
func (x *summaryIndex) has(collection, docID string) bool {
	x.mu.RLock()
	defer x.mu.RUnlock()
	return x.present[collection][docID]
}

// size is how many documents of a collection have a summary vector.
func (x *summaryIndex) size(collection string) int {
	x.mu.RLock()
	defer x.mu.RUnlock()
	return len(x.present[collection])
}

// total is how many documents of every collection have a vector here.
func (x *summaryIndex) total() int {
	x.mu.RLock()
	defer x.mu.RUnlock()
	n := 0
	for _, docs := range x.present {
		n += len(docs)
	}
	return n
}

// drop forgets a collection.
func (x *summaryIndex) drop(collection string) {
	x.flat.DropCollection(collection)
	x.quant.DropCollection(collection)
	x.mu.Lock()
	delete(x.present, collection)
	x.mu.Unlock()
}

// reset forgets every collection, before the index is loaded again.
func (x *summaryIndex) reset() {
	x.mu.Lock()
	cols := make([]string, 0, len(x.present))
	for c := range x.present {
		cols = append(cols, c)
	}
	x.mu.Unlock()
	for _, c := range cols {
		x.drop(c)
	}
}

// searchSummaries returns the documents whose summaries are nearest the
// query.
func (s *Server) searchSummaries(collection string, query []float32, topK int, threshold float64,
	allowed map[string]bool, filtered bool, metric vec.SimilarityFunc) []vec.VectorResult {
	return s.searchDocIndex(s.summaries(), collection, query, topK, threshold, allowed, filtered, metric, s.VectorStore.SummaryVectors)
}

// searchDocIndex searches a one-vector-per-document index. A disk-only
// collection is searched quantized and rescored from disk, read by full.
func (s *Server) searchDocIndex(x *summaryIndex, collection string, query []float32, topK int, threshold float64,
	allowed map[string]bool, filtered bool, metric vec.SimilarityFunc, full func(string, []string) map[string][]float32) []vec.VectorResult {
	if x.size(collection) == 0 {
		return nil
	}
	if !s.collectionDiskOnly(collection) {
		if filtered {
			return x.flat.SearchWithFilter(collection, query, topK, threshold, allowed, metric)
		}
		return x.flat.Search(collection, query, topK, threshold, metric)
	}
	var hits []vec.VectorResult
	if filtered {
		hits = x.quant.SearchWithFilter(collection, query, topK, threshold, allowed, metric)
	} else {
		hits = x.quant.Search(collection, query, topK, threshold, metric)
	}
	if len(hits) == 0 {
		return hits
	}
	if metric == nil {
		metric = vec.CosineSimilarity
	}
	ids := make([]string, len(hits))
	for i, h := range hits {
		ids[i] = h.DocID
	}
	vectors := full(collection, ids)
	for i := range hits {
		if v, ok := vectors[hits[i].DocID]; ok {
			hits[i].Score = metric(query, v)
		}
	}
	sort.SliceStable(hits, func(i, j int) bool { return hits[i].Score > hits[j].Score })
	return hits
}

// loadSummaryVectors puts a collection's stored summary vectors in the index.
func (s *Server) loadSummaryVectors(collection string, diskOnly bool) {
	for docID, v := range s.VectorStore.LoadSummaries(collection) {
		_ = s.summaries().add(collection, docID, v, diskOnly)
	}
}

// dropDocumentSummary removes a deleted document's summary vector.
func (s *Server) dropDocumentSummary(collection, docID string) {
	if s.VectorStore != nil {
		_ = s.VectorStore.DeleteSummary(collection, docID)
	}
	s.summaries().remove(collection, docID)
}

// applyReplicatedSummary mirrors a replicated summary vector write into the
// index: the key is vsum|collection|docID.
func (s *Server) applyReplicatedSummary(entry *binlog.BinlogEntry) {
	parts := strings.SplitN(string(entry.Key), "|", 3)
	if len(parts) < 3 {
		return
	}
	collection, docID := parts[1], parts[2]
	if entry.Type == binlog.BinlogDelete {
		s.summaries().remove(collection, docID)
		return
	}
	if rec, err := vec.UnmarshalEmbeddingRecord(entry.Value); err == nil {
		_ = s.summaries().add(collection, docID, rec.Vector, s.collectionDiskOnly(collection))
	}
}

// The summary half of serverIndexes: the embedding worker indexes summaries
// through the same value it indexes chunks through.

// IndexSummary indexes a document's summary vector.
func (x serverIndexes) IndexSummary(collection, docID string, v []float32) error {
	return x.s.summaries().add(collection, docID, v, x.s.collectionDiskOnly(collection))
}

// RemoveSummary drops a document's summary vector from the index.
func (x serverIndexes) RemoveSummary(collection, docID string) {
	x.s.summaries().remove(collection, docID)
}

// HasSummary reports whether a document's summary is indexed.
func (x serverIndexes) HasSummary(collection, docID string) bool {
	return x.s.summaries().has(collection, docID)
}
