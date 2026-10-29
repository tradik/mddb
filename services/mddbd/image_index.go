package main

import (
	"strings"

	"mddb/internal/binlog"
	vec "mddb/internal/vector"
)

// The image index (RAG-010).
//
// Image vectors are in the multimodal provider's space, which is not the
// space of the collection's text chunks, so they are kept apart from every
// chunk index, in an index of their own with one vector per image document:
// the same flat-or-quantized structure the summary index (RAG-009) uses, a
// second instance of it. No chunk algorithm searches it, no chunk cleanup or
// count sees it; a search reaches it only by asking for images.

// imageIndex is the server's image index, built on first use.
func (s *Server) imageIndex() *summaryIndex {
	s.imageOnce.Do(func() { s.imageIdx = newSummaryIndex(s.collectionQuantType) })
	return s.imageIdx
}

// searchImages returns the image documents whose vectors are nearest query,
// by cosine similarity.
func (s *Server) searchImages(collection string, query []float32, topK int, threshold float64,
	allowed map[string]bool, filtered bool) []vec.VectorResult {
	return s.searchDocIndex(s.imageIndex(), collection, query, topK, threshold, allowed, filtered, vec.CosineSimilarity, s.VectorStore.ImageVectors)
}

// loadImageVectors puts a collection's stored image vectors in the index.
func (s *Server) loadImageVectors(collection string, diskOnly bool) {
	for docID, v := range s.VectorStore.LoadImages(collection) {
		_ = s.imageIndex().add(collection, docID, v, diskOnly)
	}
}

// dropDocumentImage removes a deleted document's image bytes and vector.
func (s *Server) dropDocumentImage(collection, docID string) {
	s.deleteImageBytes(collection, docID)
	if s.VectorStore != nil {
		_ = s.VectorStore.DeleteImage(collection, docID)
	}
	s.imageIndex().remove(collection, docID)
}

// applyReplicatedImage mirrors a replicated image vector write into the
// index: the key is vimg|collection|docID.
func (s *Server) applyReplicatedImage(entry *binlog.BinlogEntry) {
	parts := strings.SplitN(string(entry.Key), "|", 3)
	if len(parts) < 3 {
		return
	}
	collection, docID := parts[1], parts[2]
	if entry.Type == binlog.BinlogDelete {
		s.imageIndex().remove(collection, docID)
		return
	}
	if rec, err := vec.UnmarshalEmbeddingRecord(entry.Value); err == nil {
		_ = s.imageIndex().add(collection, docID, rec.Vector, s.collectionDiskOnly(collection))
	}
}

// The image half of serverIndexes: the embedding worker indexes images
// through the same value it indexes chunks and summaries through.

// IndexImage indexes an image document's vector.
func (x serverIndexes) IndexImage(collection, docID string, v []float32) error {
	return x.s.imageIndex().add(collection, docID, v, x.s.collectionDiskOnly(collection))
}

// HasImage reports whether an image document's vector is indexed.
func (x serverIndexes) HasImage(collection, docID string) bool {
	return x.s.imageIndex().has(collection, docID)
}

// ImageBytes reads an image document's bytes for embedding.
func (x serverIndexes) ImageBytes(collection, docID string) []byte {
	return x.s.imageBytes(collection, docID)
}
