package main

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	json "mddb/internal/jsonx"
	vec "mddb/internal/vector"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Loading the vector index at startup (#346).
//
// Every stored vector is put into every algorithm's index — flat, HNSW, IVF,
// PQ, OPQ, SQ, SQ4, BQ — and the trainable ones run k-means per collection.
// On tens of thousands of vectors that takes minutes, and vector search
// answered "loading, please retry" for every collection until the last one
// was done, with no way to tell progress from a hang. Collections now load
// smallest first, each becomes searchable as soon as its own vectors are in,
// a collection with no vectors is searchable at once, and the progress is on
// /v1/vector-stats and /health and in the log every minute.

// vectorLoadState is the progress of one index load.
type vectorLoadState struct {
	mu                                sync.Mutex
	started                           time.Time
	lastLog                           time.Time
	counts                            map[string]int // vectors per collection, as counted at the start
	ready                             map[string]bool
	current                           string
	collectionsDone, collectionsTotal int
	vectorsDone, vectorsTotal         int64
	done                              bool
}

// vectorLoadProgress is the progress block of /v1/vector-stats and /health.
type vectorLoadProgress struct {
	CollectionsDone  int      `json:"collectionsDone"`
	CollectionsTotal int      `json:"collectionsTotal"`
	VectorsDone      int64    `json:"vectorsDone"`
	VectorsTotal     int64    `json:"vectorsTotal"`
	Percent          float64  `json:"percent"`
	EtaSeconds       int      `json:"etaSeconds"`
	Current          string   `json:"current,omitempty"`
	ReadyCollections []string `json:"readyCollections"`
}

func newVectorLoadState(counts map[string]int) *vectorLoadState {
	v := &vectorLoadState{started: time.Now(), counts: counts, ready: map[string]bool{}, collectionsTotal: len(counts)}
	v.lastLog = v.started
	for _, n := range counts {
		v.vectorsTotal += int64(n)
	}
	return v
}

// order is the collections to load, smallest first, so small collections
// become searchable while a large one is still loading.
func (v *vectorLoadState) order() []string {
	out := make([]string, 0, len(v.counts))
	for c := range v.counts {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool {
		if v.counts[out[i]] != v.counts[out[j]] {
			return v.counts[out[i]] < v.counts[out[j]]
		}
		return out[i] < out[j]
	})
	return out
}

func (v *vectorLoadState) begin(collection string) {
	v.mu.Lock()
	v.current = collection
	v.mu.Unlock()
}

// add counts vectors put into the indexes, and logs the progress each minute.
func (v *vectorLoadState) add(n int) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.vectorsDone += int64(n)
	if now := time.Now(); now.Sub(v.lastLog) >= time.Minute {
		v.lastLog = now
		p := v.progressLocked(now)
		slog.Info("vector index loading", "percent", p.Percent, "etaSeconds", p.EtaSeconds,
			"collection", v.current, "collectionsDone", p.CollectionsDone, "collectionsTotal", p.CollectionsTotal)
	}
}

// finishCollection makes a collection searchable.
func (v *vectorLoadState) finishCollection(collection string) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.ready[collection] = true
	v.collectionsDone++
	v.current = ""
}

func (v *vectorLoadState) finish() {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.done = true
	v.current = ""
}

// collectionReady reports whether a collection can be searched: the load is
// over, its own vectors are in, or it has none to load.
func (v *vectorLoadState) collectionReady(collection string) bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.done || v.ready[collection] {
		return true
	}
	_, hasVectors := v.counts[collection]
	return !hasVectors
}

// progress is nil once the load is over.
func (v *vectorLoadState) progress() *vectorLoadProgress {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.done {
		return nil
	}
	p := v.progressLocked(time.Now())
	return &p
}

