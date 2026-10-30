package main

import (
	"context"
	"sort"
	"sync"
	"sync/atomic"

	vec "mddb/internal/vector"
)

// Training the trainable indexes in the background (GO-047).
//
// IVF, PQ, OPQ, SQ and SQ4 search a structure trained on a collection's
// vectors; flat and HNSW search the vectors themselves. The startup load used
// to train every collection before marking it searchable, so flat waited for
// k-means it never uses: 937 vectors × 768 dimensions answered "loading 0%"
// for over a minute, and each collection waited for the training of the ones
// before it. A collection is now searchable once its vectors are in; one
// goroutine trains collection after collection behind the load, and only the
// trainable indexes refuse a collection until it is trained — auto never
// picks one that is, and an explicit request gets a 503 naming it.

// vectorTrainJob is one collection's vectors for the trainable indexes.
type vectorTrainJob struct {
	collection string
	vectors    map[string][]float32
}

// vectorTrainer trains the collections of one load, one at a time.
type vectorTrainer struct {
	mu      sync.Mutex
	pending []string // queued or training, in queue order
	current string   // training now
	jobs    chan vectorTrainJob
	train   func(collection string, vectors map[string][]float32)
	cancel  context.CancelFunc
	done    chan struct{} // closed when run returns
}

// vectorTrainingStatus is the training block of /v1/vector-stats.
type vectorTrainingStatus struct {
	// Current is the collection being trained now.
	Current string `json:"current,omitempty"`
	// Pending is every collection not trained yet, current included, in the
	// order they will be trained.
	Pending []string `json:"pending"`
	// Indexes are the indexes that refuse those collections until then.
	Indexes []string `json:"indexes"`
}

// vectorTrainerRef is the server's current trainer; nil before the first load.
type vectorTrainerRef = atomic.Pointer[vectorTrainer]

// startVectorTraining starts the trainer for a load of n collections. A
// trainer left from an earlier load (a restore reloads the index) is
// cancelled, and the new one trains nothing until the old one has stopped,
// so two never train the same index at once.
func (s *Server) startVectorTraining(n int) *vectorTrainer {
	ctx, cancel := context.WithCancel(context.Background())
	t := &vectorTrainer{jobs: make(chan vectorTrainJob, n), train: s.trainCollection, cancel: cancel, done: make(chan struct{})}
	prev := s.vectorTrain.Swap(t)
	go t.run(ctx, prev)
	return t
}

// trainCollection trains every trainable index on one collection's vectors.
func (s *Server) trainCollection(collection string, vectors map[string][]float32) {
	for _, searcher := range s.VectorSearchers {
		if trainer, ok := searcher.(vec.Trainable); ok {
			vec.TrainSafely(trainer, collection, vectors)
		}
	}
}

// enqueue queues a collection; one without vectors to train on (disk-only,
// or every vector refused) has nothing to wait for and is not queued.
func (t *vectorTrainer) enqueue(collection string, vectors map[string][]float32) {
	if len(vectors) == 0 {
		return
	}
	t.mu.Lock()
	t.pending = append(t.pending, collection)
	t.mu.Unlock()
	t.jobs <- vectorTrainJob{collection: collection, vectors: vectors}
}

// close says the load queued its last collection.
func (t *vectorTrainer) close() { close(t.jobs) }

// run trains the queued collections in order until the queue is closed and
// empty or ctx is cancelled.
func (t *vectorTrainer) run(ctx context.Context, prev *vectorTrainer) {
	defer close(t.done)
	if prev != nil {
		prev.stop()
	}
	for {
		select {
		case <-ctx.Done():
			return
		case job, ok := <-t.jobs:
			if !ok {
				return
			}
			if ctx.Err() != nil {
				return
			}
			if !t.begin(job.collection) {
				continue // dropped while it waited
			}
			t.train(job.collection, job.vectors)
			t.finish(job.collection)
		}
	}
}

// begin marks a collection as training, unless it was dropped meanwhile.
func (t *vectorTrainer) begin(collection string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.pendingLocked(collection) {
		return false
	}
	t.current = collection
	return true
}

// finish marks a collection trained.
func (t *vectorTrainer) finish(collection string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.removeLocked(collection)
	t.current = ""
}

// drop forgets a deleted collection, so its queued vectors are not trained
// back into the indexes.
func (t *vectorTrainer) drop(collection string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.current != collection {
		t.removeLocked(collection)
	}
}

func (t *vectorTrainer) removeLocked(collection string) {
	for i, c := range t.pending {
		if c == collection {
			t.pending = append(t.pending[:i], t.pending[i+1:]...)
			return
		}
	}
}

func (t *vectorTrainer) pendingLocked(collection string) bool {
	for _, c := range t.pending {
		if c == collection {
			return true
		}
	}
	return false
}

// training reports whether a collection is queued or training.
func (t *vectorTrainer) training(collection string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.pendingLocked(collection)
}

// collections is what is still to train; for "" all of it, otherwise only
// that collection if it is.
func (t *vectorTrainer) collections(collection string) []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	if collection == "" {
		return append([]string(nil), t.pending...)
	}
	if t.pendingLocked(collection) {
		return []string{collection}
	}
	return nil
}

// stop cancels the trainer and waits for it. A training already running
// finishes first: Train cannot be interrupted, and the trainer must not be
// left running.
func (t *vectorTrainer) stop() {
	t.cancel()
	<-t.done
}

// vectorTraining reports whether searcher refuses collection because it has
// not trained it yet. Only an index that must be trained before it answers
// (vec.BuildReporter) waits; BQ trains too, but answers from what it holds.
func (s *Server) vectorTraining(collection string, searcher vec.VectorSearcher) bool {
	if _, ok := searcher.(vec.BuildReporter); !ok {
		return false
	}
	t := s.vectorTrain.Load()
	return t != nil && t.training(collection)
}

// trainingCollections is what the trainer has not trained yet: every
// collection for "", otherwise that one if it has not.
func (s *Server) trainingCollections(collection string) []string {
	if t := s.vectorTrain.Load(); t != nil {
		return t.collections(collection)
	}
	return nil
}

// vectorTrainingStatus is the training block of /v1/vector-stats; nil when
// nothing is waiting to train.
func (s *Server) vectorTrainingStatus() *vectorTrainingStatus {
	t := s.vectorTrain.Load()
	if t == nil {
		return nil
	}
	t.mu.Lock()
	st := &vectorTrainingStatus{Current: t.current, Pending: append([]string(nil), t.pending...)}
	t.mu.Unlock()
	if len(st.Pending) == 0 {
		return nil
	}
	for name, searcher := range s.VectorSearchers {
		if _, ok := searcher.(vec.BuildReporter); ok {
			st.Indexes = append(st.Indexes, name)
		}
	}
	sort.Strings(st.Indexes)
	return st
}

// trainingIndexes is the indexes that cannot answer for collection yet.
func (s *Server) trainingIndexes(collection string) []string {
	var out []string
	for name, searcher := range s.VectorSearchers {
		if s.vectorTraining(collection, searcher) {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}
