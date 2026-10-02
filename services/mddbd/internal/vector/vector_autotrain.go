package vector

import (
	"log/slog"
	"maps"
	"sync"
)

// When a trainable index builds its structure.
//
// IVF, PQ, OPQ, SQ and SQ4 search a structure — clusters, codebooks, scales —
// trained from a collection's vectors. Until 2.15.3 that training happened at
// startup and on a reindex, nowhere else, and an index that had never been
// trained answered every query with nothing. So a collection created while the
// server was running was invisible to all five until the next restart: 2.15.2
// made sure each of them received the vectors, and measured on the released
// image they still found none. A collection that grew after startup was found,
// but through a structure trained on whatever it held at startup.
//
// The index now decides for itself. A collection that has vectors and was
// never trained is trained before the first search, which waits for it — the
// alternative is an empty answer. One that has doubled since it was last
// trained is retrained in the background while searches go on against the
// current structure, so the cost of training stays proportional to growth:
// a collection that grows to N vectors is trained about log2(N) times.

// trainState is what each trainable index keeps per collection to decide when
// to train it. Embedded in the collection, it supplies the `trained` field the
// search paths already check.
type trainState struct {
	trained    bool
	trainedOn  int  // vectors the collection held when training finished
	retraining bool // a background retrain is running; do not start another
}

// markTrained records a finished training over n vectors.
func (t *trainState) markTrained(n int) {
	t.trained = true
	t.trainedOn = n
	t.retraining = false
}

// due reports whether a collection holding n vectors should be trained, and
// whether the search asking must wait for it.
func (t *trainState) due(n int) (train, wait bool) {
	switch {
	case n == 0 || t.retraining:
		return false, false
	case !t.trained:
		return true, true
	case n >= 2*t.trainedOn:
		return true, false
	}
	return false, false
}

// autoTrain trains a collection when due. state is called under mu and returns
// the collection's train state and its vectors, or nil when the collection
// does not exist; train is called outside mu with a copy of the vectors.
//
// The common case — nothing to do — costs one read lock, so searches do not
// serialise on it.
func autoTrain(mu *sync.RWMutex, state func() (*trainState, map[string][]float32), train func(map[string][]float32)) {
	mu.RLock()
	t, vecs := state()
	run := false
	if t != nil {
		run, _ = t.due(len(vecs))
	}
	mu.RUnlock()
	if !run {
		return
	}

	mu.Lock()
	t, vecs = state() // re-checked: another search may have got here first
	if t == nil {
		mu.Unlock()
		return
	}
	run, wait := t.due(len(vecs))
	if !run {
		mu.Unlock()
		return
	}
	snapshot := maps.Clone(vecs)
	if !wait {
		t.retraining = true
	}
	mu.Unlock()

	runTrain := func() {
		// Whatever happens in train, the collection must not be left marked
		// as retraining, or it is never trained again.
		defer func() {
			mu.Lock()
			if t, _ := state(); t != nil {
				t.retraining = false
			}
			mu.Unlock()
		}()
		TrainSafely(trainFunc(train), "", snapshot)
	}
	if wait {
		runTrain()
		return
	}
	go runTrain()
}

// trainFunc adapts a closure to Trainable.
type trainFunc func(map[string][]float32)

func (f trainFunc) Train(_ string, v map[string][]float32) { f(v) }

// TrainSafely trains an index and turns a panic into a log line.
//
// Training runs in a goroutine after a reindex and after a collection
// doubles, and a panic in a goroutine has no handler above it: it ends the
// process. A collection holding embeddings from two models used to do
// exactly that — PQ sliced every vector by the first one's dimension (#269
// class). Every training call goes through here so the next such bug costs
// one collection its trained structure, not the server.
func TrainSafely(t Trainable, collection string, vectors map[string][]float32) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("index training panicked; the collection keeps its previous structure",
				"collection", collection, "panic", r)
		}
	}()
	t.Train(collection, vectors)
}

// sameDimension returns the vectors of the dimension most of them have, and
// that dimension (the larger one on a tie, so the answer does not depend on
// map order).
//
// A collection can hold embeddings from two models — the old one's until a
// reindex replaces them, the new one's from the first document re-embedded.
// Trained indexes build one structure of one dimension, and used to take it
// from whichever vector map iteration produced first: PQ then sliced every
// other vector by it and panicked, and the rest trained on garbage. Vectors
// of the other dimension stay in the index, uncoded; a query of their
// dimension finds nothing in the trained structure, as it could not be
// compared with it anyway.
func sameDimension(vectors map[string][]float32) (map[string][]float32, int) {
	counts := map[int]int{}
	for _, v := range vectors {
		counts[len(v)]++
	}
	dim, best := 0, 0
	for d, n := range counts {
		if d > 0 && (n > best || (n == best && d > dim)) {
			dim, best = d, n
		}
	}
	if len(counts) == 1 {
		return vectors, dim
	}
	same := make(map[string][]float32, best)
	for id, v := range vectors {
		if len(v) == dim {
			same[id] = v
		}
	}
	return same, dim
}

// The five indexes' hooks: which vectors each keeps, and its Train.

func (idx *IVFIndex) autoTrain(collection string) {
	autoTrain(&idx.mu, func() (*trainState, map[string][]float32) {
		if c, ok := idx.data[collection]; ok {
			return &c.trainState, c.allVecs
		}
		return nil, nil
	}, func(v map[string][]float32) { idx.Train(collection, v) })
}

func (p *PQIndex) autoTrain(collection string) {
	autoTrain(&p.mu, func() (*trainState, map[string][]float32) {
		if c, ok := p.data[collection]; ok {
			return &c.trainState, c.origVecs
		}
		return nil, nil
	}, func(v map[string][]float32) { p.Train(collection, v) })
}

func (o *OPQIndex) autoTrain(collection string) {
	autoTrain(&o.mu, func() (*trainState, map[string][]float32) {
		if c, ok := o.data[collection]; ok {
			return &c.trainState, c.origVecs
		}
		return nil, nil
	}, func(v map[string][]float32) { o.Train(collection, v) })
}

func (s *SQIndex) autoTrain(collection string) {
	autoTrain(&s.mu, func() (*trainState, map[string][]float32) {
		if c, ok := s.data[collection]; ok {
			return &c.trainState, c.origVecs
		}
		return nil, nil
	}, func(v map[string][]float32) { s.Train(collection, v) })
}

func (s *SQ4Index) autoTrain(collection string) {
	autoTrain(&s.mu, func() (*trainState, map[string][]float32) {
		if c, ok := s.data[collection]; ok {
			return &c.trainState, c.origVecs
		}
		return nil, nil
	}, func(v map[string][]float32) { s.Train(collection, v) })
}