func (v *vectorLoadState) progressLocked(now time.Time) vectorLoadProgress {
	p := vectorLoadProgress{CollectionsDone: v.collectionsDone, CollectionsTotal: v.collectionsTotal,
		VectorsDone: v.vectorsDone, VectorsTotal: v.vectorsTotal, Current: v.current, EtaSeconds: -1,
		ReadyCollections: make([]string, 0, len(v.ready))}
	for c := range v.ready {
		p.ReadyCollections = append(p.ReadyCollections, c)
	}
	sort.Strings(p.ReadyCollections)
	if v.vectorsTotal > 0 {
		p.Percent = float64(int(1000*float64(v.vectorsDone)/float64(v.vectorsTotal))) / 10
	}
	if elapsed := now.Sub(v.started).Seconds(); v.vectorsDone > 0 && elapsed > 0 {
		p.EtaSeconds = int(float64(v.vectorsTotal-v.vectorsDone) / (float64(v.vectorsDone) / elapsed))
	}
	return p
}

// vectorLoad is the server's current index load; nil before the first one.
type vectorLoadRef = atomic.Pointer[vectorLoadState]

// errUnknownAlgorithm and vectorLoadingError are readySearcher's refusals.
var errUnknownAlgorithm = errors.New("unknown algorithm")

type vectorLoadingError struct {
	Collection string
	Progress   *vectorLoadProgress
}

func (e *vectorLoadingError) Error() string {
	if e.Progress == nil {
		return fmt.Sprintf("the vector index for collection %q is loading, please retry", e.Collection)
	}
	return fmt.Sprintf("the vector index for collection %q is loading (%.1f%%, about %ds left), please retry",
		e.Collection, e.Progress.Percent, max(e.Progress.EtaSeconds, 0))
}

// vectorCollectionReady reports whether searcher can answer for collection.
func (s *Server) vectorCollectionReady(collection string, searcher vec.VectorSearcher) bool {
	if searcher.IsReady() {
		return true
	}
	if l := s.vectorLoad.Load(); l != nil {
		return l.collectionReady(collection)
	}
	return false
}

// readySearcher is the searcher for algo, or flat when algo's index is not
// ready for the collection, or why neither can answer. Every vector search
// path asks it; there were five copies of this choice.
func (s *Server) readySearcher(collection, algo string) (vec.VectorSearcher, string, error) {
	searcher, ok := s.VectorSearchers[algo]
	if !ok {
		return nil, algo, fmt.Errorf("%w: %s", errUnknownAlgorithm, algo)
	}
	if s.vectorCollectionReady(collection, searcher) {
		return searcher, algo, nil
	}
	if flat, ok := s.VectorSearchers["flat"]; ok && s.vectorCollectionReady(collection, flat) {
		return flat, "flat", nil
	}
	e := &vectorLoadingError{Collection: collection}
	if l := s.vectorLoad.Load(); l != nil {
		e.Progress = l.progress()
	}
	return nil, algo, e
}

// writeVectorUnavailable answers a readySearcher refusal: 400 for an
// unknown algorithm, 503 with the progress and Retry-After while loading.
func (s *Server) writeVectorUnavailable(w http.ResponseWriter, err error, available string) {
	var loading *vectorLoadingError
	if !errors.As(err, &loading) {
		bad(w, fmt.Errorf("%w, available: %s", err, available))
		return
	}
	retry := 30
	if loading.Progress != nil && loading.Progress.EtaSeconds > 0 {
		retry = min(loading.Progress.EtaSeconds, 300)
	}
	w.Header().Set("Retry-After", fmt.Sprint(retry))
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusServiceUnavailable)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": loading.Error(), "collection": loading.Collection, "progress": loading.Progress,
	})
}

// vectorSearcherStatus is readySearcher's refusal as a gRPC status.
func vectorSearcherStatus(err error) error {
	var loading *vectorLoadingError
	if errors.As(err, &loading) {
		return status.Error(codes.Unavailable, err.Error())
	}
	return status.Error(codes.InvalidArgument, err.Error())
}
