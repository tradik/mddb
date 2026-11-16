package main

import (
	"errors"
	"net/http"
	"time"

	json "mddb/internal/jsonx"
	vec "mddb/internal/vector"
)

// Compacting a collection's HNSW graph on request (SRCH-023).
//
// A removed or overwritten document stays in the graph as a tombstone that
// searches skip; the graph is rebuilt on its own once half of it is
// tombstones. Until then a search asks the graph for proportionally more
// neighbours, and the memory is held. POST /v1/vector-compact rebuilds the
// graph now. The rebuild runs beside the graph in use — searches and writes
// go on — and the checkpoint that follows no longer carries the tombstones.

// vectorCompactRequest is the body of POST /v1/vector-compact.
type vectorCompactRequest struct {
	Collection string `json:"collection"`
}

// vectorCompactResponse is its answer.
type vectorCompactResponse struct {
	Collection string `json:"collection"`
	// Vectors is the live vectors the rebuilt graph holds.
	Vectors int `json:"vectors"`
	// TombstonesBefore and TombstonesAfter are the removed and overwritten
	// nodes the graph carried before and carries now; after is above zero
	// only when documents were written while the graph was rebuilt.
	TombstonesBefore int   `json:"tombstonesBefore"`
	TombstonesAfter  int   `json:"tombstonesAfter"`
	ElapsedMs        int64 `json:"elapsedMs"`
}

// hnswIndex is the server's HNSW index, or nil when it has none.
func (s *Server) hnswIndex() *vec.HNSWIndex {
	h, _ := s.VectorSearchers["hnsw"].(*vec.HNSWIndex)
	return h
}

// handleVectorCompact handles POST /v1/vector-compact.
func (s *Server) handleVectorCompact(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req vectorCompactRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		bad(w, err)
		return
	}
	if req.Collection == "" {
		bad(w, errors.New("missing collection"))
		return
	}
	h := s.hnswIndex()
	if h == nil || h.CollectionSize(req.Collection) == 0 {
		http.Error(w, "the collection has no HNSW graph", http.StatusNotFound)
		return
	}
	// A rebuild of a large graph outlasts the write deadline, as a reindex does.
	liftWriteDeadline(w, r)
	start := time.Now()
	before := h.DeletedSince(req.Collection)
	h.Compact(req.Collection)
	ok(w, vectorCompactResponse{
		Collection: req.Collection, Vectors: h.CollectionSize(req.Collection),
		TombstonesBefore: before, TombstonesAfter: h.DeletedSince(req.Collection),
		ElapsedMs: time.Since(start).Milliseconds(),
	})
}
